package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/testpgx"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
)

func TestRepositoryOwnsRootNormalizedBundleBeforePersistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	bundle, err := testfixture.New(testfixture.Options{
		Number: 0, TransactionTypes: []uint8{types.LegacyTxType}, LogsPerTransaction: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	reference, err := RefFromBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CommitCanonical(ctx, "1", bundle, NewCoreCheckpoint(reference)); err != nil {
		t.Fatal(err)
	}
	for index := range bundle.RawBlock {
		bundle.RawBlock[index] = 'x'
	}
	stored, exists, err := repository.BundleByHash(ctx, "1", reference.Hash)
	if err != nil || !exists {
		t.Fatalf("stored bundle exists=%v error=%v", exists, err)
	}
	if err := chainbundle.Validate(stored); err != nil || stored.Block.Hash() != reference.Hash {
		t.Fatalf("stored owned bundle = %#v, error=%v", stored, err)
	}
}

func TestDerivedCanonicalRelationsIncludeUUPSImplementationObservations(t *testing.T) {
	t.Parallel()
	for _, relation := range []string{
		"uups_implementation_observations",
		"diamond_loupe_snapshots",
		"diamond_cut_events",
		"transaction_effective_execution_identities",
		"erc20_holder_balances",
		"erc20_holder_snapshots",
	} {
		if strings.Contains(testpgx.Statement("StoreSetDerivedCanonicalBatch"), "UPDATE "+relation+" AS target SET canonical") {
			continue
		}
		t.Fatalf("derived canonical query lacks %s", relation)
	}
}

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

func TestEmbeddedBaselineIsNamedAndChecksummed(t *testing.T) {
	t.Parallel()
	migrations, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	wantVersions := []string{
		"0001_core", "0002_runtime", "0003_execution", "0004_auth",
		"0005_billing", "0006_verification", "0007_derived_verification",
		"0008_proxy_observations", "0009_proxy_bindings", "0010_proxy_coverage",
		"0011_tokens_metadata", "0012_ens_search", "0013_erc4337",
		"0014_transfers_statistics", "0015_mempool", "0016_watchlist",
		"0017_schema_bindings",
	}
	if len(migrations) != len(wantVersions) {
		t.Fatalf("baseline has %d migrations, want %d", len(migrations), len(wantVersions))
	}
	for index, migration := range migrations {
		if migration.Version != wantVersions[index] {
			t.Errorf("migration %d = %q, want %q", index, migration.Version, wantVersions[index])
		}
		if strings.TrimSpace(migration.SQL) == "" {
			t.Errorf("migration %s is empty", migration.Version)
		}
		sum := sha256.Sum256([]byte(migration.SQL))
		if migration.Checksum != hex.EncodeToString(sum[:]) {
			t.Errorf("migration %s checksum does not match embedded SQL", migration.Version)
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
