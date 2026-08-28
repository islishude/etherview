package httpapi

import (
	"errors"
	"math/big"
	"net/http"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/billing"
)

func (h *Handler) prepaidBillingAvailable(w http.ResponseWriter, r *http.Request) bool {
	if h.cfg.Features.APIBilling && h.prepaidBilling != nil {
		return true
	}
	writeError(w, r, http.StatusServiceUnavailable, "billing_unavailable", "prepaid API billing is unavailable", nil)
	return false
}

func (h *Handler) currentBillingAccount(w http.ResponseWriter, r *http.Request) {
	if !h.prepaidBillingAvailable(w, r) {
		return
	}
	authentication, ok := h.requireUserSession(w, r)
	if !ok {
		return
	}
	account, err := h.prepaidBilling.EnsureAccount(
		r.Context(), authentication.Session.User.ID, h.now().UTC(),
	)
	if err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	model, err := billingAccountModel(account)
	if err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.BillingAccountResponse{Data: model, Meta: h.meta(r)})
}

func (h *Handler) createBillingTopupIntent(w http.ResponseWriter, r *http.Request) {
	if !h.prepaidBillingAvailable(w, r) || !h.cfg.Features.X402Topups ||
		!h.requireAuthOrigin(w, r) {
		return
	}
	authentication, ok := h.requireUserSession(w, r)
	if !ok || !h.requireCSRF(w, r, authentication) {
		return
	}
	var request gen.BillingTopupIntentCreateRequest
	if !decodeAuthJSON(w, r, &request) {
		return
	}
	amount := string(request.AmountAtomic)
	if !topupAmountInRange(
		amount,
		h.cfg.Billing.MinimumTopupAmountAtomic,
		h.cfg.Billing.MaximumTopupAmountAtomic,
	) {
		writeError(w, r, http.StatusBadRequest, "topup_amount_invalid", "top-up amount is outside the configured bounds", nil)
		return
	}
	payer := common.HexToAddress(string(authentication.Session.User.Address))
	intent, err := h.prepaidBilling.CreateTopupIntent(r.Context(), billing.CreateTopupIntentInput{
		UserID: authentication.Session.User.ID, Payer: payer,
		AmountAtomic: amount, ObservedAt: h.now().UTC(),
	})
	if err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	model, err := billingTopupIntentModel(intent, nil)
	if err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, gen.BillingTopupIntentResponse{Data: model, Meta: h.meta(r)})
}

func (h *Handler) listCurrentBillingTopupIntents(w http.ResponseWriter, r *http.Request) {
	userID, after, limit, ok := h.currentPrepaidPage(w, r, "user-topups")
	if !ok {
		return
	}
	intents, err := h.prepaidBilling.ListTopupIntents(
		r.Context(), userID, after, limit+1,
	)
	if err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	h.writeBillingTopupPage(
		w, r, intents, limit, "user-topups", userID,
	)
}

func (h *Handler) getBillingTopupIntent(w http.ResponseWriter, r *http.Request) {
	if !h.prepaidBillingAvailable(w, r) {
		return
	}
	authentication, ok := h.requireUserSession(w, r)
	if !ok {
		return
	}
	intentID, ok := billingIntentID(w, r)
	if !ok {
		return
	}
	intent, err := h.prepaidBilling.TopupIntent(
		r.Context(), authentication.Session.User.ID, intentID,
	)
	if err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	model, err := billingTopupIntentModel(intent, nil)
	if err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.BillingTopupIntentResponse{Data: model, Meta: h.meta(r)})
}

