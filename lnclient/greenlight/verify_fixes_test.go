package greenlight

import (
	"context"
	"testing"
	"time"

	"github.com/getAlby/hub/lnclient/cln/clngrpc"
	"google.golang.org/grpc"
)

// Reverification gap: no prior test failed when deadlineInterceptor was
// disabled (mutation survived). This pins the per-call default deadline.
func TestDeadlineInterceptorAppliesContextDeadline(t *testing.T) {
	var got time.Duration
	var had bool
	inv := func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		d, ok := ctx.Deadline()
		had = ok
		got = time.Until(d)
		return nil
	}
	_ = deadlineInterceptor(context.Background(), "/cln.Node/Getinfo", nil, nil, nil, inv)
	if !had || got <= 0 || got > defaultRPCTimeout+time.Second {
		t.Fatalf("expected default deadline ~%v, had=%v got=%v", defaultRPCTimeout, had, got)
	}
	_ = deadlineInterceptor(context.Background(), "/cln.Node/Xpay", nil, nil, nil, inv)
	if !had || got <= defaultRPCTimeout && payRPCTimeout > defaultRPCTimeout {
		t.Fatalf("pay calls need the longer pay timeout, got %v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = deadlineInterceptor(ctx, "/cln.Node/Getinfo", nil, nil, nil, inv)
	if got > 2*time.Second {
		t.Fatalf("caller deadline must be preserved, got %v", got)
	}
}

func closedChan(id string, state clngrpc.ChannelState, status []string, toUs uint64) *clngrpc.ListpeerchannelsChannels {
	scid := "101x1x0"
	return &clngrpc.ListpeerchannelsChannels{
		ChannelId: mustHex(id), PeerId: mustHex("02f1d2e5f1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d"),
		FundingTxid: mustHex("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"), FundingOutnum: uint32Ptr(0),
		ShortChannelId: &scid, State: state, Status: status,
		ToUsMsat: &clngrpc.Amount{Msat: toUs}, TotalMsat: &clngrpc.Amount{Msat: toUs * 2},
		SpendableMsat: &clngrpc.Amount{Msat: 0}, ReceivableMsat: &clngrpc.Amount{Msat: 0},
	}
}

const (
	resolvedID = "aa01cd02ef030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e"
	pendingID  = "bb01cd02ef030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e"
)

// Seen on the reverify regtest bench: closed channels in ONCHAIN state with
// "All outputs resolved" were listed as offline channels with stale balances.
func TestListChannelsHidesResolvedClosedChannels(t *testing.T) {
	node := newMockNode()
	node.channelsOverride = &clngrpc.ListpeerchannelsResponse{Channels: []*clngrpc.ListpeerchannelsChannels{
		closedChan(resolvedID, clngrpc.ChannelState_Onchain, []string{"ONCHAIN:Tracking mutual close transaction", "ONCHAIN:All outputs resolved: waiting 79 more blocks before forgetting channel"}, 50_000_000),
		closedChan(pendingID, clngrpc.ChannelState_Onchain, []string{"ONCHAIN:Tracking our own unilateral close", "ONCHAIN:1 outputs unresolved: in 140 blocks will spend DELAYED_OUTPUT_TO_US"}, 70_000_000),
	}}
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	chs, err := svc.ListChannels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chs {
		if c.Id == resolvedID {
			t.Fatalf("resolved closed channel must not be listed")
		}
	}
	if len(chs) != 1 {
		t.Fatalf("unresolved closing channel should still be listed, got %d", len(chs))
	}
}

// Our own force close: the CSV-delayed to_local output is not a wallet
// output yet, so it must appear in pendingBalancesFromChannelClosures.
func TestOnchainBalanceCountsOwnUnilateralCloseAwaitingSweep(t *testing.T) {
	node := newMockNode()
	node.channelsOverride = &clngrpc.ListpeerchannelsResponse{Channels: []*clngrpc.ListpeerchannelsChannels{
		closedChan(resolvedID, clngrpc.ChannelState_Onchain, []string{"ONCHAIN:Tracking mutual close transaction", "ONCHAIN:All outputs resolved: waiting 79 more blocks before forgetting channel"}, 50_000_000),
		closedChan(pendingID, clngrpc.ChannelState_Onchain, []string{"ONCHAIN:Tracking our own unilateral close", "ONCHAIN:1 outputs unresolved: in 140 blocks will spend DELAYED_OUTPUT_TO_US"}, 70_000_000),
	}}
	node.fundsOverride = &clngrpc.ListfundsResponse{Channels: []*clngrpc.ListfundsChannels{
		{ChannelId: mustHex(resolvedID), PeerId: mustHex("02f1d2e5f1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d"), State: clngrpc.ChannelState_Onchain, OurAmountMsat: &clngrpc.Amount{Msat: 50_000_000}},
		{ChannelId: mustHex(pendingID), PeerId: mustHex("02f1d2e5f1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d"), State: clngrpc.ChannelState_Onchain, OurAmountMsat: &clngrpc.Amount{Msat: 70_000_000}},
	}}
	svc, cleanup := newTestService(t, node)
	defer cleanup()
	b, err := svc.GetOnchainBalance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b.PendingBalancesFromChannelClosuresSat != 70_000 {
		t.Fatalf("want 70000 sat pending from own unilateral close (resolved mutual close must not double count), got %d", b.PendingBalancesFromChannelClosuresSat)
	}
}
