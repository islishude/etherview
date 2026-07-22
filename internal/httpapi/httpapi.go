// Package httpapi serves Etherview's native API and embedded SPA.
package httpapi

import (
	"context"
	"crypto/rand"
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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/events"
	"github.com/islishude/etherview/internal/mempool"
	"github.com/islishude/etherview/internal/metadata"
	"github.com/islishude/etherview/internal/observability"
	"github.com/islishude/etherview/internal/verify"
	"golang.org/x/crypto/sha3"
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

const maximumOpaqueCursorLength = 1024

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
	Transactions(context.Context, string, int) ([]gen.Transaction, string, error)
	Transaction(context.Context, string) (gen.Transaction, error)
	Address(context.Context, string) (gen.AddressSummary, error)
	Search(context.Context, string, string, int) ([]gen.SearchResult, string, error)
}

type VerificationReader interface {
	Job(context.Context, string) (verify.VerificationJob, bool, error)
	VerifiedContract(context.Context, uint64, string, string) (verify.VerifiedContract, bool, error)
}

type VerificationSubmitter interface {
	Submit(context.Context, verify.Request) (verify.VerificationJob, bool, error)
}

type VerificationTargetResolver interface {
	ResolveVerificationTarget(context.Context, string) (verify.VerificationTarget, error)
}

type SourcifyAdapter interface {
	Lookup(context.Context, uint64, string) (verify.SourcifyContract, error)
	Import(context.Context, verify.VerificationTarget) (verify.Request, error)
	Submit(context.Context, verify.SourcifyJobReader, string, bool) (verify.SourcifyTicket, error)
	Status(context.Context, string) (verify.SourcifyJob, error)
}

type Options struct {
	Config                config.Config
	Reader                Reader
	Catalog               catalog.Reader
	Web                   http.Handler
	Etherscan             http.Handler
	Metrics               http.Handler
	Events                *events.Broker
	Mempool               mempool.Reader
	NFTMediaSource        metadata.NFTImageSource
	NFTMediaProxy         *metadata.MediaProxy
	VerificationReader    VerificationReader
	VerificationSubmitter VerificationSubmitter
	VerificationTargets   VerificationTargetResolver
	Sourcify              SourcifyAdapter
	Logger                *slog.Logger
	RequestID             func() string
	Now                   func() time.Time
	RuntimeReady          func() bool
	MaxVerificationBody   int64
}

