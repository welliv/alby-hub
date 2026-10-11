package greenlight

import (
	"context"
	"strings"
	"testing"
)

func TestMakeInvoiceAboveInboundFailsClearlyWithoutJit(t *testing.T) {
	node := newMockNode()
	in := uint64(1_000_000)
	node.receivable = &in
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	_, err := svc.MakeInvoice(context.Background(), 5_000_000, "big", "", 0, nil)
	if err == nil || !strings.Contains(err.Error(), "inbound liquidity") {
		t.Fatalf("expected clear inbound error, got %v", err)
	}
}
