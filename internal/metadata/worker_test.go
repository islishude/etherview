package metadata

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRepository struct {
	mu        sync.Mutex
	lease     Lease
	found     bool
	current   Current
	finished  []Outcome
	retries   []fakeRetry
	renewals  int
	claimErr  error
	finishErr error
	retryErr  error
}

type fakeRetry struct {
	code    string
	message string
	after   time.Duration
}

func (repository *fakeRepository) Claim(context.Context, string, time.Duration) (Lease, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.lease, repository.found, repository.claimErr
}

func (repository *fakeRepository) Renew(context.Context, Lease, time.Duration) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.renewals++
	return nil
}

func (repository *fakeRepository) Current(context.Context, Lease) (Current, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.current, nil
}

func (repository *fakeRepository) Finish(_ context.Context, _ Lease, outcome Outcome) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.finished = append(repository.finished, outcome)
	return repository.finishErr
}

func (repository *fakeRepository) Retry(_ context.Context, _ Lease, code, message string, after time.Duration) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.retries = append(repository.retries, fakeRetry{code: code, message: message, after: after})
	return repository.retryErr
}

type fetcherFunc func(context.Context, string, Kind) (Result, error)

func (fetcher fetcherFunc) Fetch(ctx context.Context, rawURL string, kind Kind) (Result, error) {
	return fetcher(ctx, rawURL, kind)
}

type recordingFetchObserver struct{ transitions []FetchTransition }

func (observer *recordingFetchObserver) RecordMetadataFetch(transition FetchTransition) {
	observer.transitions = append(observer.transitions, transition)
}

func TestWorkerObservesPersistedMetadataOutcome(t *testing.T) {
	repository := readyFakeRepository(t, 1)
	observer := &recordingFetchObserver{}
	worker, err := NewWorker(repository, fetcherFunc(func(_ context.Context, rawURL string, _ Kind) (Result, error) {
		return Result{URL: rawURL, ContentType: "application/json", Body: []byte(`{"name":"NFT"}`)}, nil
	}), WorkerOptions{
		WorkerID: "test-worker", LeaseDuration: time.Second, PollInterval: time.Millisecond,
		RetryBase: time.Millisecond, RetryMaximum: 10 * time.Millisecond, Observer: observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := worker.ProcessOnce(t.Context()); err != nil || !processed {
		t.Fatalf("processed=%t error=%v", processed, err)
	}
	if len(observer.transitions) != 2 || observer.transitions[0].Event != FetchEventStarted {
		t.Fatalf("metadata observations=%v", observer.transitions)
	}
	transition := observer.transitions[1]
	request := repository.lease.Request
	if transition.Result != "succeeded" || transition.State != StateAvailable || transition.Code != "" ||
		transition.JobID != repository.lease.JobID || transition.WorkerID != "test-worker" ||
		transition.NFTContract != request.Token || transition.NFTID != request.TokenID ||
		transition.BlockNumber != request.BlockNumber || transition.BlockHash != request.BlockHash ||
		transition.Attempt != repository.lease.Attempt || transition.MaxAttempts != repository.lease.MaxAttempts ||
		transition.Diagnostic.SourceScheme != "https" {
		t.Fatalf("metadata transition=%+v", transition)
	}
}

func TestWorkerPersistsAvailableDocument(t *testing.T) {
	t.Parallel()
	repository := readyFakeRepository(t, 1)
	document := []byte(`{"name":"NFT","attributes":[]}`)
	worker := newTestWorker(t, repository, fetcherFunc(func(_ context.Context, rawURL string, kind Kind) (Result, error) {
		if rawURL != repository.lease.Request.SourceURI || kind != KindJSON {
			t.Fatalf("fetch URL=%q kind=%q", rawURL, kind)
		}
		return Result{URL: rawURL, ContentType: "application/json", Body: document}, nil
	}))
	processed, err := worker.ProcessOnce(t.Context())
	if err != nil || !processed {
		t.Fatalf("processed=%t err=%v", processed, err)
	}
	if len(repository.finished) != 1 || repository.finished[0].State != StateAvailable ||
		string(repository.finished[0].Document) != string(document) || repository.finished[0].ContentSize != int64(len(document)) {
		t.Fatalf("finished outcomes = %+v", repository.finished)
	}
	if len(repository.retries) != 0 {
		t.Fatalf("unexpected retries: %+v", repository.retries)
	}
}

func TestWorkerClassifiesTerminalFetchFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		kind  FailureKind
		state State
		code  string
	}{
		{name: "unavailable", kind: FailureUnavailable, state: StateUnavailable, code: "source_unavailable"},
		{name: "unsafe URL", kind: FailureUnsafeURL, state: StateUnsafe, code: "unsafe_url"},
		{name: "unsafe content", kind: FailureUnsafeContent, state: StateUnsafe, code: "unsafe_content"},
		{name: "too large", kind: FailureTooLarge, state: StateUnsafe, code: "response_too_large"},
		{name: "invalid", kind: FailureInvalid, state: StateError, code: "invalid_content"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := readyFakeRepository(t, 1)
			worker := newTestWorker(t, repository, fetcherFunc(func(context.Context, string, Kind) (Result, error) {
				return Result{}, &FetchError{Kind: test.kind, Err: errors.New("secret URL must not be persisted")}
			}))
			if processed, err := worker.ProcessOnce(t.Context()); err != nil || !processed {
				t.Fatalf("processed=%t err=%v", processed, err)
			}
			if len(repository.finished) != 1 || repository.finished[0].State != test.state || repository.finished[0].Code != test.code {
				t.Fatalf("finished=%+v, want state=%s code=%s", repository.finished, test.state, test.code)
			}
			if repository.finished[0].Message == "secret URL must not be persisted" {
				t.Fatal("raw fetch error was persisted")
			}
			if len(repository.retries) != 0 {
				t.Fatalf("terminal failure was retried: %+v", repository.retries)
			}
		})
	}
}

