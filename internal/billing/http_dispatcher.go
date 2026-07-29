package billing

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/apiops"
	"github.com/islishude/etherview/internal/billing/x402wire"
	"github.com/islishude/etherview/internal/config"
	x402 "github.com/x402-foundation/x402/go/v2"
)

const billingPersistenceTimeout = 5 * time.Second

type AccessMode uint8

const (
	AccessFree AccessMode = iota
	AccessAPIKey
	AccessPaid
)

type APIKeyIdentity struct {
	Authenticated bool
	Prefix        string
}

type PaymentLedger interface {
	Reserve(context.Context, ReserveInput) (Reservation, error)
	MarkVerified(context.Context, VerifiedInput) (Payment, error)
	StartHandler(context.Context, string, string, time.Time) (Payment, error)
	BeginSettlement(context.Context, string, string, time.Time) (Payment, error)
	MarkSettlementUnknown(context.Context, string, string, time.Time) (Payment, error)
	MarkSettled(context.Context, string, string, common.Hash, time.Time) (Payment, error)
	MarkFailed(context.Context, string, string, string, time.Time) (Payment, error)
}

type PaymentFacilitator interface {
	VerifyPayment(context.Context, x402wire.Payment, x402wire.Requirement) (*x402.VerifyResponse, error)
	SettlePayment(context.Context, x402wire.Payment, x402wire.Requirement) (*x402.SettleResponse, error)
	OriginDigest() [32]byte
}

// PayerUserResolver is writer-backed and is consulted only after a
// facilitator has verified the payer. Missing users are not errors and payment
// never depends on a browser session.
type PayerUserResolver interface {
	UserIDForPayer(context.Context, common.Address) (string, bool, error)
}

// RequestObserver receives only the static operation ID and one closed
// terminal outcome. Implementations must not derive labels from payment or
// facilitator data.
type RequestObserver interface {
	ObserveX402Request(operation, result string)
}

type DispatcherOptions struct {
	Config       config.Config
	Ledger       PaymentLedger
	Facilitator  PaymentFacilitator
	UserResolver PayerUserResolver
	Observer     RequestObserver
	Logger       *slog.Logger
	Now          func() time.Time
}

type HTTPDispatcher struct {
	publicOrigin      string
	chainID           uint64
	network           string
	asset             string
	assetAddress      common.Address
	recipient         string
	recipientAddress  common.Address
	assetName         string
	assetVersion      string
	maxTimeoutSeconds int
	maxBodyBytes      int64
	maxHeaderBytes    int
	routes            map[string]config.BillingRouteConfig
	fingerprintPepper []byte
	codec             *x402wire.Codec
	ledger            PaymentLedger
	facilitator       PaymentFacilitator
	userResolver      PayerUserResolver
	observer          RequestObserver
	logger            *slog.Logger
	now               func() time.Time
}

