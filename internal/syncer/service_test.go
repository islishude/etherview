package syncer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/events"
	"github.com/islishude/etherview/internal/indexer"
	"github.com/islishude/etherview/internal/store"
)

type fakeSource struct {
	mu       sync.Mutex
	head     uint64
	bundles  map[uint64]ethrpc.Bundle
	calls    []uint64
	purposes []ethrpc.Purpose
}

type notifyingSource struct {
	*fakeSource
	headCalls chan struct{}
}

type bundleHashSource struct {
	mu      sync.Mutex
	bundles map[string]ethrpc.Bundle
	calls   int
}

func newBundleHashSource(bundles ...ethrpc.Bundle) *bundleHashSource {
	source := &bundleHashSource{bundles: make(map[string]ethrpc.Bundle, len(bundles))}
	for _, bundle := range bundles {
		hash, _ := bundle.BlockHash()
		source.bundles[strings.ToLower(hash.String())] = bundle
	}
	return source
}

func (s *bundleHashSource) BundleByHash(_ context.Context, hash ethrpc.Hash) (ethrpc.Bundle, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	bundle, exists := s.bundles[strings.ToLower(hash.String())]
	return bundle, exists, nil
}

type statusRecorder struct {
	mu       sync.Mutex
	statuses []events.SyncStatus
	err      error
}

type capturedLog struct {
	message string
	attrs   map[string]string
}

type captureLogHandler struct{ records chan capturedLog }

func (handler *captureLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (handler *captureLogHandler) Handle(_ context.Context, record slog.Record) error {
	entry := capturedLog{message: record.Message, attrs: make(map[string]string)}
	record.Attrs(func(attribute slog.Attr) bool {
		entry.attrs[attribute.Key] = attribute.Value.Resolve().String()
		return true
	})
	handler.records <- entry
	return nil
}
func (handler *captureLogHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler *captureLogHandler) WithGroup(string) slog.Handler      { return handler }

func (r *statusRecorder) RecordStatus(_ context.Context, status events.SyncStatus) (events.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses = append(r.statuses, status)
	return events.Event{}, r.err
}

func (r *statusRecorder) last() events.SyncStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statuses[len(r.statuses)-1]
}

func (s *notifyingSource) Head(ctx context.Context) (uint64, error) {
	select {
	case s.headCalls <- struct{}{}:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	return s.fakeSource.Head(ctx)
}

func (s *fakeSource) Head(context.Context) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.head, nil
}

func (s *fakeSource) BundleByNumber(_ context.Context, purpose ethrpc.Purpose, number uint64) (ethrpc.Bundle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, number)
	s.purposes = append(s.purposes, purpose)
	bundle, ok := s.bundles[number]
	if !ok {
		return ethrpc.Bundle{}, errors.New("missing fixture")
	}
	return bundle, nil
}

func (*fakeSource) Finality(context.Context) (*store.BlockRef, *store.BlockRef, error) {
	return nil, nil, nil
}

func TestCycleFillsBoundedGapAndPublishesReadiness(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryRepository()
	source := &fakeSource{head: 2, bundles: testChain(3)}
	tracker := &Tracker{}
	canonicalizer := &indexer.Canonicalizer{
		ChainID: "1", StartBlock: 0, MaxReorgDepth: 128,
		Repository: repository,
	}
	service := &Service{
		ChainID: "1", StartBlock: 0, Workers: 2, CycleBatch: 1,
		Source: source, Repository: repository, Canonicalizer: canonicalizer, Tracker: tracker,
	}
	if err := service.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := tracker.Snapshot()
	if first.Indexed != 0 || !first.IndexedKnown || first.HighestCovered != 2 || !first.HighestKnown || first.Ready {
		t.Fatalf("first snapshot = %+v", first)
	}
	if err := service.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := tracker.Snapshot()
	if second.Indexed != 2 || !second.Ready || !second.BackfillComplete {
		t.Fatalf("second snapshot = %+v", second)
	}
	if second.Latest != 2 || second.LastError != "" {
		t.Fatalf("second snapshot = %+v", second)
	}
	checkpoint, exists, err := repository.Checkpoint(context.Background(), "1", store.CoreCheckpoint)
	if err != nil || !exists || checkpoint.ContiguousThrough != 2 {
		t.Fatalf("checkpoint = %+v exists=%v error=%v", checkpoint, exists, err)
	}
}

