package greenlight

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/getAlby/hub/events"
	"github.com/getAlby/hub/lnclient"
	"github.com/getAlby/hub/lnclient/cln/clngrpc"
	"github.com/getAlby/hub/logger"
	"github.com/getAlby/hub/nip47/models"
)

type Config struct {
	// CredsPath is the directory containing the extracted device credentials
	// (ca.pem, client.pem, client-key.pem) produced by extract_creds.py
	CredsPath string
	// NodeURI is the greenlight node gRPC endpoint, e.g. gl1<node_id>.gl.blckstrm.com:443
	NodeURI string
	// ServerName is the TLS SNI host. Defaults to the host of NodeURI.
	ServerName string
	Network    string
	// SignerDataDir is the signer's data directory. When set, hub backups
	// include it (covers the encrypted seed when signer and hub share a
	// filesystem). Optional; defaults to the hub workdir.
	SignerDataDir string
	// SignerStatusProvider returns the signer service's current state.
	// Nil in external-signer mode (the harness provides the signer).
	SignerStatusProvider func() SignerStatus
}

// SignerStatus carries the signer service's health state for surfacing
// in the node-status API (so a signer outage doesn't masquerade as a
// node outage).
type SignerStatus struct {
	Running   bool   `json:"running"`
	LastError string `json:"last_error,omitempty"`
}

type GreenlightService struct {
	ctx            context.Context
	cancel         context.CancelFunc
	conn           *grpc.ClientConn
	client         clngrpc.NodeClient
	eventPublisher events.EventPublisher
	pubkey         string
	config         Config
	workDir        string
	pumpRetryDelay time.Duration

	// node health watchdog state (see health.go)
	healthMtx              sync.RWMutex
	nodeHealthy            bool
	lastHealthError        string
	lastHealthCheck        time.Time
	lastInfo               *clngrpc.GetinfoResponse
	lastPumpCallStart      time.Time
	inPumpCall             bool
	healthCheckInterval    time.Duration
	healthCheckTimeout     time.Duration
	healthFailureThreshold int
	pumpStallThreshold     time.Duration

	reconciler payReconciler
}

func NewGreenlightService(ctx context.Context, eventPublisher events.EventPublisher, workDir string, config Config) (lnclient.LNClient, error) {
	if config.CredsPath == "" {
		return nil, errors.New("greenlight creds path not configured (GREENLIGHT_CREDS_PATH)")
	}
	if config.NodeURI == "" {
		return nil, errors.New("greenlight node URI not configured (GREENLIGHT_NODE_URI)")
	}

	// Normalize: glcli prints node URIs as https://gl1<id>.gl.blckstrm.com:443,
	// but the grpc dialer rejects a scheme and hostOf() needs bare host:port.
	// Strip the scheme defensively so both forms work.
	nodeURI := normalizeNodeURI(config.NodeURI)

	serverName := config.ServerName
	if serverName == "" {
		serverName = hostOf(nodeURI)
	}

	tlsConfig, err := loadTLSCredentials(config.CredsPath, serverName)
	if err != nil {
		return nil, fmt.Errorf("failed to load greenlight TLS credentials: %w", err)
	}

	creds := credentials.NewTLS(tlsConfig)

	conn, err := grpc.NewClient(nodeURI, grpc.WithTransportCredentials(creds), grpc.WithUnaryInterceptor(deadlineInterceptor))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to greenlight gRPC: %w", err)
	}

	client := clngrpc.NewNodeClient(conn)

	ctx, cancel := context.WithCancel(ctx)

	svc := &GreenlightService{
		ctx:            ctx,
		cancel:         cancel,
		conn:           conn,
		client:         client,
		eventPublisher: eventPublisher,
		config:         config,
		workDir:        workDir,
	}

	// Fail fast on a truly unreachable node — but never lock the user out
	// of their hub: a *frozen* node (Blockstream/greenlight#739 — hsmd
	// queue wedge) responds to nothing, so this probe times out even
	// though the node exists. Aborting startup on that would leave the
	// user unable to reach their wallet, backups, or apps exactly when
	// the node is broken. Instead: start degraded and let the health
	// watchdog (health.go) report the node state and keep probing.
	logger.Logger.Info("Testing greenlight gRPC connection")
	connCtx, connCancel := context.WithTimeout(ctx, 15*time.Second)
	info, err := svc.GetInfo(connCtx)
	connCancel()
	if err != nil {
		logger.Logger.WithError(err).Warn("greenlight node unreachable at boot — starting degraded; the health watchdog will keep probing (a frozen node may need a Blockstream restart)")
	} else {
		svc.pubkey = info.Pubkey
		if svc.config.Network == "" {
			svc.config.Network = info.Network
		}
		logger.Logger.Info("Successfully connected to greenlight via gRPC")
	}

	// NOTE: no channel-state subscription. The GL node server leaves all six
	// cln.Node Subscribe* stream RPCs unimplemented (they panic the gl-plugin
	// process, killing the node), so nwc_channel_ready/closed are not emitted
	// by this backend. Incoming payments are covered by waitForInvoices (pump)
	// and streamIncoming (keysends) below.
	go svc.waitForInvoices(ctx)
	go svc.streamIncoming(ctx)
	go svc.runReconciler(ctx)

	// node health watchdog (health.go): detects a wedged/frozen node even
	// when the node itself stops responding — GetNodeStatus then serves the
	// cached verdict instead of hanging on a live RPC.
	svc.healthCheckInterval = defaultHealthCheckInterval
	svc.healthCheckTimeout = defaultHealthCheckTimeout
	svc.healthFailureThreshold = defaultHealthFailureThreshold
	svc.pumpStallThreshold = defaultPumpStallThreshold
	svc.startHealthWatchdog(ctx)

	return svc, nil
}

func normalizeNodeURI(uri string) string {
	uri = strings.TrimPrefix(uri, "https://")
	return strings.TrimPrefix(uri, "http://")
}

func hostOf(uri string) string {
	host := uri
	if idx := strings.LastIndex(host, "@"); idx != -1 {
		host = host[idx+1:]
	}
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return host
}

func loadTLSCredentials(credsPath string, serverName string) (*tls.Config, error) {
	certPath := filepath.Join(credsPath, "ca.pem")
	clientCertPath := filepath.Join(credsPath, "client.pem")
	clientKeyPath := filepath.Join(credsPath, "client-key.pem")

	serverCA, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read server CA cert: %w", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(serverCA) {
		return nil, fmt.Errorf("failed to add server CA cert to pool")
	}

	clientCert, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load client cert/key: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      certPool,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func (g *GreenlightService) SendPaymentSync(payReq string, amount *uint64) (*lnclient.PayInvoiceResponse, error) {
	inv, err := validateBolt11(payReq, g.config.Network, amount, time.Now())
	if err != nil {
		return nil, err
	}
	paymentHash := hex.EncodeToString(inv.PaymentHash[:])
	logger.Logger.WithFields(logrus.Fields{"payment_hash": paymentHash, "amount": amount}).Debug("Send Payment Sync")

	var amountMsat *clngrpc.Amount
	if amount != nil && (inv.MilliSat == nil || *inv.MilliSat == 0) {
		amountMsat = &clngrpc.Amount{Msat: *amount}
	}
	retryFor := uint32(payRetryForSeconds)
	ctx, cancel := context.WithTimeout(g.ctx, payRPCTimeout)
	defer cancel()
	resp, err := g.client.Xpay(ctx, &clngrpc.XpayRequest{Invstring: payReq, AmountMsat: amountMsat, RetryFor: &retryFor})
	if err == nil {
		return &lnclient.PayInvoiceResponse{
			Preimage: hex.EncodeToString(resp.PaymentPreimage),
			FeeMsat:  feeOf(resp.AmountSentMsat, resp.AmountMsat),
		}, nil
	}
	tp := &trackedPayment{PaymentHash: paymentHash, Invoice: payReq, Started: time.Now()}
	if inv.MilliSat != nil {
		tp.AmountMsat = uint64(*inv.MilliSat)
	} else if amount != nil {
		tp.AmountMsat = *amount
	}
	return nil, g.resolvePayError(err, tp, "xpay")
}

