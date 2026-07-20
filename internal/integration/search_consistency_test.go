//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/adapters"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/islishude/etherview/internal/maintenance"
	"github.com/islishude/etherview/internal/metadata"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
)

func TestSearchCatalogFunctionsRemainBoundToTheirMigrationSchema(t *testing.T) {
	first, second := newMigratedPostgres(t), newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	firstSchema, secondSchema := currentTestSchema(t, ctx, first), currentTestSchema(t, ctx, second)
	for _, db := range []*sql.DB{first, second} {
		execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)
	}
	execFixture(t, ctx, second, `INSERT INTO operator_labels
		(chain_id, object_kind, object_key, label)
		VALUES (1, 'address', $1, 'second schema')`, testAddress(940).String())
	if got := catalogGeneration(t, ctx, second, secondSchema); got != 1 {
		t.Fatalf("second generation before cross-schema write=%d", got)
	}

	// The connection's search_path is the second schema. Both operations target
	// the first schema explicitly and therefore must execute only first-schema
	// function/table references.
	if _, err := second.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s.operator_labels
		(chain_id, object_kind, object_key, label)
		VALUES (1, 'address', $1, 'first schema')`, quoteIdentifier(firstSchema)), testAddress(941).String()); err != nil {
		t.Fatal(err)
	}
	if got := catalogGeneration(t, ctx, second, firstSchema); got != 1 {
		t.Fatalf("first generation after explicit trigger=%d", got)
	}
	if got := catalogGeneration(t, ctx, second, secondSchema); got != 1 {
		t.Fatalf("second generation changed after first-schema trigger=%d", got)
	}
	var minimum int64
	if err := second.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT %s.prune_search_catalog(1, 1000)`, quoteIdentifier(firstSchema),
	)).Scan(&minimum); err != nil {
		t.Fatal(err)
	}
	if got := catalogGeneration(t, ctx, second, firstSchema); got != 2 {
		t.Fatalf("first generation after explicit prune=%d minimum=%d", got, minimum)
	}
	if got := catalogGeneration(t, ctx, second, secondSchema); got != 1 {
		t.Fatalf("second generation changed after first-schema prune=%d", got)
	}
}

func TestDottedSearchRequiresFreshNameButCursorFreezesSuccessfulGate(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	commitCanonical(t, ctx, repository, testBundle(0, testHash(950), testHash(0), testHash(9_500), "fresh-name"))
	now := time.Now().UTC().Truncate(time.Second)
	body, _ := json.Marshal(map[string]any{
		"name": "fresh.eth", "address": testAddress(950).String(), "registry": testAddress(951).String(),
		"block_number": "0", "block_hash": testHash(950).String(), "observed_at": now.Add(-time.Minute),
	})
	fetcher := &mutableIntegrationFetcher{body: body}
	nameService, err := adapters.NewPostgresNameService(db, 1, fetcher, adapters.NameOptions{
		BaseURL: "https://name.example/v1", Freshness: time.Hour,
		FailureTTL: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := uint64(0); index < 2; index++ {
		execFixture(t, ctx, db, `INSERT INTO operator_labels
			(chain_id, object_kind, object_key, label)
			VALUES (1, 'address', $1, 'fresh.eth')`, testAddress(9_510+index).String())
	}
	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1, NameResolver: nameService})
	if err != nil {
		t.Fatal(err)
	}
	first, cursor, err := reader.Search(ctx, "fresh.eth", "", 1)
	if err != nil || len(first) != 1 || cursor == "" || fetcher.calls.Load() != 1 {
		t.Fatalf("first=%+v cursor=%q fetches=%d error=%v", first, cursor, fetcher.calls.Load(), err)
	}

	now = now.Add(2 * time.Hour)
	fetcher.setError(&metadata.FetchError{Kind: metadata.FailureTemporary, Err: errors.New("upstream secret")})
	second, _, err := reader.Search(ctx, "fresh.eth", cursor, 1)
	if err != nil || len(second) != 1 || fetcher.calls.Load() != 1 {
		t.Fatalf("cursor page=%+v fetches=%d error=%v", second, fetcher.calls.Load(), err)
	}
	results, next, err := reader.Search(ctx, "fresh.eth", "", 20)
	if !errors.Is(err, httpapi.ErrUnavailable) || len(results) != 0 || next != "" || fetcher.calls.Load() != 2 {
		t.Fatalf("expired first page=%+v next=%q fetches=%d error=%v", results, next, fetcher.calls.Load(), err)
	}
}