func TestCyclePersistsAuthoritativeStatusAndSignalsRelay(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryRepository()
	recorder := &statusRecorder{}
	wake := make(chan struct{}, 4)
	service := &Service{
		ChainID: "1", StartBlock: 0, Workers: 1,
		Source: &fakeSource{head: 0, bundles: testChain(1)}, Repository: repository,
		Canonicalizer: &indexer.Canonicalizer{
			ChainID: "1", StartBlock: 0, MaxReorgDepth: 128, Repository: repository,
		},
		Tracker: &Tracker{}, Status: recorder,
		EventWake: func() { wake <- struct{}{} },
	}
	if err := service.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := recorder.last()
	if !status.LatestKnown || !status.IndexedKnown || !status.HighestCoveredKnown ||
		status.Latest != 0 || status.Indexed != 0 || status.HighestCovered != 0 ||
		!status.BackfillComplete || !status.Ready || status.ErrorCode != "" || status.PolledAt.IsZero() {
		t.Fatalf("persisted status = %+v", status)
	}
	select {
	case <-wake:
	default:
		t.Fatal("durable event relay was not signaled")
	}
}

type failingHeadSource struct{ Source }

func (failingHeadSource) Head(context.Context) (uint64, error) {
	return 0, errors.New("upstream https://user:secret@example.invalid failed")
}

func TestCyclePersistsOnlySanitizedFailureCode(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryRepository()
	recorder := &statusRecorder{}
	base := &fakeSource{head: 0, bundles: testChain(1)}
	service := &Service{
		ChainID: "1", Source: failingHeadSource{Source: base}, Repository: repository,
		Canonicalizer: &indexer.Canonicalizer{ChainID: "1", Repository: repository},
		Tracker:       &Tracker{}, Status: recorder,
	}
	err := service.Cycle(context.Background())
	if err == nil {
		t.Fatal("expected authoritative head failure")
	}
	status := recorder.last()
	if status.LatestKnown || status.ErrorCode != "sync_cycle_failed" {
		t.Fatalf("persisted failure status = %+v", status)
	}
	if status.ErrorCode == err.Error() {
		t.Fatal("raw upstream error was persisted")
	}
}

