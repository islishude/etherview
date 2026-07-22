package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/verify"
)

type fakeSourcifyAdapter struct {
	lookupContract verify.SourcifyContract
	lookupErr      error
	lookupCalls    int
	lookupChain    uint64
	lookupAddress  string

	importRequest verify.Request
	importErr     error
	importCalls   int
	importTarget  verify.VerificationTarget

	ticket        verify.SourcifyTicket
	submitErr     error
	submitCalls   int
	submitJobID   string
	submitConsent bool
	submitReader  verify.SourcifyJobReader

	job         verify.SourcifyJob
	statusErr   error
	statusCalls int
	statusID    string
}

type fakeVerificationSubmitter struct{}

func (fakeVerificationSubmitter) Submit(context.Context, verify.Request) (verify.VerificationJob, bool, error) {
	return verify.VerificationJob{}, false, nil
}

func (adapter *fakeSourcifyAdapter) Lookup(_ context.Context, chainID uint64, address string) (verify.SourcifyContract, error) {
	adapter.lookupCalls++
	adapter.lookupChain = chainID
	adapter.lookupAddress = address
	return adapter.lookupContract, adapter.lookupErr
}

func (adapter *fakeSourcifyAdapter) Import(_ context.Context, target verify.VerificationTarget) (verify.Request, error) {
	adapter.importCalls++
	adapter.importTarget = target
	request := adapter.importRequest
	request.ChainID = target.ChainID
	request.Address = target.Address
	request.CodeHash = target.CodeHash
	request.AtBlockHash = target.AtBlockHash
	request.CreationBytecode = target.CreationBytecode
	request.RuntimeBytecode = target.RuntimeBytecode
	return request, adapter.importErr
}

func (adapter *fakeSourcifyAdapter) Submit(
	_ context.Context,
	reader verify.SourcifyJobReader,
	jobID string,
	consent bool,
) (verify.SourcifyTicket, error) {
	adapter.submitCalls++
	adapter.submitReader = reader
	adapter.submitJobID = jobID
	adapter.submitConsent = consent
	return adapter.ticket, adapter.submitErr
}

func (adapter *fakeSourcifyAdapter) Status(_ context.Context, id string) (verify.SourcifyJob, error) {
	adapter.statusCalls++
	adapter.statusID = id
	return adapter.job, adapter.statusErr
}

