package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"

	"github.com/islishude/etherview/internal/chainbundle"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func (r *PostgresRepository) ConfigureIndex(ctx context.Context, chainID string, configuredStart uint64) error {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: chainWriteIsolation})
	if err != nil {
		return fmt.Errorf("begin index configuration: %w", err)
	}
	defer func() { dbaccess.Rollback(ctx, tx) }()
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(decimal(configuredStart)); err != nil {
			return err
		}
		return dbgen.New(tx).StoreLegacyConfigureIndexStatement1(ctx, queryValue0, queryValue1)
	}(); err != nil {
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
	coverage, _, err := queryCoverageTx(ctx, tx, chainID)
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
	} else if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		return dbgen.New(tx).StoreLegacyConfigureIndexStatement2(ctx, queryValue0, CoreCheckpoint)
	}(); err != nil {
		return fmt.Errorf("clear pre-coverage core checkpoint: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit index configuration: %w", err)
	}
	return nil
}

func (r *PostgresRepository) Coverage(ctx context.Context, chainID string) (CoreCoverage, bool, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return CoreCoverage{}, false, err
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return CoreCoverage{}, false, fmt.Errorf("begin coverage read: %w", err)
	}
	defer func() { dbaccess.Rollback(ctx, tx) }()
	coverage, exists, err := queryCoverageTx(ctx, tx, chainID)
	if err != nil {
		return CoreCoverage{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CoreCoverage{}, false, fmt.Errorf("commit coverage read: %w", err)
	}
	return coverage, exists, nil
}

