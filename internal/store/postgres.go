package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
)

// Multi-statement canonical and coverage writes using this isolation level
// first take lockChain. READ COMMITTED is part of that protocol: after a
// process waits for the advisory lock, its next statement must observe the
// preceding lock holder's commit. Snapshot-wide isolation would instead turn
// expected multi-role contention into SQLSTATE 40001 or stale duplicate-insert
// failures.
const chainWriteIsolation sql.IsolationLevel = sql.LevelReadCommitted

type PostgresRepository struct {
	db         *sql.DB
	partitions partitionRangeCache
}

func NewPostgresRepository(db *sql.DB) (*PostgresRepository, error) {
	if db == nil {
		return nil, errors.New("PostgreSQL repository database is nil")
	}
	return &PostgresRepository{db: db, partitions: newPartitionRangeCache()}, nil
}

func (r *PostgresRepository) CanonicalTip(ctx context.Context, chainID string) (BlockRef, bool, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return BlockRef{}, false, err
	}
	return queryCanonicalTip(ctx, r.db, chainID, false)
}

func (r *PostgresRepository) CanonicalBlock(ctx context.Context, chainID string, number uint64) (BlockRef, bool, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return BlockRef{}, false, err
	}
	return queryCanonicalBlock(ctx, r.db, chainID, number, false)
}

func (r *PostgresRepository) BundleByHash(ctx context.Context, chainID string, hash common.Hash) (chainbundle.Bundle, bool, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return chainbundle.Bundle{}, false, err
	}
	hashBytes := hash.Bytes()
	var blockJSON []byte
	err = r.db.QueryRowContext(ctx, dbgen.StoreLegacyBundleByHashStatement1, chainID, hashBytes).Scan(&blockJSON)
	if err == sql.ErrNoRows {
		return chainbundle.Bundle{}, false, nil
	}
	if err != nil {
		return chainbundle.Bundle{}, false, fmt.Errorf("query block by hash: %w", err)
	}
	bundle, err := chainbundle.DecodeStoredBlock(blockJSON)
	if err != nil {
		return chainbundle.Bundle{}, false, fmt.Errorf("decode stored block: %w", err)
	}
	if bundle.Block.Hash() != hash {
		return chainbundle.Bundle{}, false, errors.New(
			"decode stored block: block hash does not match requested identity",
		)
	}
	rows, err := r.db.QueryContext(ctx, dbgen.StoreLegacyBundleByHashStatement2, chainID, hashBytes)
	if err != nil {
		return chainbundle.Bundle{}, false, fmt.Errorf("query stored receipts: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	rawReceipts := make([]json.RawMessage, 0, len(bundle.Block.Transactions()))
	for rows.Next() {
		var receiptJSON []byte
		if err := rows.Scan(&receiptJSON); err != nil {
			return chainbundle.Bundle{}, false, fmt.Errorf("scan stored receipt: %w", err)
		}
		rawReceipts = append(rawReceipts, json.RawMessage(receiptJSON))
	}
	if err := rows.Err(); err != nil {
		return chainbundle.Bundle{}, false, fmt.Errorf("iterate stored receipts: %w", err)
	}
	bundle, err = bundle.WithStoredReceipts(rawReceipts)
	if err != nil {
		return chainbundle.Bundle{}, false, fmt.Errorf("decode stored receipts: %w", err)
	}
	return bundle, true, nil
}

func (r *PostgresRepository) CommitCanonical(ctx context.Context, chainID string, bundle chainbundle.Bundle, checkpoint Checkpoint) error {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return err
	}
	if err := chainbundle.Validate(bundle); err != nil {
		return err
	}
	reference, err := RefFromBundle(bundle)
	if err != nil {
		return err
	}
	if err := ValidateCheckpoint(checkpoint, reference); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: chainWriteIsolation,
	})
	if err != nil {
		return fmt.Errorf("begin canonical commit: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := lockChain(ctx, tx, chainID); err != nil {
		return err
	}
	if err := ensureChain(ctx, tx, chainID); err != nil {
		return err
	}
	existing, exists, err := queryCanonicalBlock(ctx, tx, chainID, reference.Number, true)
	if err != nil {
		return err
	}
	if exists && existing.Hash != reference.Hash {
		return fmt.Errorf("%w: height %d already maps to another hash", ErrConflict, reference.Number)
	}
	if !exists {
		tip, tipExists, err := queryCanonicalTip(ctx, tx, chainID, true)
		if err != nil {
			return err
		}
		if tipExists && (reference.Number != tip.Number+1 || reference.ParentHash != tip.Hash) {
			return fmt.Errorf("%w: block does not extend canonical tip", ErrConflict)
		}
	}
	if err := checkCheckpointTx(ctx, tx, chainID, checkpoint, false); err != nil {
		return err
	}
	ensuredPartitions, err := r.ensureBundlePartitionsTx(ctx, tx, []BlockRef{reference})
	if err != nil {
		return err
	}
	if err := putBundleTx(ctx, tx, chainID, bundle); err != nil {
		return err
	}
	if !exists {
		if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyCommitCanonicalStatement1, chainID, decimal(reference.Number), mustHashBytes(reference.Hash)); err != nil {
			return fmt.Errorf("insert canonical block: %w", err)
		}
	}
	if err := upsertCheckpointTx(ctx, tx, chainID, checkpoint); err != nil {
		return err
	}
	if !exists {
		if err := insertCoreOutboxTx(ctx, tx, chainID, "core.block.canonical", reference); err != nil {
			return err
		}
		if err := insertRuntimeHeadEventTx(ctx, tx, chainID, reference); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit canonical block: %w", err)
	}
	r.partitions.add(ensuredPartitions...)
	return nil
}

