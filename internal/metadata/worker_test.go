package metadata

import (
	"context"
	"errors"
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
	return nil
}

type fetcherFunc func(context.Context, string, Kind) (Result, error)

func (fetcher fetcherFunc) Fetch(ctx context.Context, rawURL string, kind Kind) (Result, error) {
	return fetcher(ctx, rawURL, kind)
}

func TestWorkerPersistsAvailableDocument(t *testing.T) {
	t.Parallel()
	repository := readyFakeRepository(t, 1, 3)
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
			repository := readyFakeRepository(t, 1, 3)
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
	fetcher := fetcherFunc(func(context.Context, string, Kind) (Result, error) {
		return Result{}, &FetchError{Kind: FailureTemporary, Err: errors.New("temporary")}
	})
	retryRepository := readyFakeRepository(t, 2, 3)
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

	exhaustedRepository := readyFakeRepository(t, 3, 3)
	worker = newTestWorker(t, exhaustedRepository, fetcher)
	if processed, err := worker.ProcessOnce(t.Context()); err != nil || !processed {
		t.Fatalf("processed=%t err=%v", processed, err)
	}
	if len(exhaustedRepository.retries) != 0 || len(exhaustedRepository.finished) != 1 ||
		exhaustedRepository.finished[0].State != StateError || exhaustedRepository.finished[0].Code != "attempts_exhausted" {
		t.Fatalf("exhausted retries=%+v finished=%+v", exhaustedRepository.retries, exhaustedRepository.finished)
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
			repository := readyFakeRepository(t, 1, 3)
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
	repository := readyFakeRepository(t, 1, 3)
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

func readyFakeRepository(t *testing.T, attempt, maximum uint32) *fakeRepository {
	t.Helper()
	return &fakeRepository{
		lease: Lease{
			JobID: 7, Token: "lease-token", Request: validNFTRequest(t),
			Attempt: attempt, MaxAttempts: maximum,
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
