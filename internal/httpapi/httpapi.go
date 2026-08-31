// Package httpapi serves Etherview's native API and embedded SPA.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/analytics"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/apiops"
	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/billing"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/config"
	ensresolver "github.com/islishude/etherview/internal/ens"
	"github.com/islishude/etherview/internal/events"
	"github.com/islishude/etherview/internal/mempool"
	"github.com/islishude/etherview/internal/metadata"
	"github.com/islishude/etherview/internal/observability"
	"github.com/islishude/etherview/internal/publicquery"
	"github.com/islishude/etherview/internal/userauth"
	"github.com/islishude/etherview/internal/verify"
)

var (
	ErrNotFound      = publicquery.ErrNotFound
	ErrUnavailable   = publicquery.ErrUnavailable
	ErrNotReady      = publicquery.ErrNotReady
	ErrInvalidCursor = publicquery.ErrInvalidCursor
	ErrInvalidInput  = publicquery.ErrInvalidInput
)

type CapabilityUnavailableError = publicquery.CapabilityUnavailableError

func NewCapabilityUnavailableError(capability, state, code string) error {
	return publicquery.NewCapabilityUnavailableError(capability, state, code)
}

var (
	hashPattern    = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
	addressPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
)

const (
	maximumOpaqueCursorLength = publicquery.MaximumOpaqueCursorLength
	maximumPageSize           = 100
	maximumNativeQueryBytes   = 4096
)

type StatusSnapshot = publicquery.StatusSnapshot
type Reader = publicquery.Reader

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

type AddressNameReader interface {
	ResolveCurrentPrimary(context.Context, common.Address) (ensresolver.PrimaryResolution, error)
	ResolveAddressBatch(context.Context, []common.Address, string) ([]ensresolver.PrimaryResolution, string, error)
}

type AddressEnrichmentActivityReader interface {
	AddressInternalTransactions(context.Context, catalog.AddressActivityRequest) (catalog.AddressInternalTransactionPage, error)
	AddressERC20Transfers(context.Context, catalog.AddressActivityRequest) (catalog.AddressTokenTransferPage, error)
	AddressNFTTransfers(context.Context, catalog.AddressActivityRequest) (catalog.AddressTokenTransferPage, error)
}

type DelegationBindingReader interface {
	AddressDelegation(context.Context, string) (gen.DelegationBinding, error)
}

type DelegationHistoryReader interface {
	TransactionAuthorizations(context.Context, catalog.TransactionResourceRequest) (catalog.TransactionAuthorizationPage, error)
	AddressDelegations(context.Context, catalog.AddressDelegationRequest) (catalog.DelegationHistoryPage, error)
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
	AddressEnrichment     AddressEnrichmentActivityReader
	AddressNames          AddressNameReader
	DelegationBindings    DelegationBindingReader
	DelegationHistory     DelegationHistoryReader
	Genesis               GenesisReader
	Catalog               catalog.Reader
	Analytics             AnalyticsReader
	Web                   http.Handler
	WebRoutePattern       func(*http.Request) string
	Etherscan             http.Handler
	Metrics               http.Handler
	Events                *events.Broker
	HomeSnapshots         HomeSnapshotSource
	Mempool               mempool.Reader
	NFTMetadataReader     metadata.NFTMetadataReader
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
	BillingReader         BillingReader
	PrepaidBilling        *billing.PrepaidLedger
	TopupBilling          *billing.TopupDispatcher
	Logger                *slog.Logger
	RequestID             func() string
	Now                   func() time.Time
	RuntimeReady          func() bool
	ReadinessStatus       func(context.Context) (StatusSnapshot, error)
	MaxVerificationBody   int64
	Requirements          CapabilityRequirements
}

