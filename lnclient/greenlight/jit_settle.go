package greenlight

import (
	"time"

	"github.com/getAlby/hub/events"
	"github.com/getAlby/hub/lnclient"
	"github.com/getAlby/hub/logger"
	"github.com/sirupsen/logrus"
)

const (
	lspFeeViolationEvent = "nwc_gl_lsp_fee_violation"
	// alert surfaced in GetNodeStatus InternalNodeStatus
	lspFeeViolationAlert = "lsp_opening_fee_exceeded"
)

// jitInvoiceRecord remembers the LSPS2 opening fee advertised when a JIT
// invoice was minted, so settle-time verification can detect over-skimming.
type jitInvoiceRecord struct {
	InvoiceAmountMsat uint64
	AdvertisedFeeMsat uint64
	CreatedAt         time.Time
}

// jitSettleResult is the outcome of comparing received amount against the
// invoice amount minus the advertised opening fee.
type jitSettleResult struct {
	InvoiceAmountMsat    uint64
	AdvertisedFeeMsat    uint64
	ReceivedAmountMsat   uint64
	ExpectedReceivedMsat uint64
	ActualFeeMsat        uint64
	SkimExcessMsat       uint64
	Violation            bool
	HadAdvertisedFee     bool // false when we have no remembered quote
}

// checkJitOpeningFee compares what the node actually received against what
// the LSP promised (invoice − advertised opening fee). Credit always uses
// the received amount (caller already does that); this only flags over-skim.
//
// If advertisedFee > invoiceAmount, expected received is 0 (saturating).
// A received amount above expected is fine (LSP took less than advertised).
func checkJitOpeningFee(invoiceAmount, advertisedFee, received uint64) jitSettleResult {
	expected := invoiceAmount
	if advertisedFee < invoiceAmount {
		expected = invoiceAmount - advertisedFee
	} else {
		expected = 0
	}
	actualFee := uint64(0)
	if received < invoiceAmount {
		actualFee = invoiceAmount - received
	}
	res := jitSettleResult{
		InvoiceAmountMsat:    invoiceAmount,
		AdvertisedFeeMsat:    advertisedFee,
		ReceivedAmountMsat:   received,
		ExpectedReceivedMsat: expected,
		ActualFeeMsat:        actualFee,
		HadAdvertisedFee:     true,
	}
	if received < expected {
		res.Violation = true
		res.SkimExcessMsat = expected - received
	}
	return res
}

func (g *GreenlightService) rememberJitInvoice(paymentHashHex string, invoiceAmountMsat, advertisedFeeMsat uint64) {
	if paymentHashHex == "" {
		return
	}
	g.jitMtx.Lock()
	defer g.jitMtx.Unlock()
	if g.jitPending == nil {
		g.jitPending = make(map[string]jitInvoiceRecord)
	}
	g.jitPending[paymentHashHex] = jitInvoiceRecord{
		InvoiceAmountMsat: invoiceAmountMsat,
		AdvertisedFeeMsat: advertisedFeeMsat,
		CreatedAt:         time.Now(),
	}
}

func (g *GreenlightService) takeJitInvoice(paymentHashHex string) (jitInvoiceRecord, bool) {
	g.jitMtx.Lock()
	defer g.jitMtx.Unlock()
	rec, ok := g.jitPending[paymentHashHex]
	if ok {
		delete(g.jitPending, paymentHashHex)
	}
	return rec, ok
}

func (g *GreenlightService) isJitRefused() bool {
	g.jitMtx.RLock()
	defer g.jitMtx.RUnlock()
	return g.jitRefused
}

func (g *GreenlightService) lspFeeViolationCount() int {
	g.jitMtx.RLock()
	defer g.jitMtx.RUnlock()
	return g.jitViolations
}

func (g *GreenlightService) lspFeeAlerts() []string {
	g.jitMtx.RLock()
	defer g.jitMtx.RUnlock()
	if g.jitViolations == 0 {
		return nil
	}
	return []string{lspFeeViolationAlert}
}

// verifyJitSettle checks a paid invoice against a remembered LSPS2 quote.
// When there is no remembered quote, it is a no-op (restart / non-JIT).
// On violation: logs (no secrets), publishes an event, increments alert
// counters, and optionally refuses further JIT invoices.
func (g *GreenlightService) verifyJitSettle(paymentHashHex string, invoiceAmountMsat, receivedAmountMsat uint64, metadata lnclient.Metadata) jitSettleResult {
	rec, ok := g.takeJitInvoice(paymentHashHex)
	if !ok {
		return jitSettleResult{HadAdvertisedFee: false}
	}
	// Prefer the invoice amount from the node when present; fall back to remembered.
	invAmt := invoiceAmountMsat
	if invAmt == 0 {
		invAmt = rec.InvoiceAmountMsat
	}
	res := checkJitOpeningFee(invAmt, rec.AdvertisedFeeMsat, receivedAmountMsat)
	if metadata != nil {
		metadata["jit_advertised_fee_msat"] = res.AdvertisedFeeMsat
		metadata["jit_expected_received_msat"] = res.ExpectedReceivedMsat
		if res.Violation {
			metadata["jit_fee_violation"] = true
			metadata["jit_skim_excess_msat"] = res.SkimExcessMsat
		}
	}
	if !res.Violation {
		return res
	}

	logger.Logger.WithFields(logrus.Fields{
		"payment_hash":           paymentHashHex,
		"invoice_amount_msat":    res.InvoiceAmountMsat,
		"advertised_fee_msat":    res.AdvertisedFeeMsat,
		"received_amount_msat":   res.ReceivedAmountMsat,
		"expected_received_msat": res.ExpectedReceivedMsat,
		"skim_excess_msat":       res.SkimExcessMsat,
		"actual_fee_msat":        res.ActualFeeMsat,
	}).Warn("greenlight LSPS2 opening fee exceeded advertised quote; crediting received amount only")

	g.jitMtx.Lock()
	g.jitViolations++
	if g.config.RefuseJitAfterLspViolation {
		g.jitRefused = true
	}
	violations := g.jitViolations
	refused := g.jitRefused
	g.jitMtx.Unlock()

	if g.eventPublisher != nil {
		g.eventPublisher.Publish(&events.Event{
			Event: lspFeeViolationEvent,
			Properties: map[string]interface{}{
				"payment_hash":           paymentHashHex,
				"invoice_amount_msat":    res.InvoiceAmountMsat,
				"advertised_fee_msat":    res.AdvertisedFeeMsat,
				"received_amount_msat":   res.ReceivedAmountMsat,
				"expected_received_msat": res.ExpectedReceivedMsat,
				"skim_excess_msat":       res.SkimExcessMsat,
				"jit_refused":            refused,
				"violation_count":        violations,
			},
		})
	}
	return res
}
