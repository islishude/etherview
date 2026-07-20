package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
)

func (r *PostgresRepository) ConfigureIndex(ctx context.Context, chainID string, configuredStart uint64) error {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin index configuration: %w", err)
	}
	defer tx.Rollback()
	if err := lockChain(ctx, tx, chainID); err != nil {
		return err
	}
	if err := ensureChain(ctx, tx, chainID); err != nil {
		return err
	}
	stored, exists, err := queryConfiguredStartTx(ctx, tx, chainID, true)
	if err != nil {
		return err
	}
	if exists {
		if stored != configuredStart {
			return fmt.Errorf("%w: stored=%d requested=%d", ErrIndexConfigurationMismatch, stored, configuredStart)
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO core_index_configuration (chain_id, configured_start)
		VALUES ($1::numeric, $2::numeric)`, chainID, decimal(configuredStart)); err != nil {
		return fmt.Errorf("insert index configuration: %w", err)
	}
	references, err := queryCanonicalReferencesTx(ctx, tx, chainID, configuredStart)
	if err != nil {
		return err
	}
	ranges, err := coverageRangesFromRefs(references, configuredStart)
	if err != nil {
		return err
	}
	if err := replaceCoverageRangesTx(ctx, tx, chainID, ranges); err != nil {
		return err
	}
	coverage, _, err := queryCoverageTx(ctx, tx, chainID, false)
	if err != nil {
		return err
	}
	if coverage.Contiguous != nil {
		checkpoint := NewCoreCheckpoint(*coverage.Contiguous)
		if err := checkCheckpointTx(ctx, tx, chainID, checkpoint, true); err != nil {
			return err
		}
		if err := upsertCheckpointTx(ctx, tx, chainID, checkpoint); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx, `
		DELETE FROM index_checkpoints
		WHERE chain_id = $1::numeric AND stage = $2`, chainID, CoreCheckpoint); err != nil {
		return fmt.Errorf("clear pre-coverage core checkpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit index configuration: %w", err)
	}
	return nil
}

func (r *PostgresRepository) Coverage(ctx context.Context, chainID string) (CoreCoverage, bool, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return CoreCoverage{}, false, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return CoreCoverage{}, false, fmt.Errorf("begin coverage read: %w", err)
	}
	defer tx.Rollback()
	coverage, exists, err := queryCoverageTx(ctx, tx, chainID, false)
	if err != nil {
		return CoreCoverage{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return CoreCoverage{}, false, fmt.Errorf("commit coverage read: %w", err)
	}
	return coverage, exists, nil
}

func (r *PostgresRepository) CommitCanonicalSegment(
	ctx context.Context,
	chainID string,
	bundles []ethrpc.Bundle,
) (CoreCoverage, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return CoreCoverage{}, err
	}
	references, copies, err := validateCanonicalSegment(bundles)
	if err != nil {
		return CoreCoverage{}, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: r.bundleWriteIsolation(references)})
	if err != nil {
		return CoreCoverage{}, fmt.Errorf("begin canonical segment commit: %w", err)
	}
	defer tx.Rollback()
	if err := lockChain(ctx, tx, chainID); err != nil {
		return CoreCoverage{}, err
	}
	configuredStart, configured, err := queryConfiguredStartTx(ctx, tx, chainID, true)
	if err != nil {
		return CoreCoverage{}, err
	}
	if !configured {
		return CoreCoverage{}, ErrIndexNotConfigured
	}
	first, last := references[0], references[len(references)-1]
	if first.Number < configuredStart {
		return CoreCoverage{}, fmt.Errorf("%w: segment starts before configured height %d", ErrConflict, configuredStart)
	}
	newCanonical := make([]bool, len(references))
	for index, reference := range references {
		existing, exists, err := queryCanonicalBlock(ctx, tx, chainID, reference.Number, " FOR UPDATE")
		if err != nil {
			return CoreCoverage{}, err
		}
		if exists && (!existing.Hash.Equal(reference.Hash) || !existing.ParentHash.Equal(reference.ParentHash)) {
			return CoreCoverage{}, fmt.Errorf("%w: height %d already maps to another canonical identity", ErrConflict, reference.Number)
		}
		newCanonical[index] = !exists
	}
	if first.Number > 0 {
		lower, exists, err := queryCanonicalBlock(ctx, tx, chainID, first.Number-1, " FOR UPDATE")
		if err != nil {
			return CoreCoverage{}, err
		}
		if exists && !first.ParentHash.Equal(lower.Hash) {
			return CoreCoverage{}, fmt.Errorf("%w: segment parent does not match lower canonical boundary", ErrConflict)
		}
	}
	if last.Number < math.MaxUint64 {
		upper, exists, err := queryCanonicalBlock(ctx, tx, chainID, last.Number+1, " FOR UPDATE")
		if err != nil {
			return CoreCoverage{}, err
		}
		if exists && !upper.ParentHash.Equal(last.Hash) {
			return CoreCoverage{}, fmt.Errorf("%w: upper canonical boundary does not descend from segment", ErrConflict)
		}
	}
	ranges, err := queryCoverageRangesTx(ctx, tx, chainID)
	if err != nil {
		return CoreCoverage{}, err
	}
	nextRanges, err := normalizeCoverageRanges(append(ranges, BlockRange{Start: first.Number, End: last.Number}))
	if err != nil {
		return CoreCoverage{}, err
	}
	ensuredPartitions, err := r.ensureBundlePartitionsTx(ctx, tx, references)
	if err != nil {
		return CoreCoverage{}, err
	}
	for index, bundle := range copies {
		reference := references[index]
		if err := putBundleTx(ctx, tx, chainID, bundle); err != nil {
			return CoreCoverage{}, err
		}
		if newCanonical[index] {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO canonical_blocks (chain_id, number, block_hash)
				VALUES ($1::numeric, $2::numeric, $3)`,
				chainID, decimal(reference.Number), mustHashBytes(reference.Hash)); err != nil {
				return CoreCoverage{}, fmt.Errorf("insert canonical segment block %d: %w", reference.Number, err)
			}
			if err := insertCoreOutboxTx(ctx, tx, chainID, "core.block.canonical", reference); err != nil {
				return CoreCoverage{}, err
			}
		}
	}
	if err := replaceCoverageRangesTx(ctx, tx, chainID, nextRanges); err != nil {
		return CoreCoverage{}, err
	}
	coverage, exists, err := queryCoverageTx(ctx, tx, chainID, false)
	if err != nil {
		return CoreCoverage{}, err
	}
	if !exists {
		return CoreCoverage{}, ErrIndexNotConfigured
	}
	if coverage.Contiguous != nil {
		checkpoint := NewCoreCheckpoint(*coverage.Contiguous)
		if err := checkCheckpointTx(ctx, tx, chainID, checkpoint, false); err != nil {
			return CoreCoverage{}, err
		}
		if err := upsertCheckpointTx(ctx, tx, chainID, checkpoint); err != nil {
			return CoreCoverage{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return CoreCoverage{}, fmt.Errorf("commit canonical segment: %w", err)
	}
	r.partitions.add(ensuredPartitions...)
	return coverage, nil
}

func (r *PostgresRepository) ReplaceHighestCanonicalSegment(
	ctx context.Context,
	chainID string,
	replacement SparseCanonicalReplacement,
) (CoreCoverage, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return CoreCoverage{}, err
	}
	attached, copies, err := validateSparseCanonicalReplacement(replacement)
	if err != nil {
		return CoreCoverage{}, err
	}
	// A block-bound enrichment write may hold KEY SHARE on a canonical row
	// while this fork-choice waits to detach it. Core fork-choice is already
	// serialized by the chain advisory lock and canonical row locks. Keep
	// statement snapshots fresh so setDerivedCanonicalTx sees any derived fact
	// that committed while the detach was waiting and marks it orphaned in this
	// same transaction.
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return CoreCoverage{}, fmt.Errorf("begin sparse canonical replacement: %w", err)
	}
	defer tx.Rollback()
	if err := lockChain(ctx, tx, chainID); err != nil {
		return CoreCoverage{}, err
	}
	configuredStart, configured, err := queryConfiguredStartTx(ctx, tx, chainID, true)
	if err != nil {
		return CoreCoverage{}, err
	}
	if !configured {
		return CoreCoverage{}, ErrIndexNotConfigured
	}
	ranges, err := queryCoverageRangesTx(ctx, tx, chainID)
	if err != nil {
		return CoreCoverage{}, err
	}
	if err := validateHighestDisconnectedRange(ranges, configuredStart, replacement.Range); err != nil {
		return CoreCoverage{}, err
	}
	tip, exists, err := queryCanonicalTip(ctx, tx, chainID, " FOR UPDATE")
	if err != nil {
		return CoreCoverage{}, err
	}
	if !exists || tip.Number != replacement.Range.End || !tip.Hash.Equal(replacement.Detached[0].Hash) {
		return CoreCoverage{}, fmt.Errorf("%w: replacement range is not the canonical tip", ErrConflict)
	}
	for _, detached := range replacement.Detached {
		canonical, exists, err := queryCanonicalBlock(ctx, tx, chainID, detached.Number, " FOR UPDATE")
		if err != nil {
			return CoreCoverage{}, err
		}
		if !exists || !sameBlockIdentity(canonical, detached) {
			return CoreCoverage{}, fmt.Errorf("%w: detached block %d is not canonical", ErrConflict, detached.Number)
		}
	}
	if replacement.Ancestor != nil {
		var canonicalAbove int64
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM canonical_blocks
			WHERE chain_id = $1::numeric AND number > $2::numeric`,
			chainID, decimal(replacement.Ancestor.Number)).Scan(&canonicalAbove); err != nil {
			return CoreCoverage{}, fmt.Errorf("count canonical blocks above sparse ancestor: %w", err)
		}
		if canonicalAbove != int64(len(replacement.Detached)) {
			return CoreCoverage{}, fmt.Errorf("%w: generalized replacement does not detach every canonical block above its ancestor", ErrConflict)
		}
	}
	finality, hasFinality, err := queryFinality(ctx, tx, chainID, true)
	if err != nil {
		return CoreCoverage{}, err
	}
	if hasFinality && finality.Finalized != nil {
		if replacement.Ancestor != nil && replacement.Ancestor.Number < finality.Finalized.Number {
			return CoreCoverage{}, fmt.Errorf("%w: replacement ancestor is below finalized height", ErrConflict)
		}
		if replacement.Ancestor == nil && replacement.Range.Start <= finality.Finalized.Number {
			return CoreCoverage{}, fmt.Errorf("%w: replacement starts at or below finalized height", ErrConflict)
		}
	}
	for _, reference := range attached {
		if reference.Number <= replacement.Range.End || replacement.Ancestor != nil {
			continue
		}
		if _, exists, err := queryCanonicalBlock(ctx, tx, chainID, reference.Number, " FOR UPDATE"); err != nil {
			return CoreCoverage{}, err
		} else if exists {
			return CoreCoverage{}, fmt.Errorf("%w: attached height %d is already canonical", ErrConflict, reference.Number)
		}
	}
	ensuredPartitions, err := r.ensureBundlePartitionsTx(ctx, tx, attached)
	if err != nil {
		return CoreCoverage{}, err
	}
	for _, bundle := range copies {
		if err := putBundleTx(ctx, tx, chainID, bundle); err != nil {
			return CoreCoverage{}, err
		}
	}
	for _, detached := range replacement.Detached {
		result, err := tx.ExecContext(ctx, `
			DELETE FROM canonical_blocks
			WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3`,
			chainID, decimal(detached.Number), mustHashBytes(detached.Hash))
		if err != nil {
			return CoreCoverage{}, fmt.Errorf("detach sparse canonical block %d: %w", detached.Number, err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return CoreCoverage{}, fmt.Errorf("%w: detach sparse canonical block %d affected %d rows", ErrConflict, detached.Number, affected)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE block_journals SET canonical = FALSE
			WHERE chain_id = $1::numeric AND block_hash = $2`,
			chainID, mustHashBytes(detached.Hash)); err != nil {
			return CoreCoverage{}, fmt.Errorf("mark sparse detached journals: %w", err)
		}
		if err := setDerivedCanonicalTx(ctx, tx, chainID, detached.Hash, false); err != nil {
			return CoreCoverage{}, err
		}
		if err := insertCoreOutboxTx(ctx, tx, chainID, "core.block.orphaned", detached); err != nil {
			return CoreCoverage{}, err
		}
	}
	for _, reference := range attached {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO canonical_blocks (chain_id, number, block_hash)
			VALUES ($1::numeric, $2::numeric, $3)`,
			chainID, decimal(reference.Number), mustHashBytes(reference.Hash)); err != nil {
			return CoreCoverage{}, fmt.Errorf("attach sparse canonical block %d: %w", reference.Number, err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE block_journals SET canonical = TRUE
			WHERE chain_id = $1::numeric AND block_hash = $2`,
			chainID, mustHashBytes(reference.Hash)); err != nil {
			return CoreCoverage{}, fmt.Errorf("mark sparse attached journals: %w", err)
		}
		if err := setDerivedCanonicalTx(ctx, tx, chainID, reference.Hash, true); err != nil {
			return CoreCoverage{}, err
		}
		if err := insertCoreOutboxTx(ctx, tx, chainID, "core.block.canonical", reference); err != nil {
			return CoreCoverage{}, err
		}
	}
	nextRanges, err := coverageRangesAfterSparseReplacement(ranges, replacement, attached)
	if err != nil {
		return CoreCoverage{}, err
	}
	if err := replaceCoverageRangesTx(ctx, tx, chainID, nextRanges); err != nil {
		return CoreCoverage{}, err
	}
	coverage, exists, err := queryCoverageTx(ctx, tx, chainID, false)
	if err != nil {
		return CoreCoverage{}, err
	}
	if !exists {
		return CoreCoverage{}, ErrIndexNotConfigured
	}
	if coverage.Contiguous != nil {
		checkpoint := NewCoreCheckpoint(*coverage.Contiguous)
		if err := checkCheckpointTx(ctx, tx, chainID, checkpoint, replacement.Ancestor != nil); err != nil {
			return CoreCoverage{}, err
		}
		if err := upsertCheckpointTx(ctx, tx, chainID, checkpoint); err != nil {
			return CoreCoverage{}, err
		}
	}
	if err := insertSparseReorgEventsTx(ctx, tx, chainID, tip, replacement, attached); err != nil {
		return CoreCoverage{}, err
	}
	if err := tx.Commit(); err != nil {
		return CoreCoverage{}, fmt.Errorf("commit sparse canonical replacement: %w", err)
	}
	r.partitions.add(ensuredPartitions...)
	return coverage, nil
}

