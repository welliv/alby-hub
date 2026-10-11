package greenlight

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCheckJitOpeningFee_ExactQuote(t *testing.T) {
	res := checkJitOpeningFee(100_000, 5_000, 95_000)
	if res.Violation || res.SkimExcessMsat != 0 || res.ExpectedReceivedMsat != 95_000 {
		t.Fatalf("unexpected %#v", res)
	}
}

func TestCheckJitOpeningFee_LSPTookLess(t *testing.T) {
	res := checkJitOpeningFee(100_000, 5_000, 97_000)
	if res.Violation {
		t.Fatalf("under-fee should not violate: %#v", res)
	}
}

func TestCheckJitOpeningFee_LSPSkim(t *testing.T) {
	res := checkJitOpeningFee(100_000, 5_000, 92_000)
	if !res.Violation {
		t.Fatal("expected violation")
	}
	if res.SkimExcessMsat != 3_000 {
		t.Fatalf("excess %d", res.SkimExcessMsat)
	}
	if res.ActualFeeMsat != 8_000 {
		t.Fatalf("actual fee %d", res.ActualFeeMsat)
	}
	if res.ExpectedReceivedMsat != 95_000 {
		t.Fatalf("expected %d", res.ExpectedReceivedMsat)
	}
}

func TestCheckJitOpeningFee_FeeExceedsInvoice(t *testing.T) {
	res := checkJitOpeningFee(1_000, 5_000, 0)
	if res.ExpectedReceivedMsat != 0 {
		t.Fatalf("expected 0, got %d", res.ExpectedReceivedMsat)
	}
	if res.Violation {
		t.Fatalf("received 0 == expected 0 should not violate: %#v", res)
	}
}

func TestCheckJitOpeningFee_ZeroFee(t *testing.T) {
	res := checkJitOpeningFee(50_000, 0, 50_000)
	if res.Violation {
		t.Fatalf("%#v", res)
	}
	res = checkJitOpeningFee(50_000, 0, 49_000)
	if !res.Violation || res.SkimExcessMsat != 1_000 {
		t.Fatalf("%#v", res)
	}
}

func TestJitSettle_MockLSPSkim(t *testing.T) {
	node := newMockNode()
	zero := uint64(0)
	node.receivable = &zero
	node.lspInvoiceOK = true
	node.lspOpeningFee = 5_000
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	cap := &capturePublisher{}
	svc.eventPublisher = cap

	const amount int64 = 100_000
	tx, err := svc.MakeInvoice(context.Background(), amount, "jit skim", "", 3600, nil)
	if err != nil {
		t.Fatalf("MakeInvoice: %v", err)
	}
	if tx.FeesPaidMsat != 5_000 {
		t.Fatalf("advertised fee on tx: %d", tx.FeesPaidMsat)
	}

	received := uint64(amount) - 8_000
	node.markPaidReceived(tx.PaymentHash, received)

	looked, err := svc.LookupInvoice(context.Background(), tx.PaymentHash)
	if err != nil {
		t.Fatalf("LookupInvoice: %v", err)
	}
	if looked.AmountMsat != int64(received) {
		t.Fatalf("credited %d want %d", looked.AmountMsat, received)
	}
	if looked.FeesPaidMsat != 8_000 {
		t.Fatalf("reported fee %d", looked.FeesPaidMsat)
	}
	if looked.Metadata["jit_fee_violation"] != true {
		t.Fatalf("metadata missing violation: %#v", looked.Metadata)
	}
	if looked.Metadata["jit_skim_excess_msat"] != uint64(3_000) {
		t.Fatalf("excess meta %#v", looked.Metadata["jit_skim_excess_msat"])
	}

	if ev := cap.waitForEvent(lspFeeViolationEvent, 2*time.Second); ev == nil {
		t.Fatal("expected lsp fee violation event")
	}

	// warm health cache then read alerts
	_, _ = svc.runHealthCheck(svc.ctx)
	status, err := svc.GetNodeStatus(context.Background())
	if err != nil {
		t.Fatalf("GetNodeStatus: %v", err)
	}
	health := status.InternalNodeStatus.(nodeHealthStatus)
	if health.LspFeeViolations != 1 {
		t.Fatalf("violations %d", health.LspFeeViolations)
	}
	if len(health.Alerts) == 0 || health.Alerts[0] != lspFeeViolationAlert {
		t.Fatalf("alerts %#v", health.Alerts)
	}
}

func TestJitSettle_HonestLSPNoAlert(t *testing.T) {
	node := newMockNode()
	zero := uint64(0)
	node.receivable = &zero
	node.lspInvoiceOK = true
	node.lspOpeningFee = 5_000
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	cap := &capturePublisher{}
	svc.eventPublisher = cap

	tx, err := svc.MakeInvoice(context.Background(), 100_000, "jit ok", "", 3600, nil)
	if err != nil {
		t.Fatalf("MakeInvoice: %v", err)
	}
	node.markPaidReceived(tx.PaymentHash, 95_000)
	looked, err := svc.LookupInvoice(context.Background(), tx.PaymentHash)
	if err != nil {
		t.Fatalf("LookupInvoice: %v", err)
	}
	if looked.AmountMsat != 95_000 || looked.FeesPaidMsat != 5_000 {
		t.Fatalf("amounts amount=%d fee=%d", looked.AmountMsat, looked.FeesPaidMsat)
	}
	if looked.Metadata["jit_fee_violation"] != nil {
		t.Fatalf("should not flag: %#v", looked.Metadata)
	}
	if cap.waitForEvent(lspFeeViolationEvent, 200*time.Millisecond) != nil {
		t.Fatal("unexpected violation event")
	}
}

func TestJitSettle_RefuseAfterViolation(t *testing.T) {
	node := newMockNode()
	zero := uint64(0)
	node.receivable = &zero
	node.lspInvoiceOK = true
	node.lspOpeningFee = 1_000
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	svc.config.RefuseJitAfterLspViolation = true
	svc.eventPublisher = &capturePublisher{}

	tx, err := svc.MakeInvoice(context.Background(), 50_000, "first", "", 3600, nil)
	if err != nil {
		t.Fatalf("MakeInvoice: %v", err)
	}
	node.markPaidReceived(tx.PaymentHash, 40_000)
	if _, err := svc.LookupInvoice(context.Background(), tx.PaymentHash); err != nil {
		t.Fatalf("LookupInvoice: %v", err)
	}
	if !svc.isJitRefused() {
		t.Fatal("expected jit refused")
	}
	_, err = svc.MakeInvoice(context.Background(), 50_000, "second", "", 3600, nil)
	if err == nil {
		t.Fatal("expected refuse error")
	}
	if !strings.Contains(err.Error(), "JIT invoices refused") {
		t.Fatalf("error %v", err)
	}
}