func TestWorkerRetriesTemporaryFailureThenExhausts(t *testing.T) {
	t.Parallel()
	diagnostic := FetchDiagnostic{Phase: FetchPhaseTransport, Reason: FetchFailureTLSHandshakeTimeout}
	fetcher := fetcherFunc(func(context.Context, string, Kind) (Result, error) {
		return Result{}, &FetchError{Kind: FailureTemporary, Err: errors.New("temporary"), Diagnostic: diagnostic}
	})
	retryRepository := readyFakeRepository(t, 2)
	worker := newTestWorker(t, retryRepository, fetcher)
	if processed, err := worker.ProcessOnce(t.Context()); err != nil || !processed {
		t.Fatalf("processed=%t err=%v", processed, err)
	}
	if len(retryRepository.retries) != 1 || retryRepository.retries[0].code != "temporary_fetch_error" || retryRepository.retries[0].after != 2*time.Millisecond {
		t.Fatalf("retries=%+v", retryRepository.retries)
	}
	if len(retryRepository.finished) != 0 {
		t.Fatalf("retryable attempt was terminal: %+v", retryRepository.finished)
	}

	exhaustedRepository := readyFakeRepository(t, 3)
	observer := &recordingFetchObserver{}
	worker, err := NewWorker(exhaustedRepository, fetcher, WorkerOptions{
		WorkerID: "test-worker", LeaseDuration: time.Second, PollInterval: time.Millisecond,
		RetryBase: time.Millisecond, RetryMaximum: 10 * time.Millisecond, Observer: observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := worker.ProcessOnce(t.Context()); err != nil || !processed {
		t.Fatalf("processed=%t err=%v", processed, err)
	}
	if len(exhaustedRepository.retries) != 0 || len(exhaustedRepository.finished) != 1 ||
		exhaustedRepository.finished[0].State != StateError || exhaustedRepository.finished[0].Code != "attempts_exhausted" {
		t.Fatalf("exhausted retries=%+v finished=%+v", exhaustedRepository.retries, exhaustedRepository.finished)
	}
	if strings.Contains(exhaustedRepository.finished[0].Message, "temporary") {
		t.Fatalf("exhausted outcome persisted nested upstream text: %+v", exhaustedRepository.finished[0])
	}
	if len(observer.transitions) != 2 || observer.transitions[1].Code != "attempts_exhausted" ||
		observer.transitions[1].LastCode != "temporary_fetch_error" ||
		observer.transitions[1].Diagnostic.Reason != FetchFailureTLSHandshakeTimeout {
		t.Fatalf("exhausted transition=%+v", observer.transitions)
	}
}

func TestWorkerObservesRetryOnlyAfterDurableTransition(t *testing.T) {
	t.Parallel()
	diagnostic := FetchDiagnostic{
		SourceScheme: "ipfs", RequestMethod: "GET", RequestScheme: "https",
		RequestHost: "ipfs.io", RequestPort: "443",
		RequestPath: "/ipfs/Qma3sC19HbnWHqeLgcsQnR7Kvgus4oPQirXNH7QYBeACaq/0",
		Phase:       FetchPhaseTransport,
	}
	fetcher := fetcherFunc(func(context.Context, string, Kind) (Result, error) {
		return Result{}, &FetchError{Kind: FailureTemporary, Err: errors.New("nested secret"), Diagnostic: diagnostic}
	})
	repository := readyFakeRepository(t, 1)
	observer := &recordingFetchObserver{}
	worker, err := NewWorker(repository, fetcher, WorkerOptions{
		WorkerID: "metadata-worker-01", LeaseDuration: time.Second,
		PollInterval: time.Millisecond, RetryBase: time.Millisecond,
		RetryMaximum: 10 * time.Millisecond, Observer: observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if processed, processErr := worker.ProcessOnce(t.Context()); processErr != nil || !processed {
		t.Fatalf("processed=%t err=%v", processed, processErr)
	}
	if len(observer.transitions) != 2 || observer.transitions[0].Event != FetchEventStarted {
		t.Fatalf("transitions=%+v", observer.transitions)
	}
	transition := observer.transitions[1]
	if transition.Result != "retry" || transition.State != StatePending ||
		transition.Code != "temporary_fetch_error" || transition.Diagnostic.RequestHost != "ipfs.io" {
		t.Fatalf("transition=%+v", transition)
	}
}

func TestWorkerDoesNotObserveFailedPersistence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		attempt   uint32
		fetcher   Fetcher
		configure func(*fakeRepository)
	}{
		{
			name:    "finish",
			attempt: 1,
			fetcher: fetcherFunc(func(_ context.Context, rawURL string, _ Kind) (Result, error) {
				return Result{URL: rawURL, ContentType: "application/json", Body: []byte(`{"name":"NFT"}`)}, nil
			}),
			configure: func(repository *fakeRepository) { repository.finishErr = errors.New("database unavailable") },
		},
		{
			name:    "retry",
			attempt: 1,
			fetcher: fetcherFunc(func(context.Context, string, Kind) (Result, error) {
				return Result{}, &FetchError{Kind: FailureTemporary, Err: errors.New("network unavailable")}
			}),
			configure: func(repository *fakeRepository) { repository.retryErr = errors.New("database unavailable") },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := readyFakeRepository(t, test.attempt)
			test.configure(repository)
			observer := &recordingFetchObserver{}
			worker, err := NewWorker(repository, test.fetcher, WorkerOptions{
				WorkerID: "metadata-worker-01", LeaseDuration: time.Second,
				PollInterval: time.Millisecond, RetryBase: time.Millisecond,
				RetryMaximum: 10 * time.Millisecond, Observer: observer,
			})
			if err != nil {
				t.Fatal(err)
			}
			processed, processErr := worker.ProcessOnce(t.Context())
			if !processed || processErr == nil {
				t.Fatalf("processed=%t err=%v", processed, processErr)
			}
			if len(observer.transitions) != 1 || observer.transitions[0].Event != FetchEventStarted {
				t.Fatalf("failed persistence emitted transitions=%+v", observer.transitions)
			}
		})
	}
}

func TestWorkerSkipsFetchForSupersededOrOrphanSource(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		current Current
		code    string
	}{
		{name: "superseded", current: Current{}, code: "superseded"},
		{name: "orphan", current: Current{Resource: true}, code: "source_block_noncanonical"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := readyFakeRepository(t, 1)
			repository.current = test.current
			fetches := 0
			worker := newTestWorker(t, repository, fetcherFunc(func(context.Context, string, Kind) (Result, error) {
				fetches++
				return Result{}, nil
			}))
			if processed, err := worker.ProcessOnce(t.Context()); err != nil || !processed {
				t.Fatalf("processed=%t err=%v", processed, err)
			}
			if fetches != 0 || len(repository.finished) != 1 || repository.finished[0].Code != test.code {
				t.Fatalf("fetches=%d finished=%+v", fetches, repository.finished)
			}
		})
	}
}

