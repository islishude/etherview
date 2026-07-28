package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/verify"
)

type verificationV2Service struct {
	submission verify.SubmissionV2
	job        verify.VerificationJob
	contract   verify.VerifiedContract
}

func (service *verificationV2Service) SubmitV2(
	_ context.Context,
	submission verify.SubmissionV2,
) (verify.VerificationJob, bool, error) {
	service.submission = submission
	return service.job, true, nil
}

func (service *verificationV2Service) Job(_ context.Context, id string) (verify.VerificationJob, bool, error) {
	return service.job, id == service.job.ID, nil
}

func (service *verificationV2Service) VerifiedContract(
	_ context.Context,
	chainID uint64,
	address string,
	codeHash string,
) (verify.VerifiedContract, bool, error) {
	found := chainID == service.contract.ChainID && address == service.contract.Address &&
		codeHash == service.contract.CodeHash
	return service.contract, found, nil
}

type verificationTargetResolver struct {
	target verify.VerificationTarget
}

func (resolver verificationTargetResolver) ResolveVerificationTarget(
	context.Context,
	string,
) (verify.VerificationTarget, error) {
	return resolver.target, nil
}

type compilerCatalogStub struct{}

func (compilerCatalogStub) Versions(context.Context, verify.Language) ([]string, error) {
	return []string{"0.8.30+commit.73712a01"}, nil
}

func TestVerifierV2RoutesBindAddressAndRemoveV1Surface(t *testing.T) {
	t.Parallel()
	const address = "0x1111111111111111111111111111111111111111"
	now := time.Unix(100, 0).UTC()
	target := verify.VerificationTarget{
		ChainID: 1, Address: address, CodeHash: "0x" + strings.Repeat("2", 64),
		AtBlockHash:      "0x" + strings.Repeat("3", 64),
		CreationBytecode: "0x6000", RuntimeBytecode: "0x6001",
	}
	service := &verificationV2Service{
		job: verify.VerificationJob{
			ID: "123e4567-e89b-42d3-a456-426614174000", Kind: verify.JobAddress,
			Status: verify.JobQueued, CreatedAt: now, UpdatedAt: now,
		},
		contract: verify.VerifiedContract{
			ChainID: 1, Address: address, CodeHash: target.CodeHash, ValidFromBlock: 7,
			Language: verify.LanguageSolidity, CompilerVersion: "0.8.30+commit.73712a01",
			FileName: "Counter.sol", ContractName: "Counter",
			ABI: json.RawMessage(`[]`), Sources: json.RawMessage(`{"Counter.sol":{"content":"contract Counter {}"}}`),
			Settings: json.RawMessage(`{}`), CompilationArtifacts: json.RawMessage(`{}`),
			CreationCodeArtifacts: json.RawMessage(`{}`), RuntimeCodeArtifacts: json.RawMessage(`{}`),
			Libraries: map[string]string{}, IsBlueprint: false, CreatedAt: now,
		},
	}
	cfg := config.Default()
	cfg.Chain.ID = 1
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{}, VerificationReader: service,
		VerificationSubmitter: service, VerificationTargets: verificationTargetResolver{target: target},
		CompilerCatalog: compilerCatalogStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: []byte(strings.Repeat("p", 32))}
	key, err := manager.Create(context.Background(), "test", 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	protected := manager.Middleware(false, handler)
	body := `{
		"language":"solidity",
		"compiler_version":"0.8.30+commit.73712a01",
		"input_kind":"standard_json",
		"input":{"language":"Solidity","sources":{"Counter.sol":{"content":"contract Counter {}"}},"settings":{}}
	}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/contracts/"+address+"/verification", strings.NewReader(body))
	request.Header.Set("X-API-Key", key.Token)
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if service.submission.Kind != verify.JobAddress || service.submission.Target == nil ||
		service.submission.Target.CodeHash != target.CodeHash ||
		service.submission.Bytecodes[0].Runtime != target.RuntimeBytecode {
		t.Fatalf("submission was not server-bound: %#v", service.submission)
	}

	read := httptest.NewRequest(http.MethodGet, "/api/v1/contracts/"+address+"/verification", nil)
	read.Header.Set("X-API-Key", key.Token)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, read)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Counter.sol") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	for _, oldPath := range []string{
		"/api/v1/verification/jobs",
		"/api/v1/sourcify/imports",
		"/api/v1/sourcify/contracts/" + address,
	} {
		response = httptest.NewRecorder()
		protected.ServeHTTP(response, httptest.NewRequest(http.MethodGet, oldPath, nil))
		if response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("legacy route %s status=%d", oldPath, response.Code)
		}
	}
}

func TestVerifierV2SoloSourcifyAndCompilerRoutes(t *testing.T) {
	t.Parallel()
	now := time.Unix(100, 0).UTC()
	service := &verificationV2Service{job: verify.VerificationJob{
		ID: "123e4567-e89b-42d3-a456-426614174000", Kind: verify.JobSolidityStandardJSON,
		Status: verify.JobQueued, CreatedAt: now, UpdatedAt: now,
	}}
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{}, VerificationReader: service,
		VerificationSubmitter: service, CompilerCatalog: compilerCatalogStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: []byte(strings.Repeat("p", 32))}
	key, err := manager.Create(context.Background(), "test", 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	protected := manager.Middleware(false, handler)
	solo := `{
		"compiler_version":"0.8.30+commit.73712a01",
		"input":{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}},
		"bytecodes":{"runtime_bytecode":"0x6000"}
	}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/verifier/solidity/standard-json", strings.NewReader(solo))
	request.Header.Set("X-API-Key", key.Token)
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || service.submission.Kind != verify.JobSolidityStandardJSON {
		t.Fatalf("status=%d submission=%#v body=%s", response.Code, service.submission, response.Body.String())
	}

	sourcify := `{"chain_id":"1","address":"0x1111111111111111111111111111111111111111","files":{"metadata.json":"{}"}}`
	request = httptest.NewRequest(http.MethodPost, "/api/v1/verifier/sourcify", strings.NewReader(sourcify))
	request.Header.Set("X-API-Key", key.Token)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || service.submission.Kind != verify.JobSourcify {
		t.Fatalf("status=%d submission=%#v body=%s", response.Code, service.submission, response.Body.String())
	}

	response = httptest.NewRecorder()
	protected.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/verifier/compilers?language=yul", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "0.8.30") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
