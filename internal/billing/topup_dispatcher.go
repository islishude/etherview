package billing

import (
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/billing/x402wire"
	x402 "github.com/x402-foundation/x402/go/v2"
)

type PaymentFacilitator interface {
	VerifyPayment(context.Context, x402wire.Payment, x402wire.Requirement) (*x402.VerifyResponse, error)
	SettlePayment(context.Context, x402wire.Payment, x402wire.Requirement) (*x402.SettleResponse, error)
	OriginDigest() [32]byte
}

func PaymentHeaderPresent(header http.Header) bool {
	_, ok := header[http.CanonicalHeaderKey(x402wire.PaymentSignatureHeader)]
	return ok
}

func transactionHashFromHex(value string) (common.Hash, bool) {
	var result common.Hash
	if len(value) != 2+len(result)*2 || !strings.HasPrefix(value, "0x") {
		return result, false
	}
	decoded, err := hex.DecodeString(value[2:])
	if err != nil || len(decoded) != len(result) {
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

type TopupPaymentLedger interface {
	ReserveTopup(context.Context, ReserveInput) (Reservation, error)
	MarkVerified(context.Context, VerifiedInput) (Payment, error)
	Get(context.Context, string) (Payment, error)
}

type TopupAccountLedger interface {
	BeginTopupSettlement(context.Context, string, string, string, time.Time) error
	MarkTopupSettlementUnknown(context.Context, string, string, string, time.Time) error
	MarkTopupSettlementPending(context.Context, string, string, string, common.Hash, time.Time) error
	CreditTopup(context.Context, string, string, common.Hash, time.Time) (Account, error)
	FailTopupPayment(context.Context, string, string, string, string, time.Time) error
	FailTopupSettlement(context.Context, string, string, string, string, time.Time) error
	TopupIntent(context.Context, string, string) (TopupIntent, error)
}

type TopupSuccessWriter func(http.ResponseWriter, Account, TopupIntent, Payment)

type TopupDispatcherOptions struct {
	Payments          TopupPaymentLedger
	Accounts          TopupAccountLedger
	Facilitator       PaymentFacilitator
	Codec             *x402wire.Codec
	FingerprintPepper []byte
	PublicOrigin      string
	Methods           []string
	MaxTimeoutSeconds int
	AssetName         string
	AssetVersion      string
	Now               func() time.Time
	Logger            *slog.Logger
	Observer          PrepaidObserver
}

type TopupDispatcher struct {
	payments          TopupPaymentLedger
	accounts          TopupAccountLedger
	facilitator       PaymentFacilitator
	codec             *x402wire.Codec
	fingerprintPepper []byte
	publicOrigin      string
	methods           []string
	maxTimeoutSeconds int
	assetName         string
	assetVersion      string
	now               func() time.Time
	logger            *slog.Logger
	observer          PrepaidObserver
}

func NewTopupDispatcher(options TopupDispatcherOptions) (*TopupDispatcher, error) {
	if options.Payments == nil || options.Accounts == nil || options.Facilitator == nil ||
		options.Codec == nil || len(options.FingerprintPepper) < 32 ||
		options.PublicOrigin == "" || options.MaxTimeoutSeconds < 1 ||
		options.MaxTimeoutSeconds > 300 || len(options.Methods) == 0 || len(options.Methods) > 2 {
		return nil, ErrInvalidInput
	}
	seen := make(map[string]bool, len(options.Methods))
	methods := make([]string, len(options.Methods))
	for index, method := range options.Methods {
		if method != x402wire.TransferMethodEIP3009 && method != x402wire.TransferMethodPermit2 || seen[method] {
			return nil, ErrInvalidInput
		}
		if method == x402wire.TransferMethodEIP3009 &&
			(options.AssetName == "" || options.AssetVersion == "") {
			return nil, ErrInvalidInput
		}
		seen[method] = true
		methods[index] = method
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &TopupDispatcher{
		payments: options.Payments, accounts: options.Accounts,
		facilitator: options.Facilitator, codec: options.Codec,
		fingerprintPepper: append([]byte(nil), options.FingerprintPepper...),
		publicOrigin:      strings.TrimSuffix(options.PublicOrigin, "/"),
		methods:           methods, maxTimeoutSeconds: options.MaxTimeoutSeconds,
		assetName: options.AssetName, assetVersion: options.AssetVersion,
		now: now, logger: logger, observer: options.Observer,
	}, nil
}

func (dispatcher *TopupDispatcher) Serve(
	writer http.ResponseWriter,
	request *http.Request,
	intent TopupIntent,
	success TopupSuccessWriter,
	writeError UsageErrorWriter,
) {
	observedMethod, outcome := "other", "invalid"
	defer func() {
		if dispatcher != nil && dispatcher.observer != nil {
			dispatcher.observer.ObserveBillingTopup(observedMethod, outcome)
		}
	}()
	if writeError == nil {
		writeError = writeUsageError
	}
	if dispatcher == nil || request == nil || request.Method != http.MethodPost ||
		intent.State != TopupIntentOpen || intent.ExpiresAt.Before(dispatcher.now()) ||
		intent.Payer == (common.Address{}) || success == nil {
		writeError(writer, http.StatusConflict, "topup_intent_unavailable", "top-up intent is not payable")
		return
	}
	requirements, err := dispatcher.requirements(intent)
	if err != nil {
		outcome = "unavailable"
		writeError(writer, http.StatusServiceUnavailable, "topup_unavailable", "top-up payment is unavailable")
		return
	}
	payment, err := dispatcher.codec.DecodePaymentSignature(request.Header)
	if err != nil {
		if boundaryCode(err) == x402wire.CodeHeaderMissing {
			outcome = "required"
			dispatcher.writeRequired(writer, requirements, "payment_required", writeError)
			return
		}
		writeError(writer, http.StatusBadRequest, boundaryCode(err), "payment authorization is invalid")
		return
	}
	observedMethod = payment.TransferMethod()
	requirement, ok := requirementForMethod(requirements, payment.TransferMethod())
	if !ok || requirement.Match(payment) != nil || !strings.EqualFold(payment.Payer(), intent.Payer.Hex()) {
		writeError(writer, http.StatusBadRequest, x402wire.CodePaymentMismatch, "payment authorization does not match the top-up intent")
		return
	}
	fingerprint, err := x402wire.Fingerprint(dispatcher.fingerprintPepper, payment)
	if err != nil {
		outcome = "ledger_unavailable"
		writeError(writer, http.StatusBadRequest, boundaryCode(err), "payment authorization is invalid")
		return
	}
	resourceDigest := requirement.ResourceDigest()
	requirementDigest := requirement.RequirementDigest()
	intentID, userID := intent.ID, intent.UserID
	reservation, err := dispatcher.payments.ReserveTopup(request.Context(), ReserveInput{
		Fingerprint: Digest(fingerprint), Operation: "createBillingTopup",
		Method: http.MethodPost, Purpose: "account_topup",
		AssetTransferMethod: payment.TransferMethod(), PaymentFlow: payment.PaymentFlow(),
		FingerprintVersion: int16(payment.FingerprintVersion()), TopupIntentID: &intentID,
		UserID: &userID, ExpectedPayer: intent.Payer,
		ResourceDigest: Digest(resourceDigest), RequirementDigest: Digest(requirementDigest),
		Network: intent.Network, Asset: intent.Asset, AmountAtomic: intent.AmountAtomic,
		Recipient: intent.Recipient, FacilitatorDigest: Digest(dispatcher.facilitator.OriginDigest()),
		ObservedAt: dispatcher.now().UTC(),
	})
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "topup_ledger_unavailable", "top-up payment cannot be reserved")
		return
	}
	if !reservation.Owned {
		outcome = "replayed"
		status := http.StatusConflict
		if reservation.Payment.State == StateSettling {
			status = http.StatusServiceUnavailable
		}
		writeError(writer, status, "topup_payment_replayed", "payment authorization was already used")
		return
	}
	paymentID, owner := reservation.Payment.ID, reservation.Owner
	verified, verifyErr := dispatcher.facilitator.VerifyPayment(request.Context(), payment, requirement)
	if verifyErr != nil || verified == nil || !verified.IsValid ||
		!strings.EqualFold(verified.Payer, intent.Payer.Hex()) {
		code, status := x402wire.CodeFacilitatorUnavailable, http.StatusServiceUnavailable
		if x402wire.IsFailure(verifyErr, x402wire.FailureRejected) {
			code, status = x402wire.CodeFacilitatorRejected, http.StatusPaymentRequired
			outcome = "verify_rejected"
		} else {
			outcome = "verify_unavailable"
		}
		if failErr := dispatcher.accounts.FailTopupPayment(
			request.Context(), paymentID, owner, intent.ID, code, dispatcher.now().UTC(),
		); failErr != nil {
			writeError(writer, http.StatusServiceUnavailable, "topup_ledger_unavailable", "top-up failure cannot be committed")
			return
		}
		if status == http.StatusPaymentRequired {
			dispatcher.writeRequired(writer, requirements, code, writeError)
			return
		}
		writeError(writer, status, code, "payment verification is unavailable")
		return
	}
	if _, err := dispatcher.payments.MarkVerified(request.Context(), VerifiedInput{
		PaymentID: paymentID, Owner: owner, Payer: intent.Payer,
		UserID: &intent.UserID, ObservedAt: dispatcher.now().UTC(),
	}); err != nil {
		outcome = "ledger_unavailable"
		_ = dispatcher.accounts.FailTopupPayment(request.Context(), paymentID, owner, intent.ID, "ledger_verify_failed", dispatcher.now().UTC())
		writeError(writer, http.StatusServiceUnavailable, "topup_ledger_unavailable", "payment verification cannot be committed")
		return
	}
	if err := dispatcher.accounts.BeginTopupSettlement(
		request.Context(), paymentID, owner, intent.ID, dispatcher.now().UTC(),
	); err != nil {
		outcome = "ledger_unavailable"
		_ = dispatcher.accounts.FailTopupPayment(request.Context(), paymentID, owner, intent.ID, "settlement_fence_failed", dispatcher.now().UTC())
		writeError(writer, http.StatusServiceUnavailable, "topup_ledger_unavailable", "payment settlement cannot begin")
		return
	}
	settled, settleErr := dispatcher.facilitator.SettlePayment(request.Context(), payment, requirement)
	if settleErr != nil || settled == nil || !settled.Success {
		if x402wire.IsFailure(settleErr, x402wire.FailureSettlementPending) && settled != nil {
			hash, valid := transactionHashFromHex(settled.Transaction)
			if valid && dispatcher.accounts.MarkTopupSettlementPending(
				request.Context(), paymentID, owner, intent.ID, hash, dispatcher.now().UTC(),
			) == nil {
				outcome = "settlement_pending"
				if header, encodeErr := dispatcher.codec.EncodePaymentResponse(*settled); encodeErr == nil {
					writer.Header().Set(x402wire.PaymentResponseHeader, header)
				}
				writeError(writer, http.StatusPaymentRequired, x402wire.CodeSettlementPending, "payment settlement is pending")
				return
			}
		}
		if x402wire.IsFailure(settleErr, x402wire.FailureRejected) {
			if dispatcher.accounts.FailTopupSettlement(
				request.Context(), paymentID, owner, intent.ID,
				x402wire.CodeFacilitatorRejected, dispatcher.now().UTC(),
			) == nil {
				outcome = "settle_rejected"
				writeError(writer, http.StatusPaymentRequired, x402wire.CodeFacilitatorRejected, "payment settlement was rejected")
				return
			}
		}
		_ = dispatcher.accounts.MarkTopupSettlementUnknown(
			request.Context(), paymentID, owner, intent.ID, dispatcher.now().UTC(),
		)
		outcome = "settlement_unknown"
		writeError(writer, http.StatusServiceUnavailable, x402wire.CodeSettlementUnknown, "payment settlement outcome is unknown")
		return
	}
	transactionHash, ok := transactionHashFromHex(settled.Transaction)
	if !ok || !strings.EqualFold(settled.Payer, intent.Payer.Hex()) ||
		string(settled.Network) != intent.Network {
		_ = dispatcher.accounts.MarkTopupSettlementUnknown(request.Context(), paymentID, owner, intent.ID, dispatcher.now().UTC())
		outcome = "settlement_unknown"
		writeError(writer, http.StatusServiceUnavailable, x402wire.CodeSettlementUnknown, "payment settlement outcome is unknown")
		return
	}
	responseHeader, err := dispatcher.codec.EncodePaymentResponse(*settled)
	if err != nil {
		_ = dispatcher.accounts.MarkTopupSettlementUnknown(request.Context(), paymentID, owner, intent.ID, dispatcher.now().UTC())
		outcome = "settlement_unknown"
		writeError(writer, http.StatusServiceUnavailable, x402wire.CodeSettlementUnknown, "payment settlement outcome is unknown")
		return
	}
	account, err := dispatcher.accounts.CreditTopup(
		request.Context(), paymentID, owner, transactionHash, dispatcher.now().UTC(),
	)
	if err != nil {
		_ = dispatcher.accounts.MarkTopupSettlementUnknown(request.Context(), paymentID, owner, intent.ID, dispatcher.now().UTC())
		outcome = "ledger_unavailable"
		writeError(writer, http.StatusServiceUnavailable, x402wire.CodeSettlementUnknown, "top-up credit outcome requires inspection")
		return
	}
	updatedIntent, err := dispatcher.accounts.TopupIntent(request.Context(), intent.UserID, intent.ID)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "topup_receipt_unavailable", "top-up was credited but its receipt is unavailable")
		return
	}
	updatedPayment, err := dispatcher.payments.Get(request.Context(), paymentID)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "topup_receipt_unavailable", "top-up was credited but its receipt is unavailable")
		return
	}
	writer.Header().Set(x402wire.PaymentResponseHeader, responseHeader)
	outcome = "credited"
	success(writer, account, updatedIntent, updatedPayment)
}

