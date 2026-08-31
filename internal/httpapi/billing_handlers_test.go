package httpapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/billing"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/userauth"
)

type fakeBillingReader struct {
	userPayments  []billing.Payment
	adminPayments []billing.Payment
	summaryRows   []billing.SummaryRow
	err           error

	userCalls    int
	adminCalls   int
	summaryCalls int
	userID       string
	after        *billing.PageAfter
	limit        int
	filter       billing.AdminFilter
}

func (fake *fakeBillingReader) ListUser(
	_ context.Context,
	userID string,
	after *billing.PageAfter,
	limit int,
) ([]billing.Payment, error) {
	fake.userCalls++
	fake.userID, fake.after, fake.limit = userID, after, limit
	return fake.userPayments, fake.err
}

func (fake *fakeBillingReader) ListAdmin(
	_ context.Context,
	filter billing.AdminFilter,
	after *billing.PageAfter,
	limit int,
) ([]billing.Payment, error) {
	fake.adminCalls++
	fake.filter, fake.after, fake.limit = filter, after, limit
	return fake.adminPayments, fake.err
}

func (fake *fakeBillingReader) Summary(
	_ context.Context,
	filter billing.AdminFilter,
) ([]billing.SummaryRow, error) {
	fake.summaryCalls++
	fake.filter = filter
	return fake.summaryRows, fake.err
}

