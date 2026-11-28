package state

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	accountSummary    = "summary@1"
	accountDelegation = "delegation@1"
	accountCode       = "code@1"
	tokenBalance      = "balanceOf@1"
	tokenSupply       = "totalSupply@1"
)

type accountInput struct {
	Endpoint            string
	Balance, Nonce      string
	Code, DelegateCode  hexutil.Bytes
	DelegateUnavailable bool
	TokenValue          string
	TokenResults        []tokenResult
}

func (r *Reader) accountInput(ctx context.Context, ref CanonicalRef, op string, address common.Address, arg []byte) (accountInput, error) {
	if r.BalanceDemand == nil {
		return r.fetchAccount(ctx, ref, op, address, arg)
	}
	d := r.BalanceDemand
	ctx, cancel := d.waitContext(ctx)
	defer cancel()
	var chain, height pgtype.Numeric
	if err := chain.Scan(d.Store.ChainID); err != nil {
		return accountInput{}, err
	}
	if err := height.Scan(strconv.FormatUint(ref.Number, 10)); err != nil {
		return accountInput{}, err
	}
	if arg == nil {
		arg = []byte{}
	}
	key := dbgen.AccountInputReadParams{ChainID: chain, BlockNumber: height, BlockHash: ref.Hash[:], Operation: op, Address: address[:], Argument: arg}
	q := dbgen.New(d.Store.DB)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	submitted := false
	for {
		if err := r.confirmNativeCanonical(ctx, ref); err != nil {
			if ctx.Err() != nil {
				return accountInput{}, CapabilityError{Code: "endpoint_unavailable"}
			}
			return accountInput{}, err
		}
		raw, err := q.AccountInputRead(ctx, key)
		switch {
		case ctx.Err() != nil:
			return accountInput{}, CapabilityError{Code: "endpoint_unavailable"}
		case errors.Is(err, pgx.ErrNoRows):
			if !submitted {
				err = dbaccess.WithTransaction(ctx, d.Store.DB, func(q *dbgen.Queries) error {
					if e := q.BalanceDemandLock(ctx, d.Store.ChainID); e != nil {
						return e
					}
					if e := q.AccountInputExpire(ctx, chain); e != nil {
						return e
					}
					rows, e := q.AccountInputSubmit(ctx, dbgen.AccountInputSubmitParams(key))
					if e == nil && rows == 0 {
						return CapabilityError{Code: "endpoint_unavailable"}
					}
					return e
				})
				if err != nil {
					return accountInput{}, err
				}
				submitted = true
			}
		case err != nil:
			return accountInput{}, err
		case len(raw) > 0:
			var input accountInput
			if json.Unmarshal(raw, &input) != nil || input.Endpoint == "" {
				return input, stateUnavailable(nil)
			}
			return input, r.confirmNativeCanonical(ctx, ref)
		}
		select {
		case <-ctx.Done():
			return accountInput{}, CapabilityError{Code: "endpoint_unavailable"}
		case <-ticker.C:
		}
	}
}

func (r *Reader) fetchAccount(ctx context.Context, ref CanonicalRef, op string, address common.Address, arg []byte) (accountInput, error) {
	input := accountInput{}
	endpoint, err := r.Pool.Acquire(ethrpc.PurposeState)
	if err != nil {
		return input, CapabilityError{Code: "endpoint_unavailable"}
	}
	input.Endpoint = endpoint.Name
	selector := canonicalSelector(ref)
	switch op {
	case accountSummary:
		var nonce hexutil.Big
		values, e := r.nativeStateBatch(ctx, ref, []common.Address{address}, []rpc.BatchElem{
			{Method: "eth_getTransactionCount", Args: []any{address, selector}, Result: &nonce},
			{Method: "eth_getCode", Args: []any{address, selector}, Result: &input.Code},
		}, endpoint)
		if e != nil {
			return input, e
		}
		input.Balance, input.Nonce = values[0], decimal(nonce)
	case accountCode, accountDelegation:
		err = endpoint.CallContext(ctx, &input.Code, "eth_getCode", address, selector)
	case "tokenBatch@1":
		input.TokenResults, err = fetchTokenInputs(ctx, endpoint, ref, address, arg)
	case tokenBalance, tokenSupply:
		data := erc20TotalSupplySelector
		if op == tokenBalance {
			if len(arg) != common.AddressLength {
				return input, stateUnavailable(nil)
			}
			data = append(append([]byte{}, erc20BalanceOfSelector...), common.LeftPadBytes(arg, 32)...)
		}
		var result hexutil.Bytes
		err = endpoint.CallContext(ctx, &result, "eth_call", map[string]any{"to": address, "data": hexutil.Bytes(data)}, selector)
		if err == nil && len(result) != 32 {
			return input, errors.New("decode fixed-block ERC-20 uint256 result")
		}
		input.TokenValue = new(big.Int).SetBytes(result).String()
	default:
		return input, stateUnavailable(nil)
	}
	if err != nil || len(input.Code) > 65536 {
		r.Pool.ReportFailure(endpoint.Name)
		return input, stateUnavailable(err)
	}
	if delegate, ok := types.ParseDelegation(input.Code); ok && op != accountCode {
		err = endpoint.CallContext(ctx, &input.DelegateCode, "eth_getCode", delegate, selector)
		if err != nil || len(input.DelegateCode) > 65536 {
			r.Pool.ReportFailure(endpoint.Name)
			input.DelegateCode = nil
			input.DelegateUnavailable = true
		}
	}
	if !input.DelegateUnavailable {
		r.Pool.ReportSuccess(endpoint.Name)
	}
	return input, r.confirmNativeCanonical(ctx, ref)
}

func (c *BalanceCollector) collectAccount(ctx context.Context, chain pgtype.Numeric) error {
	q := dbgen.New(c.Store.DB)
	job, err := q.AccountInputClaim(ctx, (c.Timeout + time.Second).Milliseconds(), chain)
	if err != nil {
		return err
	}
	number, err := strconv.ParseUint(job.Height, 10, 64)
	if err != nil {
		return err
	}
	ref := CanonicalRef{Number: number, Hash: common.BytesToHash(job.BlockHash)}
	cache := &acquiredBalances{PostgresNativeBalances: c.Store}
	reader := Reader{Pool: c.Pool, Canonical: PostgresCanonicalSource{DB: c.Store.DB, ChainID: c.Store.ChainID}, Balances: cache}
	fetchCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	input, err := reader.fetchAccount(fetchCtx, ref, job.Operation, common.BytesToAddress(job.Address), job.Argument)
	if err != nil {
		if job.Attempts < 4 {
			return err
		}
		input = accountInput{}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return dbaccess.WithTransaction(ctx, c.Store.DB, func(q *dbgen.Queries) error {
		n, err := q.AccountInputFinish(ctx, dbgen.AccountInputFinishParams{ID: job.ID, Attempts: job.Attempts, Evidence: raw, Success: input.Endpoint != ""})
		if err != nil || n == 0 || input.Endpoint == "" {
			return err
		}
		observations := cache.fresh
		if len(observations) == 0 {
			return nil
		}
		var height pgtype.Numeric
		if err := height.Scan(job.Height); err != nil {
			return err
		}
		return saveNativeBalances(ctx, q, chain, height, ref, observations, input.Endpoint)
	})
}

type acquiredBalances struct {
	PostgresNativeBalances
	fresh map[common.Address]string
}

func (b *acquiredBalances) Save(_ context.Context, _ CanonicalRef, values map[common.Address]string) error {
	b.fresh = values
	return nil
}
