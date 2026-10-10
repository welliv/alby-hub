package greenlight

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/lightningnetwork/lnd/lnwire"
	"github.com/lightningnetwork/lnd/zpay32"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/getAlby/hub/events"
	"github.com/getAlby/hub/lnclient"
	"github.com/getAlby/hub/lnclient/cln/clngrpc"
	"github.com/getAlby/hub/logger"
	"github.com/sirupsen/logrus"
)

// Every unary gRPC call to the hosted node gets a deadline. A remote node
// can wedge (Blockstream/greenlight#739); an RPC without a deadline would
// hang the calling hub goroutine (API handler, startup, NWC) forever.
const (
	defaultRPCTimeout = 30 * time.Second
	payRPCTimeout     = 3 * time.Minute
	// xpay/keysend retry window handed to the node; must be < payRPCTimeout
	payRetryForSeconds = 60
	// an ambiguous payment with no record on the node after this long is
	// considered never-started and is marked failed
	noRecordFailAfter = 10 * time.Minute
	reconcileInterval = 20 * time.Second
	// maximum sane amount: 21M BTC in msat
	maxAmountMsat = uint64(21_000_000) * 100_000_000 * 1000
)

func rpcTimeoutFor(method string) time.Duration {
	switch {
	case strings.HasSuffix(method, "/Xpay"), strings.HasSuffix(method, "/Pay"), strings.HasSuffix(method, "/KeySend"):
		return payRPCTimeout
	case strings.HasSuffix(method, "/WaitAnyInvoice"):
		return waitAnyInvoiceTimeoutDuration + 30*time.Second
	}
	return defaultRPCTimeout
}

// deadlineInterceptor adds a default deadline to calls that don't carry one.
func deadlineInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, rpcTimeoutFor(method))
		defer cancel()
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}

func chainParams(network string) (*chaincfg.Params, error) {
	switch network {
	case "bitcoin", "mainnet":
		return &chaincfg.MainNetParams, nil
	case "testnet", "testnet3":
		return &chaincfg.TestNet3Params, nil
	case "signet":
		return &chaincfg.SigNetParams, nil
	case "regtest":
		return &chaincfg.RegressionNetParams, nil
	}
	return nil, fmt.Errorf("unknown network %q", network)
}

// ErrInvalidInvoice marks a pre-flight rejection: nothing was sent.
var ErrInvalidInvoice = errors.New("invalid invoice")

// validateBolt11 rejects invoices before they reach the node/signer. The
// VLS signer rejects some malformed invoices (e.g. missing var_onion /
// payment_secret features) and a rejected signing request has been
// observed to freeze the hosted node, so anything the signer would refuse
// must be caught here.
func validateBolt11(payReq, network string, amountMsat *uint64, now time.Time) (*zpay32.Invoice, error) {
	params, err := chainParams(network)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInvoice, err)
	}
	inv, err := zpay32.Decode(strings.TrimSpace(payReq), params)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInvoice, err)
	}
	if inv.PaymentHash == nil {
		return nil, fmt.Errorf("%w: missing payment hash", ErrInvalidInvoice)
	}
	if inv.PaymentAddr.IsNone() {
		return nil, fmt.Errorf("%w: missing payment secret", ErrInvalidInvoice)
	}
	if inv.Features == nil {
		return nil, fmt.Errorf("%w: missing feature bits", ErrInvalidInvoice)
	}
	if !inv.Features.HasFeature(lnwire.TLVOnionPayloadOptional) {
		return nil, fmt.Errorf("%w: var_onion_optin feature not set", ErrInvalidInvoice)
	}
	if !inv.Features.HasFeature(lnwire.PaymentAddrOptional) {
		return nil, fmt.Errorf("%w: payment_secret feature not set", ErrInvalidInvoice)
	}
	if unk := inv.Features.UnknownRequiredFeatures(); len(unk) > 0 {
		return nil, fmt.Errorf("%w: unsupported required feature bits %v", ErrInvalidInvoice, unk)
	}
	if now.After(inv.Timestamp.Add(inv.Expiry())) {
		return nil, fmt.Errorf("%w: invoice expired", ErrInvalidInvoice)
	}
	var invAmt uint64
	if inv.MilliSat != nil {
		invAmt = uint64(*inv.MilliSat)
	}
	switch {
	case invAmt == 0 && (amountMsat == nil || *amountMsat == 0):
		return nil, fmt.Errorf("%w: amountless invoice requires an amount", ErrInvalidInvoice)
	case invAmt > 0 && amountMsat != nil && *amountMsat != invAmt:
		return nil, fmt.Errorf("%w: amount does not match invoice amount", ErrInvalidInvoice)
	}
	if invAmt > maxAmountMsat || (amountMsat != nil && *amountMsat > maxAmountMsat) {
		return nil, fmt.Errorf("%w: amount out of range", ErrInvalidInvoice)
	}
	return inv, nil
}