// resolvePayError decides whether a pay/keysend error is a confirmed
// failure or an ambiguous outcome. It consults the node's payment records:
// only a definitive node-side failure with no pending/complete attempt is
// reported as failure; everything else is ErrPaymentPending + tracking.
func (g *GreenlightService) resolvePayError(payErr error, tp *trackedPayment, op string) error {
	logger.Logger.WithError(payErr).WithField("payment_hash", tp.PaymentHash).Warn(op + " returned an error, checking node payment records")
	ctx, cancel := context.WithTimeout(g.ctx, defaultRPCTimeout)
	defer cancel()
	pays, lerr := g.listPaysFor(ctx, tp)
	if lerr == nil {
		outcome, _ := aggregatePays(pays)
		switch outcome {
		case outcomeComplete:
			// succeeded despite the error: let the reconciler publish the
			// sent event with the real preimage
			g.trackPayment(tp)
			return fmt.Errorf("%w: %s reported an error but the node shows the payment complete", lnclient.ErrPaymentPending, op)
		case outcomePending:
			g.trackPayment(tp)
			return fmt.Errorf("%w: %v", lnclient.ErrPaymentPending, payErr)
		case outcomeFailed, outcomeNone:
			if isDefinitiveRPCFailure(payErr) {
				return fmt.Errorf("%s failed: %w", op, payErr)
			}
		}
	}
	g.trackPayment(tp)
	return fmt.Errorf("%w: %v", lnclient.ErrPaymentPending, payErr)
}

func (g *GreenlightService) SendKeysend(amount uint64, destination string, customRecords []lnclient.TLVRecord, preimage string) (*lnclient.PayKeysendResponse, error) {
	logger.Logger.WithFields(logrus.Fields{
		"amount":      amount,
		"destination": destination,
	}).Debug("Send Keysend")

	Destination, err := decodePubkey(destination)
	if err != nil || len(Destination) != 33 {
		return nil, fmt.Errorf("invalid keysend destination pubkey")
	}
	if amount == 0 || amount > maxAmountMsat {
		return nil, fmt.Errorf("invalid keysend amount")
	}

	// label lets us find this exact keysend on the node even though CLN
	// derives its own preimage/hash (the hub recorded a synthetic hash).
	requestedHash := ""
	if pre, err := hex.DecodeString(preimage); err == nil && len(pre) == 32 {
		h := sha256.Sum256(pre)
		requestedHash = hex.EncodeToString(h[:])
	}
	label := keysendLabelPrefix + requestedHash + "-" + uuid.NewString()[:8]
	retryFor := uint32(payRetryForSeconds)
	req := &clngrpc.KeysendRequest{
		Destination: Destination,
		AmountMsat:  &clngrpc.Amount{Msat: amount},
		Label:       &label,
		RetryFor:    &retryFor,
	}

	if len(customRecords) > 0 {
		Extratlvs := clngrpc.TlvStream{}
		for _, record := range customRecords {
			valueBytes, err := hex.DecodeString(record.Value)
			if err != nil {
				return nil, fmt.Errorf("could not decode TLV value to bytes")
			}
			Extratlvs.Entries = append(Extratlvs.Entries, &clngrpc.TlvEntry{Type: record.Type, Value: valueBytes})
		}
		req.Extratlvs = &Extratlvs
	}

	ctx, cancel := context.WithTimeout(g.ctx, payRPCTimeout)
	defer cancel()
	resp, err := g.client.KeySend(ctx, req)
	if err != nil {
		tp := &trackedPayment{PaymentHash: requestedHash, Label: label, AmountMsat: amount, Started: time.Now(), RequestedPaymentHash: requestedHash}
		if requestedHash == "" {
			tp.PaymentHash = label
		}
		return nil, g.resolvePayError(err, tp, "keysend")
	}
	if resp == nil {
		return nil, fmt.Errorf("%w: empty keysend response", lnclient.ErrPaymentPending)
	}
	// Greenlight (CLN-family) derives its own preimage server-side — the
	// caller-supplied preimage cannot be honored via the gl-client, so
	// report the actual preimage and payment hash from the response.
	return &lnclient.PayKeysendResponse{
		FeeMsat:     feeOf(resp.AmountSentMsat, resp.AmountMsat),
		Preimage:    hex.EncodeToString(resp.PaymentPreimage),
		PaymentHash: hex.EncodeToString(resp.PaymentHash),
	}, nil
}

func (g *GreenlightService) GetPubkey() string {
	return g.pubkey
}

func (g *GreenlightService) GetInfo(ctx context.Context) (*lnclient.NodeInfo, error) {
	resp, err := g.client.Getinfo(ctx, &clngrpc.GetinfoRequest{})
	if err != nil {
		return nil, fmt.Errorf("getinfo failed: %w", err)
	}

	return &lnclient.NodeInfo{
		Alias:       resp.GetAlias(),
		Color:       hex.EncodeToString(resp.Color),
		Pubkey:      hex.EncodeToString(resp.Id),
		Network:     resp.Network,
		BlockHeight: resp.Blockheight,
		BlockHash:   "",
	}, nil
}

