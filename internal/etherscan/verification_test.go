package etherscan

import (
	"context"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/verify"
)

type fakeVerificationService struct {
	submitted     verify.SubmissionV2
	submitJob     verify.VerificationJob
	submitCreated bool
	submitError   error
	submitCalls   int
	job           verify.VerificationJob
	jobFound      bool
	jobError      error
}

func (service *fakeVerificationService) SubmitV2(_ context.Context, request verify.SubmissionV2) (verify.VerificationJob, bool, error) {
	service.submitted = request
	service.submitCalls++
	return service.submitJob, service.submitCreated, service.submitError
}

func (service *fakeVerificationService) Job(_ context.Context, _ string) (verify.VerificationJob, bool, error) {
	return service.job, service.jobFound, service.jobError
}

func TestSourceVerificationBuildsCanonicalDurableRequest(t *testing.T) {
	t.Parallel()
	const jobID = "123e4567-e89b-42d3-a456-426614174000"
	service := &fakeVerificationService{
		submitJob:     verify.VerificationJob{ID: jobID, Status: verify.JobQueued},
		submitCreated: true,
	}
	runtimeBytecode := []byte{0x60, 0x02}
	codeHash := testRuntimeCodeHash(runtimeBytecode)
	db := fakeDatabase(t, sqlExpectation{
		contains: "FROM normalized_traces AS trace", columns: fakeColumns(4),
		rows: [][]driver.Value{{codeHash, testHashBytes(32), runtimeBytecode, "0x6001aabb"}},
	})
	backend := testPostgresBackend(t, db, PostgresOptions{
		ChainID: 1, Verification: service, VerificationMaxInputBytes: 1 << 20,
	})
	sourceContent := "\npragma solidity ^0.8.0; library L {} contract A {}\n"
	standardJSON, err := json.Marshal(map[string]any{
		"language": "Solidity",
		"sources":  map[string]any{"A.sol": map[string]string{"content": sourceContent}},
		"settings": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{
		"contractaddress":      {testContract},
		"sourceCode":           {string(standardJSON)},
		"codeformat":           {"solidity-standard-json-input"},
		"contractname":         {"A.sol:A"},
		"compilerversion":      {"v0.8.30+commit.73712a01"},
		"optimizationUsed":     {"1"},
		"runs":                 {"200"},
		"constructorArguments": {"aabb"},
		"evmVersion":           {"paris"},
		"licenseType":          {"3"},
		"libraryname1":         {"A.sol:L"},
		"libraryaddress1":      {testSender},
	}
	result, err := backend.Execute(context.Background(), Request{Module: "contract", Action: "verifysourcecode", Values: values})
	if err != nil || result != jobID {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	request := service.submitted
	if service.submitCalls != 1 || request.Kind != verify.JobAddress || request.Target == nil ||
		request.Target.ChainID != 1 || request.Target.Address != strings.ToLower(testContract) ||
		request.Target.CodeHash != "0x"+hex.EncodeToString(codeHash) || request.Target.AtBlockHash != testHash(32) ||
		request.Target.CreationBytecode != "0x6001aabb" || request.Target.RuntimeBytecode != "0x6002" ||
		len(request.Bytecodes) != 1 || request.Bytecodes[0].Creation != "0x6001aabb" ||
		request.Bytecodes[0].Runtime != "0x6002" {
		t.Fatalf("submitted request=%+v", request)
	}
	if request.Language != verify.LanguageSolidity || request.ContractNameHint != "A.sol:A" || request.CompilerVersion != "v0.8.30+commit.73712a01" {
		t.Fatalf("compiler identity=%+v", request)
	}
	var input struct {
		Sources map[string]struct {
			Content string `json:"content"`
		} `json:"sources"`
		Settings struct {
			Optimizer struct {
				Enabled bool   `json:"enabled"`
				Runs    uint64 `json:"runs"`
			} `json:"optimizer"`
			EVMVersion string                       `json:"evmVersion"`
			Libraries  map[string]map[string]string `json:"libraries"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(request.StandardJSON, &input); err != nil {
		t.Fatal(err)
	}
	if input.Sources["A.sol"].Content != sourceContent || !input.Settings.Optimizer.Enabled || input.Settings.Optimizer.Runs != 200 || input.Settings.EVMVersion != "paris" {
		t.Fatalf("compiler input=%s", request.StandardJSON)
	}
	if got := input.Settings.Libraries["A.sol"]["L"]; got != strings.ToLower(testSender) {
		t.Fatalf("library address=%q", got)
	}
}

func TestSourceVerificationRejectsMissingProofAndIgnoresConstructorHint(t *testing.T) {
	t.Parallel()
	base := url.Values{
		"contractaddress": {testContract}, "sourceCode": {"contract A {}"},
		"codeformat": {"solidity-single-file"}, "contractname": {"A"},
		"compilerversion": {"v0.8.30"},
	}
	service := &fakeVerificationService{submitJob: verify.VerificationJob{ID: "123e4567-e89b-42d3-a456-426614174000"}}

	missing := testPostgresBackend(t, fakeDatabase(t, sqlExpectation{
		contains: "FROM contract_code_observations AS observation", columns: fakeColumns(4),
	}), PostgresOptions{ChainID: 1, Verification: service})
	_, err := missing.Execute(context.Background(), Request{Module: "contract", Action: "verifysourcecode", Values: base})
	if !errors.Is(err, ErrVerificationTargetUnavailable) || !errors.Is(err, ErrVerificationUnavailable) {
		t.Fatalf("missing proof error=%v", err)
	}

	mismatchValues := cloneValues(base)
	mismatchValues.Set("constructorArguments", "ccdd")
	runtimeBytecode := []byte{0x60, 0x02}
	mismatch := testPostgresBackend(t, fakeDatabase(t, sqlExpectation{
		contains: "FROM normalized_traces AS trace", columns: fakeColumns(4),
		rows: [][]driver.Value{{testRuntimeCodeHash(runtimeBytecode), testHashBytes(32), runtimeBytecode, "0x6001aabb"}},
	}), PostgresOptions{ChainID: 1, Verification: service})
	_, err = mismatch.Execute(context.Background(), Request{Module: "contract", Action: "verifysourcecode", Values: mismatchValues})
	if err != nil || service.submitCalls != 1 || service.submitted.Bytecodes[0].Creation != "0x6001aabb" {
		t.Fatalf("constructor hint error=%v submitCalls=%d request=%+v", err, service.submitCalls, service.submitted)
	}

	corrupt := testPostgresBackend(t, fakeDatabase(t, sqlExpectation{
		contains: "FROM normalized_traces AS trace", columns: fakeColumns(4),
		rows: [][]driver.Value{{testHashBytes(31), testHashBytes(32), runtimeBytecode, "0x6001"}},
	}), PostgresOptions{ChainID: 1, Verification: service})
	_, err = corrupt.Execute(context.Background(), Request{Module: "contract", Action: "verifysourcecode", Values: base})
	if !errors.Is(err, ErrVerificationTargetUnavailable) || service.submitCalls != 1 {
		t.Fatalf("corrupt code hash error=%v submitCalls=%d", err, service.submitCalls)
	}
}

func TestResolveVerificationTargetReturnsCanonicalServerFacts(t *testing.T) {
	t.Parallel()
	runtimeBytecode := []byte{0x60, 0x02}
	codeHash := testRuntimeCodeHash(runtimeBytecode)
	backend := testPostgresBackend(t, fakeDatabase(t, sqlExpectation{
		contains: "FROM normalized_traces AS trace", columns: fakeColumns(4),
		rows: [][]driver.Value{{codeHash, testHashBytes(32), runtimeBytecode, "0x6001AABB"}},
	}), PostgresOptions{ChainID: 1, VerificationMaxInputBytes: 1 << 20})
	target, err := backend.ResolveVerificationTarget(context.Background(), testContract)
	if err != nil {
		t.Fatal(err)
	}
	if target.ChainID != 1 || target.Address != strings.ToLower(testContract) ||
		target.CodeHash != "0x"+hex.EncodeToString(codeHash) || target.AtBlockHash != testHash(32) ||
		target.CreationBytecode != "0x6001aabb" || target.RuntimeBytecode != "0x6002" {
		t.Fatalf("target=%+v", target)
	}

	if _, err := backend.ResolveVerificationTarget(context.Background(), "not-an-address"); !errors.Is(err, ErrVerificationTargetUnavailable) {
		t.Fatalf("invalid address error=%v", err)
	}
}

func TestVerificationFormRejectsAmbiguousOrConflictingInput(t *testing.T) {
	t.Parallel()
	standard := `{"language":"Solidity","sources":{"A.sol":{"content":"contract A{}"},"L.sol":{"content":"library L{}"}},"settings":{"optimizer":{"enabled":true}}}`
	base := url.Values{
		"contractaddress": {testContract}, "sourceCode": {standard},
		"codeformat": {"solidity-standard-json-input"}, "contractname": {"A.sol:A"},
		"compilerversion": {"v0.8.30"},
	}
	for _, mutate := range []func(url.Values){
		func(values url.Values) { values["contractname"] = []string{"A.sol:A", "L.sol:L"} },
		func(values url.Values) { values.Set("unexpected", "ignored") },
		func(values url.Values) { values.Set("optimizationUsed", "0") },
		func(values url.Values) {
			values.Set("libraryname1", "L")
			values.Set("libraryaddress1", testSender)
		},
		func(values url.Values) {
			values.Set("constructorArguments", "aa")
			values.Set("constructorArguements", "bb")
		},
	} {
		values := cloneValues(base)
		mutate(values)
		if _, _, _, err := parseEtherscanVerificationForm(values, 1<<20); !errors.Is(err, ErrInvalidParameter) {
			t.Fatalf("values=%v error=%v", values, err)
		}
	}
}

func TestVyperVerificationFormIsStablyUnsupported(t *testing.T) {
	t.Parallel()
	values := url.Values{
		"contractaddress": {testContract},
		"sourceCode":      {`{"language":"Vyper","sources":{"A.vy":{"content":"@external\ndef value() -> uint256: return 1"}},"settings":{"optimize":"gas"}}`},
		"codeformat":      {"vyper-json"}, "contractname": {"A.vy:A"},
		"compilerversion": {"vyper:0.4.0"}, "optimizationUsed": {"1"},
	}
	_, _, _, err := parseEtherscanVerificationForm(values, 1<<20)
	if !errors.Is(err, ErrInvalidParameter) ||
		err.Error() != "invalid parameter: unsupported codeformat" {
		t.Fatalf("Vyper error=%v", err)
	}
}

func TestVerificationFormRejectsDuplicateJSONKeysAndPreservesLargeIntegers(t *testing.T) {
	t.Parallel()
	base := url.Values{
		"contractaddress": {testContract},
		"codeformat":      {"solidity-standard-json-input"},
		"contractname":    {"A.sol:A"},
		"compilerversion": {"v0.8.30"},
	}

	duplicate := cloneURLValues(base)
	duplicate.Set("sourceCode", `{
		"language":"Solidity",
		"sources":{"A.sol":{"content":"contract A {}","content":"contract B {}"}},
		"settings":{}
	}`)
	if _, _, _, err := parseEtherscanVerificationForm(duplicate, 1<<20); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("duplicate-key error=%v", err)
	}

	largeInteger := cloneURLValues(base)
	largeInteger.Set("sourceCode", `{
		"language":"Solidity",
		"sources":{"A.sol":{"content":"contract A {}"}},
		"settings":{"modelChecker":{"timeout":9007199254740993}}
	}`)
	form, _, _, err := parseEtherscanVerificationForm(largeInteger, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(form.standardJSON), `"timeout":9007199254740993`) {
		t.Fatalf("large integer changed during normalization: %s", form.standardJSON)
	}
}

func cloneURLValues(values url.Values) url.Values {
	clone := make(url.Values, len(values))
	for key, entries := range values {
		clone[key] = append([]string(nil), entries...)
	}
	return clone
}

func TestSourceVerificationStatusUsesEtherscanSemantics(t *testing.T) {
	t.Parallel()
	const guid = "123e4567-e89b-42d3-a456-426614174000"
	for _, test := range []struct {
		name   string
		job    verify.VerificationJob
		found  bool
		result string
		want   error
	}{
		{name: "queued", job: verify.VerificationJob{Status: verify.JobQueued}, found: true, want: ErrPending},
		{name: "running", job: verify.VerificationJob{Status: verify.JobRunning}, found: true, want: ErrPending},
		{name: "verified", job: verify.VerificationJob{Status: verify.JobSucceeded, Outcome: json.RawMessage(`{"kind":"verification_success"}`)}, found: true, result: "Pass - Verified"},
		{name: "mismatch", job: verify.VerificationJob{Status: verify.JobSucceeded, Outcome: json.RawMessage(`{"kind":"verification_failure"}`)}, found: true, want: ErrVerificationFailed},
		{name: "failed", job: verify.VerificationJob{Status: verify.JobFailed}, found: true, want: ErrVerificationFailed},
		{name: "missing", found: false, want: ErrVerificationJobNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeVerificationService{job: test.job, jobFound: test.found}
			backend := testPostgresBackend(t, fakeDatabase(t), PostgresOptions{ChainID: 1, Verification: service})
			result, err := backend.Execute(context.Background(), Request{Module: "contract", Action: "checkverifystatus", Values: url.Values{"guid": {guid}}})
			if result != test.result || !errors.Is(err, test.want) {
				t.Fatalf("result=%#v error=%v, want result=%q error=%v", result, err, test.result, test.want)
			}
		})
	}
}

func TestProxyVerificationBuildsCanonicalDurableRequest(t *testing.T) {
	t.Parallel()
	const jobID = "123e4567-e89b-42d3-a456-426614174000"
	proxyCodeHash := testHashBytes(81)
	blockHash := testHashBytes(82)
	implementation := testAddressBytes(testSender)
	implementationCodeHash := testHashBytes(84)
	service := &fakeVerificationService{
		submitJob: verify.VerificationJob{ID: jobID, Kind: verify.JobProxy, Status: verify.JobQueued},
	}
	backend := testPostgresBackend(t, fakeDatabase(t, sqlExpectation{
		contains: "FROM current_proxy", columns: fakeColumns(7),
		rows: [][]driver.Value{{
			proxyCodeHash, blockHash, "eip1967", implementation,
			implementationCodeHash, true, true,
		}},
	}), PostgresOptions{ChainID: 1, Verification: service})
	result, err := backend.Execute(context.Background(), Request{
		Module: "contract", Action: "verifyproxycontract",
		Values: url.Values{
			"address":                {testContract},
			"expectedimplementation": {"0x" + hex.EncodeToString(implementation)},
		},
	})
	if err != nil || result != jobID {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	request := service.submitted
	if request.Kind != verify.JobProxy || request.Target == nil || request.ProxyTarget == nil ||
		request.Target.Address != strings.ToLower(testContract) ||
		request.Target.CodeHash != "0x"+hex.EncodeToString(proxyCodeHash) ||
		request.Target.AtBlockHash != "0x"+hex.EncodeToString(blockHash) ||
		request.ProxyTarget.Kind != "eip1967" ||
		request.ProxyTarget.ImplementationAddress != "0x"+hex.EncodeToString(implementation) ||
		request.ProxyTarget.ImplementationCodeHash != "0x"+hex.EncodeToString(implementationCodeHash) {
		t.Fatalf("proxy request=%+v", request)
	}
}

func TestProxyVerificationStatusUsesDedicatedJobKind(t *testing.T) {
	t.Parallel()
	const guid = "123e4567-e89b-42d3-a456-426614174000"
	for _, test := range []struct {
		name   string
		job    verify.VerificationJob
		found  bool
		result string
		want   error
	}{
		{name: "queued", found: true, job: verify.VerificationJob{Kind: verify.JobProxy, Status: verify.JobQueued}, want: ErrPending},
		{name: "verified", found: true, job: verify.VerificationJob{
			Kind: verify.JobProxy, Status: verify.JobSucceeded,
			Outcome: json.RawMessage(`{"kind":"proxy_verification_success"}`),
		}, result: "Pass - Verified"},
		{name: "failed", found: true, job: verify.VerificationJob{Kind: verify.JobProxy, Status: verify.JobFailed}, want: ErrProxyVerificationFailed},
		{name: "source job", found: true, job: verify.VerificationJob{Kind: verify.JobAddress, Status: verify.JobSucceeded}, want: ErrVerificationJobNotFound},
		{name: "missing", want: ErrVerificationJobNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeVerificationService{job: test.job, jobFound: test.found}
			backend := testPostgresBackend(t, fakeDatabase(t), PostgresOptions{ChainID: 1, Verification: service})
			result, err := backend.Execute(context.Background(), Request{
				Module: "contract", Action: "checkproxyverification",
				Values: url.Values{"guid": {guid}},
			})
			if result != test.result || !errors.Is(err, test.want) {
				t.Fatalf("result=%#v error=%v, want result=%q error=%v", result, err, test.result, test.want)
			}
		})
	}
}

func testRuntimeCodeHash(code []byte) []byte {
	return crypto.Keccak256(code)
}
