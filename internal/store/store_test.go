package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
)

func TestMemoryRepositoryCommitsCanonicalChainAndCheckpoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	genesis := storeTestBundle(0, common.Hash{}, 1)
	genesisRef := mustStoreTestRef(t, genesis)
	blockOne := storeTestBundle(1, genesisRef.Hash, 2)
	blockOneRef := mustStoreTestRef(t, blockOne)
	commitTestBundle(t, repository, genesis)
	commitTestBundle(t, repository, blockOne)
	tip, exists, err := repository.CanonicalTip(ctx, "1")
	if err != nil || !exists {
		t.Fatalf("tip exists = %v, error = %v", exists, err)
	}
	if tip.Number != 1 || tip.Hash != blockOneRef.Hash {
		t.Fatalf("tip = %+v", tip)
	}
	checkpoint, exists, err := repository.Checkpoint(ctx, "1", CoreCheckpoint)
	if err != nil || !exists || checkpoint.ContiguousThrough != 1 || checkpoint.BlockHash != tip.Hash {
		t.Fatalf("checkpoint = %+v, exists = %v, error = %v", checkpoint, exists, err)
	}
}

func TestMemoryRepositoryRejectsNonExtendingCommit(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	genesis := storeTestBundle(0, common.Hash{}, 1)
	commitTestBundle(t, repository, genesis)
	fork := storeTestBundle(1, storeTestHash(99), 3)
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
	genesis := storeTestBundle(0, common.Hash{}, 1)
	genesisRef := mustStoreTestRef(t, genesis)
	oldOne := storeTestBundle(1, genesisRef.Hash, 2)
	oldOneRef := mustStoreTestRef(t, oldOne)
	oldTwo := storeTestBundle(2, oldOneRef.Hash, 3)
	oldTwoRef := mustStoreTestRef(t, oldTwo)
	for _, bundle := range []chainbundle.Bundle{genesis, oldOne, oldTwo} {
		commitTestBundle(t, repository, bundle)
	}
	if err := repository.AppendJournal(ctx, "1", JournalEntry{
		BlockHash: oldTwoRef.Hash, Stage: "token", Sequence: 0, Payload: json.RawMessage(`{"undo":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	newOne := storeTestBundle(1, genesisRef.Hash, 12)
	newOneRef := mustStoreTestRef(t, newOne)
	newTwo := storeTestBundle(2, newOneRef.Hash, 13)
	newTwoRef := mustStoreTestRef(t, newTwo)
	newThree := storeTestBundle(3, newTwoRef.Hash, 14)
	ancestor, _, _ := repository.CanonicalBlock(ctx, "1", 0)
	detachedTwo, _, _ := repository.CanonicalBlock(ctx, "1", 2)
	detachedOne, _, _ := repository.CanonicalBlock(ctx, "1", 1)
	newTip, _ := RefFromBundle(newThree)
	err := repository.ApplyReorg(ctx, "1", Reorg{
		Ancestor:   ancestor,
		Detached:   []BlockRef{detachedTwo, detachedOne},
		Attached:   []chainbundle.Bundle{newOne, newTwo, newThree},
		Checkpoint: NewCoreCheckpoint(newTip),
		Reason:     "test fork",
	})
	if err != nil {
		t.Fatal(err)
	}
	canonicalTwo, exists, err := repository.CanonicalBlock(ctx, "1", 2)
	if err != nil || !exists || canonicalTwo.Hash != newTwoRef.Hash {
		t.Fatalf("canonical block 2 = %+v, exists = %v, error = %v", canonicalTwo, exists, err)
	}
	if _, exists, err := repository.BundleByHash(ctx, "1", oldTwoRef.Hash); err != nil || !exists {
		t.Fatalf("orphan retained = %v, error = %v", exists, err)
	}
	journals, err := repository.JournalsByBlock(ctx, "1", oldTwoRef.Hash)
	if err != nil || len(journals) != 1 || journals[0].Canonical {
		t.Fatalf("orphan journals = %+v, error = %v", journals, err)
	}
}

func TestMemoryRepositoryRejectsReorgBelowFinalized(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	genesis := storeTestBundle(0, common.Hash{}, 1)
	genesisRef := mustStoreTestRef(t, genesis)
	blockOne := storeTestBundle(1, genesisRef.Hash, 2)
	blockOneRef := mustStoreTestRef(t, blockOne)
	blockTwo := storeTestBundle(2, blockOneRef.Hash, 3)
	bundles := []chainbundle.Bundle{genesis, blockOne, blockTwo}
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
	newOne := storeTestBundle(1, genesisRef.Hash, 12)
	newOneRef := mustStoreTestRef(t, newOne)
	newTwo := storeTestBundle(2, newOneRef.Hash, 13)
	newTip, _ := RefFromBundle(newTwo)
	err := repository.ApplyReorg(ctx, "1", Reorg{
		Ancestor: ancestor, Detached: []BlockRef{detachedTwo, detachedOne},
		Attached: []chainbundle.Bundle{newOne, newTwo}, Checkpoint: NewCoreCheckpoint(newTip),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
}

func TestMemoryRepositoryRejectsCheckpointRegression(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	genesis := storeTestBundle(0, common.Hash{}, 1)
	genesisRef := mustStoreTestRef(t, genesis)
	blockOne := storeTestBundle(1, genesisRef.Hash, 2)
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
	genesis := storeTestBundle(0, common.Hash{}, 1)
	genesisRef := mustStoreTestRef(t, genesis)
	blockOne := storeTestBundle(1, genesisRef.Hash, 2)
	blockOneRef := mustStoreTestRef(t, blockOne)
	blockTwo := storeTestBundle(2, blockOneRef.Hash, 3)
	bundles := []chainbundle.Bundle{genesis, blockOne, blockTwo}
	for _, bundle := range bundles {
		commitTestBundle(t, repository, bundle)
	}
	checkpointBefore, exists, err := repository.Checkpoint(ctx, "1", CoreCheckpoint)
	if err != nil || !exists {
		t.Fatalf("checkpoint exists=%v error=%v", exists, err)
	}

	refreshed := storeTestBundle(1, genesisRef.Hash, 2)
	if err := repository.RefreshCanonical(ctx, "1", refreshed, RefreshOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := repository.RefreshCanonical(ctx, "1", refreshed, RefreshOptions{}); err != nil {
		t.Fatalf("idempotent refresh failed: %v", err)
	}
	stored := mustStoredBundle(t, repository, blockOneRef.Hash)
	if !bytes.Equal(stored.Block.Extra(), []byte{2}) {
		t.Fatalf("refreshed extra data=%x", stored.Block.Extra())
	}
	canonical, exists, err := repository.CanonicalBlock(ctx, "1", 1)
	if err != nil || !exists || canonical.Hash != blockOneRef.Hash {
		t.Fatalf("canonical=%+v exists=%v error=%v", canonical, exists, err)
	}
	checkpointAfter, exists, err := repository.Checkpoint(ctx, "1", CoreCheckpoint)
	if err != nil || !exists || checkpointAfter != checkpointBefore {
		t.Fatalf("checkpoint before=%+v after=%+v exists=%v error=%v", checkpointBefore, checkpointAfter, exists, err)
	}

	wrongHash := storeTestBundle(1, genesisRef.Hash, 22)
	if err := repository.RefreshCanonical(ctx, "1", wrongHash, RefreshOptions{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("identity mismatch error=%v", err)
	}
	wrongParent := storeTestBundle(1, storeTestHash(99), 2)
	if err := repository.RefreshCanonical(ctx, "1", wrongParent, RefreshOptions{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("parent mismatch error=%v", err)
	}
	invalid := refreshed
	invalid.Receipts = append(invalid.Receipts, &types.Receipt{})
	if err := repository.RefreshCanonical(ctx, "1", invalid, RefreshOptions{}); err == nil {
		t.Fatal("invalid replacement bundle was accepted")
	}
	stored = mustStoredBundle(t, repository, blockOneRef.Hash)
	if !bytes.Equal(stored.Block.Extra(), []byte{2}) {
		t.Fatalf("failed refresh mutated stored bundle: %x", stored.Block.Extra())
	}
}

func TestMemoryRepositoryRefreshCanonicalRequiresFinalizedOverride(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	genesis := storeTestBundle(0, common.Hash{}, 1)
	genesisRef := mustStoreTestRef(t, genesis)
	blockOne := storeTestBundle(1, genesisRef.Hash, 2)
	blockOneRef := mustStoreTestRef(t, blockOne)
	for _, bundle := range []chainbundle.Bundle{genesis, blockOne} {
		commitTestBundle(t, repository, bundle)
	}
	finalized, _, _ := repository.CanonicalBlock(ctx, "1", 1)
	if err := repository.UpdateFinality(ctx, "1", Finality{Safe: &finalized, Finalized: &finalized}); err != nil {
		t.Fatal(err)
	}
	if err := repository.AppendJournal(ctx, "1", JournalEntry{
		BlockHash: blockOneRef.Hash, Stage: "token", Sequence: 0,
		Payload: json.RawMessage(`{"undo":"old-core-facts"}`),
	}); err != nil {
		t.Fatal(err)
	}
	refreshed := storeTestBundle(1, genesisRef.Hash, 2)
	if err := repository.RefreshCanonical(ctx, "1", refreshed, RefreshOptions{}); !errors.Is(err, ErrFinalizedRefresh) {
		t.Fatalf("finalized refresh error=%v", err)
	}
	if stored := mustStoredBundle(t, repository, blockOneRef.Hash); !bytes.Equal(stored.Block.Extra(), []byte{2}) {
		t.Fatalf("rejected finalized refresh mutated bundle: %x", stored.Block.Extra())
	}
	if journals, err := repository.JournalsByBlock(ctx, "1", blockOneRef.Hash); err != nil || len(journals) != 1 {
		t.Fatalf("rejected refresh journals=%v error=%v", journals, err)
	}
	if err := repository.RefreshCanonical(ctx, "1", refreshed, RefreshOptions{AllowFinalized: true}); err != nil {
		t.Fatal(err)
	}
	if stored := mustStoredBundle(t, repository, blockOneRef.Hash); !bytes.Equal(stored.Block.Extra(), []byte{2}) {
		t.Fatalf("authorized finalized refresh was not applied: %x", stored.Block.Extra())
	}
	if journals, err := repository.JournalsByBlock(ctx, "1", blockOneRef.Hash); err != nil || len(journals) != 0 {
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

func storeTestBundle(
	number uint64,
	parent common.Hash,
	extraData byte,
) chainbundle.Bundle {
	bundle, err := testfixture.New(testfixture.Options{
		Number:     number,
		ParentHash: parent,
		ExtraData:  []byte{extraData},
	})
	if err != nil {
		panic(err)
	}
	return bundle
}

func storeTestHash(value byte) common.Hash {
	return common.Hash{common.HashLength - 1: value}
}

func commitTestBundle(t *testing.T, repository Repository, bundle chainbundle.Bundle) {
	t.Helper()
	reference, err := RefFromBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CommitCanonical(context.Background(), "1", bundle, NewCoreCheckpoint(reference)); err != nil {
		t.Fatal(err)
	}
}

func mustStoredBundle(t *testing.T, repository Repository, hash common.Hash) chainbundle.Bundle {
	t.Helper()
	bundle, exists, err := repository.BundleByHash(context.Background(), "1", hash)
	if err != nil || !exists {
		t.Fatalf("bundle %s exists=%v error=%v", hash, exists, err)
	}
	return bundle
}
