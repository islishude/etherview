package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/geascompiler"
)

type verifyMemoryRepository struct {
	mu sync.Mutex

	lease        VerificationLease
	claimFound   bool
	claimError   error
	renewError   error
	bindError    error
	bindings     []CompilerProvenance
	failures     []ErrorCode
	outcomes     []string
	payloads     []json.RawMessage
	compilations []AuthenticatedCompilation
	submitJob    VerificationJob
	submitErr    error
	submits      int
	job          VerificationJob
	jobFound     bool
}

func (repository *verifyMemoryRepository) Claim(
	context.Context,
	string,
	time.Duration,
) (VerificationLease, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.lease, repository.claimFound, repository.claimError
}

func (repository *verifyMemoryRepository) Renew(
	context.Context,
	VerificationLease,
	time.Duration,
) error {
	return repository.renewError
}

func (repository *verifyMemoryRepository) BindCompiler(
	_ context.Context,
	_ VerificationLease,
	provenance CompilerProvenance,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.bindings = append(repository.bindings, provenance)
	return repository.bindError
}

func (repository *verifyMemoryRepository) CompleteV2(
	_ context.Context,
	_ VerificationLease,
	kind string,
	payload json.RawMessage,
	compilations ...AuthenticatedCompilation,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.outcomes = append(repository.outcomes, kind)
	repository.payloads = append(repository.payloads, append(json.RawMessage(nil), payload...))
	repository.compilations = append(repository.compilations, compilations...)
	return nil
}

func (repository *verifyMemoryRepository) Fail(
	_ context.Context,
	_ VerificationLease,
	code ErrorCode,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.failures = append(repository.failures, code)
	return nil
}

func (repository *verifyMemoryRepository) Job(
	context.Context,
	string,
) (VerificationJob, bool, error) {
	return repository.job, repository.jobFound, nil
}

func (repository *verifyMemoryRepository) VerifiedContract(
	context.Context,
	uint64,
	string,
) (VerifiedContract, bool, error) {
	return VerifiedContract{}, false, nil
}

func (repository *verifyMemoryRepository) SubmitV2(
	context.Context,
	SubmissionV2,
) (VerificationJob, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.submits++
	return repository.submitJob, true, repository.submitErr
}

type verifyTestCompiler struct {
	provenance    CompilerProvenance
	resolveError  error
	compileError  error
	output        json.RawMessage
	compileCalls  int
	compileInputs [][]byte
}

type verifyTestGeasCompiler struct {
	provenance CompilerProvenance
	responses  map[string]geascompiler.Response
	err        error
	calls      []string
}

func (compiler *verifyTestGeasCompiler) Provenance(Language, string) (CompilerProvenance, error) {
	return compiler.provenance, nil
}

func (compiler *verifyTestGeasCompiler) Resolve(
	context.Context,
	Language,
	string,
) (CompilerProvenance, error) {
	return compiler.provenance, nil
}

func (*verifyTestGeasCompiler) Compile(
	context.Context,
	Language,
	string,
	[]byte,
) ([]byte, error) {
	return nil, errors.New("generic Geas compile must not be used")
}

func (compiler *verifyTestGeasCompiler) CompileGeasEntrypointPinned(
	_ context.Context,
	_ string,
	_ CompilerProvenance,
	_ map[string]string,
	entrypoint string,
) (geascompiler.Response, error) {
	compiler.calls = append(compiler.calls, entrypoint)
	if compiler.err != nil {
		return geascompiler.Response{}, compiler.err
	}
	return compiler.responses[entrypoint], nil
}

func (compiler *verifyTestCompiler) Provenance(
	Language,
	string,
) (CompilerProvenance, error) {
	return compiler.provenance, compiler.resolveError
}

func (compiler *verifyTestCompiler) Resolve(
	context.Context,
	Language,
	string,
) (CompilerProvenance, error) {
	return compiler.provenance, compiler.resolveError
}

func (compiler *verifyTestCompiler) Compile(
	ctx context.Context,
	language Language,
	version string,
	input []byte,
) ([]byte, error) {
	return compiler.CompilePinned(ctx, language, version, compiler.provenance, input)
}

func (compiler *verifyTestCompiler) CompilePinned(
	_ context.Context,
	_ Language,
	_ string,
	_ CompilerProvenance,
	input []byte,
) ([]byte, error) {
	compiler.compileCalls++
	compiler.compileInputs = append(compiler.compileInputs, append([]byte(nil), input...))
	return append([]byte(nil), compiler.output...), compiler.compileError
}

