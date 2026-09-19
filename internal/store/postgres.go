package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
)

// Multi-statement canonical and coverage writes using this isolation level
// first take lockChain. READ COMMITTED is part of that protocol: after a
// process waits for the advisory lock, its next statement must observe the
// preceding lock holder's commit. Snapshot-wide isolation would instead turn
// expected multi-role contention into SQLSTATE 40001 or stale duplicate-insert
// failures.
const chainWriteIsolation pgx.TxIsoLevel = pgx.ReadCommitted

type PostgresRepository struct {
	db         dbaccess.Database
	partitions partitionRangeCache
}

func NewPostgresRepository(db dbaccess.Database) (*PostgresRepository, error) {
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
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(r.db).StoreLegacyBundleByHashStatement1(ctx, queryValue0, hashBytes)
		if err != nil {
			return err
		}
		blockJSON = queryRow
		return nil
	}()
	if err == pgx.ErrNoRows {
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
	rows, err := func() ([][]byte, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return nil, err
		}
		return dbgen.New(r.db).StoreLegacyBundleByHashStatement2(ctx, queryValue0, hashBytes)
	}()
	if err != nil {
		return chainbundle.Bundle{}, false, fmt.Errorf("query stored receipts: %w", err)
	}

	rawReceipts := make([]json.RawMessage, 0, len(bundle.Block.Transactions()))
	for _, storedRow := range rows {
		var receiptJSON []byte
		{
			receiptJSON = storedRow
		}
		rawReceipts = append(rawReceipts, json.RawMessage(receiptJSON))
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
	bundle, err = cloneBundle(bundle)
	if err != nil {
		return err
	}
	reference, err := RefFromBundle(bundle)
	if err != nil {
		return err
	}
	if err := ValidateCheckpoint(checkpoint, reference); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: chainWriteIsolation,
	})
	if err != nil {
		return fmt.Errorf("begin canonical commit: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
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
		if err := insertCanonicalBlocksTx(ctx, tx, chainID, []BlockRef{reference}); err != nil {
			return err
		}
	}
	if err := upsertCheckpointTx(ctx, tx, chainID, checkpoint); err != nil {
		return err
	}
	if !exists {
		if err := insertCoreOutboxBatchTx(ctx, tx, chainID, []coreOutboxMessage{{
			Topic: "core.block.canonical", Reference: reference,
		}}); err != nil {
			return err
		}
		if err := insertRuntimeHeadEventTx(ctx, tx, chainID, reference); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
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
	bundle, err = cloneBundle(bundle)
	if err != nil {
		return err
	}
	reference, err := RefFromBundle(bundle)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: chainWriteIsolation,
	})
	if err != nil {
		return fmt.Errorf("begin canonical refresh: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
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
	if err := tx.Commit(ctx); err != nil {
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
	reorg, err = ownReorg(reorg)
	if err != nil {
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
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: chainWriteIsolation})
	if err != nil {
		return fmt.Errorf("begin reorg: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
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
	if err := putBundlesTx(ctx, tx, chainID, reorg.Attached); err != nil {
		return err
	}
	if err := deleteCanonicalBlocksTx(ctx, tx, chainID, reorg.Detached); err != nil {
		return err
	}
	if err := setBlockJournalsCanonicalBatchTx(ctx, tx, chainID, reorg.Detached, false); err != nil {
		return err
	}
	if err := setDerivedCanonicalBatchTx(ctx, tx, chainID, reorg.Detached, false); err != nil {
		return err
	}
	if err := insertCanonicalBlocksTx(ctx, tx, chainID, attachedReferences); err != nil {
		return err
	}
	if err := setBlockJournalsCanonicalBatchTx(ctx, tx, chainID, attachedReferences, true); err != nil {
		return err
	}
	if err := setDerivedCanonicalBatchTx(ctx, tx, chainID, attachedReferences, true); err != nil {
		return err
	}
	outboxMessages := make([]coreOutboxMessage, 0, len(reorg.Detached)+len(attachedReferences))
	for _, detached := range reorg.Detached {
		outboxMessages = append(outboxMessages, coreOutboxMessage{
			Topic: "core.block.orphaned", Reference: detached,
		})
	}
	for _, reference := range attachedReferences {
		outboxMessages = append(outboxMessages, coreOutboxMessage{
			Topic: "core.block.canonical", Reference: reference,
		})
	}
	if err := insertCoreOutboxBatchTx(ctx, tx, chainID, outboxMessages); err != nil {
		return err
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
	} else if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		return dbgen.New(tx).StoreLegacyApplyReorgStatement5(ctx, queryValue0, CoreCheckpoint)
	}(); err != nil {
		return fmt.Errorf("delete non-contiguous core checkpoint: %w", err)
	}
	if err := insertReorgEvent(ctx, tx, chainID, tip, reorg); err != nil {
		return err
	}
	if err := insertRuntimeReorgEventTx(ctx, tx, chainID, tip, reorg); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
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
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(r.db).StoreLegacyCheckpointStatement1(ctx, queryValue0, stage)
		if err != nil {
			return err
		}
		height = queryRow.ContiguousThrough
		hash = queryRow.BlockHash
		if !queryRow.UpdatedAt.Valid {
			return errors.New("invalid stored query value")
		}
		if queryRow.UpdatedAt.InfinityModifier != pgtype.Finite {
			return errors.New("invalid stored query value")
		}
		updatedAt = queryRow.UpdatedAt.Time
		return nil
	}()
	if err == pgx.ErrNoRows {
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
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: chainWriteIsolation})
	if err != nil {
		return fmt.Errorf("begin finality update: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if nullableNumber(finality.Safe) != nil {
			if err := queryValue1.Scan(*nullableNumber(finality.Safe)); err != nil {
				return err
			}
		}
		var queryValue2 pgtype.Numeric
		if nullableNumber(finality.Finalized) != nil {
			if err := queryValue2.Scan(*nullableNumber(finality.Finalized)); err != nil {
				return err
			}
		}
		return dbgen.New(tx).StoreLegacyUpdateFinalityStatement1(ctx, dbgen.StoreLegacyUpdateFinalityStatement1Params{ChainID: queryValue0, SafeNumber: queryValue1, SafeHash: nullableHash(finality.Safe), FinalizedNumber: queryValue2, FinalizedHash: nullableHash(finality.Finalized), UpdatedAt: pgtype.Timestamptz{Time: updatedAt, Valid: true}})
	}(); err != nil {
		return fmt.Errorf("upsert finality: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
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
	result, err := func() (int64, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return 0, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(decimal(entry.Sequence)); err != nil {
			return 0, err
		}
		return dbgen.New(r.db).StoreLegacyAppendJournalStatement1(ctx, dbgen.StoreLegacyAppendJournalStatement1Params{ChainID: queryValue0, BlockHash: mustHashBytes(entry.BlockHash), Stage: entry.Stage, Sequence: queryValue1, Payload: []byte(entry.Payload), CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true}})
	}()
	if err != nil {
		return fmt.Errorf("append block journal: %w", err)
	}
	if affected := result; affected != 1 {
		return fmt.Errorf("%w: journal block is unknown", ErrConflict)
	}
	return nil
}

func (r *PostgresRepository) JournalsByBlock(ctx context.Context, chainID string, hash common.Hash) ([]JournalEntry, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return nil, err
	}
	rows, err := func() ([]dbgen.StoreLegacyJournalsByBlockStatement1Row, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return nil, err
		}
		return dbgen.New(r.db).StoreLegacyJournalsByBlockStatement1(ctx, queryValue0, mustHashBytes(hash))
	}()
	if err != nil {
		return nil, fmt.Errorf("query block journals: %w", err)
	}

	var entries []JournalEntry
	for _, storedRow := range rows {
		var entry JournalEntry
		var sequence string
		if err := func() error {
			entry.Stage = storedRow.Stage
			sequence = storedRow.Sequence
			entry.Payload = json.RawMessage(storedRow.Payload)
			entry.Canonical = storedRow.Canonical
			if !storedRow.CreatedAt.Valid {
				return errors.New("invalid stored query value")
			}
			if storedRow.CreatedAt.InfinityModifier != pgtype.Finite {
				return errors.New("invalid stored query value")
			}
			entry.CreatedAt = storedRow.CreatedAt.Time
			return nil
		}(); err != nil {
			return nil, fmt.Errorf("scan block journal: %w", err)
		}
		entry.BlockHash = hash
		entry.Sequence, err = strconv.ParseUint(sequence, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("decode journal sequence: %w", err)
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

type queryer = dbgen.DBTX

func queryCanonicalTip(ctx context.Context, queryer queryer, chainID string, forUpdate bool) (BlockRef, bool, error) {
	queries := dbgen.New(queryer)
	var row dbgen.StoreCanonicalTipRow
	var err error
	if forUpdate {
		var locked dbgen.StoreLockCanonicalTipRow
		locked, err = queries.StoreLockCanonicalTip(ctx, chainID)
		row = dbgen.StoreCanonicalTipRow(locked)
	} else {
		row, err = queries.StoreCanonicalTip(ctx, chainID)
	}
	return decodeBlockRef(row, err, "query canonical tip")
}
func queryCanonicalBlock(ctx context.Context, queryer queryer, chainID string, number uint64, forUpdate bool) (BlockRef, bool, error) {
	queries := dbgen.New(queryer)
	var row dbgen.StoreCanonicalTipRow
	var err error
	if forUpdate {
		var locked dbgen.StoreLockCanonicalBlockRow
		locked, err = queries.StoreLockCanonicalBlock(ctx, chainID, decimal(number))
		row = dbgen.StoreCanonicalTipRow(locked)
	} else {
		var value dbgen.StoreCanonicalBlockRow
		value, err = queries.StoreCanonicalBlock(ctx, chainID, decimal(number))
		row = dbgen.StoreCanonicalTipRow(value)
	}
	return decodeBlockRef(row, err, "query canonical block")
}

func validateRefreshParentTx(ctx context.Context, tx pgx.Tx, chainID string, reference BlockRef) error {
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(decimal(reference.Number)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).StoreLegacyValidateRefreshParentTxStatement1(ctx, queryValue0, queryValue1)
		if err != nil {
			return err
		}
		hasLowerCanonical = queryRow
		return nil
	}(); err != nil {
		return fmt.Errorf("check canonical refresh predecessor: %w", err)
	}
	if hasLowerCanonical {
		return fmt.Errorf("%w: canonical gap before block %d", ErrConflict, reference.Number)
	}
	return nil
}

func deleteBundleFactsTx(ctx context.Context, tx pgx.Tx, chainID string, reference BlockRef) error {
	// Invalidate bundle-derived rows and their undo journals for this exact
	// identity. State-root observations (contract code, proxy and names) and
	// cross-block token-contract knowledge remain stable under the same block
	// hash. Other block hashes, including orphan inclusions, are outside every
	// predicate. Repair never schedules enrichment: operators must explicitly
	// reindex the affected ABI/token/stats/trace range after core refresh.
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(decimal(reference.Number)); err != nil {
			return err
		}
		return dbgen.New(tx).StoreDeleteDerivedBlockFacts(ctx, queryValue0, queryValue1, mustHashBytes(reference.Hash))
	}(); err != nil {
		return fmt.Errorf("invalidate canonical derived facts for block %d: %w", reference.Number, err)
	}
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		return dbgen.New(tx).StoreLegacyDeleteBundleFactsTxStatement1(ctx, queryValue0, mustHashBytes(reference.Hash))
	}(); err != nil {
		return fmt.Errorf("invalidate canonical block_journals for block %d: %w", reference.Number, err)
	}
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(decimal(reference.Number)); err != nil {
			return err
		}
		return dbgen.New(tx).StoreDeleteCoreBlockFacts(ctx, queryValue0, queryValue1, mustHashBytes(reference.Hash))
	}(); err != nil {
		return fmt.Errorf("delete canonical core facts for block %d: %w", reference.Number, err)
	}
	return nil
}