// RefreshCanonical replaces the core facts scoped to one already-canonical
// block hash. It deliberately leaves canonical_blocks, index_checkpoints,
// reorg history, journals, and facts belonging to every other block hash
// untouched. The chain advisory lock and one transaction make the
// identity/finality checks and replacement one atomic operation.
func (r *PostgresRepository) RefreshCanonical(
	ctx context.Context,
	chainID string,
	bundle chainbundle.Bundle,
	options RefreshOptions,
) error {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return err
	}
	if err := chainbundle.Validate(bundle); err != nil {
		return err
	}
	reference, err := RefFromBundle(bundle)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: chainWriteIsolation,
	})
	if err != nil {
		return fmt.Errorf("begin canonical refresh: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := lockChain(ctx, tx, chainID); err != nil {
		return err
	}
	canonical, exists, err := queryCanonicalBlock(ctx, tx, chainID, reference.Number, true)
	if err != nil {
		return err
	}
	if !exists || canonical.Hash != reference.Hash {
		return fmt.Errorf("%w: block %d hash %s is not canonical", ErrConflict, reference.Number, reference.Hash)
	}
	if err := validateRefreshParentTx(ctx, tx, chainID, reference); err != nil {
		return err
	}
	finality, hasFinality, err := queryFinality(ctx, tx, chainID, true)
	if err != nil {
		return err
	}
	if !options.AllowFinalized && hasFinality && finality.Finalized != nil &&
		reference.Number <= finality.Finalized.Number {
		return fmt.Errorf(
			"%w: block %d is at or below finalized height %d",
			ErrFinalizedRefresh, reference.Number, finality.Finalized.Number,
		)
	}
	ensuredPartitions, err := r.ensureBundlePartitionsTx(ctx, tx, []BlockRef{reference})
	if err != nil {
		return err
	}
	if err := deleteBundleFactsTx(ctx, tx, chainID, reference); err != nil {
		return err
	}
	if err := putBundleTx(ctx, tx, chainID, bundle); err != nil {
		return fmt.Errorf("rewrite canonical block %d: %w", reference.Number, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit canonical refresh: %w", err)
	}
	r.partitions.add(ensuredPartitions...)
	return nil
}

func (r *PostgresRepository) ApplyReorg(ctx context.Context, chainID string, reorg Reorg) error {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return err
	}
	if err := ValidateReorg(reorg); err != nil {
		return err
	}
	attachedReferences := make([]BlockRef, len(reorg.Attached))
	for index, bundle := range reorg.Attached {
		attachedReferences[index], err = RefFromBundle(bundle)
		if err != nil {
			return err
		}
	}
	// A block-bound enrichment write may hold KEY SHARE on a canonical row
	// while this fork-choice waits to detach it. Core fork-choice is already
	// serialized by the chain advisory lock and canonical row locks. Keep
	// statement snapshots fresh so setDerivedCanonicalTx sees any derived fact
	// that committed while the detach was waiting and marks it orphaned in this
	// same transaction.
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: chainWriteIsolation})
	if err != nil {
		return fmt.Errorf("begin reorg: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := lockChain(ctx, tx, chainID); err != nil {
		return err
	}
	ancestor, exists, err := queryCanonicalBlock(ctx, tx, chainID, reorg.Ancestor.Number, true)
	if err != nil {
		return err
	}
	if !exists || ancestor.Hash != reorg.Ancestor.Hash {
		return fmt.Errorf("%w: common ancestor is not canonical", ErrConflict)
	}
	tip, exists, err := queryCanonicalTip(ctx, tx, chainID, true)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: canonical chain is empty", ErrConflict)
	}
	if len(reorg.Detached) > 0 && tip.Hash != reorg.Detached[0].Hash {
		return fmt.Errorf("%w: detached branch does not begin at canonical tip", ErrConflict)
	}
	for _, detached := range reorg.Detached {
		canonical, exists, err := queryCanonicalBlock(ctx, tx, chainID, detached.Number, true)
		if err != nil {
			return err
		}
		if !exists || canonical.Hash != detached.Hash {
			return fmt.Errorf("%w: detached block %d is not canonical", ErrConflict, detached.Number)
		}
	}
	currentFinality, hasFinality, err := queryFinality(ctx, tx, chainID, true)
	if err != nil {
		return err
	}
	if hasFinality && currentFinality.Finalized != nil && reorg.Ancestor.Number < currentFinality.Finalized.Number {
		return fmt.Errorf("%w: reorg ancestor %d is below finalized height %d", ErrConflict, reorg.Ancestor.Number, currentFinality.Finalized.Number)
	}
	ensuredPartitions, err := r.ensureBundlePartitionsTx(ctx, tx, attachedReferences)
	if err != nil {
		return err
	}
	for _, bundle := range reorg.Attached {
		if err := putBundleTx(ctx, tx, chainID, bundle); err != nil {
			return err
		}
	}
	for _, detached := range reorg.Detached {
		result, err := tx.ExecContext(ctx, dbgen.StoreLegacyApplyReorgStatement1, chainID, decimal(detached.Number), mustHashBytes(detached.Hash))
		if err != nil {
			return fmt.Errorf("detach canonical block %d: %w", detached.Number, err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return fmt.Errorf("%w: detach canonical block %d affected %d rows", ErrConflict, detached.Number, affected)
		}
		if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyApplyReorgStatement2, chainID, mustHashBytes(detached.Hash)); err != nil {
			return fmt.Errorf("mark detached journals: %w", err)
		}
		if err := setDerivedCanonicalTx(ctx, tx, chainID, detached.Hash, false); err != nil {
			return err
		}
		if err := insertCoreOutboxTx(ctx, tx, chainID, "core.block.orphaned", detached); err != nil {
			return err
		}
	}
	for _, bundle := range reorg.Attached {
		reference, _ := RefFromBundle(bundle)
		if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyApplyReorgStatement3, chainID, decimal(reference.Number), mustHashBytes(reference.Hash)); err != nil {
			return fmt.Errorf("attach canonical block %d: %w", reference.Number, err)
		}
		if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyApplyReorgStatement4, chainID, mustHashBytes(reference.Hash)); err != nil {
			return fmt.Errorf("mark attached journals: %w", err)
		}
		if err := setDerivedCanonicalTx(ctx, tx, chainID, reference.Hash, true); err != nil {
			return err
		}
		if err := insertCoreOutboxTx(ctx, tx, chainID, "core.block.canonical", reference); err != nil {
			return err
		}
	}
	checkpoint := reorg.Checkpoint
	checkpointExists := true
	configuredStart, configured, err := queryConfiguredStartTx(ctx, tx, chainID, true)
	if err != nil {
		return err
	}
	if configured {
		ranges, err := queryCoverageRangesTx(ctx, tx, chainID)
		if err != nil {
			return err
		}
		nextRanges, err := coverageRangesAfterReorg(ranges, reorg)
		if err != nil {
			return err
		}
		for _, blockRange := range nextRanges {
			if blockRange.Start < configuredStart {
				return fmt.Errorf("%w: reorg coverage starts below configured height", ErrConflict)
			}
		}
		if err := replaceCoverageRangesTx(ctx, tx, chainID, nextRanges); err != nil {
			return err
		}
		coverage, exists, err := queryCoverageTx(ctx, tx, chainID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrIndexNotConfigured
		}
		if coverage.Contiguous == nil {
			checkpointExists = false
		} else {
			checkpoint = NewCoreCheckpoint(*coverage.Contiguous)
		}
	}
	if checkpointExists {
		if err := checkCheckpointTx(ctx, tx, chainID, checkpoint, true); err != nil {
			return err
		}
		if err := upsertCheckpointTx(ctx, tx, chainID, checkpoint); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyApplyReorgStatement5, chainID, CoreCheckpoint); err != nil {
		return fmt.Errorf("delete non-contiguous core checkpoint: %w", err)
	}
	if err := insertReorgEvent(ctx, tx, chainID, tip, reorg); err != nil {
		return err
	}
	if err := insertRuntimeReorgEventTx(ctx, tx, chainID, tip, reorg); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reorg: %w", err)
	}
	r.partitions.add(ensuredPartitions...)
	return nil
}