func testSolcJSProvenance() CompilerProvenance {
	compilerDigest := sha256.Sum256([]byte("soljson"))
	executorDigest := sha256.Sum256([]byte("runtime manifest"))
	return CompilerProvenance{
		Kind:              CompilerSolcJS,
		Digest:            compilerDigest,
		ExecutorDigest:    executorDigest,
		ExecutorKind:      SolcJSExecutorKind,
		ExecutionPolicy:   TrustedSubprocessPolicy,
		CatalogGeneration: 1,
		Platform:          CompilerPlatformEmscriptenWASM32,
		ArtifactURL:       "https://binaries.soliditylang.org/emscripten-wasm32/soljson.js",
		ArtifactMaxBytes:  200 << 20,
	}
}

func testGeasProvenance() CompilerProvenance {
	return CompilerProvenance{
		Kind: CompilerGeas, Digest: sha256.Sum256([]byte("geas-v0.3.3")),
		ExecutorDigest: sha256.Sum256([]byte("geas-helper")),
		ExecutorKind:   GeasExecutorKind, ExecutionPolicy: TrustedSubprocessPolicy,
		Platform: CompilerPlatformGoModule,
	}
}

func verifyV2Lease() VerificationLease {
	input := json.RawMessage(`{
		"language":"Solidity",
		"sources":{"A.sol":{"content":"contract A {}"}},
		"settings":{}
	}`)
	return VerificationLease{
		Job: VerificationJob{
			ID:     verificationID(1),
			Kind:   JobSolidityStandardJSON,
			Status: JobRunning,
			RequestV2: &SubmissionV2{
				Kind:                 JobSolidityStandardJSON,
				Language:             LanguageSolidity,
				CompilerVersion:      "0.8.30",
				StandardJSON:         input,
				StandardJSONVariants: []json.RawMessage{input},
				Bytecodes: []BytecodePair{{
					Creation: "0x6001",
					Runtime:  "0x6001",
				}},
			},
		},
		Token: "lease-token",
	}
}

