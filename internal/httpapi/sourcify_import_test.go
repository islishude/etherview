package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/verify"
)

type sourcifyImporterStub struct {
	calls       int
	target      verify.VerificationTarget
	sourceChain uint64
	err         error
}

func (s *sourcifyImporterStub) ImportV2(_ context.Context, sourceChain uint64, target verify.VerificationTarget) (verify.SubmissionV2, error) {
	s.calls++
	s.target = target
	s.sourceChain = sourceChain
	return verify.SubmissionV2{Kind: verify.JobAddress, Target: &target, Language: verify.LanguageSolidity, CompilerVersion: "0.8.30", StandardJSON: []byte(`{"sources":{}}`)}, s.err
}

func TestVerifierV2SourcifyImportAuthorizationAndErrors(t *testing.T) {
	const address = "0x1111111111111111111111111111111111111111"
	for _, tc := range []struct {
		name, body          string
		disabled, anonymous bool
		upstream            error
		status              int
	}{
		{name: "accepted", body: `{"chain_id":"9007199254740993"}`, status: 202},
		{name: "unauthorized", body: `{"chain_id":"9007199254740993"}`, anonymous: true, status: 401},
		{name: "disabled", body: `{"chain_id":"9007199254740993"}`, disabled: true, status: 503},
		{name: "missing chain", body: `{}`, status: 400},
		{name: "number chain", body: `{"chain_id":2}`, status: 400},
		{name: "zero chain", body: `{"chain_id":"0"}`, status: 400},
		{name: "leading zeros", body: `{"chain_id":"01"}`, status: 400},
		{name: "overflow chain", body: `{"chain_id":"18446744073709551616"}`, status: 400},
		{name: "negative chain", body: `{"chain_id":"-1"}`, status: 400},
		{name: "plus chain", body: `{"chain_id":"+1"}`, status: 400},
		{name: "caller source", body: `{"input":{}}`, status: 400},
		{name: "null body", body: `null`, status: 400},
		{name: "not found", body: `{"chain_id":"9007199254740993"}`, upstream: verify.ErrSourcifyNotFound, status: 404},
		{name: "mismatch", body: `{"chain_id":"9007199254740993"}`, upstream: verify.ErrSourcifyTargetMismatch, status: 409},
		{name: "invalid source", body: `{"chain_id":"9007199254740993"}`, upstream: verify.ErrSourcifyInvalidResponse, status: 502},
		{name: "upstream secret", body: `{"chain_id":"9007199254740993"}`, upstream: errors.New("https://secret.example/private-key"), status: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Features.Sourcify = !tc.disabled
			cfg.Chain.ID = 1
			importer := &sourcifyImporterStub{err: tc.upstream}
			service := &verificationV2Service{job: verify.VerificationJob{ID: "123e4567-e89b-42d3-a456-426614174000", Kind: verify.JobAddress, Status: verify.JobQueued}}
			target := verify.VerificationTarget{ChainID: 1, Address: address, AtBlockHash: "0x" + strings.Repeat("33", 32), RuntimeBytecode: "0x6001", CreationBytecode: "0x6002"}
			handler, err := New(Options{Config: cfg, Reader: fakeReader{}, VerificationSubmitter: service, VerificationTargets: verificationTargetResolver{target: target}, SourcifyImporter: importer})
			if err != nil {
				t.Fatal(err)
			}
			manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: []byte(strings.Repeat("p", 32))}
			key, err := manager.Create(t.Context(), "test", 10, 10)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/contracts/"+address+"/verification/sourcify", strings.NewReader(tc.body))
			if !tc.anonymous {
				request.Header.Set("X-API-Key", key.Token)
			}
			response := httptest.NewRecorder()
			manager.Middleware(false, handler).ServeHTTP(response, request)
			if response.Code != tc.status || strings.Contains(response.Body.String(), "secret.example") {
				t.Fatalf("status=%d expected=%d", response.Code, tc.status)
			}
			if tc.status == 202 {
				if importer.calls != 1 || importer.sourceChain != 9007199254740993 || importer.target != target || service.submission.Kind != verify.JobAddress || *service.submission.Target != target {
					t.Fatal("local job was not bound to server target")
				}
			} else if service.submission.Kind != "" {
				t.Fatal("failed import enqueued a job")
			}
			if (tc.anonymous || tc.disabled || tc.status == 400) && importer.calls != 0 {
				t.Fatal("rejected request contacted Sourcify")
			}
		})
	}
}
