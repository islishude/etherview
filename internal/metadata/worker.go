package metadata

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
)

type WorkerOptions struct {
	WorkerID      string
	LeaseDuration time.Duration
	PollInterval  time.Duration
	RetryBase     time.Duration
	RetryMaximum  time.Duration
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
	if options.RetryMaximum <= 0 {
		options.RetryMaximum = 5 * time.Minute
	}
}

type Worker struct {
	repository Repository
	fetcher    Fetcher
	options    WorkerOptions
}

func NewWorker(repository Repository, fetcher Fetcher, options WorkerOptions) (*Worker, error) {
	if repository == nil {
		return nil, errors.New("metadata worker requires a repository")
	}
	if fetcher == nil {
		return nil, errors.New("metadata worker requires a safe fetcher")
	}
	options.defaults()
	if strings.TrimSpace(options.WorkerID) == "" || len(options.WorkerID) > 128 {
		return nil, errors.New("metadata worker ID must contain between 1 and 128 bytes")
	}
	if options.LeaseDuration < 3*time.Millisecond {
		return nil, errors.New("metadata lease duration must be at least 3ms")
	}
	if options.RetryMaximum < options.RetryBase {
		return nil, errors.New("metadata maximum retry delay is less than base delay")
	}
	return &Worker{repository: repository, fetcher: fetcher, options: options}, nil
}

func (*Worker) Name() string { return "metadata-worker" }

func (worker *Worker) Run(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		processed, err := worker.ProcessOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if processed {
			timer.Reset(0)
		} else {
			timer.Reset(worker.options.PollInterval)
		}
	}
}

func (worker *Worker) ProcessOnce(ctx context.Context) (bool, error) {
	lease, found, err := worker.repository.Claim(ctx, worker.options.WorkerID, worker.options.LeaseDuration)
	if err != nil || !found {
		return false, err
	}
	if err := lease.Validate(); err != nil {
		return true, fmt.Errorf("metadata repository returned invalid lease: %w", err)
	}
	return true, worker.processLease(ctx, lease)
}

type fetchResponse struct {
	result Result
	err    error
}

func (worker *Worker) processLease(ctx context.Context, lease Lease) error {
	current, err := worker.repository.Current(ctx, lease)
	if err != nil {
		return fmt.Errorf("check metadata canonical identity: %w", err)
	}
	if !current.Resource {
		return worker.repository.Finish(ctx, lease, terminalOutcome(
			StateUnavailable, "superseded", "metadata source was superseded by a newer canonical observation",
		))
	}
	if !current.Canonical {
		return worker.repository.Finish(ctx, lease, terminalOutcome(
			StateUnavailable, "source_block_noncanonical", "metadata source block is no longer canonical",
		))
	}

	response := make(chan fetchResponse, 1)
	go func() {
		result, fetchErr := worker.fetcher.Fetch(ctx, lease.Request.SourceURI, KindJSON)
		response <- fetchResponse{result: result, err: fetchErr}
	}()
	heartbeat := time.NewTicker(worker.options.LeaseDuration / 3)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-heartbeat.C:
			if err := worker.repository.Renew(ctx, lease, worker.options.LeaseDuration); err != nil {
				return fmt.Errorf("renew metadata lease: %w", err)
			}
		case completed := <-response:
			return worker.record(ctx, lease, completed)
		}
	}
}

func (worker *Worker) record(ctx context.Context, lease Lease, completed fetchResponse) error {
	if completed.err == nil {
		if err := validateDocument(completed.result.Body); err != nil {
			return worker.repository.Finish(ctx, lease, terminalOutcome(StateError, "invalid_document", boundedError(err)))
		}
		if completed.result.ContentType == "" || completed.result.URL == "" || len(completed.result.URL) > MaxSourceURIBytes {
			return worker.repository.Finish(ctx, lease, terminalOutcome(StateError, "invalid_fetch_result", "safe metadata fetcher returned incomplete source information"))
		}
		digest := sha256.Sum256(completed.result.Body)
		outcome := Outcome{
			State: StateAvailable, ResolvedURI: completed.result.URL,
			MediaType: completed.result.ContentType, Document: completed.result.Body,
			ContentHash: digest, ContentSize: int64(len(completed.result.Body)),
		}
		if err := outcome.validate(); err != nil {
			return fmt.Errorf("validate fetched metadata outcome: %w", err)
		}
		return worker.repository.Finish(ctx, lease, outcome)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	state, code, retry := classifyFetchFailure(completed.err)
	message := fetchFailureMessage(completed.err)
	if retry && lease.Attempt < lease.MaxAttempts {
		return worker.repository.Retry(ctx, lease, code, message, worker.retryDelay(lease.Attempt))
	}
	if retry {
		state = StateError
		code = "attempts_exhausted"
		message = "maximum metadata fetch attempts exhausted"
	}
	return worker.repository.Finish(ctx, lease, terminalOutcome(state, code, message))
}

func classifyFetchFailure(err error) (State, string, bool) {
	var failure *FetchError
	if !errors.As(err, &failure) {
		return StateError, "fetch_error", true
	}
	switch failure.Kind {
	case FailureTemporary:
		return StateError, "temporary_fetch_error", true
	case FailureUnavailable:
		return StateUnavailable, "source_unavailable", false
	case FailureUnsafeURL:
		return StateUnsafe, "unsafe_url", false
	case FailureUnsafeContent:
		return StateUnsafe, "unsafe_content", false
	case FailureTooLarge:
		return StateUnsafe, "response_too_large", false
	case FailureInvalid:
		return StateError, "invalid_content", false
	default:
		return StateError, "fetch_error", false
	}
}

func terminalOutcome(state State, code, message string) Outcome {
	return Outcome{State: state, Code: code, Message: boundedText(message, MaxStoredErrorBytes)}
}

func boundedError(err error) string {
	if err == nil {
		return "metadata operation failed"
	}
	return boundedText(err.Error(), MaxStoredErrorBytes)
}

func fetchFailureMessage(err error) string {
	var failure *FetchError
	if !errors.As(err, &failure) {
		return "metadata fetch failed"
	}
	switch failure.Kind {
	case FailureTemporary:
		return "metadata source is temporarily unreachable"
	case FailureUnavailable:
		return "metadata source is unavailable"
	case FailureUnsafeURL:
		return "metadata source URL violates the network safety policy"
	case FailureUnsafeContent:
		return "metadata response content type or bytes violate the media safety policy"
	case FailureTooLarge:
		return "metadata response exceeds the configured size limit"
	case FailureInvalid:
		return "metadata response is not valid JSON"
	default:
		return "metadata fetch failed"
	}
}

func boundedText(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "metadata operation failed"
	}
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}

func (worker *Worker) retryDelay(attempt uint32) time.Duration {
	delay := worker.options.RetryBase
	for current := uint32(1); current < attempt; current++ {
		if delay >= worker.options.RetryMaximum/2 {
			return worker.options.RetryMaximum
		}
		delay *= 2
	}
	if delay > worker.options.RetryMaximum {
		return worker.options.RetryMaximum
	}
	return delay
}