func (r *PostgresRepository) CommitCanonicalSegment(
	ctx context.Context,
	chainID string,
	bundles []chainbundle.Bundle,
) (CoreCoverage, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return CoreCoverage{}, err
	}
	references, copies, err := validateCanonicalSegment(bundles)
	if err != nil {
		return CoreCoverage{}, err
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: chainWriteIsolation})
	if err != nil {
		return CoreCoverage{}, fmt.Errorf("begin canonical segment commit: %w", err)
	}
	defer func() { dbaccess.Rollback(ctx, tx) }()
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
		existing, exists, err := queryCanonicalBlock(ctx, tx, chainID, reference.Number, true)
		if err != nil {
			return CoreCoverage{}, err
		}
		if exists && (existing.Hash != reference.Hash || existing.ParentHash != reference.ParentHash) {
			return CoreCoverage{}, fmt.Errorf("%w: height %d already maps to another canonical identity", ErrConflict, reference.Number)
		}
		newCanonical[index] = !exists
	}
	if first.Number > 0 {
		lower, exists, err := queryCanonicalBlock(ctx, tx, chainID, first.Number-1, true)
		if err != nil {
			return CoreCoverage{}, err
		}
		if exists && first.ParentHash != lower.Hash {
			return CoreCoverage{}, fmt.Errorf("%w: segment parent does not match lower canonical boundary", ErrConflict)
		}
	}
	if last.Number < math.MaxUint64 {
		upper, exists, err := queryCanonicalBlock(ctx, tx, chainID, last.Number+1, true)
		if err != nil {
			return CoreCoverage{}, err
		}
		if exists && upper.ParentHash != last.Hash {
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
	if err := putBundlesTx(ctx, tx, chainID, copies); err != nil {
		return CoreCoverage{}, err
	}
	newReferences := make([]BlockRef, 0, len(references))
	outboxMessages := make([]coreOutboxMessage, 0, len(references))
	for index, reference := range references {
		if !newCanonical[index] {
			continue
		}
		newReferences = append(newReferences, reference)
		outboxMessages = append(outboxMessages, coreOutboxMessage{
			Topic: "core.block.canonical", Reference: reference,
		})
	}
	if err := insertCanonicalBlocksTx(ctx, tx, chainID, newReferences); err != nil {
		return CoreCoverage{}, err
	}
	if err := insertCoreOutboxBatchTx(ctx, tx, chainID, outboxMessages); err != nil {
		return CoreCoverage{}, err
	}
	if err := replaceCoverageRangesTx(ctx, tx, chainID, nextRanges); err != nil {
		return CoreCoverage{}, err
	}
	coverage, exists, err := queryCoverageTx(ctx, tx, chainID)
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
	if err := tx.Commit(ctx); err != nil {
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
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: chainWriteIsolation})
	if err != nil {
		return CoreCoverage{}, fmt.Errorf("begin sparse canonical replacement: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
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
	tip, exists, err := queryCanonicalTip(ctx, tx, chainID, true)
	if err != nil {
		return CoreCoverage{}, err
	}
	if !exists || tip.Number != replacement.Range.End || tip.Hash != replacement.Detached[0].Hash {
		return CoreCoverage{}, fmt.Errorf("%w: replacement range is not the canonical tip", ErrConflict)
	}
	for _, detached := range replacement.Detached {
		canonical, exists, err := queryCanonicalBlock(ctx, tx, chainID, detached.Number, true)
		if err != nil {
			return CoreCoverage{}, err
		}
		if !exists || !sameBlockIdentity(canonical, detached) {
			return CoreCoverage{}, fmt.Errorf("%w: detached block %d is not canonical", ErrConflict, detached.Number)
		}
	}
	if replacement.Ancestor != nil {
		var canonicalAbove int64
		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(chainID); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(decimal(replacement.Ancestor.Number)); err != nil {
				return err
			}
			queryRow, err := dbgen.New(tx).StoreLegacyReplaceHighestCanonicalSegmentStatement1(ctx, queryValue0, queryValue1)
			if err != nil {
				return err
			}
			canonicalAbove = queryRow
			return nil
		}(); err != nil {
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
		if _, exists, err := queryCanonicalBlock(ctx, tx, chainID, reference.Number, true); err != nil {
			return CoreCoverage{}, err
		} else if exists {
			return CoreCoverage{}, fmt.Errorf("%w: attached height %d is already canonical", ErrConflict, reference.Number)
		}
	}
	ensuredPartitions, err := r.ensureBundlePartitionsTx(ctx, tx, attached)
	if err != nil {
		return CoreCoverage{}, err
	}
	if err := putBundlesTx(ctx, tx, chainID, copies); err != nil {
		return CoreCoverage{}, err
	}
	if err := deleteCanonicalBlocksTx(ctx, tx, chainID, replacement.Detached); err != nil {
		return CoreCoverage{}, err
	}
	if err := setBlockJournalsCanonicalBatchTx(
		ctx, tx, chainID, replacement.Detached, false,
	); err != nil {
		return CoreCoverage{}, err
	}
	if err := setDerivedCanonicalBatchTx(
		ctx, tx, chainID, replacement.Detached, false,
	); err != nil {
		return CoreCoverage{}, err
	}
	if err := insertCanonicalBlocksTx(ctx, tx, chainID, attached); err != nil {
		return CoreCoverage{}, err
	}
	if err := setBlockJournalsCanonicalBatchTx(ctx, tx, chainID, attached, true); err != nil {
		return CoreCoverage{}, err
	}
	if err := setDerivedCanonicalBatchTx(ctx, tx, chainID, attached, true); err != nil {
		return CoreCoverage{}, err
	}
	outboxMessages := make([]coreOutboxMessage, 0, len(replacement.Detached)+len(attached))
	for _, detached := range replacement.Detached {
		outboxMessages = append(outboxMessages, coreOutboxMessage{
			Topic: "core.block.orphaned", Reference: detached,
		})
	}
	for _, reference := range attached {
		outboxMessages = append(outboxMessages, coreOutboxMessage{
			Topic: "core.block.canonical", Reference: reference,
		})
	}
	if err := insertCoreOutboxBatchTx(ctx, tx, chainID, outboxMessages); err != nil {
		return CoreCoverage{}, err
	}
	nextRanges, err := coverageRangesAfterSparseReplacement(ranges, replacement, attached)
	if err != nil {
		return CoreCoverage{}, err
	}
	if err := replaceCoverageRangesTx(ctx, tx, chainID, nextRanges); err != nil {
		return CoreCoverage{}, err
	}
	coverage, exists, err := queryCoverageTx(ctx, tx, chainID)
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
	if err := tx.Commit(ctx); err != nil {
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
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: chainWriteIsolation})
	if err != nil {
		return BackfillLease{}, false, fmt.Errorf("begin backfill range claim: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		return dbgen.New(tx).StoreLegacyClaimBackfillRangeStatement1(ctx, queryValue0, pgtype.Timestamptz{Time: now, Valid: true})
	}(); err != nil {
		return BackfillLease{}, false, fmt.Errorf("delete expired backfill leases: %w", err)
	}
	var overlaps bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(decimal(target.Start)); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(decimal(target.End)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).StoreLegacyClaimBackfillRangeStatement2(ctx, dbgen.StoreLegacyClaimBackfillRangeStatement2Params{ChainID: queryValue0, ExpiresAt: pgtype.Timestamptz{Time: now, Valid: true}, MaxRangeEnd: queryValue1, MinRangeStart: queryValue2})
		if err != nil {
			return err
		}
		overlaps = queryRow
		return nil
	}(); err != nil {
		return BackfillLease{}, false, fmt.Errorf("check overlapping backfill lease: %w", err)
	}
	if overlaps {
		return BackfillLease{}, false, nil
	}
	lease := newBackfillLease(chainID, target, owner, now, ttl)
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(decimal(target.Start)); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(decimal(target.End)); err != nil {
			return err
		}
		var queryValue3 pgtype.UUID
		if err := queryValue3.Scan(lease.Token); err != nil {
			return err
		}
		return dbgen.New(tx).StoreLegacyClaimBackfillRangeStatement3(ctx, dbgen.StoreLegacyClaimBackfillRangeStatement3Params{ChainID: queryValue0, RangeStart: queryValue1, RangeEnd: queryValue2, Owner: lease.Owner, LeaseToken: queryValue3, ClaimedAt: pgtype.Timestamptz{Time: now, Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: lease.ExpiresAt, Valid: true}})
	}(); err != nil {
		return BackfillLease{}, false, fmt.Errorf("insert backfill lease: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
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
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(decimal(lease.Range.Start)); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(decimal(lease.Range.End)); err != nil {
			return err
		}
		var queryValue3 pgtype.UUID
		if err := queryValue3.Scan(lease.Token); err != nil {
			return err
		}
		queryRow, err := dbgen.New(r.db).StoreLegacyRenewBackfillRangeStatement1(ctx, dbgen.StoreLegacyRenewBackfillRangeStatement1Params{ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true}, ChainID: queryValue0, RangeStart: queryValue1, RangeEnd: queryValue2, Owner: lease.Owner, LeaseToken: queryValue3, ExpiresAt2: pgtype.Timestamptz{Time: now, Valid: true}})
		if err != nil {
			return err
		}
		if !queryRow.Valid {
			return errors.New("invalid stored query value")
		}
		if queryRow.InfinityModifier != pgtype.Finite {
			return errors.New("invalid stored query value")
		}
		storedExpiry = queryRow.Time
		return nil
	}()
	if err == pgx.ErrNoRows {
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
	result, err := func() (int64, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return 0, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(decimal(lease.Range.Start)); err != nil {
			return 0, err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(decimal(lease.Range.End)); err != nil {
			return 0, err
		}
		var queryValue3 pgtype.UUID
		if err := queryValue3.Scan(lease.Token); err != nil {
			return 0, err
		}
		return dbgen.New(r.db).StoreLegacyReleaseBackfillRangeStatement1(ctx, dbgen.StoreLegacyReleaseBackfillRangeStatement1Params{ChainID: queryValue0, RangeStart: queryValue1, RangeEnd: queryValue2, Owner: lease.Owner, LeaseToken: queryValue3})
	}()
	if err != nil {
		return fmt.Errorf("release backfill lease: %w", err)
	}
	affected := result
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
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: chainWriteIsolation})
	if err != nil {
		return fmt.Errorf("begin backfill range completion: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
	if err := lockChain(ctx, tx, chainID); err != nil {
		return err
	}
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(decimal(lease.Range.Start)); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(decimal(lease.Range.End)); err != nil {
			return err
		}
		var queryValue3 pgtype.UUID
		if err := queryValue3.Scan(lease.Token); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).StoreLegacyCompleteBackfillRangeStatement1(ctx, dbgen.StoreLegacyCompleteBackfillRangeStatement1Params{ChainID: queryValue0, RangeStart: queryValue1, RangeEnd: queryValue2, Owner: lease.Owner, LeaseToken: queryValue3})
		if err != nil {
			return err
		}
		if !queryRow.Valid {
			return errors.New("invalid stored query value")
		}
		if queryRow.InfinityModifier != pgtype.Finite {
			return errors.New("invalid stored query value")
		}
		return nil
	}()
	if err == pgx.ErrNoRows {
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(decimal(lease.Range.Start)); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(decimal(lease.Range.End)); err != nil {
			return err
		}
		var queryValue3 pgtype.UUID
		if err := queryValue3.Scan(lease.Token); err != nil {
			return err
		}
		return dbgen.New(tx).StoreLegacyCompleteBackfillRangeStatement2(ctx, dbgen.StoreLegacyCompleteBackfillRangeStatement2Params{ChainID: queryValue0, RangeStart: queryValue1, RangeEnd: queryValue2, LeaseToken: queryValue3})
	}(); err != nil {
		return fmt.Errorf("complete backfill lease: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit backfill range completion: %w", err)
	}
	return nil
}

