package state

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
)

var _ catalog.ERC20StateReconciler = (*NFTReconciler)(nil)

// ErrExactERC20BalanceObservationConflict means two exact block-hash RPC reads
// disagreed for the same immutable owner and token fact. The first durable
// observation remains authoritative and is never overwritten.
var ErrExactERC20BalanceObservationConflict = errors.New("exact ERC-20 balance observation conflicts with persisted block fact")

type erc20BalanceBatchResult struct {
	index       int
	observation catalog.ERC20BalanceObservation
}

// Balances observes every ERC-20 candidate through one endpoint and one exact
// block-hash selector. Candidate discovery and database snapshots are owned by
// catalog and are closed before this external call starts.
func (reconciler *NFTReconciler) ERC20Balances(
	ctx context.Context,
	snapshot catalog.Snapshot,
	ownerAddressText string,
	candidates []catalog.ERC20BalanceCandidate,
) ([]catalog.ERC20BalanceObservation, error) {
	if reconciler == nil || reconciler.db == nil || reconciler.pool == nil || reconciler.canonical == nil {
		return nil, erc20RPCUnavailable()
	}
	if len(candidates) == 0 {
		return []catalog.ERC20BalanceObservation{}, nil
	}
	if len(candidates) > 256 {
		return nil, errors.New("too many ERC-20 balance candidates")
	}
	reference, chainID, err := validateNFTSnapshot(snapshot)
	if err != nil {
		return nil, errors.New("invalid ERC-20 state snapshot")
	}
	owner, err := ethrpc.ParseAddress(ownerAddressText)
	if err != nil {
		return nil, errors.New("invalid ERC-20 owner address")
	}
	seen := make(map[common.Address]struct{}, len(candidates))
	parsed := make([]common.Address, len(candidates))
	tokenAddresses := make([][]byte, len(candidates))
	for index, candidate := range candidates {
		contract, parseErr := ethrpc.ParseAddress(candidate.TokenAddress)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid ERC-20 candidate address %d", index)
		}
		if _, duplicate := seen[contract]; duplicate {
			return nil, errors.New("duplicate ERC-20 balance candidate")
		}
		seen[contract] = struct{}{}
		parsed[index] = contract
		tokenAddresses[index] = contract.Bytes()
	}
	cached, err := reconciler.cachedERC20Balances(ctx, chainID, owner, reference, tokenAddresses)
	if err != nil {
		return nil, err
	}
	observations := make([]catalog.ERC20BalanceObservation, len(candidates))
	missing := make([]int, 0, len(candidates))
	for index, contract := range parsed {
		if observation, found := cached[contract]; found {
			observations[index] = observation
			continue
		}
		missing = append(missing, index)
	}
	if len(missing) == 0 {
		if err := reconciler.requireCanonicalERC20(ctx, reference); err != nil {
			return nil, err
		}
		return observations, nil
	}
	endpoint, err := reconciler.pool.Acquire(ethrpc.PurposeState)
	if err != nil {
		return nil, erc20RPCUnavailable()
	}
	batchResults, err := callERC20BalanceBatch(ctx, endpoint, reference, owner, parsed, missing)
	if err != nil {
		reconciler.pool.ReportFailure(endpoint.Name)
		return nil, err
	}
	for _, result := range batchResults {
		observations[result.index] = result.observation
	}
	if err := reconciler.requireCanonicalERC20(ctx, reference); err != nil {
		return nil, err
	}
	reconciler.pool.ReportSuccess(endpoint.Name)
	if err := reconciler.persistERC20Balances(ctx, chainID, owner, parsed, observations, missing, reference); err != nil {
		return nil, err
	}
	if err := reconciler.requireCanonicalERC20(ctx, reference); err != nil {
		return nil, err
	}
	return observations, nil
}