func decodeBlockRef(row dbgen.StoreCanonicalTipRow, err error, operation string) (BlockRef, bool, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return BlockRef{}, false, nil
	}
	if err != nil {
		return BlockRef{}, false, fmt.Errorf("%s: %w", operation, err)
	}
	number, hash, parentHash := row.CanonicalNumber, row.BlockHash, row.ParentHash

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

func putBundleTx(ctx context.Context, tx pgx.Tx, chainID string, bundle chainbundle.Bundle) error {
	return putBundlesTx(ctx, tx, chainID, []chainbundle.Bundle{bundle})
}

func ensureChain(ctx context.Context, tx pgx.Tx, chainID string) error {
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		return dbgen.New(tx).StoreLegacyEnsureChainStatement1(ctx, queryValue0)
	}(); err != nil {
		return fmt.Errorf("ensure chain row: %w", err)
	}
	return nil
}

func lockChain(ctx context.Context, tx pgx.Tx, chainID string) error {
	if err := dbgen.New(tx).StoreLegacyLockChainStatement1(ctx, new(chainID)); err != nil {
		return fmt.Errorf("lock chain: %w", err)
	}
	return nil
}

func checkCheckpointTx(ctx context.Context, tx pgx.Tx, chainID string, checkpoint Checkpoint, allowRegression bool) error {
	var height string
	var hash []byte
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).StoreLegacyCheckCheckpointTxStatement1(ctx, queryValue0, checkpoint.Stage)
		if err != nil {
			return err
		}
		height = queryRow.ContiguousThrough
		hash = queryRow.BlockHash
		return nil
	}()
	if err == pgx.ErrNoRows {
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

func upsertCheckpointTx(ctx context.Context, tx pgx.Tx, chainID string, checkpoint Checkpoint) error {
	updatedAt := checkpoint.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(decimal(checkpoint.ContiguousThrough)); err != nil {
			return err
		}
		return dbgen.New(tx).StoreLegacyUpsertCheckpointTxStatement1(ctx, dbgen.StoreLegacyUpsertCheckpointTxStatement1Params{ChainID: queryValue0, Stage: checkpoint.Stage, ContiguousThrough: queryValue1, BlockHash: mustHashBytes(checkpoint.BlockHash), UpdatedAt: pgtype.Timestamptz{Time: updatedAt, Valid: true}})
	}(); err != nil {
		return fmt.Errorf("upsert checkpoint: %w", err)
	}
	return nil
}

