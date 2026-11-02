package x402wire

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPaymentProgressReadOnlyAndBounded(t *testing.T) {
	for _, body := range []string{
		`{"stage":"submitting"}`,
		`{"stage":"confirming","transaction":"0x` + strings.Repeat("1", 64) + `"}`,
		`{"stage":"unknown","transaction":"0x` + strings.Repeat("1", 64) + `"}`,
		`{"stage":"confirming"}`,
		`{"stage":"unknown","transaction":"0x` + strings.Repeat("0", 64) + `"}`,
		`{"stage":"credited"}`, `{"stage":"submitting","transaction":"secret"}`,
	} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/progress" || r.Method != http.MethodPost {
					t.Errorf("mutating request: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			client := testClientForServer(t, server, 4096, nil)
			payment, requirement := testPaymentAndRequirement(t)
			result, err := client.PaymentProgress(t.Context(), payment, requirement)
			valid := body == `{"stage":"submitting"}` || strings.Contains(body, strings.Repeat("1", 64))
			if (err == nil) != valid || (valid && result == nil) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if calls != 1 {
				t.Fatalf("retried progress: %d", calls)
			}
		})
	}
}