func newVerifyTestWorker(
	t *testing.T,
	repository Repository,
	compiler Compiler,
) *Worker {
	t.Helper()
	options := WorkerOptions{WorkerID: "test-worker"}
	worker, err := NewWorker(repository, compiler, options)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func TestWorkerPublishesExactGeasAddressVerification(t *testing.T) {
	request := validGeasSubmission()
	if err := (&Service{maxInputBytes: 5 << 20}).prepareV2(t.Context(), &request); err != nil {
		t.Fatal(err)
	}
	repository := &verifyMemoryRepository{
		claimFound: true,
		lease: VerificationLease{
			Job:   VerificationJob{ID: verificationID(63), Kind: JobAddress, Status: JobRunning, RequestV2: &request},
			Token: "geas-lease",
		},
	}
	compiler := &verifyTestGeasCompiler{
		provenance: testGeasProvenance(),
		responses: map[string]geascompiler.Response{
			"system/main.eas": {
				Schema: geascompiler.ProtocolSchema, Successful: true, Bytecode: "0x6001",
				Sources: []string{"common/value.eas", "system/main.eas"},
			},
			"system/ctor.eas": {
				Schema: geascompiler.ProtocolSchema, Successful: true, Bytecode: "0x6001",
				Sources: []string{"common/value.eas", "system/ctor.eas", "system/main.eas"},
			},
		},
	}
	worker := newVerifyTestWorker(t, repository, compiler)
	found, err := worker.ProcessOne(t.Context())
	if err != nil || !found {
		t.Fatalf("found=%t error=%v", found, err)
	}
	if len(repository.bindings) != 1 || repository.bindings[0] != compiler.provenance {
		t.Fatalf("bindings = %+v", repository.bindings)
	}
	if got, want := strings.Join(compiler.calls, ","), "system/main.eas,system/ctor.eas"; got != want {
		t.Fatalf("compile calls = %q, want %q", got, want)
	}
	if len(repository.outcomes) != 1 || repository.outcomes[0] != "verification_success" || len(repository.payloads) != 1 {
		t.Fatalf("outcomes=%v payloads=%d", repository.outcomes, len(repository.payloads))
	}
	var outcome struct {
		Kind            string                       `json:"kind"`
		Language        Language                     `json:"language"`
		FileName        string                       `json:"file_name"`
		ContractName    string                       `json:"contract_name"`
		ABI             []any                        `json:"abi"`
		Sources         map[string]map[string]string `json:"sources"`
		Settings        map[string]any               `json:"settings"`
		CreationMatch   *VerificationMatchDetails    `json:"creation_match"`
		RuntimeMatch    *VerificationMatchDetails    `json:"runtime_match"`
		Libraries       map[string]string            `json:"libraries"`
		CompilationData map[string]any               `json:"compilation_artifacts"`
	}
	if err := json.Unmarshal(repository.payloads[0], &outcome); err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != "verification_success" || outcome.Language != LanguageGeas ||
		outcome.FileName != "system/main.eas" || outcome.ContractName != "main" ||
		len(outcome.ABI) != 0 || len(outcome.Sources) != 3 || len(outcome.Libraries) != 0 ||
		len(outcome.CompilationData) != 0 || outcome.CreationMatch == nil || outcome.RuntimeMatch == nil ||
		outcome.CreationMatch.MatchType != VerificationMatchFull ||
		outcome.RuntimeMatch.MatchType != VerificationMatchFull || outcome.Settings["stack_check"] != true ||
		outcome.Settings["runtime_entrypoint"] != "system/main.eas" ||
		outcome.Settings["creation_entrypoint"] != "system/ctor.eas" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if _, exists := outcome.Sources["unused.eas"]; exists {
		t.Fatal("unused source was published")
	}
}

func TestWorkerPublishesGeasGenesisRuntimeOnlyVerification(t *testing.T) {
	request := validGeasSubmission()
	request.Geas.CreationEntrypoint = ""
	request.Bytecodes[0].Creation = ""
	request.Target.CreationBytecode = ""
	request.Target.GenesisPredeploy = true
	if err := (&Service{maxInputBytes: 5 << 20}).prepareV2(t.Context(), &request); err != nil {
		t.Fatal(err)
	}
	repository := &verifyMemoryRepository{
		claimFound: true,
		lease: VerificationLease{
			Job: VerificationJob{
				ID: verificationID(66), Kind: JobAddress, Status: JobRunning, RequestV2: &request,
			},
			Token: "geas-genesis-lease",
		},
	}
	compiler := &verifyTestGeasCompiler{
		provenance: testGeasProvenance(),
		responses: map[string]geascompiler.Response{
			"system/main.eas": {
				Schema: geascompiler.ProtocolSchema, Successful: true, Bytecode: "0x6001",
				Sources: []string{"common/value.eas", "system/main.eas"},
			},
		},
	}
	found, err := newVerifyTestWorker(t, repository, compiler).ProcessOne(t.Context())
	if err != nil || !found || strings.Join(compiler.calls, ",") != "system/main.eas" ||
		len(repository.outcomes) != 1 || repository.outcomes[0] != "verification_success" ||
		len(repository.payloads) != 1 {
		t.Fatalf("found=%t error=%v calls=%v outcomes=%v", found, err, compiler.calls, repository.outcomes)
	}
	var outcome struct {
		CreationMatch        *VerificationMatchDetails `json:"creation_match"`
		RuntimeMatch         *VerificationMatchDetails `json:"runtime_match"`
		ConstructorArguments string                    `json:"constructor_arguments"`
		Settings             map[string]any            `json:"settings"`
	}
	if err := json.Unmarshal(repository.payloads[0], &outcome); err != nil {
		t.Fatal(err)
	}
	if outcome.CreationMatch != nil || outcome.RuntimeMatch == nil ||
		outcome.RuntimeMatch.MatchType != VerificationMatchFull ||
		outcome.ConstructorArguments != "" || outcome.Settings["creation_entrypoint"] != nil {
		t.Fatalf("runtime-only Geas outcome=%+v", outcome)
	}
	assertNoCreationEvidence(t, repository.payloads[0])
}

func TestWorkerPublishesSolidityGenesisRuntimeOnlyVerification(t *testing.T) {
	runtime := []byte{0x60, 0x02}
	input := json.RawMessage(`{
		"language":"Solidity",
		"sources":{"Proxy.sol":{"content":"contract Proxy {}"}},
		"settings":{}
	}`)
	request := SubmissionV2{
		Kind: JobAddress, Language: LanguageSolidity,
		CompilerVersion: "0.8.30+commit.73712a01", StandardJSON: input,
		ContractNameHint: "Proxy.sol:Proxy",
		Bytecodes:        []BytecodePair{{Runtime: "0x" + hex.EncodeToString(runtime)}},
		Target: &VerificationTarget{
			ChainID: 1, Address: "0x" + strings.Repeat("11", 20),
			CodeHash:         "0x" + hex.EncodeToString(keccak256Bytes(runtime)),
			AtBlockHash:      "0x" + strings.Repeat("33", 32),
			RuntimeBytecode:  "0x" + hex.EncodeToString(runtime),
			GenesisPredeploy: true,
		},
	}
	if err := (&Service{maxInputBytes: 5 << 20}).prepareV2(t.Context(), &request); err != nil {
		t.Fatal(err)
	}
	repository := &verifyMemoryRepository{
		claimFound: true,
		lease: VerificationLease{
			Job: VerificationJob{
				ID: verificationID(67), Kind: JobAddress, Status: JobRunning, RequestV2: &request,
			},
			Token: "solidity-genesis-lease",
		},
	}
	compiler := &verifyTestCompiler{
		provenance: testSolcJSProvenance(),
		output: candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{
			"Proxy.sol": {"Proxy": {creation: []byte{0x60, 0x01}, runtime: runtime}},
		}),
	}
	found, err := newVerifyTestWorker(t, repository, compiler).ProcessOne(t.Context())
	if err != nil || !found || len(repository.outcomes) != 1 ||
		repository.outcomes[0] != "verification_success" || len(repository.payloads) != 1 {
		t.Fatalf("found=%t error=%v outcomes=%v", found, err, repository.outcomes)
	}
	var outcome struct {
		CreationMatch        *VerificationMatchDetails `json:"creation_match"`
		RuntimeMatch         *VerificationMatchDetails `json:"runtime_match"`
		ConstructorArguments string                    `json:"constructor_arguments"`
	}
	if err := json.Unmarshal(repository.payloads[0], &outcome); err != nil {
		t.Fatal(err)
	}
	if outcome.CreationMatch != nil || outcome.RuntimeMatch == nil ||
		outcome.RuntimeMatch.MatchType != VerificationMatchPartial ||
		outcome.ConstructorArguments != "" {
		t.Fatalf("runtime-only Solidity outcome=%+v", outcome)
	}
	if len(repository.compilations) != 1 ||
		len(repository.compilations[0].Candidates) != 1 ||
		repository.compilations[0].Candidates[0].FullyQualifiedName() != "Proxy.sol:Proxy" ||
		!json.Valid(repository.compilations[0].StandardJSON) {
		t.Fatalf("authenticated compilations = %+v", repository.compilations)
	}
	assertNoCreationEvidence(t, repository.payloads[0])
}

