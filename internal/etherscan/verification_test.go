package etherscan

import (
	"bytes"
	"context"
	"database/sql"
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
		contains: "FROM normalized_traces AS trace", columns: fakeColumns(5),
		rows: [][]driver.Value{{codeHash, testHashBytes(32), runtimeBytecode, "0x6001aabb", false}},
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
		request.Target.GenesisPredeploy ||
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

func TestSourceVerificationBuildsAuthenticatedGenesisRuntimeOnlyRequest(t *testing.T) {
	t.Parallel()
	const jobID = "123e4567-e89b-42d3-a456-426614174000"
	service := &fakeVerificationService{
		submitJob: verify.VerificationJob{ID: jobID, Status: verify.JobQueued},
	}
	runtimeBytecode := []byte{0x60, 0x02}
	codeHash := testRuntimeCodeHash(runtimeBytecode)
	backend := testPostgresBackend(t, fakeDatabase(t, sqlExpectation{
		contains: "FROM genesis_state_imports AS imported", columns: fakeColumns(5),
		rows: [][]driver.Value{{codeHash, testHashBytes(32), runtimeBytecode, nil, true}},
	}), PostgresOptions{ChainID: 1, Verification: service, VerificationMaxInputBytes: 1 << 20})
	values := url.Values{
		"contractaddress": {testContract}, "sourceCode": {"contract A {}"},
		"codeformat": {"solidity-single-file"}, "contractname": {"A"},
		"compilerversion": {"v0.8.30"},
	}
	result, err := backend.Execute(context.Background(), Request{
		Module: "contract", Action: "verifysourcecode", Values: values,
	})
	if err != nil || result != jobID {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	request := service.submitted
	if service.submitCalls != 1 || request.Target == nil || !request.Target.GenesisPredeploy ||
		request.Target.CreationBytecode != "" || request.Target.RuntimeBytecode != "0x6002" ||
		len(request.Bytecodes) != 1 || request.Bytecodes[0].Creation != "" ||
		request.Bytecodes[0].Runtime != "0x6002" {
		t.Fatalf("submitted Genesis request=%+v", request)
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
		contains: "FROM contract_code_observations AS observation", columns: fakeColumns(5),
	}), PostgresOptions{ChainID: 1, Verification: service})
	_, err := missing.Execute(context.Background(), Request{Module: "contract", Action: "verifysourcecode", Values: base})
	if !errors.Is(err, ErrVerificationTargetUnavailable) || !errors.Is(err, ErrVerificationUnavailable) {
		t.Fatalf("missing proof error=%v", err)
	}

	mismatchValues := cloneValues(base)
	mismatchValues.Set("constructorArguments", "ccdd")
	runtimeBytecode := []byte{0x60, 0x02}
	mismatch := testPostgresBackend(t, fakeDatabase(t, sqlExpectation{
		contains: "FROM normalized_traces AS trace", columns: fakeColumns(5),
		rows: [][]driver.Value{{testRuntimeCodeHash(runtimeBytecode), testHashBytes(32), runtimeBytecode, "0x6001aabb", false}},
	}), PostgresOptions{ChainID: 1, Verification: service})
	_, err = mismatch.Execute(context.Background(), Request{Module: "contract", Action: "verifysourcecode", Values: mismatchValues})
	if err != nil || service.submitCalls != 1 || service.submitted.Bytecodes[0].Creation != "0x6001aabb" {
		t.Fatalf("constructor hint error=%v submitCalls=%d request=%+v", err, service.submitCalls, service.submitted)
	}

	corrupt := testPostgresBackend(t, fakeDatabase(t, sqlExpectation{
		contains: "FROM normalized_traces AS trace", columns: fakeColumns(5),
		rows: [][]driver.Value{{testHashBytes(31), testHashBytes(32), runtimeBytecode, "0x6001", false}},
	}), PostgresOptions{ChainID: 1, Verification: service})
	_, err = corrupt.Execute(context.Background(), Request{Module: "contract", Action: "verifysourcecode", Values: base})
	if !errors.Is(err, ErrVerificationTargetUnavailable) || service.submitCalls != 1 {
		t.Fatalf("corrupt code hash error=%v submitCalls=%d", err, service.submitCalls)
	}

	malformedCreation := testPostgresBackend(t, fakeDatabase(t, sqlExpectation{
		contains: "FROM normalized_traces AS trace", columns: fakeColumns(5),
		rows: [][]driver.Value{{testRuntimeCodeHash(runtimeBytecode), testHashBytes(32), runtimeBytecode, "", true}},
	}), PostgresOptions{ChainID: 1, Verification: service})
	_, err = malformedCreation.Execute(context.Background(), Request{
		Module: "contract", Action: "verifysourcecode", Values: base,
	})
	if !errors.Is(err, ErrVerificationTargetUnavailable) || service.submitCalls != 1 {
		t.Fatalf("malformed creation fallback error=%v submitCalls=%d", err, service.submitCalls)
	}
}

