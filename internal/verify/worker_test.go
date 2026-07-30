package verify

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type verifyMemoryRepository struct {
	mu sync.Mutex

	lease      VerificationLease
	claimFound bool
	claimError error
	renewError error
	bindError  error
	bindings   []CompilerProvenance
	failures   []ErrorCode
	outcomes   []string
	submitJob  VerificationJob
	submitErr  error
	submits    int
	job        VerificationJob
	jobFound   bool
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
	_ json.RawMessage,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.outcomes = append(repository.outcomes, kind)
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
	options WorkerOptions,
) *Worker {
	t.Helper()
	if options.WorkerID == "" {
		options.WorkerID = "test-worker"
	}
	worker, err := NewWorker(repository, compiler, options)
	if err != nil {
		t.Fatal(err)
	}
	return worker
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
	worker := newVerifyTestWorker(t, repository, compiler, WorkerOptions{})
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
			worker := newVerifyTestWorker(t, repository, compiler, WorkerOptions{})
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