func (g *GreenlightService) MakeInvoice(ctx context.Context, amountMsat int64, description string, descriptionHash string, expiry int64, throughNodePubkey *string) (*lnclient.Transaction, error) {
	logger.Logger.WithFields(logrus.Fields{
		"amount":              amountMsat,
		"description":         description,
		"description_hash":    descriptionHash,
		"expiry":              expiry,
		"through_node_pubkey": throughNodePubkey,
	}).Debug("Make Invoice")

	if amountMsat < 0 || expiry < 0 {
		return nil, fmt.Errorf("invalid amount or expiry")
	}
	label := "AlbyHub-" + uuid.NewString()

	var deschashonly bool
	if descriptionHash != "" {
		if description == "" {
			return nil, fmt.Errorf("must have description when using description_hash")
		}
		myDescriptionHash := sha256.Sum256([]byte(description))
		if descriptionHash != hex.EncodeToString(myDescriptionHash[:]) {
			return nil, fmt.Errorf("description_hash does not match description")
		}
		deschashonly = true
	}

	if err := validateInvoiceDescription(description, deschashonly); err != nil {
		return nil, err
	}

	if expiry == 0 {
		expiry = lnclient.DEFAULT_INVOICE_EXPIRY
	}
	myExpiry := uint64(expiry)

	// LDK equivalent: ReceiveViaJitChannel when the amount exceeds inbound.
	// Greenlight's gl-plugin already negotiates LSPS2 in LspInvoice; we only
	// call it when inbound is short. No LSP peer → fall back to Invoice.
	if amountMsat > 0 && throughNodePubkey == nil {
		inbound := g.maxReceivableMsat(ctx)
		if amountMsat > inbound {
			jit, jitErr := g.lspInvoice(ctx, uint64(amountMsat), description, label)
			if jitErr == nil {
				expiresAt := int64(jit.ExpiresAt)
				logger.Logger.WithFields(logrus.Fields{
					"opening_fee_msat": jit.OpeningFeeMsat,
					"inbound_msat":     inbound,
					"amount_msat":      amountMsat,
				}).Info("greenlight JIT invoice via LspInvoice")
				return &lnclient.Transaction{
					Type:            "incoming",
					Invoice:         jit.Bolt11,
					Description:     description,
					DescriptionHash: descriptionHash,
					Preimage:        "",
					PaymentHash:     hex.EncodeToString(jit.PaymentHash),
					AmountMsat:      amountMsat,
					FeesPaidMsat:    i64(jit.OpeningFeeMsat),
					CreatedAt:       time.Now().Unix(),
					ExpiresAt:       &expiresAt,
					SettledAt:       nil,
					Metadata:        lnclient.Metadata{},
					SettleDeadline:  nil,
				}, nil
			}
			logJitFallback(jitErr, amountMsat, inbound)
			// Node unreachable: say so instead of blaming liquidity
			// (inbound reads as 0 when ListPeerChannels itself failed).
			if isNodeUnreachable(jitErr) {
				return nil, fmt.Errorf("greenlight node unreachable, cannot create invoice: %w", jitErr)
			}
			if g.eventPublisher != nil {
				g.eventPublisher.Publish(&events.Event{
					Event: "nwc_incoming_liquidity_required",
					Properties: map[string]interface{}{
						"node_type": "GREENLIGHT",
					},
				})
			}
			// A regular invoice above inbound capacity is unpayable: fail
			// clearly instead of handing the payer a dead invoice.
			return nil, fmt.Errorf("cannot receive %d sats: inbound liquidity is %d sats and the LSP could not open a channel (%s)",
				amountMsat/1000, inbound/1000, lspErrorReason(jitErr))
		}
	}

	Amount := clngrpc.AmountOrAny{
		Value: &clngrpc.AmountOrAny_Amount{Amount: &clngrpc.Amount{Msat: uint64(amountMsat)}}}
	// amount 0 is often used for "any" amount but CLN doesn't support 0 directly
	if amountMsat == 0 {
		Amount = clngrpc.AmountOrAny{
			Value: &clngrpc.AmountOrAny_Any{Any: true}}
	}

	preimage, err := GeneratePreimage()
	if err != nil {
		return nil, err
	}

	req := &clngrpc.InvoiceRequest{
		Description:  description,
		Label:        label,
		Preimage:     preimage,
		Expiry:       &myExpiry,
		Deschashonly: &deschashonly,
		AmountMsat:   &Amount,
	}

	Exposeprivatechannels := []string{}

	if throughNodePubkey == nil {
		// Live testnet: CLN on Greenlight added no route hints for the
		// node's private channels, making regular invoices unpayable
		// ("Unknown destination node"). Expose all usable private channels.
		if lpc, err := g.client.ListPeerChannels(ctx, &clngrpc.ListpeerchannelsRequest{}); err == nil {
			for _, ch := range lpc.Channels {
				if ch == nil || ch.State != clngrpc.ChannelState_ChanneldNormal || !ch.PeerConnected {
					continue
				}
				if ch.ShortChannelId != nil {
					Exposeprivatechannels = append(Exposeprivatechannels, *ch.ShortChannelId)
				} else if ch.Alias != nil && ch.Alias.Remote != nil {
					Exposeprivatechannels = append(Exposeprivatechannels, *ch.Alias.Remote)
				}
			}
		}
	}

	if throughNodePubkey != nil {
		throughNodePubkeyBytes, err := decodePubkey(*throughNodePubkey)
		if err != nil {
			return nil, err
		}
		lpc, err := g.client.ListPeerChannels(ctx, &clngrpc.ListpeerchannelsRequest{
			Id: throughNodePubkeyBytes,
		})
		if err != nil {
			logger.Logger.WithError(err).Error("listpeerchannels failed")
			return nil, fmt.Errorf("listpeerchannels failed")
		}

		for _, channel := range lpc.Channels {
			if channel.ShortChannelId != nil {
				Exposeprivatechannels = append(Exposeprivatechannels, *channel.ShortChannelId)
				continue
			}
			if channel.Alias != nil {
				if channel.Alias.Remote != nil {
					Exposeprivatechannels = append(Exposeprivatechannels, *channel.Alias.Remote)
				}
			}
		}
	}

	if len(Exposeprivatechannels) > 0 {
		req.Exposeprivatechannels = Exposeprivatechannels
	}

	resp, err := g.client.Invoice(ctx, req)
	if err != nil {
		logger.Logger.WithError(err).Error("invoice failed")
		return nil, fmt.Errorf("invoice failed: %w", err)
	}

	expiresAt := int64(resp.ExpiresAt)

	transaction := &lnclient.Transaction{
		Type:            "incoming",
		Invoice:         resp.Bolt11,
		Description:     description,
		DescriptionHash: descriptionHash,
		Preimage:        hex.EncodeToString(preimage),
		PaymentHash:     hex.EncodeToString(resp.PaymentHash),
		AmountMsat:      amountMsat,
		FeesPaidMsat:    0,
		CreatedAt:       time.Now().Unix(),
		ExpiresAt:       &expiresAt,
		SettledAt:       nil,
		Metadata:        lnclient.Metadata{},
		SettleDeadline:  nil,
	}

	return transaction, nil
}

// Hold invoices are not supported by Greenlight hosted nodes (no plugins).
func (g *GreenlightService) MakeHoldInvoice(ctx context.Context, amountMsat int64, description string, descriptionHash string, expiry int64, paymentHash string, minCltvExpiryDelta *uint64) (*lnclient.Transaction, error) {
	return nil, errors.New("hold invoices are not supported by the greenlight backend")
}

func (g *GreenlightService) SettleHoldInvoice(ctx context.Context, preimage string) error {
	return errors.New("hold invoices are not supported by the greenlight backend")
}

func (g *GreenlightService) CancelHoldInvoice(ctx context.Context, paymentHash string) error {
	return errors.New("hold invoices are not supported by the greenlight backend")
}

func (g *GreenlightService) LookupInvoice(ctx context.Context, paymentHash string) (*lnclient.Transaction, error) {
	logger.Logger.WithFields(logrus.Fields{
		"paymentHash": paymentHash,
	}).Debug("Lookup Invoice")

	paymentHashBytes, err := hex.DecodeString(paymentHash)
	if err != nil {
		logger.Logger.WithError(err).Error("failed to decode payment hash")
		return nil, fmt.Errorf("failed to decode payment hash: %w", err)
	}
	req := &clngrpc.ListinvoicesRequest{PaymentHash: paymentHashBytes}

	resp, err := g.client.ListInvoices(ctx, req)
	if err != nil {
		logger.Logger.WithError(err).Error("listinvoices failed")
		return nil, fmt.Errorf("listinvoices failed: %w", err)
	}
	if len(resp.Invoices) == 0 {
		return nil, fmt.Errorf("invoice not found")
	}

	transaction, err := g.clnInvoiceToTransaction(ctx, resp.Invoices[0])
	if err != nil {
		logger.Logger.WithError(err).Error("failed to convert invoice to transaction")
		return nil, fmt.Errorf("failed to convert invoice to transaction: %w", err)
	}

	return transaction, nil
}

func (g *GreenlightService) ListOnchainTransactions(ctx context.Context) ([]lnclient.OnchainTransaction, error) {
	account := "wallet"
	req := &clngrpc.BkprlistaccounteventsRequest{
		Account: &account,
	}
	bkpr, err := g.client.BkprListAccountEvents(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("bkprlistaccountevents failed: %w", err)
	}

	infoResp, infoErr := g.client.Getinfo(ctx, &clngrpc.GetinfoRequest{})
	if infoErr != nil {
		logger.Logger.WithError(infoErr).Error("getinfo failed")
		return nil, infoErr
	}

	blockheight := infoResp.Blockheight

	transactions := make([]lnclient.OnchainTransaction, 0)

	for _, event := range bkpr.Events {
		if event.ItemType != clngrpc.BkprlistaccounteventsEvents_CHAIN {
			continue
		}
		transactionType := "incoming"
		AmountSat := sat(event.CreditMsat)
		debitSat := sat(event.DebitMsat)
		if debitSat > 0 {
			transactionType = "outgoing"
			AmountSat = debitSat
		}

		numConfirmations := uint32(0)
		if event.Blockheight != nil && blockheight >= *event.Blockheight {
			numConfirmations = blockheight - *event.Blockheight
		}

		TxIdHex := hex.EncodeToString(event.Txid)

		transactions = append(transactions, lnclient.OnchainTransaction{
			AmountSat:        AmountSat,
			CreatedAt:        uint64(event.Timestamp),
			State:            "confirmed",
			Type:             transactionType,
			NumConfirmations: numConfirmations,
			TxId:             TxIdHex,
		})
	}

	slices.Reverse(transactions)

	sort.SliceStable(transactions, func(i, j int) bool {
		return transactions[i].CreatedAt > transactions[j].CreatedAt
	})

	return transactions, nil
}

