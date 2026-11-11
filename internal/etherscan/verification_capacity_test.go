package etherscan

import (
	"encoding/json"
	"fmt"
	"github.com/islishude/etherview/internal/verify"
	"net/http/httptest"
	"testing"
)

func TestVerificationCapacityKeepsCompatibilityEnvelope(t *testing.T) {
	for _, tc := range []struct {
		code   verify.ServiceErrorCode
		status int
	}{{verify.ServiceUserCapacity, 429}, {verify.ServiceGlobalCapacity, 503}} {
		t.Run(string(tc.code), func(t *testing.T) {
			response := httptest.NewRecorder()
			Handler{}.writeBackendError(response, fmt.Errorf("verification service: %w", verify.ServiceError{Code: tc.code}))
			var envelope struct{ Status, Message, Result string }
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if response.Code != tc.status || response.Header().Get("Retry-After") != "30" || envelope.Status != "0" || envelope.Message != "NOTOK" || envelope.Result != string(tc.code) {
				t.Fatalf("response=%d %v %+v", response.Code, response.Header(), envelope)
			}
		})
	}
}