type Handler struct {
	cfg                   config.Config
	reader                Reader
	catalog               catalog.Reader
	web                   http.Handler
	etherscan             http.Handler
	metrics               http.Handler
	events                *events.Broker
	mempool               mempool.Reader
	nftMediaSource        metadata.NFTImageSource
	nftMediaProxy         *metadata.MediaProxy
	verificationReader    VerificationReader
	verificationSubmitter VerificationSubmitter
	verificationTargets   VerificationTargetResolver
	sourcify              SourcifyAdapter
	logger                *slog.Logger
	requestID             func() string
	now                   func() time.Time
	runtimeReady          func() bool
	maxVerificationBody   int64
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
		catalog:               options.Catalog,
		web:                   options.Web,
		etherscan:             options.Etherscan,
		metrics:               options.Metrics,
		events:                options.Events,
		mempool:               options.Mempool,
		nftMediaSource:        options.NFTMediaSource,
		nftMediaProxy:         options.NFTMediaProxy,
		verificationReader:    options.VerificationReader,
		verificationSubmitter: options.VerificationSubmitter,
		verificationTargets:   options.VerificationTargets,
		sourcify:              options.Sourcify,
		logger:                options.Logger,
		requestID:             options.RequestID,
		now:                   options.Now,
		runtimeReady:          options.RuntimeReady,
		maxVerificationBody:   options.MaxVerificationBody,
		mux:                   http.NewServeMux(),
	}
	if h.maxVerificationBody <= 0 {
		h.maxVerificationBody = 6 << 20
	}
	h.routes()
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
	h.mux.HandleFunc("GET /api/v1/blocks", h.blocks)
	h.mux.HandleFunc("GET /api/v1/blocks/{id}", h.block)
	h.mux.HandleFunc("GET /api/v1/transactions", h.transactions)
	h.mux.HandleFunc("GET /api/v1/transactions/{hash}", h.transaction)
	h.mux.HandleFunc("GET /api/v1/pending", h.pendingTransactions)
	if h.catalog != nil {
		h.mux.HandleFunc("GET /api/v1/transactions/{hash}/trace", h.transactionTrace)
	}
	h.mux.HandleFunc("GET /api/v1/addresses/{address}", h.address)
	if h.catalog != nil {
		h.mux.HandleFunc("GET /api/v1/addresses/{address}/nfts", h.nftBalances)
		h.mux.HandleFunc("GET /api/v1/tokens", h.tokens)
		h.mux.HandleFunc("GET /api/v1/tokens/{address}", h.token)
		h.mux.HandleFunc("GET /api/v1/tokens/{address}/transfers", h.tokenTransfers)
		h.mux.HandleFunc("GET /api/v1/nfts/{address}/{token_id}", h.nftOwner)
		h.mux.HandleFunc("GET /api/v1/stats/blocks", h.blockStats)
		h.mux.HandleFunc("GET /api/v1/stats/summary", h.aggregateStats)
	}
	// The route remains present when external metadata is disabled so clients
	// receive a typed capability state instead of a misleading route-level 404.
	h.mux.HandleFunc("GET /api/v1/nfts/{address}/{token_id}/media", h.nftMedia)
	h.mux.HandleFunc("GET /api/v1/search", h.search)
	// Capability routes remain present when their backing service is disabled so
	// clients receive a typed unavailable response instead of mistaking a 404
	// for an empty or unsupported API surface.
	h.mux.HandleFunc("POST /api/v1/verification/jobs", h.submitVerification)
	h.mux.HandleFunc("GET /api/v1/verification/jobs/{id}", h.verificationJob)
	h.mux.HandleFunc("GET /api/v1/contracts/{address}/verification", h.verifiedContract)
	h.mux.HandleFunc("GET /api/v1/sourcify/contracts/{address}", h.lookupSourcifyContract)
	h.mux.HandleFunc("POST /api/v1/sourcify/imports", h.importSourcifyContract)
	h.mux.HandleFunc("POST /api/v1/verification/jobs/{id}/sourcify", h.uploadVerificationJobToSourcify)
	h.mux.HandleFunc("GET /api/v1/sourcify/jobs/{verification_id}", h.sourcifyJob)
	if h.etherscan != nil {
		h.mux.Handle("/v2/api", h.etherscan)
	}
	if h.events != nil {
		h.mux.HandleFunc("GET /api/v1/events", h.eventStream)
	}
	if h.web != nil {
		h.mux.Handle("/", h.web)
	}
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
	if requestedMethod != http.MethodGet && requestedMethod != http.MethodPost && requestedMethod != http.MethodHead {
		writeError(w, r, http.StatusBadRequest, "method_not_allowed", "requested CORS method is not allowed", nil)
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, X-Request-ID, Last-Event-ID")
	w.Header().Set("Access-Control-Max-Age", "600")
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
		w.Header().Set("Vary", "Origin")
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
		h.preflight(w, request)
		return
	}
	h.mux.ServeHTTP(w, request)
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
	status, err := h.reader.Status(r.Context())
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
	data := gen.Status{
		ChainId:          quantity(h.cfg.Chain.ID),
		CoreReady:        snapshot.CoreReady,
		LatestBlock:      quantity(snapshot.LatestBlock),
		IndexedBlock:     quantity(snapshot.IndexedBlock),
		BackfillComplete: snapshot.BackfillComplete,
		Lag:              quantity(saturatingSub(snapshot.LatestBlock, snapshot.IndexedBlock)),
		Completeness:     snapshot.Completeness,
	}
	if snapshot.HighestCoveredKnown {
		value := quantity(snapshot.HighestCoveredBlock)
		data.HighestCoveredBlock = &value
	}
	if snapshot.SafeBlock != nil {
		value := quantity(*snapshot.SafeBlock)
		data.SafeBlock = &value
	}
	if snapshot.FinalizedBlock != nil {
		value := quantity(*snapshot.FinalizedBlock)
		data.FinalizedBlock = &value
	}
	meta := h.meta(r)
	coverageStart, coverageEnd := quantity(snapshot.CoverageStart), quantity(snapshot.CoverageEnd)
	meta.CoverageStart, meta.CoverageEnd = &coverageStart, &coverageEnd
	writeJSON(w, http.StatusOK, gen.StatusResponse{Data: data, Meta: meta})
}

