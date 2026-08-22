package httpapi

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/apiops"
	"github.com/islishude/etherview/internal/billing"
)

const (
	defaultBillingPageSize   = 25
	maximumBillingPageSize   = 100
	maximumBillingInterval   = 31 * 24 * time.Hour
	maximumBillingQueryBytes = 4096
)

var (
	billingHTTPNetworkPattern = regexp.MustCompile(`^eip155:[1-9][0-9]*$`)
	billingHTTPCodePattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)
)

// BillingReader is deliberately independent from the settlement state
// machine. Production supplies the same writer-backed ledger instance, while
// HTTP tests can exercise read authorization without implementing payment
// transitions.
type BillingReader interface {
	ListUser(context.Context, string, *billing.PageAfter, int) ([]billing.Payment, error)
	ListAdmin(context.Context, billing.AdminFilter, *billing.PageAfter, int) ([]billing.Payment, error)
	Summary(context.Context, billing.AdminFilter) ([]billing.SummaryRow, error)
}

type normalizedBillingFilter struct {
	State     string `json:"state,omitempty"`
	Operation string `json:"operation,omitempty"`
	Network   string `json:"network,omitempty"`
	Asset     string `json:"asset,omitempty"`
	FromTime  string `json:"from_time,omitempty"`
	ToTime    string `json:"to_time,omitempty"`
}

type billingPageCursor struct {
	Scope     string                  `json:"scope"`
	ChainID   string                  `json:"chain_id"`
	UserID    string                  `json:"user_id,omitempty"`
	Filter    normalizedBillingFilter `json:"filter"`
	CreatedAt time.Time               `json:"created_at"`
	ID        string                  `json:"id"`
}

func (h *Handler) billingConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := parseStrictBillingQuery(w, r); !ok {
		return
	}
	data := gen.BillingConfig{
		Enabled:     h.cfg.Features.X402Billing,
		Scheme:      gen.BillingConfigSchemeExact,
		X402Version: gen.BillingConfigX402VersionN2,
		Routes:      make([]gen.BillingRoutePrice, 0, len(h.cfg.Billing.Routes)),
	}
	if !data.Enabled {
		writeJSON(w, http.StatusOK, gen.BillingConfigResponse{Data: data, Meta: h.meta(r)})
		return
	}

	asset, err := checksumAddress(h.cfg.Billing.Asset)
	if err != nil {
		h.handleBillingFailure(w, r, "map public billing configuration", err)
		return
	}
	recipient, err := checksumAddress(h.cfg.Billing.Recipient)
	if err != nil {
		h.handleBillingFailure(w, r, "map public billing configuration", err)
		return
	}
	data.Network = cloneStringPointer(h.cfg.Billing.Network)
	data.Asset = billingAPIAddressPointer(asset)
	decimals := int(h.cfg.Billing.AssetDecimals)
	data.AssetDecimals = &decimals
	data.AssetEip712Name = cloneStringPointer(h.cfg.Billing.AssetEIP712Name)
	data.AssetEip712Version = cloneStringPointer(h.cfg.Billing.AssetEIP712Version)
	data.Recipient = billingAPIAddressPointer(recipient)

	operations := make([]string, 0, len(h.cfg.Billing.Routes))
	for operation := range h.cfg.Billing.Routes {
		operations = append(operations, operation)
	}
	sort.Strings(operations)
	for _, operation := range operations {
		route := h.cfg.Billing.Routes[operation]
		spec, eligible := apiops.Lookup(operation)
		access := gen.BillingAccess(route.Access)
		if !eligible || !spec.BillingEligible || !access.Valid() ||
			!canonicalPositiveUint256(route.AmountAtomic) {
			h.handleBillingFailure(
				w, r, "map public billing configuration",
				errors.New("configured billing route is invalid"),
			)
			return
		}
		data.Routes = append(data.Routes, gen.BillingRoutePrice{
			Operation: operation, Access: access,
			AmountAtomic: gen.Quantity(route.AmountAtomic),
		})
	}
	writeJSON(w, http.StatusOK, gen.BillingConfigResponse{Data: data, Meta: h.meta(r)})
}

