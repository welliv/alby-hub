package greenlight

import (
	"context"
	"fmt"

	"github.com/getAlby/hub/events"
	"github.com/getAlby/hub/lnclient"
)

// Without LSPS2 JIT support (added in a follow-up), an invoice above inbound
// liquidity fails clearly instead of handing the payer an unpayable invoice.

type jitInvoiceRecord struct{}

func (g *GreenlightService) receiveAboveInbound(ctx context.Context, amountMsat, inbound int64, description, descriptionHash, label string) (*lnclient.Transaction, error) {
	if g.eventPublisher != nil {
		g.eventPublisher.Publish(&events.Event{
			Event:      "nwc_incoming_liquidity_required",
			Properties: map[string]interface{}{"node_type": "GREENLIGHT"},
		})
	}
	return nil, fmt.Errorf("cannot receive %d sats: inbound liquidity is %d sats", amountMsat/1000, inbound/1000)
}

func (g *GreenlightService) verifyJitSettle(string, uint64, uint64, lnclient.Metadata) {}
func (g *GreenlightService) lspFeeAlerts() []string                                 { return nil }
func (g *GreenlightService) lspFeeViolationCount() int                              { return 0 }
