package enrich

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/stagecontract"
)

var ErrAtomicPublicationRequired = errors.New("successful enrichment output requires lease-fenced atomic publication")

type StageID = stagecontract.ID

// Job identifies enrichment for one immutable block hash. Number is useful for
// scheduling but deliberately does not participate in identity.
type Job struct {
	ID          string
	Stage       StageID
	ChainID     string
	BlockHash   common.Hash
	BlockNumber uint64
	Attempt     uint32
	MaxAttempts uint32
	// Generation is the durable replay generation claimed by this attempt.
	// Zero is accepted for direct, non-queue processor fixtures; production
	// PostgreSQL claims always return a positive generation.
	Generation  uint64
	publication *stagePublication
}

func (job Job) Validate() error {
	if job.ID == "" {
		return errors.New("job ID is empty")
	}
	if err := job.Stage.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(job.ChainID) == "" {
		return errors.New("job chain ID is empty")
	}
	if job.BlockHash == (common.Hash{}) {
		return errors.New("job block hash is zero")
	}
	return nil
}

// IdempotencyKey is stable across retries and worker processes. A stage version
// bump intentionally changes the key.
func (job Job) IdempotencyKey() (string, error) {
	if err := job.Validate(); err != nil {
		return "", err
	}
	digest := sha256.Sum256(fmt.Appendf(nil,
		"%s\x00%d\x00%s\x00%s",
		job.Stage.Name,
		job.Stage.Version,
		job.ChainID,
		job.BlockHash.String(),
	))
	return hex.EncodeToString(digest[:]), nil
}

type ResultState string

const (
	ResultComplete    ResultState = "complete"
	ResultUnavailable ResultState = "unavailable"
	ResultFailed      ResultState = "failed"
)

type StageResult struct {
	State       ResultState       `json:"state"`
	Details     map[string]string `json:"details,omitempty"`
	Error       string            `json:"error,omitempty"`
	publication stagePublicationOutcome
	diagnostic  StageDiagnostic
}

// StageDiagnostic contains bounded operational context only. It deliberately
// carries no RPC parameters, response bytes, or nested error text.
type StageDiagnostic struct {
	Code             string
	Phase            string
	Endpoint         string
	TransactionHash  common.Hash
	TransactionIndex uint64
	HasTransaction   bool
}

type stageDiagnosticError struct {
	err        error
	diagnostic StageDiagnostic
}

func (err stageDiagnosticError) Error() string { return err.err.Error() }
func (err stageDiagnosticError) Unwrap() error { return err.err }

func withStageDiagnostic(err error, diagnostic StageDiagnostic) error {
	if err == nil {
		return nil
	}
	if existing, ok := errors.AsType[stageDiagnosticError](err); ok {
		if diagnostic.Code == "" {
			diagnostic.Code = existing.diagnostic.Code
		}
		if diagnostic.Phase == "" {
			diagnostic.Phase = existing.diagnostic.Phase
		}
		if diagnostic.Endpoint == "" {
			diagnostic.Endpoint = existing.diagnostic.Endpoint
		}
		if !diagnostic.HasTransaction && existing.diagnostic.HasTransaction {
			diagnostic.TransactionHash = existing.diagnostic.TransactionHash
			diagnostic.TransactionIndex = existing.diagnostic.TransactionIndex
			diagnostic.HasTransaction = true
		}
	}
	return stageDiagnosticError{err: err, diagnostic: diagnostic}
}

func (result StageResult) validateForFinish() error {
	switch result.State {
	case ResultComplete:
		if result.Error != "" {
			return errors.New("complete stage result contains an error")
		}
	case ResultUnavailable, ResultFailed:
		if strings.TrimSpace(result.Error) == "" {
			return fmt.Errorf("%s stage result requires a reason", result.State)
		}
	default:
		return fmt.Errorf("invalid stage result state %q", result.State)
	}
	return nil
}

