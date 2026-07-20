// Package syncer implements independent authoritative live-head and durable
// historical-coverage lanes. WebSocket notifications wake only the live lane;
// PostgreSQL coverage and range leases are the restart-safe backfill truth.
package syncer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/events"
	"github.com/islishude/etherview/internal/indexer"
	"github.com/islishude/etherview/internal/store"
)

const (
	defaultCycleBatch    = uint64(256)
	maximumBackfillBatch = uint64(256)
)

// Source separates scheduling from JSON-RPC transport. PurposeHead calls are
// never issued by a historical worker, so a blocked history endpoint cannot
// consume a WebSocket wake or delay the next authoritative head poll.
type Source interface {
	Head(context.Context) (uint64, error)
	BundleByNumber(context.Context, ethrpc.Purpose, uint64) (ethrpc.Bundle, error)
	Finality(context.Context) (safe, finalized *store.BlockRef, err error)
}

type Observer interface {
	SetSyncLag(uint64)
	ObserveReorg(uint64)
	RecordSyncHalt(string)
}

type StatusRecorder interface {
	RecordStatus(context.Context, events.SyncStatus) (events.Event, error)
}

type Tracker struct {
	latest           atomic.Uint64
	indexed          atomic.Uint64
	highest          atomic.Uint64
	latestKnown      atomic.Bool
	indexedKnown     atomic.Bool
	highestKnown     atomic.Bool
	ready            atomic.Bool
	backfillComplete atomic.Bool
	lastNanos        atomic.Int64
	errMu            sync.RWMutex
	lastErr          string
}

type Snapshot struct {
	Latest           uint64
	Indexed          uint64
	HighestCovered   uint64
	Known            bool
	IndexedKnown     bool
	HighestKnown     bool
	Ready            bool
	BackfillComplete bool
	LastPollAt       time.Time
	LastError        string
}

func (t *Tracker) Snapshot() Snapshot {
	if t == nil {
		return Snapshot{}
	}
	t.errMu.RLock()
	lastErr := t.lastErr
	t.errMu.RUnlock()
	snapshot := Snapshot{
		Latest: t.latest.Load(), Indexed: t.indexed.Load(), HighestCovered: t.highest.Load(),
		Known: t.latestKnown.Load(), IndexedKnown: t.indexedKnown.Load(), HighestKnown: t.highestKnown.Load(),
		Ready: t.ready.Load(), BackfillComplete: t.backfillComplete.Load(), LastError: lastErr,
	}
	if nanos := t.lastNanos.Load(); nanos != 0 {
		snapshot.LastPollAt = time.Unix(0, nanos).UTC()
	}
	return snapshot
}

func (t *Tracker) record(status events.SyncStatus) {
	if t == nil {
		return
	}
	t.latest.Store(status.Latest)
	t.indexed.Store(status.Indexed)
	t.highest.Store(status.HighestCovered)
	t.latestKnown.Store(status.LatestKnown)
	t.indexedKnown.Store(status.IndexedKnown)
	t.highestKnown.Store(status.HighestCoveredKnown)
	t.ready.Store(status.Ready)
	t.backfillComplete.Store(status.BackfillComplete)
	t.lastNanos.Store(status.PolledAt.UTC().UnixNano())
	t.errMu.Lock()
	t.lastErr = status.ErrorCode
	t.errMu.Unlock()
}

type Service struct {
	ChainID       string
	StartBlock    uint64
	PollInterval  time.Duration
	Workers       int
	CycleBatch    uint64
	WorkerID      string
	LeaseDuration time.Duration
	Source        Source
	Repository    store.Repository
	Canonicalizer *indexer.Canonicalizer
	Status        StatusRecorder
	EventWake     func()
	Tracker       *Tracker
	Observer      Observer
	Logger        *slog.Logger
	Wake          <-chan struct{}
	Now           func() time.Time

	latest          atomic.Uint64
	latestKnown     atomic.Bool
	statusMu        sync.Mutex
	liveErrorCode   string
	backfillErrCode string
}

func (s *Service) Name() string { return "core-sync" }