// isDefinitiveRPCFailure reports whether the error is a JSON-RPC error
// returned by the node itself (CLN wraps those as "Error code <n>"), i.e.
// the node processed the request and refused/failed it. Transport errors,
// deadlines and cancellations are ambiguous: the payment may be in flight.
func isDefinitiveRPCFailure(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled, codes.Aborted, codes.ResourceExhausted:
		return false
	}
	// two observed node-side error shapes: CLN JSON-RPC "Error code <n>"
	// and gl-plugin passthrough "RpcError { code: Some(<n>)" (live: xpay
	// 205 "no connection between source and destination").
	m := st.Message()
	return strings.Contains(m, "Error code") || strings.Contains(m, "RpcError { code: Some(")
}

// payOutcome is the aggregate state of all attempts for a payment.
type payOutcome int

const (
	outcomeNone payOutcome = iota
	outcomePending
	outcomeComplete
	outcomeFailed
)

func aggregatePays(pays []*clngrpc.ListpaysPays) (payOutcome, *clngrpc.ListpaysPays) {
	if len(pays) == 0 {
		return outcomeNone, nil
	}
	pending := false
	for _, p := range pays {
		switch p.Status {
		case clngrpc.ListpaysPays_COMPLETE:
			return outcomeComplete, p
		case clngrpc.ListpaysPays_PENDING:
			pending = true
		}
	}
	if pending {
		return outcomePending, nil
	}
	return outcomeFailed, pays[len(pays)-1]
}

func feeOf(sent, amt *clngrpc.Amount) uint64 {
	if sent == nil || amt == nil || sent.Msat < amt.Msat {
		return 0
	}
	return sent.Msat - amt.Msat
}

// trackedPayment is an outgoing payment whose outcome is unknown.
type trackedPayment struct {
	PaymentHash          string    `json:"payment_hash"`    // hash the hub recorded
	Label                string    `json:"label,omitempty"` // keysend: node label to find the pay by
	Invoice              string    `json:"invoice,omitempty"`
	AmountMsat           uint64    `json:"amount_msat"`
	Started              time.Time `json:"started"`
	RequestedPaymentHash string    `json:"requested_payment_hash,omitempty"` // keysend: synthetic hash recorded by the hub
}

type payReconciler struct {
	mu      sync.Mutex
	tracked map[string]*trackedPayment
}

func (g *GreenlightService) trackPayment(tp *trackedPayment) {
	g.reconciler.mu.Lock()
	defer g.reconciler.mu.Unlock()
	if g.reconciler.tracked == nil {
		g.reconciler.tracked = map[string]*trackedPayment{}
	}
	g.reconciler.tracked[tp.PaymentHash] = tp
	g.saveTrackedLocked()
}

const trackedPaymentsFile = "pending_payments.json"