func TestRunDoesNotLogCredentialBearingRPCError(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryRepository()
	base := &fakeSource{head: 0, bundles: testChain(1)}
	logs := &captureLogHandler{records: make(chan capturedLog, 1)}
	service := &Service{
		ChainID: "1", PollInterval: time.Hour,
		Source: failingHeadSource{Source: base}, Repository: repository,
		Canonicalizer: &indexer.Canonicalizer{ChainID: "1", Repository: repository},
		Tracker:       &Tracker{}, Logger: slog.New(logs),
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	select {
	case entry := <-logs.records:
		cancel()
		if entry.message != "core sync cycle failed; polling will retry" ||
			entry.attrs["error_code"] != "sync_cycle_failed" || entry.attrs["error_type"] == "" {
			t.Fatalf("sync log = %+v", entry)
		}
		for key, value := range entry.attrs {
			if strings.Contains(value, "secret") || strings.Contains(value, "example.invalid") {
				t.Fatalf("credential-bearing RPC error leaked through %s=%q", key, value)
			}
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("timed out waiting for sanitized sync log")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("sync shutdown error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("sync service did not stop")
	}
}

func TestCycleReportsStatusPersistenceFailure(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryRepository()
	recorder := &statusRecorder{err: errors.New("database unavailable")}
	service := &Service{
		ChainID: "1", Source: &fakeSource{head: 0, bundles: testChain(1)}, Repository: repository,
		Canonicalizer: &indexer.Canonicalizer{ChainID: "1", Repository: repository},
		Tracker:       &Tracker{}, Status: recorder,
	}
	if err := service.Cycle(context.Background()); err == nil || !strings.Contains(err.Error(), "persist sync runtime status") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestCycleHandlesAuthoritativeHeadMovingBackward(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryRepository()
	source := &fakeSource{head: 2, bundles: testChain(3)}
	canonicalizer := &indexer.Canonicalizer{
		ChainID: "1", StartBlock: 0, MaxReorgDepth: 128,
		Repository: repository,
	}
	service := &Service{
		ChainID: "1", Workers: 2, Source: source, Repository: repository,
		Canonicalizer: canonicalizer, Tracker: &Tracker{},
	}
	if err := service.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	source.mu.Lock()
	source.head = 1
	source.mu.Unlock()
	if err := service.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	tip, exists, err := repository.CanonicalTip(context.Background(), "1")
	if err != nil || !exists || tip.Number != 1 {
		t.Fatalf("tip = %+v exists=%v error=%v", tip, exists, err)
	}
}

func TestLiveHeadRepairsShallowForkBeforeCreatingDisconnectedIsland(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	oldChain := syncTestChain(101, 1)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", oldChain); err != nil {
		t.Fatal(err)
	}
	ancestor, _ := store.RefFromBundle(oldChain[98])
	newNinetyNine := testBundle(99, testHash(209), ancestor.Hash)
	newHundred := testBundle(100, testHash(210), testHash(209))
	newHundredOne := testBundle(101, testHash(211), testHash(210))
	newHundredTwo := testBundle(102, testHash(212), testHash(211))
	source := &fakeSource{head: 102, bundles: map[uint64]ethrpc.Bundle{102: newHundredTwo}}
	canonicalizer := &indexer.Canonicalizer{
		ChainID: "1", StartBlock: 0, MaxReorgDepth: 128, Repository: repository,
		HeadSource: newBundleHashSource(newNinetyNine, newHundred, newHundredOne),
	}
	service := &Service{
		ChainID: "1", Source: source, Repository: repository,
		Canonicalizer: canonicalizer, Tracker: &Tracker{},
	}
	if err := service.liveOnce(ctx); err != nil {
		t.Fatal(err)
	}
	coverage, _, err := repository.Coverage(ctx, "1")
	if err != nil || len(coverage.Ranges) != 1 || coverage.Ranges[0] != (store.BlockRange{Start: 0, End: 102}) ||
		coverage.Contiguous == nil || !coverage.Contiguous.Hash.Equal(testHash(212)) {
		t.Fatalf("coverage=%+v error=%v", coverage, err)
	}
	if len(source.purposes) != 1 || source.purposes[0] != ethrpc.PurposeHead {
		t.Fatalf("numbered RPC purposes=%v", source.purposes)
	}
}

func TestBackfillBoundaryConflictRepairsExistingLiveIslandFromAuthoritativeAncestry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	oldChain := syncTestChain(101, 1)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", oldChain); err != nil {
		t.Fatal(err)
	}
	ancestor, _ := store.RefFromBundle(oldChain[98])
	newNinetyNine := testBundle(99, testHash(209), ancestor.Hash)
	newHundred := testBundle(100, testHash(210), testHash(209))
	newHundredOne := testBundle(101, testHash(211), testHash(210))
	newHundredTwo := testBundle(102, testHash(212), testHash(211))
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{newHundredTwo}); err != nil {
		t.Fatal(err)
	}
	source := &fakeSource{head: 102, bundles: map[uint64]ethrpc.Bundle{
		101: newHundredOne, 102: newHundredTwo,
	}}
	canonicalizer := &indexer.Canonicalizer{
		ChainID: "1", StartBlock: 0, MaxReorgDepth: 128, Repository: repository,
		HeadSource: newBundleHashSource(newNinetyNine, newHundred, newHundredOne),
	}
	service := &Service{
		ChainID: "1", CycleBatch: 1, Source: source, Repository: repository,
		Canonicalizer: canonicalizer, Tracker: &Tracker{},
	}
	service.latest.Store(102)
	service.latestKnown.Store(true)
	didWork, err := service.backfillOnce(ctx, "worker-0")
	if err != nil || !didWork {
		t.Fatalf("didWork=%v error=%v", didWork, err)
	}
	coverage, _, err := repository.Coverage(ctx, "1")
	if err != nil || len(coverage.Ranges) != 1 || coverage.Ranges[0] != (store.BlockRange{Start: 0, End: 102}) ||
		coverage.Contiguous == nil || !coverage.Contiguous.Hash.Equal(testHash(212)) {
		t.Fatalf("coverage=%+v error=%v", coverage, err)
	}
	if len(source.purposes) != 2 || source.purposes[0] != ethrpc.PurposeHistory || source.purposes[1] != ethrpc.PurposeHead {
		t.Fatalf("numbered RPC purposes=%v", source.purposes)
	}
}

