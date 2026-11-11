package httpapi

import (
	"github.com/islishude/etherview/internal/verify"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerificationCapacityErrorsIncludeRetryAfter(t *testing.T) {
	for _, tc := range []struct {
		code   verify.ServiceErrorCode
		status int
	}{{verify.ServiceUserCapacity, 429}, {verify.ServiceGlobalCapacity, 503}} {
		t.Run(string(tc.code), func(t *testing.T) {
			response := httptest.NewRecorder()
			(&Handler{}).handleVerificationError(response, httptest.NewRequest("POST", "/api/v1/verification/solidity/standard-json", nil), verify.ServiceError{Code: tc.code})
			if response.Code != tc.status || response.Header().Get("Retry-After") != "30" || !strings.Contains(response.Body.String(), string(tc.code)) {
				t.Fatalf("response=%d %v %s", response.Code, response.Header(), response.Body)
			}
		})
	}
}
