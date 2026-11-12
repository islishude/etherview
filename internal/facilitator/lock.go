package facilitator

import (
	"context"
	"errors"
	"time"

	dbgen "github.com/islishude/etherview/internal/db/gen"
)

// Reads use the pool. Every journal mutation executes on the physical session
// that owns the advisory lock. A disconnected owner can never publish via a
// surviving pooled connection. No SQL transaction spans external work.
type lockedJournal struct {
	settlementJournal
	service *Service
}

func (db *lockedJournal) write(ctx context.Context, operation func(*dbgen.Queries) (int64, error)) (int64, error) {
	s := db.service
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if !s.active.Load() {
		return 0, errors.New("facilitator_inactive")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	count, err := operation(dbgen.New(s.lock))
	if err != nil && s.lock.IsClosed() {
		s.loseLock()
	}
	return count, err
}

func (s *Service) loseLock() {
	s.active.Store(false)
	s.fatalOnce.Do(func() {
		if s.fatal != nil {
			close(s.fatal)
		}
	})
}

// Fatal closes exactly once on irreversible loss of the singleton session.
func (s *Service) Fatal() <-chan struct{} { return s.fatal }

func (s *Service) monitorLock(ctx context.Context) {
	defer s.workers.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.guard(ctx) != nil && !s.active.Load() {
				return
			}
		}
	}
}

func (db *lockedJournal) FacilitatorAbortUnsigned(ctx context.Context, id string, response []byte) (int64, error) {
	return db.write(ctx, func(q *dbgen.Queries) (int64, error) { return q.FacilitatorAbortUnsigned(ctx, id, response) })
}

func (db *lockedJournal) FacilitatorSucceed(ctx context.Context, id string, response, evidence []byte) (int64, error) {
	return db.write(ctx, func(q *dbgen.Queries) (int64, error) { return q.FacilitatorSucceed(ctx, id, response, evidence) })
}

func (db *lockedJournal) FacilitatorFinish(ctx context.Context, id, state string, response []byte) (int64, error) {
	return db.write(ctx, func(q *dbgen.Queries) (int64, error) { return q.FacilitatorFinish(ctx, id, state, response) })
}

func (db *lockedJournal) FacilitatorReconcileUnsigned(ctx context.Context, id string) (int64, error) {
	return db.write(ctx, func(q *dbgen.Queries) (int64, error) { return q.FacilitatorReconcileUnsigned(ctx, id) })
}

func (db *lockedJournal) FacilitatorConfirmTransaction(ctx context.Context, id string, hash []byte) (int64, error) {
	return db.write(ctx, func(q *dbgen.Queries) (int64, error) { return q.FacilitatorConfirmTransaction(ctx, id, hash) })
}

func (db *lockedJournal) FacilitatorConfirmBroadcast(ctx context.Context, id string, hash []byte) (int64, error) {
	return db.write(ctx, func(q *dbgen.Queries) (int64, error) { return q.FacilitatorConfirmBroadcast(ctx, id, hash) })
}

func (db *lockedJournal) FacilitatorScanCursor(ctx context.Context, id string, cursor []byte) (int64, error) {
	return db.write(ctx, func(q *dbgen.Queries) (int64, error) { return q.FacilitatorScanCursor(ctx, id, cursor) })
}

func (db *lockedJournal) FacilitatorReserve(ctx context.Context, arg dbgen.FacilitatorReserveParams) (int64, error) {
	return db.write(ctx, func(q *dbgen.Queries) (int64, error) { return q.FacilitatorReserve(ctx, arg) })
}

func (db *lockedJournal) FacilitatorRejectUnsubmitted(ctx context.Context, arg dbgen.FacilitatorRejectUnsubmittedParams) (int64, error) {
	return db.write(ctx, func(q *dbgen.Queries) (int64, error) { return q.FacilitatorRejectUnsubmitted(ctx, arg) })
}

func (db *lockedJournal) FacilitatorSaveSigned(ctx context.Context, arg dbgen.FacilitatorSaveSignedParams) (int64, error) {
	return db.write(ctx, func(q *dbgen.Queries) (int64, error) { return q.FacilitatorSaveSigned(ctx, arg) })
}

func (db *lockedJournal) FacilitatorTouchRecovery(ctx context.Context, id string) (int64, error) {
	return db.write(ctx, func(q *dbgen.Queries) (int64, error) { return q.FacilitatorTouchRecovery(ctx, id) })
}
