package greenlight

// Adversarial tests: a hostile / compromised hosted node (the operator is a
// third party in Greenlight's threat model) and hostile local inputs.
// Reproduce: go test ./lnclient/greenlight -run Adversarial -v

import (
	"context"
	"crypto/sha256"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/getAlby/hub/lnclient"
	"github.com/getAlby/hub/lnclient/cln/clngrpc"
)

func TestAdversarialNodeLiesAboutPaymentSuccess(t *testing.T) {
	node := newMockNode()
	node.xpayPreimage = []byte("this-is-not-the-right-preimage!!")
	var hash [32]byte
	copy(hash[:], []byte("0123456789abcdef0123456789abcdef"))
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	resp, err := svc.SendPaymentSync(makeTestInvoice(t, testInvoiceOpts{amountMsat: 1000, paymentHash: &hash}), nil)
	if resp != nil || !errors.Is(err, lnclient.ErrPaymentPending) {
		t.Fatalf("success with wrong preimage must stay pending, got resp=%v err=%v", resp, err)
	}
}

func TestAdversarialReconcileRejectsBadPreimage(t *testing.T) {
	node := newMockNode()
	pre := []byte("0123456789abcdef0123456789abcdef")
	bad := sha256.Sum256([]byte("other"))
	node.pays = []*clngrpc.ListpaysPays{{PaymentHash: bad[:], Status: clngrpc.ListpaysPays_COMPLETE, Preimage: pre}}
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	pub := &capturePublisher{}
	svc.eventPublisher = pub
	tp := &trackedPayment{PaymentHash: hexOf(bad[:]), Started: time.Now()}
	if svc.reconcileOnce(svc.ctx, tp, time.Now()) {
		t.Fatal("complete-with-bad-preimage must not be final")
	}
	if ev := pub.waitForEvent("nwc_lnclient_payment_sent", 200*time.Millisecond); ev != nil {
		t.Fatal("must not publish payment_sent")
	}
}

func TestAdversarialHostileBalancesDoNotOverflow(t *testing.T) {
	node := newMockNode()
	max := uint64(math.MaxUint64)
	node.fundsOverride = &clngrpc.ListfundsResponse{Outputs: []*clngrpc.ListfundsOutputs{
		{AmountMsat: &clngrpc.Amount{Msat: max}, Status: clngrpc.ListfundsOutputs_CONFIRMED},
		{AmountMsat: &clngrpc.Amount{Msat: max}, Status: clngrpc.ListfundsOutputs_CONFIRMED},
		{AmountMsat: nil},
	}}
	state := clngrpc.ChannelState_ChanneldNormal
	node.channelsOverride = &clngrpc.ListpeerchannelsResponse{Channels: []*clngrpc.ListpeerchannelsChannels{
		{State: state, PeerConnected: true, ToUsMsat: &clngrpc.Amount{Msat: max}, TotalMsat: &clngrpc.Amount{Msat: 1}, SpendableMsat: &clngrpc.Amount{Msat: max}, ReceivableMsat: &clngrpc.Amount{Msat: max}},
		{}, // all-nil channel
	}}
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	b, err := svc.GetBalances(context.Background(), true)
	if err != nil {
		return // rejecting hostile data is fine
	}
	if b.Onchain.SpendableSat < 0 || b.Onchain.TotalSat < 0 || b.Lightning.TotalSpendableMsat < 0 || b.Lightning.TotalReceivableMsat < 0 || b.Lightning.NextMaxSpendableMsat < 0 {
		t.Fatalf("hostile node produced negative balances: %+v", b)
	}
	if _, err := svc.ListChannels(context.Background()); err != nil {
		t.Logf("ListChannels rejected hostile data: %v", err)
	}
}

func TestAdversarialSlowlorisNodeBounded(t *testing.T) {
	node := newMockNode()
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	node.mu.Lock()
	node.getinfoDelay = time.Hour
	node.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := svc.GetInfo(ctx)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("slow node must be bounded by the caller deadline: err=%v after %v", err, time.Since(start))
	}
	// and the interceptor bounds calls without a caller deadline
	if d := rpcTimeoutFor("/cln.Node/Getinfo"); d <= 0 || d > time.Minute {
		t.Fatalf("default rpc timeout %v", d)
	}
}

func TestAdversarialHugeInvoiceDescriptionFromNode(t *testing.T) {
	// A node returning a 10 MB description in an invoice must not crash
	// mapping (grpc's 4 MB default receive cap rejects it before us).
	inv := &clngrpc.ListinvoicesInvoices{Description: strPtr(strings.Repeat("A", 10<<20)), PaymentHash: make([]byte, 32), Status: clngrpc.ListinvoicesInvoices_PAID}
	svc := &GreenlightService{}
	_, _ = svc.clnInvoiceToTransaction(context.Background(), inv)
}

func TestAdversarialGlcliArgsNoInjection(t *testing.T) {
	// glcli is always exec'd with argv (no shell); hostile data dirs or
	// network names must be rejected or passed as a single argument.
	for _, net := range []string{"regtest; rm -rf /", "$(id)", "../../etc"} {
		if _, err := ScheduleNodeURIForRegtest(t.TempDir(), net, "/bin/echo"); err == nil {
			t.Fatalf("non-regtest network %q accepted by dev override", net)
		}
	}
}

func strPtr(s string) *string { return &s }
