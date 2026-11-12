package state

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrNativeBalanceIntegrity = errors.New("native_balance_integrity")

type NativeBalanceCache interface {
	Read(context.Context, CanonicalRef, []common.Address) (map[common.Address]string, error)
	Save(context.Context, CanonicalRef, map[common.Address]string) error
}

type PostgresNativeBalances struct {
	DB      dbaccess.Database
	ChainID string
}

func (s PostgresNativeBalances) Read(ctx context.Context, ref CanonicalRef, addresses []common.Address) (map[common.Address]string, error) {
	var chain, number pgtype.Numeric
	if err := chain.Scan(s.ChainID); err != nil {
		return nil, err
	}
	if err := number.Scan(strconv.FormatUint(ref.Number, 10)); err != nil {
		return nil, err
	}
	requested := make([][]byte, len(addresses))
	for i, a := range addresses {
		requested[i] = a.Bytes()
	}
	rows, err := dbgen.New(s.DB).NativeBalanceRead(ctx, dbgen.NativeBalanceReadParams{ChainID: chain, BlockNumber: number, BlockHash: ref.Hash[:], Addresses: requested})
	if err != nil {
		return nil, err
	}
	values := make(map[common.Address]string, len(rows))
	for _, row := range rows {
		if len(row.Address) != 20 {
			return nil, ErrNativeBalanceIntegrity
		}
		if _, err := parseUint256(row.Balance); err != nil {
			return nil, ErrNativeBalanceIntegrity
		}
		values[common.BytesToAddress(row.Address)] = row.Balance
	}
	return values, nil
}

func (s PostgresNativeBalances) Save(ctx context.Context, ref CanonicalRef, values map[common.Address]string) error {
	var chain, number pgtype.Numeric
	if err := chain.Scan(s.ChainID); err != nil {
		return err
	}
	if err := number.Scan(strconv.FormatUint(ref.Number, 10)); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer dbaccess.Rollback(ctx, tx)
	q := dbgen.New(tx)
	if _, err := q.NativeBalanceLockCanonical(ctx, chain, number, ref.Hash[:]); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return publicquery.ErrNotReady
		}
		return err
	}
	// All batch writers take account row locks in address order.
	addresses := make([]common.Address, 0, len(values))
	for a := range values {
		addresses = append(addresses, a)
	}
	slices.SortFunc(addresses, func(a, b common.Address) int { return bytes.Compare(a[:], b[:]) })
	for _, a := range addresses {
		if _, err := parseUint256(values[a]); err != nil {
			return ErrNativeBalanceIntegrity
		}
		var balance pgtype.Numeric
		if err := balance.Scan(values[a]); err != nil {
			return err
		}
		n, err := q.NativeBalanceInsertObservation(ctx, dbgen.NativeBalanceInsertObservationParams{ChainID: chain, BlockNumber: number, BlockHash: ref.Hash[:], Address: a[:], Balance: balance, RpcExact: true})
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrNativeBalanceIntegrity
		}
	}
	return tx.Commit(ctx)
}

// nativeStateBatch combines only missing balances with the caller's other state
// reads. The caller supplies one endpoint when code/delegation needs pinning.
func (r *Reader) nativeStateBatch(ctx context.Context, ref CanonicalRef, addresses []common.Address, extra []rpc.BatchElem, endpoint *ethrpc.Endpoint) ([]string, error) {
	if r == nil || r.Canonical == nil {
		return nil, CapabilityError{Code: "not_configured"}
	}
	values := make(map[common.Address]string)
	if r.Balances != nil {
		var err error
		values, err = r.Balances.Read(ctx, ref, addresses)
		if err != nil {
			return nil, err
		}
	}
	missing := make([]common.Address, 0, len(addresses))
	seen := make(map[common.Address]bool)
	for _, a := range addresses {
		if _, ok := values[a]; !ok && !seen[a] {
			missing = append(missing, a)
			seen[a] = true
		}
	}
	results := make([]*hexutil.Big, len(missing))
	elements := make([]rpc.BatchElem, 0, len(missing)+len(extra))
	for i, a := range missing {
		elements = append(elements, rpc.BatchElem{Method: "eth_getBalance", Args: []any{a, canonicalSelector(ref)}, Result: &results[i]})
	}
	elements = append(elements, extra...)
	if len(elements) > 0 {
		if endpoint == nil {
			if r.Pool == nil {
				return nil, CapabilityError{Code: "endpoint_unavailable"}
			}
			var err error
			endpoint, err = r.Pool.Acquire(ethrpc.PurposeState)
			if err != nil {
				return nil, CapabilityError{Code: "endpoint_unavailable"}
			}
		}
		if err := endpoint.BatchCallContext(ctx, elements); err != nil {
			r.Pool.ReportFailure(endpoint.Name)
			return nil, stateUnavailable(err)
		}
		for _, element := range elements {
			if element.Error != nil {
				r.Pool.ReportFailure(endpoint.Name)
				return nil, stateUnavailable(element.Error)
			}
		}
		r.Pool.ReportSuccess(endpoint.Name)
	}
	fresh := make(map[common.Address]string, len(missing))
	for i, a := range missing {
		if results[i] == nil {
			return nil, ErrNativeBalanceIntegrity
		}
		value := decimal(*results[i])
		if _, err := parseUint256(value); err != nil {
			return nil, ErrNativeBalanceIntegrity
		}
		values[a] = value
		fresh[a] = value
	}
	if err := r.confirmNativeCanonical(ctx, ref); err != nil {
		return nil, err
	}
	if len(fresh) > 0 && r.Balances != nil {
		if err := r.Balances.Save(ctx, ref, fresh); err != nil {
			return nil, err
		}
		if err := r.confirmNativeCanonical(ctx, ref); err != nil {
			return nil, err
		}
	}
	balances := make([]string, len(addresses))
	for i, a := range addresses {
		balances[i] = values[a]
	}
	return balances, nil
}

func (r *Reader) confirmNativeCanonical(ctx context.Context, ref CanonicalRef) error {
	valid, err := r.Canonical.IsCanonical(ctx, ref)
	if err != nil {
		return err
	}
	if !valid {
		return publicquery.ErrNotReady
	}
	return nil
}
