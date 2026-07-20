package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

type Options struct {
	DefaultPageSize   int
	MaxPageSize       int
	MaxChartPoints    int
	MaxTraceFrames    int
	MaxTraceDataBytes int
	MaxTextBytes      int
	NFTState          NFTStateReconciler
}

func (options *Options) defaults() {
	if options.DefaultPageSize <= 0 {
		options.DefaultPageSize = 50
	}
	if options.MaxPageSize <= 0 {
		options.MaxPageSize = 200
	}
	if options.MaxChartPoints <= 0 {
		options.MaxChartPoints = 5000
	}
	if options.MaxTraceFrames <= 0 {
		options.MaxTraceFrames = 100_000
	}
	if options.MaxTraceDataBytes <= 0 {
		options.MaxTraceDataBytes = 8 << 20
	}
	if options.MaxTextBytes <= 0 {
		options.MaxTextBytes = 1 << 20
	}
}

type Postgres struct {
	db       *sql.DB
	options  Options
	nftState NFTStateReconciler
}

func NewPostgres(db *sql.DB, options Options) (*Postgres, error) {
	if db == nil {
		return nil, errors.New("catalog requires a PostgreSQL database")
	}
	options.defaults()
	if options.DefaultPageSize > options.MaxPageSize || options.MaxPageSize >= int(^uint(0)>>1) ||
		options.MaxChartPoints <= 0 || options.MaxTraceFrames <= 0 || options.MaxTraceDataBytes <= 0 || options.MaxTextBytes <= 0 {
		return nil, errors.New("catalog limits are invalid")
	}
	return &Postgres{db: db, options: options, nftState: options.NFTState}, nil
}

func (catalog *Postgres) beginRead(ctx context.Context) (*sql.Tx, error) {
	if catalog == nil || catalog.db == nil {
		return nil, errors.New("catalog database is nil")
	}
	tx, err := catalog.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin catalog snapshot: %w", err)
	}
	return tx, nil
}

func validateChainID(chainID string) error {
	if !canonicalUint256(chainID) || chainID == "0" {
		return ErrInvalidInput
	}
	return nil
}

func (catalog *Postgres) pageLimit(requested int) (int, error) {
	if catalog == nil {
		return 0, errors.New("catalog is nil")
	}
	if requested == 0 {
		return catalog.options.DefaultPageSize, nil
	}
	if requested < 0 || requested > catalog.options.MaxPageSize {
		return 0, ErrInvalidInput
	}
	return requested, nil
}

func readCanonicalSnapshot(ctx context.Context, tx *sql.Tx, chainID string) (Snapshot, error) {
	var number string
	var hash []byte
	err := tx.QueryRowContext(ctx, canonicalSnapshotSQL, chainID).Scan(&number, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, StageUnavailableError{Stage: StageCore, State: StageMissing}
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("read canonical catalog snapshot: %w", err)
	}
	if !canonicalUint256(number) {
		return Snapshot{}, fmt.Errorf("%w: invalid canonical snapshot height", ErrCorruptData)
	}
	encodedHash, err := lowerHex(hash, 32)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: invalid canonical snapshot hash", ErrCorruptData)
	}
	return Snapshot{ChainID: chainID, BlockNumber: number, BlockHash: encodedHash}, nil
}

func validateCanonicalSnapshot(ctx context.Context, tx *sql.Tx, snapshot Snapshot) error {
	if err := validateChainID(snapshot.ChainID); err != nil || !canonicalUint256(snapshot.BlockNumber) {
		return ErrInvalidCursor
	}
	hash, err := decodeFixedHex(snapshot.BlockHash, 32)
	if err != nil || snapshot.BlockHash != "0x"+hex.EncodeToString(hash) {
		return ErrInvalidCursor
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, validateCanonicalSnapshotSQL,
		snapshot.ChainID, snapshot.BlockNumber, hash,
	).Scan(&exists); err != nil {
		return fmt.Errorf("validate catalog cursor snapshot: %w", err)
	}
	if !exists {
		return ErrInvalidCursor
	}
	return nil
}

func requireStage(ctx context.Context, tx *sql.Tx, snapshot Snapshot, stage Stage) error {
	hash, err := decodeFixedHex(snapshot.BlockHash, 32)
	if err != nil {
		return ErrCorruptData
	}
	var state string
	err = tx.QueryRowContext(ctx, latestStageSQL,
		snapshot.ChainID, snapshot.BlockNumber, hash, string(stage), stage.Version(),
	).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return StageUnavailableError{
			Stage: stage, State: StageMissing, BlockNumber: snapshot.BlockNumber, BlockHash: snapshot.BlockHash,
		}
	}
	if err != nil {
		return fmt.Errorf("read %s catalog stage: %w", stage, err)
	}
	switch StageState(state) {
	case StageComplete:
		return nil
	case StageUnavailable, StageFailed:
		return StageUnavailableError{
			Stage: stage, State: StageState(state), BlockNumber: snapshot.BlockNumber, BlockHash: snapshot.BlockHash,
		}
	default:
		return fmt.Errorf("%w: invalid %s stage state", ErrCorruptData, stage)
	}
}

func commitRead(tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit catalog snapshot: %w", err)
	}
	return nil
}

const canonicalSnapshotSQL = `
SELECT number::text, block_hash
FROM canonical_blocks
WHERE chain_id = $1::numeric
ORDER BY number DESC
LIMIT 1`

const validateCanonicalSnapshotSQL = `
SELECT EXISTS (
    SELECT 1
    FROM canonical_blocks
    WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
)`

const latestStageSQL = `
SELECT state
FROM published_block_stage_results
WHERE chain_id = $1::numeric
  AND block_number = $2::numeric
  AND block_hash = $3
  AND stage = $4
  AND stage_version = $5`