func (g *GreenlightService) Shutdown() error {
	logger.Logger.Info("Cancelling greenlight context")
	g.cancel()

	logger.Logger.Info("Closing gRPC connections")
	if g.conn != nil {
		if err := g.conn.Close(); err != nil {
			logger.Logger.WithError(err).Error("Failed to close greenlight gRPC connection")
		}
	}

	logger.Logger.Info("greenlight backend shutdown complete")
	return nil
}

func (g *GreenlightService) ListChannels(ctx context.Context) ([]lnclient.Channel, error) {
	resp, err := g.client.ListPeerChannels(ctx, &clngrpc.ListpeerchannelsRequest{})
	if err != nil {
		logger.Logger.WithError(err).Error("listpeerchannels failed")
		return nil, err
	}

	infoResp, infoErr := g.client.Getinfo(ctx, &clngrpc.GetinfoRequest{})
	if infoErr != nil {
		logger.Logger.WithError(infoErr).Error("getinfo failed")
		return nil, infoErr
	}

	blockheight := infoResp.Blockheight

	reChanHeight := regexp.MustCompile(`(\d+)x.*`)

	channels := []lnclient.Channel{}

	for _, channel := range resp.Channels {
		if channel == nil {
			continue
		}

		var errorStrings []string

		// We could check the funding-confirms config but it's only for remote openers
		// In reality channels often confirm with 3 or 6 confirmations
		ConfirmationsRequired := uint32(6)
		if isConfirmedState(channel.State) {
			ConfirmationsRequired = 0
		} else if isOpeningState(channel.State) {
			confRequired, errStr := confirmationsRequiredFromStatus(channel.Status)
			if errStr != nil {
				logger.Logger.Error(*errStr)
				errorStrings = append(errorStrings, *errStr)
			} else {
				ConfirmationsRequired = confRequired
			}

		} else {
			errStr := fmt.Sprintf("unexpected clngrpc.ChannelState: %#v", channel.State)
			logger.Logger.Error(errStr)
			errorStrings = append(errorStrings, errStr)
		}

		var chanBlock *uint32
		if channel.ShortChannelId != nil {
			match := reChanHeight.FindStringSubmatch(*channel.ShortChannelId)
			if len(match) > 1 {
				num, err := strconv.Atoi(match[1])
				if err != nil {
					errStr := fmt.Sprintf("Error converting number: %v", err)
					logger.Logger.Error(errStr)
					errorStrings = append(errorStrings, errStr)
				}
				num32 := uint32(num)
				chanBlock = &num32
			}
		}

		var Confirmations uint32
		if chanBlock != nil {
			if blockheight >= *chanBlock {
				Confirmations = (blockheight - *chanBlock) + 1
			} else {
				Confirmations = 0
			}
		} else {
			Confirmations = 0
		}

		isActive := channel.State == clngrpc.ChannelState_ChanneldNormal && channel.PeerConnected

		var Error *string
		if len(errorStrings) > 0 {
			combined := strings.Join(errorStrings, "; ")
			Error = &combined
		}

		LocalBalance := msatInt64(channel.ToUsMsat)
		TotalBalance := msatInt64(channel.TotalMsat)
		RemoteBalance := int64(0)
		if TotalBalance >= LocalBalance {
			RemoteBalance = TotalBalance - LocalBalance
		}

		channels = append(channels, lnclient.Channel{
			LocalBalanceMsat:                    LocalBalance,
			LocalSpendableBalanceMsat:           msatInt64(channel.SpendableMsat),
			RemoteBalanceMsat:                   RemoteBalance,
			Id:                                  hex.EncodeToString(channel.ChannelId),
			RemotePubkey:                        hex.EncodeToString(channel.PeerId),
			FundingTxId:                         hex.EncodeToString(channel.FundingTxid),
			FundingTxVout:                       channel.GetFundingOutnum(),
			Active:                              isActive,
			Public:                              !channel.GetPrivate(),
			InternalChannel:                     channel,
			Confirmations:                       &Confirmations,
			ConfirmationsRequired:               &ConfirmationsRequired,
			ForwardingFeeBaseMsat:               localFeeBaseMsat(channel),
			ForwardingFeeProportionalMillionths: localFeePPM(channel),
			UnspendablePunishmentReserveSat:     sat(channel.OurReserveMsat),
			CounterpartyUnspendablePunishmentReserveSat: sat(channel.TheirReserveMsat),
			Error:      Error,
			IsOutbound: channel.GetOpener() == clngrpc.ChannelSide_LOCAL,
		})
	}
	return channels, nil
}

func (g *GreenlightService) GetNodeConnectionInfo(ctx context.Context) (*lnclient.NodeConnectionInfo, error) {
	resp, err := g.client.Getinfo(ctx, &clngrpc.GetinfoRequest{})
	if err != nil {
		return nil, fmt.Errorf("getinfo failed: %w", err)
	}

	var (
		ipv4  *clngrpc.GetinfoAddress
		ipv6  *clngrpc.GetinfoAddress
		torv3 *clngrpc.GetinfoAddress
	)

	for _, addr := range resp.Address {
		if addr == nil {
			continue
		}

		switch addr.ItemType {
		case clngrpc.GetinfoAddress_IPV4:
			if ipv4 == nil {
				ipv4 = addr
			}
		case clngrpc.GetinfoAddress_IPV6:
			if ipv6 == nil {
				ipv6 = addr
			}
		case clngrpc.GetinfoAddress_TORV3:
			if torv3 == nil {
				torv3 = addr
			}
		}
	}

	var selected *clngrpc.GetinfoAddress
	switch {
	case ipv4 != nil:
		selected = ipv4
	case ipv6 != nil:
		selected = ipv6
	case torv3 != nil:
		selected = torv3
	default:
		addr := "not announced"
		selected = &clngrpc.GetinfoAddress{
			Address: &addr,
			Port:    0,
		}
	}

	return &lnclient.NodeConnectionInfo{
		Pubkey:  hex.EncodeToString(resp.Id),
		Address: selected.GetAddress(),
		Port:    int(selected.Port),
	}, nil
}

func (g *GreenlightService) GetNodeStatus(ctx context.Context) (*lnclient.NodeStatus, error) {
	// Serve the watchdog's cached state: a live Getinfo here would hang
	// when the node is frozen (the API must keep answering with an
	// "unhealthy" verdict, not block).
	g.healthMtx.RLock()
	cacheWarm := !g.lastHealthCheck.IsZero()
	g.healthMtx.RUnlock()
	if !cacheWarm {
		// cold cache (first seconds after startup): warm it with one
		// bounded probe — never more than the health timeout
		_, _ = g.runHealthCheck(ctx)
	}

	g.healthMtx.RLock()
	healthy := g.nodeHealthy
	lastErr := g.lastHealthError
	lastCheck := g.lastHealthCheck
	info := g.lastInfo
	g.healthMtx.RUnlock()

	hs := nodeHealthStatus{
		Healthy:       healthy,
		NodeConnected: healthy,
		LastCheckAt:   lastCheck.Unix(),
		LastError:     lastErr,
	}
	if g.config.SignerStatusProvider != nil {
		hs.Signer = g.config.SignerStatusProvider()
	}

	// One rule, mirrored in InternalNodeStatus so the UI never shows
	// "healthy" next to "not ready" without saying why.
	ready := hs.NodeConnected
	switch {
	case !hs.NodeConnected:
		hs.NotReadyReason = "node unreachable"
	case g.config.SignerStatusProvider != nil && !hs.Signer.Running:
		ready = false
		hs.NotReadyReason = "signer not running"
	case info != nil && (info.WarningBitcoindSync != nil || info.WarningLightningdSync != nil):
		ready = false
		hs.Syncing = true
		hs.NotReadyReason = "node syncing"
	}
	hs.Ready = ready

	return &lnclient.NodeStatus{
		IsReady:            ready,
		InternalNodeStatus: hs,
	}, nil
}