func (r *PostgresRepository) Checkpoint(ctx context.Context, chainID, stage string) (Checkpoint, bool, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return Checkpoint{}, false, err
	}
	var height string
	var hash []byte
	var updatedAt time.Time
	err = r.db.QueryRowContext(ctx, dbgen.StoreLegacyCheckpointStatement1, chainID, stage).Scan(&height, &hash, &updatedAt)
	if err == sql.ErrNoRows {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, fmt.Errorf("query checkpoint: %w", err)
	}
	parsedHeight, err := strconv.ParseUint(height, 10, 64)
	if err != nil {
		return Checkpoint{}, false, fmt.Errorf("decode checkpoint height: %w", err)
	}
	parsedHash, err := hashFromBytes(hash)
	if err != nil {
		return Checkpoint{}, false, err
	}
	return Checkpoint{Stage: stage, ContiguousThrough: parsedHeight, BlockHash: parsedHash, UpdatedAt: updatedAt}, true, nil
}

func (r *PostgresRepository) Finality(ctx context.Context, chainID string) (Finality, bool, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return Finality{}, false, err
	}
	return queryFinality(ctx, r.db, chainID, false)
}

func (r *PostgresRepository) UpdateFinality(ctx context.Context, chainID string, finality Finality) error {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return err
	}
	if err := ValidateFinality(finality); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: chainWriteIsolation})
	if err != nil {
		return fmt.Errorf("begin finality update: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := lockChain(ctx, tx, chainID); err != nil {
		return err
	}
	if err := ensureChain(ctx, tx, chainID); err != nil {
		return err
	}
	for name, reference := range map[string]*BlockRef{"safe": finality.Safe, "finalized": finality.Finalized} {
		if reference == nil {
			continue
		}
		canonical, exists, err := queryCanonicalBlock(ctx, tx, chainID, reference.Number, true)
		if err != nil {
			return err
		}
		if !exists || canonical.Hash != reference.Hash {
			return fmt.Errorf("%w: %s block is not canonical", ErrConflict, name)
		}
	}
	previous, exists, err := queryFinality(ctx, tx, chainID, true)
	if err != nil {
		return err
	}
	if exists {
		if err := checkFinalityRegression(previous, finality); err != nil {
			return err
		}
	}
	updatedAt := finality.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyUpdateFinalityStatement1, chainID, nullableNumber(finality.Safe), nullableHash(finality.Safe),
		nullableNumber(finality.Finalized), nullableHash(finality.Finalized), updatedAt,
	); err != nil {
		return fmt.Errorf("upsert finality: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit finality update: %w", err)
	}
	return nil
}