// Lease is an opaque claim token. Durable stores must compare Token on renew,
// finish, and retry so an expired worker cannot overwrite a newer attempt.
type Lease struct {
	Job       Job
	Token     string
	heartbeat *leaseHeartbeatGuard
}

type leaseHeartbeatGuard struct {
	mu       sync.Mutex
	finished bool
}

type Retry struct {
	Reason string
	After  time.Duration
}

// JobQueue is the correctness boundary for durable enrichment scheduling.
// Implementations normally use PostgreSQL row leases and a unique constraint
// over Job.IdempotencyKey. Optional brokers may only wake Claim calls.
type JobQueue interface {
	Claim(ctx context.Context, workerID string, stages []StageID, leaseFor time.Duration) (Lease, bool, error)
	Renew(ctx context.Context, lease Lease, leaseFor time.Duration) error
	Finish(ctx context.Context, lease Lease, result StageResult) error
	Retry(ctx context.Context, lease Lease, retry Retry) error
}

type Processor interface {
	Stage() StageID
	Process(context.Context, Job) (StageResult, error)
}

// leaseProcessor is the production PostgreSQL execution path. Process remains
// available for direct processor fixtures, but a PostgreSQL worker refuses a
// processor that cannot publish success through this lease-aware boundary.
type leaseProcessor interface {
	Processor
	ProcessLease(context.Context, Lease, *PostgresJobQueue) (StageResult, error)
}

type ProcessorFunc struct {
	ID StageID
	Fn func(context.Context, Job) (StageResult, error)
}

func (processor ProcessorFunc) Stage() StageID { return processor.ID }

func (processor ProcessorFunc) Process(ctx context.Context, job Job) (StageResult, error) {
	if processor.Fn == nil {
		return StageResult{}, errors.New("nil stage processor function")
	}
	return processor.Fn(ctx, job)
}

type stageError struct {
	kind string
	err  error
}

func (err stageError) Error() string { return err.err.Error() }
func (err stageError) Unwrap() error { return err.err }

// Unavailable reports a capability-level absence (for example no trace RPC),
// which is terminal for this job but is not an empty successful result.
func Unavailable(err error) error {
	if err == nil {
		err = errors.New("capability unavailable")
	}
	return stageError{kind: "unavailable", err: err}
}

// Permanent reports invalid source data or another non-retryable failure.
func Permanent(err error) error {
	if err == nil {
		err = errors.New("permanent enrichment failure")
	}
	return stageError{kind: "permanent", err: err}
}

type WorkerOptions struct {
	ServiceName   string
	ID            string
	LeaseDuration time.Duration
	PollInterval  time.Duration
	RetryBase     time.Duration
	RetryMax      time.Duration
	// Wake is a lossy latency hint. PostgreSQL Claim remains authoritative and
	// PollInterval remains the mandatory fallback when notifications are lost.
	Wake     <-chan struct{}
	Observer JobObserver
}

type JobEvent string

const (
	JobEventStarted         JobEvent = "started"
	JobEventTransitioned    JobEvent = "transitioned"
	JobEventExecutionFailed JobEvent = "execution_failed"
)

type JobTransition struct {
	Event      JobEvent
	Component  string
	WorkerID   string
	Job        Job
	JobStatus  string
	StageState ResultState
	Result     string
	Code       string
	RetryAfter time.Duration
	Duration   time.Duration
	Details    map[string]string
	Diagnostic StageDiagnostic
}

// JobObserver receives only controlled job identity, result, and diagnostic
// fields. Implementations never receive processor or storage error text.
type JobObserver interface {
	RecordEnrichmentJob(JobTransition)
}

