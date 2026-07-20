package enrich

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrAtomicPublicationRequired = errors.New("successful enrichment output requires lease-fenced atomic publication")

// StageID changes whenever persisted output semantics change. Replaying a new
// version therefore creates a distinct durable job instead of mutating the
// meaning of a previously completed one.
type StageID struct {
	Name    string
	Version uint32
}

func (stage StageID) Validate() error {
	if stage.Name == "" {
		return errors.New("stage name is empty")
	}
	for _, character := range stage.Name {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return fmt.Errorf("stage name %q contains an unsupported character", stage.Name)
		}
	}
	if stage.Version == 0 {
		return errors.New("stage version must be positive")
	}
	return nil
}

func (stage StageID) String() string {
	return fmt.Sprintf("%s@%d", stage.Name, stage.Version)
}

// Job identifies enrichment for one immutable block hash. Number is useful for
// scheduling but deliberately does not participate in identity.
type Job struct {
	ID          string
	Stage       StageID
	ChainID     string
	BlockHash   Word
	BlockNumber uint64
	Attempt     uint32
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
	if job.BlockHash.IsZero() {
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
	digest := sha256.Sum256([]byte(fmt.Sprintf(
		"%s\x00%d\x00%s\x00%s",
		job.Stage.Name,
		job.Stage.Version,
		job.ChainID,
		job.BlockHash.String(),
	)))
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
	ID            string
	LeaseDuration time.Duration
	PollInterval  time.Duration
	RetryBase     time.Duration
	RetryMax      time.Duration
}

func (options *WorkerOptions) defaults() {
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

func (*Worker) Name() string { return "enrichment-worker" }

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
			if err := waitContext(ctx, worker.options.PollInterval); err != nil {
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
	if err := worker.handle(ctx, lease, processor); err != nil {
		return true, err
	}
	return true, nil
}

type processResponse struct {
	result StageResult
	err    error
}

func (worker *Worker) handle(ctx context.Context, lease Lease, processor Processor) error {
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
			return worker.record(ctx, lease, completed, atomicPublication)
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

func (worker *Worker) record(ctx context.Context, lease Lease, completed processResponse, atomicPublication bool) error {
	if completed.err == nil {
		if atomicPublication {
			switch completed.result.publication {
			case stagePublicationSucceeded, stagePublicationSuperseded:
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
			return nil
		}
	}

	var classified stageError
	if errors.As(completed.err, &classified) {
		switch classified.kind {
		case "unavailable":
			return worker.finishError(ctx, lease, ResultUnavailable, completed.err)
		case "permanent":
			return worker.finishError(ctx, lease, ResultFailed, completed.err)
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
	return nil
}

func isKnownDerivedStage(stage StageID) bool {
	switch stage.Name {
	case ProxyStage.Name, ABIStage.Name, TokenStage.Name, StatsStage.Name, TraceStage.Name:
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

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
