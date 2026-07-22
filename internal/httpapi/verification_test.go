package httpapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/verify"
	"golang.org/x/crypto/sha3"
)

type verificationRepository struct {
	request     verify.Request
	submission  verify.SubmissionOptions
	job         verify.VerificationJob
	contract    verify.VerifiedContract
	submitCalls int
}

type verificationTargetResolver struct {
	target  verify.VerificationTarget
	err     error
	address string
	calls   int
}

func (resolver *verificationTargetResolver) ResolveVerificationTarget(_ context.Context, address string) (verify.VerificationTarget, error) {
	resolver.calls++
	resolver.address = address
	return resolver.target, resolver.err
}

func (repository *verificationRepository) Submit(_ context.Context, request verify.Request, options ...verify.SubmissionOptions) (verify.VerificationJob, bool, error) {
	repository.submitCalls++
	repository.request = request
	if len(options) == 1 {
		repository.submission = options[0]
	}
	return repository.job, true, nil
}

func (*verificationRepository) Claim(context.Context, string, time.Duration) (verify.VerificationLease, bool, error) {
	return verify.VerificationLease{}, false, nil
}

func (*verificationRepository) Renew(context.Context, verify.VerificationLease, time.Duration) error {
	return nil
}

func (*verificationRepository) BindCompiler(context.Context, verify.VerificationLease, verify.CompilerProvenance) error {
	return nil
}

func (*verificationRepository) Complete(context.Context, verify.VerificationLease, verify.Completion) error {
	return nil
}

func (*verificationRepository) Fail(context.Context, verify.VerificationLease, verify.ErrorCode) error {
	return nil
}

func (repository *verificationRepository) Job(_ context.Context, id string) (verify.VerificationJob, bool, error) {
	return repository.job, id == repository.job.ID, nil
}

func (repository *verificationRepository) VerifiedContract(_ context.Context, chainID uint64, address, codeHash string) (verify.VerifiedContract, bool, error) {
	found := chainID == repository.contract.ChainID && address == repository.contract.Address && codeHash == repository.contract.CodeHash
	return repository.contract, found, nil
}

func TestVerificationAPIRequiresKeyAndBindsServerChain(t *testing.T) {
	t.Parallel()
	const (
		storedAddress   = "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"
		checksumAddress = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	)
	now := time.Unix(100, 0).UTC()
	repository := &verificationRepository{
		job: verify.VerificationJob{
			ID: "123e4567-e89b-42d3-a456-426614174000", Status: verify.JobQueued,
			CreatedAt: now, UpdatedAt: now,
		},
		contract: verify.VerifiedContract{
			ChainID: 11155111, Address: storedAddress,
			CodeHash: "0x" + strings.Repeat("2", 64), ValidFromBlock: 7,
			Language: verify.LanguageSolidity, CompilerVersion: "0.8.30", MatchKind: verify.MatchExact,
			ContractName: "Counter", ABI: json.RawMessage(`[]`), Sources: json.RawMessage(`{"Counter.sol":{"content":"contract Counter {}"}}`),
			Settings: json.RawMessage(`{}`), CreatedAt: now,
		},
	}
	service, err := verify.NewService(repository, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Chain.ID = 11155111
	runtimeBytecode := []byte{0x60, 0x00}
	verificationCodeHash := testVerificationRuntimeHash(runtimeBytecode)
	resolver := &verificationTargetResolver{target: verify.VerificationTarget{
		ChainID: cfg.Chain.ID, Address: storedAddress,
		CodeHash: verificationCodeHash, AtBlockHash: "0x" + strings.Repeat("3", 64),
		CreationBytecode: "0x6000", RuntimeBytecode: "0x6000",
	}}
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{}, VerificationReader: service,
		VerificationSubmitter: service, VerificationTargets: resolver,
		RequestID: func() string { return "request-verify" },
	})
	if err != nil {
		t.Fatal(err)
	}
	body := `{
		"address":"` + storedAddress + `",
		"language":"solidity",
		"compiler_version":"0.8.30",
		"contract_identifier":"Counter.sol:Counter",
		"standard_json":{"language":"Solidity","sources":{"Counter.sol":{"content":"contract Counter {}"}},"settings":{}}
	}`

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/v1/verification/jobs", strings.NewReader(body)))
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "api_key_required") {
		t.Fatalf("status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: []byte(strings.Repeat("p", 32))}
	issued, err := manager.Create(context.Background(), "test", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(context.Background(), issued.Token); err != nil {
		t.Fatalf("created API key did not authenticate: %v", err)
	}
	protected := manager.Middleware(false, handler)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/verification/jobs", strings.NewReader(body))
	request.Header.Set("X-API-Key", issued.Token)
	recorder := httptest.NewRecorder()
	protected.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), repository.job.ID) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if resolver.calls != 1 || resolver.address != storedAddress || repository.request.ChainID != cfg.Chain.ID ||
		repository.request.Address != repository.contract.Address || repository.request.CodeHash != verificationCodeHash ||
		repository.request.AtBlockHash != resolver.target.AtBlockHash || repository.request.CreationBytecode != "0x6000" ||
		repository.request.RuntimeBytecode != "0x6000" {
		t.Fatalf("request was not server-bound: %#v", repository.request)
	}

	contractRequest := httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts/"+repository.contract.Address+"/verification?code_hash="+repository.contract.CodeHash, nil)
	contractRequest.Header.Set("X-API-Key", issued.Token)
	recorder = httptest.NewRecorder()
	protected.ServeHTTP(recorder, contractRequest)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Counter") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Address string `json:"address"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Address != checksumAddress {
		t.Fatalf("address=%q want=%q", response.Data.Address, checksumAddress)
	}
}