func (r *PostgresRepository) AppendJournal(ctx context.Context, chainID string, entry JournalEntry) error {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return err
	}
	if entry.Stage == "" {
		return errors.New("journal stage is empty")
	}
	if !json.Valid(entry.Payload) {
		return errors.New("journal payload is not valid JSON")
	}
	createdAt := entry.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, dbgen.StoreLegacyAppendJournalStatement1, chainID, mustHashBytes(entry.BlockHash), entry.Stage, decimal(entry.Sequence), []byte(entry.Payload), createdAt)
	if err != nil {
		return fmt.Errorf("append block journal: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return fmt.Errorf("%w: journal block is unknown", ErrConflict)
	}
	return nil
}

func (r *PostgresRepository) JournalsByBlock(ctx context.Context, chainID string, hash common.Hash) ([]JournalEntry, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, dbgen.StoreLegacyJournalsByBlockStatement1, chainID, mustHashBytes(hash))
	if err != nil {
		return nil, fmt.Errorf("query block journals: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var entries []JournalEntry
	for rows.Next() {
		var entry JournalEntry
		var sequence string
		if err := rows.Scan(&entry.Stage, &sequence, &entry.Payload, &entry.Canonical, &entry.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan block journal: %w", err)
		}
		entry.BlockHash = hash
		entry.Sequence, err = strconv.ParseUint(sequence, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("decode journal sequence: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate block journals: %w", err)
	}
	return entries, nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func queryCanonicalTip(ctx context.Context, queryer queryer, chainID string, forUpdate bool) (BlockRef, bool, error) {
	query := dbgen.StoreCanonicalTip
	if forUpdate {
		query = dbgen.StoreLockCanonicalTip
	}
	return scanBlockRef(queryer.QueryRowContext(ctx, query, chainID), "query canonical tip")
}

func queryCanonicalBlock(ctx context.Context, queryer queryer, chainID string, number uint64, forUpdate bool) (BlockRef, bool, error) {
	query := dbgen.StoreCanonicalBlock
	if forUpdate {
		query = dbgen.StoreLockCanonicalBlock
	}
	return scanBlockRef(queryer.QueryRowContext(ctx, query, chainID, decimal(number)), "query canonical block")
}

func validateRefreshParentTx(ctx context.Context, tx *sql.Tx, chainID string, reference BlockRef) error {
	if reference.Number == 0 {
		return nil
	}
	parent, exists, err := queryCanonicalBlock(ctx, tx, chainID, reference.Number-1, true)
	if err != nil {
		return err
	}
	if exists {
		if reference.ParentHash != parent.Hash {
			return fmt.Errorf(
				"%w: block %d parent %s does not match canonical block %d hash %s",
				ErrConflict, reference.Number, reference.ParentHash, parent.Number, parent.Hash,
			)
		}
		return nil
	}
	var hasLowerCanonical bool
	if err := tx.QueryRowContext(ctx, dbgen.StoreLegacyValidateRefreshParentTxStatement1, chainID, decimal(reference.Number)).Scan(&hasLowerCanonical); err != nil {
		return fmt.Errorf("check canonical refresh predecessor: %w", err)
	}
	if hasLowerCanonical {
		return fmt.Errorf("%w: canonical gap before block %d", ErrConflict, reference.Number)
	}
	return nil
}

func deleteBundleFactsTx(ctx context.Context, tx *sql.Tx, chainID string, reference BlockRef) error {
	// Invalidate bundle-derived rows and their undo journals for this exact
	// identity. State-root observations (contract code, proxy and names) and
	// cross-block token-contract knowledge remain stable under the same block
	// hash. Other block hashes, including orphan inclusions, are outside every
	// predicate. Repair never schedules enrichment: operators must explicitly
	// reindex the affected ABI/token/stats/trace range after core refresh.
	if _, err := tx.ExecContext(ctx, dbgen.StoreDeleteDerivedBlockFacts,
		chainID, decimal(reference.Number), mustHashBytes(reference.Hash)); err != nil {
		return fmt.Errorf("invalidate canonical derived facts for block %d: %w", reference.Number, err)
	}
	if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyDeleteBundleFactsTxStatement1, chainID, mustHashBytes(reference.Hash)); err != nil {
		return fmt.Errorf("invalidate canonical block_journals for block %d: %w", reference.Number, err)
	}
	if _, err := tx.ExecContext(ctx, dbgen.StoreDeleteCoreBlockFacts,
		chainID, decimal(reference.Number), mustHashBytes(reference.Hash)); err != nil {
		return fmt.Errorf("delete canonical core facts for block %d: %w", reference.Number, err)
	}
	return nil
}

func scanBlockRef(row *sql.Row, operation string) (BlockRef, bool, error) {
	var number string
	var hash, parentHash []byte
	if err := row.Scan(&number, &hash, &parentHash); err != nil {
		if err == sql.ErrNoRows {
			return BlockRef{}, false, nil
		}
		return BlockRef{}, false, fmt.Errorf("%s: %w", operation, err)
	}
	parsedNumber, err := strconv.ParseUint(number, 10, 64)
	if err != nil {
		return BlockRef{}, false, fmt.Errorf("%s: decode block number: %w", operation, err)
	}
	parsedHash, err := hashFromBytes(hash)
	if err != nil {
		return BlockRef{}, false, fmt.Errorf("%s: %w", operation, err)
	}
	parsedParent, err := hashFromBytes(parentHash)
	if err != nil {
		return BlockRef{}, false, fmt.Errorf("%s: %w", operation, err)
	}
	return BlockRef{Number: parsedNumber, Hash: parsedHash, ParentHash: parsedParent}, true, nil
}

func putBundleTx(ctx context.Context, tx *sql.Tx, chainID string, bundle chainbundle.Bundle) error {
	if err := chainbundle.Validate(bundle); err != nil {
		return fmt.Errorf("validate bundle for persistence: %w", err)
	}
	reference, _ := RefFromBundle(bundle)
	blockJSON, err := chainbundle.EncodeStoredBlock(bundle)
	if err != nil {
		return fmt.Errorf("encode block persistence: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyPutBundleTxStatement1, chainID, decimal(reference.Number), mustHashBytes(reference.Hash),
		mustHashBytes(reference.ParentHash), decimal(bundle.Block.Time()),
		blockJSON); err != nil {
		return fmt.Errorf("upsert block %d: %w", reference.Number, err)
	}
	for index, transaction := range bundle.Block.Transactions() {
		transactionHash := transaction.Hash()
		transactionJSON := bundle.RawTransactions[index]
		if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyPutBundleTxStatement2, chainID, mustHashBytes(transactionHash),
			strconv.FormatUint(uint64(transaction.Type()), 10),
			transactionJSON); err != nil {
			return fmt.Errorf("upsert transaction %d: %w", index, err)
		}
		if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyPutBundleTxStatement3, chainID, decimal(reference.Number), mustHashBytes(reference.Hash), index,
			mustHashBytes(transactionHash), transactionJSON); err != nil {
			return fmt.Errorf("upsert transaction inclusion %d: %w", index, err)
		}

		receipt := bundle.Receipts[index]
		receiptJSON := bundle.RawReceipts[index]
		if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyPutBundleTxStatement4, chainID, decimal(reference.Number), mustHashBytes(reference.Hash), index,
			mustHashBytes(transactionHash), receiptJSON); err != nil {
			return fmt.Errorf("upsert receipt %d: %w", index, err)
		}
		for logPosition := range receipt.Logs {
			log := receipt.Logs[logPosition]
			logJSON := bundle.RawLogs[index][logPosition]
			var topic0 any
			if len(log.Topics) > 0 {
				topic0 = mustHashBytes(log.Topics[0])
			}
			if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyPutBundleTxStatement5, chainID, decimal(reference.Number), mustHashBytes(reference.Hash), log.Index,
				index, mustHashBytes(transactionHash), log.Address.Bytes(), topic0, logJSON); err != nil {
				return fmt.Errorf("upsert log %d: %w", logPosition, err)
			}
		}
	}
	for index, withdrawal := range bundle.Block.Withdrawals() {
		withdrawalJSON := bundle.RawWithdrawals[index]
		if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyPutBundleTxStatement6, chainID, decimal(reference.Number), mustHashBytes(reference.Hash),
			decimal(withdrawal.Index), decimal(withdrawal.Validator),
			withdrawal.Address.Bytes(), decimal(withdrawal.Amount), withdrawalJSON); err != nil {
			return fmt.Errorf("upsert withdrawal %d: %w", index, err)
		}
	}
	return nil
}