func (r *PostgresRepository) ClaimBackfillRange(
	ctx context.Context,
	chainID string,
	target BlockRange,
	owner string,
	now time.Time,
	ttl time.Duration,
) (BackfillLease, bool, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return BackfillLease{}, false, err
	}
	if err := validateBackfillClaim(target, owner, now, ttl); err != nil {
		return BackfillLease{}, false, err
	}
	now = now.UTC()
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return BackfillLease{}, false, fmt.Errorf("begin backfill range claim: %w", err)
	}
	defer tx.Rollback()
	if err := lockChain(ctx, tx, chainID); err != nil {
		return BackfillLease{}, false, err
	}
	configuredStart, configured, err := queryConfiguredStartTx(ctx, tx, chainID, true)
	if err != nil {
		return BackfillLease{}, false, err
	}
	if !configured {
		return BackfillLease{}, false, ErrIndexNotConfigured
	}
	if target.Start < configuredStart {
		return BackfillLease{}, false, fmt.Errorf("%w: backfill range starts before configured height %d", ErrConflict, configuredStart)
	}
	ranges, err := queryCoverageRangesTx(ctx, tx, chainID)
	if err != nil {
		return BackfillLease{}, false, err
	}
	if rangeIntersectsCoverage(ranges, target) {
		return BackfillLease{}, false, nil
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM core_backfill_leases
		WHERE chain_id = $1::numeric AND expires_at <= $2`, chainID, now); err != nil {
		return BackfillLease{}, false, fmt.Errorf("delete expired backfill leases: %w", err)
	}
	var overlaps bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM core_backfill_leases
			WHERE chain_id = $1::numeric
			  AND expires_at > $2
			  AND NOT (range_end < $3::numeric OR range_start > $4::numeric)
		)`, chainID, now, decimal(target.Start), decimal(target.End)).Scan(&overlaps); err != nil {
		return BackfillLease{}, false, fmt.Errorf("check overlapping backfill lease: %w", err)
	}
	if overlaps {
		return BackfillLease{}, false, nil
	}
	lease := newBackfillLease(chainID, target, owner, now, ttl)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO core_backfill_leases (
			chain_id, range_start, range_end, owner, lease_token, claimed_at, expires_at
		) VALUES ($1::numeric, $2::numeric, $3::numeric, $4, $5::uuid, $6, $7)`,
		chainID, decimal(target.Start), decimal(target.End), lease.Owner, lease.Token, now, lease.ExpiresAt); err != nil {
		return BackfillLease{}, false, fmt.Errorf("insert backfill lease: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return BackfillLease{}, false, fmt.Errorf("commit backfill range claim: %w", err)
	}
	return lease, true, nil
}

func (r *PostgresRepository) RenewBackfillRange(
	ctx context.Context,
	lease BackfillLease,
	now time.Time,
	ttl time.Duration,
) (BackfillLease, error) {
	if err := validateBackfillLease(lease); err != nil {
		return BackfillLease{}, err
	}
	if err := validateBackfillClaim(lease.Range, lease.Owner, now, ttl); err != nil {
		return BackfillLease{}, err
	}
	chainID, _ := normalizeChainID(lease.ChainID)
	now = now.UTC()
	expiresAt := now.Add(ttl)
	var storedExpiry time.Time
	err := r.db.QueryRowContext(ctx, `
		UPDATE core_backfill_leases
		SET expires_at = $1, updated_at = now()
		WHERE chain_id = $2::numeric
		  AND range_start = $3::numeric AND range_end = $4::numeric
		  AND owner = $5 AND lease_token = $6::uuid AND expires_at > $7
		RETURNING expires_at`, expiresAt, chainID, decimal(lease.Range.Start), decimal(lease.Range.End),
		lease.Owner, lease.Token, now).Scan(&storedExpiry)
	if err == sql.ErrNoRows {
		return BackfillLease{}, ErrLeaseLost
	}
	if err != nil {
		return BackfillLease{}, fmt.Errorf("renew backfill lease: %w", err)
	}
	lease.ChainID, lease.ExpiresAt = chainID, storedExpiry.UTC()
	return lease, nil
}

func (r *PostgresRepository) ReleaseBackfillRange(ctx context.Context, lease BackfillLease) error {
	if err := validateBackfillLease(lease); err != nil {
		return err
	}
	chainID, _ := normalizeChainID(lease.ChainID)
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM core_backfill_leases
		WHERE chain_id = $1::numeric
		  AND range_start = $2::numeric AND range_end = $3::numeric
		  AND owner = $4 AND lease_token = $5::uuid AND expires_at > CURRENT_TIMESTAMP`,
		chainID, decimal(lease.Range.Start), decimal(lease.Range.End), lease.Owner, lease.Token)
	if err != nil {
		return fmt.Errorf("release backfill lease: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read released backfill lease count: %w", err)
	}
	if affected != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (r *PostgresRepository) CompleteBackfillRange(ctx context.Context, lease BackfillLease) error {
	if err := validateBackfillLease(lease); err != nil {
		return err
	}
	chainID, _ := normalizeChainID(lease.ChainID)
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin backfill range completion: %w", err)
	}
	defer tx.Rollback()
	if err := lockChain(ctx, tx, chainID); err != nil {
		return err
	}
	var expiresAt time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT expires_at
		FROM core_backfill_leases
		WHERE chain_id = $1::numeric
		  AND range_start = $2::numeric AND range_end = $3::numeric
		  AND owner = $4 AND lease_token = $5::uuid
		  AND expires_at > CURRENT_TIMESTAMP
		FOR UPDATE`, chainID, decimal(lease.Range.Start), decimal(lease.Range.End),
		lease.Owner, lease.Token).Scan(&expiresAt)
	if err == sql.ErrNoRows {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("lock backfill lease for completion: %w", err)
	}
	ranges, err := queryCoverageRangesTx(ctx, tx, chainID)
	if err != nil {
		return err
	}
	if !rangeCovered(ranges, lease.Range) {
		return fmt.Errorf("%w: backfill range is not fully covered", ErrConflict)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM core_backfill_leases
		WHERE chain_id = $1::numeric
		  AND range_start = $2::numeric AND range_end = $3::numeric
		  AND lease_token = $4::uuid`, chainID, decimal(lease.Range.Start),
		decimal(lease.Range.End), lease.Token); err != nil {
		return fmt.Errorf("complete backfill lease: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit backfill range completion: %w", err)
	}
	return nil
}

func queryConfiguredStartTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	forUpdate bool,
) (uint64, bool, error) {
	suffix := ""
	if forUpdate {
		suffix = " FOR UPDATE"
	}
	var value string
	err := tx.QueryRowContext(ctx, `
		SELECT configured_start::text
		FROM core_index_configuration
		WHERE chain_id = $1::numeric`+suffix, chainID).Scan(&value)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("query index configuration: %w", err)
	}
	configuredStart, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("decode configured index start: %w", err)
	}
	return configuredStart, true, nil
}

func validateHighestDisconnectedRange(ranges []BlockRange, configuredStart uint64, target BlockRange) error {
	if len(ranges) == 0 || ranges[len(ranges)-1] != target {
		return fmt.Errorf("%w: replacement target is not the highest coverage range", ErrConflict)
	}
	if target.Start == configuredStart {
		return fmt.Errorf("%w: highest coverage range is contiguous from configured start", ErrConflict)
	}
	if len(ranges) > 1 {
		previous := ranges[len(ranges)-2]
		if previous.End == math.MaxUint64 || target.Start <= previous.End+1 {
			return fmt.Errorf("%w: highest coverage range is not disconnected", ErrConflict)
		}
	}
	return nil
}

func insertSparseReorgEventsTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	oldTip BlockRef,
	replacement SparseCanonicalReplacement,
	attached []BlockRef,
) error {
	detachedJSON, err := json.Marshal(replacement.Detached)
	if err != nil {
		return fmt.Errorf("encode sparse detached branch: %w", err)
	}
	attachedJSON, err := json.Marshal(attached)
	if err != nil {
		return fmt.Errorf("encode sparse attached branch: %w", err)
	}
	newTip := oldTip
	if replacement.Ancestor != nil {
		newTip = *replacement.Ancestor
	}
	if len(attached) > 0 {
		newTip = attached[len(attached)-1]
	}
	boundaryNumber := replacement.Range.Start
	boundaryHash := replacement.Detached[len(replacement.Detached)-1].ParentHash
	if replacement.Ancestor != nil {
		boundaryNumber = replacement.Ancestor.Number
		boundaryHash = replacement.Ancestor.Hash
	} else if boundaryNumber > 0 {
		boundaryNumber--
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reorg_events (
			chain_id, ancestor_number, ancestor_hash, old_tip_number, old_tip_hash,
			new_tip_number, new_tip_hash, detached, attached, reason
		) VALUES ($1::numeric, $2::numeric, $3, $4::numeric, $5, $6::numeric, $7, $8::jsonb, $9::jsonb, $10)`,
		chainID, decimal(boundaryNumber), mustHashBytes(boundaryHash),
		decimal(oldTip.Number), mustHashBytes(oldTip.Hash), decimal(newTip.Number),
		mustHashBytes(newTip.Hash), detachedJSON, attachedJSON, replacement.Reason); err != nil {
		return fmt.Errorf("insert sparse reorg audit event: %w", err)
	}
	payload := map[string]string{
		"ancestor_number": decimal(boundaryNumber),
		"ancestor_hash":   boundaryHash.String(),
		"old_tip_number":  decimal(oldTip.Number),
		"old_tip_hash":    oldTip.Hash.String(),
		"new_tip_number":  decimal(newTip.Number),
		"new_tip_hash":    newTip.Hash.String(),
		"detached_count":  decimal(uint64(len(replacement.Detached))),
		"attached_count":  decimal(uint64(len(attached))),
	}
	return insertRuntimeEventTx(ctx, tx, chainID, "reorg", payload)
}

func queryCoverageTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	forUpdate bool,
) (CoreCoverage, bool, error) {
	configuredStart, exists, err := queryConfiguredStartTx(ctx, tx, chainID, forUpdate)
	if err != nil || !exists {
		return CoreCoverage{}, exists, err
	}
	ranges, err := queryCoverageRangesTx(ctx, tx, chainID)
	if err != nil {
		return CoreCoverage{}, false, err
	}
	coverage := CoreCoverage{ConfiguredStart: configuredStart, Ranges: ranges}
	if len(ranges) == 0 {
		return coverage, true, nil
	}
	for _, blockRange := range ranges {
		if blockRange.Start < configuredStart {
			return CoreCoverage{}, false, errors.New("coverage range starts before configured index height")
		}
	}
	highest, exists, err := queryCanonicalBlock(ctx, tx, chainID, ranges[len(ranges)-1].End, "")
	if err != nil {
		return CoreCoverage{}, false, err
	}
	if !exists {
		return CoreCoverage{}, false, errors.New("coverage highest canonical block is missing")
	}
	coverage.Highest = &highest
	if ranges[0].Start == configuredStart {
		contiguous, exists, err := queryCanonicalBlock(ctx, tx, chainID, ranges[0].End, "")
		if err != nil {
			return CoreCoverage{}, false, err
		}
		if !exists {
			return CoreCoverage{}, false, errors.New("coverage checkpoint canonical block is missing")
		}
		coverage.Contiguous = &contiguous
	}
	return coverage, true, nil
}

func queryCoverageRangesTx(ctx context.Context, tx *sql.Tx, chainID string) ([]BlockRange, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT range_start::text, range_end::text
		FROM core_coverage_ranges
		WHERE chain_id = $1::numeric
		ORDER BY range_start`, chainID)
	if err != nil {
		return nil, fmt.Errorf("query core coverage ranges: %w", err)
	}
	defer rows.Close()
	ranges := make([]BlockRange, 0)
	for rows.Next() {
		var start, end string
		if err := rows.Scan(&start, &end); err != nil {
			return nil, fmt.Errorf("scan core coverage range: %w", err)
		}
		parsedStart, err := strconv.ParseUint(start, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("decode core coverage range start: %w", err)
		}
		parsedEnd, err := strconv.ParseUint(end, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("decode core coverage range end: %w", err)
		}
		ranges = append(ranges, BlockRange{Start: parsedStart, End: parsedEnd})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate core coverage ranges: %w", err)
	}
	if err := validateNormalizedCoverageRanges(ranges); err != nil {
		return nil, fmt.Errorf("validate core coverage ranges: %w", err)
	}
	return ranges, nil
}

