// Package httpapi serves Etherview's native API and embedded SPA.
package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/analytics"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/apiops"
	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/billing"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/events"
	"github.com/islishude/etherview/internal/mempool"
	"github.com/islishude/etherview/internal/metadata"
	"github.com/islishude/etherview/internal/observability"
	"github.com/islishude/etherview/internal/userauth"
	"github.com/islishude/etherview/internal/verify"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrUnavailable   = errors.New("capability unavailable")
	ErrNotReady      = errors.New("not ready")
	ErrInvalidCursor = errors.New("invalid or stale cursor")
)

// CapabilityUnavailableError carries only controlled machine identifiers that
// are safe to expose in the shared error envelope. Upstream text is never part
// of this value.
type CapabilityUnavailableError struct {
	Capability string
	State      string
	Code       string
}

func (*CapabilityUnavailableError) Error() string { return ErrUnavailable.Error() }
func (*CapabilityUnavailableError) Unwrap() error { return ErrUnavailable }

func NewCapabilityUnavailableError(capability, state, code string) error {
	errorValue := &CapabilityUnavailableError{Capability: capability, State: state, Code: code}
	if !errorValue.valid() {
		return ErrUnavailable
	}
	return errorValue
}

func (err *CapabilityUnavailableError) valid() bool {
	return err != nil && capabilityIdentifierPattern.MatchString(err.Capability) &&
		(err.State == "unavailable" || err.State == "failed") &&
		capabilityIdentifierPattern.MatchString(err.Code)
}

var (
	hashPattern                 = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
	addressPattern              = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	capabilityIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)
)

const (
	maximumOpaqueCursorLength = 1024
	maximumPageSize           = 100
	maximumNativeQueryBytes   = 4096
)

type StatusSnapshot struct {
	LatestBlock         uint64
	IndexedBlock        uint64
	HighestCoveredBlock uint64
	HighestCoveredKnown bool
	BackfillComplete    bool
	SafeBlock           *uint64
	FinalizedBlock      *uint64
	CoverageStart       uint64
	CoverageEnd         uint64
	CoreReady           bool
	Completeness        gen.Completeness
}

// Reader is the query boundary. Store implementations must return only stable,
// fully validated API models and must honor canonical/hash lookup semantics.
type Reader interface {
	Status(context.Context) (StatusSnapshot, error)
	Blocks(context.Context, string, int) ([]gen.Block, string, error)
	Block(context.Context, string) (gen.Block, error)
	BlockTransactions(context.Context, string, string, int) ([]gen.Transaction, string, error)
	Transactions(context.Context, string, int) ([]gen.Transaction, string, error)
	Transaction(context.Context, string) (gen.Transaction, error)
	Address(context.Context, string) (gen.AddressSummary, error)
	Search(context.Context, string, string, int) ([]gen.SearchResult, string, error)
}

type TransactionReader interface {
	Transaction(context.Context, string) (gen.Transaction, error)
}

type GenesisReader interface {
	GenesisAccounts(context.Context, string, int) ([]gen.GenesisAccount, string, error)
}

type AnalyticsReader interface {
	Overview(context.Context, string, time.Time) (analytics.Overview, error)
	Detail(context.Context, analytics.DetailRequest) (analytics.Series, error)
}

type AddressActivityReader interface {
	AddressTransactions(context.Context, string, string, int) ([]gen.Transaction, string, error)
	AddressWithdrawals(context.Context, string, string, int) ([]gen.AddressWithdrawal, string, error)
}

type AddressEnrichmentActivityReader interface {
	AddressInternalTransactions(context.Context, catalog.AddressActivityRequest) (catalog.AddressInternalTransactionPage, error)
	AddressERC20Transfers(context.Context, catalog.AddressActivityRequest) (catalog.AddressTokenTransferPage, error)
	AddressNFTTransfers(context.Context, catalog.AddressActivityRequest) (catalog.AddressTokenTransferPage, error)
}

type DelegationBindingReader interface {
	AddressDelegation(context.Context, string) (gen.DelegationBinding, error)
}

type delegationCatalogReader interface {
	TransactionAuthorizations(context.Context, catalog.TransactionResourceRequest) (catalog.TransactionAuthorizationPage, error)
	AddressDelegations(context.Context, catalog.AddressDelegationRequest) (catalog.DelegationHistoryPage, error)
}

// readinessStatusReader lets a cache-decorated Reader bypass its cache for the
// readiness decision. A cached success must not hide loss of a configured
// PostgreSQL reader or writer pool.
type readinessStatusReader interface {
	ReadinessStatus(context.Context) (StatusSnapshot, error)
}

type VerificationReader interface {
	Job(context.Context, string) (verify.VerificationJob, bool, error)
	VerifiedContract(context.Context, uint64, string) (verify.VerifiedContract, bool, error)
}

type VerificationSubmitter interface {
	SubmitV2(context.Context, verify.SubmissionV2) (verify.VerificationJob, bool, error)
}

type CompilerCatalogReader interface {
	Versions(context.Context, verify.Language) ([]string, error)
}

type VerificationTargetResolver interface {
	ResolveVerificationTarget(context.Context, string) (verify.VerificationTarget, error)
}

type ProxyReader interface {
	Proxy(context.Context, string) (gen.ProxyDetails, error)
	ProxyUpgrades(context.Context, string, string, int) (gen.ProxyUpgradeHistory, string, error)
	ProxyInitializations(context.Context, string, string, int) (gen.ProxyInitializationHistory, string, error)
	DiamondCuts(context.Context, string, string, int) (gen.DiamondCutHistory, string, error)
}

type UserAPIKeyAdministration interface {
	Policy() auth.UserKeyPolicy
	Create(context.Context, string, string, []auth.Scope) (auth.IssuedAPIKey, error)
	Rotate(context.Context, string, string) (auth.IssuedAPIKey, error)
	Revoke(context.Context, string, string, time.Time) error
	List(context.Context, string, *auth.UserKeyPageAfter, int) (auth.UserKeyPage, error)
}

type Options struct {
	Config                config.Config
	Reader                Reader
	TransactionReader     TransactionReader
	AddressActivities     AddressActivityReader
	Genesis               GenesisReader
	Catalog               catalog.Reader
	Analytics             AnalyticsReader
	Web                   http.Handler
	Etherscan             http.Handler
	Metrics               http.Handler
	Events                *events.Broker
	HomeSnapshots         HomeSnapshotSource
	Mempool               mempool.Reader
	NFTMediaSource        metadata.NFTImageSource
	NFTMediaProxy         *metadata.MediaProxy
	VerificationReader    VerificationReader
	VerificationSubmitter VerificationSubmitter
	CompilerCatalog       CompilerCatalogReader
	VerificationTargets   VerificationTargetResolver
	ProxyReader           ProxyReader
	UserAuth              UserAuthenticator
	UserAdministration    UserAdministration
	UserAPIKeys           UserAPIKeyAdministration
	Billing               *billing.HTTPDispatcher
	BillingReader         BillingReader
	Quota                 func(http.Handler) http.Handler
	Logger                *slog.Logger
	RequestID             func() string
	Now                   func() time.Time
	RuntimeReady          func() bool
	MaxVerificationBody   int64
}

type Handler struct {
	cfg                   config.Config
	reader                Reader
	transactionReader     TransactionReader
	addressActivities     AddressActivityReader
	addressEnrichment     AddressEnrichmentActivityReader
	genesis               GenesisReader
	catalog               catalog.Reader
	analytics             AnalyticsReader
	web                   http.Handler
	etherscan             http.Handler
	metrics               http.Handler
	events                *events.Broker
	homeSnapshots         HomeSnapshotSource
	mempool               mempool.Reader
	nftMediaSource        metadata.NFTImageSource
	nftMediaProxy         *metadata.MediaProxy
	verificationReader    VerificationReader
	verificationSubmitter VerificationSubmitter
	compilerCatalog       CompilerCatalogReader
	verificationTargets   VerificationTargetResolver
	proxyReader           ProxyReader
	userAuth              UserAuthenticator
	userAdministration    UserAdministration
	userAPIKeys           UserAPIKeyAdministration
	billing               *billing.HTTPDispatcher
	billingReader         BillingReader
	authOrigin            string
	authSecureCookie      bool
	logger                *slog.Logger
	requestID             func() string
	now                   func() time.Time
	runtimeReady          func() bool
	maxVerificationBody   int64
	mux                   *http.ServeMux
	quotaMux              http.Handler
}

func New(options Options) (*Handler, error) {
	if options.Reader == nil {
		return nil, errors.New("httpapi reader is required")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.RequestID == nil {
		options.RequestID = randomRequestID
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.RuntimeReady == nil {
		options.RuntimeReady = func() bool { return true }
	}
	h := &Handler{
		cfg:                   options.Config,
		reader:                options.Reader,
		transactionReader:     options.TransactionReader,
		addressActivities:     options.AddressActivities,
		genesis:               options.Genesis,
		catalog:               options.Catalog,
		analytics:             options.Analytics,
		web:                   options.Web,
		etherscan:             options.Etherscan,
		metrics:               options.Metrics,
		events:                options.Events,
		homeSnapshots:         options.HomeSnapshots,
		mempool:               options.Mempool,
		nftMediaSource:        options.NFTMediaSource,
		nftMediaProxy:         options.NFTMediaProxy,
		verificationReader:    options.VerificationReader,
		verificationSubmitter: options.VerificationSubmitter,
		compilerCatalog:       options.CompilerCatalog,
		verificationTargets:   options.VerificationTargets,
		proxyReader:           options.ProxyReader,
		userAuth:              options.UserAuth,
		userAdministration:    options.UserAdministration,
		userAPIKeys:           options.UserAPIKeys,
		billing:               options.Billing,
		billingReader:         options.BillingReader,
		logger:                options.Logger,
		requestID:             options.RequestID,
		now:                   options.Now,
		runtimeReady:          options.RuntimeReady,
		maxVerificationBody:   options.MaxVerificationBody,
		mux:                   http.NewServeMux(),
	}
	if h.transactionReader == nil {
		h.transactionReader = options.Reader
	}
	h.addressEnrichment, _ = options.Catalog.(AddressEnrichmentActivityReader)
	if h.maxVerificationBody <= 0 {
		h.maxVerificationBody = 6 << 20
	}
	if h.cfg.Features.UserAuth && (h.userAuth == nil || h.userAdministration == nil) {
		return nil, errors.New("enabled user authentication requires writer services")
	}
	if h.cfg.Features.UserAPIKeys && h.userAPIKeys == nil {
		return nil, errors.New("enabled user API keys require writer services")
	}
	if h.cfg.Features.UserAuth && h.billingReader == nil {
		return nil, errors.New("enabled user authentication requires a writer-backed billing reader")
	}
	if h.cfg.Features.X402Billing && h.billing == nil {
		return nil, errors.New("enabled x402 billing requires a writer-backed dispatcher")
	}
	if h.cfg.Features.UserAuth {
		origin, err := userauth.CanonicalPublicOrigin(h.cfg.Server.PublicURL)
		if err != nil {
			return nil, fmt.Errorf("configure user authentication origin: %w", err)
		}
		h.authOrigin = origin
		h.authSecureCookie = strings.HasPrefix(origin, "https://")
	}
	h.routes()
	h.quotaMux = h.mux
	if options.Quota != nil {
		h.quotaMux = options.Quota(h.mux)
		if h.quotaMux == nil {
			return nil, errors.New("httpapi quota wrapper returned nil")
		}
	}
	return h, nil
}

func (h *Handler) routes() {
	h.mux.HandleFunc("GET /health/live", h.live)
	h.mux.HandleFunc("GET /health/ready", h.ready)
	if h.metrics != nil {
		h.mux.Handle("GET /metrics", h.metrics)
	}
	h.mux.HandleFunc("GET /api/v1/status", h.status)
	h.mux.HandleFunc("GET /api/v1/config", h.publicConfig)
	h.mux.HandleFunc("GET /api/v1/genesis/accounts", h.genesisAccounts)
	h.mux.HandleFunc("POST /api/v1/auth/challenge", h.createAuthChallenge)
	h.mux.HandleFunc("POST /api/v1/auth/verify", h.verifyAuthChallenge)
	h.mux.HandleFunc("GET /api/v1/auth/session", h.authSession)
	h.mux.HandleFunc("POST /api/v1/auth/logout", h.logoutAuthSession)
	h.mux.HandleFunc("PATCH /api/v1/users/me", h.updateCurrentUser)
	h.mux.HandleFunc("GET /api/v1/users/me/api-keys", h.listCurrentUserAPIKeys)
	h.mux.HandleFunc("POST /api/v1/users/me/api-keys", h.createCurrentUserAPIKey)
	h.mux.HandleFunc("POST /api/v1/users/me/api-keys/{prefix}/rotate", h.rotateCurrentUserAPIKey)
	h.mux.HandleFunc("DELETE /api/v1/users/me/api-keys/{prefix}", h.revokeCurrentUserAPIKey)
	h.mux.HandleFunc("GET /api/v1/admin/users", h.listAdminUsers)
	h.mux.HandleFunc("PATCH /api/v1/admin/users/{id}", h.updateAdminUser)
	h.mux.HandleFunc("POST /api/v1/admin/users/{id}/sessions/revoke", h.revokeAdminUserSessions)
	h.mux.HandleFunc("GET /api/v1/billing/config", h.billingConfig)
	h.mux.HandleFunc("GET /api/v1/billing/payments", h.listCurrentUserBillingPayments)
	h.mux.HandleFunc("GET /api/v1/admin/billing/payments", h.listAdminBillingPayments)
	h.mux.HandleFunc("GET /api/v1/admin/billing/summary", h.adminBillingSummary)
	h.handleBillable("listBlocks", h.blocks)
	h.handleBillable("getBlock", h.block)
	h.handleBillable("listBlockTransactions", h.blockTransactions)
	h.handleBillable("listTransactions", h.transactions)
	h.handleBillable("getTransaction", h.transaction)
	h.handleBillable("listPendingTransactions", h.pendingTransactions)
	if h.catalog != nil {
		h.handleBillable("getTransactionCalldata", h.transactionCalldata)
		h.handleBillable("getTransactionFailure", h.transactionFailure)
		h.handleBillable("getTransactionTrace", h.transactionTrace)
		h.handleBillable("listTransactionInternalTransactions", h.transactionInternalTransactions)
		h.handleBillable("listTransactionTokenTransfers", h.transactionTokenTransfers)
		h.handleBillable("listTransactionLogs", h.transactionLogs)
		h.handleBillable("listTransactionStateChanges", h.transactionStateChanges)
		h.handleBillable("listTransactionAuthorizations", h.transactionAuthorizations)
	}
	h.handleBillable("getAddress", h.address)
	h.handleBillable("getAddressDelegation", h.addressDelegation)
	h.handleBillable("listAddressTransactions", h.addressTransactions)
	h.handleBillable("listAddressWithdrawals", h.addressWithdrawals)
	h.handleBillable("listAddressInternalTransactions", h.addressInternalTransactions)
	h.handleBillable("listAddressERC20Transfers", h.addressERC20Transfers)
	h.handleBillable("listAddressNFTTransfers", h.addressNFTTransfers)
	if h.catalog != nil {
		h.handleBillable("listAddressDelegations", h.addressDelegations)
		h.handleBillable("listAddressNFTBalances", h.nftBalances)
		h.handleBillable("listAddressERC20Balances", h.erc20Balances)
		h.handleBillable("listTokens", h.tokens)
		h.handleBillable("getToken", h.token)
		h.handleBillable("listTokenTransfers", h.tokenTransfers)
		h.handleBillable("getNFTOwner", h.nftOwner)
		h.handleBillable("getBlockStats", h.blockStats)
		h.handleBillable("getAggregateStats", h.aggregateStats)
	}
	h.handleBillable("getChartOverview", h.chartOverview)
	h.handleBillable("getChartMetric", h.chartMetric)
	// The route remains present when external metadata is disabled so clients
	// receive a typed capability state instead of a misleading route-level 404.
	h.mux.HandleFunc("GET /api/v1/nfts/{address}/{token_id}/media", h.nftMedia)
	h.handleBillable("search", h.search)
	// Capability routes remain present when their backing service is disabled so
	// clients receive a typed unavailable response instead of mistaking a 404
	// for an empty or unsupported API surface.
	h.mux.HandleFunc("POST /api/v1/contracts/{address}/verification", h.submitAddressVerification)
	h.mux.HandleFunc("POST /api/v1/verifier/solidity/multipart", h.submitVerifier)
	h.mux.HandleFunc("POST /api/v1/verifier/solidity/standard-json", h.submitVerifier)
	h.mux.HandleFunc("POST /api/v1/verifier/solidity/batch/multipart", h.submitVerifier)
	h.mux.HandleFunc("POST /api/v1/verifier/solidity/batch/standard-json", h.submitVerifier)
	h.mux.HandleFunc("POST /api/v1/verifier/sourcify", h.submitVerifier)
	h.mux.HandleFunc("POST /api/v1/verifier/sourcify/from-etherscan", h.submitVerifier)
	h.mux.HandleFunc("GET /api/v1/verifier/compilers", h.verifierCompilers)
	h.mux.HandleFunc("POST /api/v1/verifier/lookup-methods", h.lookupVerifierMethods)
	h.handleBillable("getVerifierJob", h.verificationJob)
	h.mux.HandleFunc("GET /api/v1/contracts/{address}/verification", h.verifiedContract)
	h.mux.HandleFunc("GET /api/v1/contracts/{address}/proxy", h.contractProxy)
	h.mux.HandleFunc("GET /api/v1/contracts/{address}/proxy/upgrades", h.contractProxyUpgrades)
	h.mux.HandleFunc("GET /api/v1/contracts/{address}/proxy/initializations", h.contractProxyInitializations)
	h.mux.HandleFunc("GET /api/v1/contracts/{address}/proxy/diamond-cuts", h.contractDiamondCuts)
	if h.etherscan != nil {
		h.mux.Handle("/v2/api", h.etherscan)
	}
	if h.events != nil {
		h.mux.HandleFunc("GET /api/v1/events", h.eventStream)
	}
	h.mux.HandleFunc("GET /api/v1/home/stream", h.homeSnapshotStream)
	if h.web != nil {
		h.mux.Handle("/", h.web)
	}
}

func (h *Handler) handleBillable(operation string, handler http.HandlerFunc) {
	spec, ok := apiops.Lookup(operation)
	if !ok || !spec.BillingEligible {
		panic("httpapi billable operation is absent from the catalog: " + operation)
	}
	h.mux.Handle(spec.MuxPattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := auth.IdentityFrom(r.Context())
		billingIdentity := billing.APIKeyIdentity{
			Authenticated: identity.HasScope(requiredAPIScope(operation)),
			Prefix:        identity.Prefix,
		}
		if r.Method != spec.Method || h.billing == nil ||
			h.billing.Access(operation, billingIdentity) != billing.AccessPaid {
			handler.ServeHTTP(w, r)
			return
		}
		h.billing.ServePaid(w, r, spec, billingIdentity, handler)
	}))
}