func ensureChain(ctx context.Context, tx *sql.Tx, chainID string) error {
	if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyEnsureChainStatement1, chainID); err != nil {
		return fmt.Errorf("ensure chain row: %w", err)
	}
	return nil
}

func lockChain(ctx context.Context, tx *sql.Tx, chainID string) error {
	if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyLockChainStatement1, chainID); err != nil {
		return fmt.Errorf("lock chain: %w", err)
	}
	return nil
}

func checkCheckpointTx(ctx context.Context, tx *sql.Tx, chainID string, checkpoint Checkpoint, allowRegression bool) error {
	var height string
	var hash []byte
	err := tx.QueryRowContext(ctx, dbgen.StoreLegacyCheckCheckpointTxStatement1, chainID, checkpoint.Stage).Scan(&height, &hash)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read checkpoint for update: %w", err)
	}
	if allowRegression {
		return nil
	}
	previous, err := strconv.ParseUint(height, 10, 64)
	if err != nil {
		return fmt.Errorf("decode previous checkpoint: %w", err)
	}
	if checkpoint.ContiguousThrough < previous {
		return ErrCheckpointRegress
	}
	if checkpoint.ContiguousThrough == previous {
		previousHash, err := hashFromBytes(hash)
		if err != nil {
			return err
		}
		if previousHash != checkpoint.BlockHash {
			return fmt.Errorf("%w: hash changed at height %d", ErrCheckpointRegress, previous)
		}
	}
	return nil
}