func (s *Service) Run(ctx context.Context) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.Repository.ConfigureIndex(ctx, s.ChainID, s.StartBlock); err != nil {
		return fmt.Errorf("configure core index coverage: %w", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	backfillWake := make(chan struct{}, s.workerCount())
	results := make(chan error, s.workerCount()+1)
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		results <- s.runLive(runCtx, backfillWake)
	}()
	for index := 0; index < s.workerCount(); index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- s.runBackfill(runCtx, backfillWake, s.backfillOwner(index))
		}()
	}
	err := <-results
	cancel()
	wait.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if isFatal(err) {
		reason := cycleErrorCode(err)
		if s.Observer != nil {
			s.Observer.RecordSyncHalt(reason)
		}
		s.logger().ErrorContext(ctx, "core sync halted on canonical safety boundary; operator repair and restart are required",
			"error_code", reason, "error_type", fmt.Sprintf("%T", err))
		<-ctx.Done()
		return ctx.Err()
	}
	return err
}

// Cycle is the deterministic single-step test and operator path. Production
// Run does not call it: live polling and historical work run independently.
func (s *Service) Cycle(ctx context.Context) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.Repository.ConfigureIndex(ctx, s.ChainID, s.StartBlock); err != nil {
		return fmt.Errorf("configure core index coverage: %w", err)
	}
	liveErr := s.liveOnce(ctx)
	statusErr := s.recordRuntimeStatus(ctx, "live", liveErr)
	if liveErr != nil {
		if isFatal(liveErr) && s.Observer != nil {
			s.Observer.RecordSyncHalt(cycleErrorCode(liveErr))
		}
		return errors.Join(liveErr, statusErr)
	}
	_, backfillErr := s.backfillOnce(ctx, s.backfillOwner(0))
	statusErr = errors.Join(statusErr, s.recordRuntimeStatus(ctx, "backfill", backfillErr))
	if isFatal(backfillErr) && s.Observer != nil {
		s.Observer.RecordSyncHalt(cycleErrorCode(backfillErr))
	}
	return errors.Join(backfillErr, statusErr)
}

func (s *Service) runLive(ctx context.Context, backfillWake chan<- struct{}) error {
	logger := s.logger()
	for {
		err := s.liveOnce(ctx)
		if statusErr := s.recordRuntimeStatus(ctx, "live", err); statusErr != nil && ctx.Err() == nil {
			logger.WarnContext(ctx, "core sync status persistence failed; polling will retry",
				"error_code", "status_persistence_failed", "error_type", fmt.Sprintf("%T", statusErr))
		}
		s.notifyBackfill(backfillWake)
		if err != nil && ctx.Err() == nil {
			if isFatal(err) {
				return err
			}
			logger.WarnContext(ctx, "core sync cycle failed; polling will retry",
				"error_code", cycleErrorCode(err), "error_type", fmt.Sprintf("%T", err))
		}
		if err := s.waitLive(ctx); err != nil {
			return err
		}
	}
}

func (s *Service) runBackfill(ctx context.Context, wake <-chan struct{}, owner string) error {
	logger := s.logger()
	for {
		didWork, err := s.backfillOnce(ctx, owner)
		if statusErr := s.recordRuntimeStatus(ctx, "backfill", err); statusErr != nil && ctx.Err() == nil {
			logger.WarnContext(ctx, "core backfill status persistence failed; retrying",
				"error_code", "status_persistence_failed", "error_type", fmt.Sprintf("%T", statusErr))
		}
		if err != nil && ctx.Err() == nil {
			if isFatal(err) {
				return err
			}
			logger.WarnContext(ctx, "core backfill range failed; retrying from durable coverage",
				"error_code", "backfill_cycle_failed", "error_type", fmt.Sprintf("%T", err))
		}
		if didWork && err == nil {
			continue
		}
		if err := s.waitBackfill(ctx, wake); err != nil {
			return err
		}
	}
}

