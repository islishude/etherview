package state

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// BalanceDemand waits for a durable exact-block observation. No database
// snapshot or connection is held between polls.
type BalanceDemand struct {
	Store   PostgresNativeBalances
	Timeout time.Duration
}

// Leave enough of the caller's budget to recheck canonicality and encode the
// endpoint-specific response after a missing or failed input.
func (d *BalanceDemand) waitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	budget := d.Timeout
	if deadline, ok := ctx.Deadline(); ok {
		budget = min(budget, time.Until(deadline))
	}
	return context.WithTimeout(ctx, budget-min(250*time.Millisecond, budget/10))
}

func (d *BalanceDemand) Read(ctx context.Context, ref CanonicalRef, addresses []common.Address) ([]string, error) {
	if len(addresses) == 0 || len(addresses) > maxBalanceCandidates {
		return nil, errors.New("invalid native balance batch size")
	}
	ctx, cancel := d.waitContext(ctx)
	defer cancel()
	canonical := PostgresCanonicalSource{DB: d.Store.DB, ChainID: d.Store.ChainID}
	submitted := false
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		values, err := d.Store.Read(ctx, ref, addresses)
		if err != nil {
			if ctx.Err() != nil {
				return nil, CapabilityError{Code: "endpoint_unavailable"}
			}
			return nil, err
		}
		valid, err := canonical.IsCanonical(ctx, ref)
		if err != nil {
			if ctx.Err() != nil {
				return nil, CapabilityError{Code: "endpoint_unavailable"}
			}
			return nil, err
		}
		if !valid {
			return nil, publicquery.ErrNotReady
		}
		result := make([]string, len(addresses))
		var missing []common.Address
		for i, address := range addresses {
			value, ok := values[address]
			if !ok {
				missing = append(missing, address)
			}
			result[i] = value
		}
		if len(missing) == 0 {
			return result, nil
		}
		if !submitted {
			if err := d.submit(ctx, ref, missing); err != nil {
				return nil, err
			}
			submitted = true
		}
		select {
		case <-ctx.Done():
			return nil, CapabilityError{Code: "endpoint_unavailable"}
		case <-ticker.C:
		}
	}
}

func (d *BalanceDemand) submit(ctx context.Context, ref CanonicalRef, missing []common.Address) error {
	slices.SortFunc(missing, func(a, b common.Address) int { return bytes.Compare(a[:], b[:]) })
	addresses := make([]byte, 0, len(missing)*common.AddressLength)
	for _, a := range slices.Compact(missing) {
		addresses = append(addresses, a[:]...)
	}
	var chain, number pgtype.Numeric
	if err := chain.Scan(d.Store.ChainID); err != nil {
		return err
	}
	if err := number.Scan(strconv.FormatUint(ref.Number, 10)); err != nil {
		return err
	}
	return dbaccess.WithTransaction(ctx, d.Store.DB, func(q *dbgen.Queries) error {
		if err := q.BalanceDemandLock(ctx, d.Store.ChainID); err != nil {
			return err
		}
		if err := q.BalanceDemandExpire(ctx, chain); err != nil {
			return err
		}
		rows, err := q.BalanceDemandSubmit(ctx, dbgen.BalanceDemandSubmitParams{ChainID: chain, BlockNumber: number, BlockHash: ref.Hash[:], Addresses: addresses})
		if err == nil && rows == 0 {
			return CapabilityError{Code: "endpoint_unavailable"}
		}
		return err
	})
}

type BalanceCollector struct {
	Store   PostgresNativeBalances
	Pool    *ethrpc.Pool
	Timeout time.Duration
	Logger  *slog.Logger
}

func (*BalanceCollector) Name() string { return "balance-acquisition" }

func (c *BalanceCollector) Run(ctx context.Context) error {
	var chain pgtype.Numeric
	if err := chain.Scan(c.Store.ChainID); err != nil {
		return err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		err := c.collect(ctx, chain)
		accountErr := c.collectAccount(ctx, chain)
		if accountErr != nil && !errors.Is(accountErr, pgx.ErrNoRows) && ctx.Err() == nil && c.Logger != nil {
			c.Logger.WarnContext(ctx, "account acquisition deferred", "error_code", "account_input_unavailable")
		}
		if err == nil || accountErr == nil {
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil && c.Logger != nil {
			c.Logger.WarnContext(ctx, "balance acquisition deferred", "error_code", "balance_acquisition_unavailable")
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	return nil
}
func (c *BalanceCollector) collect(ctx context.Context, chain pgtype.Numeric) error {
	q := dbgen.New(c.Store.DB)
	job, err := q.BalanceDemandClaim(ctx, (c.Timeout + time.Second).Milliseconds(), chain)
	if err != nil {
		return err
	}
	number, err := strconv.ParseUint(job.Number, 10, 64)
	if err != nil {
		return err
	}
	ref := CanonicalRef{Number: number, Hash: common.BytesToHash(job.BlockHash)}
	addresses := make([]common.Address, 0, len(job.Addresses)/common.AddressLength)
	for i := 0; i < len(job.Addresses); i += common.AddressLength {
		addresses = append(addresses, common.BytesToAddress(job.Addresses[i:i+common.AddressLength]))
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	reader := Reader{Pool: c.Pool, Canonical: PostgresCanonicalSource{DB: c.Store.DB, ChainID: c.Store.ChainID}}
	// Another overlapping batch may already have supplied some of these inputs.
	values, err := c.Store.Read(ctx, ref, addresses)
	if err != nil {
		return err
	}
	missing := make([]common.Address, 0, len(addresses))
	for _, a := range addresses {
		if _, ok := values[a]; !ok {
			missing = append(missing, a)
		}
	}
	var endpoint *ethrpc.Endpoint
	var source string
	if len(missing) > 0 {
		endpoint, err = c.Pool.Acquire(ethrpc.PurposeState)
		if err != nil {
			return CapabilityError{Code: "endpoint_unavailable"}
		}
		source = endpoint.Name
	}
	balances, err := reader.nativeStateBatch(ctx, ref, missing, nil, endpoint)
	if err != nil && !errors.Is(err, publicquery.ErrNotReady) {
		return err
	}
	stale := errors.Is(err, publicquery.ErrNotReady)
	fresh := make(map[common.Address]string, len(balances))
	for i, v := range balances {
		fresh[missing[i]] = v
	}
	var height pgtype.Numeric
	if err := height.Scan(job.Number); err != nil {
		return err
	}
	return dbaccess.WithTransaction(ctx, c.Store.DB, func(q *dbgen.Queries) error {
		n, err := q.BalanceDemandFinish(ctx, job.ID, job.Generation)
		if err != nil || n == 0 || stale || len(fresh) == 0 {
			return err
		}
		return saveNativeBalances(ctx, q, chain, height, ref, fresh, source)
	})
}
