package greenlight

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/lightningnetwork/lnd/lnwire"
	"github.com/lightningnetwork/lnd/zpay32"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/getAlby/hub/lnclient"
	"github.com/getAlby/hub/lnclient/cln/clngrpc"
)

type testInvoiceOpts struct {
	params      *chaincfg.Params
	amountMsat  uint64
	noFeatures  bool
	noSecret    bool
	extraBits   []lnwire.FeatureBit
	timestamp   time.Time
	expiry      time.Duration
	paymentHash *[32]byte
}

func makeTestInvoice(t testing.TB, o testInvoiceOpts) string {
	t.Helper()
	if o.params == nil {
		o.params = &chaincfg.TestNet3Params
	}
	if o.timestamp.IsZero() {
		o.timestamp = time.Now()
	}
	key, _ := btcec.NewPrivateKey()
	var hash [32]byte
	if o.paymentHash != nil {
		hash = *o.paymentHash
	} else {
		_, _ = rand.Read(hash[:])
	}
	var secret [32]byte
	_, _ = rand.Read(secret[:])
	opts := []func(*zpay32.Invoice){zpay32.Description("test")}
	if o.amountMsat > 0 {
		opts = append(opts, zpay32.Amount(lnwire.MilliSatoshi(o.amountMsat)))
	}
	if !o.noSecret {
		opts = append(opts, zpay32.PaymentAddr(secret))
	}
	if o.expiry > 0 {
		opts = append(opts, zpay32.Expiry(o.expiry))
	}
	if !o.noFeatures {
		bits := append([]lnwire.FeatureBit{lnwire.TLVOnionPayloadRequired, lnwire.PaymentAddrRequired}, o.extraBits...)
		opts = append(opts, zpay32.Features(lnwire.NewFeatureVector(lnwire.NewRawFeatureVector(bits...), lnwire.Features)))
	}
	inv, err := zpay32.NewInvoice(o.params, hash, o.timestamp, opts...)
	if err != nil {
		t.Fatal(err)
	}
	s, err := inv.Encode(zpay32.MessageSigner{SignCompact: func(m []byte) ([]byte, error) {
		return ecdsa.SignCompact(key, m, true), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestValidateBolt11(t *testing.T) {
	now := time.Now()
	amt := uint64(5000)
	other := uint64(6000)
	cases := []struct {
		name   string
		inv    string
		amount *uint64
		ok     bool
	}{
		{"valid", makeTestInvoice(t, testInvoiceOpts{amountMsat: 5000}), nil, true},
		{"valid amountless with amount", makeTestInvoice(t, testInvoiceOpts{}), &amt, true},
		{"amountless without amount", makeTestInvoice(t, testInvoiceOpts{}), nil, false},
		{"amount mismatch", makeTestInvoice(t, testInvoiceOpts{amountMsat: 5000}), &other, false},
		{"wrong network (mainnet invoice)", makeTestInvoice(t, testInvoiceOpts{amountMsat: 5000, params: &chaincfg.MainNetParams}), nil, false},
		// regression: the live testnet freeze was triggered by an invoice without features
		{"no features (live freeze regression)", makeTestInvoice(t, testInvoiceOpts{amountMsat: 5000, noFeatures: true}), nil, false},
		{"no payment secret", makeTestInvoice(t, testInvoiceOpts{amountMsat: 5000, noSecret: true}), nil, false},
		{"unknown required bit", makeTestInvoice(t, testInvoiceOpts{amountMsat: 5000, extraBits: []lnwire.FeatureBit{100}}), nil, false},
		{"expired", makeTestInvoice(t, testInvoiceOpts{amountMsat: 5000, timestamp: now.Add(-2 * time.Hour), expiry: time.Hour}), nil, false},
		{"garbage", "lntb1garbage", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := validateBolt11(c.inv, "testnet", c.amount, now)
			if c.ok && err != nil {
				t.Fatalf("expected valid, got %v", err)
			}
			if !c.ok && !errors.Is(err, ErrInvalidInvoice) {
				t.Fatalf("expected ErrInvalidInvoice, got %v", err)
			}
		})
	}
}

func TestInvalidInvoiceNeverReachesNode(t *testing.T) {
	node := newMockNode()
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	_, err := svc.SendPaymentSync(makeTestInvoice(t, testInvoiceOpts{amountMsat: 1000, noFeatures: true}), nil)
	if !errors.Is(err, ErrInvalidInvoice) {
		t.Fatalf("expected ErrInvalidInvoice, got %v", err)
	}
	if node.xpayCalls != 0 {
		t.Fatalf("xpay must not be called for an invalid invoice")
	}
}

func TestPayTransportErrorIsPendingNotFailed(t *testing.T) {
	node := newMockNode()
	node.xpayErr = status.Error(codes.Unavailable, "connection reset")
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	_, err := svc.SendPaymentSync(makeTestInvoice(t, testInvoiceOpts{amountMsat: 1000}), nil)
	if !errors.Is(err, lnclient.ErrPaymentPending) {
		t.Fatalf("transport error must be pending, got %v", err)
	}
	if len(svc.reconciler.tracked) != 1 {
		t.Fatalf("payment must be tracked for reconciliation")
	}
}

func TestPayDefinitiveFailureIsFailed(t *testing.T) {
	node := newMockNode()
	node.xpayErr = status.Error(codes.Unknown, "Error code 209: Failed after 3 attempts")
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	_, err := svc.SendPaymentSync(makeTestInvoice(t, testInvoiceOpts{amountMsat: 1000}), nil)
	if err == nil || errors.Is(err, lnclient.ErrPaymentPending) {
		t.Fatalf("definitive failure must be a plain error, got %v", err)
	}
}

func TestPayDefinitiveFailureGlRpcErrorShape(t *testing.T) {
	node := newMockNode()
	node.xpayErr = status.Error(codes.Unknown, `Error calling method Xpay: RpcError { code: Some(205), message: "Failed: There is no connection between source and destination at all", data: None }`)
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	_, err := svc.SendPaymentSync(makeTestInvoice(t, testInvoiceOpts{amountMsat: 1000}), nil)
	if err == nil || errors.Is(err, lnclient.ErrPaymentPending) {
		t.Fatalf("live-observed xpay 205 must be a confirmed failure, got %v", err)
	}
}

func TestPayErrorButNodeCompleteReconcilesSent(t *testing.T) {
	node := newMockNode()
	node.xpayErr = status.Error(codes.Unknown, "Error code 209: odd")
	pre := []byte("0123456789abcdef0123456789abcdef")
	hash := sha256.Sum256(pre)
	node.pays = []*clngrpc.ListpaysPays{{PaymentHash: hash[:], Status: clngrpc.ListpaysPays_COMPLETE, Preimage: pre,
		AmountMsat: &clngrpc.Amount{Msat: 1000}, AmountSentMsat: &clngrpc.Amount{Msat: 1002}}}
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	pub := &capturePublisher{}
	svc.eventPublisher = pub
	_, err := svc.SendPaymentSync(makeTestInvoice(t, testInvoiceOpts{amountMsat: 1000, paymentHash: &hash}), nil)
	if !errors.Is(err, lnclient.ErrPaymentPending) {
		t.Fatalf("must not be reported failed when node shows complete: %v", err)
	}
	for _, tp := range svc.reconciler.tracked {
		if !svc.reconcileOnce(svc.ctx, tp, time.Now()) {
			t.Fatal("expected final")
		}
	}
	if ev := pub.waitForEvent("nwc_lnclient_payment_sent", time.Second); ev == nil {
		t.Fatal("expected payment_sent event")
	} else if tx := ev.Properties.(*lnclient.Transaction); tx.FeesPaidMsat != 2 {
		t.Fatalf("fee mismatch %d", tx.FeesPaidMsat)
	}
}

func TestReconcileNoRecordFailsAfterWindow(t *testing.T) {
	node := newMockNode()
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	pub := &capturePublisher{}
	svc.eventPublisher = pub
	tp := &trackedPayment{PaymentHash: "00" + string(make([]byte, 0)) + "11223344556677889900112233445566778899001122334455667788990011", Started: time.Now()}
	if svc.reconcileOnce(svc.ctx, tp, time.Now()) {
		t.Fatal("must stay pending inside the window")
	}
	if !svc.reconcileOnce(svc.ctx, tp, time.Now().Add(noRecordFailAfter+time.Second)) {
		t.Fatal("must fail after the window")
	}
	if pub.waitForEvent("nwc_lnclient_payment_failed", time.Second) == nil {
		t.Fatal("expected payment_failed")
	}
}

func TestKeysendAmbiguousTrackedByLabel(t *testing.T) {
	node := newMockNode()
	node.keysendErr = status.Error(codes.DeadlineExceeded, "deadline")
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	pub := &capturePublisher{}
	svc.eventPublisher = pub
	pre := make([]byte, 32)
	_, err := svc.SendKeysend(1000, "02f1d2e5f1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d", nil, "00"+hexOf(pre)[2:])
	if !errors.Is(err, lnclient.ErrPaymentPending) {
		t.Fatalf("expected pending, got %v", err)
	}
	var tp *trackedPayment
	for _, v := range svc.reconciler.tracked {
		tp = v
	}
	if tp == nil || tp.Label == "" {
		t.Fatal("keysend must be tracked by label")
	}
	sum := sha256.Sum256(pre)
	if tp.RequestedPaymentHash != hexOf(sum[:]) {
		t.Fatal("requested hash must be sha256(preimage)")
	}
	realSum := sha256.Sum256(pre)
	real := realSum[:]
	node.mu.Lock()
	node.pays = []*clngrpc.ListpaysPays{{PaymentHash: real, Label: &tp.Label, Status: clngrpc.ListpaysPays_COMPLETE, Preimage: pre}}
	node.mu.Unlock()
	if !svc.reconcileOnce(svc.ctx, tp, time.Now()) {
		t.Fatal("expected final")
	}
	ev := pub.waitForEvent("nwc_lnclient_payment_sent", time.Second)
	if ev == nil {
		t.Fatal("expected sent event")
	}
	if ev.Properties.(*lnclient.Transaction).Metadata[lnclient.MetadataRequestedPaymentHash] != tp.RequestedPaymentHash {
		t.Fatal("event must carry the requested hash for remapping")
	}
}

func TestDeadlineInterceptorAddsDeadline(t *testing.T) {
	if rpcTimeoutFor("/cln.Node/Getinfo") != defaultRPCTimeout || rpcTimeoutFor("/cln.Node/Xpay") != payRPCTimeout {
		t.Fatal("timeouts")
	}
}

func FuzzValidateBolt11(f *testing.F) {
	f.Add(makeTestInvoice(f, testInvoiceOpts{amountMsat: 1000}))
	f.Add("lntb1")
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = validateBolt11(s, "testnet", nil, time.Now())
	})
}

func hexOf(b []byte) string {
	const h = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2], out[i*2+1] = h[c>>4], h[c&0xf]
	}
	return string(out)
}