func (options *WorkerOptions) defaults() {
	if options.ServiceName == "" {
		options.ServiceName = "enrichment-worker"
	}
	if options.LeaseDuration <= 0 {
		options.LeaseDuration = 30 * time.Second
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
	if options.RetryBase <= 0 {
		options.RetryBase = time.Second
	}
	if options.RetryMax <= 0 {
		options.RetryMax = 5 * time.Minute
	}
}

type Worker struct {
	queue      JobQueue
	options    WorkerOptions
	stages     []StageID
	processors map[string]Processor
	publisher  *PostgresJobQueue
}

func (worker *Worker) Name() string {
	if worker == nil || worker.options.ServiceName == "" {
		return "enrichment-worker"
	}
	return worker.options.ServiceName
}

func NewWorker(queue JobQueue, processors []Processor, options WorkerOptions) (*Worker, error) {
	if queue == nil {
		return nil, errors.New("enrichment worker requires a job queue")
	}
	if strings.TrimSpace(options.ID) == "" {
		return nil, errors.New("enrichment worker ID is empty")
	}
	options.defaults()
	if options.LeaseDuration < 3*time.Millisecond {
		return nil, errors.New("lease duration must be at least 3ms")
	}
	if options.RetryMax < options.RetryBase {
		return nil, errors.New("maximum retry delay is less than base delay")
	}
	if len(processors) == 0 {
		return nil, errors.New("enrichment worker has no processors")
	}
	worker := &Worker{
		queue:      queue,
		options:    options,
		processors: make(map[string]Processor, len(processors)),
	}
	if publisher, ok := queue.(*PostgresJobQueue); ok {
		worker.publisher = publisher
	}
	for _, processor := range processors {
		if processor == nil {
			return nil, errors.New("enrichment worker contains a nil processor")
		}
		stage := processor.Stage()
		if err := stage.Validate(); err != nil {
			return nil, fmt.Errorf("processor stage: %w", err)
		}
		key := stage.String()
		if _, exists := worker.processors[key]; exists {
			return nil, fmt.Errorf("duplicate processor for stage %s", key)
		}
		worker.processors[key] = processor
		if worker.publisher != nil && isKnownDerivedStage(stage) {
			if _, ok := processor.(leaseProcessor); !ok {
				return nil, fmt.Errorf("PostgreSQL processor %s does not support lease-fenced atomic publication", key)
			}
		}
		worker.stages = append(worker.stages, stage)
	}
	sort.Slice(worker.stages, func(left, right int) bool {
		if worker.stages[left].Name == worker.stages[right].Name {
			return worker.stages[left].Version < worker.stages[right].Version
		}
		return worker.stages[left].Name < worker.stages[right].Name
	})
	return worker, nil
}

// Run continuously claims durable jobs. It returns on cancellation or on a
// queue/lease error; supervisors can then stop peer services consistently.
func (worker *Worker) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		found, err := worker.ProcessOne(ctx)
		if err != nil {
			return err
		}
		if !found {
			if err := waitContextOrWake(ctx, worker.options.PollInterval, worker.options.Wake); err != nil {
				return err
			}
		}
	}
}

// ProcessOne claims and processes at most one durable job. It is the same path
// used by Run and exists so recovery/idempotency tests and bounded supervisors
// can exercise the production lease contract without timing a polling loop.
func (worker *Worker) ProcessOne(ctx context.Context) (bool, error) {
	if worker == nil || worker.queue == nil {
		return false, errors.New("process enrichment job using nil worker")
	}
	lease, found, err := worker.queue.Claim(ctx, worker.options.ID, worker.stages, worker.options.LeaseDuration)
	if err != nil {
		return false, fmt.Errorf("claim enrichment job: %w", err)
	}
	if !found {
		return false, nil
	}
	if err := lease.Job.Validate(); err != nil {
		return true, fmt.Errorf("queue returned invalid enrichment job: %w", err)
	}
	if lease.Token == "" {
		return true, errors.New("queue returned an empty lease token")
	}
	processor := worker.processors[lease.Job.Stage.String()]
	if processor == nil {
		return true, fmt.Errorf("queue returned unsupported stage %s", lease.Job.Stage)
	}
	startedAt := time.Now()
	worker.observe(JobTransition{
		Event: JobEventStarted, Component: worker.Name(), WorkerID: worker.options.ID,
		Job: lease.Job,
	})
	if err := worker.handle(ctx, lease, processor, startedAt); err != nil {
		transition := JobTransition{
			Event: JobEventExecutionFailed, Component: worker.Name(), WorkerID: worker.options.ID,
			Job: lease.Job, Result: "error", Code: "execution_failed", Duration: time.Since(startedAt),
		}
		if diagnostic, ok := errors.AsType[stageDiagnosticError](err); ok {
			transition.Diagnostic = diagnostic.diagnostic
		}
		worker.observe(transition)
		return true, err
	}
	return true, nil
}