func TestSourcifyRoutesRemainTypedWhenDisabled(t *testing.T) {
	t.Parallel()
	handler, err := New(Options{Config: config.Default(), Reader: fakeReader{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/sourcify/contracts/0x1111111111111111111111111111111111111111", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/sourcify/imports", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodPost, "/api/v1/verification/jobs/123e4567-e89b-42d3-a456-426614174000/sourcify", strings.NewReader(`{"consent":true}`)),
		httptest.NewRequest(http.MethodGet, "/api/v1/sourcify/jobs/123e4567-e89b-42d3-a456-426614174000", nil),
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"code":"sourcify_unavailable"`) ||
			!strings.Contains(recorder.Body.String(), `"reason":"feature_disabled"`) {
			t.Fatalf("%s %s: status=%d body=%s", request.Method, request.URL, recorder.Code, recorder.Body.String())
		}
	}
}

func TestSourcifyRoutesRequireNativeAPIKey(t *testing.T) {
	t.Parallel()
	adapter := &fakeSourcifyAdapter{}
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{}, Sourcify: adapter,
		VerificationReader: &verificationRepository{}, VerificationSubmitter: fakeVerificationSubmitter{},
		VerificationTargets: &verificationTargetResolver{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/sourcify/contracts/0x1111111111111111111111111111111111111111", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/sourcify/imports", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodPost, "/api/v1/verification/jobs/123e4567-e89b-42d3-a456-426614174000/sourcify", strings.NewReader(`{"consent":true}`)),
		httptest.NewRequest(http.MethodGet, "/api/v1/sourcify/jobs/123e4567-e89b-42d3-a456-426614174000", nil),
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), `"code":"api_key_required"`) {
			t.Fatalf("%s %s: status=%d body=%s", request.Method, request.URL, recorder.Code, recorder.Body.String())
		}
	}
	if adapter.lookupCalls != 0 || adapter.importCalls != 0 || adapter.submitCalls != 0 || adapter.statusCalls != 0 {
		t.Fatalf("unauthenticated request reached adapter: %+v", adapter)
	}
}

func TestSourcifyRoutesBindLocalTargetAndReturnGeneratedEnvelopes(t *testing.T) {
	t.Parallel()
	const (
		address        = "0x1111111111111111111111111111111111111111"
		verificationID = "123e4567-e89b-42d3-a456-426614174001"
		jobID          = "123e4567-e89b-42d3-a456-426614174000"
	)
	runtime := []byte{0x60, 0x00}
	target := verify.VerificationTarget{
		ChainID: 1, Address: address, CodeHash: testVerificationRuntimeHash(runtime),
		AtBlockHash:      "0x" + strings.Repeat("3", 64),
		CreationBytecode: "0x6000aabb", RuntimeBytecode: "0x6000",
	}
	now := time.Unix(100, 0).UTC()
	repository := &verificationRepository{job: verify.VerificationJob{
		ID: jobID, Status: verify.JobQueued, CreatedAt: now, UpdatedAt: now,
	}}
	service, err := verify.NewService(repository, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	exact := "exact_match"
	adapter := &fakeSourcifyAdapter{
		lookupContract: verify.SourcifyContract{
			ChainID: "1", Address: address, Match: exact, CreationMatch: exact, RuntimeMatch: exact,
			Compilation: verify.SourcifyCompilation{
				Language: "Solidity", CompilerVersion: "0.8.30", FullyQualifiedName: "A.sol:A",
			},
		},
		importRequest: verify.Request{
			Language: verify.LanguageSolidity, CompilerVersion: "0.8.30", ContractIdentifier: "A.sol:A",
			StandardJSON: json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
		},
		ticket: verify.SourcifyTicket{VerificationID: verificationID},
		job: verify.SourcifyJob{
			IsJobCompleted: true, VerificationID: verificationID,
			Contract: &verify.SourcifyJobContract{
				ChainID: "1", Address: address, Match: &exact, CreationMatch: &exact, RuntimeMatch: &exact,
			},
		},
	}
	resolver := &verificationTargetResolver{target: target}
	cfg := config.Default()
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{}, Sourcify: adapter,
		VerificationReader: service, VerificationSubmitter: service, VerificationTargets: resolver,
		RequestID: func() string { return "sourcify-request" },
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: []byte(strings.Repeat("p", 32))}
	issued, err := manager.Create(context.Background(), "sourcify", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	protected := manager.Middleware(false, handler)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("X-API-Key", issued.Token)
		recorder := httptest.NewRecorder()
		protected.ServeHTTP(recorder, request)
		return recorder
	}

	recorder := do(http.MethodGet, "/api/v1/sourcify/contracts/"+address, "")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"chain_id":"1"`) ||
		!strings.Contains(recorder.Body.String(), `"compiler_version":"0.8.30"`) {
		t.Fatalf("lookup status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if adapter.lookupChain != 1 || adapter.lookupAddress != address {
		t.Fatalf("lookup identity=%d/%s", adapter.lookupChain, adapter.lookupAddress)
	}

	recorder = do(http.MethodPost, "/api/v1/sourcify/imports", `{"address":"`+address+`","constructor_arguments":"0xAABB"}`)
	if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), jobID) {
		t.Fatalf("import status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if adapter.importTarget.CodeHash != target.CodeHash || adapter.importTarget.AtBlockHash != target.AtBlockHash ||
		adapter.importTarget.CreationBytecode != "0x6000" || adapter.importTarget.RuntimeBytecode != target.RuntimeBytecode ||
		repository.request.ConstructorArgs != "aabb" || repository.request.SubmitToSourcify {
		t.Fatalf("import target=%+v request=%+v", adapter.importTarget, repository.request)
	}

	recorder = do(http.MethodPost, "/api/v1/verification/jobs/"+jobID+"/sourcify", `{"consent":true}`)
	if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), verificationID) {
		t.Fatalf("upload status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if adapter.submitReader == nil || adapter.submitJobID != jobID || !adapter.submitConsent {
		t.Fatalf("upload binding reader=%T id=%s consent=%v", adapter.submitReader, adapter.submitJobID, adapter.submitConsent)
	}

	recorder = do(http.MethodGet, "/api/v1/sourcify/jobs/"+verificationID, "")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"state":"succeeded"`) ||
		!strings.Contains(recorder.Body.String(), `"runtime_match":"exact_match"`) {
		t.Fatalf("status status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if adapter.statusID != verificationID {
		t.Fatalf("status ID=%s", adapter.statusID)
	}
}

func TestSourcifyErrorsUseStableRedactedBoundaryCodes(t *testing.T) {
	t.Parallel()
	address := "0x1111111111111111111111111111111111111111"
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "not found", err: verify.ErrSourcifyNotFound, status: http.StatusNotFound, code: "sourcify_not_found"},
		{name: "unavailable", err: verify.ErrSourcifyUnavailable, status: http.StatusServiceUnavailable, code: "sourcify_unavailable"},
		{name: "invalid response", err: verify.ErrSourcifyInvalidResponse, status: http.StatusBadGateway, code: "sourcify_invalid_response"},
		{name: "target mismatch", err: verify.ErrSourcifyTargetMismatch, status: http.StatusConflict, code: "sourcify_target_mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			secret := "nested upstream secret"
			adapter := &fakeSourcifyAdapter{lookupErr: errors.Join(test.err, errors.New(secret))}
			handler, err := New(Options{Config: config.Default(), Reader: fakeReader{}, Sourcify: adapter})
			if err != nil {
				t.Fatal(err)
			}
			manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: []byte(strings.Repeat("p", 32))}
			issued, err := manager.Create(context.Background(), "sourcify", 100, 100)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/sourcify/contracts/"+address, nil)
			request.Header.Set("X-API-Key", issued.Token)
			recorder := httptest.NewRecorder()
			manager.Middleware(false, handler).ServeHTTP(recorder, request)
			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), `"code":"`+test.code+`"`) ||
				strings.Contains(recorder.Body.String(), secret) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestSourcifyRejectsMalformedJobIDsBeforeAdapter(t *testing.T) {
	t.Parallel()
	adapter := &fakeSourcifyAdapter{}
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{}, Sourcify: adapter,
		VerificationReader: &verificationRepository{},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: []byte(strings.Repeat("p", 32))}
	issued, err := manager.Create(context.Background(), "sourcify", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/v1/verification/jobs/not-a-uuid/sourcify", strings.NewReader(`{"consent":true}`)),
		httptest.NewRequest(http.MethodGet, "/api/v1/sourcify/jobs/not-a-uuid", nil),
	} {
		request.Header.Set("X-API-Key", issued.Token)
		recorder := httptest.NewRecorder()
		manager.Middleware(false, handler).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_sourcify_request"`) {
			t.Fatalf("%s %s: status=%d body=%s", request.Method, request.URL, recorder.Code, recorder.Body.String())
		}
	}
	if adapter.submitCalls != 0 || adapter.statusCalls != 0 {
		t.Fatalf("malformed ID reached adapter: submit=%d status=%d", adapter.submitCalls, adapter.statusCalls)
	}
}