func upsertCheckpointTx(ctx context.Context, tx *sql.Tx, chainID string, checkpoint Checkpoint) error {
	updatedAt := checkpoint.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyUpsertCheckpointTxStatement1, chainID, checkpoint.Stage, decimal(checkpoint.ContiguousThrough),
		mustHashBytes(checkpoint.BlockHash), updatedAt); err != nil {
		return fmt.Errorf("upsert checkpoint: %w", err)
	}
	return nil
}

func queryFinality(ctx context.Context, queryer queryer, chainID string, forUpdate bool) (Finality, bool, error) {
	query := dbgen.StoreFinality
	if forUpdate {
		query = dbgen.StoreLockFinality
	}
	var safeNumber, finalizedNumber sql.NullString
	var safeHash, finalizedHash []byte
	var updatedAt time.Time
	err := queryer.QueryRowContext(ctx, query, chainID).Scan(
		&safeNumber, &safeHash, &finalizedNumber, &finalizedHash, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return Finality{}, false, nil
	}
	if err != nil {
		return Finality{}, false, fmt.Errorf("query finality: %w", err)
	}
	finality := Finality{UpdatedAt: updatedAt}
	if safeNumber.Valid {
		reference, err := nullableBlockRef(safeNumber.String, safeHash)
		if err != nil {
			return Finality{}, false, err
		}
		finality.Safe = &reference
	}
	if finalizedNumber.Valid {
		reference, err := nullableBlockRef(finalizedNumber.String, finalizedHash)
		if err != nil {
			return Finality{}, false, err
		}
		finality.Finalized = &reference
	}
	return finality, true, nil
}