type blockingHistorySource struct {
	bundle         ethrpc.Bundle
	headCalls      chan struct{}
	historyStarted chan struct{}
}

func (s *blockingHistorySource) Head(ctx context.Context) (uint64, error) {
	select {
	case s.headCalls <- struct{}{}:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	return 2, nil
}

func (s *blockingHistorySource) BundleByNumber(
	ctx context.Context,
	purpose ethrpc.Purpose,
	_ uint64,
) (ethrpc.Bundle, error) {
	if purpose == ethrpc.PurposeHead {
		return s.bundle, nil
	}
	select {
	case s.historyStarted <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ethrpc.Bundle{}, ctx.Err()
}

func (*blockingHistorySource) Finality(context.Context) (*store.BlockRef, *store.BlockRef, error) {
	return nil, nil, nil
}

func TestBlockedHistoryWorkerDoesNotConsumeWakeOrDelayLiveHeadLane(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryRepository()
	wake := make(chan struct{}, 1)
	source := &blockingHistorySource{
		bundle: testChain(3)[2], headCalls: make(chan struct{}, 4), historyStarted: make(chan struct{}, 1),
	}
	service := &Service{
		ChainID: "1", PollInterval: time.Hour, Source: source, Repository: repository,
		Canonicalizer: &indexer.Canonicalizer{ChainID: "1", Repository: repository},
		Tracker:       &Tracker{}, Wake: wake,
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	waitForHeadCall(t, source.headCalls)
	select {
	case <-source.historyStarted:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("history worker did not start")
	}
	wake <- struct{}{}
	waitForHeadCall(t, source.headCalls)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("sync service did not stop")
	}
}

func TestMissingBackfillRangesHandlesUint64Boundary(t *testing.T) {
	t.Parallel()
	start := ^uint64(0) - 10
	ranges := missingBackfillRanges(store.CoreCoverage{ConfiguredStart: start}, ^uint64(0), 256)
	if len(ranges) != 1 || ranges[0] != (store.BlockRange{Start: start, End: ^uint64(0)}) {
		t.Fatalf("ranges=%+v", ranges)
	}
}

func TestFatalClassificationStopsUnsafeReorgs(t *testing.T) {
	t.Parallel()
	for _, err := range []error{indexer.ErrFinalizedReorg, indexer.ErrReorgTooDeep, indexer.ErrNoCommonAncestor, indexer.ErrSourceInconsistent} {
		if !isFatal(err) {
			t.Fatalf("%v must be fatal", err)
		}
	}
	if isFatal(indexer.ErrGap) {
		t.Fatal("transient gap must be retried")
	}
}

func TestRunKeepsFatalSafetyHaltObservableUntilOperatorStopsIt(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryRepository()
	observer := &recordingSyncObserver{halted: make(chan string, 1)}
	service := &Service{
		ChainID: "1", StartBlock: 0, PollInterval: time.Hour,
		Source: fatalHeadSource{err: indexer.ErrFinalizedReorg}, Repository: repository,
		Canonicalizer: &indexer.Canonicalizer{ChainID: "1", Repository: repository},
		Tracker:       &Tracker{}, Observer: observer,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	select {
	case reason := <-observer.halted:
		if reason != "finalized_reorg" {
			t.Fatalf("halt reason=%q", reason)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("fatal sync halt was not reported")
	}
	select {
	case err := <-done:
		cancel()
		t.Fatalf("fatal sync halt exited before operator cancellation: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("halt shutdown error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("halted sync service did not stop on operator cancellation")
	}
}

type fatalHeadSource struct{ err error }

func (source fatalHeadSource) Head(context.Context) (uint64, error) {
	return 0, source.err
}

func (fatalHeadSource) BundleByNumber(context.Context, ethrpc.Purpose, uint64) (ethrpc.Bundle, error) {
	return ethrpc.Bundle{}, errors.New("unexpected bundle request")
}

func (fatalHeadSource) Finality(context.Context) (*store.BlockRef, *store.BlockRef, error) {
	return nil, nil, nil
}

type recordingSyncObserver struct {
	halted chan string
}

func (*recordingSyncObserver) SetSyncLag(uint64)   {}
func (*recordingSyncObserver) ObserveReorg(uint64) {}
func (observer *recordingSyncObserver) RecordSyncHalt(reason string) {
	observer.halted <- reason
}

type cancelSource struct{ fakeSource }

func (*cancelSource) BundleByNumber(ctx context.Context, _ ethrpc.Purpose, _ uint64) (ethrpc.Bundle, error) {
	<-ctx.Done()
	return ethrpc.Bundle{}, ctx.Err()
}

func TestSyncRangeCancellationDoesNotDeadlockDispatcher(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	service := &Service{Workers: 1, Source: &cancelSource{}}
	done := make(chan error, 1)
	go func() { done <- service.syncRange(ctx, 0, 100, 100) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("sync range dispatcher deadlocked after cancellation")
	}
}

func TestRunWakeTriggersEarlyAuthoritativePoll(t *testing.T) {
	t.Parallel()
	wake := make(chan struct{}, 1)
	service, source := runnableService(time.Hour, wake)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	waitForHeadCall(t, source.headCalls)
	wake <- struct{}{}
	waitForHeadCall(t, source.headCalls)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("sync service did not stop")
	}
}

func TestRunPollingContinuesWhenWakeMessagesAreMissed(t *testing.T) {
	t.Parallel()
	service, source := runnableService(10*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	waitForHeadCall(t, source.headCalls)
	// No wake signal is sent. The regular polling interval remains the
	// authoritative fallback for disconnects and lost WebSocket messages.
	waitForHeadCall(t, source.headCalls)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("sync service did not stop")
	}
}

func runnableService(interval time.Duration, wake <-chan struct{}) (*Service, *notifyingSource) {
	repository := store.NewMemoryRepository()
	source := &notifyingSource{
		fakeSource: &fakeSource{head: 0, bundles: testChain(1)},
		headCalls:  make(chan struct{}, 4),
	}
	canonicalizer := &indexer.Canonicalizer{
		ChainID: "1", StartBlock: 0, MaxReorgDepth: 128,
		Repository: repository,
	}
	return &Service{
		ChainID: "1", PollInterval: interval,
		Source: source, Repository: repository, Canonicalizer: canonicalizer,
		Tracker: &Tracker{}, Wake: wake,
	}, source
}

func waitForHeadCall(t *testing.T, calls <-chan struct{}) {
	t.Helper()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("authoritative head was not polled")
	}
}

func testChain(length int) map[uint64]ethrpc.Bundle {
	bundles := make(map[uint64]ethrpc.Bundle, length)
	parent := testHash(0)
	for index := range length {
		hash := testHash(byte(index + 1))
		bundles[uint64(index)] = testBundle(uint64(index), hash, parent)
		parent = hash
	}
	return bundles
}

func syncTestChain(length int, firstHash byte) []ethrpc.Bundle {
	chain := make([]ethrpc.Bundle, length)
	parent := testHash(0)
	for index := range length {
		hash := testHash(firstHash + byte(index))
		chain[index] = testBundle(uint64(index), hash, parent)
		parent = hash
	}
	return chain
}

func testBundle(number uint64, hash, parent ethrpc.Hash) ethrpc.Bundle {
	quantity := ethrpc.QuantityFromUint64(number)
	zero := testHash(0)
	return ethrpc.Bundle{Block: ethrpc.Block{
		Number: &quantity, Hash: &hash, ParentHash: parent,
		Sha3Uncles: zero, TransactionsRoot: zero, StateRoot: zero, ReceiptsRoot: zero,
		ExtraData: "0x", GasLimit: ethrpc.QuantityFromUint64(30_000_000),
		GasUsed: ethrpc.QuantityFromUint64(0), Timestamp: ethrpc.QuantityFromUint64(1_700_000_000 + number),
		Transactions: []ethrpc.TransactionRef{}, Uncles: []ethrpc.Hash{},
	}}
}

func testHash(value byte) ethrpc.Hash {
	bytes := make([]byte, 32)
	bytes[31] = value
	parsed, err := ethrpc.ParseHash(ethrpc.DataFromBytes(bytes).String())
	if err != nil {
		panic(err)
	}
	return parsed
}