func queryFinality(ctx context.Context, queryer queryer, chainID string, forUpdate bool) (Finality, bool, error) {
	queries := dbgen.New(queryer)
	var row dbgen.StoreFinalityRow
	var err error
	if forUpdate {
		var locked dbgen.StoreLockFinalityRow
		locked, err = queries.StoreLockFinality(ctx, chainID)
		row = dbgen.StoreFinalityRow(locked)
	} else {
		row, err = queries.StoreFinality(ctx, chainID)
	}

	if err == pgx.ErrNoRows {
		return Finality{}, false, nil
	}
	if err != nil {
		return Finality{}, false, fmt.Errorf("query finality: %w", err)
	}
	safeNumber, err := dbaccess.NumericText(row.SafeNumber)
	if err != nil {
		return Finality{}, false, err
	}
	finalizedNumber, err := dbaccess.NumericText(row.FinalizedNumber)
	if err != nil {
		return Finality{}, false, err
	}
	safeHash, finalizedHash := row.SafeHash, row.FinalizedHash
	if !row.UpdatedAt.Valid || row.UpdatedAt.InfinityModifier != pgtype.Finite {
		return Finality{}, false, errors.New("stored finality timestamp is invalid")
	}
	updatedAt := row.UpdatedAt.Time

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

func insertReorgEvent(ctx context.Context, tx pgx.Tx, chainID string, oldTip BlockRef, reorg Reorg) error {
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(decimal(reorg.Ancestor.Number)); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(decimal(oldTip.Number)); err != nil {
			return err
		}
		var queryValue3 pgtype.Numeric
		if err := queryValue3.Scan(decimal(newTip.Number)); err != nil {
			return err
		}
		return dbgen.New(tx).StoreLegacyInsertReorgEventStatement1(ctx, dbgen.StoreLegacyInsertReorgEventStatement1Params{ChainID: queryValue0, AncestorNumber: queryValue1, AncestorHash: mustHashBytes(reorg.Ancestor.Hash), OldTipNumber: queryValue2, OldTipHash: mustHashBytes(oldTip.Hash), NewTipNumber: queryValue3, NewTipHash: mustHashBytes(newTip.Hash), Detached: detachedJSON, Attached: attachedJSON, Reason: reorg.Reason})
	}(); err != nil {
		return fmt.Errorf("insert reorg audit event: %w", err)
	}
	return nil
}