func queryConfiguredStartTx(
	ctx context.Context,
	tx pgx.Tx,
	chainID string,
	forUpdate bool,
) (uint64, bool, error) {
	queries := dbgen.New(tx)
	var value string
	var err error
	if forUpdate {
		value, err = queries.StoreLockConfiguredStart(ctx, chainID)
	} else {
		value, err = queries.StoreConfiguredStart(ctx, chainID)
	}

	if err == pgx.ErrNoRows {
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
	tx pgx.Tx,
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(decimal(boundaryNumber)); err != nil {
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
		return dbgen.New(tx).StoreLegacyInsertSparseReorgEventsTxStatement1(ctx, dbgen.StoreLegacyInsertSparseReorgEventsTxStatement1Params{ChainID: queryValue0, AncestorNumber: queryValue1, AncestorHash: mustHashBytes(boundaryHash), OldTipNumber: queryValue2, OldTipHash: mustHashBytes(oldTip.Hash), NewTipNumber: queryValue3, NewTipHash: mustHashBytes(newTip.Hash), Detached: detachedJSON, Attached: attachedJSON, Reason: replacement.Reason})
	}(); err != nil {
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
	tx pgx.Tx,
	chainID string,
) (CoreCoverage, bool, error) {
	configuredStart, exists, err := queryConfiguredStartTx(ctx, tx, chainID, false)
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
	highest, exists, err := queryCanonicalBlock(ctx, tx, chainID, ranges[len(ranges)-1].End, false)
	if err != nil {
		return CoreCoverage{}, false, err
	}
	if !exists {
		return CoreCoverage{}, false, errors.New("coverage highest canonical block is missing")
	}
	coverage.Highest = &highest
	if ranges[0].Start == configuredStart {
		contiguous, exists, err := queryCanonicalBlock(ctx, tx, chainID, ranges[0].End, false)
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

// coverageReadPageSize bounds generated result allocations without changing
// the caller's repeatable snapshot or chain-locked READ COMMITTED transaction.
const coverageReadPageSize = 512

func queryCoverageRangesTx(ctx context.Context, tx pgx.Tx, chainID string) ([]BlockRange, error) {
	queries := dbgen.New(tx)
	cursor := dbgen.StoreLegacyQueryCoverageRangesTxStatement1Params{ChainID: chainID, AfterStart: "0", PageLimit: coverageReadPageSize}
	ranges := make([]BlockRange, 0)
	for {
		rows, err := queries.StoreLegacyQueryCoverageRangesTxStatement1(ctx, cursor)
		if err != nil {
			return nil, fmt.Errorf("query core coverage ranges: %w", err)
		}
		for _, row := range rows {
			start, err := strconv.ParseUint(row.RangeStart, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("decode core coverage range start: %w", err)
			}
			end, err := strconv.ParseUint(row.RangeEnd, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("decode core coverage range end: %w", err)
			}
			ranges = append(ranges, BlockRange{Start: start, End: end})
		}
		if len(rows) < coverageReadPageSize {
			break
		}
		cursor.HasCursor = true
		cursor.AfterStart = rows[len(rows)-1].RangeStart
	}
	if err := validateNormalizedCoverageRanges(ranges); err != nil {
		return nil, fmt.Errorf("validate core coverage ranges: %w", err)
	}
	return ranges, nil
}

func replaceCoverageRangesTx(ctx context.Context, tx pgx.Tx, chainID string, ranges []BlockRange) error {
	if err := validateNormalizedCoverageRanges(ranges); err != nil {
		return err
	}
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		return dbgen.New(tx).StoreLegacyReplaceCoverageRangesTxStatement1(ctx, queryValue0)
	}(); err != nil {
		return fmt.Errorf("replace core coverage ranges: %w", err)
	}
	for _, blockRange := range ranges {
		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(chainID); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(decimal(blockRange.Start)); err != nil {
				return err
			}
			var queryValue2 pgtype.Numeric
			if err := queryValue2.Scan(decimal(blockRange.End)); err != nil {
				return err
			}
			return dbgen.New(tx).StoreLegacyReplaceCoverageRangesTxStatement2(ctx, queryValue0, queryValue1, queryValue2)
		}(); err != nil {
			return fmt.Errorf("insert core coverage range %d-%d: %w", blockRange.Start, blockRange.End, err)
		}
	}
	return nil
}

func queryCanonicalReferencesTx(ctx context.Context, tx pgx.Tx, chainID string, configuredStart uint64) ([]BlockRef, error) {
	queries := dbgen.New(tx)
	cursor := dbgen.StoreLegacyQueryCanonicalReferencesTxStatement1Params{ChainID: chainID, ConfiguredStart: decimal(configuredStart), AfterNumber: "0", PageLimit: coverageReadPageSize}
	references := make([]BlockRef, 0)
	for {
		rows, err := queries.StoreLegacyQueryCanonicalReferencesTxStatement1(ctx, cursor)
		if err != nil {
			return nil, fmt.Errorf("query canonical blocks for coverage: %w", err)
		}
		for _, row := range rows {
			number, err := strconv.ParseUint(row.Number, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("decode coverage block number: %w", err)
			}
			hash, err := hashFromBytes(row.BlockHash)
			if err != nil {
				return nil, err
			}
			parent, err := hashFromBytes(row.ParentHash)
			if err != nil {
				return nil, err
			}
			references = append(references, BlockRef{Number: number, Hash: hash, ParentHash: parent})
		}
		if len(rows) < coverageReadPageSize {
			break
		}
		cursor.HasCursor = true
		cursor.AfterNumber = rows[len(rows)-1].Number
	}
	return references, nil
}