type processResponse struct {
	result StageResult
	err    error
}

func (worker *Worker) handle(ctx context.Context, lease Lease, processor Processor, startedAt time.Time) error {
	atomicPublication := worker.publisher != nil && isKnownDerivedStage(lease.Job.Stage)
	if atomicPublication {
		lease.heartbeat = &leaseHeartbeatGuard{}
	}
	processContext, cancel := context.WithCancel(ctx)
	defer cancel()
	response := make(chan processResponse, 1)
	go func() {
		var result StageResult
		var err error
		if atomicPublication {
			result, err = processor.(leaseProcessor).ProcessLease(processContext, lease, worker.publisher)
		} else {
			result, err = processor.Process(processContext, lease.Job)
		}
		response <- processResponse{result: result, err: err}
	}()

	heartbeat := time.NewTicker(worker.options.LeaseDuration / 3)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			cancel()
			return ctx.Err()
		case <-heartbeat.C:
			if err := worker.renew(ctx, lease); err != nil {
				cancel()
				return fmt.Errorf("renew enrichment lease: %w", err)
			}
		case completed := <-response:
			return worker.record(ctx, lease, completed, atomicPublication, startedAt)
		}
	}
}

func (worker *Worker) renew(ctx context.Context, lease Lease) error {
	if lease.heartbeat == nil {
		return worker.queue.Renew(ctx, lease, worker.options.LeaseDuration)
	}
	lease.heartbeat.mu.Lock()
	defer lease.heartbeat.mu.Unlock()
	if lease.heartbeat.finished {
		return nil
	}
	return worker.queue.Renew(ctx, lease, worker.options.LeaseDuration)
}

func (worker *Worker) record(
	ctx context.Context,
	lease Lease,
	completed processResponse,
	atomicPublication bool,
	startedAt time.Time,
) error {
	if completed.err == nil {
		if atomicPublication {
			switch completed.result.publication {
			case stagePublicationSucceeded:
				worker.observe(worker.transition(
					lease.Job, completed.result, "succeeded", "succeeded", "", 0, startedAt,
				))
				return nil
			case stagePublicationSuperseded:
				worker.observe(worker.transition(
					lease.Job, completed.result, "queued", "superseded", "superseded", 0, startedAt,
				))
				return nil
			default:
				return ErrAtomicPublicationRequired
			}
		}
		if completed.result.State == "" {
			completed.result.State = ResultComplete
		}
		if err := completed.result.validateForFinish(); err != nil {
			completed.err = Permanent(fmt.Errorf("processor returned invalid result: %w", err))
		} else if err := worker.queue.Finish(ctx, lease, completed.result); err != nil {
			return fmt.Errorf("finish enrichment job: %w", err)
		} else {
			worker.observe(worker.transition(
				lease.Job, completed.result, "succeeded", "succeeded", "", 0, startedAt,
			))
			return nil
		}
	}

	if classified, ok := errors.AsType[stageError](completed.err); ok {
		switch classified.kind {
		case "unavailable":
			if err := worker.finishError(ctx, lease, ResultUnavailable, completed.err); err != nil {
				return err
			}
			worker.observe(worker.errorTransition(
				lease.Job, ResultUnavailable, "failed", "unavailable", "capability_unavailable",
				completed.err, 0, startedAt,
			))
			return nil
		case "permanent":
			if err := worker.finishError(ctx, lease, ResultFailed, completed.err); err != nil {
				return err
			}
			worker.observe(worker.errorTransition(
				lease.Job, ResultFailed, "failed", "failed", "permanent_failure",
				completed.err, 0, startedAt,
			))
			return nil
		}
	}
	// The queue owns retry exhaustion. In PostgreSQL this decision is made from
	// durable_jobs.max_attempts in the same fenced transaction as the retry;
	// keeping a second worker-local limit can terminate an eight-attempt worker
	// while the durable producer granted ten attempts.
	retry := Retry{Reason: completed.err.Error(), After: worker.retryDelay(lease.Job.Attempt)}
	if err := worker.queue.Retry(ctx, lease, retry); err != nil {
		return fmt.Errorf("retry enrichment job: %w", err)
	}
	result, status, code := "retry", "queued", "retryable_failure"
	if lease.Job.MaxAttempts > 0 && lease.Job.Attempt >= lease.Job.MaxAttempts {
		result, status, code = "failed", "failed", "attempts_exhausted"
	}
	worker.observe(worker.errorTransition(
		lease.Job, "", status, result, code, completed.err, retry.After, startedAt,
	))
	return nil
}