func (s *Service) liveOnce(ctx context.Context) error {
	head, err := s.Source.Head(ctx)
	if err != nil {
		return fmt.Errorf("read authoritative head: %w", err)
	}
	s.latest.Store(head)
	s.latestKnown.Store(true)
	if head < s.StartBlock {
		return fmt.Errorf("configured start block %d is above current head %d", s.StartBlock, head)
	}
	bundle, err := s.Source.BundleByNumber(ctx, ethrpc.PurposeHead, head)
	if err != nil {
		return fmt.Errorf("fetch authoritative head %d: %w", head, err)
	}
	reference, err := store.RefFromBundle(bundle)
	if err != nil {
		return fmt.Errorf("authoritative head bundle identity mismatch at %d: %w", head, err)
	}
	if reference.Number != head {
		return fmt.Errorf("authoritative head bundle height is %d, expected %d", reference.Number, head)
	}

	tip, tipExists, err := s.Repository.CanonicalTip(ctx, s.ChainID)
	if err != nil {
		return fmt.Errorf("read canonical tip: %w", err)
	}
	canonical, canonicalExists, err := s.Repository.CanonicalBlock(ctx, s.ChainID, head)
	if err != nil {
		return fmt.Errorf("read authoritative canonical height: %w", err)
	}
	if canonicalExists && canonical.Hash.Equal(reference.Hash) {
		if tipExists && tip.Number > head {
			result, applyErr := s.Canonicalizer.ApplyHead(ctx, bundle)
			if applyErr != nil {
				return applyErr
			}
			s.publish(result)
		}
		return s.updateAvailableFinality(ctx)
	}
	coverage, configured, err := s.Repository.Coverage(ctx, s.ChainID)
	if err != nil {
		return fmt.Errorf("read canonical coverage before live commit: %w", err)
	}
	if configured && tipExists && shouldResolveShallowLiveGap(coverage, tip, reference, s.maxReorgDepth()) {
		result, applyErr := s.Canonicalizer.ApplyHead(ctx, bundle)
		if applyErr != nil {
			return applyErr
		}
		s.publish(result)
		return s.updateAvailableFinality(ctx)
	}

	_, err = s.Repository.CommitCanonicalSegment(ctx, s.ChainID, []ethrpc.Bundle{bundle})
	if err == nil {
		s.signalEvents()
		return s.updateAvailableFinality(ctx)
	}
	if !errors.Is(err, store.ErrConflict) {
		return fmt.Errorf("commit authoritative live head %d: %w", head, err)
	}
	result, applyErr := s.Canonicalizer.ApplyHead(ctx, bundle)
	if applyErr != nil {
		return applyErr
	}
	s.publish(result)
	return s.updateAvailableFinality(ctx)
}

func (s *Service) backfillOnce(ctx context.Context, owner string) (bool, error) {
	if !s.latestKnown.Load() {
		return false, nil
	}
	latest := s.latest.Load()
	if latest < s.StartBlock {
		return false, nil
	}
	coverage, exists, err := s.Repository.Coverage(ctx, s.ChainID)
	if err != nil {
		return false, fmt.Errorf("read durable canonical coverage: %w", err)
	}
	if !exists {
		return false, errors.New("core index coverage is not configured")
	}
	for _, target := range missingBackfillRanges(coverage, latest, s.batch()) {
		lease, claimed, err := s.Repository.ClaimBackfillRange(
			ctx, s.ChainID, target, owner, s.now(), s.leaseDuration(),
		)
		if err != nil {
			return false, fmt.Errorf("claim backfill range %d-%d: %w", target.Start, target.End, err)
		}
		if !claimed {
			continue
		}
		return true, s.processBackfillLease(ctx, lease)
	}
	return false, nil
}