func TestPaidInvoiceFallbackWhenLookupFails(t *testing.T) {
	node := newMockNode()
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	paidAt := uint64(time.Now().Unix())
	resp := &clngrpc.WaitanyinvoiceResponse{
		PaymentHash:        []byte("01234567890123456789012345678901"),
		PaymentPreimage:    []byte("preimagepreimagepreimagepreimage"),
		AmountMsat:         &clngrpc.Amount{Msat: 50_000_000},
		AmountReceivedMsat: &clngrpc.Amount{Msat: 47_500_000},
		PaidAt:             &paidAt,
		Status:             clngrpc.WaitanyinvoiceResponse_PAID,
	}
	tx := svc.paidInvoiceTransaction(svc.ctx, resp) // keysend-style: not in listinvoices
	if tx == nil {
		t.Fatal("fallback must still produce a transaction so the payment is credited")
	}
	if tx.AmountMsat != 47_500_000 || tx.FeesPaidMsat != 2_500_000 {
		t.Fatalf("amounts %d/%d", tx.AmountMsat, tx.FeesPaidMsat)
	}
}

func TestTrackedPaymentsSurviveRestart(t *testing.T) {
	node := newMockNode()
	dir := t.TempDir()
	svc, cleanup := newTestServiceWithWorkDir(t, node, dir)
	svc.trackPayment(&trackedPayment{PaymentHash: "ab", Started: time.Now()})
	cleanup()
	svc2, cleanup2 := newTestServiceWithWorkDir(t, node, dir)
	defer cleanup2()
	svc2.loadTracked()
	if _, ok := svc2.reconciler.tracked["ab"]; !ok {
		t.Fatal("ambiguous payment must still be tracked after restart")
	}
	svc2.untrack("ab")
	svc3, cleanup3 := newTestServiceWithWorkDir(t, node, dir)
	defer cleanup3()
	svc3.loadTracked()
	if len(svc3.reconciler.tracked) != 0 {
		t.Fatal("untracked payment must not come back")
	}
}

