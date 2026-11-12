package enrich

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/islishude/etherview/internal/chainbundle"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type PostgresNativeBalanceProcessor struct {
	db      dbaccess.Database
	fetcher *ethrpc.BALFetcher
}

func NewPostgresNativeBalanceProcessor(db dbaccess.Database, pool *ethrpc.Pool) (*PostgresNativeBalanceProcessor, error) {
	if db == nil || pool == nil {
		return nil, errors.New("native balance processor requires database and RPC pool")
	}
	return &PostgresNativeBalanceProcessor{db: db, fetcher: &ethrpc.BALFetcher{Pool: pool}}, nil
}
func (*PostgresNativeBalanceProcessor) Stage() StageID { return NativeBalanceStage }
func (p *PostgresNativeBalanceProcessor) ProcessLease(ctx context.Context, lease Lease, queue *PostgresJobQueue) (StageResult, error) {
	return p.Process(ctx, bindStagePublication(lease.Job, lease, queue))
}

type nativeBalanceChange struct {
	Address     string `json:"address"`
	AccessIndex uint32 `json:"access_index"`
	Balance     string `json:"balance"`
}

func (p *PostgresNativeBalanceProcessor) Process(ctx context.Context, job Job) (StageResult, error) {
	if err := job.Validate(); err != nil {
		return StageResult{}, Permanent(err)
	}
	if job.Stage != NativeBalanceStage {
		return StageResult{}, Permanent(errors.New("invalid native balance stage"))
	}
	var chain, number pgtype.Numeric
	if err := chain.Scan(job.ChainID); err != nil {
		return StageResult{}, Permanent(err)
	}
	if err := number.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
		return StageResult{}, Permanent(err)
	}
	q := dbgen.New(p.db)
	raw, err := q.NativeTransferSourceBlock(ctx, chain, number, job.BlockHash[:])
	if err != nil {
		return StageResult{}, err
	}
	bundle, err := chainbundle.DecodeStoredBlock(raw)
	if err != nil || bundle.Block.Hash() != job.BlockHash || bundle.Block.NumberU64() != job.BlockNumber {
		return StageResult{}, Permanent(ethrpc.ErrBALInvalid)
	}
	applicable := bundle.Block.Header().BlockAccessListHash != nil
	canonical, err := q.StateIsCanonical(ctx, chain, number, job.BlockHash[:])
	if err != nil {
		return StageResult{}, err
	}
	var result ethrpc.BALResult
	if applicable && canonical {
		// All reads above and below are completed statements, never live snapshots
		// across the external request. Retained bodies are independently revalidated.
		stored, readErr := q.NativeBalanceBody(ctx, chain, job.BlockHash[:], number)
		switch {
		case readErr == nil:
			result, err = ethrpc.DecodeBAL(stored.Raw, stored.Method, bundle.Block)
		case errors.Is(readErr, pgx.ErrNoRows):
			result, err = p.fetcher.Fetch(ctx, bundle.Block)
		default:
			return StageResult{}, readErr
		}
		if errors.Is(err, ethrpc.ErrBALUnavailable) {
			return StageResult{}, Unavailable(err)
		}
		if errors.Is(err, ethrpc.ErrBALInvalid) {
			return StageResult{}, Permanent(err)
		}
		if err != nil {
			return StageResult{}, err
		}
	}
	return runStageTransaction(ctx, p.db, job, func(ctx context.Context, tx pgx.Tx) (StageResult, error) {
		valid, err := lockCanonicalBlock(ctx, tx, job)
		if err != nil {
			return StageResult{}, err
		}
		if !valid || !canonical {
			return StageResult{State: ResultComplete, Details: map[string]string{"outcome": "stale_canonical_skipped"}}, nil
		}
		if !applicable {
			return StageResult{State: ResultComplete, Details: map[string]string{"outcome": "not_applicable"}}, nil
		}
		q := dbgen.New(tx)
		count, err := q.NativeBalanceInsertBody(ctx, dbgen.NativeBalanceInsertBodyParams{ChainID: chain, BlockNumber: number, BlockHash: job.BlockHash[:], Raw: result.Raw, Method: result.Method})
		if err != nil {
			return StageResult{}, err
		}
		if count != 1 {
			return StageResult{}, Permanent(ethrpc.ErrBALInvalid)
		}
		changes := make([]nativeBalanceChange, 0, 256)
		ends := make([]nativeBalanceChange, 0, 256)
		flush := func(final bool) error {
			if len(changes) > 0 && (final || len(changes) >= 256) {
				data, e := json.Marshal(changes)
				if e != nil {
					return e
				}
				n, e := q.NativeBalanceInsertChanges(ctx, dbgen.NativeBalanceInsertChangesParams{ChainID: chain, BlockNumber: number, BlockHash: job.BlockHash[:], Changes: data})
				if e != nil {
					return e
				}
				if n != int64(len(changes)) {
					return Permanent(ethrpc.ErrBALInvalid)
				}
				changes = changes[:0]
			}
			if len(ends) > 0 && (final || len(ends) >= 256) {
				data, e := json.Marshal(ends)
				if e != nil {
					return e
				}
				n, e := q.NativeBalanceInsertEnds(ctx, dbgen.NativeBalanceInsertEndsParams{ChainID: chain, BlockNumber: number, BlockHash: job.BlockHash[:], Observations: data})
				if e != nil {
					return e
				}
				if n != int64(len(ends)) {
					return Permanent(ethrpc.ErrBALInvalid)
				}
				ends = ends[:0]
			}
			return nil
		}
		total := 0
		for _, account := range result.List {
			for i, c := range account.BalanceChanges {
				row := nativeBalanceChange{Address: hex.EncodeToString(account.Address[:]), AccessIndex: c.BlockAccessIndex, Balance: c.PostBalance.Dec()}
				changes = append(changes, row)
				total++
				if i == len(account.BalanceChanges)-1 {
					ends = append(ends, row)
				}
				if err := flush(false); err != nil {
					return StageResult{}, err
				}
			}
		}
		if err := flush(true); err != nil {
			return StageResult{}, err
		}
		return StageResult{State: ResultComplete, Details: map[string]string{"outcome": "indexed", "changes": strconv.Itoa(total)}}, nil
	})
}