func (s *Service) processBackfillLease(ctx context.Context, lease store.BackfillLease) error {
	bundles := make([]ethrpc.Bundle, 0, lease.Range.End-lease.Range.Start+1)
	for number := lease.Range.Start; ; number++ {
		if s.now().Add(s.leaseDuration() / 2).After(lease.ExpiresAt) {
			renewed, err := s.Repository.RenewBackfillRange(ctx, lease, s.now(), s.leaseDuration())
			if err != nil {
				return fmt.Errorf("renew backfill range %d-%d: %w", lease.Range.Start, lease.Range.End, err)
			}
			lease = renewed
		}
		bundle, err := s.Source.BundleByNumber(ctx, ethrpc.PurposeHistory, number)
		if err != nil {
			_ = s.Repository.ReleaseBackfillRange(ctx, lease)
			return fmt.Errorf("fetch historical block %d: %w", number, err)
		}
		bundles = append(bundles, bundle)
		if number == lease.Range.End {
			break
		}
	}
	if _, err := s.Repository.CommitCanonicalSegment(ctx, s.ChainID, bundles); err != nil {
		_ = s.Repository.ReleaseBackfillRange(ctx, lease)
		if errors.Is(err, store.ErrConflict) {
			result, resolveErr := s.resolveAuthoritativeBackfillConflict(ctx)
			if resolveErr == nil && result.Disposition != indexer.DispositionAlreadyKnown {
				s.publish(result)
				return nil
			}
			if resolveErr != nil {
				return fmt.Errorf("resolve historical boundary conflict from authoritative head: %w", resolveErr)
			}
		}
		return fmt.Errorf("commit historical range %d-%d: %w", lease.Range.Start, lease.Range.End, err)
	}
	s.signalEvents()
	if err := s.Repository.CompleteBackfillRange(ctx, lease); err != nil && !errors.Is(err, store.ErrLeaseLost) {
		return fmt.Errorf("complete historical range %d-%d: %w", lease.Range.Start, lease.Range.End, err)
	}
	return nil
}

func (s *Service) resolveAuthoritativeBackfillConflict(ctx context.Context) (indexer.ApplyResult, error) {
	head, err := s.Source.Head(ctx)
	if err != nil {
		return indexer.ApplyResult{}, fmt.Errorf("refresh authoritative head after backfill conflict: %w", err)
	}
	s.latest.Store(head)
	s.latestKnown.Store(true)
	bundle, err := s.Source.BundleByNumber(ctx, ethrpc.PurposeHead, head)
	if err != nil {
		return indexer.ApplyResult{}, fmt.Errorf("fetch authoritative head %d after backfill conflict: %w", head, err)
	}
	reference, err := store.RefFromBundle(bundle)
	if err != nil {
		return indexer.ApplyResult{}, fmt.Errorf("decode authoritative conflict head: %w", err)
	}
	if reference.Number != head {
		return indexer.ApplyResult{}, fmt.Errorf("authoritative conflict head height is %d, expected %d", reference.Number, head)
	}
	return s.Canonicalizer.ApplyHead(ctx, bundle)
}

// syncRange remains a bounded cancellation-safe helper for focused transport
// tests. Production backfill uses durable leases and CommitCanonicalSegment.
func (s *Service) syncRange(ctx context.Context, start, end, head uint64) error {
	if end < start {
		return nil
	}
	count := end - start + 1
	bundles := make([]ethrpc.Bundle, count)
	errs := make([]error, count)
	jobs := make(chan uint64)
	workers := s.workerCount()
	if uint64(workers) > count {
		workers = int(count)
	}
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for number := range jobs {
				purpose := ethrpc.PurposeHistory
				if number == head {
					purpose = ethrpc.PurposeHead
				}
				bundle, err := s.Source.BundleByNumber(ctx, purpose, number)
				index := number - start
				bundles[index], errs[index] = bundle, err
			}
		}()
	}
	dispatchCancelled := false
	for number := start; number <= end; number++ {
		select {
		case jobs <- number:
		case <-ctx.Done():
			dispatchCancelled = true
		}
		if dispatchCancelled || number == ^uint64(0) {
			break
		}
	}
	close(jobs)
	wait.Wait()
	if dispatchCancelled {
		return ctx.Err()
	}
	for index, err := range errs {
		if err != nil {
			return fmt.Errorf("fetch block %d: %w", start+uint64(index), err)
		}
	}
	if s.Repository == nil {
		return errors.New("sync range repository is nil")
	}
	if _, err := s.Repository.CommitCanonicalSegment(ctx, s.ChainID, bundles); err != nil {
		return err
	}
	s.signalEvents()
	return nil
}