type webRoutePatternProvider interface {
	RoutePattern(*http.Request) string
}

// RoutePattern reports the pattern selected by the same mux used to dispatch
// the request. The catch-all web route delegates to its own bounded classifier
// so SPA IDs, asset names, and reserved misses never become labels.
func (h *Handler) RoutePattern(request *http.Request) string {
	if request.Method == http.MethodOptions {
		return "/{path...}"
	}
	pattern := observability.MuxRoutePattern(h.mux, request)
	if pattern != "/" {
		return pattern
	}
	provider, ok := h.web.(webRoutePatternProvider)
	if !ok {
		if request.URL.Path == "/" {
			return "/"
		}
		return "unmatched"
	}
	switch pattern = provider.RoutePattern(request); pattern {
	case "/", "/assets/*", "/{spa...}", "unmatched", "method_not_allowed":
		return pattern
	default:
		return "unmatched"
	}
}

func (h *Handler) preflight(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" || !contains(h.cfg.Security.AllowedOrigins, origin) {
		writeError(w, r, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed", nil)
		return
	}
	requestedMethod := strings.ToUpper(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")))
	if requestedMethod != http.MethodGet && requestedMethod != http.MethodPost &&
		requestedMethod != http.MethodPatch && requestedMethod != http.MethodHead {
		writeError(w, r, http.StatusBadRequest, "method_not_allowed", "requested CORS method is not allowed", nil)
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PATCH, OPTIONS")
	w.Header().Set(
		"Access-Control-Allow-Headers",
		"Content-Type, X-API-Key, X-Request-ID, Last-Event-ID, X-CSRF-Token, Payment-Signature",
	)
	w.Header().Set("Access-Control-Max-Age", "600")
	addVary(w.Header(), "Origin")
	addVary(w.Header(), "Access-Control-Request-Method")
	addVary(w.Header(), "Access-Control-Request-Headers")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) eventStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "stream_unsupported", "streaming is unsupported", nil)
		return
	}
	channel, err := h.events.Subscribe(r.Context(), r.Header.Get("Last-Event-ID"))
	if err != nil {
		switch {
		case errors.Is(err, events.ErrInvalidCursor), errors.Is(err, events.ErrExpiredCursor), errors.Is(err, events.ErrFutureCursor):
			writeError(w, r, http.StatusBadRequest, "invalid_event_cursor", err.Error(), nil)
		case errors.Is(err, events.ErrReplayUnavailable):
			writeError(w, r, http.StatusServiceUnavailable, "event_replay_unavailable", events.ErrReplayUnavailable.Error(), nil)
		default:
			h.logger.ErrorContext(r.Context(), "event subscription failed",
				"request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
			writeError(w, r, http.StatusServiceUnavailable, "event_replay_unavailable", events.ErrReplayUnavailable.Error(), nil)
		}
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case event, open := <-channel:
			if !open {
				return
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, event.Data); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

type homeSnapshotResponse struct {
	Data json.RawMessage `json:"data"`
	Meta gen.Meta        `json:"meta"`
}

func (h *Handler) homeSnapshotStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "stream_unsupported", "streaming is unsupported", nil)
		return
	}
	if h.homeSnapshots == nil {
		writeError(
			w, r, http.StatusServiceUnavailable,
			"home_snapshot_unavailable", ErrHomeSnapshotUnavailable.Error(), nil,
		)
		return
	}
	channel, err := h.homeSnapshots.Subscribe(r.Context())
	if err != nil {
		writeError(
			w, r, http.StatusServiceUnavailable,
			"home_snapshot_unavailable", ErrHomeSnapshotUnavailable.Error(), nil,
		)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case publication, open := <-channel:
			if !open {
				return
			}
			meta := h.meta(r)
			meta.CoverageStart = &publication.CoverageStart
			meta.CoverageEnd = &publication.CoverageEnd
			encodedData := publication.EncodedData
			if len(encodedData) == 0 {
				encodedData, err = json.Marshal(publication.Data)
				if err != nil {
					h.logger.ErrorContext(
						r.Context(), "home snapshot data encoding failed",
						"request_id", requestIDFrom(r.Context()),
					)
					return
				}
			}
			encoded, err := json.Marshal(homeSnapshotResponse{
				Data: encodedData,
				Meta: meta,
			})
			if err != nil || len(encoded) > maxHomeSnapshotBytes {
				h.logger.ErrorContext(
					r.Context(), "home snapshot response encoding failed",
					"request_id", requestIDFrom(r.Context()),
				)
				return
			}
			if _, err := fmt.Fprintf(
				w, "id: %d\nevent: snapshot\ndata: %s\n\n",
				publication.EventID, encoded,
			); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
	if requestID == "" || len(requestID) > 128 {
		requestID = h.requestID()
	}
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/v2/api" {
		// Canonicality and completeness may change after a reorg or an
		// enrichment result. Shared caches require the durable event invalidator;
		// browsers and unmanaged intermediaries must not retain API responses.
		w.Header().Set("Cache-Control", "no-store")
	}
	if origin := r.Header.Get("Origin"); origin != "" && contains(h.cfg.Security.AllowedOrigins, origin) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, Payment-Required, Payment-Response")
		addVary(w.Header(), "Origin")
	}
	ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
	defer func() {
		if recovered := recover(); recovered != nil {
			if observability.IsHTTPAbortHandlerPanic(recovered) {
				panic(recovered)
			}
			// A downstream panic may contain an RPC URL, API key, compiler input,
			// or other hostile text. Log only its type at the public boundary.
			h.logger.ErrorContext(ctx, "panic handling HTTP request",
				"error_code", "http_handler_panic", "request_id", requestID,
				"error_type", fmt.Sprintf("%T", recovered),
			)
			if state, ok := w.(interface {
				ResponseCommitted() bool
				MarkPanicked()
			}); ok {
				state.MarkPanicked()
				if state.ResponseCommitted() {
					panic(http.ErrAbortHandler)
				}
			}
			WriteRecoveredPanicResponse(w, r.WithContext(ctx), requestID)
		}
	}()
	request := r.WithContext(ctx)
	if request.Method == http.MethodOptions {
		if billing.PaymentHeaderPresent(request.Header) {
			writeError(w, request, http.StatusBadRequest, "unexpected_payment_header", "payment authorization is not accepted for this request", nil)
			return
		}
		h.preflight(w, request)
		return
	}
	spec, catalogMatch := h.matchedOperation(request)
	identity := auth.IdentityFrom(request.Context())
	if catalogMatch && operationUsesAPIKeyScope(string(spec.ID)) && identity.Authenticated &&
		!identity.HasScope(requiredAPIScope(string(spec.ID))) {
		writeError(w, request, http.StatusForbidden, "api_key_scope_required", "API key scope does not authorize this operation", nil)
		return
	}
	billingIdentity := billing.APIKeyIdentity{
		Authenticated: identity.HasScope(requiredAPIScope(string(spec.ID))),
		Prefix:        identity.Prefix,
	}
	access := billing.AccessFree
	if catalogMatch && h.billing != nil {
		access = h.billing.Access(string(spec.ID), billingIdentity)
	}
	if billing.PaymentHeaderPresent(request.Header) && access != billing.AccessPaid {
		writeError(w, request, http.StatusBadRequest, "unexpected_payment_header", "payment authorization is not accepted for this request", nil)
		return
	}
	if access == billing.AccessPaid {
		if suppliedAPIKey(request) && !identity.Authenticated {
			writeError(w, request, http.StatusUnauthorized, "invalid_api_key", "authentication failed", nil)
			return
		}
		h.mux.ServeHTTP(w, request)
		return
	}
	// Price discovery must remain available after the original explorer quota
	// is exhausted. The outer coarse per-peer limiter still bounds this free
	// endpoint when x402 is enabled.
	if request.Method == http.MethodGet &&
		request.URL.Path == "/api/v1/billing/config" {
		h.mux.ServeHTTP(w, request)
		return
	}
	h.quotaMux.ServeHTTP(w, request)
}

func (h *Handler) matchedOperation(request *http.Request) (apiops.Spec, bool) {
	_, pattern := h.mux.Handler(request)
	for _, spec := range apiops.All() {
		if spec.MuxPattern == pattern && request.Method == spec.Method {
			return spec, true
		}
	}
	return apiops.Spec{}, false
}

func suppliedAPIKey(request *http.Request) bool {
	for _, value := range request.Header.Values("X-API-Key") {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

type compatibilityPanicResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Result  string `json:"result"`
}

// WriteRecoveredPanicResponse preserves the selected public boundary without
// exposing a recovered value. A request ID already chosen by Handler wins over
// an outer trace-derived fallback.
func WriteRecoveredPanicResponse(w http.ResponseWriter, r *http.Request, fallbackRequestID string) {
	requestID := strings.TrimSpace(w.Header().Get("X-Request-ID"))
	if requestID == "" || len(requestID) > 128 {
		requestID = strings.TrimSpace(fallbackRequestID)
	}
	if requestID == "" || len(requestID) > 128 {
		requestID = randomRequestID()
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Request-ID", requestID)
	if r.URL.Path == "/v2/api" {
		writeJSON(w, http.StatusInternalServerError, compatibilityPanicResponse{
			Status: "0", Message: "NOTOK", Result: "query failed",
		})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, http.StatusInternalServerError, gen.ErrorResponse{Error: gen.APIError{
			Code: "internal_error", Message: "internal server error", RequestId: requestID,
		}})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error"})
}

func (h *Handler) live(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "live", "time": h.now().UTC()})
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	if !h.runtimeReady() {
		writeError(w, r, http.StatusServiceUnavailable, "not_ready", "runtime is not ready", nil)
		return
	}
	var (
		status StatusSnapshot
		err    error
	)
	if reader, ok := h.reader.(readinessStatusReader); ok {
		status, err = reader.ReadinessStatus(r.Context())
	} else {
		status, err = h.reader.Status(r.Context())
	}
	if err != nil || !status.CoreReady {
		writeError(w, r, http.StatusServiceUnavailable, "not_ready", "core index is not ready", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.reader.Status(r.Context())
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	coverageStart, coverageEnd := quantity(snapshot.CoverageStart), quantity(snapshot.CoverageEnd)
	meta.CoverageStart, meta.CoverageEnd = &coverageStart, &coverageEnd
	writeJSON(w, http.StatusOK, gen.StatusResponse{
		Data: statusModel(h.cfg.Chain.ID, snapshot),
		Meta: meta,
	})
}

func (h *Handler) publicConfig(w http.ResponseWriter, r *http.Request) {
	features := map[string]bool{
		"trace":            h.cfg.Features.Trace,
		"mempool":          h.cfg.Features.Mempool,
		"historical_state": h.cfg.Features.HistoricalState,
		"verification":     h.verificationSubmitter != nil && h.verificationTargets != nil,
		"sourcify":         h.cfg.Features.Sourcify,
		"nft_metadata":     h.cfg.Features.NFTMetadata,
		"pricing":          h.cfg.Features.Pricing,
		"user_auth":        h.cfg.Features.UserAuth,
		"user_api_keys":    h.cfg.Features.UserAPIKeys,
		"x402_billing":     h.cfg.Features.X402Billing,
	}
	data := gen.PublicConfig{
		ChainId:        quantity(h.cfg.Chain.ID),
		ChainName:      h.cfg.Chain.Name,
		NativeSymbol:   h.cfg.Chain.NativeSymbol,
		NativeName:     h.cfg.Chain.NativeName,
		NativeDecimals: int(h.cfg.Chain.NativeDecimals),
		Features:       features,
	}
	if len(h.cfg.Wallet.AddChain.RPCURLs) > 0 {
		var blockExplorerURLs, iconURLs *[]string
		if len(h.cfg.Wallet.AddChain.BlockExplorerURLs) > 0 {
			values := slices.Clone(h.cfg.Wallet.AddChain.BlockExplorerURLs)
			blockExplorerURLs = &values
		}
		if len(h.cfg.Wallet.AddChain.IconURLs) > 0 {
			values := slices.Clone(h.cfg.Wallet.AddChain.IconURLs)
			iconURLs = &values
		}
		data.WalletAddChain = &gen.WalletAddChainConfig{
			ChainId: quantity(h.cfg.Chain.ID), ChainName: h.cfg.Chain.Name,
			NativeCurrency: gen.WalletNativeCurrency{
				Name: h.cfg.Chain.NativeName, Symbol: h.cfg.Chain.NativeSymbol,
				Decimals: int(h.cfg.Chain.NativeDecimals),
			},
			RpcUrls:           slices.Clone(h.cfg.Wallet.AddChain.RPCURLs),
			BlockExplorerUrls: blockExplorerURLs, IconUrls: iconURLs,
		}
	}
	writeJSON(w, http.StatusOK, gen.PublicConfigResponse{Data: data, Meta: h.meta(r)})
}

func (h *Handler) blocks(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(w, r, 25)
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maximumOpaqueCursorLength {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is too long", nil)
		return
	}
	items, next, err := h.reader.Blocks(r.Context(), cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, gen.BlockListResponse{Data: items, Meta: meta})
}

func (h *Handler) block(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validBlockID(id) {
		writeError(w, r, http.StatusBadRequest, "invalid_block_id", "block id must be a decimal/hex number or block hash", nil)
		return
	}
	item, err := h.reader.Block(r.Context(), id)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.BlockResponse{Data: item, Meta: h.meta(r)})
}

func (h *Handler) blockTransactions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validBlockID(id) {
		writeError(w, r, http.StatusBadRequest, "invalid_block_id", "block id must be a decimal/hex number or block hash", nil)
		return
	}
	limit, ok := parseLimit(w, r, 25)
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maximumOpaqueCursorLength {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is too long", nil)
		return
	}
	items, next, err := h.reader.BlockTransactions(r.Context(), id, cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, gen.TransactionListResponse{Data: items, Meta: meta})
}