func TestResolveVerificationTargetReturnsCanonicalServerFacts(t *testing.T) {
	t.Parallel()
	runtimeBytecode := []byte{0x60, 0x02}
	codeHash := testRuntimeCodeHash(runtimeBytecode)
	backend := testPostgresBackend(t, fakeDatabase(t, sqlExpectation{
		contains: "FROM normalized_traces AS trace", columns: fakeColumns(5),
		rows: [][]driver.Value{{codeHash, testHashBytes(32), runtimeBytecode, "0x6001AABB", false}},
	}), PostgresOptions{ChainID: 1, VerificationMaxInputBytes: 1 << 20})
	target, err := backend.ResolveVerificationTarget(context.Background(), testContract)
	if err != nil {
		t.Fatal(err)
	}
	if target.ChainID != 1 || target.Address != strings.ToLower(testContract) ||
		target.CodeHash != "0x"+hex.EncodeToString(codeHash) || target.AtBlockHash != testHash(32) ||
		target.CreationBytecode != "0x6001aabb" || target.RuntimeBytecode != "0x6002" ||
		target.GenesisPredeploy {
		t.Fatalf("target=%+v", target)
	}

	if _, err := backend.ResolveVerificationTarget(context.Background(), "not-an-address"); !errors.Is(err, ErrVerificationTargetUnavailable) {
		t.Fatalf("invalid address error=%v", err)
	}
}

func TestResolveVerificationTargetReturnsAuthenticatedGenesisFacts(t *testing.T) {
	t.Parallel()
	runtimeBytecode := []byte{0x60, 0x02}
	codeHash := testRuntimeCodeHash(runtimeBytecode)
	backend := testPostgresBackend(t, fakeDatabase(t, sqlExpectation{
		contains: "genesis_canonical.number = 0", columns: fakeColumns(5),
		rows: [][]driver.Value{{codeHash, testHashBytes(32), runtimeBytecode, nil, true}},
	}), PostgresOptions{ChainID: 1, VerificationMaxInputBytes: 1 << 20})
	target, err := backend.ResolveVerificationTarget(context.Background(), testContract)
	if err != nil {
		t.Fatal(err)
	}
	if !target.GenesisPredeploy || target.CreationBytecode != "" ||
		target.RuntimeBytecode != "0x6002" {
		t.Fatalf("Genesis target=%+v", target)
	}
}

func TestResolveVerificationTargetRejectsUnprovenGenesisShapes(t *testing.T) {
	t.Parallel()
	runtime := []byte{0x60, 0x02}
	for _, test := range []struct {
		name     string
		codeHash []byte
		runtime  []byte
		proven   bool
	}{
		{
			name:     "incomplete noncanonical or missing account proof",
			codeHash: testRuntimeCodeHash(runtime), runtime: runtime,
		},
		{
			name:     "empty account code",
			codeHash: testRuntimeCodeHash(nil), proven: true,
		},
		{
			name:     "code hash mismatch",
			codeHash: testRuntimeCodeHash([]byte{0x60, 0x03}), runtime: runtime, proven: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := testPostgresBackend(t, fakeDatabase(t, sqlExpectation{
				contains: "FROM genesis_state_imports AS imported", columns: fakeColumns(5),
				rows: [][]driver.Value{{
					test.codeHash, testHashBytes(32), test.runtime, nil, test.proven,
				}},
			}), PostgresOptions{ChainID: 1, VerificationMaxInputBytes: 1 << 20})
			if _, err := backend.ResolveVerificationTarget(
				context.Background(), testContract,
			); !errors.Is(err, ErrVerificationTargetUnavailable) {
				t.Fatalf("unproven target error = %v", err)
			}
		})
	}
}

