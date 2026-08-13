package greenlight

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/getAlby/hub/lnclient/cln/clngrpc"
	"github.com/getAlby/hub/logger"
	"github.com/sirupsen/logrus"
)

// lspInvoiceResult is the decoded greenlight.LspInvoiceResponse.
// opening_fee_msat is 0 when the node already had enough inbound capacity.
type lspInvoiceResult struct {
	Bolt11         string
	ExpiresAt      uint32
	PaymentHash    []byte
	OpeningFeeMsat uint64
}

// lspInvoice calls /greenlight.Node/LspInvoice on the same mTLS connection
// as cln-grpc. The plugin negotiates LSPS2 when inbound is short and
// otherwise mints a regular invoice (opening_fee_msat = 0).
func (g *GreenlightService) lspInvoice(ctx context.Context, amountMsat uint64, description, label string) (*lspInvoiceResult, error) {
	if g.conn == nil {
		return nil, fmt.Errorf("no greenlight connection")
	}
	req := encodeLspInvoiceRequest(amountMsat, description, label)
	resp := &lspInvoiceResponse{}
	err := g.conn.Invoke(ctx, "/greenlight.Node/LspInvoice", req, resp, grpc.CallCustomCodec(rawCodec{}))
	if err != nil {
		return nil, err
	}
	if resp.Bolt11 == "" || len(resp.PaymentHash) == 0 {
		return nil, fmt.Errorf("lsp invoice returned empty bolt11 or payment hash")
	}
	return &lspInvoiceResult{
		Bolt11:         resp.Bolt11,
		ExpiresAt:      resp.ExpiresAt,
		PaymentHash:    resp.PaymentHash,
		OpeningFeeMsat: resp.OpeningFeeMsat,
	}, nil
}

func (g *GreenlightService) maxReceivableMsat(ctx context.Context) int64 {
	resp, err := g.client.ListPeerChannels(ctx, &clngrpc.ListpeerchannelsRequest{})
	if err != nil {
		logger.Logger.WithError(err).Debug("listpeerchannels for JIT inbound check failed")
		return 0
	}
	var total int64
	for _, ch := range resp.Channels {
		if ch == nil || ch.State != clngrpc.ChannelState_ChanneldNormal || !ch.PeerConnected {
			continue
		}
		if ch.ReceivableMsat != nil {
			total += int64(ch.ReceivableMsat.Msat)
		}
	}
	return total
}

func encodeLspInvoiceRequest(amountMsat uint64, description, label string) []byte {
	var b []byte
	// lsp_id (1) and token (2) left empty: plugin picks the LSP.
	if amountMsat > 0 {
		b = protowire.AppendTag(b, 3, protowire.VarintType)
		b = protowire.AppendVarint(b, amountMsat)
	}
	if description != "" {
		b = protowire.AppendTag(b, 4, protowire.BytesType)
		b = protowire.AppendString(b, description)
	}
	if label != "" {
		b = protowire.AppendTag(b, 5, protowire.BytesType)
		b = protowire.AppendString(b, label)
	}
	return b
}

type lspInvoiceResponse struct {
	Bolt11         string
	ExpiresAt      uint32
	PaymentHash    []byte
	OpeningFeeMsat uint64
}

func (*lspInvoiceResponse) Reset()         {}
func (*lspInvoiceResponse) String() string { return "LspInvoiceResponse" }
func (*lspInvoiceResponse) ProtoMessage()  {}

func (r *lspInvoiceResponse) decode(data []byte) error {
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return protowire.ParseError(n)
		}
		data = data[n:]
		switch {
		case num == 1 && typ == protowire.BytesType:
			v, n := protowire.ConsumeString(data)
			if n < 0 {
				return protowire.ParseError(n)
			}
			data = data[n:]
			r.Bolt11 = v
		case num == 2 && typ == protowire.VarintType:
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return protowire.ParseError(n)
			}
			data = data[n:]
			// created_index unused
			_ = v
		case num == 3 && typ == protowire.VarintType:
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return protowire.ParseError(n)
			}
			data = data[n:]
			r.ExpiresAt = uint32(v)
		case num == 4 && typ == protowire.BytesType:
			v, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return protowire.ParseError(n)
			}
			data = data[n:]
			r.PaymentHash = v
		case num == 5 && typ == protowire.BytesType:
			v, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return protowire.ParseError(n)
			}
			data = data[n:]
			// payment_secret unused
			_ = v
		case num == 6 && typ == protowire.VarintType:
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return protowire.ParseError(n)
			}
			data = data[n:]
			r.OpeningFeeMsat = v
		default:
			n := protowire.ConsumeFieldValue(num, typ, data)
			if n < 0 {
				return protowire.ParseError(n)
			}
			data = data[n:]
		}
	}
	return nil
}

func logJitFallback(err error, amountMsat int64, inbound int64) {
	logger.Logger.WithError(err).WithFields(logrus.Fields{
		"amount_msat":  amountMsat,
		"inbound_msat": inbound,
	}).Info("greenlight LspInvoice unavailable, minting a regular invoice")
}