func testVerificationRuntimeHash(runtimeBytecode []byte) string {
	hasher := sha3.NewLegacyKeccak256()
	_, _ = hasher.Write(runtimeBytecode)
	return "0x" + hex.EncodeToString(hasher.Sum(nil))
}

func TestVerificationAPIRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	repository := &verificationRepository{job: verify.VerificationJob{ID: "123e4567-e89b-42d3-a456-426614174000"}}
	service, err := verify.NewService(repository, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &verificationTargetResolver{target: verify.VerificationTarget{
		ChainID: 1, Address: "0x" + strings.Repeat("1", 40),
		CodeHash: testVerificationRuntimeHash([]byte{0x60, 0x00}), AtBlockHash: "0x" + strings.Repeat("2", 64),
		CreationBytecode: "0x6000", RuntimeBytecode: "0x6000",
	}}
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{}, VerificationReader: service,
		VerificationSubmitter: service, VerificationTargets: resolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: []byte(strings.Repeat("p", 32))}
	issued, err := manager.Create(context.Background(), "test", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/verification/jobs", strings.NewReader(`{"unexpected":true}`))
	request.Header.Set("X-API-Key", issued.Token)
	recorder := httptest.NewRecorder()
	manager.Middleware(false, handler).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestVerificationRoutesReportDisabledCapability(t *testing.T) {
	t.Parallel()
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{},
		RequestID: func() string { return "request-disabled-verification" },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/v1/verification/jobs", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodGet, "/api/v1/verification/jobs/123e4567-e89b-42d3-a456-426614174000", nil),
		httptest.NewRequest(http.MethodGet, "/api/v1/contracts/0x1111111111111111111111111111111111111111/verification?code_hash=0x"+strings.Repeat("2", 64), nil),
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"code":"verification_unavailable"`) {
			t.Fatalf("%s %s: status=%d body=%s", request.Method, request.URL, recorder.Code, recorder.Body.String())
		}
		if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
			t.Fatalf("%s %s: content type=%q", request.Method, request.URL, contentType)
		}
	}
}

func TestVerificationReadsRemainAvailableWhenPublicSubmissionIsDisabled(t *testing.T) {
	t.Parallel()
	now := time.Unix(100, 0).UTC()
	repository := &verificationRepository{job: verify.VerificationJob{
		ID: "123e4567-e89b-42d3-a456-426614174000", Status: verify.JobQueued,
		CreatedAt: now, UpdatedAt: now,
	}}
	service, err := verify.NewService(repository, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{}, VerificationReader: service,
		RequestID: func() string { return "read-only-verification" },
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: []byte(strings.Repeat("p", 32))}
	issued, err := manager.Create(context.Background(), "reader", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	protected := manager.Middleware(false, handler)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/verification/jobs/"+repository.job.ID, nil)
	request.Header.Set("X-API-Key", issued.Token)
	recorder := httptest.NewRecorder()
	protected.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), repository.job.ID) {
		t.Fatalf("read status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/verification/jobs", strings.NewReader(`{}`))
	request.Header.Set("X-API-Key", issued.Token)
	recorder = httptest.NewRecorder()
	protected.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "verification_unavailable") {
		t.Fatalf("submit status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"verification":false`) {
		t.Fatalf("config status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestVerificationSubmissionRejectsClientSelectedIdentityBeforeResolution(t *testing.T) {
	t.Parallel()
	repository := &verificationRepository{job: verify.VerificationJob{
		ID: "123e4567-e89b-42d3-a456-426614174000", Status: verify.JobQueued,
	}}
	service, err := verify.NewService(repository, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &verificationTargetResolver{}
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{}, VerificationReader: service,
		VerificationSubmitter: service, VerificationTargets: resolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: []byte(strings.Repeat("p", 32))}
	issued, err := manager.Create(context.Background(), "submitter", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	body := `{
		"address":"0x1111111111111111111111111111111111111111",
		"code_hash":"0x` + strings.Repeat("2", 64) + `",
		"language":"solidity","compiler_version":"0.8.30",
		"contract_identifier":"A.sol:A",
		"standard_json":{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}
	}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/verification/jobs", strings.NewReader(body))
	request.Header.Set("X-API-Key", issued.Token)
	recorder := httptest.NewRecorder()
	manager.Middleware(false, handler).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || resolver.calls != 0 || repository.submitCalls != 0 {
		t.Fatalf("status=%d resolver_calls=%d submit_calls=%d body=%s", recorder.Code, resolver.calls, repository.submitCalls, recorder.Body.String())
	}
}

func TestVerificationSubmissionStripsOnlyCanonicalConstructorSuffix(t *testing.T) {
	t.Parallel()
	address := "0x1111111111111111111111111111111111111111"
	runtime := []byte{0x60, 0x00}
	repository := &verificationRepository{job: verify.VerificationJob{
		ID: "123e4567-e89b-42d3-a456-426614174000", Status: verify.JobQueued,
		CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
	}}
	service, err := verify.NewService(repository, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &verificationTargetResolver{target: verify.VerificationTarget{
		ChainID: 1, Address: address, CodeHash: testVerificationRuntimeHash(runtime),
		AtBlockHash:      "0x" + strings.Repeat("3", 64),
		CreationBytecode: "0x6000aabb", RuntimeBytecode: "0x6000",
	}}
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{}, VerificationReader: service,
		VerificationSubmitter: service, VerificationTargets: resolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: []byte(strings.Repeat("p", 32))}
	issued, err := manager.Create(context.Background(), "submitter", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	submit := func(arguments string) *httptest.ResponseRecorder {
		body := `{
			"address":"` + address + `","language":"solidity","compiler_version":"0.8.30",
			"contract_identifier":"A.sol:A","constructor_arguments":"` + arguments + `",
			"standard_json":{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}
		}`
		request := httptest.NewRequest(http.MethodPost, "/api/v1/verification/jobs", strings.NewReader(body))
		request.Header.Set("X-API-Key", issued.Token)
		recorder := httptest.NewRecorder()
		manager.Middleware(false, handler).ServeHTTP(recorder, request)
		return recorder
	}

	recorder := submit("0xAABB")
	if recorder.Code != http.StatusAccepted || repository.request.CreationBytecode != "0x6000" || repository.request.ConstructorArgs != "aabb" {
		t.Fatalf("status=%d request=%#v body=%s", recorder.Code, repository.request, recorder.Body.String())
	}
	before := repository.submitCalls
	recorder = submit("0xccdd")
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "invalid_constructor_arguments") || repository.submitCalls != before {
		t.Fatalf("status=%d submit_calls=%d body=%s", recorder.Code, repository.submitCalls, recorder.Body.String())
	}
}