type Handler struct {
	cfg                   config.Config
	reader                Reader
	transactionReader     TransactionReader
	addressActivities     AddressActivityReader
	addressNames          AddressNameReader
	addressEnrichment     AddressEnrichmentActivityReader
	delegationBindings    DelegationBindingReader
	delegationHistory     DelegationHistoryReader
	genesis               GenesisReader
	catalog               catalog.Reader
	analytics             AnalyticsReader
	web                   http.Handler
	webRoutePattern       func(*http.Request) string
	etherscan             http.Handler
	metrics               http.Handler
	events                *events.Broker
	homeSnapshots         HomeSnapshotSource
	mempool               mempool.Reader
	nftMetadataReader     metadata.NFTMetadataReader
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
	billingReader         BillingReader
	prepaidBilling        *billing.PrepaidLedger
	topupBilling          *billing.TopupDispatcher
	authOrigin            string
	authSecureCookie      bool
	logger                *slog.Logger
	requestID             func() string
	now                   func() time.Time
	runtimeReady          func() bool
	readinessStatus       func(context.Context) (StatusSnapshot, error)
	readinessExplicit     bool
	maxVerificationBody   int64
	requirements          CapabilityRequirements
	mux                   *http.ServeMux
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
		addressEnrichment:     options.AddressEnrichment,
		addressNames:          options.AddressNames,
		delegationBindings:    options.DelegationBindings,
		delegationHistory:     options.DelegationHistory,
		genesis:               options.Genesis,
		catalog:               options.Catalog,
		analytics:             options.Analytics,
		web:                   options.Web,
		webRoutePattern:       options.WebRoutePattern,
		etherscan:             options.Etherscan,
		metrics:               options.Metrics,
		events:                options.Events,
		homeSnapshots:         options.HomeSnapshots,
		mempool:               options.Mempool,
		nftMetadataReader:     options.NFTMetadataReader,
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
		billingReader:         options.BillingReader,
		prepaidBilling:        options.PrepaidBilling,
		topupBilling:          options.TopupBilling,
		logger:                options.Logger,
		requestID:             options.RequestID,
		now:                   options.Now,
		runtimeReady:          options.RuntimeReady,
		readinessStatus:       options.ReadinessStatus,
		readinessExplicit:     options.ReadinessStatus != nil,
		maxVerificationBody:   options.MaxVerificationBody,
		requirements:          options.Requirements,
		mux:                   http.NewServeMux(),
	}
	if h.readinessStatus == nil {
		h.readinessStatus = h.reader.Status
	}
	if h.maxVerificationBody <= 0 {
		h.maxVerificationBody = 6 << 20
	}
	if h.cfg.Features.UserAuth {
		origin, err := userauth.CanonicalPublicOrigin(h.cfg.Server.PublicURL)
		if err != nil {
			return nil, fmt.Errorf("configure user authentication origin: %w", err)
		}
		h.authOrigin = origin
		h.authSecureCookie = strings.HasPrefix(origin, "https://")
	}
	if err := h.routes(); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *Handler) handleBillable(operation string, handler http.HandlerFunc) {
	spec, ok := apiops.Lookup(operation)
	if !ok || !spec.BillingEligible {
		panic("httpapi billable operation is absent from the catalog: " + operation)
	}
	h.mux.Handle(spec.MuxPattern, handler)
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
	if h.webRoutePattern == nil {
		if request.URL.Path == "/" {
			return "/"
		}
		return "unmatched"
	}
	switch pattern = h.webRoutePattern(request); pattern {
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
	// Clear the whole-response deadline before durable replay. Replay is bounded,
	// but it must not consume the stream's future write budget before headers are
	// committed.
	stream, err := newSSEStream(w, h.cfg.Server.WriteTimeout)
	if err != nil {
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
	if err := stream.flush(); err != nil {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case event, open := <-channel:
			if !open {
				return
			}
			if err := stream.write("id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, event.Data); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := stream.write(": heartbeat\n\n"); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

type homeSnapshotResponse struct {
	Data json.RawMessage `json:"data"`
	Meta gen.Meta        `json:"meta"`
}

func (h *Handler) homeSnapshot(w http.ResponseWriter, r *http.Request) {
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
	select {
	case publication, open := <-channel:
		if !open {
			writeError(
				w, r, http.StatusServiceUnavailable,
				"home_snapshot_unavailable", ErrHomeSnapshotUnavailable.Error(), nil,
			)
			return
		}
		encoded, err := h.encodeHomeSnapshotResponse(r, publication)
		if err != nil {
			h.logger.ErrorContext(
				r.Context(), "home snapshot response encoding failed",
				"request_id", requestIDFrom(r.Context()),
			)
			writeError(
				w, r, http.StatusInternalServerError,
				"home_snapshot_encoding_failed", "home snapshot encoding failed", nil,
			)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encoded)
	case <-r.Context().Done():
		return
	}
}

func (h *Handler) homeSnapshotStream(w http.ResponseWriter, r *http.Request) {
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
	stream, err := newSSEStream(w, h.cfg.Server.WriteTimeout)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "stream_unsupported", "streaming is unsupported", nil)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := stream.flush(); err != nil {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case publication, open := <-channel:
			if !open {
				return
			}
			encoded, err := h.encodeHomeSnapshotResponse(r, publication)
			if err != nil {
				h.logger.ErrorContext(
					r.Context(), "home snapshot response encoding failed",
					"request_id", requestIDFrom(r.Context()),
				)
				return
			}
			if err := stream.write(
				"id: %d\nevent: snapshot\ndata: %s\n\n",
				publication.EventID, encoded,
			); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := stream.write(": heartbeat\n\n"); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (h *Handler) encodeHomeSnapshotResponse(
	r *http.Request,
	publication HomePublication,
) ([]byte, error) {
	meta := h.meta(r)
	meta.CoverageStart = &publication.CoverageStart
	meta.CoverageEnd = &publication.CoverageEnd
	encodedData := publication.EncodedData
	var err error
	if len(encodedData) == 0 {
		encodedData, err = json.Marshal(publication.Data)
		if err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(homeSnapshotResponse{Data: encodedData, Meta: meta})
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxHomeSnapshotBytes {
		return nil, errors.New("home snapshot response exceeds encoded size limit")
	}
	return encoded, nil
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
	if request.URL.Path == "/v2/api" {
		if billing.PaymentHeaderPresent(request.Header) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(compatibilityPanicResponse{
				Status: "0", Message: "NOTOK",
				Result: "payment authorization is accepted only for account top-ups",
			})
			return
		}
		h.mux.ServeHTTP(w, request)
		return
	}
	if request.Method == http.MethodPost {
		_, pattern := h.mux.Handler(request)
		if pattern == "POST /api/v1/billing/topup-intents/{id}/pay" {
			h.mux.ServeHTTP(w, request)
			return
		}
	}
	if billing.PaymentHeaderPresent(request.Header) {
		writeError(w, request, http.StatusBadRequest, "unexpected_payment_header", "payment authorization is accepted only for account top-ups", nil)
		return
	}
	spec, catalogMatch := h.matchedOperation(request)
	identity := auth.IdentityFrom(request.Context())
	if catalogMatch && operationUsesAPIKeyScope(string(spec.ID)) && identity.Authenticated &&
		!identity.HasScope(requiredAPIScope(string(spec.ID))) {
		writeError(w, request, http.StatusForbidden, "api_key_scope_required", "API key scope does not authorize this operation", nil)
		return
	}
	h.mux.ServeHTTP(w, request)
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
