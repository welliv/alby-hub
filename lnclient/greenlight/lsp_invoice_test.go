package greenlight

import (
	"bytes"
	"context"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestEncodeLspInvoiceRequest(t *testing.T) {
	got := encodeLspInvoiceRequest(21000, "hello", "lbl-1")
	amount, desc, label := decodeLspInvoiceRequest(t, got)
	if amount != 21000 || desc != "hello" || label != "lbl-1" {
		t.Fatalf("got amount=%d desc=%q label=%q", amount, desc, label)
	}
	empty := encodeLspInvoiceRequest(0, "", "")
	if len(empty) != 0 {
		t.Fatalf("zero request should omit all fields, got %d bytes", len(empty))
	}
}

func TestDecodeLspInvoiceResponse(t *testing.T) {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendString(b, "lntb10n1test")
	b = protowire.AppendTag(b, 3, protowire.VarintType)
	b = protowire.AppendVarint(b, 1700000000)
	hash := bytes.Repeat([]byte{0xab}, 32)
	b = protowire.AppendTag(b, 4, protowire.BytesType)
	b = protowire.AppendBytes(b, hash)
	b = protowire.AppendTag(b, 6, protowire.VarintType)
	b = protowire.AppendVarint(b, 1234)

	resp := &lspInvoiceResponse{}
	if err := resp.decode(b); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Bolt11 != "lntb10n1test" {
		t.Fatalf("bolt11 %q", resp.Bolt11)
	}
	if resp.ExpiresAt != 1700000000 {
		t.Fatalf("expires %d", resp.ExpiresAt)
	}
	if !bytes.Equal(resp.PaymentHash, hash) {
		t.Fatalf("hash mismatch")
	}
	if resp.OpeningFeeMsat != 1234 {
		t.Fatalf("fee %d", resp.OpeningFeeMsat)
	}
}

func TestMakeInvoiceFallsBackWhenLspUnavailable(t *testing.T) {
	// mock has 49m msat inbound, so 100k uses the regular Invoice path.
	svc, cleanup := newTestService(t, newMockNode())
	defer cleanup()
	tx, err := svc.MakeInvoice(context.Background(), 100000, "regular", "", 3600, nil)
	if err != nil {
		t.Fatalf("MakeInvoice: %v", err)
	}
	if tx.Invoice == "" || tx.FeesPaidMsat != 0 {
		t.Fatalf("expected regular invoice with 0 fee, got %#v", tx)
	}
}

func TestMakeInvoiceJitFallsBackWithoutLsp(t *testing.T) {
	node := newMockNode()
	zero := uint64(0)
	node.receivable = &zero
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	tx, err := svc.MakeInvoice(context.Background(), 100000, "needs jit", "", 3600, nil)
	if err != nil {
		t.Fatalf("MakeInvoice should fall back to Invoice: %v", err)
	}
	if tx.Invoice == "" {
		t.Fatal("expected fallback bolt11")
	}
	if tx.FeesPaidMsat != 0 {
		t.Fatalf("fallback fee should be 0, got %d", tx.FeesPaidMsat)
	}
}

func decodeLspInvoiceRequest(t *testing.T, data []byte) (amount uint64, desc, label string) {
	t.Helper()
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			t.Fatal(protowire.ParseError(n))
		}
		data = data[n:]
		switch {
		case num == 3 && typ == protowire.VarintType:
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				t.Fatal(protowire.ParseError(n))
			}
			data = data[n:]
			amount = v
		case num == 4 && typ == protowire.BytesType:
			v, n := protowire.ConsumeString(data)
			if n < 0 {
				t.Fatal(protowire.ParseError(n))
			}
			data = data[n:]
			desc = v
		case num == 5 && typ == protowire.BytesType:
			v, n := protowire.ConsumeString(data)
			if n < 0 {
				t.Fatal(protowire.ParseError(n))
			}
			data = data[n:]
			label = v
		default:
			n := protowire.ConsumeFieldValue(num, typ, data)
			if n < 0 {
				t.Fatal(protowire.ParseError(n))
			}
			data = data[n:]
		}
	}
	return
}