func (h *Handler) listCurrentUserBillingPayments(w http.ResponseWriter, r *http.Request) {
	if !h.userAuthAvailable(w, r) {
		return
	}
	authentication, ok := h.requireUserSession(w, r)
	if !ok {
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
	after, ok := h.decodeBillingPageCursor(
		w, r, values, "user", authentication.Session.User.ID,
		normalizedBillingFilter{},
	)
	if !ok {
		return
	}
	payments, err := h.billingReader.ListUser(
		r.Context(), authentication.Session.User.ID, after, limit+1,
	)
	if err != nil {
		h.handleBillingFailure(w, r, "list current user billing payments", err)
		return
	}
	h.writeBillingPaymentPage(
		w, r, payments, limit, "user", authentication.Session.User.ID,
		normalizedBillingFilter{},
	)
}

func (h *Handler) listAdminBillingPayments(w http.ResponseWriter, r *http.Request) {
	if !h.userAuthAvailable(w, r) {
		return
	}
	if _, ok := h.requireAdminSession(w, r); !ok {
		return
	}
	values, ok := parseStrictBillingQuery(
		w, r,
		"state", "operation", "network", "asset",
		"from_time", "to_time", "cursor", "limit",
	)
	if !ok {
		return
	}
	limit, ok := parseBillingPageLimit(w, r, values)
	if !ok {
		return
	}
	filter, normalized, ok := h.parseAdminBillingFilter(w, r, values, false)
	if !ok {
		return
	}
	after, ok := h.decodeBillingPageCursor(w, r, values, "admin", "", normalized)
	if !ok {
		return
	}
	payments, err := h.billingReader.ListAdmin(r.Context(), filter, after, limit+1)
	if err != nil {
		h.handleBillingFailure(w, r, "list administrator billing payments", err)
		return
	}
	h.writeBillingPaymentPage(w, r, payments, limit, "admin", "", normalized)
}

func (h *Handler) adminBillingSummary(w http.ResponseWriter, r *http.Request) {
	if !h.userAuthAvailable(w, r) {
		return
	}
	if _, ok := h.requireAdminSession(w, r); !ok {
		return
	}
	values, ok := parseStrictBillingQuery(
		w, r,
		"state", "operation", "network", "asset", "from_time", "to_time",
	)
	if !ok {
		return
	}
	filter, _, ok := h.parseAdminBillingFilter(w, r, values, true)
	if !ok {
		return
	}
	rows, err := h.billingReader.Summary(r.Context(), filter)
	if err != nil {
		h.handleBillingFailure(w, r, "summarize billing payments", err)
		return
	}
	models := make([]gen.BillingSummaryRow, 0, len(rows))
	totalCount, totalAmount := new(big.Int), new(big.Int)
	for _, row := range rows {
		model, count, amount, mapErr := billingSummaryRowModel(row)
		if mapErr != nil {
			h.handleBillingFailure(w, r, "map billing summary", mapErr)
			return
		}
		models = append(models, model)
		totalCount.Add(totalCount, count)
		totalAmount.Add(totalAmount, amount)
	}
	if !canonicalBillingAggregate(totalCount.String()) ||
		!canonicalBillingAggregate(totalAmount.String()) {
		h.handleBillingFailure(
			w, r, "map billing summary",
			errors.New("billing summary total exceeds the public contract"),
		)
		return
	}
	writeJSON(w, http.StatusOK, gen.BillingSummaryResponse{
		Data: gen.BillingSummary{
			FromTime: filter.FromTime.UTC(), ToTime: filter.ToTime.UTC(),
			PaymentCount: gen.BillingAggregateQuantity(totalCount.String()),
			AmountAtomic: gen.BillingAggregateQuantity(totalAmount.String()),
			Rows:         models,
		},
		Meta: h.meta(r),
	})
}

func (h *Handler) writeBillingPaymentPage(
	w http.ResponseWriter,
	r *http.Request,
	payments []billing.Payment,
	limit int,
	scope string,
	userID string,
	filter normalizedBillingFilter,
) {
	if len(payments) > limit+1 {
		h.handleBillingFailure(
			w, r, "map billing payment page",
			errors.New("billing reader exceeded its requested bound"),
		)
		return
	}
	hasNext := len(payments) > limit
	if hasNext {
		payments = payments[:limit]
	}
	models := make([]gen.BillingPayment, len(payments))
	for index := range payments {
		if scope == "user" &&
			(payments[index].UserID == nil || *payments[index].UserID != userID) {
			h.handleBillingFailure(
				w, r, "map billing payment",
				errors.New("user billing payment attribution is invalid"),
			)
			return
		}
		model, err := h.billingPaymentModel(payments[index])
		if err != nil {
			h.handleBillingFailure(w, r, "map billing payment", err)
			return
		}
		models[index] = model
	}
	meta := h.meta(r)
	if hasNext {
		cursor, err := h.encodeBillingPageCursor(
			payments[len(payments)-1], scope, userID, filter,
		)
		if err != nil {
			h.handleBillingFailure(w, r, "encode billing payment cursor", err)
			return
		}
		meta.NextCursor = &cursor
	}
	writeJSON(w, http.StatusOK, gen.BillingPaymentListResponse{Data: models, Meta: meta})
}

func (h *Handler) billingPaymentModel(payment billing.Payment) (gen.BillingPayment, error) {
	identifier, err := uuid.Parse(payment.ID)
	if err != nil || identifier.String() != payment.ID || identifier.Version() != 4 ||
		payment.ChainID != h.cfg.Chain.ID || payment.CreatedAt.IsZero() ||
		payment.UpdatedAt.Before(payment.CreatedAt) {
		return gen.BillingPayment{}, errors.New("billing payment identity is invalid")
	}
	spec, ok := apiops.Lookup(payment.Operation)
	if !ok || !spec.BillingEligible || payment.Method != http.MethodGet ||
		!billingHTTPNetworkPattern.MatchString(payment.Network) ||
		!canonicalPositiveUint256(payment.AmountAtomic) {
		return gen.BillingPayment{}, errors.New("billing payment binding is invalid")
	}
	state := gen.BillingPaymentState(payment.State)
	if !state.Valid() {
		return gen.BillingPayment{}, errors.New("billing payment state is invalid")
	}
	asset := billingAddressModel(payment.Asset)
	recipient := billingAddressModel(payment.Recipient)
	model := gen.BillingPayment{
		Id: identifier, Operation: payment.Operation, State: state,
		Network: payment.Network, Asset: asset,
		AmountAtomic: gen.Quantity(payment.AmountAtomic), Recipient: recipient,
		CreatedAt: payment.CreatedAt.UTC(), UpdatedAt: payment.UpdatedAt.UTC(),
	}
	if payment.Payer != nil {
		payer := billingAddressModel(*payment.Payer)
		model.Payer = &payer
	}
	if payment.UserID != nil {
		userID, userErr := uuid.Parse(*payment.UserID)
		if userErr != nil || userID.String() != *payment.UserID ||
			userID.Version() != 4 {
			return gen.BillingPayment{}, errors.New("billing payment user is invalid")
		}
		model.UserId = &userID
	}
	if payment.APIKeyPrefix != nil {
		if !validBillingAPIKeyPrefix(*payment.APIKeyPrefix) {
			return gen.BillingPayment{}, errors.New("billing payment API key attribution is invalid")
		}
		model.ApiKeyPrefix = cloneStringPointer(*payment.APIKeyPrefix)
	}
	if payment.TransactionHash != nil {
		value := gen.Hash("0x" + hex.EncodeToString(payment.TransactionHash[:]))
		model.TransactionHash = &value
	}
	if payment.FailureCode != nil {
		if !billingHTTPCodePattern.MatchString(*payment.FailureCode) {
			return gen.BillingPayment{}, errors.New("billing payment failure code is invalid")
		}
		model.FailureCode = cloneStringPointer(*payment.FailureCode)
	}
	if payment.SettledAt != nil {
		value := payment.SettledAt.UTC()
		if value.Before(payment.CreatedAt) {
			return gen.BillingPayment{}, errors.New("billing payment settlement time is invalid")
		}
		model.SettledAt = &value
	}
	return model, nil
}

func billingSummaryRowModel(
	row billing.SummaryRow,
) (gen.BillingSummaryRow, *big.Int, *big.Int, error) {
	state := gen.BillingPaymentState(row.State)
	spec, operationOK := apiops.Lookup(row.Operation)
	if !state.Valid() || !operationOK || !spec.BillingEligible ||
		!billingHTTPNetworkPattern.MatchString(row.Network) {
		return gen.BillingSummaryRow{}, nil, nil,
			errors.New("billing summary identity is invalid")
	}
	asset := billingAddressModel(row.Asset)
	count, countOK := parseBillingAggregate(row.PaymentCount)
	amount, amountOK := parseBillingAggregate(row.AmountAtomic)
	if !countOK || count.Sign() <= 0 || !amountOK || amount.Sign() <= 0 {
		return gen.BillingSummaryRow{}, nil, nil,
			errors.New("billing summary values are invalid")
	}
	return gen.BillingSummaryRow{
		State: state, Operation: row.Operation, Network: row.Network, Asset: asset,
		PaymentCount: gen.BillingAggregateQuantity(row.PaymentCount),
		AmountAtomic: gen.BillingAggregateQuantity(row.AmountAtomic),
	}, count, amount, nil
}

func (h *Handler) parseAdminBillingFilter(
	w http.ResponseWriter,
	r *http.Request,
	values url.Values,
	summary bool,
) (billing.AdminFilter, normalizedBillingFilter, bool) {
	var (
		filter     billing.AdminFilter
		normalized normalizedBillingFilter
	)
	if value, present := singleBillingQueryValue(values, "state"); present {
		state := billing.State(value)
		switch state {
		case billing.StateReserved, billing.StateVerified, billing.StateSettling,
			billing.StateSettled, billing.StateFailed, billing.StateExpired:
		default:
			writeInvalidBillingQuery(w, r)
			return filter, normalized, false
		}
		filter.State, normalized.State = &state, value
	}
	if value, present := singleBillingQueryValue(values, "operation"); present {
		spec, ok := apiops.Lookup(value)
		if !ok || !spec.BillingEligible || len(value) > 128 {
			writeInvalidBillingQuery(w, r)
			return filter, normalized, false
		}
		filter.Operation, normalized.Operation = cloneStringPointer(value), value
	}
	if value, present := singleBillingQueryValue(values, "network"); present {
		if len(value) > 96 || !billingHTTPNetworkPattern.MatchString(value) {
			writeInvalidBillingQuery(w, r)
			return filter, normalized, false
		}
		filter.Network, normalized.Network = cloneStringPointer(value), value
	}
	if value, present := singleBillingQueryValue(values, "asset"); present {
		asset, ok := parseBillingAddress(value)
		if !ok {
			writeInvalidBillingQuery(w, r)
			return filter, normalized, false
		}
		filter.Asset = &asset
		normalized.Asset = "0x" + hex.EncodeToString(asset[:])
	}
	if value, present := singleBillingQueryValue(values, "from_time"); present {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || parsed.IsZero() {
			writeInvalidBillingQuery(w, r)
			return filter, normalized, false
		}
		parsed = parsed.UTC()
		filter.FromTime = &parsed
		normalized.FromTime = parsed.Format(time.RFC3339Nano)
	}
	if value, present := singleBillingQueryValue(values, "to_time"); present {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || parsed.IsZero() {
			writeInvalidBillingQuery(w, r)
			return filter, normalized, false
		}
		parsed = parsed.UTC()
		filter.ToTime = &parsed
		normalized.ToTime = parsed.Format(time.RFC3339Nano)
	}
	if summary {
		if filter.ToTime == nil {
			value := h.now().UTC()
			filter.ToTime = &value
			normalized.ToTime = value.Format(time.RFC3339Nano)
		}
		if filter.FromTime == nil {
			value := filter.ToTime.Add(-24 * time.Hour)
			filter.FromTime = &value
			normalized.FromTime = value.Format(time.RFC3339Nano)
		}
	}
	if filter.FromTime != nil && filter.ToTime != nil {
		interval := filter.ToTime.Sub(*filter.FromTime)
		if interval <= 0 || summary && interval > maximumBillingInterval {
			writeInvalidBillingQuery(w, r)
			return billing.AdminFilter{}, normalizedBillingFilter{}, false
		}
	}
	return filter, normalized, true
}

func parseStrictBillingQuery(
	w http.ResponseWriter,
	r *http.Request,
	allowed ...string,
) (url.Values, bool) {
	if len(r.URL.RawQuery) > maximumBillingQueryBytes {
		writeInvalidBillingQuery(w, r)
		return nil, false
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeInvalidBillingQuery(w, r)
		return nil, false
	}
	allowlist := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowlist[name] = struct{}{}
	}
	for name, items := range values {
		if _, ok := allowlist[name]; !ok || name == "" || len(items) != 1 ||
			items[0] == "" {
			writeInvalidBillingQuery(w, r)
			return nil, false
		}
	}
	return values, true
}