func TestWorkerPublishesYulGenesisRuntimeOnlyVerification(t *testing.T) {
	runtime, err := hex.DecodeString("7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe03601600081602082378035828234f58015156039578182fd5b8082525050506014600cf3")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{
		"language": "Yul",
		"sources": map[string]any{
			"deterministic-deployment-proxy.yul": map[string]string{
				"content": `object "Proxy" {
	code {
		let size := datasize("runtime")
		datacopy(0, dataoffset("runtime"), size)
		return(0, size)
	}
	object "runtime" {
		code {
			calldatacopy(0, 32, sub(calldatasize(), 32))
			let result := create2(callvalue(), 0, sub(calldatasize(), 32), calldataload(0))
			if iszero(result) { revert(0, 0) }
			mstore(0, result)
			return(12, 20)
		}
	}
}`,
			},
		},
		"settings": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := SubmissionV2{
		Kind: JobAddress, Language: LanguageYul,
		CompilerVersion: "0.5.8+commit.23d335f2", StandardJSON: input,
		ContractNameHint: "deterministic-deployment-proxy.yul:Proxy",
		Bytecodes:        []BytecodePair{{Runtime: "0x" + hex.EncodeToString(runtime)}},
		Target: &VerificationTarget{
			ChainID: 1, Address: "0x" + strings.Repeat("11", 20),
			CodeHash:         "0x" + hex.EncodeToString(keccak256Bytes(runtime)),
			AtBlockHash:      "0x" + strings.Repeat("33", 32),
			RuntimeBytecode:  "0x" + hex.EncodeToString(runtime),
			GenesisPredeploy: true,
		},
	}
	if err := (&Service{maxInputBytes: 5 << 20}).prepareV2(t.Context(), &request); err != nil {
		t.Fatal(err)
	}
	repository := &verifyMemoryRepository{
		claimFound: true,
		lease: VerificationLease{
			Job: VerificationJob{
				ID: verificationID(65), Kind: JobAddress, Status: JobRunning, RequestV2: &request,
			},
			Token: "yul-genesis-lease",
		},
	}
	compiler := &verifyTestCompiler{
		provenance: testSolcJSProvenance(),
		output:     solc058YulProxyCompilerOutput(t),
	}
	found, err := newVerifyTestWorker(t, repository, compiler).ProcessOne(t.Context())
	if err != nil || !found || len(repository.outcomes) != 1 ||
		repository.outcomes[0] != "verification_success" || len(repository.payloads) != 1 {
		t.Fatalf("found=%t error=%v outcomes=%v", found, err, repository.outcomes)
	}
	var outcome struct {
		Language             Language                  `json:"language"`
		FileName             string                    `json:"file_name"`
		ContractName         string                    `json:"contract_name"`
		CreationMatch        *VerificationMatchDetails `json:"creation_match"`
		RuntimeMatch         *VerificationMatchDetails `json:"runtime_match"`
		ConstructorArguments string                    `json:"constructor_arguments"`
	}
	if err := json.Unmarshal(repository.payloads[0], &outcome); err != nil {
		t.Fatal(err)
	}
	if outcome.Language != LanguageYul || outcome.FileName != "deterministic-deployment-proxy.yul" ||
		outcome.ContractName != "Proxy" || outcome.CreationMatch != nil ||
		outcome.RuntimeMatch == nil || outcome.RuntimeMatch.MatchType != VerificationMatchPartial ||
		outcome.ConstructorArguments != "" {
		t.Fatalf("runtime-only Yul outcome=%+v", outcome)
	}
	assertNoCreationEvidence(t, repository.payloads[0])
}

func assertNoCreationEvidence(t *testing.T, payload json.RawMessage) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["creation_match"]; exists {
		t.Fatalf("runtime-only outcome contains creation_match: %s", payload)
	}
	if _, exists := fields["constructor_arguments"]; exists {
		t.Fatalf("runtime-only outcome contains constructor_arguments: %s", payload)
	}
}