func (h *Handler) payBillingTopupIntent(w http.ResponseWriter, r *http.Request) {
	if !h.prepaidBillingAvailable(w, r) || !h.cfg.Features.X402Topups ||
		!h.requireAuthOrigin(w, r) {
		return
	}
	authentication, ok := h.requireUserSession(w, r)
	if !ok || !h.requireCSRF(w, r, authentication) {
		return
	}
	intentID, ok := billingIntentID(w, r)
	if !ok {
		return
	}
	intent, err := h.prepaidBilling.TopupIntent(
		r.Context(), authentication.Session.User.ID, intentID,
	)
	if err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	if h.topupBilling == nil {
		writeError(w, r, http.StatusServiceUnavailable, "topup_payment_unavailable", "x402 top-up payment is unavailable", nil)
		return
	}
	h.topupBilling.Serve(
		w,
		r,
		intent,
		func(destination http.ResponseWriter, account billing.Account, updated billing.TopupIntent, payment billing.Payment) {
			model, modelErr := billingTopupIntentModel(updated, payment.TransactionHash)
			if modelErr != nil {
				h.handlePrepaidBillingError(destination, r, modelErr)
				return
			}
			accountModel, modelErr := billingAccountModel(account)
			if modelErr != nil {
				h.handlePrepaidBillingError(destination, r, modelErr)
				return
			}
			writeJSON(destination, http.StatusOK, gen.BillingTopupReceiptResponse{
				Data: gen.BillingTopupReceipt{Intent: model, Account: accountModel},
				Meta: h.meta(r),
			})
		},
		func(destination http.ResponseWriter, status int, code, message string) {
			writeError(destination, r, status, code, message, nil)
		},
	)
}

func (h *Handler) listCurrentUserBillingUsage(w http.ResponseWriter, r *http.Request) {
	userID, after, limit, ok := h.currentPrepaidPage(w, r, "user-usage")
	if !ok {
		return
	}
	charges, err := h.prepaidBilling.ListUsage(
		r.Context(), userID, after, limit+1,
	)
	if err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	h.writeBillingUsagePage(w, r, charges, limit, "user-usage", userID)
}

func (h *Handler) currentPrepaidPage(
	w http.ResponseWriter,
	r *http.Request,
	scope string,
) (string, *billing.PageAfter, int, bool) {
	if !h.prepaidBillingAvailable(w, r) {
		return "", nil, 0, false
	}
	authentication, ok := h.requireUserSession(w, r)
	if !ok {
		return "", nil, 0, false
	}
	values, ok := parseStrictBillingQuery(w, r, "cursor", "limit")
	if !ok {
		return "", nil, 0, false
	}
	limit, ok := parseBillingPageLimit(w, r, values)
	if !ok {
		return "", nil, 0, false
	}
	after, ok := h.decodeBillingPageCursor(
		w, r, values, scope, authentication.Session.User.ID,
		normalizedBillingFilter{},
	)
	if !ok {
		return "", nil, 0, false
	}
	return authentication.Session.User.ID, after, limit, true
}

func (h *Handler) listAdminBillingAccounts(w http.ResponseWriter, r *http.Request) {
	if !h.prepaidBillingAvailable(w, r) {
		return
	}
	if _, ok := h.requireAdminSession(w, r); !ok {
		return
	}
	values, ok := parseStrictBillingQuery(w, r, "cursor", "limit")
	if !ok {
		return
	}
	limit, ok := parseBillingPageLimit(w, r, values)
	if !ok {
		return
	}
	position, ok := h.decodeBillingPageCursor(
		w, r, values, "admin-accounts", "", normalizedBillingFilter{},
	)
	if !ok {
		return
	}
	var after *billing.AccountPageAfter
	if position != nil {
		after = &billing.AccountPageAfter{UpdatedAt: position.CreatedAt, UserID: position.ID}
	}
	accounts, err := h.prepaidBilling.ListAdminAccounts(r.Context(), after, limit+1)
	if err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	h.writeBillingAccountPage(w, r, accounts, limit)
}