func replaceCoverageRangesTx(ctx context.Context, tx *sql.Tx, chainID string, ranges []BlockRange) error {
	if err := validateNormalizedCoverageRanges(ranges); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM core_coverage_ranges
		WHERE chain_id = $1::numeric`, chainID); err != nil {
		return fmt.Errorf("replace core coverage ranges: %w", err)
	}
	for _, blockRange := range ranges {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO core_coverage_ranges (chain_id, range_start, range_end)
			VALUES ($1::numeric, $2::numeric, $3::numeric)`,
			chainID, decimal(blockRange.Start), decimal(blockRange.End)); err != nil {
			return fmt.Errorf("insert core coverage range %d-%d: %w", blockRange.Start, blockRange.End, err)
		}
	}
	return nil
}

func queryCanonicalReferencesTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	configuredStart uint64,
) ([]BlockRef, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT cb.number::text, cb.block_hash, b.parent_hash
		FROM canonical_blocks cb
		JOIN blocks b
		  ON b.chain_id = cb.chain_id AND b.number = cb.number AND b.hash = cb.block_hash
		WHERE cb.chain_id = $1::numeric AND cb.number >= $2::numeric
		ORDER BY cb.number`, chainID, decimal(configuredStart))
	if err != nil {
		return nil, fmt.Errorf("query canonical blocks for coverage: %w", err)
	}
	defer rows.Close()
	references := make([]BlockRef, 0)
	for rows.Next() {
		var number string
		var hashBytes, parentBytes []byte
		if err := rows.Scan(&number, &hashBytes, &parentBytes); err != nil {
			return nil, fmt.Errorf("scan canonical block for coverage: %w", err)
		}
		parsedNumber, err := strconv.ParseUint(number, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("decode coverage block number: %w", err)
		}
		hash, err := hashFromBytes(hashBytes)
		if err != nil {
			return nil, err
		}
		parent, err := hashFromBytes(parentBytes)
		if err != nil {
			return nil, err
		}
		references = append(references, BlockRef{Number: parsedNumber, Hash: hash, ParentHash: parent})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate canonical blocks for coverage: %w", err)
	}
	return references, nil
}
