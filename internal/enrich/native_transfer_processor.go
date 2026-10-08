package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"

	"github.com/islishude/etherview/internal/chainbundle"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/nativetransfer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type PostgresNativeTransferProcessor struct{ db dbaccess.Database }

func NewPostgresNativeTransferProcessor(db dbaccess.Database) (*PostgresNativeTransferProcessor, error) {
	if db == nil {
		return nil, errors.New("native transfer processor requires a database")
	}
	return &PostgresNativeTransferProcessor{db: db}, nil
}
func (*PostgresNativeTransferProcessor) Stage() StageID { return NativeTransferStage }
func (p *PostgresNativeTransferProcessor) ProcessLease(ctx context.Context, lease Lease, queue *PostgresJobQueue) (StageResult, error) {
	return p.Process(ctx, bindStagePublication(lease.Job, lease, queue))
}
func (p *PostgresNativeTransferProcessor) Process(ctx context.Context, job Job) (StageResult, error) {
	if err := job.Validate(); err != nil {
		return StageResult{}, Permanent(err)
	}
	if job.Stage != NativeTransferStage {
		return StageResult{}, Permanent(errors.New("invalid native transfer stage"))
	}
	return runStageTransaction(ctx, p.db, job, func(ctx context.Context, tx pgx.Tx) (StageResult, error) {
		canonical, err := lockCanonicalBlock(ctx, tx, job)
		if err != nil {
			return StageResult{}, err
		}
		if !canonical {
			return StageResult{State: ResultComplete, Details: map[string]string{"outcome": "stale_canonical_skipped"}}, nil
		}
		var chain, number pgtype.Numeric
		if err := chain.Scan(job.ChainID); err != nil {
			return StageResult{}, Permanent(err)
		}
		if err := number.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return StageResult{}, Permanent(err)
		}
		q := dbgen.New(tx)
		raw, err := q.NativeTransferSourceBlock(ctx, chain, number, job.BlockHash[:])
		if err != nil {
			return StageResult{}, err
		}
		bundle, err := chainbundle.DecodeStoredBlock(json.RawMessage(raw))
		if err != nil {
			return StageResult{}, Permanent(err)
		}
		if bundle.Block.Hash() != job.BlockHash || bundle.Block.NumberU64() != job.BlockNumber {
			return StageResult{}, Permanent(errors.New("native transfer block identity mismatch"))
		}
		if err := q.NativeTransferClear(ctx, chain, number, job.BlockHash[:]); err != nil {
			return StageResult{}, err
		}
		if bundle.Block.Header().SlotNumber == nil {
			return StageResult{State: ResultComplete, Details: map[string]string{"outcome": "not_applicable"}}, nil
		}
		rows, err := q.NativeTransferSourceReceipts(ctx, chain, number, job.BlockHash[:])
		if err != nil {
			return StageResult{}, err
		}
		receipts := make([]json.RawMessage, len(rows))
		for i := range rows {
			receipts[i] = rows[i]
		}
		bundle, err = bundle.WithStoredReceipts(receipts)
		if err != nil {
			return StageResult{}, Permanent(err)
		}
		count := 0
		for _, receipt := range bundle.Receipts {
			for _, log := range receipt.Logs {
				transfer, recognized, err := nativetransfer.Parse(log)
				if err != nil {
					return StageResult{}, Permanent(err)
				}
				if !recognized {
					continue
				}
				if receipt.Status != 1 || uint64(log.Index) > math.MaxInt64 || uint64(log.TxIndex) > math.MaxInt64 {
					return StageResult{}, Permanent(errors.New("invalid native transfer inclusion"))
				}
				var amount pgtype.Numeric
				if err := amount.Scan(transfer.Amount); err != nil {
					return StageResult{}, Permanent(err)
				}
				if err := q.NativeTransferInsert(ctx, dbgen.NativeTransferInsertParams{ChainID: chain, BlockNumber: number, BlockHash: job.BlockHash[:], TransactionHash: log.TxHash[:], TransactionIndex: int64(log.TxIndex), LogIndex: int64(log.Index), FromAddress: transfer.From[:], ToAddress: transfer.To[:], Amount: amount}); err != nil {
					return StageResult{}, err
				}
				count++
			}
		}
		return StageResult{State: ResultComplete, Details: map[string]string{"transfers": strconv.Itoa(count)}}, nil
	})
}
