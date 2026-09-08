package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/verify"
)

func TestVyperStandaloneTransport(t *testing.T) {
	service := &verificationV2Service{job: verify.VerificationJob{ID: "123e4567-e89b-42d3-a456-426614174000", Kind: verify.JobVyperStandardJSON, Status: verify.JobQueued, CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	handler, err := New(Options{Config: config.Default(), Reader: fakeReader{}, VerificationReader: service, VerificationSubmitter: service})
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: []byte(strings.Repeat("p", 32))}
	key, err := manager.Create(context.Background(), "vyper", 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	protected := manager.Middleware(false, handler)
	valid := `{"compiler_version":"0.4.3","target_file":"A.vy","input":{"language":"Vyper","sources":{"A.vy":{"content":"@external\ndef value() -> uint256: return 42"}},"settings":{}},"bytecodes":{"runtime_bytecode":"0x6000"}}`
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"valid", valid, http.StatusAccepted},
		{"missing target", strings.Replace(valid, `"target_file":"A.vy",`, "", 1), http.StatusBadRequest},
		{"Solidity optimization", strings.Replace(valid, `"target_file":`, `"optimization_runs":200,"target_file":`, 1), http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/verifier/vyper/standard-json", strings.NewReader(test.body))
			req.Header.Set("X-API-Key", key.Token)
			response := httptest.NewRecorder()
			protected.ServeHTTP(response, req)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.status == http.StatusAccepted && (service.submission.Language != verify.LanguageVyper || service.submission.TargetFile != "A.vy" || service.submission.Kind != verify.JobVyperStandardJSON) {
				t.Fatalf("submission=%+v", service.submission)
			}
		})
	}
}
