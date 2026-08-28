package billing

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type UsageLedger interface {
	ReserveUsage(context.Context, ReserveUsageInput) (UsageReservation, error)
	CommitUsage(context.Context, CommitUsageInput) (UsageCharge, error)
	ReleaseUsage(context.Context, string, string, string, time.Time) (UsageCharge, error)
}

type UsageErrorWriter func(http.ResponseWriter, int, string, string)

type UsageDispatcherOptions struct {
	Ledger         UsageLedger
	Prices         map[string]string
	MaxBodyBytes   int64
	MaxHeaderBytes int
	Now            func() time.Time
	Logger         *slog.Logger
	Observer       PrepaidObserver
}

type UsageDispatcher struct {
	ledger         UsageLedger
	prices         map[string]string
	maxBodyBytes   int64
	maxHeaderBytes int
	now            func() time.Time
	logger         *slog.Logger
	observer       PrepaidObserver
}

func NewUsageDispatcher(options UsageDispatcherOptions) (*UsageDispatcher, error) {
	if options.Ledger == nil || options.MaxBodyBytes <= 0 || options.MaxBodyBytes > 8<<20 ||
		options.MaxHeaderBytes < 1024 || options.MaxHeaderBytes > 64<<10 {
		return nil, ErrInvalidInput
	}
	prices := make(map[string]string, len(options.Prices))
	for operation, amount := range options.Prices {
		if !validUsageOperation(operation) {
			return nil, ErrInvalidInput
		}
		if _, ok := canonicalPositiveNumeric(amount); !ok {
			return nil, ErrInvalidInput
		}
		prices[operation] = amount
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &UsageDispatcher{
		ledger: options.Ledger, prices: prices,
		maxBodyBytes: options.MaxBodyBytes, maxHeaderBytes: options.MaxHeaderBytes,
		now: now, logger: logger, observer: options.Observer,
	}, nil
}

func (dispatcher *UsageDispatcher) Price(operation string) (string, bool) {
	if dispatcher == nil {
		return "", false
	}
	amount, ok := dispatcher.prices[operation]
	return amount, ok
}

type UsageRequest struct {
	UserID       string
	APIKeyPrefix string
	Operation    string
	Resource     Digest
	MaxBodyBytes int64
	Chargeable   func(int, http.Header, []byte) bool
	WriteError   UsageErrorWriter
}

func (dispatcher *UsageDispatcher) Serve(
	writer http.ResponseWriter,
	request *http.Request,
	usage UsageRequest,
	handler http.Handler,
) {
	writeError := usage.WriteError
	if writeError == nil {
		writeError = writeUsageError
	}
	amount, priced := dispatcher.Price(usage.Operation)
	if dispatcher == nil || !priced || request == nil || handler == nil ||
		usage.UserID == "" || !validAPIKeyPrefix(usage.APIKeyPrefix) ||
		usage.Chargeable == nil ||
		(request.Method != http.MethodGet && request.Method != http.MethodPost) {
		writeError(writer, http.StatusServiceUnavailable, "billing_unavailable", "API billing is unavailable")
		return
	}
	reservation, err := dispatcher.ledger.ReserveUsage(request.Context(), ReserveUsageInput{
		UserID: usage.UserID, APIKeyPrefix: usage.APIKeyPrefix,
		Method: request.Method, Operation: usage.Operation, Resource: usage.Resource,
		AmountAtomic: amount, ObservedAt: dispatcher.now().UTC(),
	})
	if err != nil {
		if errors.Is(err, ErrInsufficientCredit) {
			dispatcher.observe(usage.Operation, "credit_required")
			writeError(writer, http.StatusPaymentRequired, "billing_credit_required", "prepaid API credit is required")
			return
		}
		dispatcher.log("usage_reservation_failed", err)
		dispatcher.observe(usage.Operation, "ledger_unavailable")
		writeError(writer, http.StatusServiceUnavailable, "billing_ledger_unavailable", "API billing is unavailable")
		return
	}
	chargeID, owner := reservation.Charge.ID, reservation.Owner
	maximum := min(dispatcher.maxBodyBytes, usage.MaxBodyBytes)
	if maximum <= 0 {
		maximum = dispatcher.maxBodyBytes
	}
	capture := newCapturedResponse(maximum, dispatcher.maxHeaderBytes)
	panicked := invokeCapturedHandler(capture, request, handler)
	if panicked {
		if dispatcher.releaseDetached(request.Context(), chargeID, owner, "handler_panic") != nil {
			writeError(writer, http.StatusServiceUnavailable, "billing_ledger_unavailable", "API billing is unavailable")
			return
		}
		writeError(writer, http.StatusInternalServerError, "query_failed", "query failed")
		dispatcher.observe(usage.Operation, "released")
		return
	}
	if request.Context().Err() != nil {
		_ = dispatcher.releaseDetached(request.Context(), chargeID, owner, "request_canceled")
		writeError(writer, http.StatusServiceUnavailable, "request_canceled", "request was canceled")
		dispatcher.observe(usage.Operation, "released")
		return
	}
	if finishErr := capture.finish(); finishErr != nil {
		code := "handler_capture_invalid"
		if errors.Is(finishErr, errCapturedBodyLimit) {
			code = "handler_body_too_large"
		} else if errors.Is(finishErr, errCapturedHeaderLimit) {
			code = "handler_headers_too_large"
		} else if errors.Is(finishErr, errCapturedStreaming) {
			code = "handler_streaming_unsupported"
		}
		if dispatcher.releaseDetached(request.Context(), chargeID, owner, code) != nil {
			writeError(writer, http.StatusServiceUnavailable, "billing_ledger_unavailable", "API billing is unavailable")
			return
		}
		writeError(writer, http.StatusBadGateway, "billing_response_invalid", "priced response cannot be delivered")
		dispatcher.observe(usage.Operation, "released")
		return
	}
	if !usage.Chargeable(capture.status, capture.snapshot, capture.body.Bytes()) {
		if dispatcher.releaseDetached(request.Context(), chargeID, owner, "response_not_chargeable") != nil {
			writeError(writer, http.StatusServiceUnavailable, "billing_ledger_unavailable", "API billing is unavailable")
			return
		}
		releaseCapturedResponse(writer, capture)
		dispatcher.observe(usage.Operation, "released")
		return
	}
	digest := sha256.Sum256(capture.body.Bytes())
	if _, err := dispatcher.ledger.CommitUsage(request.Context(), CommitUsageInput{
		ChargeID: chargeID, Owner: owner, Response: Digest(digest),
		ResponseBytes: int64(capture.body.Len()), ObservedAt: dispatcher.now().UTC(),
	}); err != nil {
		dispatcher.log("usage_commit_unknown", err)
		dispatcher.observe(usage.Operation, "commit_unknown")
		writeError(writer, http.StatusServiceUnavailable, "billing_commit_unknown", "API usage outcome requires inspection")
		return
	}
	releaseCapturedResponse(writer, capture)
	dispatcher.observe(usage.Operation, "committed")
}

func (dispatcher *UsageDispatcher) observe(operation, result string) {
	if dispatcher != nil && dispatcher.observer != nil {
		dispatcher.observer.ObserveBillingUsage(operation, result)
	}
}

func (dispatcher *UsageDispatcher) releaseDetached(
	parent context.Context,
	chargeID, owner, code string,
) error {
	ctx, cancel := detachedPersistenceContext(parent)
	defer cancel()
	_, err := dispatcher.ledger.ReleaseUsage(ctx, chargeID, owner, code, dispatcher.now().UTC())
	if err != nil {
		dispatcher.log("usage_release_failed", err)
	}
	return err
}

func (dispatcher *UsageDispatcher) log(code string, err error) {
	if dispatcher == nil || dispatcher.logger == nil || err == nil {
		return
	}
	dispatcher.logger.Error("prepaid API billing failure", "error_code", code, "error_type", fmt.Sprintf("%T", err))
}

func writeUsageError(writer http.ResponseWriter, status int, code, message string) {
	writeBillingError(writer, status, code, message)
}