func TestWorkerClassifiesGeasCompilationAndMatchFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		response   geascompiler.Response
		compileErr error
		wantKind   string
		wantError  ErrorCode
	}{
		{
			name: "source diagnostics", response: geascompiler.Response{
				Schema: geascompiler.ProtocolSchema, Sources: []string{"system/main.eas"},
			}, wantKind: "compilation_failure",
		},
		{
			name: "bytecode mismatch", response: geascompiler.Response{
				Schema: geascompiler.ProtocolSchema, Successful: true, Bytecode: "0x6002",
				Sources: []string{"system/main.eas"},
			}, wantKind: "verification_failure",
		},
		{name: "nondeterministic helper", compileErr: ErrCompilerNondeterministic, wantError: ErrorCompilerOutput},
		{name: "bound helper changed", compileErr: ErrCompilerProvenanceConflict, wantError: ErrorCompilerProvenanceMismatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := validGeasSubmission()
			request.Geas.CreationEntrypoint = ""
			request.Bytecodes[0].Creation = ""
			request.Target.CreationBytecode = ""
			request.Target.GenesisPredeploy = true
			if err := (&Service{maxInputBytes: 5 << 20}).prepareV2(t.Context(), &request); err != nil {
				t.Fatal(err)
			}
			repository := &verifyMemoryRepository{
				claimFound: true,
				lease: VerificationLease{
					Job:   VerificationJob{ID: verificationID(64), Kind: JobAddress, Status: JobRunning, RequestV2: &request},
					Token: "geas-lease",
				},
			}
			compiler := &verifyTestGeasCompiler{
				provenance: testGeasProvenance(), err: test.compileErr,
				responses: map[string]geascompiler.Response{"system/main.eas": test.response},
			}
			found, err := newVerifyTestWorker(t, repository, compiler).ProcessOne(t.Context())
			if err != nil || !found {
				t.Fatalf("found=%t error=%v", found, err)
			}
			if test.wantKind != "" && (len(repository.outcomes) != 1 || repository.outcomes[0] != test.wantKind) {
				t.Fatalf("outcomes = %v", repository.outcomes)
			}
			if test.wantError != "" && (len(repository.failures) != 1 || repository.failures[0] != test.wantError) {
				t.Fatalf("failures = %v", repository.failures)
			}
		})
	}
}

