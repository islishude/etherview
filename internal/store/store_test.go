package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
)

func TestDerivedCanonicalRelationsIncludeUUPSImplementationObservations(t *testing.T) {
	t.Parallel()
	for _, relation := range []string{
		"uups_implementation_observations",
		"diamond_loupe_snapshots",
		"diamond_cut_events",
	} {
		if slices.Contains(derivedCanonicalRelations[:], relation) {
			continue
		}
		t.Fatalf("derived canonical relations=%v", derivedCanonicalRelations)
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
	var runtimeSQL, coverageSQL, abiSQL, statusWriterSQL, addressActivitySQL, verifierV2SQL, proxyInteractionSQL, proxyCoverageRangesSQL, uupsObservationSQL, uupsBindingSQL, proxyHistoryEpochSQL, delegatedAccountsSQL string
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
		case "0026_address_activity_indexes":
			addressActivitySQL = migration.SQL
		case "0027_verifier_v2":
			verifierV2SQL = migration.SQL
		case "0032_openzeppelin_proxy_interaction":
			proxyInteractionSQL = migration.SQL
		case "0033_proxy_interaction_coverage_ranges":
			proxyCoverageRangesSQL = migration.SQL
		case "0034_uups_implementation_observations":
			uupsObservationSQL = migration.SQL
		case "0035_uups_proxy_binding_identity":
			uupsBindingSQL = migration.SQL
		case "0036_proxy_history_epochs":
			proxyHistoryEpochSQL = migration.SQL
		case "0040_eip7702_delegated_accounts":
			delegatedAccountsSQL = migration.SQL
		}
	}
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS eip7702_authorizations",
		"CREATE TABLE IF NOT EXISTS transaction_execution_code_resolutions",
		"ADD COLUMN IF NOT EXISTS execution_address BYTEA",
		"ADD COLUMN IF NOT EXISTS decoding_kind TEXT",
		"'trace_constructor'",
		"TRUNCATE TABLE proxy_interaction_coverage_ranges",
	} {
		if !strings.Contains(delegatedAccountsSQL, fragment) {
			t.Errorf("delegated account migration missing %q", fragment)
		}
	}
	for _, fragment := range []string{
		"ADD COLUMN stage_version INTEGER NOT NULL DEFAULT 1",
		"PRIMARY KEY (chain_id, proxy_address, block_hash, stage_version)",
		"CREATE TABLE beacon_implementation_observations",
		"CREATE TABLE proxy_detection_evidence",
		"'immutable_args_creation_unverified'",
		"CREATE TABLE proxy_upgrade_events",
		"CREATE TABLE proxy_initialization_events",
		"REFERENCES logs(chain_id, block_number, block_hash, log_index)",
		"ADD COLUMN proxy_artifact_kind TEXT",
		"ADD COLUMN proxy_standard_version TEXT",
		"ADD COLUMN proxy_runtime_immutable_address BYTEA",
		"ADD COLUMN proxy_source_manifest_sha256 BYTEA",
		"CONSTRAINT verification_results_proxy_attestation_shape",
		"AND runtime_immutable_address IS NOT NULL",
		"result.proxy_artifact_kind = NEW.artifact_kind",
		"result.proxy_standard_version = NEW.standard_version",
		"result.proxy_runtime_immutable_address IS NOT DISTINCT FROM",
		"result.proxy_source_manifest_sha256 = NEW.source_manifest_sha256",
		"target_block.hash = job.block_hash",
		"target_block.number = NEW.valid_from_block",
		"NEW.runtime_immutable_address = job.address",
		"observation_stage_version",
		"CREATE TABLE verified_proxy_bindings",
		"CONSTRAINT verified_proxy_bindings_management_semantics",
		"management_address IS NOT DISTINCT FROM admin_address",
		"management_address IS NOT DISTINCT FROM beacon_address",
		"NEW.proxy_pattern <> 'clone' OR (\n                  observation.proxy_kind = NEW.proxy_kind",
		"JOIN proxy_observations AS observation",
		"observation.proxy_code_hash = NEW.proxy_code_hash",
		"observation.beacon_code_hash = NEW.beacon_code_hash",
		"observation.confidence IN ('verified', 'high')",
		"newer_observation.block_number > observation.block_number",
		"newer_generation.id > generation.id",
	} {
		if !strings.Contains(proxyInteractionSQL, fragment) {
			t.Errorf("proxy interaction migration missing %q", fragment)
		}
	}
	for _, fragment := range []string{
		"CHECK (target_kind IN ('proxy', 'beacon', 'uups'))",
		"CREATE TABLE uups_implementation_observations",
		"CREATE TABLE uups_implementation_observation_generations",
		"CREATE INDEX uups_implementation_observations_latest_idx",
		"uups_implementation_observations_exact_probe",
		"360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc",
		"upgrade_interface_version = '5.0.0'",
		"artifact.artifact_kind = 'uups_implementation'",
		"artifact.runtime_immutable_address = NEW.implementation_address",
		"code_observation.code_hash = NEW.implementation_code_hash",
		"code_observation.canonical = TRUE",
		"FOR EACH ROW EXECUTE FUNCTION enforce_proxy_observation_generation()",
		"DEFERRABLE INITIALLY DEFERRED",
		"UUPS implementation observation lacks a lease-fenced generation witness",
		"UUPS implementation observations are append-only",
	} {
		if !strings.Contains(uupsObservationSQL, fragment) {
			t.Errorf("UUPS implementation observation migration missing %q", fragment)
		}
	}
	for _, fragment := range []string{
		"ADD COLUMN uups_generation_id BIGINT",
		"verified_proxy_bindings_uups_generation_shape",
		"CREATE INDEX transaction_state_changes_code_epoch_idx",
		"WHERE canonical AND field_kind = 'code'",
		"CREATE INDEX proxy_detection_evidence_negative_shadow_idx",
		"chain_id, address, candidate_kind, code_hash",
		"WHERE canonical AND stage_version = 2",
		"(proxy_pattern = 'uups') = (uups_generation_id IS NOT NULL)",
		"WHEN 'uups' THEN 'erc1967'",
		"resolution.implementation_artifact_job_id IS NULL",
		"UUPS compatibility is shared implementation evidence, not a proxy artifact resolution",
		"result.outcome->>'uups_generation_id'",
		"generation.id = NEW.uups_generation_id",
		"published.state = 'complete'",
		"proxy binding lacks continuous observation-to-context coverage",
		"'etherview:proxy-interaction-coverage:' || NEW.chain_id::text",
		"proxy binding context is not the canonical tip",
		"proxy binding is shadowed by published negative detection evidence",
		"evidence.reason = 'immutable_args_creation_unverified'",
		"exact_clone.details->>'immutable_args_creation_authenticated' = 'true'",
		"proxy binding beacon is shadowed by published negative detection evidence",
		"evidence.candidate_kind = 'proxy'",
		"evidence.candidate_kind = 'beacon'",
		"observation.probe_state = 'compatible'",
		"artifact.valid_from_block >= code_epoch.block_number",
		"proxy_interaction_coverage_contains(",
		"conflict_generation.id > generation.id",
		"UUPS implementation generation is not the latest compatible published evidence",
	} {
		if !strings.Contains(uupsBindingSQL, fragment) {
			t.Errorf("UUPS proxy binding migration missing %q", fragment)
		}
	}
	if joins, complete := strings.Count(uupsBindingSQL, "JOIN published_block_stage_results AS"), strings.Count(uupsBindingSQL, ".state = 'complete'"); joins != complete {
		t.Errorf("UUPS binding migration complete-publication guards=%d, want one for each of %d joins", complete, joins)
	}
	lockPosition := strings.Index(uupsBindingSQL, "PERFORM pg_advisory_xact_lock")
	tipPosition := strings.Index(uupsBindingSQL, "proxy binding context is not the canonical tip")
	if lockPosition < 0 || tipPosition < 0 || lockPosition > tipPosition {
		t.Errorf("UUPS binding trigger must acquire the coverage lock before its canonical-tip recheck")
	}
	for _, fragment := range []string{
		"CREATE TABLE proxy_history_epochs",
		"phase IN ('requested', 'published')",
		"CREATE INDEX proxy_history_epochs_latest_idx",
		"INCLUDE (block_number, block_hash)",
		"CREATE TRIGGER durable_jobs_proxy_history_epoch",
		"CREATE TRIGGER block_stage_results_proxy_history_epoch",
		"proxy history epochs are append-only",
	} {
		if !strings.Contains(proxyHistoryEpochSQL, fragment) {
			t.Errorf("proxy history epoch migration missing %q", fragment)
		}
	}
	for _, fragment := range []string{
		"CREATE TABLE proxy_interaction_covered_blocks",
		"CREATE TABLE proxy_interaction_coverage_ranges",
		"CREATE OR REPLACE FUNCTION proxy_interaction_coverage_contains",
		"CREATE OR REPLACE FUNCTION refresh_proxy_interaction_coverage_block",
		"pg_advisory_xact_lock(hashtextextended(",
		"('trace'::text, 1)",
		"('state_diff'::text, 1)",
		"('proxy'::text, 2)",
		"CREATE TRIGGER proxy_interaction_coverage_canonical_trigger",
		"CREATE TRIGGER proxy_interaction_coverage_stage_result_trigger",
		"CREATE TRIGGER proxy_interaction_coverage_job_trigger",
		"CREATE TRIGGER proxy_interaction_coverage_journal_trigger",
		"CREATE TRIGGER proxy_interaction_coverage_outbox_trigger",
		"covered.block_number - row_number() OVER",
		"required_start.block_hash = target_start_hash",
		"required_end.block_hash = target_end_hash",
		"range_start.block_hash = coverage.start_block_hash",
		"range_end.block_hash = coverage.end_block_hash",
	} {
		if !strings.Contains(proxyCoverageRangesSQL, fragment) {
			t.Errorf("proxy coverage-ranges migration missing %q", fragment)
		}
	}
	membershipSQL, _, found := strings.Cut(
		proxyCoverageRangesSQL,
		"CREATE OR REPLACE FUNCTION refresh_proxy_interaction_coverage_block",
	)
	if !found {
		t.Fatal("proxy coverage-ranges migration lacks refresh function boundary")
	}
	if strings.Contains(membershipSQL, "generate_series") {
		t.Error("proxy coverage membership function must not scan generated chain heights")
	}
	for _, fragment := range []string{
		"DROP TABLE IF EXISTS verification_jobs CASCADE",
		"CREATE TABLE compiler_catalog_generations",
		"CREATE TABLE compiler_catalog_entries",
		"CREATE TABLE verification_jobs",
		"CREATE TABLE verification_results",
		"CREATE TABLE verified_contracts",
		"runtime_match,match_type",
	} {
		if !strings.Contains(verifierV2SQL, fragment) {
			t.Errorf("verifier v2 migration missing %q", fragment)
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
	for _, fragment := range []string{
		"transaction_inclusions_from_address_idx",
		"lower(raw->>'from')",
		"transaction_inclusions_to_address_idx",
		"receipts_contract_address_idx",
		"normalized_traces_created_idx",
		"WHERE canonical AND created_address IS NOT NULL",
	} {
		if !strings.Contains(addressActivitySQL, fragment) {
			t.Errorf("address activity migration missing %q", fragment)
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
