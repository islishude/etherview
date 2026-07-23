package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/ethrpc"
)

func TestMemoryRepositoryCommitsCanonicalChainAndCheckpoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	genesis := storeTestBundle(0, storeTestHash(1), storeTestHash(0))
	blockOne := storeTestBundle(1, storeTestHash(2), storeTestHash(1))
	commitTestBundle(t, repository, genesis)
	commitTestBundle(t, repository, blockOne)
	tip, exists, err := repository.CanonicalTip(ctx, "1")
	if err != nil || !exists {
		t.Fatalf("tip exists = %v, error = %v", exists, err)
	}
	if tip.Number != 1 || !tip.Hash.Equal(storeTestHash(2)) {
		t.Fatalf("tip = %+v", tip)
	}
	checkpoint, exists, err := repository.Checkpoint(ctx, "1", CoreCheckpoint)
	if err != nil || !exists || checkpoint.ContiguousThrough != 1 || !checkpoint.BlockHash.Equal(tip.Hash) {
		t.Fatalf("checkpoint = %+v, exists = %v, error = %v", checkpoint, exists, err)
	}
}

func TestMemoryRepositoryRejectsNonExtendingCommit(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	commitTestBundle(t, repository, storeTestBundle(0, storeTestHash(1), storeTestHash(0)))
	fork := storeTestBundle(1, storeTestHash(3), storeTestHash(99))
	reference, _ := RefFromBundle(fork)
	err := repository.CommitCanonical(context.Background(), "1", fork, NewCoreCheckpoint(reference))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
}