func TestVerificationTargetQueryAuthenticatesExactGenesisRuntime(t *testing.T) {
	t.Parallel()
	query := compactSQL(verificationTargetSQL)
	for _, required := range []string{
		"imported.state = 'complete'",
		"genesis_canonical.number = 0",
		"genesis_canonical.block_hash = imported.block_hash",
		"account.address = $2",
		"octet_length(account.code) > 0",
		"account.code_hash = current_code.code_hash",
		"account.code = current_code.code",
	} {
		if !strings.Contains(query, compactSQL(required)) {
			t.Fatalf("verification target query lacks %q: %s", required, query)
		}
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
	admin := testAddressBytes(testRecipient)
	adminCodeHash := testHashBytes(85)
	service := &fakeVerificationService{
		submitJob: verify.VerificationJob{ID: jobID, Kind: verify.JobProxy, Status: verify.JobQueued},
	}
	backend := testPostgresBackend(t, fakeDatabase(t, sqlExpectation{
		contains: "FROM current_proxy", columns: fakeColumns(24),
		rows: [][]driver.Value{{
			proxyCodeHash, blockHash, "123", testHashBytes(83),
			"eip1967", "transparent", "5.6.1",
			implementation, implementationCodeHash, admin, adminCodeHash, nil, nil,
			"proxy_admin", admin, adminCodeHash, int64(101), int64(102), nil, nil,
			true, true, true, nil,
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
		request.ProxyTarget.Pattern != "transparent" ||
		request.ProxyTarget.StandardVersion != "5.6.1" ||
		request.ProxyTarget.SubmissionContextBlockNumber != "123" ||
		request.ProxyTarget.SubmissionContextBlockHash != "0x"+hex.EncodeToString(testHashBytes(83)) ||
		request.ProxyTarget.ImplementationAddress != "0x"+hex.EncodeToString(implementation) ||
		request.ProxyTarget.ImplementationCodeHash != "0x"+hex.EncodeToString(implementationCodeHash) ||
		request.ProxyTarget.AdminAddress != "0x"+hex.EncodeToString(admin) ||
		request.ProxyTarget.AdminCodeHash != "0x"+hex.EncodeToString(adminCodeHash) ||
		request.ProxyTarget.ManagementKind != "proxy_admin" ||
		request.ProxyTarget.ManagementAddress != "0x"+hex.EncodeToString(admin) ||
		request.ProxyTarget.ManagementCodeHash != "0x"+hex.EncodeToString(adminCodeHash) ||
		request.ProxyTarget.ObservationGenerationID != "101" ||
		request.ProxyTarget.ArtifactResolutionID != "102" ||
		request.ProxyTarget.BeaconGenerationID != "" ||
		request.ProxyTarget.UUPSGenerationID != "" {
		t.Fatalf("proxy request=%+v", request)
	}
}

func TestProxyVerificationReusesStillCurrentBinding(t *testing.T) {
	t.Parallel()
	const bindingID = "123e4567-e89b-42d3-a456-426614174000"
	proxyCodeHash := testHashBytes(86)
	blockHash := testHashBytes(87)
	implementation := testAddressBytes(testSender)
	implementationCodeHash := testHashBytes(88)
	service := &fakeVerificationService{}
	backend := testPostgresBackend(t, fakeDatabase(t, sqlExpectation{
		contains: "FROM current_proxy", columns: fakeColumns(24),
		rows: [][]driver.Value{{
			proxyCodeHash, blockHash, "124", testHashBytes(89),
			"eip1967", "erc1967", "5.6.1",
			implementation, implementationCodeHash, nil, nil, nil, nil,
			"none", nil, nil, int64(201), int64(202), nil, nil,
			true, true, true, bindingID,
		}},
	}), PostgresOptions{ChainID: 1, Verification: service})
	result, err := backend.Execute(context.Background(), Request{
		Module: "contract", Action: "verifyproxycontract",
		Values: url.Values{
			"address":                {testContract},
			"expectedimplementation": {"0x" + hex.EncodeToString(implementation)},
		},
	})
	if err != nil || result != bindingID {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	if service.submitted.ProxyTarget != nil {
		t.Fatalf("still-current binding created another request: %+v", service.submitted)
	}
}

func TestProxyVerificationRejectsUnverifiedOrMalformedManagementBinding(t *testing.T) {
	t.Parallel()
	proxyCodeHash := testHashBytes(91)
	blockHash := testHashBytes(92)
	implementation := testAddressBytes(testSender)
	implementationCodeHash := testHashBytes(93)
	admin := testAddressBytes(testRecipient)
	adminCodeHash := testHashBytes(94)
	for _, test := range []struct {
		name               string
		managementAddress  []byte
		managementVerified bool
	}{
		{name: "unverified", managementAddress: admin, managementVerified: false},
		{name: "wrong admin", managementAddress: implementation, managementVerified: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeVerificationService{}
			backend := testPostgresBackend(t, fakeDatabase(t, sqlExpectation{
				contains: "FROM current_proxy", columns: fakeColumns(24),
				rows: [][]driver.Value{{
					proxyCodeHash, blockHash, "123", testHashBytes(95),
					"eip1967", "transparent", "5.6.1",
					implementation, implementationCodeHash, admin, adminCodeHash, nil, nil,
					"proxy_admin", test.managementAddress, adminCodeHash, int64(101), int64(102), nil, nil,
					true, true, test.managementVerified, nil,
				}},
			}), PostgresOptions{ChainID: 1, Verification: service})
			result, err := backend.Execute(context.Background(), Request{
				Module: "contract", Action: "verifyproxycontract",
				Values: url.Values{"address": {testContract}},
			})
			if result != "" || !errors.Is(err, ErrProxyVerificationTargetUnavailable) ||
				service.submitted.ProxyTarget != nil {
				t.Fatalf("result=%#v error=%v submitted=%+v", result, err, service.submitted)
			}
		})
	}
}

func TestExactProxyVerificationTargetCoversSupportedManagementShapes(t *testing.T) {
	t.Parallel()
	identity := func(addressByte, hashByte byte) ([]byte, []byte) {
		return bytes.Repeat([]byte{addressByte}, 20), bytes.Repeat([]byte{hashByte}, 32)
	}
	implementation, implementationHash := identity(1, 2)
	admin, adminHash := identity(3, 4)
	beacon, beaconHash := identity(5, 6)
	base := proxyVerificationTarget{
		proxyCodeHash: bytes.Repeat([]byte{7}, 32), blockHash: bytes.Repeat([]byte{8}, 32),
		contextBlockNumber: "123", contextBlockHash: bytes.Repeat([]byte{9}, 32),
		implementationAddress: implementation, implementationCodeHash: implementationHash,
		observationGeneration: 1, managementKind: "none",
	}
	clone := base
	clone.kind, clone.pattern = "eip1167", "clone"
	erc1967 := base
	erc1967.kind, erc1967.pattern = "eip1967", "erc1967"
	erc1967.artifactResolution = sql.NullInt64{Int64: 2, Valid: true}
	erc1967.standardVersion = sql.NullString{String: "5.6.1", Valid: true}
	uups := base
	uups.kind, uups.pattern = "eip1967", "uups"
	uups.artifactResolution = sql.NullInt64{Int64: 2, Valid: true}
	uups.uupsGeneration = sql.NullInt64{Int64: 4, Valid: true}
	uups.standardVersion = sql.NullString{String: "5.6.1", Valid: true}
	transparent := base
	transparent.kind, transparent.pattern = "eip1967", "transparent"
	transparent.artifactResolution = sql.NullInt64{Int64: 2, Valid: true}
	transparent.standardVersion = sql.NullString{String: "5.6.1", Valid: true}
	transparent.adminAddress, transparent.adminCodeHash = admin, adminHash
	transparent.managementKind = "proxy_admin"
	transparent.managementAddress, transparent.managementCodeHash = admin, adminHash
	beaconProxy := base
	beaconProxy.kind, beaconProxy.pattern = "beacon", "beacon"
	beaconProxy.artifactResolution = sql.NullInt64{Int64: 2, Valid: true}
	beaconProxy.beaconGeneration = sql.NullInt64{Int64: 3, Valid: true}
	beaconProxy.standardVersion = sql.NullString{String: "5.6.1", Valid: true}
	beaconProxy.beaconAddress, beaconProxy.beaconCodeHash = beacon, beaconHash
	beaconProxy.managementKind = "upgradeable_beacon"
	beaconProxy.managementAddress, beaconProxy.managementCodeHash = beacon, beaconHash
	for _, target := range []proxyVerificationTarget{clone, erc1967, uups, transparent, beaconProxy} {
		if !validExactProxyVerificationTarget(target) {
			t.Fatalf("valid target rejected: %+v", target)
		}
	}
	transparent.managementAddress = implementation
	if validExactProxyVerificationTarget(transparent) {
		t.Fatal("mismatched immutable ProxyAdmin was accepted")
	}
	beaconProxy.beaconCodeHash = nil
	if validExactProxyVerificationTarget(beaconProxy) {
		t.Fatal("partial immutable beacon identity was accepted")
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