func (h *Handler) genesisAccounts(w http.ResponseWriter, r *http.Request) {
	if h.genesis == nil {
		h.handleReaderError(w, r, NewCapabilityUnavailableError(
			"genesis_state", "unavailable", "genesis_state_not_configured",
		))
		return
	}
	limit, ok := parseLimit(w, r, 25)
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maximumOpaqueCursorLength {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is too long", nil)
		return
	}
	items, next, err := h.genesis.GenesisAccounts(r.Context(), cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, gen.GenesisAccountListResponse{Data: items, Meta: meta})
}

func (h *Handler) transaction(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !hashPattern.MatchString(hash) {
		writeError(w, r, http.StatusBadRequest, "invalid_transaction_hash", "transaction hash must be 32 bytes", nil)
		return
	}
	item, err := h.transactionReader.Transaction(r.Context(), strings.ToLower(hash))
	if err == nil {
		detail, modelErr := includedTransactionDetail(item)
		if modelErr != nil {
			h.logger.ErrorContext(r.Context(), "encode included transaction detail", "request_id", requestIDFrom(r.Context()))
			writeError(w, r, http.StatusInternalServerError, "query_failed", "query failed", nil)
			return
		}
		writeJSON(w, http.StatusOK, gen.TransactionResponse{Data: detail, Meta: h.meta(r)})
		return
	}
	if !errors.Is(err, ErrNotFound) {
		h.handleReaderError(w, r, err)
		return
	}
	if !h.cfg.Features.Mempool {
		h.handleReaderError(w, r, ErrNotFound)
		return
	}
	if h.mempool == nil {
		h.handleMempoolError(w, r, mempool.CapabilityError{State: mempool.StateUnavailable, Code: "reader_unavailable"})
		return
	}
	mempoolDetail, err := h.mempool.Lookup(r.Context(), strings.ToLower(hash))
	if err != nil {
		if errors.Is(err, mempool.ErrNotFound) {
			h.handleReaderError(w, r, ErrNotFound)
			return
		}
		h.handleMempoolError(w, r, err)
		return
	}
	detail, err := mempoolTransactionDetail(mempoolDetail)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "encode mempool transaction detail", "request_id", requestIDFrom(r.Context()))
		writeError(w, r, http.StatusInternalServerError, "query_failed", "query failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, gen.TransactionResponse{Data: detail, Meta: h.meta(r)})
}

func includedTransactionDetail(transaction gen.Transaction) (gen.TransactionDetail, error) {
	var detail gen.TransactionDetail
	err := detail.FromIncludedTransactionDetail(gen.IncludedTransactionDetail{
		Kind: gen.IncludedTransactionDetailKindIncluded, Transaction: transaction,
	})
	return detail, err
}

func mempoolTransactionDetail(detail mempool.Detail) (gen.TransactionDetail, error) {
	var model gen.TransactionDetail
	switch detail.Kind {
	case mempool.DetailPending:
		err := model.FromPendingTransactionDetail(gen.PendingTransactionDetail{
			Kind: gen.PendingTransactionDetailKindPending, Transaction: pendingTransactionModel(detail.Transaction),
		})
		return model, err
	case mempool.DetailReplaced:
		err := model.FromReplacedTransactionDetail(gen.ReplacedTransactionDetail{
			Kind: gen.ReplacedTransactionDetailKindReplaced, Transaction: pendingTransactionModel(detail.Transaction),
			ReplacementHash: detail.ReplacementHash, ReplacedAt: detail.ReplacedAt.UTC(),
		})
		return model, err
	default:
		return gen.TransactionDetail{}, fmt.Errorf("unknown mempool transaction detail kind %q", detail.Kind)
	}
}

func (h *Handler) transactions(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(w, r, 25)
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maximumOpaqueCursorLength {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is too long", nil)
		return
	}
	items, next, err := h.reader.Transactions(r.Context(), cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, gen.TransactionListResponse{Data: items, Meta: meta})
}

func (h *Handler) pendingTransactions(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(w, r, 25)
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maximumOpaqueCursorLength {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is too long", nil)
		return
	}
	if h.mempool == nil {
		state, reason := mempool.StateUnavailable, "feature_disabled"
		if h.cfg.Features.Mempool {
			reason = "reader_unavailable"
		}
		writeError(w, r, http.StatusServiceUnavailable, "mempool_unavailable", "pending transaction capability is unavailable", map[string]any{
			"state": state, "reason": reason,
		})
		return
	}
	page, err := h.mempool.Pending(r.Context(), cursor, limit)
	if err != nil {
		h.handleMempoolError(w, r, err)
		return
	}
	items := make([]gen.PendingTransaction, len(page.Items))
	for index := range page.Items {
		items[index] = pendingTransactionModel(page.Items[index])
	}
	meta := gen.PendingMeta{
		Capability: gen.StageStateComplete,
		ChainId:    strconv.FormatUint(h.cfg.Chain.ID, 10), Endpoint: page.Snapshot.Endpoint,
		ExpiresAt: page.Snapshot.ExpiresAt.UTC(), RequestId: requestIDFrom(r.Context()),
		SnapshotAt: page.Snapshot.ObservedAt.UTC(), SnapshotId: strconv.FormatInt(page.Snapshot.ID, 10),
		TransactionCount: strconv.Itoa(page.Snapshot.TransactionCount),
	}
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, gen.PendingTransactionListResponse{Data: items, Meta: meta})
}

func pendingTransactionModel(transaction mempool.Transaction) gen.PendingTransaction {
	model := gen.PendingTransaction{
		Endpoint: transaction.Endpoint, ExpiresAt: transaction.ExpiresAt.UTC(),
		FirstSeenAt: transaction.FirstSeenAt.UTC(), LastSeenAt: transaction.LastSeenAt.UTC(),
		Hash: transaction.Hash, From: transaction.From, Nonce: transaction.Nonce,
		Value: transaction.Value, Gas: transaction.Gas, Input: transaction.Input,
	}
	model.To = (*gen.Address)(transaction.To)
	model.GasPrice = (*gen.Quantity)(transaction.GasPrice)
	model.MaxFeePerGas = (*gen.Quantity)(transaction.MaxFeePerGas)
	model.MaxPriorityFeePerGas = (*gen.Quantity)(transaction.MaxPriorityFeePerGas)
	model.Type = (*gen.Quantity)(transaction.Type)
	model.ReplacesHash = (*gen.Hash)(transaction.ReplacesHash)
	return model
}

func (h *Handler) handleMempoolError(w http.ResponseWriter, r *http.Request, err error) {
	var capability mempool.CapabilityError
	switch {
	case errors.As(err, &capability):
		details := map[string]any{"state": capability.State, "reason": capability.Code}
		if !capability.LastAttemptAt.IsZero() {
			details["last_attempt_at"] = capability.LastAttemptAt.UTC()
		}
		writeError(w, r, http.StatusServiceUnavailable, "mempool_unavailable", "pending transaction capability is unavailable", details)
	case errors.Is(err, mempool.ErrInvalidCursor):
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or its pending snapshot expired", nil)
	default:
		h.logger.ErrorContext(r.Context(), "mempool query failed", "request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
		writeError(w, r, http.StatusInternalServerError, "query_failed", "query failed", nil)
	}
}

func (h *Handler) address(w http.ResponseWriter, r *http.Request) {
	address := r.PathValue("address")
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_address", "address must be 20 bytes", nil)
		return
	}
	item, err := h.reader.Address(r.Context(), strings.ToLower(address))
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.AddressResponse{Data: item, Meta: h.meta(r)})
}

func (h *Handler) addressTransactions(w http.ResponseWriter, r *http.Request) {
	address, limit, cursor, ok := h.addressActivityPage(w, r)
	if !ok {
		return
	}
	if h.addressActivities == nil {
		h.handleReaderError(w, r, ErrUnavailable)
		return
	}
	items, next, err := h.addressActivities.AddressTransactions(r.Context(), address, cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, gen.TransactionListResponse{Data: items, Meta: meta})
}

func (h *Handler) addressWithdrawals(w http.ResponseWriter, r *http.Request) {
	address, limit, cursor, ok := h.addressActivityPage(w, r)
	if !ok {
		return
	}
	if h.addressActivities == nil {
		h.handleReaderError(w, r, ErrUnavailable)
		return
	}
	items, next, err := h.addressActivities.AddressWithdrawals(r.Context(), address, cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, gen.AddressWithdrawalListResponse{Data: items, Meta: meta})
}

func (h *Handler) addressInternalTransactions(w http.ResponseWriter, r *http.Request) {
	address, limit, cursor, ok := h.addressActivityPage(w, r)
	if !ok {
		return
	}
	if h.addressEnrichment == nil {
		h.handleCatalogError(w, r, catalog.StageUnavailableError{
			Stage: catalog.StageTrace, State: catalog.StageUnavailable,
		})
		return
	}
	page, err := h.addressEnrichment.AddressInternalTransactions(r.Context(), catalog.AddressActivityRequest{
		ChainID: h.chainID(), Address: address, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.AddressInternalTransaction, len(page.Items))
	for index := range page.Items {
		item := page.Items[index]
		path := make([]int, len(item.Path))
		for pathIndex := range item.Path {
			path[pathIndex] = int(item.Path[pathIndex])
		}
		items[index] = gen.AddressInternalTransaction{
			BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
			BlockTimestamp: item.BlockTimestamp.UTC(), TransactionHash: item.TransactionHash,
			TransactionIndex: item.TransactionIndex, Path: path, Depth: int(item.Depth),
			CallType: item.CallType, From: item.From, To: item.To,
			CreatedAddress: item.CreatedAddress, Value: item.Value, Gas: item.Gas,
			GasUsed: item.GasUsed, Input: item.Input, Error: item.Error, Reverted: item.Reverted,
		}
	}
	writeJSON(w, http.StatusOK, gen.AddressInternalTransactionListResponse{
		Data: items, Meta: h.catalogPageMeta(r, page.NextCursor, page.Snapshot),
	})
}

func (h *Handler) addressERC20Transfers(w http.ResponseWriter, r *http.Request) {
	h.addressTokenTransfers(w, r, false)
}

func (h *Handler) addressNFTTransfers(w http.ResponseWriter, r *http.Request) {
	h.addressTokenTransfers(w, r, true)
}

func (h *Handler) addressTokenTransfers(w http.ResponseWriter, r *http.Request, nft bool) {
	address, limit, cursor, ok := h.addressActivityPage(w, r)
	if !ok {
		return
	}
	if h.addressEnrichment == nil {
		h.handleCatalogError(w, r, catalog.StageUnavailableError{
			Stage: catalog.StageToken, State: catalog.StageUnavailable,
		})
		return
	}
	request := catalog.AddressActivityRequest{
		ChainID: h.chainID(), Address: address, Cursor: cursor, Limit: limit,
	}
	var page catalog.AddressTokenTransferPage
	var err error
	if nft {
		page, err = h.addressEnrichment.AddressNFTTransfers(r.Context(), request)
	} else {
		page, err = h.addressEnrichment.AddressERC20Transfers(r.Context(), request)
	}
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.AddressTokenTransfer, len(page.Items))
	for index := range page.Items {
		item := page.Items[index]
		items[index] = gen.AddressTokenTransfer{
			BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
			BlockTimestamp: item.BlockTimestamp.UTC(), TransactionHash: item.TransactionHash,
			TransactionIndex: item.TransactionIndex, LogIndex: item.LogIndex,
			SubIndex: item.SubIndex, TokenAddress: item.TokenAddress,
			Standard: gen.AddressTokenTransferStandard(item.Standard),
			Kind:     gen.AddressTokenTransferKind(item.Kind),
			From:     item.From, To: item.To, TokenId: item.TokenID,
			Amount: item.Amount, Confidence: item.Confidence,
		}
		if item.Decimals != nil {
			value := int(*item.Decimals)
			items[index].Decimals = &value
		}
	}
	writeJSON(w, http.StatusOK, gen.AddressTokenTransferListResponse{
		Data: items, Meta: h.catalogPageMeta(r, page.NextCursor, page.Snapshot),
	})
}

func (h *Handler) addressActivityPage(
	w http.ResponseWriter,
	r *http.Request,
) (string, int, string, bool) {
	address, ok := parseAddressPath(w, r)
	if !ok {
		return "", 0, "", false
	}
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return "", 0, "", false
	}
	return address, limit, cursor, true
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len(query) > 256 {
		writeError(w, r, http.StatusBadRequest, "invalid_query", "q must contain 1 to 256 bytes", nil)
		return
	}
	limit, ok := parseLimit(w, r, 20)
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maximumOpaqueCursorLength {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is too long", nil)
		return
	}
	items, next, err := h.reader.Search(r.Context(), query, cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, gen.SearchResponse{Data: items, Meta: meta})
}