func NewHTTPDispatcher(options DispatcherOptions) (*HTTPDispatcher, error) {
	cfg := options.Config
	if !cfg.Features.X402Billing {
		return nil, errors.New("x402 billing is disabled")
	}
	if options.Ledger == nil || options.Facilitator == nil {
		return nil, errors.New("x402 billing requires a writer ledger and facilitator")
	}
	codec, err := x402wire.NewCodec(cfg.Billing.MaxPaymentHeaderBytes)
	if err != nil {
		return nil, errors.New("configure x402 header codec")
	}
	asset, ok := addressFromHex(cfg.Billing.Asset)
	if !ok {
		return nil, errors.New("configure x402 asset")
	}
	recipient, ok := addressFromHex(cfg.Billing.Recipient)
	if !ok {
		return nil, errors.New("configure x402 recipient")
	}
	if len(cfg.Billing.FingerprintPepper) < 32 {
		return nil, errors.New("configure x402 fingerprint pepper")
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	routes := make(map[string]config.BillingRouteConfig, len(cfg.Billing.Routes))
	maps.Copy(routes, cfg.Billing.Routes)
	return &HTTPDispatcher{
		publicOrigin:      strings.TrimSuffix(cfg.Server.PublicURL, "/"),
		chainID:           cfg.Chain.ID,
		network:           cfg.Billing.Network,
		asset:             strings.ToLower(cfg.Billing.Asset),
		assetAddress:      asset,
		recipient:         strings.ToLower(cfg.Billing.Recipient),
		recipientAddress:  recipient,
		assetName:         cfg.Billing.AssetEIP712Name,
		assetVersion:      cfg.Billing.AssetEIP712Version,
		maxTimeoutSeconds: int(cfg.Billing.RequirementMaxTimeout / time.Second),
		maxBodyBytes:      cfg.Billing.MaxBufferedResponseBytes,
		maxHeaderBytes:    cfg.Billing.MaxCapturedHeaderBytes,
		routes:            routes,
		fingerprintPepper: append([]byte(nil), cfg.Billing.FingerprintPepper...),
		codec:             codec,
		ledger:            options.Ledger,
		facilitator:       options.Facilitator,
		userResolver:      options.UserResolver,
		observer:          options.Observer,
		logger:            logger,
		now:               now,
	}, nil
}

func (dispatcher *HTTPDispatcher) Access(operation string, identity APIKeyIdentity) AccessMode {
	if dispatcher == nil {
		return AccessFree
	}
	route, ok := dispatcher.routes[operation]
	if !ok {
		return AccessFree
	}
	if route.Access == "api_key_or_x402" && identity.Authenticated {
		return AccessAPIKey
	}
	return AccessPaid
}

func PaymentHeaderPresent(header http.Header) bool {
	_, ok := header[http.CanonicalHeaderKey(x402wire.PaymentSignatureHeader)]
	return ok
}

// ServePaid executes the complete verify-capture-settle-release chain for one
// statically matched eligible operation.
func (dispatcher *HTTPDispatcher) ServePaid(
	writer http.ResponseWriter,
	request *http.Request,
	spec apiops.Spec,
	identity APIKeyIdentity,
	handler http.Handler,
) {
	outcome := "other"
	if dispatcher != nil && dispatcher.observer != nil {
		defer func() {
			dispatcher.observer.ObserveX402Request(string(spec.ID), outcome)
		}()
	}
	if dispatcher == nil || handler == nil || !spec.BillingEligible ||
		spec.MaxResponseBytes <= 0 || request.Method != spec.Method {
		outcome = "invalid"
		writeBillingError(writer, http.StatusServiceUnavailable, "x402_unavailable", "request billing is unavailable")
		return
	}
	route, configured := dispatcher.routes[string(spec.ID)]
	if !configured {
		outcome = "invalid"
		writeBillingError(writer, http.StatusBadRequest, "x402_route_not_priced", "request billing is not configured")
		return
	}
	resource, err := canonicalResource(dispatcher.publicOrigin, request.URL, spec)
	if err != nil {
		outcome = "invalid"
		writeBillingError(writer, http.StatusBadRequest, "x402_resource_invalid", "request resource is invalid")
		return
	}
	requirement, err := x402wire.NewRequirement(x402wire.RequirementOptions{
		Network: dispatcher.network, Asset: dispatcher.asset,
		Amount: route.AmountAtomic, PayTo: dispatcher.recipient,
		MaxTimeoutSeconds:  dispatcher.maxTimeoutSeconds,
		AssetEIP712Name:    dispatcher.assetName,
		AssetEIP712Version: dispatcher.assetVersion,
		Resource:           resource,
	})
	if err != nil {
		dispatcher.logFailure(request.Context(), "requirement", err)
		writeBillingError(writer, http.StatusServiceUnavailable, "x402_unavailable", "request billing is unavailable")
		return
	}

	payment, err := dispatcher.codec.DecodePaymentSignature(request.Header)
	if err != nil {
		if boundaryCode(err) == x402wire.CodeHeaderMissing {
			outcome = "required"
			dispatcher.writePaymentRequired(writer, requirement, "payment_required")
			return
		}
		outcome = "invalid"
		writeBillingError(writer, http.StatusBadRequest, boundaryCode(err), "payment authorization is invalid")
		return
	}
	if err := requirement.Match(payment); err != nil {
		outcome = "invalid"
		writeBillingError(writer, http.StatusBadRequest, x402wire.CodePaymentMismatch, "payment authorization does not match this request")
		return
	}
	fingerprint, err := x402wire.Fingerprint(dispatcher.fingerprintPepper, payment)
	if err != nil {
		outcome = "invalid"
		writeBillingError(writer, http.StatusBadRequest, boundaryCode(err), "payment authorization is invalid")
		return
	}

	now := dispatcher.observedAt()
	resourceDigest := requirement.ResourceDigest()
	requirementDigest := requirement.RequirementDigest()
	facilitatorDigest := dispatcher.facilitator.OriginDigest()
	apiKeyPrefix := optionalAPIKeyPrefix(identity)
	reservation, err := dispatcher.ledger.Reserve(request.Context(), ReserveInput{
		Fingerprint: Digest(fingerprint), Operation: string(spec.ID),
		ResourceDigest: Digest(resourceDigest), RequirementDigest: Digest(requirementDigest),
		Network: dispatcher.network, Asset: dispatcher.assetAddress,
		AmountAtomic: route.AmountAtomic, Recipient: dispatcher.recipientAddress,
		APIKeyPrefix: apiKeyPrefix, FacilitatorDigest: Digest(facilitatorDigest),
		ObservedAt: now,
	})
	if err != nil {
		status, code := http.StatusServiceUnavailable, "x402_ledger_unavailable"
		outcome = "ledger_unavailable"
		if errors.Is(err, ErrIntegrity) {
			status, code = http.StatusConflict, "x402_payment_binding_conflict"
			outcome = "binding_conflict"
		}
		dispatcher.logFailure(request.Context(), code, err)
		writeBillingError(writer, status, code, "payment authorization cannot be reserved")
		return
	}
	if !reservation.Owned {
		if reservation.Payment.State == StateSettling {
			outcome = "settlement_unknown"
		} else {
			outcome = "replayed"
		}
		dispatcher.writeDuplicate(writer, reservation.Payment)
		return
	}
	paymentID, owner := reservation.Payment.ID, reservation.Owner

	verifyResponse, err := dispatcher.facilitator.VerifyPayment(request.Context(), payment, requirement)
	if err != nil || verifyResponse == nil || !verifyResponse.IsValid {
		code := x402wire.CodeFacilitatorUnavailable
		status := http.StatusServiceUnavailable
		if x402wire.IsFailure(err, x402wire.FailureRejected) {
			code, status = x402wire.CodeFacilitatorRejected, http.StatusPaymentRequired
		}
		if failErr := dispatcher.failDetached(request.Context(), paymentID, owner, code); failErr != nil {
			outcome = "ledger_unavailable"
			writeBillingError(writer, http.StatusServiceUnavailable, "x402_ledger_unavailable", "payment failure could not be committed")
			return
		}
		if status == http.StatusPaymentRequired {
			outcome = "verify_rejected"
			dispatcher.writePaymentRequired(writer, requirement, code)
		} else {
			outcome = "verify_unavailable"
			writeBillingError(writer, status, code, "payment verification is unavailable")
		}
		return
	}
	payer, ok := addressFromHex(verifyResponse.Payer)
	if !ok {
		if failErr := dispatcher.failDetached(request.Context(), paymentID, owner, "facilitator_response_invalid"); failErr != nil {
			outcome = "ledger_unavailable"
			writeBillingError(writer, http.StatusServiceUnavailable, "x402_ledger_unavailable", "payment failure could not be committed")
			return
		}
		outcome = "verify_unavailable"
		writeBillingError(writer, http.StatusServiceUnavailable, x402wire.CodeFacilitatorResponseInvalid, "payment verification is unavailable")
		return
	}
	userID, err := dispatcher.resolveUser(request.Context(), payer)
	if err != nil {
		if failErr := dispatcher.failDetached(request.Context(), paymentID, owner, "payer_attribution_unavailable"); failErr != nil {
			outcome = "ledger_unavailable"
			writeBillingError(writer, http.StatusServiceUnavailable, "x402_ledger_unavailable", "payment failure could not be committed")
			return
		}
		dispatcher.logFailure(request.Context(), "payer_attribution_unavailable", err)
		outcome = "ledger_unavailable"
		writeBillingError(writer, http.StatusServiceUnavailable, "x402_ledger_unavailable", "payment verification is unavailable")
		return
	}
	if _, err := dispatcher.ledger.MarkVerified(request.Context(), VerifiedInput{
		PaymentID: paymentID, Owner: owner, Payer: payer,
		UserID: userID, APIKeyPrefix: apiKeyPrefix, ObservedAt: dispatcher.observedAt(),
	}); err != nil {
		_ = dispatcher.failDetached(
			request.Context(), paymentID, owner, "ledger_verify_failed",
		)
		dispatcher.logFailure(request.Context(), "ledger_verify_failed", err)
		outcome = "ledger_unavailable"
		writeBillingError(writer, http.StatusServiceUnavailable, "x402_ledger_unavailable", "payment verification could not be committed")
		return
	}
	if _, err := dispatcher.ledger.StartHandler(
		request.Context(), paymentID, owner, dispatcher.observedAt(),
	); err != nil {
		_ = dispatcher.failDetached(
			request.Context(), paymentID, owner, "handler_fence_failed",
		)
		dispatcher.logFailure(request.Context(), "handler_fence_failed", err)
		outcome = "ledger_unavailable"
		writeBillingError(writer, http.StatusServiceUnavailable, "x402_ledger_unavailable", "payment handler fence could not be committed")
		return
	}

	maxBodyBytes := min(spec.MaxResponseBytes, dispatcher.maxBodyBytes)
	capture := newCapturedResponse(maxBodyBytes, dispatcher.maxHeaderBytes)
	panicked := invokeCapturedHandler(
		capture,
		request.WithContext(withPaidOperation(request.Context(), string(spec.ID))),
		handler,
	)
	if panicked {
		if err := dispatcher.failDetached(request.Context(), paymentID, owner, "handler_panic"); err != nil {
			outcome = "ledger_unavailable"
			writeBillingError(writer, http.StatusServiceUnavailable, "x402_ledger_unavailable", "handler failure could not be committed")
			return
		}
		outcome = "handler_failed"
		writeBillingError(writer, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if request.Context().Err() != nil {
		if err := dispatcher.failDetached(request.Context(), paymentID, owner, "request_canceled"); err != nil {
			outcome = "ledger_unavailable"
			writeBillingError(writer, http.StatusServiceUnavailable, "x402_ledger_unavailable", "handler failure could not be committed")
			return
		}
		outcome = "handler_failed"
		writeBillingError(writer, http.StatusServiceUnavailable, "x402_request_canceled", "request was canceled before settlement")
		return
	}
	if err := capture.finish(); err != nil || capture.status < 100 || capture.status > 999 {
		code := "handler_capture_invalid"
		if errors.Is(err, errCapturedBodyLimit) {
			code = "handler_body_too_large"
		} else if errors.Is(err, errCapturedHeaderLimit) {
			code = "handler_headers_too_large"
		} else if errors.Is(err, errCapturedStreaming) {
			code = "handler_streaming_unsupported"
		}
		if failErr := dispatcher.failDetached(request.Context(), paymentID, owner, code); failErr != nil {
			outcome = "ledger_unavailable"
			writeBillingError(writer, http.StatusServiceUnavailable, "x402_ledger_unavailable", "handler failure could not be committed")
			return
		}
		outcome = "handler_failed"
		writeBillingError(writer, http.StatusBadGateway, "x402_handler_response_invalid", "protected response cannot be billed")
		return
	}
	if capture.status < http.StatusOK || capture.status >= http.StatusMultipleChoices {
		if err := dispatcher.failDetached(request.Context(), paymentID, owner, "handler_non_success"); err != nil {
			outcome = "ledger_unavailable"
			writeBillingError(writer, http.StatusServiceUnavailable, "x402_ledger_unavailable", "handler failure could not be committed")
			return
		}
		outcome = "handler_non_success"
		releaseCapturedResponse(writer, capture)
		return
	}

	if _, err := dispatcher.ledger.BeginSettlement(
		request.Context(), paymentID, owner, dispatcher.observedAt(),
	); err != nil {
		_ = dispatcher.failDetached(
			request.Context(), paymentID, owner, "settlement_fence_failed",
		)
		dispatcher.logFailure(request.Context(), "settlement_fence_failed", err)
		outcome = "ledger_unavailable"
		writeBillingError(writer, http.StatusServiceUnavailable, "x402_ledger_unavailable", "payment settlement could not begin")
		return
	}
	settleResponse, settleErr := dispatcher.facilitator.SettlePayment(request.Context(), payment, requirement)
	if settleErr != nil || settleResponse == nil || !settleResponse.Success {
		if x402wire.IsFailure(settleErr, x402wire.FailureRejected) {
			if err := dispatcher.failDetached(request.Context(), paymentID, owner, x402wire.CodeFacilitatorRejected); err != nil {
				dispatcher.markUnknownDetached(request.Context(), paymentID, owner)
				outcome = "settlement_unknown"
				writeBillingError(writer, http.StatusServiceUnavailable, "x402_settlement_unknown", "payment settlement outcome is unknown")
				return
			}
			outcome = "settle_rejected"
			dispatcher.writePaymentRequired(writer, requirement, x402wire.CodeFacilitatorRejected)
			return
		}
		dispatcher.markUnknownDetached(request.Context(), paymentID, owner)
		outcome = "settlement_unknown"
		writeBillingError(writer, http.StatusServiceUnavailable, "x402_settlement_unknown", "payment settlement outcome is unknown")
		return
	}
	responseHeader, err := dispatcher.codec.EncodePaymentResponse(*settleResponse)
	if err != nil {
		dispatcher.markUnknownDetached(request.Context(), paymentID, owner)
		outcome = "settlement_unknown"
		writeBillingError(writer, http.StatusServiceUnavailable, "x402_settlement_unknown", "payment settlement outcome is unknown")
		return
	}
	transactionHash, ok := transactionHashFromHex(settleResponse.Transaction)
	if !ok {
		dispatcher.markUnknownDetached(request.Context(), paymentID, owner)
		outcome = "settlement_unknown"
		writeBillingError(writer, http.StatusServiceUnavailable, "x402_settlement_unknown", "payment settlement outcome is unknown")
		return
	}
	if _, err := dispatcher.ledger.MarkSettled(
		request.Context(), paymentID, owner, transactionHash, dispatcher.observedAt(),
	); err != nil {
		dispatcher.markUnknownDetached(request.Context(), paymentID, owner)
		dispatcher.logFailure(request.Context(), "settlement_commit_unknown", err)
		outcome = "settlement_unknown"
		writeBillingError(writer, http.StatusServiceUnavailable, "x402_settlement_unknown", "payment settlement outcome is unknown")
		return
	}
	writer.Header().Set(x402wire.PaymentResponseHeader, responseHeader)
	releaseCapturedResponse(writer, capture)
	outcome = "settled"
}

func invokeCapturedHandler(capture *capturedResponse, request *http.Request, handler http.Handler) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	handler.ServeHTTP(capture, request)
	return false
}

func (dispatcher *HTTPDispatcher) writePaymentRequired(
	writer http.ResponseWriter,
	requirement x402wire.Requirement,
	code string,
) {
	header, err := dispatcher.codec.EncodePaymentRequired(requirement.PaymentRequired(code))
	if err != nil {
		writeBillingError(writer, http.StatusServiceUnavailable, "x402_unavailable", "request billing is unavailable")
		return
	}
	writer.Header().Set(x402wire.PaymentRequiredHeader, header)
	writeBillingError(writer, http.StatusPaymentRequired, "payment_required", "payment is required")
}

func (dispatcher *HTTPDispatcher) writeDuplicate(writer http.ResponseWriter, payment Payment) {
	if payment.State == StateSettling {
		writeBillingError(writer, http.StatusServiceUnavailable, "x402_settlement_unknown", "payment settlement outcome is unknown")
		return
	}
	writeBillingError(writer, http.StatusConflict, "x402_payment_replayed", "payment authorization was already used")
}

func (dispatcher *HTTPDispatcher) resolveUser(
	ctx context.Context,
	payer common.Address,
) (*string, error) {
	if dispatcher.userResolver == nil {
		return nil, nil
	}
	id, found, err := dispatcher.userResolver.UserIDForPayer(ctx, payer)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return &id, nil
}

func (dispatcher *HTTPDispatcher) failDetached(
	parent context.Context,
	paymentID, owner, code string,
) error {
	ctx, cancel := detachedPersistenceContext(parent)
	defer cancel()
	_, err := dispatcher.ledger.MarkFailed(ctx, paymentID, owner, code, dispatcher.observedAt())
	if err != nil {
		dispatcher.logFailure(parent, code, err)
	}
	return err
}

func (dispatcher *HTTPDispatcher) markUnknownDetached(
	parent context.Context,
	paymentID, owner string,
) {
	ctx, cancel := detachedPersistenceContext(parent)
	defer cancel()
	if _, err := dispatcher.ledger.MarkSettlementUnknown(
		ctx, paymentID, owner, dispatcher.observedAt(),
	); err != nil {
		dispatcher.logFailure(parent, "settlement_unknown_commit_failed", err)
	}
}

func detachedPersistenceContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), billingPersistenceTimeout)
}

