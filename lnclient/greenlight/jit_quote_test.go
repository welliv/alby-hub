package greenlight

import (
	"context"
	"strings"
	"testing"
)

func TestCheckJitQuote(t *testing.T) {
	cases := []struct {
		name               string
		amount, fee, limit uint64
		ok                 bool
	}{
		{"normal fee, no cap", 100_000, 5_000, 0, true},
		{"zero fee", 100_000, 0, 0, true},
		{"fee equals amount", 100_000, 100_000, 0, false},
		{"fee above amount", 1_000, 5_000, 0, false},
		{"fee at cap", 100_000, 5_000, 5_000, true},
		{"fee above cap", 100_000, 5_001, 5_000, false},
	}
	for _, c := range cases {
		err := checkJitQuote(c.amount, c.fee, c.limit)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestMakeInvoice_JitRefusedWhenFeeAboveCap(t *testing.T) {
	node := newMockNode()
	zero := uint64(0)
	node.receivable = &zero
	node.lspInvoiceOK = true
	node.lspOpeningFee = 6_000_000 // 6,000 sats
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	svc.config.JitMaxFeeMsat = 5_000_000

	_, err := svc.MakeInvoice(context.Background(), 20_000_000, "too expensive", "", 3600, nil)
	if err == nil || !strings.Contains(err.Error(), "above the configured maximum") {
		t.Fatalf("expected cap refusal, got %v", err)
	}
	if len(svc.jitPending) != 0 {
		t.Fatalf("refused JIT invoice must not be remembered: %d", len(svc.jitPending))
	}

	svc.config.JitMaxFeeMsat = 0
	if _, err := svc.MakeInvoice(context.Background(), 20_000_000, "ok", "", 3600, nil); err != nil {
		t.Fatalf("no cap: %v", err)
	}
}

func TestMakeInvoice_JitRefusedWhenFeeNotBelowAmount(t *testing.T) {
	node := newMockNode()
	zero := uint64(0)
	node.receivable = &zero
	node.lspInvoiceOK = true
	node.lspOpeningFee = 2_000_000
	svc, cleanup := newTestService(t, node)
	defer cleanup()

	_, err := svc.MakeInvoice(context.Background(), 2_000_000, "all fee", "", 3600, nil)
	if err == nil || !strings.Contains(err.Error(), "not below the amount") {
		t.Fatalf("expected refusal, got %v", err)
	}
}