func (h *Handler) tokens(w http.ResponseWriter, r *http.Request) {
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.TokenContracts(r.Context(), catalog.TokenListRequest{
		ChainID: h.chainID(), Cursor: cursor, Limit: limit,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.TokenContract, len(page.Items))
	for index := range page.Items {
		items[index] = tokenContractModel(page.Items[index])
	}
	meta := h.catalogPageMeta(r, page.NextCursor, page.Snapshot)
	writeJSON(w, http.StatusOK, gen.TokenListResponse{Data: items, Meta: meta})
}

func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	address, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	item, err := h.catalog.TokenContract(r.Context(), h.chainID(), address)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.TokenResponse{Data: tokenContractModel(item), Meta: h.meta(r)})
}

func (h *Handler) tokenTransfers(w http.ResponseWriter, r *http.Request) {
	address, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.TokenEvents(r.Context(), catalog.TokenEventRequest{
		ChainID: h.chainID(), TokenAddress: address, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.TokenEvent, len(page.Items))
	for index := range page.Items {
		items[index] = tokenEventModel(page.Items[index])
	}
	meta := h.catalogPageMeta(r, page.NextCursor, page.Snapshot)
	writeJSON(w, http.StatusOK, gen.TokenEventListResponse{Data: items, Meta: meta})
}

func (h *Handler) nftOwner(w http.ResponseWriter, r *http.Request) {
	address, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	tokenID := r.PathValue("token_id")
	if !canonicalQuantity(tokenID) {
		writeError(w, r, http.StatusBadRequest, "invalid_token_id", "token_id must be a canonical decimal uint256", nil)
		return
	}
	item, err := h.catalog.NFTOwner(r.Context(), h.chainID(), address, tokenID)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.NFTOwnershipResponse{Data: nftOwnershipModel(item), Meta: h.meta(r)})
}

func (h *Handler) nftMedia(w http.ResponseWriter, r *http.Request) {
	setNFTMediaHeaders(w)
	w.Header().Set("X-Etherview-Media-State", "unauthorized")
	if !h.requireAPIKey(w, r, auth.ScopeRead) {
		return
	}
	if h.nftMediaSource == nil || h.nftMediaProxy == nil {
		writeNFTMediaError(w, r, http.StatusServiceUnavailable, "disabled", "nft_media_disabled", "NFT media proxy is unavailable")
		return
	}

	address, ok := parseAddressPath(w, r)
	if !ok {
		w.Header().Set("X-Etherview-Media-State", "invalid")
		return
	}
	parsedAddress, err := ethrpc.ParseAddress(address)
	if err != nil {
		writeNFTMediaError(w, r, http.StatusBadRequest, "invalid", "invalid_address", "address must be 20 bytes")
		return
	}
	tokenID := r.PathValue("token_id")
	if !canonicalQuantity(tokenID) {
		writeNFTMediaError(w, r, http.StatusBadRequest, "invalid", "invalid_token_id", "token_id must be a canonical decimal uint256")
		return
	}

	selection, err := h.nftMediaSource.SelectNFTImage(r.Context(), parsedAddress, tokenID)
	if err != nil {
		if h.handleNFTMediaSourceError(w, r, err) {
			return
		}
		h.logger.ErrorContext(r.Context(), "NFT media source query failed",
			"request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
		writeNFTMediaError(w, r, http.StatusInternalServerError, "error", "nft_media_query_failed", "NFT media lookup failed")
		return
	}

	proxied, err := h.nftMediaProxy.Fetch(r.Context(), selection.URI)
	if err != nil {
		h.handleNFTMediaFetchError(w, r, err)
		return
	}
	extension, ok := nftMediaExtension(proxied.ContentType)
	if !ok || len(proxied.Body) == 0 || !proxied.NoStore {
		h.logger.ErrorContext(r.Context(), "NFT media proxy returned invalid output",
			"request_id", requestIDFrom(r.Context()))
		writeNFTMediaError(w, r, http.StatusBadGateway, "error", "nft_media_fetch_failed", "NFT media could not be fetched safely")
		return
	}
	current, err := h.nftMediaSource.NFTImageCurrent(r.Context(), parsedAddress, tokenID, selection)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "NFT media canonicality recheck failed",
			"request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
		writeNFTMediaError(w, r, http.StatusInternalServerError, "error", "nft_media_query_failed", "NFT media lookup failed")
		return
	}
	if !current {
		writeNFTMediaError(w, r, http.StatusConflict, "noncanonical", "nft_media_noncanonical", "NFT metadata changed while media was fetched")
		return
	}

	w.Header().Set("Content-Type", proxied.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(proxied.Body)))
	w.Header().Set("Content-Disposition", `inline; filename="nft-media.`+extension+`"`)
	w.Header().Set("X-Etherview-Media-State", "available")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(proxied.Body)
}

func (h *Handler) handleNFTMediaSourceError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case errors.Is(err, metadata.ErrMediaSourceNotFound):
		writeNFTMediaError(w, r, http.StatusNotFound, "not_found", "nft_metadata_not_found", "canonical NFT metadata was not found")
	case errors.Is(err, metadata.ErrMediaImageNotFound):
		writeNFTMediaError(w, r, http.StatusNotFound, "not_found", "nft_media_not_found", "canonical NFT metadata has no image")
	case errors.Is(err, metadata.ErrMediaSourcePending):
		w.Header().Set("Retry-After", "30")
		writeNFTMediaError(w, r, http.StatusServiceUnavailable, "pending", "nft_metadata_pending", "NFT metadata is still pending")
	case errors.Is(err, metadata.ErrMediaSourceUnavailable):
		writeNFTMediaError(w, r, http.StatusServiceUnavailable, "unavailable", "nft_media_unavailable", "NFT media is unavailable")
	case errors.Is(err, metadata.ErrMediaSourceError):
		writeNFTMediaError(w, r, http.StatusServiceUnavailable, "error", "nft_metadata_error", "NFT metadata processing failed")
	case errors.Is(err, metadata.ErrMediaSourceNoncanonical):
		writeNFTMediaError(w, r, http.StatusConflict, "noncanonical", "nft_media_noncanonical", "NFT metadata exists only for a noncanonical block")
	case errors.Is(err, metadata.ErrMediaSourceUnsafe):
		writeNFTMediaError(w, r, http.StatusUnprocessableEntity, "unsafe", "nft_media_unsafe", "NFT media source is unsafe")
	default:
		return false
	}
	return true
}

func (h *Handler) handleNFTMediaFetchError(w http.ResponseWriter, r *http.Request, err error) {
	var fetchError *metadata.FetchError
	if !errors.As(err, &fetchError) {
		h.logger.ErrorContext(r.Context(), "NFT media fetch failed",
			"request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
		writeNFTMediaError(w, r, http.StatusBadGateway, "error", "nft_media_fetch_failed", "NFT media could not be fetched safely")
		return
	}
	switch fetchError.Kind {
	case metadata.FailureUnsafeURL, metadata.FailureUnsafeContent:
		writeNFTMediaError(w, r, http.StatusUnprocessableEntity, "unsafe", "nft_media_unsafe", "NFT media source or content is unsafe")
	case metadata.FailureUnavailable:
		writeNFTMediaError(w, r, http.StatusBadGateway, "unavailable", "nft_media_unavailable", "NFT media is unavailable")
	case metadata.FailureTemporary:
		w.Header().Set("Retry-After", "30")
		writeNFTMediaError(w, r, http.StatusServiceUnavailable, "temporary", "nft_media_temporary_unavailable", "NFT media is temporarily unavailable")
	case metadata.FailureTooLarge:
		writeNFTMediaError(w, r, http.StatusRequestEntityTooLarge, "too_large", "nft_media_too_large", "NFT media exceeds the configured size limit")
	case metadata.FailureInvalid:
		writeNFTMediaError(w, r, http.StatusUnprocessableEntity, "unsafe", "nft_media_invalid", "NFT media response is invalid")
	default:
		writeNFTMediaError(w, r, http.StatusBadGateway, "error", "nft_media_fetch_failed", "NFT media could not be fetched safely")
	}
}

func setNFTMediaHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox; frame-ancestors 'none'")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// NFTMediaSecurityMiddleware applies the media boundary headers before
// authentication and rate limiting can reject a request. This keeps every
// response for the fixed media route no-store and hostile-content-safe.
func NFTMediaSecurityMiddleware(next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isNFTMediaPath(r.URL.Path) {
			setNFTMediaHeaders(w)
			w.Header().Set("X-Etherview-Media-State", "unauthorized")
		}
		next.ServeHTTP(w, r)
	})
}

func isNFTMediaPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) == 6 && parts[0] == "api" && parts[1] == "v1" &&
		parts[2] == "nfts" && parts[3] != "" && parts[4] != "" && parts[5] == "media"
}

func writeNFTMediaError(w http.ResponseWriter, r *http.Request, status int, state, code, message string) {
	w.Header().Set("X-Etherview-Media-State", state)
	writeError(w, r, status, code, message, nil)
}

func nftMediaExtension(contentType string) (string, bool) {
	switch contentType {
	case "image/png":
		return "png", true
	case "image/jpeg":
		return "jpg", true
	case "image/gif":
		return "gif", true
	case "image/webp":
		return "webp", true
	case "image/avif":
		return "avif", true
	default:
		return "", false
	}
}