func TestSearchRejectsNameDetachedAfterResolveBeforeSnapshot(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range []struct{ number, hash, parent uint64 }{{0, 960, 0}, {1, 961, 960}, {2, 962, 961}} {
		commitCanonical(t, ctx, repository, testBundle(block.number, testHash(block.hash), testHash(block.parent), testHash(9_600+block.number), "resolve-reorg"))
	}
	now := time.Now().UTC().Truncate(time.Second)
	body, _ := json.Marshal(map[string]any{
		"name": "race.eth", "address": testAddress(960).String(), "registry": testAddress(961).String(),
		"block_number": "1", "block_hash": testHash(961).String(), "observed_at": now.Add(-time.Minute),
	})
	service, err := adapters.NewPostgresNameService(db, 1, &integrationJSONFetcher{body: body}, adapters.NameOptions{
		BaseURL: "https://name.example/v1", Freshness: time.Hour,
		FailureTTL: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver := nameResolverFunc(func(resolveCtx context.Context, name string) (string, error) {
		address, resolveErr := service.Resolve(resolveCtx, name)
		if resolveErr != nil {
			return "", resolveErr
		}
		if _, deleteErr := db.ExecContext(resolveCtx, `DELETE FROM canonical_blocks
			WHERE chain_id = 1 AND number = 1`); deleteErr != nil {
			return "", deleteErr
		}
		return address, nil
	})
	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1, NameResolver: resolver})
	if err != nil {
		t.Fatal(err)
	}
	results, cursor, err := reader.Search(ctx, "race.eth", "", 20)
	if !errors.Is(err, httpapi.ErrUnavailable) || len(results) != 0 || cursor != "" {
		t.Fatalf("results=%+v cursor=%q error=%v", results, cursor, err)
	}
}