func (dispatcher *HTTPDispatcher) observedAt() time.Time {
	return dispatcher.now().UTC().Truncate(time.Microsecond)
}

func (dispatcher *HTTPDispatcher) logFailure(ctx context.Context, code string, err error) {
	dispatcher.logger.ErrorContext(
		ctx, "x402 request failed", "error_code", code,
		"error_type", fmt.Sprintf("%T", err),
	)
}

func optionalAPIKeyPrefix(identity APIKeyIdentity) *string {
	if !identity.Authenticated || identity.Prefix == "" {
		return nil
	}
	value := identity.Prefix
	return &value
}

func addressFromHex(value string) (common.Address, bool) {
	var result common.Address
	canonical, ok := canonicalFixedHex(value, len(result))
	if !ok {
		return result, false
	}
	decoded, err := hex.DecodeString(canonical[2:])
	if err != nil {
		return result, false
	}
	copy(result[:], decoded)
	return result, true
}

func transactionHashFromHex(value string) (common.Hash, bool) {
	var result common.Hash
	canonical, ok := canonicalFixedHex(value, len(result))
	if !ok {
		return result, false
	}
	decoded, err := hex.DecodeString(canonical[2:])
	if err != nil {
		return result, false
	}
	copy(result[:], decoded)
	return result, true
}

func boundaryCode(err error) string {
	var boundary *x402wire.BoundaryError
	if errors.As(err, &boundary) && boundary.Code != "" {
		return boundary.Code
	}
	return x402wire.CodeHeaderMalformed
}

type paidOperationKey struct{}

func withPaidOperation(ctx context.Context, operation string) context.Context {
	return context.WithValue(ctx, paidOperationKey{}, operation)
}

// PaidOperationFrom returns the operation identity placed only around one
// verified, fenced paid handler invocation. There is intentionally no exported
// setter.
func PaidOperationFrom(ctx context.Context) (string, bool) {
	operation, ok := ctx.Value(paidOperationKey{}).(string)
	return operation, ok && operation != ""
}

type billingErrorEnvelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func writeBillingError(writer http.ResponseWriter, status int, code, message string) {
	response := billingErrorEnvelope{}
	response.Error.Code = code
	response.Error.Message = message
	response.Error.RequestID = writer.Header().Get("X-Request-ID")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(response)
}