func nullableBlockRef(number string, hashBytes []byte) (BlockRef, error) {
	height, err := strconv.ParseUint(number, 10, 64)
	if err != nil {
		return BlockRef{}, fmt.Errorf("decode finality height: %w", err)
	}
	hash, err := hashFromBytes(hashBytes)
	if err != nil {
		return BlockRef{}, err
	}
	return BlockRef{Number: height, Hash: hash}, nil
}

func insertReorgEvent(ctx context.Context, tx *sql.Tx, chainID string, oldTip BlockRef, reorg Reorg) error {
	detachedJSON, err := json.Marshal(reorg.Detached)
	if err != nil {
		return fmt.Errorf("encode detached reorg branch: %w", err)
	}
	attachedRefs := make([]BlockRef, len(reorg.Attached))
	for index, bundle := range reorg.Attached {
		attachedRefs[index], _ = RefFromBundle(bundle)
	}
	attachedJSON, err := json.Marshal(attachedRefs)
	if err != nil {
		return fmt.Errorf("encode attached reorg branch: %w", err)
	}
	newTip := reorg.Ancestor
	if len(attachedRefs) > 0 {
		newTip = attachedRefs[len(attachedRefs)-1]
	}
	if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyInsertReorgEventStatement1, chainID, decimal(reorg.Ancestor.Number), mustHashBytes(reorg.Ancestor.Hash),
		decimal(oldTip.Number), mustHashBytes(oldTip.Hash), decimal(newTip.Number),
		mustHashBytes(newTip.Hash), detachedJSON, attachedJSON, reorg.Reason); err != nil {
		return fmt.Errorf("insert reorg audit event: %w", err)
	}
	return nil
}

