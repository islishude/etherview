package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/billing"
)

func TestLookupMethodsRejectsExcessiveABIDimensions(t *testing.T) {
	h := testHandler(t, fakeReader{})
	body := fmt.Sprintf(`{"bytecode":"0x00","abi":[{"type":"function","name":"f","inputs":[{"name":"a","type":"uint256%s"}],"outputs":[]}],"source_map":"0:1:0","file_ids":{"0":"a.sol"}}`, strings.Repeat("[]", 8000))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/verifier/lookup-methods", strings.NewReader(body)))
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"invalid_method_lookup"`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestDisabledTopupsReturnOneTypedError(t *testing.T) {
	for _, billingEnabled := range []bool{true, false} {
		h := testHandler(t, fakeReader{}).(*Handler)
		h.cfg.Features.APIBilling = billingEnabled
		h.cfg.Features.X402Topups = false
		h.prepaidBilling = &billing.PrepaidLedger{}
		code := "billing_unavailable"
		if billingEnabled {
			code = "topup_payment_unavailable"
		}
		for _, path := range []string{"/api/v1/billing/topup-intents", "/api/v1/billing/topup-intents/00000000-0000-4000-8000-000000000000/pay"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(`{}`)))
			if w.Code != 503 || !json.Valid(w.Body.Bytes()) || !strings.Contains(w.Body.String(), `"`+code+`"`) {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
			}
		}
	}
}