func (s *Service) updateAvailableFinality(ctx context.Context) error {
	safe, finalized, err := s.Source.Finality(ctx)
	if err != nil {
		return fmt.Errorf("read safe/finalized heads: %w", err)
	}
	if safe == nil && finalized == nil {
		return nil
	}
	for _, requested := range []*store.BlockRef{safe, finalized} {
		if requested == nil {
			continue
		}
		canonical, exists, err := s.Repository.CanonicalBlock(ctx, s.ChainID, requested.Number)
		if err != nil {
			return fmt.Errorf("check finality coverage: %w", err)
		}
		if !exists || !canonical.Hash.Equal(requested.Hash) {
			// Sparse live coverage is expected while history is filling. The next
			// authoritative poll retries once both finality identities are stored.
			return nil
		}
	}
	if err := s.Canonicalizer.UpdateFinality(ctx, safe, finalized); err != nil {
		return fmt.Errorf("update finality: %w", err)
	}
	return nil
}

func (s *Service) recordRuntimeStatus(ctx context.Context, lane string, laneErr error) error {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	code := cycleErrorCode(laneErr)
	if lane == "live" {
		s.liveErrorCode = code
	} else {
		if code != "" {
			code = "backfill_cycle_failed"
		}
		s.backfillErrCode = code
	}
	coverage, exists, err := s.Repository.Coverage(ctx, s.ChainID)
	if err != nil {
		return fmt.Errorf("read coverage for runtime status: %w", err)
	}
	if !exists {
		return errors.New("core index coverage disappeared")
	}
	status := events.SyncStatus{
		Latest: s.latest.Load(), LatestKnown: s.latestKnown.Load(),
		PolledAt: s.now(), ErrorCode: s.liveErrorCode,
	}
	if status.ErrorCode == "" {
		status.ErrorCode = s.backfillErrCode
	}
	if coverage.Contiguous != nil {
		status.Indexed = coverage.Contiguous.Number
		status.IndexedKnown = true
	}
	if coverage.Highest != nil {
		status.HighestCovered = coverage.Highest.Number
		status.HighestCoveredKnown = true
	}
	status.BackfillComplete = status.LatestKnown && status.Latest >= coverage.ConfiguredStart &&
		status.IndexedKnown && status.Indexed >= status.Latest
	status.Ready = status.BackfillComplete && s.liveErrorCode == ""
	s.Tracker.record(status)
	if s.Observer != nil {
		lag := status.Latest
		if status.LatestKnown && status.IndexedKnown {
			lag = 0
			if status.Latest > status.Indexed {
				lag = status.Latest - status.Indexed
			}
		}
		s.Observer.SetSyncLag(lag)
	}
	if s.Status == nil || ctx.Err() != nil {
		return nil
	}
	if _, err := s.Status.RecordStatus(ctx, status); err != nil {
		return fmt.Errorf("persist sync runtime status: %w", err)
	}
	s.signalEvents()
	return nil
}

func missingBackfillRanges(coverage store.CoreCoverage, through, batch uint64) []store.BlockRange {
	if through < coverage.ConfiguredStart {
		return nil
	}
	if batch == 0 || batch > maximumBackfillBatch {
		batch = maximumBackfillBatch
	}
	var result []store.BlockRange
	appendGap := func(start, end uint64) {
		for start <= end {
			chunkEnd := end
			if end-start >= batch {
				chunkEnd = start + batch - 1
			}
			result = append(result, store.BlockRange{Start: start, End: chunkEnd})
			if chunkEnd == ^uint64(0) {
				return
			}
			start = chunkEnd + 1
		}
	}
	cursor := coverage.ConfiguredStart
	for _, covered := range coverage.Ranges {
		if covered.End < cursor {
			continue
		}
		if covered.Start > through {
			break
		}
		if covered.Start > cursor {
			appendGap(cursor, minUint64(covered.Start-1, through))
		}
		if covered.End >= through || covered.End == ^uint64(0) {
			return result
		}
		if covered.End >= cursor {
			cursor = covered.End + 1
		}
	}
	if cursor <= through {
		appendGap(cursor, through)
	}
	return result
}