func (h *Handler) nftBalances(w http.ResponseWriter, r *http.Request) {
	owner, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.NFTBalances(r.Context(), catalog.NFTBalanceRequest{
		ChainID: h.chainID(), Owner: owner, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.NFTBalance, len(page.Items))
	for index := range page.Items {
		items[index] = nftBalanceModel(page.Items[index])
	}
	meta := h.catalogPageMeta(r, page.NextCursor, page.Snapshot)
	writeJSON(w, http.StatusOK, gen.NFTBalanceListResponse{Data: items, Meta: meta})
}

func (h *Handler) erc20Balances(w http.ResponseWriter, r *http.Request) {
	owner, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.ERC20Balances(r.Context(), catalog.ERC20BalanceRequest{
		ChainID: h.chainID(), Owner: owner, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.ERC20Balance, len(page.Items))
	for index, item := range page.Items {
		var decimals *int
		if item.Decimals != nil {
			value := int(*item.Decimals)
			decimals = &value
		}
		items[index] = gen.ERC20Balance{
			ChainId: item.ChainID, Owner: item.Owner,
			TokenAddress: item.TokenAddress, Balance: item.Balance,
			Confidence: gen.StateConfidence(item.Confidence),
			Name:       item.Name, Symbol: item.Symbol, Decimals: decimals,
		}
	}
	writeJSON(w, http.StatusOK, gen.ERC20BalanceListResponse{
		Data: items, Meta: h.catalogPageMeta(r, page.NextCursor, page.Snapshot),
	})
}

func (h *Handler) blockStats(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from_block"), r.URL.Query().Get("to_block")
	if !canonicalQuantity(from) || !canonicalQuantity(to) {
		writeError(w, r, http.StatusBadRequest, "invalid_block_range", "from_block and to_block must be canonical decimal uint256 values", nil)
		return
	}
	items, err := h.catalog.BlockStats(r.Context(), catalog.BlockStatsRequest{
		ChainID: h.chainID(), FromBlock: from, ToBlock: to,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	models := make([]gen.BlockStat, len(items))
	for index := range items {
		models[index] = blockStatModel(items[index])
	}
	meta := h.meta(r)
	meta.CoverageStart, meta.CoverageEnd = &from, &to
	writeJSON(w, http.StatusOK, gen.BlockStatListResponse{Data: models, Meta: meta})
}

func (h *Handler) aggregateStats(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from_block"), r.URL.Query().Get("to_block")
	if !canonicalQuantity(from) || !canonicalQuantity(to) {
		writeError(w, r, http.StatusBadRequest, "invalid_block_range", "from_block and to_block must be canonical decimal uint256 values", nil)
		return
	}
	item, err := h.catalog.AggregateStats(r.Context(), catalog.AggregateStatsRequest{
		ChainID: h.chainID(), FromBlock: from, ToBlock: to,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	meta := h.catalogPageMeta(r, "", item.Snapshot)
	meta.CoverageStart, meta.CoverageEnd = &from, &to
	writeJSON(w, http.StatusOK, gen.AggregateStatsResponse{Data: aggregateStatsModel(item), Meta: meta})
}

func (h *Handler) chartOverview(w http.ResponseWriter, r *http.Request) {
	if h.analytics == nil {
		writeError(w, r, http.StatusServiceUnavailable, "analytics_pending", "historical analytics are still being rebuilt", nil)
		return
	}
	item, err := h.analytics.Overview(r.Context(), h.chainID(), h.now().UTC())
	if err != nil {
		h.handleAnalyticsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.ChartOverviewResponse{
		Data: chartOverviewModel(item),
		Meta: h.meta(r),
	})
}

func (h *Handler) chartMetric(w http.ResponseWriter, r *http.Request) {
	if h.analytics == nil {
		writeError(w, r, http.StatusServiceUnavailable, "analytics_pending", "historical analytics are still being rebuilt", nil)
		return
	}
	metric, ok := analytics.ParseMetric(r.PathValue("metric"))
	if !ok {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_chart_metric", "metric is not supported", nil)
		return
	}
	from, fromErr := time.Parse(time.RFC3339, r.URL.Query().Get("from_time"))
	to, toErr := time.Parse(time.RFC3339, r.URL.Query().Get("to_time"))
	intervalText := r.URL.Query().Get("interval")
	if intervalText == "" {
		intervalText = string(analytics.IntervalAuto)
	}
	interval, intervalOK := analytics.ParseInterval(intervalText)
	if fromErr != nil || toErr != nil || !intervalOK {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_chart_range", "chart times or interval are invalid", nil)
		return
	}
	item, err := h.analytics.Detail(r.Context(), analytics.DetailRequest{
		ChainID: h.chainID(), Metric: metric, From: from, To: to,
		Interval: interval, Now: h.now().UTC(),
	})
	if err != nil {
		h.handleAnalyticsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.ChartMetricResponse{
		Data: chartSeriesModel(item),
		Meta: h.meta(r),
	})
}

func (h *Handler) handleAnalyticsError(w http.ResponseWriter, r *http.Request, err error) {
	var pending analytics.PendingError
	switch {
	case errors.As(err, &pending), errors.Is(err, analytics.ErrPending):
		details := map[string]any{"state": "pending"}
		if errors.As(err, &pending) {
			details["coverage"] = chartCoverageModel(pending.Coverage)
		}
		writeError(w, r, http.StatusServiceUnavailable, "analytics_pending", "historical analytics are still being rebuilt", details)
	case errors.Is(err, analytics.ErrInvalidInput):
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_chart_range", "chart range exceeds the supported point limit or is invalid", nil)
	case errors.Is(err, analytics.ErrCorruptData):
		writeError(w, r, http.StatusServiceUnavailable, "analytics_inconsistent", "historical analytics are temporarily unavailable", nil)
	default:
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
	}
}

func (h *Handler) transactionTrace(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(r.PathValue("hash"))
	if !hashPattern.MatchString(hash) {
		writeError(w, r, http.StatusBadRequest, "invalid_transaction_hash", "transaction hash must be 32 bytes", nil)
		return
	}
	item, err := h.catalog.TransactionTrace(r.Context(), h.chainID(), hash)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.TransactionTraceResponse{Data: transactionTraceModel(item), Meta: h.meta(r)})
}

func (h *Handler) transactionCalldata(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(r.PathValue("hash"))
	if !hashPattern.MatchString(hash) {
		writeError(w, r, http.StatusBadRequest, "invalid_transaction_hash", "transaction hash must be 32 bytes", nil)
		return
	}
	item, err := h.catalog.TransactionCalldata(r.Context(), h.chainID(), hash)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.TransactionCalldataResponse{Data: transactionCalldataModel(item), Meta: h.meta(r)})
}

func (h *Handler) transactionFailure(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(r.PathValue("hash"))
	if !hashPattern.MatchString(hash) {
		writeError(w, r, http.StatusBadRequest, "invalid_transaction_hash", "transaction hash must be 32 bytes", nil)
		return
	}
	item, err := h.catalog.TransactionFailure(r.Context(), h.chainID(), hash)
	if err != nil {
		switch {
		case errors.Is(err, catalog.ErrNotApplicable):
			writeError(w, r, http.StatusUnprocessableEntity, "failure_not_applicable", "transaction failure decoding is not applicable", nil)
		case errors.Is(err, catalog.ErrCorruptData):
			writeError(w, r, http.StatusServiceUnavailable, "failure_inconsistent", "transaction failure data is temporarily unavailable", nil)
		default:
			h.handleCatalogError(w, r, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, gen.TransactionFailureResponse{Data: transactionFailureModel(item), Meta: h.meta(r)})
}

func (h *Handler) transactionTokenTransfers(w http.ResponseWriter, r *http.Request) {
	request, ok := h.transactionResourceRequest(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.TransactionTokenEvents(r.Context(), request)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.TokenEvent, len(page.Items))
	for index := range page.Items {
		items[index] = tokenEventModel(page.Items[index])
	}
	meta := h.meta(r)
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, gen.TransactionTokenTransferResponse{
		Data: transactionTokenTransfersModel(page.Identity, items), Meta: meta,
	})
}

func (h *Handler) transactionInternalTransactions(w http.ResponseWriter, r *http.Request) {
	request, ok := h.transactionResourceRequest(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.TransactionInternalTransactions(r.Context(), request)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.TransactionInternalTransaction, len(page.Items))
	for index, item := range page.Items {
		path := make([]int, len(item.Path))
		for pathIndex, value := range item.Path {
			path[pathIndex] = int(value)
		}
		items[index] = gen.TransactionInternalTransaction{
			Path: path, Depth: int(item.Depth), CallType: item.CallType,
			From: item.From, To: item.To, CreatedAddress: item.CreatedAddress, Value: item.Value,
		}
	}
	meta := h.meta(r)
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, gen.TransactionInternalTransactionResponse{
		Data: transactionInternalTransactionsModel(page.Identity, items), Meta: meta,
	})
}

func (h *Handler) transactionLogs(w http.ResponseWriter, r *http.Request) {
	request, ok := h.transactionResourceRequest(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.TransactionLogs(r.Context(), request)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.TransactionLog, len(page.Items))
	for index := range page.Items {
		item := page.Items[index]
		topics := make([]gen.Hash, len(item.Topics))
		copy(topics, item.Topics)
		items[index] = gen.TransactionLog{
			Address: item.Address, LogIndex: item.LogIndex, Topics: topics, Data: item.Data,
			Decoding: transactionLogDecodingModel(item.Decoding),
		}
	}
	meta := h.meta(r)
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, gen.TransactionLogResponse{
		Data: transactionLogsModel(page.Identity, items), Meta: meta,
	})
}

func transactionLogDecodingModel(value catalog.TransactionLogDecoding) gen.TransactionLogDecoding {
	if value.Status == "" {
		value.Status = "unavailable"
	}
	model := gen.TransactionLogDecoding{
		Status:     gen.TransactionLogDecodingStatus(value.Status),
		Arguments:  make([]gen.TransactionLogArgument, len(value.Arguments)),
		Candidates: make([]string, len(value.Candidates)),
		Attribution: gen.TransactionLogAttribution{
			Mode:      gen.TransactionLogAttributionMode(value.Attribution.Mode),
			TracePath: uint32PathModel(value.Attribution.TracePath),
		},
	}
	copy(model.Candidates, value.Candidates)
	for index, argument := range value.Arguments {
		model.Arguments[index] = gen.TransactionLogArgument{
			Name: argument.Name, Type: argument.Type, Indexed: argument.Indexed,
			Hashed: argument.Hashed, Value: argument.Value,
		}
	}
	if value.EventName != "" {
		model.EventName = &value.EventName
	}
	if value.Signature != "" {
		model.Signature = &value.Signature
	}
	if value.Confidence != "" {
		confidence := gen.TransactionLogDecodingConfidence(value.Confidence)
		model.Confidence = &confidence
	}
	if value.Warning != "" {
		model.Warning = &value.Warning
	}
	if value.ABISource != nil {
		model.AbiSource = abiSourceModel(value.ABISource)
	}
	if value.Attribution.ExecutionAddress != "" {
		address := gen.Address(value.Attribution.ExecutionAddress)
		model.Attribution.ExecutionAddress = &address
	}
	return model
}

func abiSourceModel(value *catalog.ABISource) *gen.ABISource {
	if value == nil {
		return nil
	}
	result := &gen.ABISource{Kind: gen.ABISourceKind(value.Kind)}
	if value.Address != "" {
		address := gen.Address(value.Address)
		result.Address = &address
	}
	if value.CodeHash != "" {
		codeHash := gen.Hash(value.CodeHash)
		result.CodeHash = &codeHash
	}
	return result
}

func uint32PathModel(path []uint32) []int {
	result := make([]int, len(path))
	for index, component := range path {
		result[index] = int(component)
	}
	return result
}

func (h *Handler) transactionStateChanges(w http.ResponseWriter, r *http.Request) {
	request, ok := h.transactionResourceRequest(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.TransactionStateChanges(r.Context(), request)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.TransactionStateChange, len(page.Items))
	for index := range page.Items {
		item := page.Items[index]
		items[index] = gen.TransactionStateChange{
			Address: item.Address, Kind: gen.TransactionStateChangeKind(item.Kind),
			StorageKey: item.StorageKey, Before: item.Before, After: item.After,
		}
	}
	meta := h.meta(r)
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, gen.TransactionStateChangeResponse{
		Data: transactionStateChangesModel(page.Identity, items), Meta: meta,
	})
}

func (h *Handler) transactionAuthorizations(w http.ResponseWriter, r *http.Request) {
	reader, exists := h.catalog.(delegationCatalogReader)
	if !exists {
		writeError(w, r, http.StatusServiceUnavailable, "capability_unavailable", "authorization history is unavailable", nil)
		return
	}
	request, ok := h.transactionResourceRequest(w, r)
	if !ok {
		return
	}
	page, err := reader.TransactionAuthorizations(r.Context(), request)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.EIP7702Authorization, len(page.Items))
	for index, item := range page.Items {
		items[index] = gen.EIP7702Authorization{
			Index: item.Index, ChainId: item.ChainID, Nonce: item.Nonce,
			Delegate: item.Delegate, YParity: item.YParity, R: item.R, S: item.S,
			SignatureStatus:   gen.EIP7702AuthorizationSignatureStatus(item.SignatureStatus),
			ApplicationStatus: gen.EIP7702AuthorizationApplicationStatus(item.ApplicationStatus),
		}
		items[index].Authority = item.Authority
		if item.SkipReason != nil {
			reason := gen.EIP7702AuthorizationSkipReason(*item.SkipReason)
			items[index].SkipReason = &reason
		}
	}
	meta := h.meta(r)
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, gen.TransactionAuthorizationResponse{
		Data: gen.TransactionAuthorizations{
			ChainId: page.Identity.ChainID, BlockNumber: page.Identity.BlockNumber,
			BlockHash: page.Identity.BlockHash, TransactionHash: page.Identity.TransactionHash,
			TransactionIndex: page.Identity.TransactionIndex,
			State:            gen.TransactionAuthorizationsState(page.Identity.State), Items: items,
		},
		Meta: meta,
	})
}

func (h *Handler) addressDelegations(w http.ResponseWriter, r *http.Request) {
	reader, exists := h.catalog.(delegationCatalogReader)
	if !exists {
		writeError(w, r, http.StatusServiceUnavailable, "capability_unavailable", "delegation history is unavailable", nil)
		return
	}
	address := r.PathValue("address")
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_address", "address must be 20 bytes", nil)
		return
	}
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return
	}
	page, err := reader.AddressDelegations(r.Context(), catalog.AddressDelegationRequest{
		ChainID: h.chainID(), Address: address, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.DelegationHistoryItem, len(page.Items))
	for index, item := range page.Items {
		items[index] = gen.DelegationHistoryItem{
			Authority: item.Authority, Kind: gen.DelegationHistoryItemKind(item.Kind),
			Delegate: item.Delegate, PreviousDelegate: item.PreviousDelegate,
			BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
			TransactionHash: item.TransactionHash, TransactionIndex: item.TransactionIndex,
			AuthorizationIndex: item.AuthorizationIndex,
		}
	}
	meta := h.meta(r)
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, gen.DelegationHistoryResponse{Data: items, Meta: meta})
}

func (h *Handler) addressDelegation(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.reader.(DelegationBindingReader)
	if !ok {
		writeError(w, r, http.StatusServiceUnavailable, "capability_unavailable", "delegation state is unavailable", nil)
		return
	}
	address := r.PathValue("address")
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_address", "address must be 20 bytes", nil)
		return
	}
	binding, err := reader.AddressDelegation(r.Context(), address)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.DelegationBindingResponse{Data: binding, Meta: h.meta(r)})
}

func (h *Handler) transactionResourceRequest(
	w http.ResponseWriter,
	r *http.Request,
) (catalog.TransactionResourceRequest, bool) {
	hash := strings.ToLower(r.PathValue("hash"))
	if !hashPattern.MatchString(hash) {
		writeError(w, r, http.StatusBadRequest, "invalid_transaction_hash", "transaction hash must be 32 bytes", nil)
		return catalog.TransactionResourceRequest{}, false
	}
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return catalog.TransactionResourceRequest{}, false
	}
	return catalog.TransactionResourceRequest{
		ChainID: h.chainID(), TransactionHash: hash, Cursor: cursor, Limit: limit,
	}, true
}

type verifierSubmission struct {
	Language           verify.Language       `json:"language"`
	CompilerVersion    string                `json:"compiler_version"`
	InputKind          string                `json:"input_kind"`
	Input              json.RawMessage       `json:"input"`
	Sources            map[string]string     `json:"sources"`
	EVMVersion         string                `json:"evm_version"`
	OptimizationRuns   *int                  `json:"optimization_runs"`
	Libraries          map[string]string     `json:"libraries"`
	Bytecodes          *verify.BytecodePair  `json:"bytecodes"`
	Contracts          []verify.BytecodePair `json:"contracts"`
	ContractNameHint   string                `json:"contract_name_hint"`
	RuntimeEntrypoint  string                `json:"runtime_entrypoint"`
	CreationEntrypoint string                `json:"creation_entrypoint"`
}

func (h *Handler) submitAddressVerification(w http.ResponseWriter, r *http.Request) {
	if h.verificationSubmitter == nil || h.verificationTargets == nil {
		writeError(w, r, http.StatusServiceUnavailable, "verification_unavailable", "contract verification submission is unavailable", nil)
		return
	}
	if !h.requireAPIKey(w, r, auth.ScopeVerification) {
		return
	}
	address := strings.ToLower(r.PathValue("address"))
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "address verification request is invalid", nil)
		return
	}
	var submission verifierSubmission
	if !h.decodeBoundedJSON(w, r, &submission, "invalid_verification_request", "address verification request is invalid") {
		return
	}
	if submission.Bytecodes != nil || submission.Contracts != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "address verification request is invalid", nil)
		return
	}
	target, err := h.verificationTargets.ResolveVerificationTarget(r.Context(), address)
	if err != nil || target.ChainID != h.cfg.Chain.ID || !strings.EqualFold(target.Address, address) {
		h.handleVerificationTargetError(w, r, err)
		return
	}
	request := verify.SubmissionV2{
		Kind: verify.JobAddress, Language: submission.Language,
		CompilerVersion: submission.CompilerVersion, ContractNameHint: submission.ContractNameHint,
		Target: &target, Bytecodes: []verify.BytecodePair{{
			Creation: target.CreationBytecode, Runtime: target.RuntimeBytecode,
		}},
	}
	switch submission.InputKind {
	case "standard_json":
		if submission.Language == verify.LanguageGeas || len(submission.Sources) != 0 ||
			submission.RuntimeEntrypoint != "" || submission.CreationEntrypoint != "" {
			writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "address verification request is invalid", nil)
			return
		}
		request.StandardJSON = submission.Input
	case "multipart":
		if submission.Language == verify.LanguageGeas || len(submission.Input) != 0 ||
			submission.RuntimeEntrypoint != "" || submission.CreationEntrypoint != "" {
			writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "address verification request is invalid", nil)
			return
		}
		request.Multipart = &verify.MultipartRequest{
			Language: submission.Language, Sources: submission.Sources,
			EVMVersion: submission.EVMVersion, OptimizationRuns: submission.OptimizationRuns,
			Libraries: submission.Libraries,
		}
	case "geas_sources":
		if submission.Language != verify.LanguageGeas || len(submission.Input) != 0 ||
			submission.EVMVersion != "" || submission.OptimizationRuns != nil ||
			len(submission.Libraries) != 0 {
			writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "address verification request is invalid", nil)
			return
		}
		request.Geas = &verify.GeasRequest{
			Sources: submission.Sources, RuntimeEntrypoint: submission.RuntimeEntrypoint,
			CreationEntrypoint: submission.CreationEntrypoint,
		}
	default:
		writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "input_kind must be multipart, standard_json, or geas_sources", nil)
		return
	}
	h.submitV2(w, r, request)
}