func (g *GreenlightService) ConnectPeer(ctx context.Context, connectPeerRequest *lnclient.ConnectPeerRequest) error {
	logger.Logger.WithFields(logrus.Fields{
		"connectPeerRequest": connectPeerRequest,
	}).Debug("Connecting to Peer")

	// CLN gRPC's ConnectRequest uses a oneof for host/port, so we can't
	// safely set both via the generated Go struct. Send the socket address
	// as the host field instead; CLN will parse host:port itself.
	socket := fmt.Sprintf("%s:%d", connectPeerRequest.Address, connectPeerRequest.Port)
	req := &clngrpc.ConnectRequest{
		Id:   connectPeerRequest.Pubkey,
		Host: &socket,
	}

	_, err := g.client.ConnectPeer(ctx, req)
	if err != nil {
		logger.Logger.WithError(err).Error("Failed to connect peer")
		return err
	}

	return nil
}

func (g *GreenlightService) OpenChannel(ctx context.Context, openChannelRequest *lnclient.OpenChannelRequest) (*lnclient.OpenChannelResponse, error) {
	logger.Logger.WithFields(logrus.Fields{
		"openChannelRequest": openChannelRequest,
	}).Debug("Open Channel")

	Amount := clngrpc.AmountOrAll{Value: &clngrpc.AmountOrAll_Amount{
		Amount: &clngrpc.Amount{Msat: uint64(openChannelRequest.AmountSats) * 1000},
	}}

	Id, err := decodePubkey(openChannelRequest.Pubkey)
	if err != nil {
		return nil, fmt.Errorf("could not convert Pubkey to bytes")
	}

	req := &clngrpc.FundchannelRequest{
		Amount:   &Amount,
		Announce: &openChannelRequest.Public,
		Id:       Id,
	}
	resp, err := g.client.FundChannel(ctx, req)
	if err != nil {
		logger.Logger.WithError(err).Error("fundchannel failed")
		return nil, fmt.Errorf("fundchannel failed: %w", err)
	}

	if resp == nil {
		return nil, fmt.Errorf("empty fundchannel response")
	}

	FundingTxId := hex.EncodeToString(resp.Txid)

	return &lnclient.OpenChannelResponse{
		FundingTxId: FundingTxId,
	}, nil
}

func (g *GreenlightService) CloseChannel(ctx context.Context, closeChannelRequest *lnclient.CloseChannelRequest) error {
	logger.Logger.WithFields(logrus.Fields{
		"closeChannelRequest": closeChannelRequest,
	}).Debug("Closing Channel")

	req := &clngrpc.CloseRequest{
		Id: closeChannelRequest.ChannelId,
	}

	if closeChannelRequest.Force {
		// There is no force option in CLN, only a Unilateraltimeout after which the channel will be force closed
		// 0 means waiting forever so we choose 1 second
		timeout := uint32(1)
		req.Unilateraltimeout = &timeout
	}

	_, err := g.client.Close(ctx, req)
	if err != nil {
		logger.Logger.WithError(err).Error("Failed to close channel")
		return fmt.Errorf("close failed: %w", err)
	}

	return nil
}