func TestTrackOrphanPendingHashesSettlesComplete(t *testing.T) {
	node := newMockNode()
	pre := []byte("0123456789abcdef0123456789abcdef")
	hash := sha256.Sum256(pre)
	node.pays = []*clngrpc.ListpaysPays{{
		PaymentHash: hash[:], Status: clngrpc.ListpaysPays_COMPLETE, Preimage: pre,
		AmountMsat: &clngrpc.Amount{Msat: 1000}, AmountSentMsat: &clngrpc.Amount{Msat: 1001},
	}}
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	pub := &capturePublisher{}
	svc.eventPublisher = pub
	svc.TrackOrphanPendingHashes(svc.ctx, []string{hexOf(hash[:]), "zz", "00"})
	if ev := pub.waitForEvent("nwc_lnclient_payment_sent", time.Second); ev == nil {
		t.Fatal("expected settle from orphan reconcile")
	}
}

func TestTrackOrphanPendingHashesTracksUnknown(t *testing.T) {
	node := newMockNode()
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	h := hexOf(bytes.Repeat([]byte{0xab}, 32))
	svc.TrackOrphanPendingHashes(svc.ctx, []string{h})
	svc.reconciler.mu.Lock()
	_, ok := svc.reconciler.tracked[h]
	svc.reconciler.mu.Unlock()
	if !ok {
		t.Fatal("unknown hash must stay tracked (budget held)")
	}
}

func TestResolvePaymentByHashDoesNotFreeWithoutEvidence(t *testing.T) {
	svc, cleanup := newTestService(t, newMockNode())
	defer cleanup()
	h := hexOf(bytes.Repeat([]byte{0xcd}, 32))
	res, err := svc.ResolvePaymentByHash(svc.ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if res["action"] != "tracked" {
		t.Fatalf("want tracked, got %v", res)
	}
}
