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
	bindings   []CompilerProvenance
	bindError  error
	failures   []ErrorCode
	failError  error
	renewals   int
	renewed    chan struct{}

	submitJob     VerificationJob
	submitCreated bool
	submitError   error
	submitCalls   int
	submission    SubmissionOptions
	job           VerificationJob
	jobFound      bool
	jobError      error
	contract      VerifiedContract
	contractFound bool
	contractError error
}

func (repository *verifyMemoryRepository) Submit(_ context.Context, _ Request, options ...SubmissionOptions) (VerificationJob, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.submitCalls++
	if len(options) == 1 {
		repository.submission = options[0]
	}
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

func (repository *verifyMemoryRepository) BindCompiler(_ context.Context, _ VerificationLease, provenance CompilerProvenance) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.bindings = append(repository.bindings, provenance)
	return repository.bindError
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
	output          []byte
	err             error
	isolated        bool
	panic           bool
	started         chan struct{}
	release         chan struct{}
	cleaned         chan struct{}
	cancelCleanup   time.Duration
	cancelError     error
	once            sync.Once
	provenanceError error
}

func (compiler *verifyTestCompiler) Provenance(Language, string) (CompilerProvenance, error) {
	if compiler.provenanceError != nil {
		return CompilerProvenance{}, compiler.provenanceError
	}
	var digest [32]byte
	digest[0] = 1
	kind := CompilerProcess
	if compiler.isolated {
		kind = CompilerContainer
	}
	return CompilerProvenance{Kind: kind, Digest: digest, HardIsolated: compiler.isolated}, nil
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
			if compiler.cancelCleanup > 0 {
				timer := time.NewTimer(compiler.cancelCleanup)
				defer timer.Stop()
				<-timer.C
			}
			if compiler.cleaned != nil {
				close(compiler.cleaned)
				time.Sleep(25 * time.Millisecond)
			}
			if compiler.cancelError != nil {
				return nil, compiler.cancelError
			}
			return nil, ctx.Err()
		case <-compiler.release:
		}
	}
	return append([]byte(nil), compiler.output...), compiler.err
}

func (compiler *verifyTestCompiler) HardIsolated() bool { return compiler.isolated }