func (worker *Worker) transition(
	job Job,
	stageResult StageResult,
	jobStatus, result, code string,
	retryAfter time.Duration,
	startedAt time.Time,
) JobTransition {
	return JobTransition{
		Event: JobEventTransitioned, Component: worker.Name(), WorkerID: worker.options.ID,
		Job: job, JobStatus: jobStatus, StageState: stageResult.State, Result: result,
		Code: code, RetryAfter: retryAfter, Duration: time.Since(startedAt),
		Details: maps.Clone(stageResult.Details), Diagnostic: stageResult.diagnostic,
	}
}

func (worker *Worker) errorTransition(
	job Job,
	stageState ResultState,
	jobStatus, result, code string,
	cause error,
	retryAfter time.Duration,
	startedAt time.Time,
) JobTransition {
	transition := worker.transition(
		job, StageResult{State: stageState}, jobStatus, result, code, retryAfter, startedAt,
	)
	if diagnostic, ok := errors.AsType[stageDiagnosticError](cause); ok {
		transition.Diagnostic = diagnostic.diagnostic
		if transition.Diagnostic.Code != "" {
			transition.Code = transition.Diagnostic.Code
		}
	}
	return transition
}

func (worker *Worker) observe(transition JobTransition) {
	if worker.options.Observer != nil {
		worker.options.Observer.RecordEnrichmentJob(transition)
	}
}

func isKnownDerivedStage(stage StageID) bool {
	switch stage.Name {
	case ProxyStage.Name, ABIStage.Name, TokenStage.Name, StatsStage.Name, TraceStage.Name, StateDiffStage.Name, UserOperationStage.Name, HolderStage.Name:
		return true
	default:
		return false
	}
}

func (worker *Worker) finishError(ctx context.Context, lease Lease, state ResultState, cause error) error {
	result := StageResult{State: state, Error: cause.Error()}
	if err := worker.queue.Finish(ctx, lease, result); err != nil {
		return fmt.Errorf("finish enrichment job: %w", err)
	}
	return nil
}

func (worker *Worker) retryDelay(attempt uint32) time.Duration {
	delay := worker.options.RetryBase
	for current := uint32(1); current < attempt && delay < worker.options.RetryMax; current++ {
		if delay > worker.options.RetryMax/2 {
			return worker.options.RetryMax
		}
		delay *= 2
	}
	if delay > worker.options.RetryMax {
		return worker.options.RetryMax
	}
	return delay
}

func waitContextOrWake(ctx context.Context, duration time.Duration, wake <-chan struct{}) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	case <-wake:
		return nil
	}
}