// The tracked set is persisted so an ambiguous payment with no node record
// yet is still reconciled (and its budget eventually released or settled)
// after a hub restart.
func (g *GreenlightService) saveTrackedLocked() {
	if g.workDir == "" {
		return
	}
	list := make([]*trackedPayment, 0, len(g.reconciler.tracked))
	for _, tp := range g.reconciler.tracked {
		list = append(list, tp)
	}
	data, err := json.Marshal(list)
	if err != nil {
		return
	}
	p := filepath.Join(g.workDir, trackedPaymentsFile)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		logger.Logger.WithError(err).Error("greenlight: failed to persist pending payments")
		return
	}
	if err := os.Rename(tmp, p); err != nil {
		logger.Logger.WithError(err).Error("greenlight: failed to persist pending payments")
	}
}

func (g *GreenlightService) loadTracked() {
	if g.workDir == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(g.workDir, trackedPaymentsFile)) // #nosec G304 -- hub workdir
	if err != nil {
		return
	}
	var list []*trackedPayment
	if json.Unmarshal(data, &list) != nil {
		return
	}
	for _, tp := range list {
		if tp != nil && tp.PaymentHash != "" {
			g.trackPayment(tp)
		}
	}
}

func (g *GreenlightService) untrack(hash string) {
	g.reconciler.mu.Lock()
	delete(g.reconciler.tracked, hash)
	g.saveTrackedLocked()
	g.reconciler.mu.Unlock()
}

func (g *GreenlightService) listPaysFor(ctx context.Context, tp *trackedPayment) ([]*clngrpc.ListpaysPays, error) {
	if tp.Label != "" {
		resp, err := g.client.ListPays(ctx, &clngrpc.ListpaysRequest{})
		if err != nil {
			return nil, err
		}
		var out []*clngrpc.ListpaysPays
		for _, p := range resp.Pays {
			if p.Label != nil && *p.Label == tp.Label {
				out = append(out, p)
			}
		}
		return out, nil
	}
	h, err := hex.DecodeString(tp.PaymentHash)
	if err != nil {
		return nil, err
	}
	if len(h) != 32 {
		// cln-grpc unwraps Sha256::from_slice: never send a bad hash.
		return nil, fmt.Errorf("invalid payment hash length %d", len(h))
	}
	resp, err := g.client.ListPays(ctx, &clngrpc.ListpaysRequest{PaymentHash: h})
	if err != nil {
		return nil, err
	}
	return resp.Pays, nil
}

func (g *GreenlightService) publish(e *events.Event) {
	if g.eventPublisher != nil {
		g.eventPublisher.Publish(e)
	}
}

// reconcileOnce checks a tracked payment; returns true when it is final.
func (g *GreenlightService) reconcileOnce(ctx context.Context, tp *trackedPayment, now time.Time) bool {
	pays, err := g.listPaysFor(ctx, tp)
	if err != nil {
		logger.Logger.WithError(err).WithField("payment_hash", tp.PaymentHash).Debug("reconcile: listpays failed")
		return false
	}
	outcome, p := aggregatePays(pays)
	switch outcome {
	case outcomeComplete:
		g.publishPaymentSent(tp, p)
		return true
	case outcomeFailed:
		g.publishPaymentFailed(tp, "payment failed (reconciled from node)")
		return true
	case outcomeNone:
		if now.Sub(tp.Started) > noRecordFailAfter {
			g.publishPaymentFailed(tp, "payment never reached the node")
			return true
		}
	}
	return false
}

func (g *GreenlightService) publishPaymentSent(tp *trackedPayment, p *clngrpc.ListpaysPays) {
	tx := &lnclient.Transaction{
		Type:         "outgoing",
		Invoice:      tp.Invoice,
		Preimage:     hex.EncodeToString(p.Preimage),
		PaymentHash:  hex.EncodeToString(p.PaymentHash),
		AmountMsat:   i64(tp.AmountMsat),
		FeesPaidMsat: i64(feeOf(p.AmountSentMsat, p.AmountMsat)),
		Metadata:     lnclient.Metadata{},
	}
	if p.AmountMsat != nil {
		tx.AmountMsat = i64(p.AmountMsat.Msat)
	}
	if tp.RequestedPaymentHash != "" {
		tx.Metadata[lnclient.MetadataRequestedPaymentHash] = tp.RequestedPaymentHash
	}
	logger.Logger.WithFields(logrus.Fields{"payment_hash": tx.PaymentHash}).Info("greenlight: reconciled payment as sent")
	g.publish(&events.Event{Event: "nwc_lnclient_payment_sent", Properties: tx})
}