func (g *GreenlightService) UpdateChannel(ctx context.Context, updateChannelRequest *lnclient.UpdateChannelRequest) error {
	logger.Logger.WithFields(logrus.Fields{
		"updateChannelRequest": updateChannelRequest,
	}).Debug("Updating Channel")

	req := &clngrpc.SetchannelRequest{
		Id:      updateChannelRequest.ChannelId,
		Feebase: &clngrpc.Amount{Msat: uint64(updateChannelRequest.ForwardingFeeBaseMsat)},
		Feeppm:  &updateChannelRequest.ForwardingFeeProportionalMillionths,
	}

	resp, err := g.client.SetChannel(ctx, req)
	if err != nil {
		logger.Logger.WithError(err).Error("setchannel failed")
		return fmt.Errorf("setchannel failed: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("setchannel result empty")
	}

	return nil
}

func (g *GreenlightService) DisconnectPeer(ctx context.Context, peerId string) error {
	logger.Logger.WithFields(logrus.Fields{
		"peerId": peerId,
	}).Debug("Disconnecting Peer")

	pubkey, err := decodePubkey(peerId)
	if err != nil {
		return err
	}
	req := &clngrpc.DisconnectRequest{
		Id: pubkey,
	}

	_, err = g.client.Disconnect(ctx, req)
	if err != nil {
		logger.Logger.WithError(err).Error("Failed to disconnect peer")
		return err
	}

	return nil
}

func (g *GreenlightService) MakeOffer(ctx context.Context, description string) (string, error) {
	logger.Logger.WithFields(logrus.Fields{
		"description": description,
	}).Debug("Make Offer")

	req := &clngrpc.OfferRequest{
		Amount:      "any",
		Description: &description,
	}
	resp, err := g.client.Offer(ctx, req)
	if err != nil {
		logger.Logger.WithError(err).Error("offer failed")
		return "", fmt.Errorf("offer failed: %w", err)
	}
	if resp == nil {
		return "", fmt.Errorf("empty offer response")
	}

	return resp.Bolt12, nil
}

func (g *GreenlightService) GetNewOnchainAddress(ctx context.Context) (string, error) {
	resp, err := g.client.NewAddr(ctx, &clngrpc.NewaddrRequest{})
	if err != nil {
		logger.Logger.WithError(err).Error("Failed to generate onchain address")
		return "", err
	}

	if resp.Bech32 != nil {
		return *resp.Bech32, nil
	}

	if resp.P2Tr != nil {
		return *resp.P2Tr, nil
	}

	logger.Logger.WithField("resp", resp).Error("No known onchain address type returned")
	return "", fmt.Errorf("unknown default onchain address type")
}

func (g *GreenlightService) ResetRouter(key string) error {
	return errors.New("not implemented")
}

func (g *GreenlightService) GetOnchainBalance(ctx context.Context) (*lnclient.OnchainBalanceResponse, error) {
	lf, err := g.client.ListFunds(ctx, &clngrpc.ListfundsRequest{})
	if err != nil {
		return nil, fmt.Errorf("listfunds failed: %w", err)
	}

	lpc, err := g.client.ListPeerChannels(ctx, &clngrpc.ListpeerchannelsRequest{})
	if err != nil {
		return nil, fmt.Errorf("listpeerchannels failed: %w", err)
	}

	chByID := make(map[string]*clngrpc.ListpeerchannelsChannels)
	for _, ch := range lpc.Channels {
		if ch == nil || len(ch.ChannelId) == 0 {
			continue
		}
		chByID[hex.EncodeToString(ch.ChannelId)] = ch
	}

	balances := &lnclient.OnchainBalanceResponse{
		PendingBalancesDetails:      []lnclient.PendingBalanceDetails{},
		PendingSweepBalancesDetails: []lnclient.PendingBalanceDetails{},
	}

	var reservedSats int64

	for _, utxo := range lf.Outputs {
		if utxo == nil || utxo.AmountMsat == nil {
			continue
		}

		amt := satInt64(utxo.AmountMsat)
		balances.TotalSat += amt

		if utxo.Reserved {
			balances.ReservedSat += amt
			reservedSats += amt
		}

		switch utxo.Status {
		case clngrpc.ListfundsOutputs_CONFIRMED:
			if !utxo.Reserved {
				balances.SpendableSat += amt
			}

		case clngrpc.ListfundsOutputs_UNCONFIRMED:
			balances.PendingSweepBalancesDetails = append(
				balances.PendingSweepBalancesDetails,
				lnclient.PendingBalanceDetails{
					AmountSat:     uint64(amt),
					FundingTxId:   hex.EncodeToString(utxo.Txid),
					FundingTxVout: utxo.Output,
				},
			)
		}
	}

	for _, ch := range lf.Channels {
		if ch == nil || ch.OurAmountMsat == nil || !isClosingState(ch.State) {
			continue
		}

		amt := sat(ch.OurAmountMsat)
		balances.PendingBalancesFromChannelClosuresSat += amt
		chanIdStr := hex.EncodeToString(ch.ChannelId)

		detail := lnclient.PendingBalanceDetails{
			ChannelId: chanIdStr,
			NodeId:    hex.EncodeToString(ch.PeerId),
			AmountSat: amt,
		}

		if pc, ok := chByID[chanIdStr]; ok {
			if len(pc.FundingTxid) > 0 {
				detail.FundingTxId = hex.EncodeToString(pc.FundingTxid)
			}
			if pc.FundingOutnum != nil {
				detail.FundingTxVout = *pc.FundingOutnum
			}
		}

		balances.PendingBalancesDetails = append(
			balances.PendingBalancesDetails,
			detail,
		)
	}

	balances.InternalBalances = map[string]int64{
		"reserved": reservedSats,
	}

	return balances, nil
}

func (g *GreenlightService) GetBalances(ctx context.Context, includeInactiveChannels bool) (*lnclient.BalancesResponse, error) {
	logger.Logger.WithFields(logrus.Fields{
		"includeInactiveChannels": includeInactiveChannels,
	}).Debug("Get all Balances")

	onchainBalance, err := g.GetOnchainBalance(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := g.client.ListPeerChannels(ctx, &clngrpc.ListpeerchannelsRequest{})
	if err != nil {
		return nil, fmt.Errorf("listpeerchannels failed: %w", err)
	}

	lightning := lnclient.LightningBalanceResponse{}

	for _, ch := range resp.Channels {
		if ch == nil {
			continue
		}

		// Never include closing or closed channels
		if ch.State != clngrpc.ChannelState_ChanneldNormal {
			continue
		}

		// This isn't perfect to determine if a channel is active
		active := ch.PeerConnected
		include := active || includeInactiveChannels
		if !include {
			continue
		}

		if ch.SpendableMsat != nil {
			spendable := i64(ch.SpendableMsat.Msat)
			lightning.TotalSpendableMsat += spendable

			if spendable > lightning.NextMaxSpendableMsat {
				lightning.NextMaxSpendableMsat = spendable
			}
		}

		if ch.ReceivableMsat != nil {
			receivable := i64(ch.ReceivableMsat.Msat)
			lightning.TotalReceivableMsat += receivable

			if receivable > lightning.NextMaxReceivableMsat {
				lightning.NextMaxReceivableMsat = receivable
			}
		}
	}

	lightning.NextMaxSpendableMPPMsat = lightning.TotalSpendableMsat
	lightning.NextMaxReceivableMPPMsat = lightning.TotalReceivableMsat

	return &lnclient.BalancesResponse{
		Onchain:   *onchainBalance,
		Lightning: lightning,
	}, nil
}

func (g *GreenlightService) RedeemOnchainFunds(ctx context.Context, toAddress string, amount uint64, feeRate *uint64, sendAll bool) (string, error) {
	logger.Logger.WithFields(logrus.Fields{
		"toAddress": toAddress,
		"amount":    amount,
		"feeRate":   feeRate,
		"sendAll":   sendAll,
	}).Debug("Redeem Onchain Funds")

	Satoshi := clngrpc.AmountOrAll{Value: &clngrpc.AmountOrAll_Amount{
		Amount: &clngrpc.Amount{Msat: uint64(amount) * 1000},
	}}
	if sendAll {
		Satoshi = clngrpc.AmountOrAll{Value: &clngrpc.AmountOrAll_All{
			All: true,
		}}
	}

	req := &clngrpc.WithdrawRequest{
		Destination: toAddress,
		Satoshi:     &Satoshi,
	}

	if feeRate != nil {
		if *feeRate > math.MaxUint32/1000 {
			return "", fmt.Errorf("fee rate too high")
		}
		req.Feerate = &clngrpc.Feerate{
			Style: &clngrpc.Feerate_Perkb{
				Perkb: uint32(*feeRate) * 1000,
			},
		}
	}

	resp, err := g.client.Withdraw(ctx, req)
	if err != nil {
		logger.Logger.WithError(err).Error("withdraw failed")
		return "", fmt.Errorf("withdraw failed: %w", err)
	}

	if resp == nil {
		return "", fmt.Errorf("empty withdraw response")
	}

	return hex.EncodeToString(resp.Txid), nil
}

func (g *GreenlightService) ListPeers(ctx context.Context) ([]lnclient.PeerDetails, error) {
	resp, err := g.client.ListPeers(ctx, &clngrpc.ListpeersRequest{})
	if err != nil {
		return nil, fmt.Errorf("listpeers failed: %w", err)
	}

	peers := make([]lnclient.PeerDetails, 0, len(resp.Peers))
	for _, peer := range resp.Peers {
		if peer == nil {
			continue
		}

		req_node := &clngrpc.ListnodesRequest{Id: peer.Id}

		resp_node, err := g.client.ListNodes(ctx, req_node)
		if err != nil {
			return nil, fmt.Errorf("listnodes failed: %w", err)
		}

		if len(resp_node.Nodes) == 0 {
			addr := ""
			peers = append(peers, lnclient.PeerDetails{
				NodeId:      hex.EncodeToString(peer.Id),
				Address:     addr,
				IsPersisted: peer.GetNumChannels() > 0,
				IsConnected: peer.Connected,
			})
			continue
		}

		var (
			ipv4  *clngrpc.ListnodesNodesAddresses
			ipv6  *clngrpc.ListnodesNodesAddresses
			torv3 *clngrpc.ListnodesNodesAddresses
		)

		for _, addr := range resp_node.Nodes[0].Addresses {
			if addr == nil {
				continue
			}

			switch addr.ItemType {
			case clngrpc.ListnodesNodesAddresses_IPV4:
				if ipv4 == nil {
					ipv4 = addr
				}
			case clngrpc.ListnodesNodesAddresses_IPV6:
				if ipv6 == nil {
					ipv6 = addr
				}
			case clngrpc.ListnodesNodesAddresses_TORV3:
				if torv3 == nil {
					torv3 = addr
				}
			}
		}

		var selected *clngrpc.ListnodesNodesAddresses
		switch {
		case ipv4 != nil:
			selected = ipv4
		case ipv6 != nil:
			selected = ipv6
		case torv3 != nil:
			selected = torv3
		default:
			addr := ""
			selected = &clngrpc.ListnodesNodesAddresses{
				Address: &addr,
				Port:    0,
			}
		}

		peers = append(peers, lnclient.PeerDetails{
			NodeId:      hex.EncodeToString(peer.Id),
			Address:     selected.GetAddress(),
			IsPersisted: peer.GetNumChannels() > 0,
			IsConnected: peer.Connected,
		})
	}

	return peers, nil
}

// GetLogOutput returns the tail of the local signer log (the only node-side
// log the hub can see; the hosted lightningd log stays on Blockstream infra),
// passed through the secret sanitizer.
func (g *GreenlightService) GetLogOutput(ctx context.Context, maxLen int) ([]byte, error) {
	return readSignerLogTail(g.config.SignerDataDir, maxLen)
}

func readSignerLogTail(dataDir string, maxLen int) ([]byte, error) {
	if dataDir == "" {
		return []byte{}, nil
	}
	f, err := os.Open(filepath.Join(dataDir, "signer.log"))
	if err != nil {
		if os.IsNotExist(err) {
			return []byte{}, nil
		}
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if maxLen <= 0 || maxLen > 1<<20 {
		maxLen = 1 << 20
	}
	off := st.Size() - int64(maxLen)
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil, err
	}
	return []byte(sanitizeGlcliOutput(string(buf))), nil
}

func (g *GreenlightService) SignMessage(ctx context.Context, message string) (string, error) {
	// NOT SUPPORTED, by design: signmessage is intentionally blocked by
	// the Greenlight signer and is not part of the allowed API
	// (Blockstream/greenlight#739, closed 2026-09-06). Older node stacks
	// wedged lightningd's serial hsmd queue on this call (observed on
	// testnet, v26.06gl1); gl-plugin at upstream 15b22cf now rejects it
	// immediately with Unimplemented (regtest bench, 2026-10-10). Never
	// forward it: we cannot know which plugin a hosted node runs.
	return "", errors.New("sign message is not supported by the greenlight backend (the Greenlight signer intentionally does not sign arbitrary messages)")
}

func (g *GreenlightService) GetStorageDir() (string, error) {
	// Prefer directories that actually exist so Hub .bkp includes device PEMs / seed.
	// SignerDataDir (product path) → CredsPath (connect-only) → workDir.
	candidates := []string{g.config.SignerDataDir, g.config.CredsPath, g.workDir}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p, nil
		}
	}
	if g.config.SignerDataDir != "" {
		return g.config.SignerDataDir, nil
	}
	return g.workDir, nil
}

