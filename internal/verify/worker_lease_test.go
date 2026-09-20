package verify

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/components"
)

type leaseRecoveryRepository struct {
	verifyMemoryRepository
	calls     int
	completed chan struct{}
	failure   error
}

func (r *leaseRecoveryRepository) ClaimRunnable(ctx context.Context, _ string, _ time.Duration, _ CompilerAvailability) (VerificationLease, bool, error) {
	r.calls++
	if r.calls > 2 {
		<-ctx.Done()
		return VerificationLease{}, false, ctx.Err()
	}
	return VerificationLease{Job: VerificationJob{RequestV2: &SubmissionV2{Kind: JobProxy}}}, true, nil
}
func (r *leaseRecoveryRepository) CompleteProxyV2(context.Context, VerificationLease) error {
	if r.calls == 1 {
		return r.failure
	}
	close(r.completed)
	return nil
}

type verificationPeer struct{ started chan context.Context }

func (*verificationPeer) Name() string { return "api-peer" }
func (p *verificationPeer) Run(ctx context.Context) error {
	p.started <- ctx
	<-ctx.Done()
	return ctx.Err()
}

func TestWorkerLeaseLossKeepsSupervisorAndPeersRunning(t *testing.T) {
	repository := &leaseRecoveryRepository{failure: ErrLeaseLost, completed: make(chan struct{})}
	worker := newVerifyTestWorker(t, repository, &verifyTestCompiler{})
	worker.options.PollInterval = time.Millisecond
	peer := &verificationPeer{started: make(chan context.Context, 1)}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- components.Run(ctx, []components.Service{worker, peer}) }()
	peerContext := <-peer.started
	select {
	case <-repository.completed:
	case err := <-done:
		t.Fatalf("supervisor exited: %v", err)
	case <-time.After(time.Second):
		t.Fatal("next task did not complete")
	}
	if peerContext.Err() != nil {
		t.Fatal("lease loss cancelled API peer")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWorkerDoesNotHideFatalOrUnknownErrors(t *testing.T) {
	for _, failure := range []error{errors.New("database failure"), errors.Join(ErrLeaseLost, errors.New("database failure")), errors.Join(ErrLeaseLost, ErrCompilerCleanup), errors.Join(ErrLeaseLost, ErrCompilerRuntime)} {
		t.Run(failure.Error(), func(t *testing.T) {
			repository := &leaseRecoveryRepository{failure: failure, completed: make(chan struct{})}
			worker := newVerifyTestWorker(t, repository, &verifyTestCompiler{})
			if err := worker.Run(t.Context()); !errors.Is(err, failure) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestHeartbeatPreservesFatalCleanupAlongsideLeaseLoss(t *testing.T) {
	repository := &verifyMemoryRepository{renewError: ErrLeaseLost}
	worker := newVerifyTestWorker(t, repository, &verifyTestCompiler{})
	worker.options.LeaseDuration = 3 * time.Millisecond
	_, err := runWithLeaseHeartbeat(t.Context(), worker, VerificationLease{}, func(ctx context.Context) (struct{}, error) {
		<-ctx.Done()
		return struct{}{}, ErrCompilerCleanup
	})
	if !errors.Is(err, ErrLeaseLost) || !errors.Is(err, ErrCompilerCleanup) {
		t.Fatalf("lost error: %v", err)
	}
}

func TestWorkerDoesNotFailTaskAfterLosingLeaseDuringResolutionOrBinding(t *testing.T) {
	for _, phase := range []string{"resolve", "bind"} {
		t.Run(phase, func(t *testing.T) {
			repository := &verifyMemoryRepository{lease: verifyV2Lease(), claimFound: true}
			compiler := &verifyTestCompiler{}
			if phase == "resolve" {
				compiler.resolveError = ErrLeaseLost
			} else {
				repository.bindError = ErrLeaseLost
			}
			worker := newVerifyTestWorker(t, repository, compiler)
			_, err := worker.ProcessOne(t.Context())
			if !errors.Is(err, ErrLeaseLost) || len(repository.failures) != 0 {
				t.Fatalf("err=%v failures=%v", err, repository.failures)
			}
		})
	}
}

type phasedLeaseRepository struct {
	verifyMemoryRepository
	phase       string
	claims      int
	failures    int
	completions int
	done        chan struct{}
}

func (r *phasedLeaseRepository) ClaimRunnable(ctx context.Context, _ string, _ time.Duration, _ CompilerAvailability) (VerificationLease, bool, error) {
	r.claims++
	if r.claims == 1 {
		return verifyV2Lease(), true, nil
	}
	if r.claims == 2 {
		return VerificationLease{Job: VerificationJob{RequestV2: &SubmissionV2{Kind: JobProxy}}}, true, nil
	}
	<-ctx.Done()
	return VerificationLease{}, false, ctx.Err()
}
func (r *phasedLeaseRepository) Renew(context.Context, VerificationLease, time.Duration) error {
	if r.phase == "renew" {
		return ErrLeaseLost
	}
	return nil
}
func (r *phasedLeaseRepository) Fail(context.Context, VerificationLease, ErrorCode) error {
	r.failures++
	return ErrLeaseLost
}
func (r *phasedLeaseRepository) CompleteV2(context.Context, VerificationLease, string, json.RawMessage, ...AuthenticatedCompilation) error {
	r.completions++
	return ErrLeaseLost
}
func (r *phasedLeaseRepository) CompleteProxyV2(context.Context, VerificationLease) error {
	close(r.done)
	return nil
}

type renewingTestCompiler struct{ verifyTestCompiler }

func (compiler *renewingTestCompiler) Resolve(ctx context.Context, _ Language, _ string) (CompilerProvenance, error) {
	<-ctx.Done()
	return CompilerProvenance{}, ctx.Err()
}

func TestWorkerContinuesAfterRenewCompleteAndFailureLeaseLoss(t *testing.T) {
	for _, phase := range []string{"renew", "complete", "fail"} {
		t.Run(phase, func(t *testing.T) {
			repository := &phasedLeaseRepository{phase: phase, done: make(chan struct{})}
			compiler := &verifyTestCompiler{provenance: testSolcJSProvenance(), output: candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{"A.sol": {"A": {creation: []byte{0x60, 0x01}, runtime: []byte{0x60, 0x01}}}})}
			var engine Compiler = compiler
			if phase == "renew" {
				engine = &renewingTestCompiler{}
			}
			if phase == "fail" {
				compiler.resolveError = ErrCompilerVersionUnavailable
			}
			worker := newVerifyTestWorker(t, repository, engine)
			worker.options.PollInterval = time.Millisecond
			worker.options.LeaseDuration = 3 * time.Millisecond
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			finished := make(chan error, 1)
			go func() { finished <- worker.Run(ctx) }()
			select {
			case <-repository.done:
			case err := <-finished:
				t.Fatalf("lease loss stopped worker: %v", err)
			case <-time.After(time.Second):
				t.Fatal("next job was not processed")
			}
			cancel()
			if err := <-finished; !errors.Is(err, context.Canceled) {
				t.Fatalf("parent cancellation=%v", err)
			}
			if phase == "renew" && (repository.failures != 0 || repository.completions != 0) {
				t.Fatal("worker wrote after renewal lost ownership")
			}
			if phase == "complete" && (repository.completions != 1 || repository.failures != 0) {
				t.Fatal("completion loss was followed by a failure write")
			}
			if phase == "fail" && repository.failures != 1 {
				t.Fatal("failure transition was retried after lease loss")
			}
		})
	}
}