func (dispatcher *TopupDispatcher) requirements(intent TopupIntent) ([]x402wire.Requirement, error) {
	resource := x402.ResourceInfo{
		URL:      dispatcher.publicOrigin + "/api/v1/billing/topup-intents/" + intent.ID + "/pay",
		MimeType: "application/json", ServiceName: "Etherview",
	}
	result := make([]x402wire.Requirement, len(dispatcher.methods))
	for index, method := range dispatcher.methods {
		options := x402wire.RequirementOptions{
			Network: intent.Network, Asset: intent.Asset.Hex(), Amount: intent.AmountAtomic,
			PayTo: intent.Recipient.Hex(), MaxTimeoutSeconds: dispatcher.maxTimeoutSeconds,
			AssetTransferMethod: method, PaymentFlow: x402wire.PaymentFlowAuthorization,
			Resource: resource,
		}
		if method == x402wire.TransferMethodEIP3009 {
			options.AssetEIP712Name = dispatcher.assetName
			options.AssetEIP712Version = dispatcher.assetVersion
		}
		var err error
		result[index], err = x402wire.NewRequirement(options)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (dispatcher *TopupDispatcher) writeRequired(
	writer http.ResponseWriter,
	requirements []x402wire.Requirement,
	code string,
	writeError UsageErrorWriter,
) {
	required, err := x402wire.PaymentRequired(requirements, code)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "topup_unavailable", "top-up payment is unavailable")
		return
	}
	header, err := dispatcher.codec.EncodePaymentRequired(required)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "topup_unavailable", "top-up payment is unavailable")
		return
	}
	writer.Header().Set(x402wire.PaymentRequiredHeader, header)
	writeError(writer, http.StatusPaymentRequired, "payment_required", "payment is required")
}

func requirementForMethod(
	requirements []x402wire.Requirement,
	method string,
) (x402wire.Requirement, bool) {
	for _, requirement := range requirements {
		if requirement.TransferMethod() == method {
			return requirement, true
		}
	}
	return x402wire.Requirement{}, false
}