func (g *GreenlightService) GetNetworkGraph(ctx context.Context, nodeIds []string) (lnclient.NetworkGraphResponse, error) {
	logger.Logger.WithFields(logrus.Fields{
		"nodeIds": nodeIds,
	}).Debug("Get Network Graph")

	listnodes := make([]*clngrpc.ListnodesNodes, 0)
	listchannels := make([]*clngrpc.ListchannelsChannels, 0)

	for _, nodeId := range nodeIds {
		nodeIdBytes, err := decodePubkey(nodeId)
		if err != nil {
			logger.Logger.WithError(err).Error("failed to decode nodeId string")
			return nil, fmt.Errorf("failed to decode nodeId string: %w", err)
		}

		listnode, err := g.client.ListNodes(ctx, &clngrpc.ListnodesRequest{Id: nodeIdBytes})
		if err != nil {
			logger.Logger.WithError(err).Error("listnodes failed")
			return nil, err
		}
		listnodes = append(listnodes, listnode.Nodes...)

		listchannel, err := g.client.ListChannels(ctx, &clngrpc.ListchannelsRequest{Source: nodeIdBytes})
		if err != nil {
			logger.Logger.WithError(err).Error("listchannels failed")
			return nil, err
		}
		listchannels = append(listchannels, listchannel.Channels...)

		listchannel, err = g.client.ListChannels(ctx, &clngrpc.ListchannelsRequest{Destination: nodeIdBytes})
		if err != nil {
			logger.Logger.WithError(err).Error("listchannels failed")
			return nil, err
		}
		listchannels = append(listchannels, listchannel.Channels...)
	}

	type NetworkNode struct {
		NodeId    string   `json:"nodeId"`
		Alias     string   `json:"alias"`
		Color     string   `json:"color"`
		Addresses []string `json:"addresses"`
		Features  string   `json:"features"`
	}

	type NodeInfoWithId struct {
		Node   *NetworkNode `json:"node"`
		NodeId string       `json:"nodeId"`
	}

	type NetworkChannel struct {
		Scid     string `json:"scid"`
		Node1    string `json:"node1"`
		Node2    string `json:"node2"`
		Capacity uint64 `json:"capacity"`
		Active   bool   `json:"active"`
		Public   bool   `json:"public"`
	}

	nodes := []NodeInfoWithId{}
	channels := []*NetworkChannel{}

	for _, node := range listnodes {
		nodeIdStr := hex.EncodeToString(node.Nodeid)
		addrs := []string{}
		for _, a := range node.Addresses {
			addrs = append(addrs, fmt.Sprintf("%s:%d", a.GetAddress(), a.GetPort()))
		}
		networkNode := NetworkNode{
			NodeId:    nodeIdStr,
			Alias:     node.GetAlias(),
			Color:     hex.EncodeToString(node.Color),
			Addresses: addrs,
			Features:  hex.EncodeToString(node.Features),
		}
		nodes = append(nodes, NodeInfoWithId{
			Node:   &networkNode,
			NodeId: nodeIdStr,
		})
	}

	seen := make(map[string]struct{})
	for _, edge := range listchannels {
		key := fmt.Sprintf("%s:%d", edge.ShortChannelId, edge.Direction)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		channel := NetworkChannel{
			Scid:     edge.ShortChannelId,
			Node1:    hex.EncodeToString(edge.Source),
			Node2:    hex.EncodeToString(edge.Destination),
			Capacity: sat(edge.AmountMsat),
			Active:   edge.Active,
			Public:   edge.Public,
		}
		channels = append(channels, &channel)
	}

	networkGraph := map[string]interface{}{
		"nodes":    nodes,
		"channels": channels,
	}
	return networkGraph, nil
}

func (g *GreenlightService) UpdateLastWalletSyncRequest() {
}

func (g *GreenlightService) GetSupportedNIP47Methods() []string {
	methods := []string{
		models.PAY_INVOICE_METHOD,
		models.PAY_KEYSEND_METHOD,
		models.GET_BALANCE_METHOD,
		models.GET_BUDGET_METHOD,
		models.GET_INFO_METHOD,
		models.MAKE_INVOICE_METHOD,
		models.LOOKUP_INVOICE_METHOD,
		models.LIST_TRANSACTIONS_METHOD,
		models.MULTI_PAY_INVOICE_METHOD,
		models.MULTI_PAY_KEYSEND_METHOD,
		// sign_message intentionally omitted: blocked by the GL signer (GL#739)
		// (see SignMessage); older node stacks wedged on it.
	}

	return methods
}

// Push tier: the WaitAnyInvoice pump publishes nwc_lnclient_payment_received
// with a persisted pay index, so payment_received notifications are safe.
// Advertising it switches off the hub's reconcile safety net
// (transactions_service.go:703) - the pump covers that gap by replaying
// anything that happened while the hub was down (index-based catch-up).
func (g *GreenlightService) GetSupportedNIP47NotificationTypes() []string {
	// payment_sent: the hub emits it on settlement, including payments
	// settled asynchronously by the reconciler (payments.go)
	return []string{
		"payment_received",
		"payment_sent",
	}
}

func (g *GreenlightService) GetCustomNodeCommandDefinitions() []lnclient.CustomNodeCommandDef {
	return nil
}

func (g *GreenlightService) ExecuteCustomNodeCommand(ctx context.Context, command *lnclient.CustomNodeCommandRequest) (*lnclient.CustomNodeCommandResponse, error) {
	return nil, lnclient.ErrUnknownCustomNodeCommand
}