func verifyCompilerOutput(bytecode string) []byte {
	return []byte(fmt.Sprintf(
		`{"contracts":{"A.sol":{"A":{"abi":[],"metadata":"{}","evm":{"bytecode":{"object":%q,"linkReferences":{}},"deployedBytecode":{"object":%q,"linkReferences":{},"immutableReferences":{}}}}}}}`,
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

type recordingVerificationObserver struct{ results []string }

func (observer *recordingVerificationObserver) RecordVerificationJob(result string) {
	observer.results = append(observer.results, result)
}

func TestWorkerObservesPersistedVerificationOutcome(t *testing.T) {
	repository := &verifyMemoryRepository{lease: verifyLease(), claimFound: true}
	observer := &recordingVerificationObserver{}
	worker := newVerifyTestWorker(t, repository, &verifyTestCompiler{
		output: verifyCompilerOutput("6001"), isolated: true,
	}, WorkerOptions{Observer: observer})
	if found, err := worker.ProcessOne(t.Context()); err != nil || !found {
		t.Fatalf("found=%t error=%v", found, err)
	}
	if len(observer.results) != 1 || observer.results[0] != "succeeded" {
		t.Fatalf("verification observations=%v", observer.results)
	}
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

func TestWorkerWaitsForCompilerCleanupOnCancellation(t *testing.T) {
	repository := &verifyMemoryRepository{lease: verifyLease(), claimFound: true}
	compiler := &verifyTestCompiler{
		isolated: true, started: make(chan struct{}), release: make(chan struct{}),
		cleaned: make(chan struct{}), cancelCleanup: 25 * time.Millisecond,
	}
	worker := newVerifyTestWorker(t, repository, compiler, WorkerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := worker.ProcessOne(ctx)
		done <- err
	}()
	select {
	case <-compiler.started:
	case <-time.After(time.Second):
		t.Fatal("compiler did not start")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("worker returned before compiler cleanup: %v", err)
	case <-compiler.cleaned:
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not return after compiler cleanup")
	}
}

func TestWorkerStopsWithoutTerminalizingOnCompilerFatalInvariant(t *testing.T) {
	t.Run("compiler panic", func(t *testing.T) {
		repository := &verifyMemoryRepository{lease: verifyLease(), claimFound: true}
		worker := newVerifyTestWorker(t, repository, &verifyTestCompiler{panic: true}, WorkerOptions{})
		err := worker.Run(context.Background())
		if !errors.Is(err, ErrCompilerRuntime) || err.Error() != ErrCompilerRuntime.Error() || strings.Contains(err.Error(), "sensitive") {
			t.Fatalf("worker error=%v", err)
		}
		repository.mu.Lock()
		defer repository.mu.Unlock()
		if len(repository.failures) != 0 || len(repository.complete) != 0 {
			t.Fatalf("failures=%v completions=%d", repository.failures, len(repository.complete))
		}
	})

	t.Run("runtime sentinel", func(t *testing.T) {
		repository := &verifyMemoryRepository{lease: verifyLease(), claimFound: true}
		compiler := &verifyTestCompiler{isolated: true, err: ErrCompilerRuntime}
		worker := newVerifyTestWorker(t, repository, compiler, WorkerOptions{})
		err := worker.Run(context.Background())
		if !errors.Is(err, ErrCompilerRuntime) || err.Error() != ErrCompilerRuntime.Error() {
			t.Fatalf("worker error=%v", err)
		}
		repository.mu.Lock()
		defer repository.mu.Unlock()
		if len(repository.failures) != 0 || len(repository.complete) != 0 {
			t.Fatalf("failures=%v completions=%d", repository.failures, len(repository.complete))
		}
	})

	t.Run("compile failure cleanup", func(t *testing.T) {
		repository := &verifyMemoryRepository{lease: verifyLease(), claimFound: true}
		compiler := &verifyTestCompiler{isolated: true, err: ErrCompilerCleanup}
		worker := newVerifyTestWorker(t, repository, compiler, WorkerOptions{})
		if err := worker.Run(context.Background()); !errors.Is(err, ErrCompilerCleanup) {
			t.Fatalf("worker error=%v", err)
		}
		repository.mu.Lock()
		defer repository.mu.Unlock()
		if len(repository.failures) != 0 || len(repository.complete) != 0 {
			t.Fatalf("failures=%v completions=%d", repository.failures, len(repository.complete))
		}
	})

	t.Run("cancelled compile cleanup", func(t *testing.T) {
		repository := &verifyMemoryRepository{lease: verifyLease(), claimFound: true}
		compiler := &verifyTestCompiler{
			isolated: true, started: make(chan struct{}), release: make(chan struct{}),
			cancelError: ErrCompilerCleanup,
		}
		worker := newVerifyTestWorker(t, repository, compiler, WorkerOptions{})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := worker.ProcessOne(ctx)
			done <- err
		}()
		select {
		case <-compiler.started:
		case <-time.After(time.Second):
			t.Fatal("compiler did not start")
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, ErrCompilerCleanup) {
				t.Fatalf("worker error=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("worker did not return after failed cleanup")
		}
		repository.mu.Lock()
		defer repository.mu.Unlock()
		if len(repository.failures) != 0 || len(repository.complete) != 0 {
			t.Fatalf("failures=%v completions=%d", repository.failures, len(repository.complete))
		}
	})
}

func TestWorkerRequiresHardIsolationForPublicVerification(t *testing.T) {
	repository := &verifyMemoryRepository{}
	_, err := NewWorker(repository, &verifyTestCompiler{}, WorkerOptions{WorkerID: "worker", Public: true})
	if !errors.Is(err, ErrSandboxRequired) {
		t.Fatalf("error=%v", err)
	}
}

func TestWorkerEnforcesDurableIsolationAndCompilerProvenance(t *testing.T) {
	tests := []struct {
		name      string
		compiler  *verifyTestCompiler
		bindError error
		want      ErrorCode
	}{
		{
			name:     "public job on process worker",
			compiler: &verifyTestCompiler{output: verifyCompilerOutput("6001")},
			want:     ErrorSandboxRequired,
		},
		{
			name:     "compiler is not allowlisted",
			compiler: &verifyTestCompiler{provenanceError: errors.New("mutable compiler")},
			want:     ErrorCompilerUnavailable,
		},
		{
			name:      "reclaim changed compiler digest",
			compiler:  &verifyTestCompiler{output: verifyCompilerOutput("6001"), isolated: true},
			bindError: ErrCompilerProvenanceConflict,
			want:      ErrorCompilerProvenanceMismatch,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lease := verifyLease()
			if test.name == "public job on process worker" {
				lease.Job.RequiresHardIsolation = true
			}
			repository := &verifyMemoryRepository{
				lease: lease, claimFound: true, bindError: test.bindError,
			}
			worker := newVerifyTestWorker(t, repository, test.compiler, WorkerOptions{})
			found, err := worker.ProcessOne(context.Background())
			if err != nil || !found {
				t.Fatalf("found=%v error=%v", found, err)
			}
			repository.mu.Lock()
			defer repository.mu.Unlock()
			if len(repository.failures) != 1 || repository.failures[0] != test.want || len(repository.complete) != 0 {
				t.Fatalf("failures=%v completions=%d", repository.failures, len(repository.complete))
			}
		})
	}
}

func TestWorkerTreatsStaleCanonicalCompletionAsHandledTerminal(t *testing.T) {
	repository := &verifyMemoryRepository{
		lease: verifyLease(), claimFound: true, completeEr: ErrTargetNotCanonical,
	}
	compiler := &verifyTestCompiler{output: verifyCompilerOutput("6001"), isolated: true}
	worker := newVerifyTestWorker(t, repository, compiler, WorkerOptions{})
	found, err := worker.ProcessOne(context.Background())
	if err != nil || !found {
		t.Fatalf("found=%v error=%v", found, err)
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

func TestVerificationServicePersistsServerIsolationPolicy(t *testing.T) {
	repository := &verifyMemoryRepository{submitJob: VerificationJob{ID: verificationID(1)}, submitCreated: true}
	service, err := NewService(repository, 64<<10, ServiceOptions{RequiresHardIsolation: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Submit(context.Background(), validVerifyRequest()); err != nil {
		t.Fatal(err)
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if !repository.submission.RequiresHardIsolation {
		t.Fatal("public service did not persist the server-derived hard-isolation requirement")
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

func TestBuildCompletionClassifiesMalformedVyperAuxdataAsCompilerOutput(t *testing.T) {
	t.Parallel()
	request := validVerifyRequest()
	request.Language = LanguageVyper
	request.CompilerVersion = "0.4.3"
	request.ContractIdentifier = vyperFixtureIdentifier
	request.StandardJSON = readCompilerJSONFixture(t, "vyper", "input.metadata.json")
	request.CreationBytecode = readCompilerHexFixture(t, "vyper", "creation.compiled.metadata.hex")
	request.RuntimeBytecode = readCompilerHexFixture(t, "vyper", "runtime.onchain.synthetic.hex")

	// This layout is internally well-formed, so extraction succeeds, but its
	// immutable size contradicts the authenticated compiler auxdata tuple.
	output := replaceFixtureTargetField(
		t,
		readCompilerJSONFixture(t, "vyper", "output.metadata.json"),
		[]string{"layout"},
		map[string]any{"code_layout": map[string]any{
			"owner": map[string]any{"offset": 0, "length": 32, "type": "address"},
		}},
	)
	if _, code := buildCompletion(request, output, 64<<10); code != ErrorCompilerOutput {
		t.Fatalf("code=%q, want %q", code, ErrorCompilerOutput)
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