func (h *Handler) submitVerifier(w http.ResponseWriter, r *http.Request) {
	if h.verificationSubmitter == nil {
		writeError(w, r, http.StatusServiceUnavailable, "verification_unavailable", "contract verification submission is unavailable", nil)
		return
	}
	if !h.requireAPIKey(w, r, auth.ScopeVerification) {
		return
	}
	if r.Pattern == "POST /api/v1/verifier/sourcify" {
		var submission struct {
			ChainID string            `json:"chain_id"`
			Address string            `json:"address"`
			Files   map[string]string `json:"files"`
		}
		if !h.decodeBoundedJSON(w, r, &submission, "invalid_verification_request", "Sourcify request is invalid") {
			return
		}
		encoded, _ := json.Marshal(submission)
		h.submitV2(w, r, verify.SubmissionV2{
			Kind: verify.JobSourcify, SourcifyRequest: encoded,
		})
		return
	}
	if r.Pattern == "POST /api/v1/verifier/sourcify/from-etherscan" {
		var submission struct {
			ChainID string `json:"chain_id"`
			Address string `json:"address"`
		}
		if !h.decodeBoundedJSON(w, r, &submission, "invalid_verification_request", "Sourcify Etherscan request is invalid") {
			return
		}
		encoded, _ := json.Marshal(submission)
		h.submitV2(w, r, verify.SubmissionV2{
			Kind: verify.JobSourcifyFromEtherscan, SourcifyRequest: encoded,
		})
		return
	}
	var submission verifierSubmission
	if !h.decodeBoundedJSON(w, r, &submission, "invalid_verification_request", "verifier request is invalid") {
		return
	}
	request := verify.SubmissionV2{
		CompilerVersion:  submission.CompilerVersion,
		ContractNameHint: submission.ContractNameHint,
	}
	var bytecodes verify.BytecodePair
	if submission.Bytecodes != nil {
		bytecodes = *submission.Bytecodes
	}
	switch r.Pattern {
	case "POST /api/v1/verifier/solidity/multipart":
		request.Kind, request.Language = verify.JobSolidityMultipart, submission.Language
		if request.Language == "" {
			request.Language = verify.LanguageSolidity
		}
		request.Multipart = multipartSubmission(request.Language, submission)
		request.Bytecodes = []verify.BytecodePair{bytecodes}
	case "POST /api/v1/verifier/solidity/standard-json":
		request.Kind, request.Language = verify.JobSolidityStandardJSON, submission.Language
		if request.Language == "" {
			request.Language = verify.LanguageSolidity
		}
		request.StandardJSON = submission.Input
		request.Bytecodes = []verify.BytecodePair{bytecodes}
	case "POST /api/v1/verifier/solidity/batch/multipart":
		request.Kind, request.Language = verify.JobSolidityBatchMultipart, submission.Language
		if request.Language == "" {
			request.Language = verify.LanguageSolidity
		}
		request.Multipart = multipartSubmission(request.Language, submission)
		request.Bytecodes = submission.Contracts
	case "POST /api/v1/verifier/solidity/batch/standard-json":
		request.Kind, request.Language = verify.JobSolidityBatchStandardJSON, submission.Language
		if request.Language == "" {
			request.Language = verify.LanguageSolidity
		}
		request.StandardJSON = submission.Input
		request.Bytecodes = submission.Contracts
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "verifier route not found", nil)
		return
	}
	h.submitV2(w, r, request)
}

func multipartSubmission(language verify.Language, submission verifierSubmission) *verify.MultipartRequest {
	return &verify.MultipartRequest{
		Language: language, Sources: submission.Sources, EVMVersion: submission.EVMVersion,
		OptimizationRuns: submission.OptimizationRuns, Libraries: submission.Libraries,
	}
}

func (h *Handler) submitV2(w http.ResponseWriter, r *http.Request, request verify.SubmissionV2) {
	job, _, err := h.verificationSubmitter.SubmitV2(r.Context(), request)
	if err != nil {
		h.handleVerificationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, gen.VerificationJobResponse{Data: verificationJobModel(job), Meta: h.meta(r)})
}

func (h *Handler) verifierCompilers(w http.ResponseWriter, r *http.Request) {
	language := verify.Language(r.URL.Query().Get("language"))
	if language != verify.LanguageSolidity && language != verify.LanguageYul &&
		language != verify.LanguageGeas {
		writeError(w, r, http.StatusBadRequest, "invalid_language", "language must be solidity, yul, or geas", nil)
		return
	}
	if language == verify.LanguageGeas {
		writeJSON(w, http.StatusOK, gen.CompilerCatalogResponse{
			Data: gen.CompilerCatalog{
				Language: gen.VerifierLanguage(language),
				Versions: []string{verify.GeasCompilerVersion},
			},
			Meta: h.meta(r),
		})
		return
	}
	if h.compilerCatalog == nil {
		writeError(w, r, http.StatusServiceUnavailable, "compiler_catalog_unavailable", "compiler catalog is unavailable", nil)
		return
	}
	versions, err := h.compilerCatalog.Versions(r.Context(), language)
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "compiler_catalog_unavailable", "compiler catalog is unavailable", nil)
		return
	}
	writeJSON(w, http.StatusOK, gen.CompilerCatalogResponse{
		Data: gen.CompilerCatalog{Language: gen.VerifierLanguage(language), Versions: versions},
		Meta: h.meta(r),
	})
}

func (h *Handler) lookupVerifierMethods(w http.ResponseWriter, r *http.Request) {
	var request verify.MethodLookupRequest
	if !h.decodeBoundedJSON(w, r, &request, "invalid_method_lookup", "method lookup request is invalid") {
		return
	}
	methods, err := verify.LookupMethods(request)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_method_lookup", "method lookup request is invalid", nil)
		return
	}
	models := make([]gen.MethodSource, 0, len(methods))
	for _, method := range methods {
		models = append(models, gen.MethodSource{
			Selector: method.Selector, Signature: method.Signature, FileName: method.FileName,
			Offset: method.Offset, Length: method.Length,
		})
	}
	writeJSON(w, http.StatusOK, gen.LookupMethodsResponse{
		Data: gen.LookupMethods{Methods: models}, Meta: h.meta(r),
	})
}

func (h *Handler) verificationJob(w http.ResponseWriter, r *http.Request) {
	if !h.verificationReadAvailable(w, r) {
		return
	}
	if !h.requireAPIKey(w, r, auth.ScopeVerification) {
		return
	}
	job, found, err := h.verificationReader.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		h.handleVerificationError(w, r, err)
		return
	}
	if !found {
		writeError(w, r, http.StatusNotFound, "not_found", "verification job not found", nil)
		return
	}
	writeJSON(w, http.StatusOK, gen.VerificationJobResponse{Data: verificationJobModel(job), Meta: h.meta(r)})
}

func (h *Handler) verifiedContract(w http.ResponseWriter, r *http.Request) {
	if _, ok := parseExactQuery(w, r); !ok {
		return
	}
	if !h.verificationReadAvailable(w, r) {
		return
	}
	address := strings.ToLower(r.PathValue("address"))
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_contract_identity", "address must be a fixed-size hexadecimal value", nil)
		return
	}
	contract, found, err := h.verificationReader.VerifiedContract(
		r.Context(), h.cfg.Chain.ID, address,
	)
	if err != nil {
		h.handleVerificationError(w, r, err)
		return
	}
	if !found {
		writeError(w, r, http.StatusNotFound, "not_found", "verified contract not found", nil)
		return
	}
	model, err := verifiedContractModel(contract)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "decode verified contract", "request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
		writeError(w, r, http.StatusInternalServerError, "query_failed", "verified contract is invalid", nil)
		return
	}
	writeJSON(w, http.StatusOK, gen.VerifiedContractResponse{Data: model, Meta: h.meta(r)})
}

func (h *Handler) contractProxy(w http.ResponseWriter, r *http.Request) {
	address, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	if _, ok := parseExactQuery(w, r); !ok {
		return
	}
	if h.proxyReader == nil {
		writeError(w, r, http.StatusServiceUnavailable, "proxy_unavailable", "proxy details are unavailable", nil)
		return
	}
	detail, err := h.proxyReader.Proxy(r.Context(), address)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.ProxyDetailsResponse{Data: detail, Meta: h.meta(r)})
}

func (h *Handler) contractProxyUpgrades(w http.ResponseWriter, r *http.Request) {
	h.contractProxyHistory(w, r, true)
}

func (h *Handler) contractProxyInitializations(w http.ResponseWriter, r *http.Request) {
	h.contractProxyHistory(w, r, false)
}

func (h *Handler) contractDiamondCuts(w http.ResponseWriter, r *http.Request) {
	address, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	values, ok := parseExactQuery(w, r, "cursor", "limit")
	if !ok {
		return
	}
	cursor := ""
	if items, present := values["cursor"]; present {
		cursor = items[0]
	}
	if len(cursor) > maximumOpaqueCursorLength || (values.Has("cursor") && cursor == "") {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or too long", nil)
		return
	}
	limit, ok := parseExactLimit(w, r, values, 20)
	if !ok {
		return
	}
	if h.proxyReader == nil {
		writeError(w, r, http.StatusServiceUnavailable, "proxy_unavailable", "Diamond history is unavailable", nil)
		return
	}
	page, next, err := h.proxyReader.DiamondCuts(r.Context(), address, cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.DiamondCutHistoryResponse{
		Data: page, Meta: pageMeta(h.meta(r), next),
	})
}

func (h *Handler) contractProxyHistory(w http.ResponseWriter, r *http.Request, upgrades bool) {
	address, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	values, ok := parseExactQuery(w, r, "cursor", "limit")
	if !ok {
		return
	}
	cursor := ""
	if items, present := values["cursor"]; present {
		cursor = items[0]
	}
	if len(cursor) > maximumOpaqueCursorLength || (values.Has("cursor") && cursor == "") {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or too long", nil)
		return
	}
	limit, ok := parseExactLimit(w, r, values, 20)
	if !ok {
		return
	}
	if h.proxyReader == nil {
		writeError(w, r, http.StatusServiceUnavailable, "proxy_unavailable", "proxy history is unavailable", nil)
		return
	}
	if upgrades {
		page, next, err := h.proxyReader.ProxyUpgrades(r.Context(), address, cursor, limit)
		if err != nil {
			h.handleReaderError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, gen.ProxyUpgradeHistoryResponse{
			Data: page, Meta: pageMeta(h.meta(r), next),
		})
		return
	}
	page, next, err := h.proxyReader.ProxyInitializations(r.Context(), address, cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.ProxyInitializationHistoryResponse{
		Data: page, Meta: pageMeta(h.meta(r), next),
	})
}

func pageMeta(meta gen.Meta, next string) gen.Meta {
	if next != "" {
		meta.NextCursor = &next
	}
	return meta
}

func parseExactQuery(w http.ResponseWriter, r *http.Request, allowed ...string) (url.Values, bool) {
	if len(r.URL.RawQuery) > maximumNativeQueryBytes {
		writeError(w, r, http.StatusBadRequest, "invalid_query", "query parameters are invalid", nil)
		return nil, false
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_query", "query parameters are invalid", nil)
		return nil, false
	}
	allowlist := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowlist[name] = struct{}{}
	}
	for name, items := range values {
		if _, exists := allowlist[name]; !exists || name == "" || len(items) != 1 {
			writeError(w, r, http.StatusBadRequest, "invalid_query", "query parameters are invalid", nil)
			return nil, false
		}
	}
	return values, true
}

func parseExactLimit(w http.ResponseWriter, r *http.Request, values url.Values, defaultValue int) (int, bool) {
	items, present := values["limit"]
	if !present {
		return defaultValue, true
	}
	raw := items[0]
	value, err := strconv.Atoi(raw)
	if err != nil || strconv.Itoa(value) != raw || value < 1 || value > maximumPageSize {
		writeError(w, r, http.StatusBadRequest, "invalid_limit", fmt.Sprintf("limit must be between 1 and %d", maximumPageSize), nil)
		return 0, false
	}
	return value, true
}

func (h *Handler) verificationReadAvailable(w http.ResponseWriter, r *http.Request) bool {
	if h.verificationReader != nil {
		return true
	}
	writeError(w, r, http.StatusServiceUnavailable, "verification_unavailable", "contract verification is unavailable", nil)
	return false
}

func (h *Handler) decodeBoundedJSON(w http.ResponseWriter, r *http.Request, destination any, code, message string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxVerificationBody)
	body, err := io.ReadAll(r.Body)
	if err != nil || verify.ValidateUniqueJSON(body) != nil {
		writeError(w, r, http.StatusBadRequest, code, message, nil)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, r, http.StatusBadRequest, code, message, nil)
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, code, message, nil)
		return false
	}
	return true
}

