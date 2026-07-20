//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

func TestPostgresRepositoryCanonicalReorgRetainsOrphansAndRejectsFinalizedCrossing(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatalf("create PostgreSQL repository: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	genesis := testBundle(0, testHash(100), testHash(0), testHash(1_000), "genesis")
	oldOne := testBundle(1, testHash(101), testHash(100), testHash(1_001), "old-one")
	oldTwo := testBundle(2, testHash(102), testHash(101), testHash(1_002), "old-two")
	for _, bundle := range []ethrpc.Bundle{genesis, oldOne, oldTwo} {
		commitCanonical(t, ctx, repository, bundle)
	}

	tip, exists, err := repository.CanonicalTip(ctx, "1")
	if err != nil || !exists || !tip.Hash.Equal(testHash(102)) || tip.Number != 2 {
		t.Fatalf("canonical tip = %+v, exists=%t, err=%v", tip, exists, err)
	}
	checkpoint, exists, err := repository.Checkpoint(ctx, "1", store.CoreCheckpoint)
	if err != nil || !exists || checkpoint.ContiguousThrough != 2 || !checkpoint.BlockHash.Equal(testHash(102)) {
		t.Fatalf("core checkpoint = %+v, exists=%t, err=%v", checkpoint, exists, err)
	}
	if err := repository.AppendJournal(ctx, "1", store.JournalEntry{
		BlockHash: testHash(101), Stage: "token", Sequence: 1,
		Payload: json.RawMessage(`{"kind":"old-branch"}`),
	}); err != nil {
		t.Fatalf("append old-branch journal: %v", err)
	}

	newOne := testBundle(1, testHash(201), testHash(100), testHash(2_001), "new-one")
	newTwo := testBundle(2, testHash(202), testHash(201), testHash(2_002), "new-two")
	reorg := store.Reorg{
		Ancestor:   mustBlockRef(t, genesis),
		Detached:   []store.BlockRef{mustBlockRef(t, oldTwo), mustBlockRef(t, oldOne)},
		Attached:   []ethrpc.Bundle{newOne, newTwo},
		Checkpoint: store.NewCoreCheckpoint(mustBlockRef(t, newTwo)),
		Reason:     "integration fork",
	}
	if err := repository.ApplyReorg(ctx, "1", reorg); err != nil {
		t.Fatalf("apply reorg: %v", err)
	}
	for number, wantHash := range map[uint64]ethrpc.Hash{0: testHash(100), 1: testHash(201), 2: testHash(202)} {
		canonical, found, err := repository.CanonicalBlock(ctx, "1", number)
		if err != nil || !found || !canonical.Hash.Equal(wantHash) {
			t.Fatalf("canonical block %d = %+v, found=%t, err=%v, want %s", number, canonical, found, err, wantHash)
		}
	}
	for _, orphan := range []ethrpc.Bundle{oldOne, oldTwo} {
		hash, _ := orphan.BlockHash()
		stored, found, err := repository.BundleByHash(ctx, "1", hash)
		if err != nil || !found {
			t.Fatalf("orphan bundle %s found=%t err=%v", hash, found, err)
		}
		storedHash, _ := stored.BlockHash()
		if !storedHash.Equal(hash) || len(stored.Receipts) != 1 {
			t.Fatalf("orphan bundle %s was not retained: %+v", hash, stored)
		}
	}
	journals, err := repository.JournalsByBlock(ctx, "1", testHash(101))
	if err != nil || len(journals) != 1 || journals[0].Canonical {
		t.Fatalf("detached journals = %+v, err=%v", journals, err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM transaction_inclusions WHERE chain_id = 1 AND block_hash = $1`, 1, mustBytes(t, testHash(101)))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM reorg_events WHERE chain_id = 1`, 1)

	finalized := mustBlockRef(t, newOne)
	if err := repository.UpdateFinality(ctx, "1", store.Finality{Safe: &finalized, Finalized: &finalized}); err != nil {
		t.Fatalf("update finality: %v", err)
	}
	thirdOne := testBundle(1, testHash(301), testHash(100), testHash(3_001), "third-one")
	thirdTwo := testBundle(2, testHash(302), testHash(301), testHash(3_002), "third-two")
	finalizedCrossing := store.Reorg{
		Ancestor:   mustBlockRef(t, genesis),
		Detached:   []store.BlockRef{mustBlockRef(t, newTwo), mustBlockRef(t, newOne)},
		Attached:   []ethrpc.Bundle{thirdOne, thirdTwo},
		Checkpoint: store.NewCoreCheckpoint(mustBlockRef(t, thirdTwo)),
		Reason:     "must be rejected",
	}
	if err := repository.ApplyReorg(ctx, "1", finalizedCrossing); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("finalized-crossing reorg error = %v, want ErrConflict", err)
	}
	canonicalTwo, found, err := repository.CanonicalBlock(ctx, "1", 2)
	if err != nil || !found || !canonicalTwo.Hash.Equal(testHash(202)) {
		t.Fatalf("canonical block changed after rejected reorg: %+v, found=%t, err=%v", canonicalTwo, found, err)
	}
	checkpoint, exists, err = repository.Checkpoint(ctx, "1", store.CoreCheckpoint)
	if err != nil || !exists || !checkpoint.BlockHash.Equal(testHash(202)) {
		t.Fatalf("checkpoint changed after rejected reorg: %+v, exists=%t, err=%v", checkpoint, exists, err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM reorg_events WHERE chain_id = 1`, 1)
}

func TestPostgresCoverageLeaseRestartAndSparseForkRepair(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	oldZero := testBundle(0, testHash(600), testHash(0), testHash(6_000), "old-zero")
	oldOne := testBundle(1, testHash(601), testHash(600), testHash(6_001), "old-one")
	oldTwo := testBundle(2, testHash(602), testHash(601), testHash(6_002), "old-two")
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{oldZero, oldOne, oldTwo}); err != nil {
		t.Fatal(err)
	}
	newOne := testBundle(1, testHash(611), testHash(600), testHash(6_011), "new-one")
	newTwo := testBundle(2, testHash(612), testHash(611), testHash(6_012), "new-two")
	newThree := testBundle(3, testHash(613), testHash(612), testHash(6_013), "new-three")
	newFour := testBundle(4, testHash(614), testHash(613), testHash(6_014), "new-four")
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{newFour}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	firstLease, claimed, err := repository.ClaimBackfillRange(
		ctx, "1", store.BlockRange{Start: 3, End: 3}, "worker-a", now, time.Minute,
	)
	if err != nil || !claimed {
		t.Fatalf("first lease=%+v claimed=%v error=%v", firstLease, claimed, err)
	}
	restarted, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := restarted.ClaimBackfillRange(
		ctx, "1", firstLease.Range, "worker-b", now, time.Minute,
	); err != nil || claimed {
		t.Fatalf("overlapping restart claim=%v error=%v", claimed, err)
	}
	reclaimed, claimed, err := restarted.ClaimBackfillRange(
		ctx, "1", firstLease.Range, "worker-b", now.Add(2*time.Minute), time.Minute,
	)
	if err != nil || !claimed || reclaimed.Token == firstLease.Token {
		t.Fatalf("reclaimed=%+v claimed=%v error=%v", reclaimed, claimed, err)
	}

	ancestor := mustBlockRef(t, oldZero)
	coverage, err := restarted.ReplaceHighestCanonicalSegment(ctx, "1", store.SparseCanonicalReplacement{
		Range:    store.BlockRange{Start: 4, End: 4},
		Ancestor: &ancestor,
		Detached: []store.BlockRef{mustBlockRef(t, newFour), mustBlockRef(t, oldTwo), mustBlockRef(t, oldOne)},
		Attached: []ethrpc.Bundle{newOne, newTwo, newThree, newFour},
		Reason:   "integration sparse live fork",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage.Ranges) != 1 || coverage.Ranges[0] != (store.BlockRange{Start: 0, End: 4}) ||
		coverage.Contiguous == nil || coverage.Contiguous.Number != 4 ||
		coverage.Highest == nil || !coverage.Highest.Hash.Equal(testHash(614)) {
		t.Fatalf("coverage=%+v", coverage)
	}
	if err := restarted.CompleteBackfillRange(ctx, reclaimed); err != nil {
		t.Fatal(err)
	}
	checkpoint, exists, err := restarted.Checkpoint(ctx, "1", store.CoreCheckpoint)
	if err != nil || !exists || checkpoint.ContiguousThrough != 4 || !checkpoint.BlockHash.Equal(testHash(614)) {
		t.Fatalf("checkpoint=%+v exists=%v error=%v", checkpoint, exists, err)
	}
	if err := restarted.ConfigureIndex(ctx, "1", 1); !errors.Is(err, store.ErrIndexConfigurationMismatch) {
		t.Fatalf("configuration mismatch error=%v", err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM reorg_events WHERE chain_id = 1`, 1)
	for _, orphanHash := range []ethrpc.Hash{testHash(601), testHash(602)} {
		if _, exists, err := restarted.BundleByHash(ctx, "1", orphanHash); err != nil || !exists {
			t.Fatalf("orphan %s retained=%v error=%v", orphanHash, exists, err)
		}
	}
}

func TestPostgresRepositoryRefreshCanonicalIsIdentityBoundAndInvalidatesReplayableDerivedState(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatalf("create PostgreSQL repository: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	genesis := testBundle(0, testHash(400), testHash(0), testHash(4_000), "genesis")
	original := testBundle(1, testHash(401), testHash(400), testHash(4_001), "original")
	for _, bundle := range []ethrpc.Bundle{genesis, original} {
		commitCanonical(t, ctx, repository, bundle)
	}
	if err := repository.AppendJournal(ctx, "1", store.JournalEntry{
		BlockHash: testHash(400), Stage: "stats", Sequence: 1,
		Payload: json.RawMessage(`{"keep":true}`),
	}); err != nil {
		t.Fatalf("append genesis journal: %v", err)
	}
	if err := repository.AppendJournal(ctx, "1", store.JournalEntry{
		BlockHash: testHash(401), Stage: "token", Sequence: 1,
		Payload: json.RawMessage(`{"stale":true}`),
	}); err != nil {
		t.Fatalf("append refreshed-block journal: %v", err)
	}
	insertRefreshFixtures(t, ctx, db, original)

	wrongHash := testBundle(1, testHash(499), testHash(400), testHash(4_099), "wrong-hash")
	if err := repository.RefreshCanonical(ctx, "1", wrongHash, store.RefreshOptions{}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("wrong-hash refresh error = %v, want ErrConflict", err)
	}
	wrongParent := testBundle(1, testHash(401), testHash(499), testHash(4_001), "wrong-parent")
	if err := repository.RefreshCanonical(ctx, "1", wrongParent, store.RefreshOptions{}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("wrong-parent refresh error = %v, want ErrConflict", err)
	}
	assertBlockVariant(t, ctx, db, testHash(401), "original")
	assertRefreshFixtures(t, ctx, db, original, 1, 1)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1`, 1, mustBytes(t, testHash(401)))

	finalized := mustBlockRef(t, original)
	if err := repository.UpdateFinality(ctx, "1", store.Finality{Safe: &finalized, Finalized: &finalized}); err != nil {
		t.Fatalf("finalize original block: %v", err)
	}
	refreshed := testBundle(1, testHash(401), testHash(400), testHash(4_001), "refreshed")
	if err := repository.RefreshCanonical(ctx, "1", refreshed, store.RefreshOptions{}); !errors.Is(err, store.ErrFinalizedRefresh) {
		t.Fatalf("finalized refresh error = %v, want ErrFinalizedRefresh", err)
	}
	assertBlockVariant(t, ctx, db, testHash(401), "original")
	assertRefreshFixtures(t, ctx, db, original, 1, 1)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1`, 1, mustBytes(t, testHash(401)))

	checkpointBefore, exists, err := repository.Checkpoint(ctx, "1", store.CoreCheckpoint)
	if err != nil || !exists {
		t.Fatalf("checkpoint before refresh = %+v, exists=%t, err=%v", checkpointBefore, exists, err)
	}
	if err := repository.RefreshCanonical(ctx, "1", refreshed, store.RefreshOptions{AllowFinalized: true}); err != nil {
		t.Fatalf("authorized finalized refresh: %v", err)
	}
	assertBlockVariant(t, ctx, db, testHash(401), "refreshed")
	assertRefreshFixtures(t, ctx, db, original, 0, 1)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1`, 0, mustBytes(t, testHash(401)))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1`, 1, mustBytes(t, testHash(400)))
	checkpointAfter, exists, err := repository.Checkpoint(ctx, "1", store.CoreCheckpoint)
	if err != nil || !exists || checkpointAfter.ContiguousThrough != checkpointBefore.ContiguousThrough ||
		!checkpointAfter.BlockHash.Equal(checkpointBefore.BlockHash) || !checkpointAfter.UpdatedAt.Equal(checkpointBefore.UpdatedAt) {
		t.Fatalf("refresh moved checkpoint: before=%+v after=%+v exists=%t err=%v", checkpointBefore, checkpointAfter, exists, err)
	}
	canonical, exists, err := repository.CanonicalBlock(ctx, "1", 1)
	if err != nil || !exists || !canonical.Hash.Equal(testHash(401)) || !canonical.ParentHash.Equal(testHash(400)) {
		t.Fatalf("refresh moved canonical identity: %+v, exists=%t err=%v", canonical, exists, err)
	}
	finality, exists, err := repository.Finality(ctx, "1")
	if err != nil || !exists || finality.Finalized == nil || !finality.Finalized.Hash.Equal(testHash(401)) {
		t.Fatalf("refresh moved finality: %+v, exists=%t err=%v", finality, exists, err)
	}
	stored, found, err := repository.BundleByHash(ctx, "1", testHash(401))
	if err != nil || !found || len(stored.Receipts) != 1 || len(stored.Receipts[0].Logs) != 1 {
		t.Fatalf("refreshed bundle = %+v, found=%t err=%v", stored, found, err)
	}
	if got := stored.Block.Transactions[0].Transaction.Input; got != ethrpc.DataFromBytes([]byte("refreshed")) {
		t.Fatalf("refreshed transaction input = %s", got)
	}
	if got := stored.Receipts[0].Logs[0].Data; got != ethrpc.DataFromBytes([]byte("refreshed")) {
		t.Fatalf("refreshed log data = %s", got)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM withdrawals WHERE chain_id = 1 AND block_hash = $1`, 1, mustBytes(t, testHash(401)))
}

func commitCanonical(t *testing.T, ctx context.Context, repository *store.PostgresRepository, bundle ethrpc.Bundle) {
	t.Helper()
	reference := mustBlockRef(t, bundle)
	if err := repository.CommitCanonical(ctx, "1", bundle, store.NewCoreCheckpoint(reference)); err != nil {
		t.Fatalf("commit canonical block %d (%s): %v", reference.Number, reference.Hash, err)
	}
}

func insertRefreshFixtures(t *testing.T, ctx context.Context, db *sql.DB, bundle ethrpc.Bundle) {
	t.Helper()
	reference := mustBlockRef(t, bundle)
	transaction := bundle.Block.Transactions[0].Transaction
	blockHash := mustBytes(t, reference.Hash)
	transactionHash := mustBytes(t, transaction.Hash)
	tokenAddress := mustBytes(t, testAddress(10))
	ownerAddress := mustBytes(t, testAddress(11))
	codeHash := mustBytes(t, testHash(5_000))

	execFixture(t, ctx, db, `
		INSERT INTO block_stage_results (
			chain_id, block_number, block_hash, stage, stage_version, state, details
		) VALUES (1, $1, $2, 'token', 1, 'complete', '{"events":1}')`, reference.Number, blockHash)
	execFixture(t, ctx, db, `
		INSERT INTO token_events (
			chain_id, block_number, block_hash, log_index, sub_index,
			transaction_hash, token_address, standard, event_kind,
			from_address, to_address, amount, canonical, confidence, raw
		) VALUES (1, $1, $2, 0, 0, $3, $4, 'erc20', 'transfer',
			$5, $6, 1, TRUE, 'high', '{}')`,
		reference.Number, blockHash, transactionHash, tokenAddress, ownerAddress, tokenAddress)
	execFixture(t, ctx, db, `
		INSERT INTO token_balance_deltas (
			chain_id, block_number, block_hash, log_index, sub_index,
			token_address, owner_address, delta, canonical
		) VALUES (1, $1, $2, 0, 0, $3, $4, 1, TRUE)`,
		reference.Number, blockHash, tokenAddress, ownerAddress)
	execFixture(t, ctx, db, `
		INSERT INTO normalized_traces (
			chain_id, block_number, block_hash, transaction_hash, transaction_index,
			trace_path, depth, call_type, from_address, to_address, value,
			reverted, canonical
		) VALUES (1, $1, $2, $3, 0, '0', 0, 'CALL', $4, $5, 1, FALSE, TRUE)`,
		reference.Number, blockHash, transactionHash, ownerAddress, tokenAddress)
	execFixture(t, ctx, db, `
		INSERT INTO address_activities (
			chain_id, block_number, block_hash, transaction_hash, activity_index,
			address, activity_kind, canonical, details
		) VALUES (1, $1, $2, $3, 0, $4, 'transaction', TRUE, '{}')`,
		reference.Number, blockHash, transactionHash, ownerAddress)
	execFixture(t, ctx, db, `
		INSERT INTO block_statistics (
			chain_id, block_number, block_hash, transaction_count,
			gas_used, gas_limit, canonical
		) VALUES (1, $1, $2, 1, 21000, 30000000, TRUE)`, reference.Number, blockHash)

	// These rows are state-RPC observations, not direct derivatives of the
	// transaction/receipt/log bundle. Refresh intentionally preserves them.
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES (1, $1, $2, $3, $4, '\x6000', TRUE)`,
		tokenAddress, reference.Number, blockHash, codeHash)
	execFixture(t, ctx, db, `
		INSERT INTO proxy_observations (
			chain_id, proxy_address, block_number, block_hash, proxy_code_hash,
			proxy_kind, confidence, canonical, details
		) VALUES (1, $1, $2, $3, $4, 'unknown', 'inferred', TRUE, '{}')`,
		tokenAddress, reference.Number, blockHash, codeHash)
	execFixture(t, ctx, db, `
		INSERT INTO token_contracts (
			chain_id, address, code_hash, standard, confidence, metadata_state,
			observed_block_number, observed_block_hash
		) VALUES (1, $1, $2, 'erc20', 'high', 'complete', $3, $4)`,
		tokenAddress, codeHash, reference.Number, blockHash)
	execFixture(t, ctx, db, `
		INSERT INTO name_records (
			chain_id, registry, name, address, block_number, block_hash, canonical
		) VALUES (1, $1, 'integration.eth', $2, $3, $4, TRUE)`,
		tokenAddress, ownerAddress, reference.Number, blockHash)
}

func assertRefreshFixtures(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	bundle ethrpc.Bundle,
	replayableCount int,
	stateObservationCount int,
) {
	t.Helper()
	reference := mustBlockRef(t, bundle)
	blockHash := mustBytes(t, reference.Hash)
	for _, table := range []string{
		"block_stage_results",
		"token_balance_deltas",
		"token_events",
		"normalized_traces",
		"address_activities",
		"block_statistics",
	} {
		assertRowCount(t, ctx, db,
			fmt.Sprintf(`SELECT count(*) FROM %s WHERE chain_id = 1 AND block_hash = $1`, table),
			replayableCount, blockHash,
		)
	}
	for _, table := range []string{
		"contract_code_observations",
		"proxy_observations",
		"name_records",
	} {
		assertRowCount(t, ctx, db,
			fmt.Sprintf(`SELECT count(*) FROM %s WHERE chain_id = 1 AND block_hash = $1`, table),
			stateObservationCount, blockHash,
		)
	}
	assertRowCount(t, ctx, db,
		`SELECT count(*) FROM token_contracts WHERE chain_id = 1 AND observed_block_hash = $1`,
		stateObservationCount, blockHash,
	)
}

func assertBlockVariant(t *testing.T, ctx context.Context, db *sql.DB, hash ethrpc.Hash, want string) {
	t.Helper()
	var variant string
	if err := db.QueryRowContext(ctx, `
		SELECT raw->>'integrationVariant'
		FROM blocks
		WHERE chain_id = 1 AND hash = $1`, mustBytes(t, hash)).Scan(&variant); err != nil {
		t.Fatalf("query block variant %s: %v", hash, err)
	}
	if variant != want {
		t.Fatalf("block %s variant = %q, want %q", hash, variant, want)
	}
}

func execFixture(t *testing.T, ctx context.Context, db *sql.DB, query string, arguments ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query, arguments...); err != nil {
		t.Fatalf("insert integration fixture: %v\nquery:%s", err, query)
	}
}

func assertRowCount(t *testing.T, ctx context.Context, db *sql.DB, query string, want int, arguments ...any) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(ctx, query, arguments...).Scan(&got); err != nil {
		t.Fatalf("query row count: %v\nquery:%s", err, query)
	}
	if got != want {
		t.Fatalf("row count = %d, want %d\nquery:%s", got, want, query)
	}
}