func (h *Handler) adjustAdminBillingAccount(w http.ResponseWriter, r *http.Request) {
	if !h.prepaidBillingAvailable(w, r) || !h.requireAuthOrigin(w, r) {
		return
	}
	authentication, ok := h.requireAdminSession(w, r)
	if !ok || !h.requireCSRF(w, r, authentication) {
		return
	}
	userID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || userID.Version() != 4 {
		writeError(w, r, http.StatusNotFound, "billing_account_not_found", "billing account was not found", nil)
		return
	}
	var input gen.BillingAdjustmentRequest
	if !decodeAuthJSON(w, r, &input) {
		return
	}
	direction := string(input.Direction)
	if !input.Direction.Valid() || !canonicalPositiveUint256(string(input.AmountAtomic)) {
		writeError(w, r, http.StatusBadRequest, "billing_adjustment_invalid", "billing adjustment is invalid", nil)
		return
	}
	if _, err := h.prepaidBilling.EnsureAccount(r.Context(), userID.String(), h.now().UTC()); err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	account, err := h.prepaidBilling.Adjust(r.Context(), billing.AdjustmentInput{
		UserID: userID.String(), Direction: direction,
		AmountAtomic: string(input.AmountAtomic), Reason: input.Reason,
		ObservedAt: h.now().UTC(),
	})
	if err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	model, err := billingAccountModel(account)
	if err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.BillingAccountResponse{Data: model, Meta: h.meta(r)})
}

func (h *Handler) listAdminBillingTopups(w http.ResponseWriter, r *http.Request) {
	if !h.prepaidBillingAvailable(w, r) {
		return
	}
	if _, ok := h.requireAdminSession(w, r); !ok {
		return
	}
	values, ok := parseStrictBillingQuery(w, r, "cursor", "limit")
	if !ok {
		return
	}
	limit, ok := parseBillingPageLimit(w, r, values)
	if !ok {
		return
	}
	after, ok := h.decodeBillingPageCursor(w, r, values, "admin-topups", "", normalizedBillingFilter{})
	if !ok {
		return
	}
	intents, err := h.prepaidBilling.ListAdminTopupIntents(r.Context(), after, limit+1)
	if err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	h.writeBillingTopupPage(w, r, intents, limit, "admin-topups", "")
}

func (h *Handler) listAdminBillingUsage(w http.ResponseWriter, r *http.Request) {
	if !h.prepaidBillingAvailable(w, r) {
		return
	}
	if _, ok := h.requireAdminSession(w, r); !ok {
		return
	}
	values, ok := parseStrictBillingQuery(w, r, "cursor", "limit")
	if !ok {
		return
	}
	limit, ok := parseBillingPageLimit(w, r, values)
	if !ok {
		return
	}
	after, ok := h.decodeBillingPageCursor(w, r, values, "admin-usage", "", normalizedBillingFilter{})
	if !ok {
		return
	}
	charges, err := h.prepaidBilling.ListAdminUsage(r.Context(), after, limit+1)
	if err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	h.writeBillingUsagePage(w, r, charges, limit, "admin-usage", "")
}

func (h *Handler) adminPrepaidBillingSummary(w http.ResponseWriter, r *http.Request) {
	if !h.prepaidBillingAvailable(w, r) {
		return
	}
	if _, ok := h.requireAdminSession(w, r); !ok {
		return
	}
	if _, ok := parseStrictBillingQuery(w, r); !ok {
		return
	}
	summary, err := h.prepaidBilling.AccountSummary(r.Context())
	if err != nil {
		h.handlePrepaidBillingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.BillingAccountSummaryResponse{
		Data: gen.BillingAccountSummary{
			AccountCount:      summary.AccountCount,
			TotalCreditAtomic: summary.TotalCreditAtomic,
			TotalDebitAtomic:  summary.TotalDebitAtomic,
			ReservedAtomic:    summary.ReservedAtomic,
			AvailableAtomic:   summary.AvailableAtomic,
		},
		Meta: h.meta(r),
	})
}

func (h *Handler) writeBillingTopupPage(
	w http.ResponseWriter,
	r *http.Request,
	intents []billing.TopupIntent,
	limit int,
	scope, userID string,
) {
	if len(intents) > limit+1 {
		h.handlePrepaidBillingError(w, r, billing.ErrIntegrity)
		return
	}
	hasNext := len(intents) > limit
	if hasNext {
		intents = intents[:limit]
	}
	models := make([]gen.BillingTopupIntent, len(intents))
	for index := range intents {
		if userID != "" && intents[index].UserID != userID {
			h.handlePrepaidBillingError(w, r, billing.ErrIntegrity)
			return
		}
		var err error
		models[index], err = billingTopupIntentModel(intents[index], nil)
		if err != nil {
			h.handlePrepaidBillingError(w, r, err)
			return
		}
	}
	meta := h.meta(r)
	if hasNext {
		cursor, err := h.encodeBillingPageCursor(
			billing.Payment{ID: intents[len(intents)-1].ID, CreatedAt: intents[len(intents)-1].CreatedAt},
			scope, userID, normalizedBillingFilter{},
		)
		if err != nil {
			h.handlePrepaidBillingError(w, r, err)
			return
		}
		meta.NextCursor = &cursor
	}
	writeJSON(w, http.StatusOK, gen.BillingTopupIntentListResponse{Data: models, Meta: meta})
}

