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
	request    verify.Request
	submission verify.SubmissionOptions
	job        verify.VerificationJob
	contract   verify.VerifiedContract
}

func (repository *verificationRepository) Submit(_ context.Context, request verify.Request, options ...verify.SubmissionOptions) (verify.VerificationJob, bool, error) {
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
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{}, Verification: service,
		RequestID: func() string { return "request-verify" },
	})
	if err != nil {
		t.Fatal(err)
	}
	runtimeBytecode := []byte{0x60, 0x00}
	verificationCodeHash := testVerificationRuntimeHash(runtimeBytecode)
	body := `{
		"address":"` + storedAddress + `",
		"code_hash":"` + verificationCodeHash + `",
		"at_block_hash":"0x` + strings.Repeat("3", 64) + `",
		"language":"solidity",
		"compiler_version":"0.8.30",
		"contract_identifier":"Counter.sol:Counter",
		"standard_json":{"language":"Solidity","sources":{},"settings":{}},
		"creation_bytecode":"0x6000",
		"runtime_bytecode":"0x6000"
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
	if repository.request.ChainID != cfg.Chain.ID || repository.request.Address != repository.contract.Address {
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
	handler, err := New(Options{Config: config.Default(), Reader: fakeReader{}, Verification: service})
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