func (s *Service) publish(result indexer.ApplyResult) {
	if result.Disposition == indexer.DispositionAlreadyKnown {
		return
	}
	if result.Disposition == indexer.DispositionReorganized && s.Observer != nil {
		s.Observer.ObserveReorg(uint64(len(result.Detached)))
	}
	s.signalEvents()
}

func (s *Service) signalEvents() {
	if s.EventWake != nil {
		s.EventWake()
	}
}

func (s *Service) notifyBackfill(wake chan<- struct{}) {
	for index := 0; index < s.workerCount(); index++ {
		select {
		case wake <- struct{}{}:
		default:
			return
		}
	}
}

func (s *Service) waitLive(ctx context.Context) error {
	timer := time.NewTimer(s.pollInterval())
	defer stopTimer(timer)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	case <-s.Wake:
		return nil
	}
}

func (s *Service) waitBackfill(ctx context.Context, wake <-chan struct{}) error {
	timer := time.NewTimer(s.pollInterval())
	defer stopTimer(timer)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	case <-wake:
		return nil
	}
}

func stopTimer(timer *time.Timer) {
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

func (s *Service) validate() error {
	if s == nil || s.Source == nil || s.Repository == nil || s.Canonicalizer == nil {
		return errors.New("core sync service is not fully configured")
	}
	if s.ChainID == "" || s.Canonicalizer.ChainID != s.ChainID || s.Canonicalizer.StartBlock != s.StartBlock {
		return errors.New("core sync chain identity or configured start is inconsistent")
	}
	if s.Tracker == nil {
		return errors.New("core sync tracker is nil")
	}
	return nil
}

func (s *Service) pollInterval() time.Duration {
	if s.PollInterval <= 0 {
		return 2 * time.Second
	}
	return s.PollInterval
}

func (s *Service) workerCount() int {
	if s.Workers <= 0 {
		return 1
	}
	return s.Workers
}

func (s *Service) batch() uint64 {
	if s.CycleBatch == 0 || s.CycleBatch > maximumBackfillBatch {
		return defaultCycleBatch
	}
	return s.CycleBatch
}

func (s *Service) leaseDuration() time.Duration {
	if s.LeaseDuration <= 0 {
		return 30 * time.Second
	}
	return s.LeaseDuration
}

func (s *Service) backfillOwner(index int) string {
	base := s.WorkerID
	if base == "" {
		base = "core-backfill"
	}
	suffix := fmt.Sprintf("-%d", index)
	if len(base)+len(suffix) > 128 {
		base = base[:128-len(suffix)]
	}
	return base + suffix
}

func (s *Service) maxReorgDepth() uint64 {
	if s.Canonicalizer.MaxReorgDepth == 0 {
		return 128
	}
	return s.Canonicalizer.MaxReorgDepth
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func isFatal(err error) bool {
	return errors.Is(err, indexer.ErrFinalizedReorg) ||
		errors.Is(err, indexer.ErrReorgTooDeep) ||
		errors.Is(err, indexer.ErrNoCommonAncestor) ||
		errors.Is(err, indexer.ErrSourceInconsistent)
}

func cycleErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, indexer.ErrFinalizedReorg):
		return "finalized_reorg"
	case errors.Is(err, indexer.ErrReorgTooDeep):
		return "reorg_too_deep"
	case errors.Is(err, indexer.ErrNoCommonAncestor):
		return "no_common_ancestor"
	case errors.Is(err, indexer.ErrSourceInconsistent):
		return "source_inconsistent"
	default:
		return "sync_cycle_failed"
	}
}

func minUint64(left, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}

func shouldResolveShallowLiveGap(
	coverage store.CoreCoverage,
	tip, candidate store.BlockRef,
	maxDepth uint64,
) bool {
	if coverage.Contiguous == nil || coverage.Highest == nil ||
		coverage.Contiguous.Number != tip.Number || coverage.Highest.Number != tip.Number ||
		candidate.Number <= tip.Number || tip.Number == ^uint64(0) || candidate.Number == tip.Number+1 {
		return false
	}
	return candidate.Number-tip.Number-1 <= maxDepth
}
