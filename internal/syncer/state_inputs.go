package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chaininput"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type StateInputCollector struct {
	DB                    dbaccess.Database
	ChainID               string
	Pool                  *ethrpc.Pool
	Timeout, PollInterval time.Duration
	Logger                *slog.Logger
}

func (*StateInputCollector) Name() string { return "stage-state-inputs" }
func (c *StateInputCollector) Run(ctx context.Context) error {
	var chain pgtype.Numeric
	if err := chain.Scan(c.ChainID); err != nil {
		return err
	}
	ticker := time.NewTicker(c.PollInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		err := c.collect(ctx, chain)
		if err == nil {
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil && c.Logger != nil {
			c.Logger.WarnContext(ctx, "stage input collection deferred", "error_code", "stage_input_unavailable")
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	return nil
}
func (c *StateInputCollector) collect(ctx context.Context, chain pgtype.Numeric) error {
	q := dbgen.New(c.DB)
	job, err := q.StageInputClaim(ctx, (c.Timeout + time.Second).Milliseconds(), chain)
	if err != nil {
		return err
	}
	pin := job.Endpoint
	if pin == "" {
		ep, e := c.Pool.Acquire(ethrpc.PurposeState)
		if e != nil {
			if job.Attempts >= 4 {
				_, err = q.StageInputFinish(ctx, dbgen.StageInputFinishParams{ID: job.ID, LeaseGeneration: job.LeaseGeneration, Outcome: "failed"})
				return err
			}
			return chaininput.ErrUnavailable
		}
		pin, err = q.StateInputPin(ctx, job.GroupID, ep.Name)
		if err != nil {
			return err
		}
	}
	fetchCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	ep, err := c.Pool.AcquireNamed(ethrpc.PurposeState, pin)
	finish := dbgen.StageInputFinishParams{ID: job.ID, LeaseGeneration: job.LeaseGeneration, Endpoint: pin, Outcome: "failed"}
	if err == nil {
		var req chaininput.Request
		if json.Unmarshal(job.Request, &req) != nil {
			return chaininput.ErrInvalid
		}
		selector := rpc.BlockNumberOrHashWithHash(common.BytesToHash(job.BlockHash), true)
		var value hexutil.Bytes
		switch req.Kind {
		case "code":
			err = ep.CallContext(fetchCtx, &value, "eth_getCode", req.Address, selector)
		case "storage":
			if req.Slot == nil {
				return chaininput.ErrInvalid
			}
			err = ep.CallContext(fetchCtx, &value, "eth_getStorageAt", req.Address, *req.Slot, selector)
		case "call":
			if len(req.Data) < 4 || len(req.Data) > 128*1024 || req.Gas > 30000000 {
				return chaininput.ErrInvalid
			}
			call := map[string]any{"to": req.Address, "data": req.Data}
			if req.From != nil {
				call["from"] = *req.From
			}
			if req.Gas != 0 {
				call["gas"] = req.Gas
			}
			err = ep.CallContext(fetchCtx, &value, "eth_call", call, selector)
		default:
			return chaininput.ErrInvalid
		}
		var rpcErr rpc.Error
		switch {
		case err == nil && len(value) <= 1<<20:
			finish.Result = value
			finish.Outcome = "ready"
			c.Pool.ReportSuccess(pin)
		case errors.As(err, &rpcErr) && rpcErr.ErrorCode() == 3:
			finish.Outcome = "reverted"
			if dataErr, ok := errors.AsType[rpc.DataError](err); ok {
				if raw, ok := dataErr.ErrorData().(string); ok && len(raw) <= 2+(1<<21) {
					finish.Result, _ = hexutil.Decode(raw)
				}
			}
		case err != nil:
			c.Pool.ReportFailure(pin)
		}
	}
	retry := err != nil && finish.Outcome == "failed" && job.Attempts < 4
	return dbaccess.WithTransaction(ctx, c.DB, func(q *dbgen.Queries) error {
		current, err := q.LockStateInputGroup(ctx, job.GroupID)
		if err != nil || current != pin {
			return err
		}
		if retry {
			if err := q.StateInputUnpinEmpty(ctx, dbgen.StateInputUnpinEmptyParams{GroupID: job.GroupID, Endpoint: pin, ID: job.ID, LeaseGeneration: job.LeaseGeneration}); err != nil {
				return err
			}
			return q.StageInputRetry(ctx, job.ID, job.LeaseGeneration)
		}
		_, err = q.StageInputFinish(ctx, finish)
		return err
	})
}
