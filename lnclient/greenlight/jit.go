package greenlight

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/getAlby/hub/events"
	"github.com/getAlby/hub/lnclient"
	"github.com/getAlby/hub/logger"
	"github.com/sirupsen/logrus"
)

// receiveAboveInbound handles an invoice larger than current inbound
// liquidity. LDK equivalent: ReceiveViaJitChannel. Greenlight's gl-plugin
// negotiates LSPS2 in LspInvoice; the advertised opening fee is remembered
// and verified on settle (jit_settle.go).
func (g *GreenlightService) receiveAboveInbound(ctx context.Context, amountMsat, inbound int64, description, descriptionHash, label string) (*lnclient.Transaction, error) {
	if g.isJitRefused() {
		return nil, fmt.Errorf("cannot receive %d sats: JIT invoices refused after an LSP opening-fee violation (inbound %d sats)",
			amountMsat/1000, inbound/1000)
	}
	jit, jitErr := g.lspInvoice(ctx, uint64(amountMsat), description, label)
	if jitErr == nil {
		if err := checkJitQuote(uint64(amountMsat), jit.OpeningFeeMsat, g.config.JitMaxFeeMsat); err != nil {
			// The invoice exists on the node but is never handed out, so
			// nobody can pay it and no channel is opened.
			logger.Logger.WithFields(logrus.Fields{
				"opening_fee_msat": jit.OpeningFeeMsat,
				"amount_msat":      amountMsat,
				"max_fee_msat":     g.config.JitMaxFeeMsat,
			}).Warn("greenlight JIT invoice refused: LSP opening fee too high")
			return nil, err
		}
		expiresAt := int64(jit.ExpiresAt)
		paymentHash := hex.EncodeToString(jit.PaymentHash)
		g.rememberJitInvoice(paymentHash, uint64(amountMsat), jit.OpeningFeeMsat)
		logger.Logger.WithFields(logrus.Fields{
			"opening_fee_msat": jit.OpeningFeeMsat,
			"inbound_msat":     inbound,
			"amount_msat":      amountMsat,
		}).Info("greenlight JIT invoice via LspInvoice")
		return &lnclient.Transaction{
			Type:            "incoming",
			Invoice:         jit.Bolt11,
			Description:     description,
			DescriptionHash: descriptionHash,
			Preimage:        "",
			PaymentHash:     paymentHash,
			AmountMsat:      amountMsat,
			FeesPaidMsat:    i64(jit.OpeningFeeMsat),
			CreatedAt:       time.Now().Unix(),
			ExpiresAt:       &expiresAt,
			SettledAt:       nil,
			Metadata: lnclient.Metadata{
				"jit_advertised_fee_msat": jit.OpeningFeeMsat,
			},
			SettleDeadline: nil,
		}, nil
	}
	logJitFallback(jitErr, amountMsat, inbound)
	// Node unreachable: say so instead of blaming liquidity
	// (inbound reads as 0 when ListPeerChannels itself failed).
	if isNodeUnreachable(jitErr) {
		return nil, fmt.Errorf("greenlight node unreachable, cannot create invoice: %w", jitErr)
	}
	if g.eventPublisher != nil {
		g.eventPublisher.Publish(&events.Event{
			Event: "nwc_incoming_liquidity_required",
			Properties: map[string]interface{}{
				"node_type": "GREENLIGHT",
			},
		})
	}
	// A regular invoice above inbound capacity is unpayable: fail
	// clearly instead of handing the payer a dead invoice.
	return nil, fmt.Errorf("cannot receive %d sats: inbound liquidity is %d sats and the LSP could not open a channel (%s)",
		amountMsat/1000, inbound/1000, lspErrorReason(jitErr))
}

func lspErrorReason(err error) string {
	if err == nil {
		return "unknown"
	}
	msg := err.Error()
	if i := strings.LastIndex(msg, "Error code"); i >= 0 {
		msg = msg[i:]
	}
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

// checkJitQuote refuses a JIT invoice whose advertised LSPS2 opening
// fee would leave the user with nothing (fee >= amount) or exceeds the
// operator's optional cap (maxFeeMsat > 0).
func checkJitQuote(amountMsat, feeMsat, maxFeeMsat uint64) error {
	if feeMsat >= amountMsat {
		return fmt.Errorf("cannot receive %d sats: the LSP channel-opening fee (%d sats) is not below the amount",
			amountMsat/1000, feeMsat/1000)
	}
	if maxFeeMsat > 0 && feeMsat > maxFeeMsat {
		return fmt.Errorf("cannot receive %d sats: the LSP channel-opening fee (%d sats) is above the configured maximum (%d sats, GREENLIGHT_JIT_MAX_FEE_SAT)",
			amountMsat/1000, feeMsat/1000, maxFeeMsat/1000)
	}
	return nil
}