func insertRuntimeHeadEventTx(ctx context.Context, tx *sql.Tx, chainID string, reference BlockRef) error {
	return insertRuntimeEventTx(ctx, tx, chainID, "head", map[string]string{
		"number":      decimal(reference.Number),
		"hash":        reference.Hash.String(),
		"parent_hash": reference.ParentHash.String(),
	})
}

func insertRuntimeReorgEventTx(ctx context.Context, tx *sql.Tx, chainID string, oldTip BlockRef, reorg Reorg) error {
	newTip := reorg.Ancestor
	if len(reorg.Attached) > 0 {
		newTip, _ = RefFromBundle(reorg.Attached[len(reorg.Attached)-1])
	}
	payload := map[string]string{
		"ancestor_number": decimal(reorg.Ancestor.Number),
		"ancestor_hash":   reorg.Ancestor.Hash.String(),
		"old_tip_number":  decimal(oldTip.Number),
		"old_tip_hash":    oldTip.Hash.String(),
		"new_tip_number":  decimal(newTip.Number),
		"new_tip_hash":    newTip.Hash.String(),
		"detached_count":  decimal(uint64(len(reorg.Detached))),
		"attached_count":  decimal(uint64(len(reorg.Attached))),
	}
	return insertRuntimeEventTx(ctx, tx, chainID, "reorg", payload)
}

func insertRuntimeEventTx(ctx context.Context, tx *sql.Tx, chainID, eventType string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode %s runtime event: %w", eventType, err)
	}
	if len(encoded) > 8192 {
		return fmt.Errorf("encode %s runtime event: payload exceeds 8192 bytes", eventType)
	}
	if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyInsertRuntimeEventTxStatement1, chainID, eventType, encoded); err != nil {
		return fmt.Errorf("insert %s runtime event: %w", eventType, err)
	}
	return nil
}

func insertCoreOutboxTx(ctx context.Context, tx *sql.Tx, chainID, topic string, reference BlockRef) error {
	payload, err := json.Marshal(map[string]string{
		"block_hash":   reference.Hash.String(),
		"block_number": decimal(reference.Number),
	})
	if err != nil {
		return fmt.Errorf("encode core outbox message: %w", err)
	}
	messageKey := reference.Hash.String()
	if _, err := tx.ExecContext(ctx, dbgen.StoreLegacyInsertCoreOutboxTxStatement1, chainID, topic, messageKey, payload); err != nil {
		return fmt.Errorf("insert %s outbox message: %w", topic, err)
	}
	return nil
}

func setDerivedCanonicalTx(ctx context.Context, tx *sql.Tx, chainID string, hash common.Hash, canonical bool) error {
	if _, err := tx.ExecContext(ctx, dbgen.StoreSetDerivedCanonical,
		chainID, mustHashBytes(hash), canonical); err != nil {
		return fmt.Errorf("set derived canonical=%t: %w", canonical, err)
	}
	return nil
}

func normalizeChainID(chainID string) (string, error) {
	normalized, err := ethrpc.NormalizeChainID(chainID)
	if err != nil {
		return "", fmt.Errorf("invalid chain ID: %w", err)
	}
	return normalized, nil
}

func decimal(value uint64) string { return strconv.FormatUint(value, 10) }

func mustHashBytes(hash common.Hash) []byte {
	return hash.Bytes()
}

func hashFromBytes(value []byte) (common.Hash, error) {
	if len(value) != common.HashLength {
		return common.Hash{}, fmt.Errorf(
			"stored hash has %d bytes, expected %d",
			len(value), common.HashLength,
		)
	}
	return common.BytesToHash(value), nil
}

func nullableNumber(reference *BlockRef) any {
	if reference == nil {
		return nil
	}
	return decimal(reference.Number)
}

func nullableHash(reference *BlockRef) any {
	if reference == nil {
		return nil
	}
	return mustHashBytes(reference.Hash)
}
