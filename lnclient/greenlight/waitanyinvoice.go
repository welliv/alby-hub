package greenlight

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/getAlby/hub/events"
	"github.com/getAlby/hub/lnclient"
	"github.com/getAlby/hub/lnclient/cln/clngrpc"
	"github.com/getAlby/hub/logger"
	"github.com/sirupsen/logrus"
)

const (
	pumpStateFile                 = "pump_state.json"
	waitAnyInvoiceTimeout         = uint64(30) // seconds; GL serves the Wait family on cln.Node
	waitAnyInvoiceTimeoutDuration = time.Duration(waitAnyInvoiceTimeout) * time.Second
	defaultPumpRetryDelay         = 10 * time.Second
)

type pumpState struct {
	LastPayIndex uint64 `json:"last_pay_index"`
}

// waitForInvoices is the push tier: it waits for the next settled invoice
// (pay_index > last seen), publishes nwc_lnclient_payment_received so the
// hub's transactions service records and notifies, and persists the index so
// a restart catches up from where it left off instead of missing payments.
//
// This is what makes advertising payment_received in
// GetSupportedNIP47NotificationTypes safe: the hub's reconcile safety net
// turns off the moment that notification type is advertised, so the pump
// must be able to replay anything that happened while it was down.
func (g *GreenlightService) waitForInvoices(ctx context.Context) {
	retryDelay := g.pumpRetryDelay
	if retryDelay == 0 {
		retryDelay = defaultPumpRetryDelay
	}

	lastPayIndex, err := g.loadPumpState()
	if err != nil {
		logger.Logger.WithError(err).Warn("Failed to load pump state, starting from pay_index 0")
		lastPayIndex = 0
	} else {
		logger.Logger.WithField("last_pay_index", lastPayIndex).Info("Resuming invoice pump from persisted pay index")
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		timeout := waitAnyInvoiceTimeout
		req := &clngrpc.WaitanyinvoiceRequest{
			LastpayIndex: &lastPayIndex,
			Timeout:      &timeout,
		}
		// mark the pump call for the health watchdog (health.go): a call
		// outstanding well past the server timeout means the node stopped
		// processing (wedged/frozen).
		g.markPumpCallStart()
		resp, err := g.client.WaitAnyInvoice(ctx, req)
		g.markPumpCallDone()
		if err != nil {
			// timeout or transient error: retry
			if !errors.Is(err, context.Canceled) {
				logger.Logger.WithError(err).Debug("waitanyinvoice returned, retrying")
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}
			continue
		}
		if resp == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}
			continue
		}

		// any path that does not advance lastPayIndex must back off, or
		// the next WaitAnyinvoice returns the same record immediately
		backoff := func() bool {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(retryDelay):
				return true
			}
		}

		if resp.Status == clngrpc.WaitanyinvoiceResponse_EXPIRED {
			// fast-forward past expired invoices so they never block the pump
			if resp.PayIndex != nil {
				lastPayIndex = *resp.PayIndex
				if err := g.savePumpState(lastPayIndex); err != nil {
					logger.Logger.WithError(err).Error("Failed to persist pump state")
				}
			} else if !backoff() {
				return
			}
			continue
		}

		if resp.Status != clngrpc.WaitanyinvoiceResponse_PAID {
			logger.Logger.WithField("status", resp.Status).Warn("Unexpected waitanyinvoice status")
			if !backoff() {
				return
			}
			continue
		}
		if resp.PayIndex == nil {
			logger.Logger.WithField("payment_hash", hex.EncodeToString(resp.PaymentHash)).Warn("paid invoice without pay_index, skipping")
			if !backoff() {
				return
			}
			continue
		}

		// Publish FIRST, persist the cursor only after a successful
		// publish: a crash or lookup failure must replay the invoice (the
		// hub's payment_received handling is idempotent by payment hash),
		// never silently skip it.
		tx := g.paidInvoiceTransaction(ctx, resp)
		if tx == nil {
			// lookup and fallback both failed: do not advance; retry
			if !backoff() {
				return
			}
			continue
		}

		logger.Logger.WithFields(logrus.Fields{
			"payment_hash": tx.PaymentHash,
			"amount_msat":  tx.AmountMsat,
		}).Info("Invoice paid")

		g.publish(&events.Event{
			Event:      "nwc_lnclient_payment_received",
			Properties: tx,
		})

		lastPayIndex = *resp.PayIndex
		if err := g.savePumpState(lastPayIndex); err != nil {
			logger.Logger.WithError(err).Error("Failed to persist pump state")
		}
	}
}

func (g *GreenlightService) loadPumpState() (uint64, error) {
	data, err := os.ReadFile(filepath.Join(g.workDir, pumpStateFile))
	if err != nil {
		return 0, err
	}
	state := pumpState{}
	if err := json.Unmarshal(data, &state); err != nil {
		return 0, fmt.Errorf("failed to parse pump state: %w", err)
	}
	return state.LastPayIndex, nil
}

func (g *GreenlightService) savePumpState(lastPayIndex uint64) error {
	if err := os.MkdirAll(g.workDir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(pumpState{LastPayIndex: lastPayIndex})
	if err != nil {
		return err
	}
	tmp := filepath.Join(g.workDir, pumpStateFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(g.workDir, pumpStateFile))
}

// paidInvoiceTransaction maps a paid waitanyinvoice result to a transaction,
// retrying the full-fidelity lookup and falling back to the fields carried
// by the waitanyinvoice response itself (keysend invoices have no bolt11 and
// cannot be decoded, but must still be credited).
func (g *GreenlightService) paidInvoiceTransaction(ctx context.Context, resp *clngrpc.WaitanyinvoiceResponse) *lnclient.Transaction {
	hash := hex.EncodeToString(resp.PaymentHash)
	for attempt := 0; attempt < 3; attempt++ {
		tx, err := g.LookupInvoice(ctx, hash)
		if err == nil {
			return tx
		}
		logger.Logger.WithError(err).WithField("payment_hash", hash).Warn("Failed to look up paid invoice, retrying")
		if !sleepOrDone(ctx, time.Duration(attempt+1)*time.Second) {
			return nil
		}
	}
	if len(resp.PaymentHash) == 0 || len(resp.PaymentPreimage) == 0 || resp.AmountReceivedMsat == nil {
		return nil
	}
	now := time.Now().Unix()
	settledAt := now
	if resp.PaidAt != nil {
		settledAt = int64(*resp.PaidAt)
	}
	tx := &lnclient.Transaction{
		Type:        "incoming",
		PaymentHash: hash,
		Preimage:    hex.EncodeToString(resp.PaymentPreimage),
		AmountMsat:  i64(resp.AmountReceivedMsat.Msat),
		CreatedAt:   settledAt,
		SettledAt:   &settledAt,
		Metadata:    lnclient.Metadata{},
	}
	if resp.Bolt11 != nil {
		tx.Invoice = *resp.Bolt11
	}
	if resp.Description != nil {
		tx.Description = *resp.Description
	}
	if resp.AmountMsat != nil && resp.AmountReceivedMsat.Msat < resp.AmountMsat.Msat {
		tx.FeesPaidMsat = i64(resp.AmountMsat.Msat - resp.AmountReceivedMsat.Msat)
	}
	return tx
}