func (h *Handler) publicConfig(w http.ResponseWriter, r *http.Request) {
	features := map[string]bool{
		"trace":            h.cfg.Features.Trace,
		"mempool":          h.cfg.Features.Mempool,
		"historical_state": h.cfg.Features.HistoricalState,
		"verification":     h.verificationSubmitter != nil && h.verificationTargets != nil,
		"sourcify":         h.sourcify != nil,
		"nft_metadata":     h.cfg.Features.NFTMetadata,
		"pricing":          h.cfg.Features.Pricing,
	}
	data := gen.PublicConfig{
		ChainId:        quantity(h.cfg.Chain.ID),
		ChainName:      h.cfg.Chain.Name,
		NativeSymbol:   h.cfg.Chain.NativeSymbol,
		NativeName:     h.cfg.Chain.NativeName,
		NativeDecimals: int(h.cfg.Chain.NativeDecimals),
		Features:       features,
	}
	writeJSON(w, http.StatusOK, gen.PublicConfigResponse{Data: data, Meta: h.meta(r)})
}

func (h *Handler) blocks(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(w, r, 25, 100)
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

func (h *Handler) transaction(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !hashPattern.MatchString(hash) {
		writeError(w, r, http.StatusBadRequest, "invalid_transaction_hash", "transaction hash must be 32 bytes", nil)
		return
	}
	item, err := h.reader.Transaction(r.Context(), strings.ToLower(hash))
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.TransactionResponse{Data: item, Meta: h.meta(r)})
}

