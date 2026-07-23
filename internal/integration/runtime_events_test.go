//go:build integration

package integration_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/events"
	"github.com/islishude/etherview/internal/store"
)

func TestCanonicalTransitionsAndRuntimeStatusShareDurableEventLedger(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	eventStore, err := events.NewPostgresStore(db, "1", events.PostgresOptions{ReplayLimit: 16})
	if err != nil {
		t.Fatal(err)
	}

	genesis := testBundle(0, testHash(800), testHash(0), testHash(8_000), "runtime-genesis")
	oldOne := testBundle(1, testHash(801), testHash(800), testHash(8_001), "runtime-old-one")
	for _, bundle := range []ethrpc.Bundle{genesis, oldOne} {
		commitCanonical(t, ctx, repository, bundle)
	}
	newOne := testBundle(1, testHash(901), testHash(800), testHash(9_001), "runtime-new-one")
	reorg := store.Reorg{
		Ancestor: mustBlockRef(t, genesis), Detached: []store.BlockRef{mustBlockRef(t, oldOne)},
		Attached: []ethrpc.Bundle{newOne}, Checkpoint: store.NewCoreCheckpoint(mustBlockRef(t, newOne)),
		Reason: "integration runtime event",
	}
	if err := repository.ApplyReorg(ctx, "1", reorg); err != nil {
		t.Fatalf("apply event-producing reorg: %v", err)
	}
	statusEvent, err := eventStore.RecordStatus(ctx, events.SyncStatus{
		Latest: 1, Indexed: 1, HighestCovered: 1,
		LatestKnown: true, IndexedKnown: true, HighestCoveredKnown: true,
		BackfillComplete: true, Ready: true, PolledAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("record durable sync status: %v", err)
	}
	status, exists, err := eventStore.Status(ctx)
	if err != nil || !exists || !status.LatestKnown || !status.IndexedKnown || status.Latest != 1 || status.Indexed != 1 || !status.Ready {
		t.Fatalf("durable status = %+v, exists=%t, err=%v", status, exists, err)
	}
	replayed, err := eventStore.Replay(ctx, nil, 16)
	if err != nil {
		t.Fatalf("replay durable events: %v", err)
	}
	if len(replayed) != 4 {
		t.Fatalf("runtime events = %+v", replayed)
	}
	for index, wantType := range []string{"head", "head", "reorg", "status"} {
		if replayed[index].Type != wantType || index > 0 && replayed[index-1].ID >= replayed[index].ID {
			t.Fatalf("runtime events are not ordered head/head/reorg/status: %+v", replayed)
		}
	}
	if replayed[len(replayed)-1].ID != statusEvent.ID {
		t.Fatalf("status event ID = %d, replay tail = %d", statusEvent.ID, replayed[len(replayed)-1].ID)
	}

	// A rejected canonical transition must not leave an event behind because
	// the canonical write and runtime event append share one transaction.
	if err := repository.ApplyReorg(ctx, "1", reorg); err == nil {
		t.Fatal("reapplying stale reorg unexpectedly succeeded")
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM runtime_events WHERE chain_id = 1`, 4)
}

func TestBoundedRuntimeReplayAndIndependentAPIReplicaRelays(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `INSERT INTO chains (chain_id) VALUES (1)`); err != nil {
		t.Fatalf("insert runtime event chain: %v", err)
	}
	firstStore, err := events.NewPostgresStore(db, "1", events.PostgresOptions{ReplayLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	for number := uint64(0); number < 6; number++ {
		if _, err := firstStore.RecordStatus(ctx, events.SyncStatus{
			Latest: number, Indexed: number, HighestCovered: number,
			LatestKnown: true, IndexedKnown: true, HighestCoveredKnown: true,
			BackfillComplete: true, Ready: true, PolledAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("record status %d: %v", number, err)
		}
	}
	recent, err := firstStore.Replay(ctx, nil, 100)
	if err != nil || len(recent) != 3 {
		t.Fatalf("bounded replay = %+v, err=%v", recent, err)
	}
	oldCursor := recent[0].ID - 2
	if _, err := firstStore.Replay(ctx, &oldCursor, 3); !errors.Is(err, events.ErrExpiredCursor) {
		t.Fatalf("expired cursor error = %v", err)
	}
	futureCursor := recent[len(recent)-1].ID + 1
	if _, err := firstStore.Replay(ctx, &futureCursor, 3); !errors.Is(err, events.ErrFutureCursor) {
		t.Fatalf("future cursor error = %v", err)
	}
	validCursor := recent[0].ID - 1
	if replay, err := firstStore.Replay(ctx, &validCursor, 3); err != nil || len(replay) != 3 {
		t.Fatalf("valid bounded replay = %+v, err=%v", replay, err)
	}

	secondStore, err := events.NewPostgresStore(db, "1", events.PostgresOptions{ReplayLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	lastID := recent[len(recent)-1].ID
	firstBroker, err := events.NewDurableBroker(3, firstStore)
	if err != nil {
		t.Fatal(err)
	}
	secondBroker, err := events.NewDurableBroker(3, secondStore)
	if err != nil {
		t.Fatal(err)
	}
	firstEvents, err := firstBroker.Subscribe(ctx, uintString(lastID))
	if err != nil {
		t.Fatal(err)
	}
	secondEvents, err := secondBroker.Subscribe(ctx, uintString(lastID))
	if err != nil {
		t.Fatal(err)
	}
	firstWake, secondWake := make(chan struct{}, 1), make(chan struct{}, 1)
	firstRelay, err := events.NewRelay(firstStore, firstBroker, events.RelayOptions{PollInterval: time.Hour, Wake: firstWake})
	if err != nil {
		t.Fatal(err)
	}
	secondRelay, err := events.NewRelay(secondStore, secondBroker, events.RelayOptions{PollInterval: time.Hour, Wake: secondWake})
	if err != nil {
		t.Fatal(err)
	}
	relayCtx, stopRelays := context.WithCancel(ctx)
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- firstRelay.Run(relayCtx) }()
	go func() { secondDone <- secondRelay.Run(relayCtx) }()
	newEvent, err := firstStore.RecordStatus(ctx, events.SyncStatus{
		Latest: 6, Indexed: 6, HighestCovered: 6,
		LatestKnown: true, IndexedKnown: true, HighestCoveredKnown: true,
		BackfillComplete: true, Ready: true, PolledAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	firstWake <- struct{}{}
	secondWake <- struct{}{}
	assertRuntimeEvent(t, firstEvents, newEvent.ID)
	assertRuntimeEvent(t, secondEvents, newEvent.ID)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM runtime_events WHERE chain_id = 1`, 3)
	stopRelays()
	for name, done := range map[string]<-chan error{"first": firstDone, "second": secondDone} {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s relay shutdown error = %v", name, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s relay did not stop", name)
		}
	}
}