func TestBillingConfigIsFreeSortedAndSecretFree(t *testing.T) {
	t.Parallel()

	disabled, err := New(Options{
		Config: config.Default(), Reader: fakeReader{},
		RequestID: func() string { return "billing-config-disabled" },
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	disabled.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/billing/config", nil),
	)
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"api_billing_enabled":false`) ||
		!strings.Contains(recorder.Body.String(), `"operations":[]`) {
		t.Fatalf("disabled config status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	cfg := config.Default()
	cfg.Features.APIBilling = true
	cfg.Features.X402Topups = true
	cfg.Billing.AssetTransferMethods = []string{"eip3009", "permit2"}
	cfg.Billing.MinimumTopupAmountAtomic = "1"
	cfg.Billing.MaximumTopupAmountAtomic = "1000000"
	cfg.Billing.Asset = "0x52908400098527886e0f7030069857d2e4169ee7"
	cfg.Billing.Recipient = "0xde709f2102306220921060314715629080e2fb77"
	cfg.Billing.FacilitatorURL = "https://facilitator-secret.example"
	cfg.Billing.FacilitatorAllowedCIDRs = []string{"203.0.113.0/24"}
	cfg.Billing.FacilitatorHeaders = map[string]string{
		"Authorization": "Bearer facilitator-secret",
	}
	cfg.Billing.Operations = map[string]config.BillingOperationConfig{
		"etherscan.account.balance":       {AmountAtomic: "10"},
		"etherscan.transaction.getstatus": {AmountAtomic: "20"},
	}
	handler, err := New(Options{Config: cfg, Reader: fakeReader{}})
	if err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/billing/config", nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("enabled config status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response gen.BillingConfigResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Data.ApiBillingEnabled || !response.Data.X402TopupsEnabled ||
		response.Data.X402Version != 2 ||
		response.Data.Scheme != gen.BillingConfigSchemeExact ||
		response.Data.Asset == nil ||
		*response.Data.Asset != "0x52908400098527886E0F7030069857D2E4169EE7" ||
		response.Data.Recipient == nil ||
		*response.Data.Recipient != "0xde709f2102306220921060314715629080e2fb77" ||
		len(response.Data.Operations) != 2 ||
		response.Data.Operations[0].Operation != "etherscan.account.balance" ||
		response.Data.Operations[1].Operation != "etherscan.transaction.getstatus" {
		t.Fatalf("unexpected enabled billing config: %#v", response.Data)
	}
	body := recorder.Body.String()
	for _, forbidden := range []string{
		"facilitator-secret.example", "facilitator-secret", "203.0.113.0/24",
		"fingerprint", "pepper",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("billing config leaked %q: %s", forbidden, body)
		}
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/billing/config?unknown=1", nil),
	)
	assertAuthError(t, recorder, http.StatusBadRequest, "invalid_billing_query")
}

func TestBillingHistoryIsUnavailableBeforeParsingWhenUserAuthIsOff(t *testing.T) {
	t.Parallel()
	reader := &fakeBillingReader{}
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{}, BillingReader: reader,
		RequestID: func() string { return "billing-off" },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/v1/billing/payments?unknown=1&limit=bad",
		"/api/v1/admin/billing/payments?state=not-a-state",
		"/api/v1/admin/billing/summary?from_time=bad",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-API-Key", "evk_aaaaaaaaaa_"+strings.Repeat("A", 43))
		handler.ServeHTTP(recorder, request)
		assertAuthError(
			t, recorder, http.StatusServiceUnavailable, "user_auth_unavailable",
		)
	}
	if reader.userCalls != 0 || reader.adminCalls != 0 || reader.summaryCalls != 0 {
		t.Fatalf("feature-off billing reader calls: %#v", reader)
	}
}

func TestPaymentSignatureIsRoutedOnlyToTopupPayEndpoint(t *testing.T) {
	t.Parallel()
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{},
		RequestID: func() string { return "topup-payment-boundary" },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path   string
		code   string
		status int
	}{
		{
			path: "/api/v1/billing/topup-intents/74e5f6a2-30ef-4d7f-93b8-e430f1fdfac4/pay",
			code: "billing_unavailable", status: http.StatusServiceUnavailable,
		},
		{
			path: "/api/v1/billing/account",
			code: "unexpected_payment_header", status: http.StatusBadRequest,
		},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, test.path, nil)
		request.Header.Set("PAYMENT-SIGNATURE", "opaque")
		handler.ServeHTTP(recorder, request)
		assertAuthError(t, recorder, test.status, test.code)
	}
}

func TestEnabledUserAuthRequiresWriterBillingReader(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Features.UserAuth = true
	cfg.Server.PublicURL = testAuthOrigin
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{},
		UserAuth:           &fakeUserAuthenticator{},
		UserAdministration: &fakeUserAdministration{},
	})
	if err == nil || handler != nil ||
		!strings.Contains(err.Error(), "writer-backed billing reader") {
		t.Fatalf("handler=%v error=%v", handler, err)
	}
}

func TestCurrentUserBillingHistoryAuthenticatesAndBindsCursor(t *testing.T) {
	t.Parallel()
	now := billingHandlerNow()
	first := billingTestPayment(now)
	second := first
	second.ID = "00000000-0000-4000-8000-000000000002"
	second.CreatedAt = first.CreatedAt.Add(-time.Minute)
	second.UpdatedAt = second.CreatedAt
	second.SettledAt = new(second.CreatedAt)
	reader := &fakeBillingReader{userPayments: []billing.Payment{first, second}}
	handler, authenticator := enabledBillingHistoryHandler(
		t, userauth.RoleUser, reader,
	)

	recorder := serveBillingGET(
		handler, "/api/v1/billing/payments?limit=1",
	)
	if recorder.Code != http.StatusOK || reader.userCalls != 1 ||
		reader.userID != testUserID || reader.limit != 2 {
		t.Fatalf(
			"status=%d calls=%d user=%q limit=%d body=%s",
			recorder.Code, reader.userCalls, reader.userID, reader.limit,
			recorder.Body.String(),
		)
	}
	var page gen.BillingPaymentListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Meta.NextCursor == nil ||
		page.Data[0].Asset != "0x1111111111111111111111111111111111111111" ||
		page.Data[0].TransactionHash == nil ||
		*page.Data[0].TransactionHash !=
			"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected payment page: %#v", page)
	}
	for _, forbidden := range []string{
		"fingerprint", "resource_digest", "requirement_digest",
		"facilitator", "reservation_owner",
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("payment response leaked %q: %s", forbidden, recorder.Body.String())
		}
	}

	reader.userPayments = nil
	recorder = serveBillingGET(
		handler,
		"/api/v1/billing/payments?limit=1&cursor="+
			url.QueryEscape(*page.Meta.NextCursor),
	)
	if recorder.Code != http.StatusOK || reader.after == nil ||
		reader.after.ID != first.ID ||
		!reader.after.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf(
			"cursor status=%d after=%+v body=%s",
			recorder.Code, reader.after, recorder.Body.String(),
		)
	}

	other := authenticator.authentication.Session.User
	other.ID = "91dd854b-455e-4aba-94c1-b034166a3f8d"
	authenticator.authentication.Session.User = other
	calls := reader.userCalls
	recorder = serveBillingGET(
		handler,
		"/api/v1/billing/payments?limit=1&cursor="+
			url.QueryEscape(*page.Meta.NextCursor),
	)
	assertAuthError(t, recorder, http.StatusBadRequest, "invalid_cursor")
	if reader.userCalls != calls {
		t.Fatalf("cross-user cursor reached reader: %d -> %d", calls, reader.userCalls)
	}
}

func TestCurrentUserBillingHistoryRejectsReaderAttributionDrift(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		userID *string
	}{
		{name: "missing attribution"},
		{
			name: "different user",
			userID: func() *string {
				value := "91dd854b-455e-4aba-94c1-b034166a3f8d"
				return &value
			}(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payment := billingTestPayment(billingHandlerNow())
			payment.UserID = test.userID
			reader := &fakeBillingReader{
				userPayments: []billing.Payment{payment},
			}
			handler, _ := enabledBillingHistoryHandler(
				t, userauth.RoleUser, reader,
			)
			recorder := serveBillingGET(
				handler, "/api/v1/billing/payments",
			)
			assertAuthError(
				t, recorder, http.StatusServiceUnavailable,
				"billing_unavailable",
			)
			if strings.Contains(recorder.Body.String(), payment.ID) {
				t.Fatalf(
					"mismatched payment leaked: %s",
					recorder.Body.String(),
				)
			}
		})
	}
}

func TestBillingHistoryCannotUseAPIKeyAsUserAuthentication(t *testing.T) {
	t.Parallel()
	reader := &fakeBillingReader{}
	handler, _ := enabledBillingHistoryHandler(t, userauth.RoleAdmin, reader)
	for _, path := range []string{
		"/api/v1/billing/payments",
		"/api/v1/admin/billing/payments",
		"/api/v1/admin/billing/summary",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-API-Key", "valid-looking-but-not-a-session")
		handler.ServeHTTP(recorder, request)
		assertAuthError(
			t, recorder, http.StatusUnauthorized, "authentication_required",
		)
	}
	if reader.userCalls != 0 || reader.adminCalls != 0 || reader.summaryCalls != 0 {
		t.Fatalf("API key reached user billing reader: %#v", reader)
	}
}

func TestAdminBillingRoutesRequireAdministratorCookie(t *testing.T) {
	t.Parallel()
	reader := &fakeBillingReader{}
	handler, _ := enabledBillingHistoryHandler(t, userauth.RoleUser, reader)
	for _, path := range []string{
		"/api/v1/admin/billing/payments",
		"/api/v1/admin/billing/summary",
	} {
		recorder := serveBillingGET(handler, path)
		assertAuthError(t, recorder, http.StatusForbidden, "admin_required")
	}
	if reader.adminCalls != 0 || reader.summaryCalls != 0 {
		t.Fatalf("non-admin reached billing reader: %#v", reader)
	}
}

func TestAdminBillingHistoryIncludesAccountlessPayments(t *testing.T) {
	t.Parallel()
	payment := billingTestPayment(billingHandlerNow())
	payment.UserID = nil
	reader := &fakeBillingReader{
		adminPayments: []billing.Payment{payment},
	}
	handler, _ := enabledBillingHistoryHandler(
		t, userauth.RoleAdmin, reader,
	)
	recorder := serveBillingGET(
		handler, "/api/v1/admin/billing/payments",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var page gen.BillingPaymentListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].Id.String() != payment.ID ||
		page.Data[0].UserId != nil {
		t.Fatalf("accountless administrator page=%+v", page)
	}
}

func TestAdminBillingFiltersAndCursorAreStrictlyBound(t *testing.T) {
	t.Parallel()
	now := billingHandlerNow()
	first := billingTestPayment(now)
	second := first
	second.ID = "00000000-0000-4000-8000-000000000002"
	reader := &fakeBillingReader{adminPayments: []billing.Payment{first, second}}
	handler, _ := enabledBillingHistoryHandler(t, userauth.RoleAdmin, reader)
	from := now.Add(-time.Hour).Format(time.RFC3339Nano)
	to := now.Format(time.RFC3339Nano)
	query := url.Values{
		"state": {"settled"}, "operation": {"listBlocks"},
		"network":   {"eip155:84532"},
		"asset":     {"0x1111111111111111111111111111111111111111"},
		"from_time": {from}, "to_time": {to}, "limit": {"1"},
	}
	recorder := serveBillingGET(
		handler, "/api/v1/admin/billing/payments?"+query.Encode(),
	)
	if recorder.Code != http.StatusOK || reader.adminCalls != 1 ||
		reader.limit != 2 || reader.filter.State == nil ||
		*reader.filter.State != billing.StateSettled ||
		reader.filter.Operation == nil ||
		*reader.filter.Operation != "listBlocks" ||
		reader.filter.Network == nil ||
		*reader.filter.Network != "eip155:84532" ||
		reader.filter.Asset == nil ||
		hex.EncodeToString(reader.filter.Asset[:]) != strings.Repeat("11", 20) ||
		reader.filter.FromTime == nil || reader.filter.ToTime == nil {
		t.Fatalf(
			"status=%d calls=%d limit=%d filter=%+v body=%s",
			recorder.Code, reader.adminCalls, reader.limit, reader.filter,
			recorder.Body.String(),
		)
	}
	var page gen.BillingPaymentListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Meta.NextCursor == nil {
		t.Fatalf("missing next cursor: %#v", page)
	}

	reader.adminPayments = nil
	query.Set("cursor", *page.Meta.NextCursor)
	recorder = serveBillingGET(
		handler, "/api/v1/admin/billing/payments?"+query.Encode(),
	)
	if recorder.Code != http.StatusOK || reader.after == nil ||
		reader.after.ID != first.ID {
		t.Fatalf(
			"cursor status=%d after=%+v body=%s",
			recorder.Code, reader.after, recorder.Body.String(),
		)
	}

	calls := reader.adminCalls
	query.Set("state", "failed")
	recorder = serveBillingGET(
		handler, "/api/v1/admin/billing/payments?"+query.Encode(),
	)
	assertAuthError(t, recorder, http.StatusBadRequest, "invalid_cursor")
	if reader.adminCalls != calls {
		t.Fatalf("changed-filter cursor reached reader: %d -> %d", calls, reader.adminCalls)
	}
}

func TestBillingQueriesRejectUnknownRepeatedAndMalformedValues(t *testing.T) {
	t.Parallel()
	reader := &fakeBillingReader{}
	handler, _ := enabledBillingHistoryHandler(t, userauth.RoleAdmin, reader)
	for _, path := range []string{
		"/api/v1/billing/payments?unknown=1",
		"/api/v1/billing/payments?limit=1&limit=2",
		"/api/v1/billing/payments?limit=01",
		"/api/v1/billing/payments?cursor=not-a-cursor",
		"/api/v1/admin/billing/payments?state=unknown",
		"/api/v1/admin/billing/payments?operation=getStatus",
		"/api/v1/admin/billing/payments?network=eip155%3A0",
		"/api/v1/admin/billing/payments?asset=0x12",
		"/api/v1/admin/billing/payments?from_time=2026-07-26T12%3A00%3A00Z&to_time=2026-07-25T12%3A00%3A00Z",
		"/api/v1/admin/billing/summary?cursor=unexpected",
	} {
		recorder := serveBillingGET(handler, path)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf(
				"path=%s status=%d body=%s",
				path, recorder.Code, recorder.Body.String(),
			)
		}
	}
	request := httptest.NewRequest(
		http.MethodGet, "/api/v1/admin/billing/payments", nil,
	)
	request.URL.RawQuery = "state=%zz"
	request.AddCookie(&http.Cookie{
		Name: userauth.SessionCookieName, Value: "session-token",
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	assertAuthError(t, recorder, http.StatusBadRequest, "invalid_billing_query")
	if reader.userCalls != 0 || reader.adminCalls != 0 || reader.summaryCalls != 0 {
		t.Fatalf("malformed query reached billing reader: %#v", reader)
	}
}

func TestAdminBillingSummaryUsesBoundedDefaultsAndBigIntegerTotals(t *testing.T) {
	t.Parallel()
	now := billingHandlerNow()
	amount := strings.Repeat("9", 97)
	reader := &fakeBillingReader{summaryRows: []billing.SummaryRow{{
		State: billing.StateSettled, Operation: "listBlocks",
		Network: "eip155:84532", Asset: repeatedBillingAddress(0x11),
		PaymentCount: "2", AmountAtomic: amount,
	}}}
	handler, _ := enabledBillingHistoryHandler(t, userauth.RoleAdmin, reader)

	recorder := serveBillingGET(
		handler, "/api/v1/admin/billing/summary",
	)
	if recorder.Code != http.StatusOK || reader.summaryCalls != 1 ||
		reader.filter.FromTime == nil || reader.filter.ToTime == nil ||
		!reader.filter.FromTime.Equal(now.Add(-24*time.Hour)) ||
		!reader.filter.ToTime.Equal(now) {
		t.Fatalf(
			"status=%d calls=%d filter=%+v body=%s",
			recorder.Code, reader.summaryCalls, reader.filter,
			recorder.Body.String(),
		)
	}
	var response gen.BillingSummaryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.PaymentCount != "2" ||
		response.Data.AmountAtomic != amount ||
		len(response.Data.Rows) != 1 ||
		response.Data.Rows[0].AmountAtomic != amount {
		t.Fatalf("unexpected billing summary: %#v", response.Data)
	}

	reader.summaryRows = nil
	recorder = serveBillingGET(
		handler, "/api/v1/admin/billing/summary",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("empty summary status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.PaymentCount != "0" ||
		response.Data.AmountAtomic != "0" ||
		response.Data.Rows == nil || len(response.Data.Rows) != 0 {
		t.Fatalf("unexpected empty billing summary: %#v", response.Data)
	}

	reader.summaryRows = []billing.SummaryRow{
		{
			State: billing.StateSettled, Operation: "listBlocks",
			Network: "eip155:84532", Asset: repeatedBillingAddress(0x11),
			PaymentCount: "1", AmountAtomic: amount,
		},
		{
			State: billing.StateFailed, Operation: "listBlocks",
			Network: "eip155:84532", Asset: repeatedBillingAddress(0x11),
			PaymentCount: "1", AmountAtomic: amount,
		},
	}
	recorder = serveBillingGET(
		handler, "/api/v1/admin/billing/summary",
	)
	assertAuthError(t, recorder, http.StatusServiceUnavailable, "billing_unavailable")

	reader.summaryRows = []billing.SummaryRow{{
		State: billing.StateSettled, Operation: "listBlocks",
		Network: "eip155:84532", Asset: repeatedBillingAddress(0x11),
		PaymentCount: "01", AmountAtomic: "1",
	}}
	recorder = serveBillingGET(
		handler, "/api/v1/admin/billing/summary",
	)
	assertAuthError(t, recorder, http.StatusServiceUnavailable, "billing_unavailable")

	reader.summaryRows = []billing.SummaryRow{{
		State: billing.StateSettled, Operation: "listBlocks",
		Network: "eip155:84532", Asset: repeatedBillingAddress(0x11),
		PaymentCount: "2", AmountAtomic: amount,
	}}
	exactFrom := now.Add(-maximumBillingInterval).Format(time.RFC3339Nano)
	to := now.Format(time.RFC3339Nano)
	recorder = serveBillingGET(
		handler,
		"/api/v1/admin/billing/summary?from_time="+
			url.QueryEscape(exactFrom)+"&to_time="+url.QueryEscape(to),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("exact 31-day status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	tooOld := now.Add(-maximumBillingInterval - time.Nanosecond).
		Format(time.RFC3339Nano)
	calls := reader.summaryCalls
	recorder = serveBillingGET(
		handler,
		"/api/v1/admin/billing/summary?from_time="+
			url.QueryEscape(tooOld)+"&to_time="+url.QueryEscape(to),
	)
	assertAuthError(t, recorder, http.StatusBadRequest, "invalid_billing_query")
	if reader.summaryCalls != calls {
		t.Fatalf("oversized summary reached reader: %d -> %d", calls, reader.summaryCalls)
	}
}

func TestBillingReaderFailuresUseStableRedactedUnavailableError(t *testing.T) {
	t.Parallel()
	reader := &fakeBillingReader{err: errors.New(
		"postgres://user:secret@database.invalid/etherview",
	)}
	handler, _ := enabledBillingHistoryHandler(t, userauth.RoleUser, reader)
	recorder := serveBillingGET(
		handler, "/api/v1/billing/payments",
	)
	assertAuthError(t, recorder, http.StatusServiceUnavailable, "billing_unavailable")
	if strings.Contains(recorder.Body.String(), "secret") ||
		strings.Contains(recorder.Body.String(), "database.invalid") {
		t.Fatalf("billing reader error leaked: %s", recorder.Body.String())
	}
}

func enabledBillingHistoryHandler(
	t *testing.T,
	role userauth.Role,
	reader BillingReader,
) (*Handler, *fakeUserAuthenticator) {
	t.Helper()
	now := billingHandlerNow()
	user := authTestUser(now, role)
	authenticator := &fakeUserAuthenticator{authentication: SessionAuthentication{
		Session: userauth.Session{
			ID: testChallengeID, User: user,
			CreatedAt: now.Add(-time.Hour), LastUsedAt: now,
			ExpiresAt: now.Add(time.Hour),
		},
	}}
	cfg := config.Default()
	cfg.Chain.ID = 11155111
	cfg.Server.PublicURL = testAuthOrigin
	cfg.Features.UserAuth = true
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{},
		UserAuth:           authenticator,
		UserAdministration: &fakeUserAdministration{},
		BillingReader:      reader,
		RequestID:          func() string { return "billing-request" },
		Now:                billingHandlerNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, authenticator
}

func serveBillingGET(handler http.Handler, target string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.AddCookie(&http.Cookie{
		Name: userauth.SessionCookieName, Value: "session-token",
	})
	handler.ServeHTTP(recorder, request)
	return recorder
}

func billingHandlerNow() time.Time {
	return time.Date(2026, 7, 26, 12, 0, 0, 123456000, time.UTC)
}

func billingTestPayment(now time.Time) billing.Payment {
	userID := testUserID
	apiKeyPrefix := "aaaaaaaaaa"
	transaction := common.Hash{}
	for index := range transaction {
		transaction[index] = 0xaa
	}
	createdAt := now.Add(-time.Hour)
	return billing.Payment{
		ID:      "00000000-0000-4000-8000-000000000001",
		ChainID: 11155111, Operation: "listBlocks", Method: http.MethodGet,
		Purpose: "legacy_request", AssetTransferMethod: "eip3009",
		PaymentFlow: "authorization", FingerprintVersion: 1,
		Network: "eip155:84532", Asset: repeatedBillingAddress(0x11),
		AmountAtomic: "115792089237316195423570985008687907853269984665640564039457584007913129639935",
		Recipient:    repeatedBillingAddress(0x22),
		Payer: func() *common.Address {
			value := repeatedBillingAddress(0x33)
			return &value
		}(),
		UserID: &userID, APIKeyPrefix: &apiKeyPrefix,
		TransactionHash: &transaction, State: billing.StateSettled,
		SettledAt: new(createdAt.Add(time.Minute)),
		CreatedAt: createdAt, UpdatedAt: createdAt.Add(time.Minute),
	}
}

func repeatedBillingAddress(value byte) common.Address {
	var address common.Address
	for index := range address {
		address[index] = value
	}
	return address
}