func TestWorkerRejectsNonObjectDocument(t *testing.T) {
	t.Parallel()
	repository := readyFakeRepository(t, 1)
	worker := newTestWorker(t, repository, fetcherFunc(func(_ context.Context, rawURL string, _ Kind) (Result, error) {
		return Result{URL: rawURL, ContentType: "application/json", Body: []byte(`[]`)}, nil
	}))
	if processed, err := worker.ProcessOnce(t.Context()); err != nil || !processed {
		t.Fatalf("processed=%t err=%v", processed, err)
	}
	if len(repository.finished) != 1 || repository.finished[0].State != StateError || repository.finished[0].Code != "invalid_document" {
		t.Fatalf("finished=%+v", repository.finished)
	}
}

func readyFakeRepository(t *testing.T, attempt uint32) *fakeRepository {
	t.Helper()
	return &fakeRepository{
		lease: Lease{
			JobID: 7, Token: "lease-token", Request: validNFTRequest(t),
			Attempt: attempt, MaxAttempts: 3,
		},
		found: true, current: Current{Resource: true, Canonical: true},
	}
}

func newTestWorker(t *testing.T, repository Repository, fetcher Fetcher) *Worker {
	t.Helper()
	worker, err := NewWorker(repository, fetcher, WorkerOptions{
		WorkerID: "test-worker", LeaseDuration: time.Second,
		PollInterval: time.Millisecond, RetryBase: time.Millisecond, RetryMaximum: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}