func TestSyncStatusWriterLeaseRejectsLaggingAndFailingReplicas(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `INSERT INTO chains (chain_id) VALUES (1)`); err != nil {
		t.Fatalf("insert runtime status chain: %v", err)
	}
	eventStore, err := events.NewPostgresStore(db, "1", events.PostgresOptions{ReplayLimit: 16})
	if err != nil {
		t.Fatal(err)
	}
	status := func(reporter string, latest uint64, code string) events.SyncStatus {
		return events.SyncStatus{
			Latest: latest, Indexed: latest, HighestCovered: latest,
			LatestKnown: true, IndexedKnown: true, HighestCoveredKnown: true,
			BackfillComplete: true, Ready: code == "", ErrorCode: code,
			PolledAt: time.Now().UTC(), ReporterID: reporter, ReporterLease: time.Minute,
		}
	}

	first, err := eventStore.RecordStatus(ctx, status("sync-a", 10, ""))
	if err != nil || first.ID == 0 {
		t.Fatalf("record first writer status: event=%+v err=%v", first, err)
	}
	lagging, err := eventStore.RecordStatus(ctx, status("sync-b", 9, ""))
	if err != nil || lagging.ID != 0 {
		t.Fatalf("lagging writer result: event=%+v err=%v", lagging, err)
	}
	failing, err := eventStore.RecordStatus(ctx, status("sync-b", 0, "sync_cycle_failed"))
	if err != nil || failing.ID != 0 {
		t.Fatalf("failing non-writer result: event=%+v err=%v", failing, err)
	}
	stored, exists, err := eventStore.Status(ctx)
	if err != nil || !exists || stored.Latest != 10 || stored.ErrorCode != "" || !stored.Ready {
		t.Fatalf("status after rejected peers: status=%+v exists=%t err=%v", stored, exists, err)
	}

	var takeover sync.WaitGroup
	takeover.Add(2)
	higherResult := make(chan events.Event, 1)
	laggingResult := make(chan events.Event, 1)
	errs := make(chan error, 2)
	go func() {
		defer takeover.Done()
		event, recordErr := eventStore.RecordStatus(ctx, status("sync-b", 11, ""))
		higherResult <- event
		errs <- recordErr
	}()
	go func() {
		defer takeover.Done()
		event, recordErr := eventStore.RecordStatus(ctx, status("sync-c", 9, ""))
		laggingResult <- event
		errs <- recordErr
	}()
	takeover.Wait()
	close(errs)
	for recordErr := range errs {
		if recordErr != nil {
			t.Fatalf("concurrent writer takeover: %v", recordErr)
		}
	}
	if higher := <-higherResult; higher.ID == 0 {
		t.Fatalf("higher writer takeover event = %+v", higher)
	}
	if lagging := <-laggingResult; lagging.ID != 0 {
		t.Fatalf("concurrent lagging writer event = %+v", lagging)
	}
	oldWriterFailure, err := eventStore.RecordStatus(ctx, status("sync-a", 10, "sync_cycle_failed"))
	if err != nil || oldWriterFailure.ID != 0 {
		t.Fatalf("old writer failure result: event=%+v err=%v", oldWriterFailure, err)
	}
	currentFailure, err := eventStore.RecordStatus(ctx, status("sync-b", 11, "sync_cycle_failed"))
	if err != nil || currentFailure.ID == 0 {
		t.Fatalf("current writer failure: event=%+v err=%v", currentFailure, err)
	}
	recovered, err := eventStore.RecordStatus(ctx, status("sync-a", 10, ""))
	if err != nil || recovered.ID == 0 {
		t.Fatalf("healthy writer recovery: event=%+v err=%v", recovered, err)
	}
	stored, exists, err = eventStore.Status(ctx)
	if err != nil || !exists || stored.Latest != 10 || stored.ErrorCode != "" || !stored.Ready {
		t.Fatalf("recovered status: status=%+v exists=%t err=%v", stored, exists, err)
	}

	// A lower contender that started from the same observed status must not
	// overwrite the higher contender even when its write completes afterward.
	twelve, err := eventStore.RecordStatus(ctx, status("sync-d", 12, ""))
	if err != nil || twelve.ID == 0 {
		t.Fatalf("record higher reversed-order writer: event=%+v err=%v", twelve, err)
	}
	eleven, err := eventStore.RecordStatus(ctx, status("sync-e", 11, ""))
	if err != nil || eleven.ID != 0 {
		t.Fatalf("lower reversed-order writer: event=%+v err=%v", eleven, err)
	}
	stored, exists, err = eventStore.Status(ctx)
	if err != nil || !exists || stored.Latest != 12 || stored.ErrorCode != "" || !stored.Ready {
		t.Fatalf("status after reversed writer completion: status=%+v exists=%t err=%v", stored, exists, err)
	}

	halted := status("sync-f", 12, "finalized_reorg")
	halted.SafetyHalt = true
	haltEvent, err := eventStore.RecordStatus(ctx, halted)
	if err != nil || haltEvent.ID == 0 {
		t.Fatalf("record safety halt: event=%+v err=%v", haltEvent, err)
	}
	activeLeaseTakeover, err := eventStore.RecordStatus(ctx, status("sync-e", 13, ""))
	if err != nil || activeLeaseTakeover.ID != 0 {
		t.Fatalf("active safety lease takeover result: event=%+v err=%v", activeLeaseTakeover, err)
	}
	stored, exists, err = eventStore.Status(ctx)
	if err != nil || !exists || stored.Latest != 12 ||
		stored.ErrorCode != "finalized_reorg" || !stored.SafetyHalt || stored.Ready {
		t.Fatalf("active safety lease status: status=%+v exists=%t err=%v", stored, exists, err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE sync_runtime_status_writer_leases
		SET expires_at = clock_timestamp() - interval '1 second'
		WHERE chain_id = 1`); err != nil {
		t.Fatalf("force safety reporter lease expiry: %v", err)
	}
	afterExpiry, err := eventStore.RecordStatus(ctx, status("sync-e", 13, ""))
	if err != nil || afterExpiry.ID == 0 {
		t.Fatalf("healthy takeover after safety lease expiry: event=%+v err=%v", afterExpiry, err)
	}
	stored, exists, err = eventStore.Status(ctx)
	if err != nil || !exists || stored.Latest != 13 || stored.ErrorCode != "" ||
		stored.SafetyHalt || !stored.Ready {
		t.Fatalf("status after safety lease expiry: status=%+v exists=%t err=%v", stored, exists, err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM runtime_events WHERE chain_id = 1`, 7)
}

func assertRuntimeEvent(t *testing.T, stream <-chan events.Event, wantID uint64) {
	t.Helper()
	select {
	case event := <-stream:
		if event.ID != wantID || event.Type != "status" {
			t.Fatalf("runtime event = %+v, want status %d", event, wantID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for runtime event %d", wantID)
	}
}

func uintString(value uint64) string {
	return strconv.FormatUint(value, 10)
}