func parseBillingPageLimit(
	w http.ResponseWriter,
	r *http.Request,
	values url.Values,
) (int, bool) {
	raw, present := singleBillingQueryValue(values, "limit")
	if !present {
		return defaultBillingPageSize, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || strconv.Itoa(value) != raw ||
		value < 1 || value > maximumBillingPageSize {
		writeError(
			w, r, http.StatusBadRequest, "invalid_limit",
			"limit must be between 1 and 100", nil,
		)
		return 0, false
	}
	return value, true
}

func (h *Handler) decodeBillingPageCursor(
	w http.ResponseWriter,
	r *http.Request,
	values url.Values,
	scope string,
	userID string,
	filter normalizedBillingFilter,
) (*billing.PageAfter, bool) {
	raw, present := singleBillingQueryValue(values, "cursor")
	if !present {
		return nil, true
	}
	var cursor billingPageCursor
	identifier, idErr := uuid.Nil, error(nil)
	if DecodeCursor(raw, &cursor) == nil {
		identifier, idErr = uuid.Parse(cursor.ID)
	}
	if idErr != nil || identifier == uuid.Nil || identifier.String() != cursor.ID ||
		identifier.Version() != 4 || cursor.Scope != scope ||
		cursor.ChainID != strconv.FormatUint(h.cfg.Chain.ID, 10) ||
		cursor.UserID != userID || cursor.Filter != filter ||
		cursor.CreatedAt.IsZero() {
		writeError(
			w, r, http.StatusBadRequest, "invalid_cursor",
			"cursor is invalid or stale", nil,
		)
		return nil, false
	}
	return &billing.PageAfter{
		CreatedAt: cursor.CreatedAt.UTC(), ID: identifier.String(),
	}, true
}

func (h *Handler) encodeBillingPageCursor(
	payment billing.Payment,
	scope string,
	userID string,
	filter normalizedBillingFilter,
) (string, error) {
	if payment.CreatedAt.IsZero() {
		return "", errors.New("billing cursor boundary time is missing")
	}
	identifier, err := uuid.Parse(payment.ID)
	if err != nil || identifier.String() != payment.ID || identifier.Version() != 4 {
		return "", errors.New("billing cursor boundary ID is invalid")
	}
	return EncodeCursor(billingPageCursor{
		Scope: scope, ChainID: strconv.FormatUint(h.cfg.Chain.ID, 10),
		UserID: userID, Filter: filter,
		CreatedAt: payment.CreatedAt.UTC(), ID: identifier.String(),
	})
}

func singleBillingQueryValue(values url.Values, name string) (string, bool) {
	items, present := values[name]
	if !present || len(items) != 1 {
		return "", false
	}
	return items[0], true
}

func writeInvalidBillingQuery(w http.ResponseWriter, r *http.Request) {
	writeError(
		w, r, http.StatusBadRequest, "invalid_billing_query",
		"billing query is invalid", nil,
	)
}

func (h *Handler) handleBillingFailure(
	w http.ResponseWriter,
	r *http.Request,
	message string,
	err error,
) {
	errorType := "<nil>"
	if err != nil {
		errorType = fmt.Sprintf("%T", err)
	}
	h.logger.LogAttrs(
		r.Context(), slog.LevelError, message,
		slog.String("error_code", "billing_query_failed"),
		slog.String("request_id", requestIDFrom(r.Context())),
		slog.String("error_type", errorType),
	)
	writeError(
		w, r, http.StatusServiceUnavailable, "billing_unavailable",
		"billing ledger is unavailable", nil,
	)
}

func billingAddressModel(value common.Address) gen.Address {
	return gen.Address(value.Hex())
}

func parseBillingAddress(value string) (common.Address, bool) {
	var address common.Address
	if !addressPattern.MatchString(value) {
		return address, false
	}
	decoded, err := hex.DecodeString(value[2:])
	if err != nil || len(decoded) != len(address) {
		return address, false
	}
	copy(address[:], decoded)
	return address, true
}

func canonicalPositiveUint256(value string) bool {
	return len(value) <= 78 && value != "0" && canonicalQuantity(value)
}

func canonicalBillingAggregate(value string) bool {
	_, ok := parseBillingAggregate(value)
	return ok
}

func parseBillingAggregate(value string) (*big.Int, bool) {
	if value == "" || len(value) > 97 ||
		len(value) > 1 && value[0] == '0' {
		return nil, false
	}
	integer, ok := new(big.Int).SetString(value, 10)
	if !ok || integer.Sign() < 0 {
		return nil, false
	}
	return integer, true
}

func validBillingAPIKeyPrefix(value string) bool {
	if len(value) != 10 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).
		DecodeString(strings.ToUpper(value))
	return err == nil && len(decoded) == 6
}

func cloneStringPointer(value string) *string {
	copy := value
	return &copy
}

func billingAPIAddressPointer(value string) *gen.Address {
	address := gen.Address(value)
	return &address
}