func (g *GreenlightService) clnInvoiceToTransaction(ctx context.Context, invoice *clngrpc.ListinvoicesInvoices) (*lnclient.Transaction, error) {
	var invstring string
	var bolt11Invoice string
	if invoice.Bolt11 != nil {
		invstring = *invoice.Bolt11
		bolt11Invoice = *invoice.Bolt11
	} else if invoice.Bolt12 != nil {
		invstring = *invoice.Bolt12
	} else {
		return nil, fmt.Errorf("bolt11 and bolt12 missing from invoice")
	}

	var amountMsat int64
	var lspFeeMsat int64
	if invoice.Status == clngrpc.ListinvoicesInvoices_PAID && invoice.AmountReceivedMsat != nil {
		amountMsat = i64(invoice.AmountReceivedMsat.Msat)
		// LSPS2 JIT: the LSP skims its opening fee, so the node receives
		// less than the invoice amount; report the skim as the fee (LDK
		// reports CounterpartySkimmedFeeMsat the same way).
		if invoice.AmountMsat != nil && invoice.AmountReceivedMsat.Msat < invoice.AmountMsat.Msat {
			lspFeeMsat = i64(invoice.AmountMsat.Msat - invoice.AmountReceivedMsat.Msat)
		}
	} else if invoice.AmountMsat != nil {
		amountMsat = i64(invoice.AmountMsat.Msat)
	} else {
		amountMsat = 0
	}

	expires_at := int64(invoice.ExpiresAt)

	var paid_at *int64
	if invoice.Status == clngrpc.ListinvoicesInvoices_PAID {
		if invoice.PaidAt == nil {
			return nil, fmt.Errorf("paid_at missing from paid invoice")
		}
		paid_at_int64 := int64(*invoice.PaidAt)
		paid_at = &paid_at_int64
	}

	decoded_invoice, err := g.client.Decode(ctx, &clngrpc.DecodeRequest{String_: invstring})
	if err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	var created_at int64
	metadata := map[string]interface{}{}
	var description_hash string
	switch decoded_invoice.ItemType {
	case clngrpc.DecodeResponse_BOLT12_INVOICE:
		if decoded_invoice.InvoiceCreatedAt == nil {
			return nil, fmt.Errorf("invoice_created_at missing from bolt12 invoice")
		}
		created_at = int64(*decoded_invoice.InvoiceCreatedAt)

		offer := map[string]interface{}{}
		offer["id"] = hex.EncodeToString(decoded_invoice.OfferId)
		if invoice.InvreqPayerNote != nil {
			offer["payer_note"] = *invoice.InvreqPayerNote
		}
		metadata["offer"] = offer
	case clngrpc.DecodeResponse_BOLT11_INVOICE:
		if decoded_invoice.CreatedAt == nil {
			return nil, fmt.Errorf("created_at missing from bolt11 invoice")
		}
		created_at = int64(*decoded_invoice.CreatedAt)

		if decoded_invoice.DescriptionHash != nil {
			description_hash = hex.EncodeToString(decoded_invoice.DescriptionHash)
		}

	default:
		return nil, fmt.Errorf("invoice is not a bolt11 or bolt12 invoice")
	}

	var description string
	if invoice.Description != nil {
		description = *invoice.Description
	} else {
		description = ""
	}

	transaction := &lnclient.Transaction{
		Type:            "incoming",
		Invoice:         bolt11Invoice,
		Description:     description,
		DescriptionHash: description_hash,
		Preimage:        hex.EncodeToString(invoice.PaymentPreimage),
		PaymentHash:     hex.EncodeToString(invoice.PaymentHash),
		AmountMsat:      amountMsat,
		FeesPaidMsat:    lspFeeMsat,
		CreatedAt:       created_at,
		ExpiresAt:       &expires_at,
		SettledAt:       paid_at,
		Metadata:        metadata,
	}
	return transaction, nil
}

func GeneratePreimage() ([]byte, error) {
	preimage := make([]byte, 32)

	_, err := crand.Read(preimage[:])
	if err != nil {
		return nil, fmt.Errorf("failed to generate preimage: %w", err)
	}

	return preimage, nil
}

// --- helpers ported from the CLN backend (same wire surface) ---

func isClosingState(state clngrpc.ChannelState) bool {
	switch state {
	case clngrpc.ChannelState_ChanneldShuttingDown,
		clngrpc.ChannelState_ClosingdSigexchange,
		clngrpc.ChannelState_ClosingdComplete,
		clngrpc.ChannelState_AwaitingUnilateral,
		clngrpc.ChannelState_FundingSpendSeen:
		return true
	default:
		return false
	}
}

func isOpeningState(state clngrpc.ChannelState) bool {
	switch state {
	case clngrpc.ChannelState_ChanneldAwaitingLockin,
		clngrpc.ChannelState_DualopendAwaitingLockin,
		clngrpc.ChannelState_DualopendOpenCommittReady,
		clngrpc.ChannelState_DualopendOpenCommitted,
		clngrpc.ChannelState_DualopendOpenInit,
		clngrpc.ChannelState_Openingd:
		return true
	default:
		return false
	}
}

func isConfirmedState(state clngrpc.ChannelState) bool {
	switch state {
	case clngrpc.ChannelState_AwaitingUnilateral,
		clngrpc.ChannelState_ChanneldAwaitingSplice,
		clngrpc.ChannelState_ChanneldNormal,
		clngrpc.ChannelState_ChanneldShuttingDown,
		clngrpc.ChannelState_ClosingdComplete,
		clngrpc.ChannelState_ClosingdSigexchange,
		clngrpc.ChannelState_FundingSpendSeen,
		clngrpc.ChannelState_Onchain:
		return true
	default:
		return false
	}
}

func msatInt64(a *clngrpc.Amount) int64 {
	if a == nil {
		return 0
	}
	return i64(a.Msat)
}

func satInt64(a *clngrpc.Amount) int64 {
	if a == nil {
		return 0
	}
	return int64(a.Msat / 1000)
}

func sat(a *clngrpc.Amount) uint64 {
	if a == nil {
		return 0
	}
	return a.Msat / 1000
}

func localFeeBaseMsat(ch *clngrpc.ListpeerchannelsChannels) uint32 {
	if ch == nil {
		return 0
	}
	u := ch.Updates
	if u == nil {
		return 0
	}
	l := u.Local
	if l == nil {
		return 0
	}
	f := l.FeeBaseMsat
	if f == nil {
		return 0
	}
	return uint32(f.Msat)
}

func localFeePPM(ch *clngrpc.ListpeerchannelsChannels) uint32 {
	if ch == nil {
		return 0
	}
	u := ch.Updates
	if u == nil {
		return 0
	}
	l := u.Local
	if l == nil {
		return 0
	}
	return l.FeeProportionalMillionths
}

func confirmationsRequiredFromStatus(status []string) (uint32, *string) {
	reStatus := regexp.MustCompile(`.*Funding needs (\d+) more confirmations to be ready.*`)

	for _, status := range status {
		match := reStatus.FindStringSubmatch(status)
		if len(match) > 1 {
			num, err := strconv.Atoi(match[1])
			if err != nil {
				errStr := fmt.Sprintf("Error converting number of confirmations required: %v", err)
				return 0, &errStr
			}
			return uint32(num), nil
		}
	}

	errNotFound := "Could not find status indicating number of confirmations required"
	return 0, &errNotFound
}

func lspErrorReason(err error) string {
	if err == nil {
		return "unknown"
	}
	msg := err.Error()
	if i := strings.LastIndex(msg, "Error code"); i >= 0 {
		msg = msg[i:]
	}
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

// isNodeUnreachable reports a transport-level failure to reach the hosted
// node (as opposed to an application error from the node/LSP).
func isNodeUnreachable(err error) bool {
	c := status.Code(err)
	return err != nil && (c == codes.Unavailable || c == codes.DeadlineExceeded)
}

// decodePubkey parses a hex secp256k1 node id. Every pubkey MUST pass through
// this before reaching the hosted node: cln-grpc's request conversion does
// PublicKey::from_slice(..).unwrap(), so a malformed id panics gl-plugin,
// which is an "important" plugin, shutting the whole hosted lightningd down
// (reproduced on the regtest bench with ListNodes{id: []}).
func decodePubkey(s string) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid node pubkey: %w", err)
	}
	if _, err := btcec.ParsePubKey(b); err != nil || len(b) != 33 {
		return nil, fmt.Errorf("invalid node pubkey %q", s)
	}
	return b, nil
}

// maxBolt11DescriptionBytes is the largest description CLN will embed in a
// bolt11 (`d` field); longer ones must use description_hash.
const maxBolt11DescriptionBytes = 639

// validateInvoiceDescription turns CLN's opaque -32602 "Internal" failures
// into clear client errors (seen on the regtest bench via NWC make_invoice).
func validateInvoiceDescription(description string, hashOnly bool) error {
	for _, r := range description {
		if (r < 0x20 && r != '\n' && r != '\r' && r != '\t') || r == 0x7f {
			return fmt.Errorf("invoice description contains control characters")
		}
	}
	if !hashOnly && len(description) > maxBolt11DescriptionBytes {
		return fmt.Errorf("invoice description too long: %d bytes (max %d, use description_hash)", len(description), maxBolt11DescriptionBytes)
	}
	return nil
}