func (h *Handler) handleVerificationTargetError(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.ErrorContext(r.Context(), "resolve verification target", "request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
	writeError(w, r, http.StatusServiceUnavailable, "verification_target_unavailable", "canonical contract code or creation facts are unavailable", nil)
}

func (h *Handler) requireAPIKey(w http.ResponseWriter, r *http.Request, scope auth.Scope) bool {
	identity := auth.IdentityFrom(r.Context())
	if identity.Authenticated {
		if identity.HasScope(scope) {
			return true
		}
		writeError(w, r, http.StatusForbidden, "api_key_scope_required", "API key scope does not authorize this operation", nil)
		return false
	}
	if operation, ok := billing.PaidOperationFrom(r.Context()); ok {
		spec, exists := apiops.Lookup(operation)
		if exists && spec.BillingEligible && spec.MuxPattern == r.Pattern {
			return true
		}
	}
	writeError(w, r, http.StatusUnauthorized, "api_key_required", "an API key is required", nil)
	return false
}

func requiredAPIScope(operation string) auth.Scope {
	switch operation {
	case "getVerifierJob", "getVerifiedContract", "submitAddressVerification",
		"verifySolidityMultipart", "verifySolidityStandardJson",
		"batchVerifySolidityMultipart", "batchVerifySolidityStandardJson",
		"listVerifierCompilers", "lookupVerifierMethods",
		"submitSourcifyVerification", "submitSourcifyFromEtherscan":
		return auth.ScopeVerification
	default:
		return auth.ScopeRead
	}
}

func operationUsesAPIKeyScope(operation string) bool {
	switch operation {
	case "createAuthChallenge", "verifyAuthChallenge", "getAuthSession",
		"logoutAuthSession", "updateCurrentUser", "listCurrentUserAPIKeys",
		"createCurrentUserAPIKey", "rotateCurrentUserAPIKey",
		"revokeCurrentUserAPIKey", "listAdminUsers", "updateAdminUser",
		"revokeAdminUserSessions", "getBillingConfig",
		"listCurrentUserBillingPayments", "listAdminBillingPayments",
		"getAdminBillingSummary":
		return false
	default:
		return true
	}
}

func (h *Handler) handleVerificationError(w http.ResponseWriter, r *http.Request, err error) {
	var serviceError verify.ServiceError
	if errors.As(err, &serviceError) && serviceError.Code == verify.ServiceInvalidRequest {
		writeError(w, r, http.StatusBadRequest, "invalid_verification_request", serviceError.Error(), nil)
		return
	}
	h.logger.ErrorContext(r.Context(), "verification request failed", "request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
	writeError(w, r, http.StatusInternalServerError, "verification_failed", "verification service failed", nil)
}

func verificationJobModel(job verify.VerificationJob) gen.VerificationJob {
	id, _ := uuid.Parse(job.ID)
	kind := gen.VerificationJobKind(job.Kind)
	if job.Kind == "" {
		kind = gen.VerificationJobKindAddress
	}
	model := gen.VerificationJob{
		Id: id, Kind: kind, Status: gen.VerificationJobStatus(job.Status),
		CreatedAt: job.CreatedAt.UTC(), UpdatedAt: job.UpdatedAt.UTC(),
	}
	if len(job.Outcome) > 0 {
		var outcome gen.VerificationOutcome
		if json.Unmarshal(job.Outcome, &outcome) == nil {
			model.Outcome = &outcome
		}
	}
	if job.ErrorCode != "" {
		value := string(job.ErrorCode)
		model.ErrorCode = &value
	}
	return model
}

func verifiedContractModel(contract verify.VerifiedContract) (gen.VerifiedContract, error) {
	var abi []map[string]any
	var sources, settings, compilation, creationArtifacts, runtimeArtifacts map[string]any
	targetAddress, err := checksumAddress(contract.Target.Address)
	if err != nil {
		return gen.VerifiedContract{}, fmt.Errorf("checksum artifact target address: %w", err)
	}
	sourceAddress, err := checksumAddress(contract.Source.Address)
	if err != nil {
		return gen.VerifiedContract{}, fmt.Errorf("checksum artifact source address: %w", err)
	}
	if err := json.Unmarshal(contract.ABI, &abi); err != nil {
		return gen.VerifiedContract{}, err
	}
	if err := json.Unmarshal(contract.Sources, &sources); err != nil {
		return gen.VerifiedContract{}, err
	}
	if err := json.Unmarshal(contract.Settings, &settings); err != nil {
		return gen.VerifiedContract{}, err
	}
	if len(contract.CompilationArtifacts) == 0 {
		contract.CompilationArtifacts = json.RawMessage(`{}`)
	}
	if len(contract.CreationCodeArtifacts) == 0 {
		contract.CreationCodeArtifacts = json.RawMessage(`{}`)
	}
	if len(contract.RuntimeCodeArtifacts) == 0 {
		contract.RuntimeCodeArtifacts = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(contract.CompilationArtifacts, &compilation); err != nil {
		return gen.VerifiedContract{}, err
	}
	if err := json.Unmarshal(contract.CreationCodeArtifacts, &creationArtifacts); err != nil {
		return gen.VerifiedContract{}, err
	}
	if err := json.Unmarshal(contract.RuntimeCodeArtifacts, &runtimeArtifacts); err != nil {
		return gen.VerifiedContract{}, err
	}
	fileName := contract.FileName
	if fileName == "" {
		fileName = "unknown"
	}
	model := gen.VerifiedContract{
		Resolution: gen.VerifiedContractResolution(contract.Resolution),
		Target: gen.ContractArtifactTarget{
			ChainId: strconv.FormatUint(contract.Target.ChainID, 10),
			Address: targetAddress, CodeHash: contract.Target.CodeHash,
			BlockNumber: strconv.FormatUint(contract.Target.BlockNumber, 10),
			BlockHash:   contract.Target.BlockHash,
		},
		Source: gen.ContractArtifactSource{
			Address: sourceAddress, CodeHash: contract.Source.CodeHash,
			ValidFromBlock: strconv.FormatUint(contract.Source.ValidFromBlock, 10),
			CreatedAt:      contract.Source.CreatedAt.UTC(),
		},
		Kind: gen.VerifiedContractKindVerificationSuccess, Language: gen.VerifierLanguage(contract.Language),
		CompilerVersion: contract.CompilerVersion, FileName: fileName,
		ContractName: contract.ContractName, Abi: &abi, Sources: sources, Settings: settings,
		CompilationArtifacts: compilation, CreationCodeArtifacts: creationArtifacts,
		RuntimeCodeArtifacts: runtimeArtifacts, CreationMatch: matchDetailsModel(contract.CreationMatch),
		RuntimeMatch: matchDetailsModel(contract.RuntimeMatch), Libraries: contract.Libraries,
		IsBlueprint: contract.IsBlueprint,
	}
	if model.Libraries == nil {
		model.Libraries = map[string]string{}
	}
	if contract.ConstructorArguments != "" {
		value := contract.ConstructorArguments
		model.ConstructorArguments = &value
	}
	if contract.Source.ValidToBlock != nil {
		value := strconv.FormatUint(*contract.Source.ValidToBlock, 10)
		model.Source.ValidToBlock = &value
	}
	return model, nil
}

func matchDetailsModel(details *verify.VerificationMatchDetails) *gen.VerificationMatchDetails {
	if details == nil {
		return nil
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return nil
	}
	var model gen.VerificationMatchDetails
	if json.Unmarshal(encoded, &model) != nil {
		return nil
	}
	if model.Transformations == nil {
		model.Transformations = make([]gen.VerificationTransformation, 0)
	}
	return &model
}

func checksumAddress(value string) (string, error) {
	address, err := ethrpc.ParseAddress(value)
	if err != nil {
		return "", err
	}
	return common.Address(address).Hex(), nil
}

func (h *Handler) handleReaderError(w http.ResponseWriter, r *http.Request, err error) {
	var capability *CapabilityUnavailableError
	switch {
	case errors.As(err, &capability) && capability.valid():
		writeError(w, r, http.StatusServiceUnavailable, "capability_unavailable", "required capability is unavailable", map[string]any{
			"capability": capability.Capability,
			"state":      capability.State,
			"code":       capability.Code,
		})
	case errors.Is(err, ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
	case errors.Is(err, ErrUnavailable):
		writeError(w, r, http.StatusServiceUnavailable, "capability_unavailable", "required capability is unavailable", nil)
	case errors.Is(err, ErrNotReady):
		writeError(w, r, http.StatusServiceUnavailable, "not_ready", "indexed data is not ready", nil)
	case errors.Is(err, ErrInvalidCursor):
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or stale after a canonical change", nil)
	default:
		h.logger.ErrorContext(r.Context(), "query failed", "request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
		writeError(w, r, http.StatusInternalServerError, "query_failed", "query failed", nil)
	}
}

func (h *Handler) handleCatalogError(w http.ResponseWriter, r *http.Request, err error) {
	var stageError catalog.StageUnavailableError
	switch {
	case errors.As(err, &stageError):
		details := map[string]any{
			"stage": stageError.Stage,
			"state": stageError.State,
		}
		if stageError.BlockNumber != "" {
			details["block_number"] = stageError.BlockNumber
		}
		if stageError.BlockHash != "" {
			details["block_hash"] = stageError.BlockHash
		}
		writeError(w, r, http.StatusServiceUnavailable, "stage_unavailable", "required enrichment stage is unavailable", details)
	case errors.Is(err, catalog.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
	case errors.Is(err, catalog.ErrInvalidCursor):
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or stale after a canonical change", nil)
	case errors.Is(err, catalog.ErrInvalidInput):
		writeError(w, r, http.StatusBadRequest, "invalid_query", "catalog query is invalid", nil)
	case errors.Is(err, catalog.ErrLimitExceeded):
		writeError(w, r, http.StatusUnprocessableEntity, "result_limit_exceeded", "catalog result exceeds the configured safety limit", nil)
	case errors.Is(err, catalog.ErrNotApplicable):
		writeError(w, r, http.StatusUnprocessableEntity, "calldata_not_applicable", "transaction calldata decoding is not applicable", nil)
	default:
		h.logger.ErrorContext(r.Context(), "catalog query failed", "request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
		writeError(w, r, http.StatusInternalServerError, "query_failed", "query failed", nil)
	}
}

func (h *Handler) chainID() string {
	return strconv.FormatUint(h.cfg.Chain.ID, 10)
}

func (h *Handler) catalogPageMeta(r *http.Request, next string, snapshot catalog.Snapshot) gen.Meta {
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	if snapshot.BlockNumber != "" {
		meta.CoverageEnd = &snapshot.BlockNumber
	}
	return meta
}

func parseCatalogPage(w http.ResponseWriter, r *http.Request) (int, string, bool) {
	limit, ok := parseLimit(w, r, 25)
	if !ok {
		return 0, "", false
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maximumOpaqueCursorLength {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is too long", nil)
		return 0, "", false
	}
	return limit, cursor, true
}

func parseAddressPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	address := strings.ToLower(r.PathValue("address"))
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_address", "address must be 20 bytes", nil)
		return "", false
	}
	return address, true
}

func canonicalQuantity(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	integer, ok := new(big.Int).SetString(value, 10)
	if !ok || integer.Sign() < 0 {
		return false
	}
	return integer.BitLen() <= 256
}

func tokenContractModel(item catalog.TokenContract) gen.TokenContract {
	model := gen.TokenContract{
		ChainId: item.ChainID, Address: item.Address, CodeHash: item.CodeHash,
		Standard: gen.TokenContractStandard(item.Standard), Confidence: gen.TokenContractConfidence(item.Confidence),
		Name: item.Name, Symbol: item.Symbol, TotalSupply: item.TotalSupply,
		MetadataState: item.MetadataState, ObservedBlockNumber: item.ObservedBlockNumber,
		ObservedBlockHash: item.ObservedBlockHash, UpdatedAt: item.UpdatedAt.UTC(),
	}
	if item.Decimals != nil {
		value := int(*item.Decimals)
		model.Decimals = &value
	}
	return model
}

func tokenEventModel(item catalog.TokenEvent) gen.TokenEvent {
	model := gen.TokenEvent{
		ChainId: item.ChainID, BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
		LogIndex: item.LogIndex, SubIndex: item.SubIndex, TransactionHash: item.TransactionHash,
		TokenAddress: item.TokenAddress, Standard: item.Standard, Kind: item.Kind,
		Operator: item.Operator, From: item.From, To: item.To, TokenId: item.TokenID,
		Amount: item.Amount, Confidence: item.Confidence,
	}
	if item.Decimals != nil {
		value := int(*item.Decimals)
		model.Decimals = &value
	}
	return model
}

func catalogSnapshotModel(snapshot catalog.Snapshot) gen.CatalogSnapshot {
	return gen.CatalogSnapshot{
		ChainId: snapshot.ChainID, BlockNumber: snapshot.BlockNumber, BlockHash: snapshot.BlockHash,
	}
}

func nftOwnershipModel(item catalog.NFTOwnership) gen.NFTOwnership {
	return gen.NFTOwnership{
		ChainId: item.ChainID, TokenAddress: item.TokenAddress, TokenId: item.TokenID,
		Owner: item.Owner, Balance: item.Balance, Confidence: gen.StateConfidence(item.Confidence),
		Snapshot: catalogSnapshotModel(item.Snapshot),
	}
}

func nftBalanceModel(item catalog.NFTBalance) gen.NFTBalance {
	return gen.NFTBalance{
		ChainId: item.ChainID, Owner: item.Owner, TokenAddress: item.TokenAddress,
		TokenId: item.TokenID, Balance: item.Balance, Confidence: gen.StateConfidence(item.Confidence),
	}
}

func blockStatModel(item catalog.BlockStat) gen.BlockStat {
	return gen.BlockStat{
		ChainId: item.ChainID, BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
		TransactionCount: item.TransactionCount, GasUsed: item.GasUsed, GasLimit: item.GasLimit,
		BaseFeePerGas: item.BaseFeePerGas, BlobGasUsed: item.BlobGasUsed,
		ExcessBlobGas: item.ExcessBlobGas, BlobBaseFeePerGas: item.BlobBaseFeePerGas,
		BurnedWei: item.BurnedWei, BlobBurnedWei: item.BlobBurnedWei,
		BlockTimestamp: item.BlockTimestamp, BlockIntervalSeconds: item.BlockIntervalSeconds,
		TransactionsPerSecond: item.TransactionsPerSecond,
		TokenEventCount:       item.TokenEventCount, TokenTransferCount: item.TokenTransferCount,
		NftTransferCount: item.NFTTransferCount, ComputedAt: item.ComputedAt.UTC(),
	}
}

func aggregateStatsModel(item catalog.AggregateStats) gen.AggregateStats {
	return gen.AggregateStats{
		ChainId: item.ChainID, FromBlock: item.FromBlock, ToBlock: item.ToBlock,
		Snapshot: gen.CatalogSnapshot{
			ChainId: item.Snapshot.ChainID, BlockNumber: item.Snapshot.BlockNumber,
			BlockHash: item.Snapshot.BlockHash,
		},
		BlockCount: item.BlockCount, TransactionCount: item.TransactionCount,
		GasUsed: item.GasUsed, BurnedWei: item.BurnedWei, BlobBurnedWei: item.BlobBurnedWei,
		TokenEventCount: item.TokenEventCount, TokenTransferCount: item.TokenTransferCount,
		NftTransferCount: item.NFTTransferCount, AverageTps: item.AverageTPS,
		Completeness: gen.AggregateStatsCompleteness{
			Core: item.CoreComplete, Stats: item.StatsComplete, Token: item.TokenComplete,
		},
	}
}

func chartOverviewModel(item analytics.Overview) gen.ChartOverview {
	metrics := make([]gen.ChartPreview, len(item.Metrics))
	for index := range item.Metrics {
		preview := item.Metrics[index]
		metrics[index] = gen.ChartPreview{
			Metric: gen.ChartMetric(preview.Metric), CurrentValue: preview.CurrentValue,
			PreviousValue: preview.PreviousValue, ChangePercent: preview.ChangePercent,
			Points: chartPointsModel(preview.Points),
		}
	}
	return gen.ChartOverview{
		GeneratedAt: item.GeneratedAt.UTC(), Snapshot: chartSnapshotModel(item.Snapshot),
		Coverage: chartCoverageModel(item.Coverage), Metrics: metrics, Pending: item.Pending,
	}
}

func chartSeriesModel(item analytics.Series) gen.ChartMetricSeries {
	return gen.ChartMetricSeries{
		Metric: gen.ChartMetric(item.Metric), Interval: gen.ChartInterval(item.Interval),
		FromTime: item.FromTime.UTC(), ToTime: item.ToTime.UTC(),
		Points: chartPointsModel(item.Points),
		Summary: gen.ChartSummary{
			Current: item.Summary.Current, Highest: item.Summary.Highest,
			Lowest: item.Summary.Lowest, Total: item.Summary.Total, Average: item.Summary.Average,
		},
		Snapshot: chartSnapshotModel(item.Snapshot), Coverage: chartCoverageModel(item.Coverage),
	}
}

func chartPointsModel(items []analytics.Point) []gen.ChartPoint {
	result := make([]gen.ChartPoint, len(items))
	for index := range items {
		item := items[index]
		result[index] = gen.ChartPoint{
			BucketStart: item.BucketStart.UTC(), BucketEnd: item.BucketEnd.UTC(),
			Value: item.Value, Partial: item.Partial,
			FromBlock: item.FromBlock, ToBlock: item.ToBlock,
		}
	}
	return result
}

func chartSnapshotModel(item analytics.Snapshot) gen.CatalogSnapshot {
	return gen.CatalogSnapshot{
		ChainId: item.ChainID, BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
	}
}

func chartCoverageModel(item analytics.Coverage) gen.ChartCoverage {
	state := gen.ChartCoverageBackfillStatePartial
	if item.Complete {
		state = gen.ChartCoverageBackfillStateComplete
	} else if item.AvailableFrom == nil {
		state = gen.ChartCoverageBackfillStateEmpty
	}
	return gen.ChartCoverage{
		AvailableFrom: item.AvailableFrom, AvailableTo: item.AvailableTo,
		Complete: item.Complete, DirtyHours: item.DirtyHours,
		BackfillState: state, BackfillProgress: item.Progress,
	}
}

func transactionTraceModel(item catalog.TransactionTrace) gen.TransactionTrace {
	frames := make([]gen.TraceFrame, len(item.Frames))
	for index := range item.Frames {
		frame := item.Frames[index]
		path, parentPath := uint32PathModel(frame.Path), uint32PathModel(frame.ParentPath)
		frames[index] = gen.TraceFrame{
			Path: path, ParentPath: parentPath, Depth: int(frame.Depth), CallType: frame.CallType,
			From: frame.From, To: frame.To, CreatedAddress: frame.CreatedAddress,
			Value: frame.Value, Gas: frame.Gas, GasUsed: frame.GasUsed,
			Input: frame.Input, Output: frame.Output, Error: frame.Error,
			DirectReverted: frame.DirectReverted, Reverted: frame.Reverted,
		}
		if frame.Execution != nil {
			execution := gen.TraceExecution{
				ContextAddress: frame.Execution.ContextAddress,
				Resolution:     gen.TraceExecutionResolution(frame.Execution.Resolution),
			}
			if frame.Execution.Address != "" {
				execution.Address = &frame.Execution.Address
			}
			if frame.Execution.CodeHash != "" {
				execution.CodeHash = &frame.Execution.CodeHash
			}
			frames[index].Execution = execution
		}
		if frame.Decoding != nil {
			frames[index].Decoding = traceCallDecodingModel(frame.Decoding)
		}
	}
	return gen.TransactionTrace{
		ChainId: item.ChainID, BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
		TransactionHash: item.TransactionHash, TransactionIndex: item.TransactionIndex,
		State: gen.TransactionTraceState(item.State), Frames: frames,
	}
}

func transactionCalldataModel(item catalog.TransactionCalldata) gen.TransactionCalldata {
	execution := gen.TransactionExecution{
		ContextAddress: item.Execution.ContextAddress,
		Resolution:     gen.TransactionExecutionResolution(item.Execution.Resolution),
		EvidenceSource: gen.TransactionExecutionEvidenceSource(item.Execution.EvidenceSource),
	}
	if item.Execution.Address != "" {
		execution.Address = &item.Execution.Address
	}
	if item.Execution.CodeHash != "" {
		execution.CodeHash = &item.Execution.CodeHash
	}
	decoding := gen.TransactionCalldataDecoding{
		Status:     gen.TransactionCalldataDecodingStatus(item.Decoding.Status),
		Inputs:     transactionCalldataInputsModel(item.Decoding.Inputs),
		Candidates: append([]string{}, item.Decoding.Candidates...),
		AbiSource:  abiSourceModel(item.Decoding.ABISource),
	}
	if item.Decoding.FunctionName != "" {
		decoding.FunctionName = &item.Decoding.FunctionName
	}
	if item.Decoding.Signature != "" {
		decoding.Signature = &item.Decoding.Signature
	}
	if item.Decoding.Confidence != "" {
		confidence := gen.TransactionCalldataDecodingConfidence(item.Decoding.Confidence)
		decoding.Confidence = &confidence
	}
	if item.Decoding.Warning != "" {
		decoding.Warning = &item.Decoding.Warning
	}
	return gen.TransactionCalldata{
		ChainId: item.Identity.ChainID, BlockNumber: item.Identity.BlockNumber,
		BlockHash: item.Identity.BlockHash, TransactionHash: item.Identity.TransactionHash,
		TransactionIndex: item.Identity.TransactionIndex, State: gen.TransactionCalldataState(item.Identity.State),
		Input: item.Input, Execution: execution, Decoding: decoding,
	}
}

func transactionFailureModel(item catalog.TransactionFailure) gen.TransactionFailure {
	decoding := gen.TransactionFailureDecoding{
		Status:     gen.TransactionFailureDecodingStatus(item.Decoding.Status),
		Arguments:  transactionCalldataInputsModel(item.Decoding.Arguments),
		Candidates: append([]string{}, item.Decoding.Candidates...),
		AbiSource:  abiSourceModel(item.Decoding.ABISource), Reason: item.Decoding.Reason,
	}
	if item.Decoding.ErrorName != "" {
		decoding.ErrorName = &item.Decoding.ErrorName
	}
	if item.Decoding.Signature != "" {
		decoding.Signature = &item.Decoding.Signature
	}
	if item.Decoding.Confidence != "" {
		confidence := gen.TransactionFailureDecodingConfidence(item.Decoding.Confidence)
		decoding.Confidence = &confidence
	}
	if item.Decoding.Warning != "" {
		decoding.Warning = &item.Decoding.Warning
	}
	result := gen.TransactionFailure{
		ChainId: item.Identity.ChainID, BlockNumber: item.Identity.BlockNumber,
		BlockHash: item.Identity.BlockHash, TransactionHash: item.Identity.TransactionHash,
		TransactionIndex: item.Identity.TransactionIndex,
		State:            gen.TransactionFailureState(item.Identity.State), Error: item.Error,
		RevertData: item.RevertData, Decoding: decoding,
	}
	if item.Execution != nil {
		execution := gen.TraceExecution{
			ContextAddress: item.Execution.ContextAddress,
			Resolution:     gen.TraceExecutionResolution(item.Execution.Resolution),
		}
		if item.Execution.Address != "" {
			execution.Address = &item.Execution.Address
		}
		if item.Execution.CodeHash != "" {
			execution.CodeHash = &item.Execution.CodeHash
		}
		result.Execution = &execution
	}
	return result
}

func traceCallDecodingModel(value *catalog.TraceCallDecoding) *gen.TraceCallDecoding {
	if value == nil {
		return nil
	}
	result := &gen.TraceCallDecoding{
		Kind:   gen.TraceCallDecodingKind(value.Kind),
		Status: gen.TraceCallDecodingStatus(value.Status), Inputs: abiValuesModel(value.Inputs),
		OutputStatus: gen.TraceCallDecodingOutputStatus(value.OutputStatus), Outputs: abiValuesModel(value.Outputs),
		Candidates: append([]string{}, value.Candidates...), AbiSource: abiSourceModel(value.ABISource),
	}
	if value.FunctionName != "" {
		result.FunctionName = &value.FunctionName
	}
	if value.Signature != "" {
		result.Signature = &value.Signature
	}
	if value.Confidence != "" {
		confidence := gen.TraceCallDecodingConfidence(value.Confidence)
		result.Confidence = &confidence
	}
	if value.Warning != "" {
		result.Warning = &value.Warning
	}
	if value.Revert != nil {
		result.Revert = traceRevertDecodingModel(value.Revert)
	}
	return result
}

func traceRevertDecodingModel(value *catalog.TraceRevertDecoding) *gen.TraceRevertDecoding {
	if value == nil {
		return nil
	}
	result := &gen.TraceRevertDecoding{
		Status: gen.TraceRevertDecodingStatus(value.Status), Arguments: abiValuesModel(value.Arguments),
		Candidates: append([]string{}, value.Candidates...), AbiSource: abiSourceModel(value.ABISource),
	}
	if value.ErrorName != "" {
		result.ErrorName = &value.ErrorName
	}
	if value.Signature != "" {
		result.Signature = &value.Signature
	}
	if value.Confidence != "" {
		confidence := gen.TraceRevertDecodingConfidence(value.Confidence)
		result.Confidence = &confidence
	}
	if value.Warning != "" {
		result.Warning = &value.Warning
	}
	return result
}

func abiValuesModel(values []catalog.ABIValue) []gen.ABIValue {
	result := make([]gen.ABIValue, len(values))
	for index, value := range values {
		result[index] = gen.ABIValue{Name: value.Name, Type: value.Type, Value: value.Value}
	}
	return result
}

func transactionCalldataInputsModel(values []catalog.TransactionCalldataInput) []gen.TransactionCalldataInput {
	result := make([]gen.TransactionCalldataInput, len(values))
	for index, value := range values {
		result[index] = gen.TransactionCalldataInput{
			Name: value.Name, Type: value.Type, Value: value.Value,
			Components: transactionCalldataParametersModel(value.Components),
		}
		if value.InternalType != "" {
			result[index].InternalType = &value.InternalType
		}
	}
	return result
}

func transactionCalldataParametersModel(
	values []catalog.TransactionCalldataParameter,
) []gen.TransactionCalldataParameter {
	result := make([]gen.TransactionCalldataParameter, len(values))
	for index, value := range values {
		result[index] = gen.TransactionCalldataParameter{
			Name: value.Name, Type: value.Type,
			Components: transactionCalldataParametersModel(value.Components),
		}
		if value.InternalType != "" {
			result[index].InternalType = &value.InternalType
		}
	}
	return result
}

func transactionTokenTransfersModel(
	identity catalog.TransactionResourceIdentity,
	items []gen.TokenEvent,
) gen.TransactionTokenTransfers {
	return gen.TransactionTokenTransfers{
		ChainId: identity.ChainID, BlockNumber: identity.BlockNumber, BlockHash: identity.BlockHash,
		TransactionHash: identity.TransactionHash, TransactionIndex: identity.TransactionIndex,
		State: gen.TransactionTokenTransfersState(identity.State), Items: items,
	}
}

func transactionInternalTransactionsModel(
	identity catalog.TransactionResourceIdentity,
	items []gen.TransactionInternalTransaction,
) gen.TransactionInternalTransactions {
	return gen.TransactionInternalTransactions{
		ChainId: identity.ChainID, BlockNumber: identity.BlockNumber, BlockHash: identity.BlockHash,
		TransactionHash: identity.TransactionHash, TransactionIndex: identity.TransactionIndex,
		State: gen.TransactionInternalTransactionsState(identity.State), Items: items,
	}
}

func transactionLogsModel(
	identity catalog.TransactionResourceIdentity,
	items []gen.TransactionLog,
) gen.TransactionLogs {
	return gen.TransactionLogs{
		ChainId: identity.ChainID, BlockNumber: identity.BlockNumber, BlockHash: identity.BlockHash,
		TransactionHash: identity.TransactionHash, TransactionIndex: identity.TransactionIndex,
		State: gen.TransactionLogsState(identity.State), Items: items,
	}
}

func transactionStateChangesModel(
	identity catalog.TransactionResourceIdentity,
	items []gen.TransactionStateChange,
) gen.TransactionStateChanges {
	return gen.TransactionStateChanges{
		ChainId: identity.ChainID, BlockNumber: identity.BlockNumber, BlockHash: identity.BlockHash,
		TransactionHash: identity.TransactionHash, TransactionIndex: identity.TransactionIndex,
		State: gen.TransactionStateChangesState(identity.State), Items: items,
	}
}

func (h *Handler) meta(r *http.Request) gen.Meta {
	return gen.Meta{RequestId: requestIDFrom(r.Context()), ChainId: quantity(h.cfg.Chain.ID)}
}

type Service struct {
	server          *http.Server
	listen          func(string, string) (net.Listener, error)
	shutdownTimeout time.Duration
	tlsCertFile     string
	tlsKeyFile      string
}

func NewService(cfg config.Config, handler http.Handler, loggers ...*slog.Logger) *Service {
	var logger *slog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	return &Service{
		server: &http.Server{
			Addr:              cfg.Server.Address,
			Handler:           handler,
			ErrorLog:          observability.HTTPServerErrorLog(logger),
			ReadHeaderTimeout: cfg.Server.ReadTimeout,
			ReadTimeout:       cfg.Server.ReadTimeout,
			WriteTimeout:      cfg.Server.WriteTimeout,
			IdleTimeout:       60 * time.Second,
		},
		listen:          net.Listen,
		shutdownTimeout: cfg.Server.ShutdownTimeout,
		tlsCertFile:     cfg.Server.TLSCertFile,
		tlsKeyFile:      cfg.Server.TLSKeyFile,
	}
}

func (s *Service) Name() string { return "http-api" }

func (s *Service) Run(ctx context.Context) error {
	if (s.tlsCertFile == "") != (s.tlsKeyFile == "") {
		return errors.New("API TLS certificate and key must be configured together")
	}
	tlsEnabled := s.tlsCertFile != "" && s.tlsKeyFile != ""
	if tlsEnabled {
		certificate, err := tls.LoadX509KeyPair(s.tlsCertFile, s.tlsKeyFile)
		if err != nil {
			return fmt.Errorf("load API TLS key pair: %w", err)
		}
		s.server.TLSConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{certificate},
		}
	}
	listener, err := s.listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		if tlsEnabled {
			done <- s.server.ServeTLS(listener, "", "")
			return
		}
		done <- s.server.Serve(listener)
	}()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		timeout := s.shutdownTimeout
		if timeout <= 0 {
			timeout = 20 * time.Second
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := s.server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		err := <-done
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return ctx.Err()
	}
}

type requestIDKey struct{}

func requestIDFrom(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey{}).(string)
	return value
}

func randomRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(value[:])
}

func parseLimit(w http.ResponseWriter, r *http.Request, defaultValue int) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultValue, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maximumPageSize {
		writeError(w, r, http.StatusBadRequest, "invalid_limit", fmt.Sprintf("limit must be between 1 and %d", maximumPageSize), nil)
		return 0, false
	}
	return value, true
}

func validBlockID(value string) bool {
	if hashPattern.MatchString(value) {
		return true
	}
	if strings.HasPrefix(value, "0x") {
		if len(value) <= 2 {
			return false
		}
		_, err := strconv.ParseUint(value[2:], 16, 64)
		return err == nil
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	var detailsPointer *map[string]any
	if details != nil {
		detailsPointer = &details
	}
	writeJSON(w, status, gen.ErrorResponse{Error: gen.APIError{
		Code: code, Message: message, Details: detailsPointer, RequestId: requestIDFrom(r.Context()),
	}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func quantity(value uint64) gen.Quantity { return strconv.FormatUint(value, 10) }

func saturatingSub(left, right uint64) uint64 {
	if right >= left {
		return 0
	}
	return left - right
}

func contains(values []string, value string) bool {
	return slices.Contains(values, value)
}

func addVary(header http.Header, value string) {
	values := header.Values("Vary")
	for _, existing := range values {
		for token := range strings.SplitSeq(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(token), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

// EncodeCursor provides a stable, versioned opaque cursor helper for stores.
func EncodeCursor(value any) (string, error) {
	payload, err := json.Marshal(struct {
		Version int `json:"v"`
		Value   any `json:"value"`
	}{Version: 1, Value: value})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	if len(encoded) > maximumOpaqueCursorLength {
		return "", errors.New("cursor exceeds maximum length")
	}
	return encoded, nil
}

// DecodeCursor rejects malformed or unsupported cursor versions.
func DecodeCursor(cursor string, target any) error {
	if len(cursor) == 0 || len(cursor) > maximumOpaqueCursorLength {
		return errors.New("invalid cursor length")
	}
	payload, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return errors.New("invalid cursor encoding")
	}
	var envelope struct {
		Version int             `json:"v"`
		Value   json.RawMessage `json:"value"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || envelope.Version != 1 {
		return errors.New("invalid cursor payload")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("invalid cursor payload")
	}
	if target == nil || len(envelope.Value) == 0 || string(envelope.Value) == "null" {
		return errors.New("cursor target is required")
	}
	decoder = json.NewDecoder(strings.NewReader(string(envelope.Value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid cursor value")
	}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("invalid cursor value")
	}
	return nil
}