func TestNameSuccessSerializesWithCanonicalDetach(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	commitCanonical(t, ctx, repository, testBundle(0, testHash(970), testHash(0), testHash(9_700), "name-lock"))
	now := time.Now().UTC().Truncate(time.Second)
	body, _ := json.Marshal(map[string]any{
		"name": "locked.eth", "address": testAddress(970).String(), "registry": testAddress(971).String(),
		"block_number": "0", "block_hash": testHash(970).String(), "observed_at": now.Add(-time.Minute),
	})
	service, err := adapters.NewPostgresNameService(db, 1, &integrationJSONFetcher{body: body}, adapters.NameOptions{
		BaseURL: "https://name.example/v1", Freshness: time.Hour,
		FailureTTL: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	detach, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer detach.Rollback()
	if _, err := detach.ExecContext(ctx, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = 0`); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, resolveErr := service.Resolve(ctx, "locked.eth")
		finished <- resolveErr
	}()
	waitForNameWriteLock(t, ctx, db)
	select {
	case err := <-finished:
		t.Fatalf("name write did not wait for canonical detach: %v", err)
	default:
	}
	if err := detach.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; !errors.Is(err, adapters.ErrUnavailable) {
		t.Fatalf("resolve after detach error=%v", err)
	} else {
		var capability adapters.CapabilityError
		if !errors.As(err, &capability) || capability.Code != "stale_block" {
			t.Fatalf("resolve capability=%#v", err)
		}
	}
	var names, observations int
	if err := db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM name_records WHERE name = 'locked.eth'),
		(SELECT count(*) FROM external_adapter_observations WHERE observation_key = 'locked.eth')`).Scan(&names, &observations); err != nil {
		t.Fatal(err)
	}
	if names != 0 || observations != 0 {
		t.Fatalf("names=%d observations=%d", names, observations)
	}
}

func TestCanonicalDetachWaitsForNameSuccessAndThenOrphansIt(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(972), testHash(0), testHash(9_720), "name-first-genesis")
	oldOne := testBundle(1, testHash(973), testHash(972), testHash(9_721), "name-first-old")
	newOne := testBundle(1, testHash(974), testHash(972), testHash(9_722), "name-first-new")
	for _, bundle := range []ethrpc.Bundle{genesis, oldOne} {
		commitCanonical(t, ctx, repository, bundle)
	}
	now := time.Now().UTC().Truncate(time.Second)
	body, _ := json.Marshal(map[string]any{
		"name": "name-first.eth", "address": testAddress(972).String(), "registry": testAddress(973).String(),
		"block_number": "1", "block_hash": testHash(973).String(), "observed_at": now.Add(-time.Minute),
	})
	service := newIntegrationNameService(t, db, now, &integrationJSONFetcher{body: body})

	const advisoryKey = 20_097_200
	execFixture(t, ctx, db, `CREATE FUNCTION pause_name_first_insert()
		RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.name = 'name-first.eth' THEN
				PERFORM pg_advisory_xact_lock(20, 20097200);
			END IF;
			RETURN NEW;
		END
		$$`)
	execFixture(t, ctx, db, `CREATE TRIGGER pause_name_first_insert_trigger
		BEFORE INSERT ON name_records
		FOR EACH ROW EXECUTE FUNCTION pause_name_first_insert()`)
	pauseConnection, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pauseConnection.Close()
	if _, err := pauseConnection.ExecContext(ctx, `SELECT pg_advisory_lock(20, $1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	paused := true
	defer func() {
		if paused {
			_, _ = pauseConnection.ExecContext(context.Background(), `SELECT pg_advisory_unlock(20, $1)`, advisoryKey)
		}
	}()

	nameDone := make(chan error, 1)
	go func() {
		_, resolveErr := service.Resolve(ctx, "name-first.eth")
		nameDone <- resolveErr
	}()
	waitForNameWriteLock(t, ctx, db)

	ancestor := mustBlockRef(t, genesis)
	detached := mustBlockRef(t, oldOne)
	newTip := mustBlockRef(t, newOne)
	reorgDone := make(chan error, 1)
	go func() {
		reorgDone <- repository.ApplyReorg(ctx, "1", store.Reorg{
			Ancestor: ancestor, Detached: []store.BlockRef{detached},
			Attached: []ethrpc.Bundle{newOne}, Checkpoint: store.NewCoreCheckpoint(newTip),
			Reason: "name consistency lock test",
		})
	}()
	waitForCanonicalDetachLock(t, ctx, db)
	select {
	case err := <-nameDone:
		t.Fatalf("paused name write completed early: %v", err)
	default:
	}
	select {
	case err := <-reorgDone:
		t.Fatalf("canonical detach did not wait for name key-share lock: %v", err)
	default:
	}

	if _, err := pauseConnection.ExecContext(ctx, `SELECT pg_advisory_unlock(20, $1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	paused = false
	if err := <-nameDone; err != nil {
		t.Fatalf("complete name write: %v", err)
	}
	if err := <-reorgDone; err != nil {
		t.Fatalf("complete canonical detach: %v", err)
	}

	var canonical bool
	if err := db.QueryRowContext(ctx, `SELECT canonical FROM name_records
		WHERE chain_id = 1 AND name = 'name-first.eth' AND block_hash = $1`, mustBytes(t, testHash(973))).Scan(&canonical); err != nil {
		t.Fatal(err)
	}
	if canonical {
		t.Fatal("detached name record remained canonical")
	}
	reader, err := query.NewPostgresReader(db, query.Options{
		ChainID: 1,
		NameResolver: nameResolverFunc(func(context.Context, string) (string, error) {
			return testAddress(972).String(), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	results, cursor, err := reader.Search(ctx, "name-first.eth", "", 20)
	if !errors.Is(err, httpapi.ErrUnavailable) || len(results) != 0 || cursor != "" {
		t.Fatalf("orphan name search results=%+v cursor=%q error=%v", results, cursor, err)
	}
}

func TestSparseCanonicalReplacementWaitsForNameSuccessAndThenOrphansIt(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(975), testHash(0), testHash(9_750), "sparse-name-genesis")
	oldTwo := testBundle(2, testHash(976), testHash(9_751), testHash(9_752), "sparse-name-old")
	newTwo := testBundle(2, testHash(977), testHash(9_751), testHash(9_753), "sparse-name-new")
	for _, segment := range [][]ethrpc.Bundle{{genesis}, {oldTwo}} {
		if _, err := repository.CommitCanonicalSegment(ctx, "1", segment); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	body, _ := json.Marshal(map[string]any{
		"name": "sparse-name.eth", "address": testAddress(975).String(), "registry": testAddress(976).String(),
		"block_number": "2", "block_hash": testHash(976).String(), "observed_at": now.Add(-time.Minute),
	})
	service := newIntegrationNameService(t, db, now, &integrationJSONFetcher{body: body})

	const advisoryKey = 20_097_500
	execFixture(t, ctx, db, `CREATE FUNCTION pause_sparse_name_insert()
		RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.name = 'sparse-name.eth' THEN
				PERFORM pg_advisory_xact_lock(21, 20097500);
			END IF;
			RETURN NEW;
		END
		$$`)
	execFixture(t, ctx, db, `CREATE TRIGGER pause_sparse_name_insert_trigger
		BEFORE INSERT ON name_records
		FOR EACH ROW EXECUTE FUNCTION pause_sparse_name_insert()`)
	pauseConnection, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pauseConnection.Close()
	if _, err := pauseConnection.ExecContext(ctx, `SELECT pg_advisory_lock(21, $1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	paused := true
	defer func() {
		if paused {
			_, _ = pauseConnection.ExecContext(context.Background(), `SELECT pg_advisory_unlock(21, $1)`, advisoryKey)
		}
	}()

	nameDone := make(chan error, 1)
	go func() {
		_, resolveErr := service.Resolve(ctx, "sparse-name.eth")
		nameDone <- resolveErr
	}()
	waitForNameWriteLock(t, ctx, db)

	detached := mustBlockRef(t, oldTwo)
	replacementDone := make(chan error, 1)
	go func() {
		_, replaceErr := repository.ReplaceHighestCanonicalSegment(ctx, "1", store.SparseCanonicalReplacement{
			Range: store.BlockRange{Start: 2, End: 2}, Detached: []store.BlockRef{detached},
			Attached: []ethrpc.Bundle{newTwo}, Reason: "sparse name consistency lock test",
		})
		replacementDone <- replaceErr
	}()
	waitForCanonicalDetachLock(t, ctx, db)
	select {
	case err := <-nameDone:
		t.Fatalf("paused sparse name write completed early: %v", err)
	default:
	}
	select {
	case err := <-replacementDone:
		t.Fatalf("sparse canonical replacement did not wait for name key-share lock: %v", err)
	default:
	}

	if _, err := pauseConnection.ExecContext(ctx, `SELECT pg_advisory_unlock(21, $1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	paused = false
	if err := <-nameDone; err != nil {
		t.Fatalf("complete sparse name write: %v", err)
	}
	if err := <-replacementDone; err != nil {
		t.Fatalf("complete sparse canonical replacement: %v", err)
	}

	var canonical bool
	if err := db.QueryRowContext(ctx, `SELECT canonical FROM name_records
		WHERE chain_id = 1 AND name = 'sparse-name.eth' AND block_hash = $1`, mustBytes(t, testHash(976))).Scan(&canonical); err != nil {
		t.Fatal(err)
	}
	if canonical {
		t.Fatal("sparse-detached name record remained canonical")
	}
	reader, err := query.NewPostgresReader(db, query.Options{
		ChainID: 1,
		NameResolver: nameResolverFunc(func(context.Context, string) (string, error) {
			return testAddress(975).String(), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	results, cursor, err := reader.Search(ctx, "sparse-name.eth", "", 20)
	if !errors.Is(err, httpapi.ErrUnavailable) || len(results) != 0 || cursor != "" {
		t.Fatalf("sparse orphan name search results=%+v cursor=%q error=%v", results, cursor, err)
	}
}

func TestConcurrentNameObservationsAreIdempotentOrConflictWithoutCatalogChurn(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	commitCanonical(t, ctx, repository, testBundle(0, testHash(980), testHash(0), testHash(9_800), "name-race"))
	now := time.Now().UTC().Truncate(time.Second)

	identicalBefore := currentCatalogGeneration(t, ctx, db)
	identicalTimes := []time.Time{now.Add(-2 * time.Minute), now.Add(-time.Minute)}
	identicalBodies := make([][]byte, 2)
	for index := range identicalBodies {
		identicalBodies[index], _ = json.Marshal(map[string]any{
			"name": "identical.eth", "address": testAddress(980).String(), "registry": testAddress(981).String(),
			"block_number": "0", "block_hash": testHash(980).String(), "observed_at": identicalTimes[index],
		})
	}
	identicalResults := resolveConcurrently(t, ctx, db, now, "identical.eth", identicalBodies)
	for index, result := range identicalResults {
		if result.err != nil || !strings.EqualFold(result.address, testAddress(980).String()) {
			t.Fatalf("identical result %d=%+v", index, result)
		}
	}
	if delta := currentCatalogGeneration(t, ctx, db) - identicalBefore; delta != 1 {
		t.Fatalf("identical catalog generation delta=%d", delta)
	}
	var firstObservedAt time.Time
	if err := db.QueryRowContext(ctx, `SELECT observed_at FROM name_records
		WHERE chain_id = 1 AND name = 'identical.eth'`).Scan(&firstObservedAt); err != nil {
		t.Fatal(err)
	}
	if !firstObservedAt.Equal(identicalTimes[0]) && !firstObservedAt.Equal(identicalTimes[1]) {
		t.Fatalf("first observed_at=%s", firstObservedAt)
	}

	later := now.Add(2 * time.Hour)
	laterBody, _ := json.Marshal(map[string]any{
		"name": "identical.eth", "address": testAddress(980).String(), "registry": testAddress(981).String(),
		"block_number": "0", "block_hash": testHash(980).String(), "observed_at": later.Add(-time.Minute),
	})
	laterService := newIntegrationNameService(t, db, later, &integrationJSONFetcher{body: laterBody})
	if _, err := laterService.Resolve(ctx, "identical.eth"); err != nil {
		t.Fatal(err)
	}
	if generation := currentCatalogGeneration(t, ctx, db); generation != identicalBefore+1 {
		t.Fatalf("idempotent refresh changed generation=%d", generation)
	}
	var preserved time.Time
	if err := db.QueryRowContext(ctx, `SELECT observed_at FROM name_records
		WHERE chain_id = 1 AND name = 'identical.eth'`).Scan(&preserved); err != nil || !preserved.Equal(firstObservedAt) {
		t.Fatalf("preserved observed_at=%s want=%s error=%v", preserved, firstObservedAt, err)
	}

	conflictBefore := currentCatalogGeneration(t, ctx, db)
	conflictBodies := make([][]byte, 2)
	for index := range conflictBodies {
		conflictBodies[index], _ = json.Marshal(map[string]any{
			"name": "conflict.eth", "address": testAddress(982 + uint64(index)).String(), "registry": testAddress(984).String(),
			"block_number": "0", "block_hash": testHash(980).String(), "observed_at": now.Add(-time.Minute),
		})
	}
	conflictResults := resolveConcurrently(t, ctx, db, now, "conflict.eth", conflictBodies)
	var successes, conflicts int
	for _, result := range conflictResults {
		switch {
		case result.err == nil:
			successes++
		case errors.Is(result.err, adapters.ErrUnavailable):
			var capability adapters.CapabilityError
			if errors.As(result.err, &capability) && capability.Code == "identity_conflict" {
				conflicts++
			}
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("conflict results=%+v successes=%d conflicts=%d", conflictResults, successes, conflicts)
	}
	if delta := currentCatalogGeneration(t, ctx, db) - conflictBefore; delta != 1 {
		t.Fatalf("conflict catalog generation delta=%d", delta)
	}
}

func TestNameAdapterRejectsNonCanonicalBlockNumbersAsStableFailure(t *testing.T) {
	for _, test := range []struct {
		name        string
		blockNumber string
	}{
		{name: "leading-zero", blockNumber: "01"},
		{name: "numeric-overflow", blockNumber: strings.Repeat("9", 79)},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)
			now := time.Now().UTC().Truncate(time.Second)
			name := test.name + ".eth"
			body, _ := json.Marshal(map[string]any{
				"name": name, "address": testAddress(990).String(), "registry": testAddress(991).String(),
				"block_number": test.blockNumber, "block_hash": testHash(990).String(),
				"observed_at": now.Add(-time.Minute),
			})
			service := newIntegrationNameService(t, db, now, &integrationJSONFetcher{body: body})
			if _, err := service.Resolve(ctx, name); !errors.Is(err, adapters.ErrUnavailable) {
				t.Fatalf("resolve error=%v", err)
			} else {
				var capability adapters.CapabilityError
				if !errors.As(err, &capability) || capability.State != "failed" || capability.Code != "invalid_response" {
					t.Fatalf("resolve capability=%#v", err)
				}
			}
			var state, code string
			if err := db.QueryRowContext(ctx, `SELECT state, code
				FROM external_adapter_observations
				WHERE chain_id = 1 AND capability = 'name' AND observation_key = $1`, name).Scan(&state, &code); err != nil {
				t.Fatal(err)
			}
			if state != "failed" || code != "invalid_response" {
				t.Fatalf("persisted state=%q code=%q", state, code)
			}
		})
	}
}

func TestPostgresCatalogMaintenanceUsesTryLockAndBoundedAdapterBatch(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1), (2)`)
	now := time.Now().UTC().Truncate(time.Second)
	execFixture(t, ctx, db, `INSERT INTO external_adapter_observations (
		chain_id, capability, observation_key, state, code, observed_at, expires_at
	)
	SELECT 1, 'price', 'expired-' || value::text, 'unavailable', 'temporary',
		$1::timestamptz - interval '2 hours', $1::timestamptz - interval '1 hour'
	FROM generate_series(1, 4) AS value`, now)
	execFixture(t, ctx, db, `INSERT INTO external_adapter_observations (
		chain_id, capability, observation_key, state, code, observed_at, expires_at
	) VALUES (1, 'price', 'fresh', 'unavailable', 'temporary',
		$1::timestamptz - interval '1 minute', $1::timestamptz + interval '1 hour')`, now)
	execFixture(t, ctx, db, `INSERT INTO external_adapter_observations (
		chain_id, capability, observation_key, state, code, observed_at, expires_at
	) VALUES (2, 'price', 'other-chain-expired', 'unavailable', 'temporary',
		$1::timestamptz - interval '4 hours', $1::timestamptz - interval '3 hours')`, now)
	cleaner, err := maintenance.NewPostgresCatalogCleaner(db)
	if err != nil {
		t.Fatal(err)
	}

	lock, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err := lock.ExecContext(ctx, `SELECT pg_advisory_xact_lock(
		hashtext('etherview:search-catalog-maintenance'), hashtext('1'))`); err != nil {
		t.Fatal(err)
	}
	result, err := cleaner.Sweep(ctx, 1, 1000, 2, now)
	if err != nil || result.Ran || result.Deleted != 0 {
		t.Fatalf("locked sweep result=%+v error=%v", result, err)
	}
	assertAdapterObservationCounts(t, ctx, db, 1, now, 4, 1)
	if err := lock.Commit(); err != nil {
		t.Fatal(err)
	}

	result, err = cleaner.Sweep(ctx, 1, 1000, 2, now)
	if err != nil || !result.Ran || result.MinGeneration != 0 || result.Deleted != 2 {
		t.Fatalf("first sweep result=%+v error=%v", result, err)
	}
	assertAdapterObservationCounts(t, ctx, db, 1, now, 2, 1)
	result, err = cleaner.Sweep(ctx, 1, 1000, 2, now)
	if err != nil || !result.Ran || result.Deleted != 2 {
		t.Fatalf("second sweep result=%+v error=%v", result, err)
	}
	assertAdapterObservationCounts(t, ctx, db, 1, now, 0, 1)
	var retention, generation int64
	if err := db.QueryRowContext(ctx, `SELECT retention_generations, generation
		FROM search_catalog_generations WHERE chain_id = 1`).Scan(&retention, &generation); err != nil {
		t.Fatal(err)
	}
	if retention != 1000 || generation != 2 {
		t.Fatalf("catalog retention=%d generation=%d", retention, generation)
	}
	assertAdapterObservationCounts(t, ctx, db, 2, now, 1, 0)
	result, err = cleaner.Sweep(ctx, 2, 1000, 2, now)
	if err != nil || !result.Ran || result.Deleted != 1 {
		t.Fatalf("other-chain sweep result=%+v error=%v", result, err)
	}
	assertAdapterObservationCounts(t, ctx, db, 2, now, 0, 0)
}

type nameResolverFunc func(context.Context, string) (string, error)

func (resolve nameResolverFunc) Resolve(ctx context.Context, name string) (string, error) {
	return resolve(ctx, name)
}

type mutableIntegrationFetcher struct {
	mu    sync.Mutex
	body  []byte
	err   error
	calls atomic.Int64
}

func (fetcher *mutableIntegrationFetcher) Fetch(context.Context, string, metadata.Kind) (metadata.Result, error) {
	fetcher.calls.Add(1)
	fetcher.mu.Lock()
	defer fetcher.mu.Unlock()
	if fetcher.err != nil {
		return metadata.Result{}, fetcher.err
	}
	return metadata.Result{Body: append([]byte(nil), fetcher.body...), FetchedAt: time.Now().UTC()}, nil
}

func (fetcher *mutableIntegrationFetcher) setError(err error) {
	fetcher.mu.Lock()
	defer fetcher.mu.Unlock()
	fetcher.err = err
}

type fetchBarrier struct {
	want    int64
	arrived atomic.Int64
	ready   chan struct{}
	once    sync.Once
}

func newFetchBarrier(want int64) *fetchBarrier {
	return &fetchBarrier{want: want, ready: make(chan struct{})}
}

func (barrier *fetchBarrier) wait(ctx context.Context) error {
	if barrier.arrived.Add(1) == barrier.want {
		barrier.once.Do(func() { close(barrier.ready) })
	}
	select {
	case <-barrier.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type barrierIntegrationFetcher struct {
	body    []byte
	barrier *fetchBarrier
}

func (fetcher *barrierIntegrationFetcher) Fetch(ctx context.Context, _ string, _ metadata.Kind) (metadata.Result, error) {
	if err := fetcher.barrier.wait(ctx); err != nil {
		return metadata.Result{}, err
	}
	return metadata.Result{Body: append([]byte(nil), fetcher.body...), FetchedAt: time.Now().UTC()}, nil
}

type concurrentResolveResult struct {
	address string
	err     error
}

func resolveConcurrently(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	now time.Time,
	name string,
	bodies [][]byte,
) []concurrentResolveResult {
	t.Helper()
	barrier := newFetchBarrier(int64(len(bodies)))
	services := make([]*adapters.NameService, len(bodies))
	for index, body := range bodies {
		services[index] = newIntegrationNameService(t, db, now, &barrierIntegrationFetcher{body: body, barrier: barrier})
	}
	results := make([]concurrentResolveResult, len(services))
	var group sync.WaitGroup
	for index, service := range services {
		index, service := index, service
		group.Add(1)
		go func() {
			defer group.Done()
			results[index].address, results[index].err = service.Resolve(ctx, name)
		}()
	}
	group.Wait()
	return results
}

func newIntegrationNameService(t *testing.T, db *sql.DB, now time.Time, fetcher adapters.JSONFetcher) *adapters.NameService {
	t.Helper()
	service, err := adapters.NewPostgresNameService(db, 1, fetcher, adapters.NameOptions{
		BaseURL: "https://name.example/v1", Freshness: time.Hour,
		FailureTTL: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func currentTestSchema(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

func catalogGeneration(t *testing.T, ctx context.Context, db *sql.DB, schema string) int64 {
	t.Helper()
	var generation int64
	if err := db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT COALESCE((SELECT generation FROM %s.search_catalog_generations WHERE chain_id = 1), 0)`,
		quoteIdentifier(schema),
	)).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	return generation
}

func currentCatalogGeneration(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	var generation int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT generation
		FROM search_catalog_generations WHERE chain_id = 1), 0)`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	return generation
}

func waitForNameWriteLock(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("name write did not wait on the canonical row lock")
		case <-ticker.C:
			var waiting bool
			if err := db.QueryRowContext(ctx, `SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE datname = current_database()
				  AND wait_event_type = 'Lock'
				  AND query LIKE '%INSERT INTO name_records AS stored_name%'
			)`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				return
			}
		}
	}
}

func waitForCanonicalDetachLock(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("canonical detach did not wait on the name key-share lock")
		case <-ticker.C:
			var waiting bool
			if err := db.QueryRowContext(ctx, `SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE datname = current_database()
				  AND wait_event_type = 'Lock'
				  AND query LIKE '%FROM canonical_blocks cb%'
				  AND query LIKE '%FOR UPDATE%'
			)`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				return
			}
		}
	}
}

func assertAdapterObservationCounts(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	chainID uint64,
	now time.Time,
	expired, fresh int,
) {
	t.Helper()
	var gotExpired, gotFresh int
	if err := db.QueryRowContext(ctx, `SELECT
		count(*) FILTER (WHERE expires_at <= $1),
		count(*) FILTER (WHERE expires_at > $1)
		FROM external_adapter_observations
		WHERE chain_id = $2`, now, chainID).Scan(&gotExpired, &gotFresh); err != nil {
		t.Fatal(err)
	}
	if gotExpired != expired || gotFresh != fresh {
		t.Fatalf("adapter observations expired=%d fresh=%d want=%d/%d", gotExpired, gotFresh, expired, fresh)
	}
}