func insertRuntimeHeadEventTx(ctx context.Context, tx pgx.Tx, chainID string, reference BlockRef) error {
	return insertRuntimeEventTx(ctx, tx, chainID, "head", map[string]string{
		"number":      decimal(reference.Number),
		"hash":        reference.Hash.String(),
		"parent_hash": reference.ParentHash.String(),
	})
}

func insertRuntimeReorgEventTx(ctx context.Context, tx pgx.Tx, chainID string, oldTip BlockRef, reorg Reorg) error {
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

func insertRuntimeEventTx(ctx context.Context, tx pgx.Tx, chainID, eventType string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode %s runtime event: %w", eventType, err)
	}
	if len(encoded) > 8192 {
		return fmt.Errorf("encode %s runtime event: payload exceeds 8192 bytes", eventType)
	}
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		return dbgen.New(tx).StoreLegacyInsertRuntimeEventTxStatement1(ctx, queryValue0, eventType, encoded)
	}(); err != nil {
		return fmt.Errorf("insert %s runtime event: %w", eventType, err)
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

func nullableNumber(reference *BlockRef) *string {
	if reference == nil {
		return nil
	}
	return new(decimal(reference.Number))
}

func nullableHash(reference *BlockRef) []byte {
	if reference == nil {
		return nil
	}
	return mustHashBytes(reference.Hash)
}