func (h *Handler) writeBillingUsagePage(
	w http.ResponseWriter,
	r *http.Request,
	charges []billing.UsageCharge,
	limit int,
	scope, userID string,
) {
	if len(charges) > limit+1 {
		h.handlePrepaidBillingError(w, r, billing.ErrIntegrity)
		return
	}
	hasNext := len(charges) > limit
	if hasNext {
		charges = charges[:limit]
	}
	models := make([]gen.BillingUsage, len(charges))
	for index := range charges {
		if userID != "" && charges[index].UserID != userID {
			h.handlePrepaidBillingError(w, r, billing.ErrIntegrity)
			return
		}
		var err error
		models[index], err = billingUsageModel(charges[index])
		if err != nil {
			h.handlePrepaidBillingError(w, r, err)
			return
		}
	}
	meta := h.meta(r)
	if hasNext {
		cursor, err := h.encodeBillingPageCursor(
			billing.Payment{ID: charges[len(charges)-1].ID, CreatedAt: charges[len(charges)-1].CreatedAt},
			scope, userID, normalizedBillingFilter{},
		)
		if err != nil {
			h.handlePrepaidBillingError(w, r, err)
			return
		}
		meta.NextCursor = &cursor
	}
	writeJSON(w, http.StatusOK, gen.BillingUsageListResponse{Data: models, Meta: meta})
}

func (h *Handler) writeBillingAccountPage(
	w http.ResponseWriter,
	r *http.Request,
	accounts []billing.Account,
	limit int,
) {
	if len(accounts) > limit+1 {
		h.handlePrepaidBillingError(w, r, billing.ErrIntegrity)
		return
	}
	hasNext := len(accounts) > limit
	if hasNext {
		accounts = accounts[:limit]
	}
	models := make([]gen.BillingAccount, len(accounts))
	for index := range accounts {
		var err error
		models[index], err = billingAccountModel(accounts[index])
		if err != nil {
			h.handlePrepaidBillingError(w, r, err)
			return
		}
	}
	meta := h.meta(r)
	if hasNext {
		last := accounts[len(accounts)-1]
		cursor, err := h.encodeBillingPageCursor(
			billing.Payment{ID: last.UserID, CreatedAt: last.UpdatedAt},
			"admin-accounts", "", normalizedBillingFilter{},
		)
		if err != nil {
			h.handlePrepaidBillingError(w, r, err)
			return
		}
		meta.NextCursor = &cursor
	}
	writeJSON(w, http.StatusOK, gen.BillingAccountListResponse{Data: models, Meta: meta})
}

func billingAccountModel(account billing.Account) (gen.BillingAccount, error) {
	userID, err := uuid.Parse(account.UserID)
	if err != nil || userID.Version() != 4 || account.CreatedAt.IsZero() ||
		account.UpdatedAt.Before(account.CreatedAt) {
		return gen.BillingAccount{}, billing.ErrIntegrity
	}
	return gen.BillingAccount{
		UserId: userID, Network: account.Network,
		Asset:             billingAddressModel(account.Asset),
		TotalCreditAtomic: account.TotalCreditAtomic,
		TotalDebitAtomic:  account.TotalDebitAtomic,
		ReservedAtomic:    account.ReservedAtomic,
		AvailableAtomic:   account.AvailableAtomic,
		CreatedAt:         account.CreatedAt.UTC(), UpdatedAt: account.UpdatedAt.UTC(),
	}, nil
}