func (reconciler *NFTReconciler) cachedERC20Balances(
	ctx context.Context,
	chainID string,
	owner common.Address,
	reference CanonicalRef,
	tokenAddresses [][]byte,
) (map[common.Address]catalog.ERC20BalanceObservation, error) {
	rows, err := reconciler.db.QueryContext(
		ctx,
		dbgen.StateERC20BalanceObservations,
		chainID,
		owner.Bytes(),
		strconv.FormatUint(reference.Number, 10),
		reference.Hash.Bytes(),
		tokenAddresses,
	)
	if err != nil {
		return nil, fmt.Errorf("read exact ERC-20 balance observations: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	requested := make(map[common.Address]struct{}, len(tokenAddresses))
	for _, address := range tokenAddresses {
		if len(address) != common.AddressLength {
			return nil, errors.New("invalid requested ERC-20 cache address")
		}
		requested[common.BytesToAddress(address)] = struct{}{}
	}
	observations := make(map[common.Address]catalog.ERC20BalanceObservation, len(tokenAddresses))
	for rows.Next() {
		var tokenAddress []byte
		var balance, confidence string
		if err := rows.Scan(&tokenAddress, &balance, &confidence); err != nil {
			return nil, fmt.Errorf("scan exact ERC-20 balance observation: %w", err)
		}
		if len(tokenAddress) != common.AddressLength {
			return nil, errors.New("cached ERC-20 token address has invalid length")
		}
		address := common.BytesToAddress(tokenAddress)
		if _, found := requested[address]; !found {
			return nil, errors.New("cached ERC-20 balance has an unexpected token address")
		}
		if _, duplicate := observations[address]; duplicate {
			return nil, errors.New("duplicate cached ERC-20 balance observation")
		}
		if _, err := parseUint256(balance); err != nil || confidence != catalog.NFTStateConfidenceRPCExact {
			return nil, errors.New("invalid cached ERC-20 balance observation")
		}
		observations[address] = catalog.ERC20BalanceObservation{Balance: balance, Confidence: confidence}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exact ERC-20 balance observations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close exact ERC-20 balance observations: %w", err)
	}
	return observations, nil
}

func callERC20BalanceBatch(
	ctx context.Context,
	endpoint *ethrpc.Endpoint,
	reference CanonicalRef,
	owner common.Address,
	candidates []common.Address,
	missing []int,
) ([]erc20BalanceBatchResult, error) {
	ownerBytes := owner.Bytes()
	callData := make([]byte, len(erc20BalanceOfSelector)+32)
	copy(callData, erc20BalanceOfSelector)
	copy(callData[len(callData)-len(ownerBytes):], ownerBytes)
	rpcResults := make([]hexutil.Bytes, len(missing))
	elements := make([]rpc.BatchElem, len(missing))
	for batchIndex, candidateIndex := range missing {
		elements[batchIndex] = rpc.BatchElem{
			Method: "eth_call",
			Args: []any{
				map[string]any{"to": candidates[candidateIndex], "data": hexutil.Bytes(callData)},
				canonicalSelector(reference),
			},
			Result: &rpcResults[batchIndex],
		}
	}
	if err := endpoint.BatchCallContext(ctx, elements); err != nil {
		return nil, erc20RPCUnavailable()
	}
	results := make([]erc20BalanceBatchResult, len(missing))
	for batchIndex, candidateIndex := range missing {
		if elements[batchIndex].Error != nil || len(rpcResults[batchIndex]) != 32 {
			return nil, erc20RPCUnavailable()
		}
		results[batchIndex] = erc20BalanceBatchResult{
			index: candidateIndex,
			observation: catalog.ERC20BalanceObservation{
				Balance:    new(big.Int).SetBytes(rpcResults[batchIndex]).String(),
				Confidence: catalog.NFTStateConfidenceRPCExact,
			},
		}
	}
	return results, nil
}

func (reconciler *NFTReconciler) persistERC20Balances(
	ctx context.Context,
	chainID string,
	owner common.Address,
	candidates []common.Address,
	observations []catalog.ERC20BalanceObservation,
	missing []int,
	reference CanonicalRef,
) error {
	tx, err := reconciler.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin ERC-20 balance observation transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	for _, index := range missing {
		if err := insertERC20Balance(ctx, tx, chainID, candidates[index], owner, reference, observations[index]); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ERC-20 balance observations: %w", err)
	}
	return nil
}

func insertERC20Balance(
	ctx context.Context,
	executor sqlExecutor,
	chainID string,
	contract common.Address,
	owner common.Address,
	reference CanonicalRef,
	observation catalog.ERC20BalanceObservation,
) error {
	if _, err := parseUint256(observation.Balance); err != nil || observation.Confidence != catalog.NFTStateConfidenceRPCExact {
		return errors.New("persist invalid ERC-20 balance observation")
	}
	result, err := executor.ExecContext(
		ctx,
		dbgen.StateWriteInsertERC20Balance,
		chainID,
		contract.Bytes(),
		owner.Bytes(),
		strconv.FormatUint(reference.Number, 10),
		reference.Hash.Bytes(),
		observation.Balance,
	)
	if err != nil {
		return fmt.Errorf("persist exact ERC-20 balance observation: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect exact ERC-20 balance persistence: %w", err)
	}
	if rows != 1 {
		return classifyERC20BalancePersistenceMiss(
			ctx,
			executor,
			chainID,
			contract.Bytes(),
			owner.Bytes(),
			strconv.FormatUint(reference.Number, 10),
			reference.Hash.Bytes(),
		)
	}
	return nil
}

func classifyERC20BalancePersistenceMiss(
	ctx context.Context,
	executor sqlExecutor,
	chainID string,
	contract []byte,
	owner []byte,
	blockNumber string,
	blockHash []byte,
) error {
	var canonical, stored bool
	err := executor.QueryRowContext(
		ctx,
		dbgen.StateWriteClassifyERC20BalancePersistenceMiss,
		chainID,
		contract,
		owner,
		blockNumber,
		blockHash,
	).Scan(&canonical, &stored)
	if err != nil {
		return fmt.Errorf("inspect exact ERC-20 balance persistence miss: %w", err)
	}
	if !canonical {
		return fmt.Errorf("%w: canonical block changed before ERC-20 balance persistence", publicquery.ErrNotReady)
	}
	if stored {
		return ErrExactERC20BalanceObservationConflict
	}
	return errors.New("exact ERC-20 balance persistence affected no row")
}

func (reconciler *NFTReconciler) requireCanonicalERC20(
	ctx context.Context,
	reference CanonicalRef,
) error {
	canonical, err := reconciler.canonical.IsCanonical(ctx, reference)
	if err != nil {
		return fmt.Errorf("recheck exact ERC-20 state block: %w", err)
	}
	if !canonical {
		return fmt.Errorf("%w: canonical block changed during ERC-20 state reconciliation", publicquery.ErrNotReady)
	}
	return nil
}

func erc20RPCUnavailable() error {
	return fmt.Errorf("%w: exact ERC-20 state RPC is unavailable", publicquery.ErrUnavailable)
}
