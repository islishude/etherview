package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	complete   []Completion
	completeEr error
	failures   []ErrorCode
	failError  error
	renewals   int
	renewed    chan struct{}

	submitJob     VerificationJob
	submitCreated bool
	submitError   error
	submitCalls   int
	job           VerificationJob
	jobFound      bool
	jobError      error
	contract      VerifiedContract
	contractFound bool
	contractError error
}

func (repository *verifyMemoryRepository) Submit(_ context.Context, _ Request) (VerificationJob, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.submitCalls++
	return repository.submitJob, repository.submitCreated, repository.submitError
}

func (repository *verifyMemoryRepository) Claim(context.Context, string, time.Duration) (VerificationLease, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.lease, repository.claimFound, repository.claimError
}

func (repository *verifyMemoryRepository) Renew(context.Context, VerificationLease, time.Duration) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.renewals++
	if repository.renewed != nil {
		select {
		case repository.renewed <- struct{}{}:
		default:
		}
	}
	return repository.renewError
}

func (repository *verifyMemoryRepository) Complete(_ context.Context, _ VerificationLease, completion Completion) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.complete = append(repository.complete, completion)
	return repository.completeEr
}

func (repository *verifyMemoryRepository) Fail(_ context.Context, _ VerificationLease, code ErrorCode) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.failures = append(repository.failures, code)
	return repository.failError
}

func (repository *verifyMemoryRepository) Job(context.Context, string) (VerificationJob, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.job, repository.jobFound, repository.jobError
}

func (repository *verifyMemoryRepository) VerifiedContract(context.Context, uint64, string, string) (VerifiedContract, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.contract, repository.contractFound, repository.contractError
}

type verifyTestCompiler struct {
	output   []byte
	err      error
	isolated bool
	panic    bool
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (compiler *verifyTestCompiler) Compile(ctx context.Context, _ Language, _ string, _ []byte) ([]byte, error) {
	if compiler.panic {
		panic("sensitive compiler panic")
	}
	if compiler.started != nil {
		compiler.once.Do(func() { close(compiler.started) })
	}
	if compiler.release != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-compiler.release:
		}
	}
	return append([]byte(nil), compiler.output...), compiler.err
}

func (compiler *verifyTestCompiler) HardIsolated() bool { return compiler.isolated }

func verifyCompilerOutput(bytecode string) []byte {
	return []byte(fmt.Sprintf(
		`{"contracts":{"A.sol":{"A":{"abi":[],"metadata":"{}","evm":{"bytecode":{"object":%q},"deployedBytecode":{"object":%q}}}}}}`,
		bytecode,
		bytecode,
	))
}

func verifyLease() VerificationLease {
	return VerificationLease{
		Job:   VerificationJob{ID: verificationID(1), Request: validVerifyRequest(), Status: JobRunning},
		Token: "lease-token",
	}
}

func newVerifyTestWorker(t *testing.T, repository Repository, compiler Compiler, options WorkerOptions) *Worker {
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

func TestWorkerPersistsExactAndMismatchWithoutLeakingArtifact(t *testing.T) {
	tests := []struct {
		name          string
		bytecode      string
		wantKind      MatchKind
		wantPublished bool
	}{
		{name: "exact", bytecode: "6001", wantKind: MatchExact, wantPublished: true},
		{name: "mismatch", bytecode: "6002", wantKind: MatchMismatch, wantPublished: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &verifyMemoryRepository{lease: verifyLease(), claimFound: true}
			compiler := &verifyTestCompiler{output: verifyCompilerOutput(test.bytecode), isolated: true}
			worker := newVerifyTestWorker(t, repository, compiler, WorkerOptions{})
			found, err := worker.ProcessOne(context.Background())
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			repository.mu.Lock()
			defer repository.mu.Unlock()
			if len(repository.complete) != 1 || len(repository.failures) != 0 {
				t.Fatalf("completions=%#v failures=%v", repository.complete, repository.failures)
			}
			completion := repository.complete[0]
			if completion.Kind != test.wantKind {
				t.Fatalf("kind=%q, want %q", completion.Kind, test.wantKind)
			}
			published := len(completion.Artifact.ABI) != 0 || len(completion.Sources) != 0 || len(completion.Settings) != 0
			if published != test.wantPublished {
				t.Fatalf("published material=%v, want %v; completion=%#v", published, test.wantPublished, completion)
			}
		})
	}
}

func TestWorkerPersistsOnlyStableFailureCodes(t *testing.T) {
	tests := []struct {
		name     string
		compiler *verifyTestCompiler
		limit    int
		want     ErrorCode
	}{
		{name: "compile failure", compiler: &verifyTestCompiler{err: errors.New("password=secret")}, want: ErrorCompileFailed},
		{name: "compiler panic", compiler: &verifyTestCompiler{panic: true}, want: ErrorCompileFailed},
		{name: "invalid output", compiler: &verifyTestCompiler{output: []byte(`{"private":"source"}`)}, want: ErrorCompilerOutput},
		{name: "oversized output", compiler: &verifyTestCompiler{output: verifyCompilerOutput("6001")}, limit: 1, want: ErrorCompilerTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &verifyMemoryRepository{lease: verifyLease(), claimFound: true}
			options := WorkerOptions{MaxOutputBytes: test.limit}
			if test.limit == 0 {
				options.MaxOutputBytes = 64 << 10
			}
			worker := newVerifyTestWorker(t, repository, test.compiler, options)
			found, err := worker.ProcessOne(context.Background())
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			repository.mu.Lock()
			defer repository.mu.Unlock()
			if len(repository.failures) != 1 || repository.failures[0] != test.want || len(repository.complete) != 0 {
				t.Fatalf("failures=%v completions=%#v", repository.failures, repository.complete)
			}
			if strings.Contains(string(repository.failures[0]), "secret") || strings.Contains(string(repository.failures[0]), "private") {
				t.Fatalf("failure code leaked compiler data: %q", repository.failures[0])
			}
		})
	}
}