func billingTopupIntentModel(
	intent billing.TopupIntent,
	transactionHash *common.Hash,
) (gen.BillingTopupIntent, error) {
	identifier, err := uuid.Parse(intent.ID)
	userID, userErr := uuid.Parse(intent.UserID)
	if err != nil || identifier.Version() != 4 || userErr != nil ||
		userID.Version() != 4 || !intent.State.Valid() {
		return gen.BillingTopupIntent{}, billing.ErrIntegrity
	}
	model := gen.BillingTopupIntent{
		Id: identifier, UserId: userID,
		AmountAtomic: gen.Quantity(intent.AmountAtomic),
		Network:      intent.Network, Asset: billingAddressModel(intent.Asset),
		Recipient: billingAddressModel(intent.Recipient), Payer: billingAddressModel(intent.Payer),
		State: gen.BillingTopupIntentState(intent.State), ExpiresAt: intent.ExpiresAt.UTC(),
		CreatedAt: intent.CreatedAt.UTC(), UpdatedAt: intent.UpdatedAt.UTC(),
		FailureCode: intent.FailureCode, CreditedAt: intent.CreditedAt,
	}
	if intent.ActivePaymentID != nil {
		paymentID, paymentErr := uuid.Parse(*intent.ActivePaymentID)
		if paymentErr != nil || paymentID.Version() != 4 {
			return gen.BillingTopupIntent{}, billing.ErrIntegrity
		}
		model.PaymentId = &paymentID
	}
	if transactionHash == nil {
		transactionHash = intent.TransactionHash
	}
	if transactionHash != nil {
		value := gen.Hash(transactionHash.Hex())
		model.TransactionHash = &value
	}
	return model, nil
}

func billingUsageModel(charge billing.UsageCharge) (gen.BillingUsage, error) {
	identifier, err := uuid.Parse(charge.ID)
	userID, userErr := uuid.Parse(charge.UserID)
	if err != nil || identifier.Version() != 4 || userErr != nil ||
		userID.Version() != 4 || !charge.State.Valid() {
		return gen.BillingUsage{}, billing.ErrIntegrity
	}
	return gen.BillingUsage{
		Id: identifier, UserId: userID, ApiKeyPrefix: charge.APIKeyPrefix,
		Method: gen.BillingUsageMethod(charge.Method), Operation: charge.Operation,
		AmountAtomic: gen.Quantity(charge.AmountAtomic), State: gen.BillingUsageState(charge.State),
		FailureCode: charge.FailureCode, CreatedAt: charge.CreatedAt.UTC(), UpdatedAt: charge.UpdatedAt.UTC(),
	}, nil
}

func billingIntentID(w http.ResponseWriter, r *http.Request) (string, bool) {
	identifier, err := uuid.Parse(r.PathValue("id"))
	if err != nil || identifier.Version() != 4 {
		writeError(w, r, http.StatusNotFound, "topup_intent_not_found", "top-up intent was not found", nil)
		return "", false
	}
	return identifier.String(), true
}

func topupAmountInRange(value, minimum, maximum string) bool {
	if !canonicalPositiveUint256(value) || !canonicalPositiveUint256(minimum) ||
		!canonicalPositiveUint256(maximum) {
		return false
	}
	amount, _ := new(big.Int).SetString(value, 10)
	minAmount, _ := new(big.Int).SetString(minimum, 10)
	maxAmount, _ := new(big.Int).SetString(maximum, 10)
	return amount.Cmp(minAmount) >= 0 && amount.Cmp(maxAmount) <= 0
}

func (h *Handler) handlePrepaidBillingError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, billing.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "billing_not_found", "billing resource was not found", nil)
	case errors.Is(err, billing.ErrInsufficientCredit):
		writeError(w, r, http.StatusPaymentRequired, "billing_credit_required", "prepaid API credit is required", nil)
	case errors.Is(err, billing.ErrInvalidInput):
		writeError(w, r, http.StatusBadRequest, "billing_input_invalid", "billing input is invalid", nil)
	default:
		h.logger.ErrorContext(r.Context(), "prepaid billing failure", "error_code", "billing_unavailable")
		writeError(w, r, http.StatusServiceUnavailable, "billing_unavailable", "prepaid API billing is unavailable", nil)
	}
}
