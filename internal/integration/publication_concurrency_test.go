//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/testdbguard"
)

func TestPublicationReplayAndRenewConcurrent(t *testing.T) {
	for _, stage := range []enrich.StageID{enrich.NativeBalanceStage, enrich.ProxyStage, enrich.TokenStage, enrich.TraceStage, enrich.StatsStage} {
		t.Run(stage.String(), func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			repo, err := store.NewPostgresRepository(db)
			if err != nil {
				t.Fatal(err)
			}
			block := testBundle(0, testHash(196000), testHash(0), testHash(196001), "publication-concurrency")
			commitCanonical(t, ctx, repo, block)
			execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at=now()`)
			ref := mustBlockRef(t, block)
			word, err := enrich.ParseWord(ref.Hash.String())
			if err != nil {
				t.Fatal(err)
			}
			queue, err := enrich.NewPostgresJobQueue(db)
			if err != nil {
				t.Fatal(err)
			}
			request := enrich.EnqueueRequest{Stage: stage, ChainID: "1", BlockHash: word, BlockNumber: ref.Number}
			if _, err := queue.Enqueue(ctx, request); err != nil {
				t.Fatal(err)
			}
			lease, found, err := queue.Claim(ctx, "concurrent-publisher", []enrich.StageID{stage}, time.Minute)
			if err != nil || !found {
				t.Fatalf("claim: %v %v", found, err)
			}
			allowLeaseLost := func(err error) error {
				if errors.Is(err, enrich.ErrLeaseLost) {
					return nil
				}
				return err
			}
			if err := testdbguard.Concurrent(ctx, map[string]func(context.Context) error{
				"finish": func(ctx context.Context) error {
					return allowLeaseLost(queue.Finish(ctx, lease, enrich.StageResult{State: enrich.ResultUnavailable, Error: "fixture unavailable"}))
				},
				"renew": func(ctx context.Context) error { return allowLeaseLost(queue.Renew(ctx, lease, time.Minute)) },
				"replay": func(ctx context.Context) error {
					request.Replay = enrich.ReplaySource{Kind: "concurrent", Key: "next"}
					_, err := queue.Enqueue(ctx, request)
					return err
				},
			}); err != nil {
				t.Fatal(err)
			}
			next, found, err := queue.Claim(ctx, "next-publisher", []enrich.StageID{stage}, time.Minute)
			if err != nil || !found || next.Job.Generation != 2 {
				t.Fatalf("next generation: %+v found=%v err=%v", next, found, err)
			}
			if err := queue.Finish(ctx, next, enrich.StageResult{State: enrich.ResultUnavailable, Error: "fixture unavailable"}); err != nil {
				t.Fatal(err)
			}
			assertPublishedTerminalNoJournal(t, ctx, db, next.Job.ID, 2, enrich.ResultUnavailable, "fixture unavailable")
		})
	}
}

// Each public Enqueue call owns one transaction. Exercise replay streams with
// equal, reversed and partially overlapping job order without inventing a
// caller-owned multi-job transaction that the production entry point lacks.
func TestConcurrentReplayStreamsPreserveGenerations(t *testing.T) {
	for _, test := range []struct {
		name   string
		second []int
	}{
		{"same", []int{0, 1, 2}}, {"reverse", []int{2, 1, 0}}, {"partial", []int{1, 2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			repo, err := store.NewPostgresRepository(db)
			if err != nil {
				t.Fatal(err)
			}
			queue, err := enrich.NewPostgresJobQueue(db)
			if err != nil {
				t.Fatal(err)
			}
			requests := make([]enrich.EnqueueRequest, 3)
			parent := testHash(0)
			for i := range 3 {
				block := testBundle(uint64(i), testHash(uint64(197000+i)), parent, testHash(uint64(197100+i)), "replay-stream")
				commitCanonical(t, ctx, repo, block)
				parent = mustBlockRef(t, block).Hash
				hash, err := enrich.ParseWord(parent.String())
				if err != nil {
					t.Fatal(err)
				}
				requests[i] = enrich.EnqueueRequest{Stage: enrich.ProxyStage, ChainID: "1", BlockHash: hash, BlockNumber: uint64(i)}
				if _, err := queue.Enqueue(ctx, requests[i]); err != nil {
					t.Fatal(err)
				}
			}
			execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at=now()`)
			operations := map[string]func(context.Context) error{}
			for stream, order := range map[string][]int{"first": {0, 1, 2}, "second": test.second} {
				operations[stream] = func(ctx context.Context) error {
					for _, i := range order {
						request := requests[i]
						request.Replay = enrich.ReplaySource{Kind: "ordered-stream", Key: stream}
						if _, err := queue.Enqueue(ctx, request); err != nil {
							return err
						}
					}
					return nil
				}
			}
			if err := testdbguard.Concurrent(ctx, operations); err != nil {
				t.Fatal(err)
			}
			for range 3 {
				lease, found, err := queue.Claim(ctx, "stream-publisher", []enrich.StageID{enrich.ProxyStage}, time.Minute)
				if err != nil || !found {
					t.Fatalf("claim: %v %v", found, err)
				}
				want := uint64(3)
				if test.name == "partial" && lease.Job.BlockNumber == 0 {
					want = 2
				}
				if uint64(lease.Job.Generation) != want {
					t.Fatalf("block %d generation %d want %d", lease.Job.BlockNumber, lease.Job.Generation, want)
				}
				if err := queue.Finish(ctx, lease, enrich.StageResult{State: enrich.ResultUnavailable, Error: "fixture unavailable"}); err != nil {
					t.Fatal(err)
				}
				assertPublishedTerminalNoJournal(t, ctx, db, lease.Job.ID, int64(want), enrich.ResultUnavailable, "fixture unavailable")
			}
		})
	}
}