func (h *Handler) transactions(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(w, r, 25, 100)
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
	limit, ok := parseLimit(w, r, 25, 100)
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
		writeError(w, r, http.StatusServiceUnavailable, "mempool_unavailable", "pending transaction capability is unavailable", map[string]interface{}{
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
	return model
}

func (h *Handler) handleMempoolError(w http.ResponseWriter, r *http.Request, err error) {
	var capability mempool.CapabilityError
	switch {
	case errors.As(err, &capability):
		details := map[string]interface{}{"state": capability.State, "reason": capability.Code}
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

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len(query) > 256 {
		writeError(w, r, http.StatusBadRequest, "invalid_query", "q must contain 1 to 256 bytes", nil)
		return
	}
	limit, ok := parseLimit(w, r, 20, 100)
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
	if !h.requireAPIKey(w, r) {
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

type verificationSubmission struct {
	Address            string          `json:"address"`
	Language           verify.Language `json:"language"`
	CompilerVersion    string          `json:"compiler_version"`
	ContractIdentifier string          `json:"contract_identifier"`
	StandardJSON       json.RawMessage `json:"standard_json"`
	ConstructorArgs    string          `json:"constructor_arguments"`
	LicenseType        string          `json:"license_type"`
	SubmitToSourcify   bool            `json:"submit_to_sourcify"`
}

type sourcifyImportSubmission struct {
	Address         string `json:"address"`
	ConstructorArgs string `json:"constructor_arguments"`
}

type sourcifyUploadSubmission struct {
	Consent bool `json:"consent"`
}

func (h *Handler) submitVerification(w http.ResponseWriter, r *http.Request) {
	if !h.verificationSubmissionAvailable(w, r) {
		return
	}
	if !h.requireAPIKey(w, r) {
		return
	}
	var submission verificationSubmission
	if !h.decodeBoundedJSON(w, r, &submission, "invalid_verification_request", "verification request is invalid") {
		return
	}
	if !addressPattern.MatchString(submission.Address) {
		writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "verification request is invalid", nil)
		return
	}
	target, constructorArguments, err := h.resolveVerificationTarget(r.Context(), submission.Address, submission.ConstructorArgs)
	if err != nil {
		h.handleVerificationTargetError(w, r, err)
		return
	}
	request := verify.Request{
		ChainID: target.ChainID, Address: target.Address,
		CodeHash: target.CodeHash, AtBlockHash: target.AtBlockHash,
		Language: submission.Language, CompilerVersion: submission.CompilerVersion,
		ContractIdentifier: submission.ContractIdentifier, StandardJSON: submission.StandardJSON,
		CreationBytecode: target.CreationBytecode, RuntimeBytecode: target.RuntimeBytecode,
		ConstructorArgs: constructorArguments, LicenseType: submission.LicenseType,
		SubmitToSourcify: submission.SubmitToSourcify,
	}
	job, created, err := h.verificationSubmitter.Submit(r.Context(), request)
	if err != nil {
		h.handleVerificationError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, gen.VerificationJobResponse{Data: verificationJobModel(job), Meta: h.meta(r)})
}

func (h *Handler) verificationJob(w http.ResponseWriter, r *http.Request) {
	if !h.verificationReadAvailable(w, r) {
		return
	}
	if !h.requireAPIKey(w, r) {
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
	if !h.verificationReadAvailable(w, r) {
		return
	}
	if !h.requireAPIKey(w, r) {
		return
	}
	address, codeHash := strings.ToLower(r.PathValue("address")), strings.ToLower(r.URL.Query().Get("code_hash"))
	if !addressPattern.MatchString(address) || !hashPattern.MatchString(codeHash) {
		writeError(w, r, http.StatusBadRequest, "invalid_contract_identity", "address and code_hash must be fixed-size hexadecimal values", nil)
		return
	}
	contract, found, err := h.verificationReader.VerifiedContract(r.Context(), h.cfg.Chain.ID, address, codeHash)
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

func (h *Handler) verificationReadAvailable(w http.ResponseWriter, r *http.Request) bool {
	if h.verificationReader != nil {
		return true
	}
	writeError(w, r, http.StatusServiceUnavailable, "verification_unavailable", "contract verification is unavailable", nil)
	return false
}

func (h *Handler) verificationSubmissionAvailable(w http.ResponseWriter, r *http.Request) bool {
	if h.verificationSubmitter != nil && h.verificationTargets != nil {
		return true
	}
	writeError(w, r, http.StatusServiceUnavailable, "verification_unavailable", "contract verification submission is unavailable", nil)
	return false
}

func (h *Handler) sourcifyAvailable(w http.ResponseWriter, r *http.Request) bool {
	if h.sourcify != nil {
		return true
	}
	writeError(w, r, http.StatusServiceUnavailable, "sourcify_unavailable", "Sourcify interoperability is unavailable", map[string]interface{}{
		"state": "unavailable", "reason": "feature_disabled",
	})
	return false
}

func (h *Handler) decodeBoundedJSON(w http.ResponseWriter, r *http.Request, destination any, code, message string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxVerificationBody)
	decoder := json.NewDecoder(r.Body)
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

func (h *Handler) resolveVerificationTarget(
	ctx context.Context,
	address string,
	constructorArguments string,
) (verify.VerificationTarget, string, error) {
	target, err := h.verificationTargets.ResolveVerificationTarget(ctx, strings.ToLower(address))
	if err != nil {
		return verify.VerificationTarget{}, "", err
	}
	if target.ChainID != h.cfg.Chain.ID || !strings.EqualFold(target.Address, address) {
		return verify.VerificationTarget{}, "", verify.ErrVerificationTargetInvalid
	}
	return verify.BindConstructorArguments(target, constructorArguments, int(h.maxVerificationBody))
}

func (h *Handler) handleVerificationTargetError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, verify.ErrConstructorArgumentsMismatch) {
		writeError(w, r, http.StatusBadRequest, "invalid_constructor_arguments", "constructor arguments do not match the canonical creation input", nil)
		return
	}
	h.logger.ErrorContext(r.Context(), "resolve verification target", "request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
	writeError(w, r, http.StatusServiceUnavailable, "verification_target_unavailable", "canonical contract code or creation facts are unavailable", nil)
}

func (h *Handler) lookupSourcifyContract(w http.ResponseWriter, r *http.Request) {
	if !h.sourcifyAvailable(w, r) || !h.requireAPIKey(w, r) {
		return
	}
	address := strings.ToLower(r.PathValue("address"))
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_contract_identity", "address must be 20 bytes", nil)
		return
	}
	contract, err := h.sourcify.Lookup(r.Context(), h.cfg.Chain.ID, address)
	if err != nil {
		h.handleSourcifyError(w, r, err)
		return
	}
	model, err := sourcifyContractModel(contract)
	if err != nil || contract.ChainID != h.chainID() {
		h.handleSourcifyError(w, r, verify.ErrSourcifyInvalidResponse)
		return
	}
	writeJSON(w, http.StatusOK, gen.SourcifyContractResponse{Data: model, Meta: h.meta(r)})
}

func (h *Handler) importSourcifyContract(w http.ResponseWriter, r *http.Request) {
	if !h.sourcifyAvailable(w, r) || !h.verificationSubmissionAvailable(w, r) || !h.requireAPIKey(w, r) {
		return
	}
	var submission sourcifyImportSubmission
	if !h.decodeBoundedJSON(w, r, &submission, "invalid_sourcify_request", "Sourcify import request is invalid") {
		return
	}
	if !addressPattern.MatchString(submission.Address) {
		writeError(w, r, http.StatusBadRequest, "invalid_sourcify_request", "Sourcify import request is invalid", nil)
		return
	}
	target, constructorArguments, err := h.resolveVerificationTarget(r.Context(), submission.Address, submission.ConstructorArgs)
	if err != nil {
		h.handleVerificationTargetError(w, r, err)
		return
	}
	request, err := h.sourcify.Import(r.Context(), target)
	if err != nil {
		h.handleSourcifyError(w, r, err)
		return
	}
	request.ConstructorArgs = constructorArguments
	request.SubmitToSourcify = false
	job, created, err := h.verificationSubmitter.Submit(r.Context(), request)
	if err != nil {
		h.handleVerificationError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, gen.VerificationJobResponse{Data: verificationJobModel(job), Meta: h.meta(r)})
}

func (h *Handler) uploadVerificationJobToSourcify(w http.ResponseWriter, r *http.Request) {
	if !h.sourcifyAvailable(w, r) || !h.verificationReadAvailable(w, r) || !h.requireAPIKey(w, r) {
		return
	}
	var submission sourcifyUploadSubmission
	if !h.decodeBoundedJSON(w, r, &submission, "invalid_sourcify_request", "Sourcify upload request is invalid") {
		return
	}
	if !submission.Consent {
		writeError(w, r, http.StatusBadRequest, "sourcify_consent_required", "explicit Sourcify upload consent is required", nil)
		return
	}
	jobID := r.PathValue("id")
	if _, err := uuid.Parse(jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_sourcify_request", "local verification job ID is invalid", nil)
		return
	}
	ticket, err := h.sourcify.Submit(r.Context(), h.verificationReader, jobID, submission.Consent)
	if err != nil {
		h.handleSourcifyError(w, r, err)
		return
	}
	id, err := uuid.Parse(ticket.VerificationID)
	if err != nil {
		h.handleSourcifyError(w, r, verify.ErrSourcifyInvalidResponse)
		return
	}
	writeJSON(w, http.StatusAccepted, gen.SourcifyTicketResponse{
		Data: gen.SourcifyTicket{VerificationId: id}, Meta: h.meta(r),
	})
}

func (h *Handler) sourcifyJob(w http.ResponseWriter, r *http.Request) {
	if !h.sourcifyAvailable(w, r) || !h.requireAPIKey(w, r) {
		return
	}
	verificationID := r.PathValue("verification_id")
	if _, err := uuid.Parse(verificationID); err != nil || verificationID != strings.ToLower(verificationID) {
		writeError(w, r, http.StatusBadRequest, "invalid_sourcify_request", "Sourcify verification ID is invalid", nil)
		return
	}
	job, err := h.sourcify.Status(r.Context(), verificationID)
	if err != nil {
		h.handleSourcifyError(w, r, err)
		return
	}
	model, err := sourcifyJobModel(job, h.chainID())
	if err != nil {
		h.handleSourcifyError(w, r, verify.ErrSourcifyInvalidResponse)
		return
	}
	writeJSON(w, http.StatusOK, gen.SourcifyJobResponse{Data: model, Meta: h.meta(r)})
}

func (h *Handler) handleSourcifyError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, verify.ErrConsentRequired):
		writeError(w, r, http.StatusBadRequest, "sourcify_consent_required", "explicit Sourcify upload consent is required", nil)
	case errors.Is(err, verify.ErrSourcifyNotFound), errors.Is(err, verify.ErrSourcifyRequestMissing):
		writeError(w, r, http.StatusNotFound, "sourcify_not_found", "Sourcify contract or job is unavailable", nil)
	case errors.Is(err, verify.ErrSourcifyTargetMismatch):
		writeError(w, r, http.StatusConflict, "sourcify_target_mismatch", "Sourcify data does not match the canonical local target", nil)
	case errors.Is(err, verify.ErrSourcifyAlreadyVerified):
		writeError(w, r, http.StatusConflict, "sourcify_already_verified", "Sourcify already has a verified contract", nil)
	case errors.Is(err, verify.ErrSourcifyRejected):
		writeError(w, r, http.StatusBadRequest, "sourcify_rejected", "Sourcify rejected the request", nil)
	case errors.Is(err, verify.ErrSourcifyUnavailable), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		writeError(w, r, http.StatusServiceUnavailable, "sourcify_unavailable", "Sourcify is unavailable", map[string]interface{}{
			"state": "unavailable", "reason": "upstream_unavailable",
		})
	case errors.Is(err, verify.ErrSourcifyInvalidResponse):
		writeError(w, r, http.StatusBadGateway, "sourcify_invalid_response", "Sourcify returned an invalid response", nil)
	default:
		h.logger.ErrorContext(r.Context(), "Sourcify request failed", "request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
		writeError(w, r, http.StatusInternalServerError, "sourcify_failed", "Sourcify request failed", nil)
	}
}

func sourcifyContractModel(contract verify.SourcifyContract) (gen.SourcifyContract, error) {
	address, err := checksumAddress(contract.Address)
	if err != nil || !canonicalQuantity(contract.ChainID) {
		return gen.SourcifyContract{}, verify.ErrSourcifyInvalidResponse
	}
	model := gen.SourcifyContract{ChainId: contract.ChainID, Address: address}
	if model.Match, err = sourcifyMatchModel(contract.Match); err != nil {
		return gen.SourcifyContract{}, err
	}
	if model.CreationMatch, err = sourcifyMatchModel(contract.CreationMatch); err != nil {
		return gen.SourcifyContract{}, err
	}
	if model.RuntimeMatch, err = sourcifyMatchModel(contract.RuntimeMatch); err != nil {
		return gen.SourcifyContract{}, err
	}
	if contract.Compilation.Language != "" {
		var language gen.SourcifyContractLanguage
		switch contract.Compilation.Language {
		case "Solidity":
			language = gen.SourcifyContractLanguageSolidity
		case "Vyper":
			language = gen.SourcifyContractLanguageVyper
		default:
			return gen.SourcifyContract{}, verify.ErrSourcifyInvalidResponse
		}
		model.Language = &language
	}
	if contract.Compilation.CompilerVersion != "" {
		value := contract.Compilation.CompilerVersion
		model.CompilerVersion = &value
	}
	if contract.Compilation.FullyQualifiedName != "" {
		value := contract.Compilation.FullyQualifiedName
		model.ContractIdentifier = &value
	}
	return model, nil
}

func sourcifyJobModel(job verify.SourcifyJob, chainID string) (gen.SourcifyJob, error) {
	id, err := uuid.Parse(job.VerificationID)
	if err != nil {
		return gen.SourcifyJob{}, verify.ErrSourcifyInvalidResponse
	}
	model := gen.SourcifyJob{VerificationId: id, State: gen.SourcifyJobStatePending}
	if job.IsJobCompleted {
		model.State = gen.SourcifyJobStateSucceeded
		if job.ErrorCode != "" {
			model.State = gen.SourcifyJobStateFailed
		}
	}
	if job.Contract == nil {
		return model, nil
	}
	if job.Contract.ChainID != chainID {
		return gen.SourcifyJob{}, verify.ErrSourcifyInvalidResponse
	}
	address, err := checksumAddress(job.Contract.Address)
	if err != nil {
		return gen.SourcifyJob{}, verify.ErrSourcifyInvalidResponse
	}
	contract := gen.SourcifyContract{ChainId: job.Contract.ChainID, Address: address}
	if contract.Match, err = sourcifyOptionalMatchModel(job.Contract.Match); err != nil {
		return gen.SourcifyJob{}, err
	}
	if contract.CreationMatch, err = sourcifyOptionalMatchModel(job.Contract.CreationMatch); err != nil {
		return gen.SourcifyJob{}, err
	}
	if contract.RuntimeMatch, err = sourcifyOptionalMatchModel(job.Contract.RuntimeMatch); err != nil {
		return gen.SourcifyJob{}, err
	}
	model.Contract = &contract
	return model, nil
}

func sourcifyMatchModel(value string) (*gen.SourcifyMatch, error) {
	if value == "" {
		return nil, nil
	}
	match := gen.SourcifyMatch(value)
	if !match.Valid() {
		return nil, verify.ErrSourcifyInvalidResponse
	}
	return &match, nil
}

func sourcifyOptionalMatchModel(value *string) (*gen.SourcifyMatch, error) {
	if value == nil {
		return nil, nil
	}
	return sourcifyMatchModel(*value)
}

func (h *Handler) requireAPIKey(w http.ResponseWriter, r *http.Request) bool {
	if auth.IdentityFrom(r.Context()).Authenticated {
		return true
	}
	writeError(w, r, http.StatusUnauthorized, "api_key_required", "an API key is required", nil)
	return false
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
	model := gen.VerificationJob{
		Id: id, Status: gen.VerificationJobStatus(job.Status),
		CreatedAt: job.CreatedAt.UTC(), UpdatedAt: job.UpdatedAt.UTC(),
	}
	if job.ResultKind != nil {
		value := gen.VerificationMatch(*job.ResultKind)
		model.ResultKind = &value
	}
	if job.Result != nil {
		creation, runtime := gen.VerificationMatch(job.Result.Match.Creation), gen.VerificationMatch(job.Result.Match.Runtime)
		model.CreationMatch, model.RuntimeMatch = &creation, &runtime
		published := job.Result.Published
		model.Published = &published
	}
	if job.ErrorCode != "" {
		value := string(job.ErrorCode)
		model.ErrorCode = &value
	}
	return model
}

func verifiedContractModel(contract verify.VerifiedContract) (gen.VerifiedContract, error) {
	var abi []map[string]interface{}
	var sources, settings map[string]interface{}
	address, err := checksumAddress(contract.Address)
	if err != nil {
		return gen.VerifiedContract{}, fmt.Errorf("checksum verified contract address: %w", err)
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
	model := gen.VerifiedContract{
		ChainId: strconv.FormatUint(contract.ChainID, 10), Address: address, CodeHash: contract.CodeHash,
		ValidFromBlock: strconv.FormatUint(contract.ValidFromBlock, 10), Language: gen.VerifiedContractLanguage(contract.Language),
		CompilerVersion: contract.CompilerVersion, MatchKind: gen.VerificationMatch(contract.MatchKind),
		ContractName: contract.ContractName, Abi: abi, Sources: sources, Settings: settings,
		CreatedAt: contract.CreatedAt.UTC(),
	}
	if contract.ValidToBlock != nil {
		value := strconv.FormatUint(*contract.ValidToBlock, 10)
		model.ValidToBlock = &value
	}
	return model, nil
}

func checksumAddress(value string) (string, error) {
	address, err := ethrpc.ParseAddress(value)
	if err != nil {
		return "", err
	}
	lower := strings.ToLower(address.String()[2:])
	hasher := sha3.NewLegacyKeccak256()
	if _, err := hasher.Write([]byte(lower)); err != nil {
		return "", fmt.Errorf("hash address: %w", err)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	checksummed := []byte(lower)
	for index := range checksummed {
		if checksummed[index] >= 'a' && checksummed[index] <= 'f' && digest[index] >= '8' {
			checksummed[index] -= 'a' - 'A'
		}
	}
	return "0x" + string(checksummed), nil
}

func (h *Handler) handleReaderError(w http.ResponseWriter, r *http.Request, err error) {
	var capability *CapabilityUnavailableError
	switch {
	case errors.As(err, &capability) && capability.valid():
		writeError(w, r, http.StatusServiceUnavailable, "capability_unavailable", "required capability is unavailable", map[string]interface{}{
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
		details := map[string]interface{}{
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
	limit, ok := parseLimit(w, r, 25, 100)
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
	return gen.TokenEvent{
		ChainId: item.ChainID, BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
		LogIndex: item.LogIndex, SubIndex: item.SubIndex, TransactionHash: item.TransactionHash,
		TokenAddress: item.TokenAddress, Standard: item.Standard, Kind: item.Kind,
		Operator: item.Operator, From: item.From, To: item.To, TokenId: item.TokenID,
		Amount: item.Amount, Confidence: item.Confidence,
	}
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

func transactionTraceModel(item catalog.TransactionTrace) gen.TransactionTrace {
	frames := make([]gen.TraceFrame, len(item.Frames))
	for index := range item.Frames {
		frame := item.Frames[index]
		path, parentPath := make([]int, len(frame.Path)), make([]int, len(frame.ParentPath))
		for component := range frame.Path {
			path[component] = int(frame.Path[component])
		}
		for component := range frame.ParentPath {
			parentPath[component] = int(frame.ParentPath[component])
		}
		frames[index] = gen.TraceFrame{
			Path: path, ParentPath: parentPath, Depth: int(frame.Depth), CallType: frame.CallType,
			From: frame.From, To: frame.To, CreatedAddress: frame.CreatedAddress,
			Value: frame.Value, Gas: frame.Gas, GasUsed: frame.GasUsed,
			Input: frame.Input, Output: frame.Output, Error: frame.Error, Reverted: frame.Reverted,
		}
	}
	return gen.TransactionTrace{
		ChainId: item.ChainID, BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
		TransactionHash: item.TransactionHash, TransactionIndex: item.TransactionIndex,
		State: gen.TransactionTraceState(item.State), Frames: frames,
	}
}

func (h *Handler) meta(r *http.Request) gen.Meta {
	return gen.Meta{RequestId: requestIDFrom(r.Context()), ChainId: quantity(h.cfg.Chain.ID)}
}

type Service struct {
	server          *http.Server
	listen          func(string, string) (net.Listener, error)
	shutdownTimeout time.Duration
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
	}
}

func (s *Service) Name() string { return "http-api" }

func (s *Service) Run(ctx context.Context) error {
	listener, err := s.listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- s.server.Serve(listener) }()
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

func parseLimit(w http.ResponseWriter, r *http.Request, defaultValue, maxValue int) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultValue, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maxValue {
		writeError(w, r, http.StatusBadRequest, "invalid_limit", fmt.Sprintf("limit must be between 1 and %d", maxValue), nil)
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

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]interface{}) {
	var detailsPointer *map[string]interface{}
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
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
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
