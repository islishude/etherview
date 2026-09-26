package watchlist

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Service) Name() string { return "watchlist-notifications" }

// Run owns no API secrets; every replica resumes independent durable work.
func (s *Service) Run(ctx context.Context) error {
	cleanupAt := time.Time{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		workCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		busy, err := s.ProcessOne(workCtx)
		if time.Now().After(cleanupAt) {
			if e := s.Cleanup(workCtx); e != nil && err == nil {
				err = e
			}
			cleanupAt = time.Now().Add(time.Minute)
		}
		cancel()
		delay := time.Duration(0)
		if !busy {
			delay = time.Second
		}
		if err != nil {
			delay = 5 * time.Second
			slog.WarnContext(ctx, "notification processing failed", "component", s.Name(), "event", "watchlist_processing_failed", "error_code", "watchlist_processing_unavailable")
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (s *Service) ProcessOne(ctx context.Context) (bool, error) {
	lease := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	work, err := dbgen.New(s.db).WatchClaimWork(ctx, lease, s.numeric)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	err = dbaccess.WithTransaction(ctx, s.db, func(q *dbgen.Queries) error {
		// Producers take the same chain lock before touching work. Claim is already committed.
		if e := q.StoreLegacyLockChainStatement1(ctx, new(s.chain)); e != nil {
			return e
		}
		current, e := q.WatchLockWork(ctx, s.numeric, work.BlockHash, lease)
		if e != nil {
			return e
		}
		// A producer may have advanced the generation since claim; consume its reset cursor.
		page, e := q.WatchWorkPage(ctx, dbgen.WatchWorkPageParams{ChainID: s.numeric, BlockNumber: current.BlockNumber, BlockHash: current.BlockHash, AfterWatch: current.AfterWatch, AfterSource: current.AfterSource})
		if e != nil {
			return e
		}
		// Progress-only rows cover ineligible followers and source-end markers.
		for _, item := range page {
			if item.UserID.Valid {
				e = q.WatchPublishNotification(ctx, dbgen.WatchPublishNotificationParams{WatchID: item.WatchID, UserID: item.UserID, ChainID: s.numeric, BlockNumber: current.BlockNumber, BlockHash: current.BlockHash, SourceKey: item.SourceKey, SourceKind: item.SourceKind, SourceGeneration: item.SourceGeneration, Activity: item.Activity})
				if e != nil {
					return e
				}
			}
			current.AfterWatch = item.WatchID
			current.AfterSource = item.SourceKey
		}
		count, e := q.WatchFinishPage(ctx, dbgen.WatchFinishPageParams{ChainID: s.numeric, BlockHash: current.BlockHash, Generation: current.Generation, LeaseToken: lease, AfterWatch: current.AfterWatch, AfterSource: current.AfterSource, Finished: len(page) < 200 && (len(page) == 0 || !page[0].SourcePageFull)})
		if e != nil {
			return e
		}
		if count != 1 {
			return ErrUnavailable
		}
		return nil
	})
	return true, err
}
func (s *Service) Cleanup(ctx context.Context) error {
	q := dbgen.New(s.db)
	for _, step := range []func(context.Context) error{q.WatchCleanupNotifications, q.WatchCleanupWork, q.WatchCleanupDeleted, q.ExportCleanup} {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}