func (g *GreenlightService) publishPaymentFailed(tp *trackedPayment, reason string) {
	hash := tp.PaymentHash
	if tp.RequestedPaymentHash != "" {
		hash = tp.RequestedPaymentHash
	}
	logger.Logger.WithFields(logrus.Fields{"payment_hash": hash, "reason": reason}).Info("greenlight: reconciled payment as failed")
	g.publish(&events.Event{Event: "nwc_lnclient_payment_failed", Properties: &lnclient.PaymentFailedEventProperties{
		Transaction: &lnclient.Transaction{Type: "outgoing", PaymentHash: hash, Invoice: tp.Invoice, AmountMsat: i64(tp.AmountMsat)},
		Reason:      reason,
	}})
}

// runReconciler polls ambiguous payments until they are final, and on
// startup replays recent node payment history so a hub crash mid-payment
// (or an outage) cannot leave the hub's view diverged from the node.
func (g *GreenlightService) runReconciler(ctx context.Context) {
	g.loadTracked()
	g.startupReconcile(ctx)
	t := time.NewTicker(reconcileInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		g.reconciler.mu.Lock()
		list := make([]*trackedPayment, 0, len(g.reconciler.tracked))
		for _, tp := range g.reconciler.tracked {
			list = append(list, tp)
		}
		g.reconciler.mu.Unlock()
		for _, tp := range list {
			if g.reconcileOnce(ctx, tp, time.Now()) {
				g.untrack(tp.PaymentHash)
			}
		}
	}
}

// startupReconcile publishes sent events for completed node payments of
// the last 7 days (idempotent in the hub: already-settled payments are a
// no-op) and failed events for recent fully-failed ones (the hub only acts
// on its pending transactions). Pending pays are tracked until final.
func (g *GreenlightService) startupReconcile(ctx context.Context) {
	resp, err := g.client.ListPays(ctx, &clngrpc.ListpaysRequest{})
	if err != nil {
		logger.Logger.WithError(err).Warn("greenlight: startup payment reconciliation failed (will rely on polling)")
		return
	}
	byHash := map[string][]*clngrpc.ListpaysPays{}
	for _, p := range resp.Pays {
		if time.Since(time.Unix(int64(p.CreatedAt), 0)) > 7*24*time.Hour {
			continue
		}
		h := hex.EncodeToString(p.PaymentHash)
		byHash[h] = append(byHash[h], p)
	}
	for h, pays := range byHash {
		tp := &trackedPayment{PaymentHash: h, Started: time.Now()}
		if pays[0].Bolt11 != nil {
			tp.Invoice = *pays[0].Bolt11
		}
		if pays[0].Label != nil {
			if rh, ok := strings.CutPrefix(*pays[0].Label, keysendLabelPrefix); ok {
				if i := strings.LastIndex(rh, "-"); i > 0 {
					rh = rh[:i]
				}
				tp.RequestedPaymentHash = rh
			}
		}
		outcome, p := aggregatePays(pays)
		switch outcome {
		case outcomeComplete:
			g.publishPaymentSent(tp, p)
		case outcomeFailed:
			if time.Since(time.Unix(int64(pays[len(pays)-1].CreatedAt), 0)) < 24*time.Hour {
				g.publishPaymentFailed(tp, "payment failed (startup reconciliation)")
			}
		case outcomePending:
			g.trackPayment(tp)
		}
	}
}

const keysendLabelPrefix = "AlbyHub-keysend-"

// i64 converts msat/sat values to int64, clamping instead of wrapping (a
// malicious or buggy node must not be able to turn a huge uint64 into a
// negative amount in hub accounting).
func i64(u uint64) int64 {
	if u > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(u) // #nosec G115 -- bounded above
}