func TestWorkerBindsSolcJSAndCompilesDeterminismInputsSeparately(t *testing.T) {
	lease := verifyV2Lease()
	repository := &verifyMemoryRepository{lease: lease, claimFound: true}
	output := candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{
		"A.sol": {"A": {creation: []byte{0x60, 0x01}, runtime: []byte{0x60, 0x01}}},
	})
	compiler := &verifyTestCompiler{
		provenance: testSolcJSProvenance(),
		output:     output,
	}
	worker := newVerifyTestWorker(t, repository, compiler)
	found, err := worker.ProcessOne(context.Background())
	if err != nil || !found {
		t.Fatalf("found=%t error=%v", found, err)
	}
	if compiler.compileCalls != 2 ||
		len(compiler.compileInputs) != 2 ||
		string(compiler.compileInputs[0]) == string(compiler.compileInputs[1]) {
		t.Fatalf("compile calls=%d inputs=%d", compiler.compileCalls, len(compiler.compileInputs))
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.bindings) != 1 ||
		repository.bindings[0].Platform != CompilerPlatformEmscriptenWASM32 ||
		len(repository.outcomes) != 1 ||
		repository.outcomes[0] != "verification_success" ||
		len(repository.compilations) != 0 ||
		len(repository.failures) != 0 {
		t.Fatalf(
			"bindings=%#v outcomes=%v failures=%v",
			repository.bindings, repository.outcomes, repository.failures,
		)
	}
}

func TestWorkerUsesStableCompilerFailures(t *testing.T) {
	tests := []struct {
		name      string
		resolve   error
		bind      error
		want      ErrorCode
		wantBound bool
	}{
		{
			name: "unsupported version", resolve: ErrCompilerVersionUnavailable,
			want: ErrorCompilerUnavailable,
		},
		{
			name: "executor changed", bind: ErrCompilerProvenanceConflict,
			want: ErrorCompilerProvenanceMismatch, wantBound: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &verifyMemoryRepository{
				lease: verifyV2Lease(), claimFound: true, bindError: test.bind,
			}
			compiler := &verifyTestCompiler{
				provenance:   testSolcJSProvenance(),
				resolveError: test.resolve,
			}
			worker := newVerifyTestWorker(t, repository, compiler)
			found, err := worker.ProcessOne(context.Background())
			if err != nil || !found {
				t.Fatalf("found=%t error=%v", found, err)
			}
			repository.mu.Lock()
			defer repository.mu.Unlock()
			if len(repository.failures) != 1 || repository.failures[0] != test.want ||
				(len(repository.bindings) == 1) != test.wantBound {
				t.Fatalf("failures=%v bindings=%v", repository.failures, repository.bindings)
			}
		})
	}
}

func TestLeaseHeartbeatPreservesCompilerError(t *testing.T) {
	repository := &verifyMemoryRepository{}
	worker := &Worker{
		repository: repository,
		options:    WorkerOptions{LeaseDuration: 3 * time.Millisecond},
	}
	want := errors.New("compiler operation failed")
	_, err := runWithLeaseHeartbeat(
		context.Background(), worker, verifyV2Lease(),
		func(context.Context) (struct{}, error) {
			return struct{}{}, want
		},
	)
	if !errors.Is(err, want) {
		t.Fatalf("heartbeat replaced operation error: %v", err)
	}
}

func TestVerificationServiceUsesStableNonSensitiveErrors(t *testing.T) {
	repository := &verifyMemoryRepository{
		submitErr: errors.New("postgres://admin:secret@database"),
	}
	service, err := NewService(repository, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	request := *verifyV2Lease().Job.RequestV2
	_, _, err = service.SubmitV2(context.Background(), request)
	var serviceError ServiceError
	if !errors.As(err, &serviceError) ||
		serviceError.Code != ServiceStorageFailure ||
		strings.Contains(err.Error(), "secret") {
		t.Fatalf("service error=%v", err)
	}
	request.Language = Language("vyper")
	_, _, err = service.SubmitV2(context.Background(), request)
	if !errors.As(err, &serviceError) ||
		serviceError.Code != ServiceInvalidRequest {
		t.Fatalf("unsupported language error=%v", err)
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.submits != 1 {
		t.Fatalf("invalid request reached repository; submits=%d", repository.submits)
	}
}