func TestMemoryRepositoryReorgRetainsOrphanAndFlipsJournals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	genesis := storeTestBundle(0, storeTestHash(1), storeTestHash(0))
	oldOne := storeTestBundle(1, storeTestHash(2), storeTestHash(1))
	oldTwo := storeTestBundle(2, storeTestHash(3), storeTestHash(2))
	for _, bundle := range []ethrpc.Bundle{genesis, oldOne, oldTwo} {
		commitTestBundle(t, repository, bundle)
	}
	if err := repository.AppendJournal(ctx, "1", JournalEntry{
		BlockHash: storeTestHash(3), Stage: "token", Sequence: 0, Payload: json.RawMessage(`{"undo":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	newOne := storeTestBundle(1, storeTestHash(12), storeTestHash(1))
	newTwo := storeTestBundle(2, storeTestHash(13), storeTestHash(12))
	newThree := storeTestBundle(3, storeTestHash(14), storeTestHash(13))
	ancestor, _, _ := repository.CanonicalBlock(ctx, "1", 0)
	detachedTwo, _, _ := repository.CanonicalBlock(ctx, "1", 2)
	detachedOne, _, _ := repository.CanonicalBlock(ctx, "1", 1)
	newTip, _ := RefFromBundle(newThree)
	err := repository.ApplyReorg(ctx, "1", Reorg{
		Ancestor:   ancestor,
		Detached:   []BlockRef{detachedTwo, detachedOne},
		Attached:   []ethrpc.Bundle{newOne, newTwo, newThree},
		Checkpoint: NewCoreCheckpoint(newTip),
		Reason:     "test fork",
	})
	if err != nil {
		t.Fatal(err)
	}
	canonicalTwo, exists, err := repository.CanonicalBlock(ctx, "1", 2)
	if err != nil || !exists || !canonicalTwo.Hash.Equal(storeTestHash(13)) {
		t.Fatalf("canonical block 2 = %+v, exists = %v, error = %v", canonicalTwo, exists, err)
	}
	if _, exists, err := repository.BundleByHash(ctx, "1", storeTestHash(3)); err != nil || !exists {
		t.Fatalf("orphan retained = %v, error = %v", exists, err)
	}
	journals, err := repository.JournalsByBlock(ctx, "1", storeTestHash(3))
	if err != nil || len(journals) != 1 || journals[0].Canonical {
		t.Fatalf("orphan journals = %+v, error = %v", journals, err)
	}
}

func TestMemoryRepositoryRejectsReorgBelowFinalized(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	bundles := []ethrpc.Bundle{
		storeTestBundle(0, storeTestHash(1), storeTestHash(0)),
		storeTestBundle(1, storeTestHash(2), storeTestHash(1)),
		storeTestBundle(2, storeTestHash(3), storeTestHash(2)),
	}
	for _, bundle := range bundles {
		commitTestBundle(t, repository, bundle)
	}
	finalized, _, _ := repository.CanonicalBlock(ctx, "1", 1)
	safe, _, _ := repository.CanonicalBlock(ctx, "1", 2)
	if err := repository.UpdateFinality(ctx, "1", Finality{Safe: &safe, Finalized: &finalized}); err != nil {
		t.Fatal(err)
	}
	ancestor, _, _ := repository.CanonicalBlock(ctx, "1", 0)
	detachedTwo, _, _ := repository.CanonicalBlock(ctx, "1", 2)
	detachedOne, _, _ := repository.CanonicalBlock(ctx, "1", 1)
	newOne := storeTestBundle(1, storeTestHash(12), storeTestHash(1))
	newTwo := storeTestBundle(2, storeTestHash(13), storeTestHash(12))
	newTip, _ := RefFromBundle(newTwo)
	err := repository.ApplyReorg(ctx, "1", Reorg{
		Ancestor: ancestor, Detached: []BlockRef{detachedTwo, detachedOne},
		Attached: []ethrpc.Bundle{newOne, newTwo}, Checkpoint: NewCoreCheckpoint(newTip),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
}

func TestMemoryRepositoryRejectsCheckpointRegression(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	genesis := storeTestBundle(0, storeTestHash(1), storeTestHash(0))
	blockOne := storeTestBundle(1, storeTestHash(2), storeTestHash(1))
	commitTestBundle(t, repository, genesis)
	commitTestBundle(t, repository, blockOne)
	reference, _ := RefFromBundle(genesis)
	err := repository.CommitCanonical(ctx, "1", genesis, NewCoreCheckpoint(reference))
	if !errors.Is(err, ErrCheckpointRegress) {
		t.Fatalf("error = %v, want ErrCheckpointRegress", err)
	}
}

func TestMemoryRepositoryRefreshCanonicalIsIdentityBoundAtomicAndIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	bundles := []ethrpc.Bundle{
		storeTestBundle(0, storeTestHash(1), storeTestHash(0)),
		storeTestBundle(1, storeTestHash(2), storeTestHash(1)),
		storeTestBundle(2, storeTestHash(3), storeTestHash(2)),
	}
	for _, bundle := range bundles {
		commitTestBundle(t, repository, bundle)
	}
	checkpointBefore, exists, err := repository.Checkpoint(ctx, "1", CoreCheckpoint)
	if err != nil || !exists {
		t.Fatalf("checkpoint exists=%v error=%v", exists, err)
	}

	refreshed := bundles[1]
	refreshed.Block.ExtraData = ethrpc.Data("0x1234")
	if err := repository.RefreshCanonical(ctx, "1", refreshed, RefreshOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := repository.RefreshCanonical(ctx, "1", refreshed, RefreshOptions{}); err != nil {
		t.Fatalf("idempotent refresh failed: %v", err)
	}
	stored := mustStoredBundle(t, repository, storeTestHash(2))
	if stored.Block.ExtraData.String() != "0x1234" {
		t.Fatalf("refreshed extra data=%s", stored.Block.ExtraData)
	}
	canonical, exists, err := repository.CanonicalBlock(ctx, "1", 1)
	if err != nil || !exists || !canonical.Hash.Equal(storeTestHash(2)) {
		t.Fatalf("canonical=%+v exists=%v error=%v", canonical, exists, err)
	}
	checkpointAfter, exists, err := repository.Checkpoint(ctx, "1", CoreCheckpoint)
	if err != nil || !exists || checkpointAfter != checkpointBefore {
		t.Fatalf("checkpoint before=%+v after=%+v exists=%v error=%v", checkpointBefore, checkpointAfter, exists, err)
	}

	wrongHash := refreshed
	wrongHash.Block.Hash = new(storeTestHash(22))
	if err := repository.RefreshCanonical(ctx, "1", wrongHash, RefreshOptions{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("identity mismatch error=%v", err)
	}
	wrongParent := refreshed
	wrongParent.Block.ParentHash = storeTestHash(99)
	if err := repository.RefreshCanonical(ctx, "1", wrongParent, RefreshOptions{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("parent mismatch error=%v", err)
	}
	invalid := refreshed
	invalid.Receipts = append(invalid.Receipts, ethrpc.Receipt{})
	if err := repository.RefreshCanonical(ctx, "1", invalid, RefreshOptions{}); err == nil {
		t.Fatal("invalid replacement bundle was accepted")
	}
	stored = mustStoredBundle(t, repository, storeTestHash(2))
	if stored.Block.ExtraData.String() != "0x1234" {
		t.Fatalf("failed refresh mutated stored bundle: %s", stored.Block.ExtraData)
	}
}

func TestMemoryRepositoryRefreshCanonicalRequiresFinalizedOverride(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	genesis := storeTestBundle(0, storeTestHash(1), storeTestHash(0))
	blockOne := storeTestBundle(1, storeTestHash(2), storeTestHash(1))
	for _, bundle := range []ethrpc.Bundle{genesis, blockOne} {
		commitTestBundle(t, repository, bundle)
	}
	finalized, _, _ := repository.CanonicalBlock(ctx, "1", 1)
	if err := repository.UpdateFinality(ctx, "1", Finality{Safe: &finalized, Finalized: &finalized}); err != nil {
		t.Fatal(err)
	}
	if err := repository.AppendJournal(ctx, "1", JournalEntry{
		BlockHash: storeTestHash(2), Stage: "token", Sequence: 0,
		Payload: json.RawMessage(`{"undo":"old-core-facts"}`),
	}); err != nil {
		t.Fatal(err)
	}
	refreshed := blockOne
	refreshed.Block.ExtraData = ethrpc.Data("0xab")
	if err := repository.RefreshCanonical(ctx, "1", refreshed, RefreshOptions{}); !errors.Is(err, ErrFinalizedRefresh) {
		t.Fatalf("finalized refresh error=%v", err)
	}
	if stored := mustStoredBundle(t, repository, storeTestHash(2)); stored.Block.ExtraData.String() != "0x" {
		t.Fatalf("rejected finalized refresh mutated bundle: %s", stored.Block.ExtraData)
	}
	if journals, err := repository.JournalsByBlock(ctx, "1", storeTestHash(2)); err != nil || len(journals) != 1 {
		t.Fatalf("rejected refresh journals=%v error=%v", journals, err)
	}
	if err := repository.RefreshCanonical(ctx, "1", refreshed, RefreshOptions{AllowFinalized: true}); err != nil {
		t.Fatal(err)
	}
	if stored := mustStoredBundle(t, repository, storeTestHash(2)); stored.Block.ExtraData.String() != "0xab" {
		t.Fatalf("authorized finalized refresh was not applied: %s", stored.Block.ExtraData)
	}
	if journals, err := repository.JournalsByBlock(ctx, "1", storeTestHash(2)); err != nil || len(journals) != 0 {
		t.Fatalf("refreshed block retained stale journals=%v error=%v", journals, err)
	}
}

func TestMigrationsContainHashKeyedCoreAndRangePartitions(t *testing.T) {
	t.Parallel()
	migrations, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 3 {
		t.Fatalf("migrations = %+v", migrations)
	}
	for index, migration := range migrations {
		if migration.Checksum == "" || migration.Version == "" || (index > 0 && migrations[index-1].Version >= migration.Version) {
			t.Fatalf("migrations are not named, checksummed, and strictly ordered: %+v", migrations)
		}
	}
	sql := migrations[0].SQL
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS blocks",
		"PRIMARY KEY (chain_id, number, hash)",
		"CREATE TABLE IF NOT EXISTS canonical_blocks",
		"PARTITION BY RANGE (block_number)",
		"FOR VALUES FROM (0) TO (1000000)",
		"CREATE TABLE IF NOT EXISTS index_checkpoints",
		"CREATE TABLE IF NOT EXISTS block_journals",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("migration missing %q", fragment)
		}
	}
	controlSQL := migrations[1].SQL
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS durable_jobs",
		"CREATE TABLE IF NOT EXISTS transactional_outbox",
		"CREATE TABLE IF NOT EXISTS api_keys",
		"CREATE TABLE IF NOT EXISTS repair_requests",
		"CREATE TABLE IF NOT EXISTS verification_jobs",
	} {
		if !strings.Contains(controlSQL, fragment) {
			t.Errorf("control migration missing %q", fragment)
		}
	}
	enrichmentSQL := migrations[2].SQL
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS token_events",
		"CREATE TABLE IF NOT EXISTS token_balance_deltas",
		"CREATE TABLE IF NOT EXISTS normalized_traces",
		"CREATE TABLE IF NOT EXISTS proxy_observations",
		"CREATE TABLE IF NOT EXISTS block_statistics",
	} {
		if !strings.Contains(enrichmentSQL, fragment) {
			t.Errorf("enrichment migration missing %q", fragment)
		}
	}
	var runtimeSQL, coverageSQL, abiSQL, statusWriterSQL string
	for _, migration := range migrations {
		switch migration.Version {
		case "0006_runtime_events":
			runtimeSQL = migration.SQL
		case "0007_core_coverage":
			coverageSQL = migration.SQL
		case "0008_abi_stage":
			abiSQL = migration.SQL
		case "0021_sync_status_writer_lease":
			statusWriterSQL = migration.SQL
		}
	}
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS abi_signature_candidates",
		"CREATE TABLE IF NOT EXISTS abi_decodings",
		"source = 'verified' AND confidence = 'verified'",
		"source = 'signature_database' AND confidence = 'guess'",
		"PARTITION BY RANGE (block_number)",
	} {
		if !strings.Contains(abiSQL, fragment) {
			t.Errorf("ABI stage migration missing %q", fragment)
		}
	}
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS sync_runtime_status",
		"CREATE TABLE IF NOT EXISTS runtime_events",
		"octet_length(payload::text) <= 8192",
	} {
		if !strings.Contains(runtimeSQL, fragment) {
			t.Errorf("runtime event migration missing %q", fragment)
		}
	}
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS core_index_configuration",
		"CREATE TABLE IF NOT EXISTS core_coverage_ranges",
		"CREATE TABLE IF NOT EXISTS core_backfill_leases",
		"highest_covered_number NUMERIC(78, 0)",
		"backfill_complete BOOLEAN NOT NULL DEFAULT false",
	} {
		if !strings.Contains(coverageSQL, fragment) {
			t.Errorf("core coverage migration missing %q", fragment)
		}
	}
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS sync_runtime_status_writer_leases",
		"observed_latest_number NUMERIC(78, 0)",
		"observed_latest_known BOOLEAN NOT NULL",
		"safety_halt BOOLEAN NOT NULL DEFAULT false",
	} {
		if !strings.Contains(statusWriterSQL, fragment) {
			t.Errorf("sync status writer migration missing %q", fragment)
		}
	}
	var mempoolSQL string
	for _, migration := range migrations {
		if migration.Version == "0005_mempool" {
			mempoolSQL = migration.SQL
			break
		}
	}
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS mempool_snapshots",
		"CREATE TABLE IF NOT EXISTS mempool_transactions",
		"CREATE TABLE IF NOT EXISTS mempool_snapshot_transactions",
		"CREATE TABLE IF NOT EXISTS mempool_status",
	} {
		if !strings.Contains(mempoolSQL, fragment) {
			t.Errorf("mempool migration missing %q", fragment)
		}
	}
}

func storeTestBundle(number uint64, hash, parent ethrpc.Hash) ethrpc.Bundle {
	numberQuantity := ethrpc.QuantityFromUint64(number)
	zeroHash := storeTestHash(0)
	return ethrpc.Bundle{Block: ethrpc.Block{
		Number:           &numberQuantity,
		Hash:             new(hash),
		ParentHash:       parent,
		Sha3Uncles:       zeroHash,
		TransactionsRoot: zeroHash,
		StateRoot:        zeroHash,
		ReceiptsRoot:     zeroHash,
		ExtraData:        ethrpc.Data("0x"),
		GasLimit:         ethrpc.QuantityFromUint64(30_000_000),
		GasUsed:          ethrpc.QuantityFromUint64(0),
		Timestamp:        ethrpc.QuantityFromUint64(1_700_000_000 + number),
		Transactions:     []ethrpc.TransactionRef{},
		Uncles:           []ethrpc.Hash{},
	}, Receipts: []ethrpc.Receipt{}}
}

func storeTestHash(value byte) ethrpc.Hash {
	hash, err := ethrpc.ParseHash(fmt.Sprintf("0x%064x", value))
	if err != nil {
		panic(err)
	}
	return hash
}

func commitTestBundle(t *testing.T, repository Repository, bundle ethrpc.Bundle) {
	t.Helper()
	reference, err := RefFromBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CommitCanonical(context.Background(), "1", bundle, NewCoreCheckpoint(reference)); err != nil {
		t.Fatal(err)
	}
}

func mustStoredBundle(t *testing.T, repository Repository, hash ethrpc.Hash) ethrpc.Bundle {
	t.Helper()
	bundle, exists, err := repository.BundleByHash(context.Background(), "1", hash)
	if err != nil || !exists {
		t.Fatalf("bundle %s exists=%v error=%v", hash, exists, err)
	}
	return bundle
}