func TestWorkerRenewsLeaseDuringCompilation(t *testing.T) {
	repository := &verifyMemoryRepository{
		lease:      verifyLease(),
		claimFound: true,
		renewed:    make(chan struct{}, 1),
	}
	compiler := &verifyTestCompiler{
		output:   verifyCompilerOutput("6001"),
		isolated: true,
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	worker := newVerifyTestWorker(t, repository, compiler, WorkerOptions{LeaseDuration: 30 * time.Millisecond})
	done := make(chan error, 1)
	go func() {
		_, err := worker.ProcessOne(context.Background())
		done <- err
	}()
	select {
	case <-compiler.started:
	case <-time.After(time.Second):
		t.Fatal("compiler did not start")
	}
	select {
	case <-repository.renewed:
	case <-time.After(time.Second):
		t.Fatal("worker did not renew its lease")
	}
	close(compiler.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not complete after compiler release")
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.renewals < 1 || len(repository.complete) != 1 {
		t.Fatalf("renewals=%d completions=%d", repository.renewals, len(repository.complete))
	}
}

func TestWorkerRequiresHardIsolationForPublicVerification(t *testing.T) {
	repository := &verifyMemoryRepository{}
	_, err := NewWorker(repository, &verifyTestCompiler{}, WorkerOptions{WorkerID: "worker", Public: true})
	if !errors.Is(err, ErrSandboxRequired) {
		t.Fatalf("error=%v", err)
	}
}

func TestVerificationServiceUsesStableNonSensitiveErrors(t *testing.T) {
	request := validVerifyRequest()
	repository := &verifyMemoryRepository{submitError: errors.New("postgres://admin:secret@database")}
	service, err := NewService(repository, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.Submit(context.Background(), request)
	var serviceError ServiceError
	if !errors.As(err, &serviceError) || serviceError.Code != ServiceStorageFailure {
		t.Fatalf("error=%#v", err)
	}
	if strings.Contains(err.Error(), "secret") || err.Error() != "verification storage unavailable" {
		t.Fatalf("service leaked storage details: %q", err)
	}

	invalid := request
	invalid.Address = "0x12"
	_, _, err = service.Submit(context.Background(), invalid)
	if !errors.As(err, &serviceError) || serviceError.Code != ServiceInvalidRequest || err.Error() != "invalid verification request" {
		t.Fatalf("invalid request error=%#v", err)
	}
	repository.mu.Lock()
	if repository.submitCalls != 1 {
		t.Fatalf("invalid request reached repository; submit calls=%d", repository.submitCalls)
	}
	repository.mu.Unlock()

	if _, _, err := service.Job(context.Background(), "not-a-uuid"); !errors.As(err, &serviceError) || serviceError.Code != ServiceInvalidRequest {
		t.Fatalf("invalid job error=%#v", err)
	}
	if _, _, err := service.VerifiedContract(context.Background(), 0, request.Address, request.CodeHash); !errors.As(err, &serviceError) || serviceError.Code != ServiceInvalidRequest {
		t.Fatalf("invalid contract error=%#v", err)
	}
}

func TestBuildCompletionRequiresObjectSourcesAndArrayABI(t *testing.T) {
	request := validVerifyRequest()
	request.StandardJSON = json.RawMessage(`{"sources":[],"settings":{}}`)
	if _, code := buildCompletion(request, verifyCompilerOutput("6001"), 64<<10); code != ErrorCompilerOutput {
		t.Fatalf("code=%q", code)
	}
	request = validVerifyRequest()
	output := []byte(`{"contracts":{"A.sol":{"A":{"abi":{},"evm":{"bytecode":{"object":"6001"},"deployedBytecode":{"object":"6001"}}}}}}`)
	if _, code := buildCompletion(request, output, 64<<10); code != ErrorCompilerOutput {
		t.Fatalf("code=%q", code)
	}
}

func TestBuildCompletionPersistsEtherscanMetadataOutsideCompilerInput(t *testing.T) {
	t.Parallel()
	request := validVerifyRequest()
	request.ConstructorArgs = "aabb"
	request.LicenseType = "3"
	originalInput := append(json.RawMessage(nil), request.StandardJSON...)
	completion, code := buildCompletion(request, verifyCompilerOutput("6001"), 64<<10)
	if code != "" || completion.Kind != MatchExact {
		t.Fatalf("completion=%#v code=%q", completion, code)
	}
	var settings map[string]any
	if err := json.Unmarshal(completion.Settings, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["constructorArguments"] != "aabb" || settings["licenseType"] != "3" {
		t.Fatalf("settings=%s", completion.Settings)
	}
	if string(request.StandardJSON) != string(originalInput) || strings.Contains(string(request.StandardJSON), "constructorArguments") || strings.Contains(string(request.StandardJSON), "licenseType") {
		t.Fatalf("compiler input was mutated: %s", request.StandardJSON)
	}
}
