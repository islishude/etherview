package state

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
)

var _ catalog.ERC20StateReconciler = (*NFTReconciler)(nil)

// Balances observes every ERC-20 candidate through one endpoint and one exact
// block-hash selector. Candidate discovery and database snapshots are owned by
// catalog and are closed before this external call starts.
func (reconciler *NFTReconciler) ERC20Balances(
	ctx context.Context,
	snapshot catalog.Snapshot,
	ownerAddressText string,
	candidates []catalog.ERC20BalanceCandidate,
) ([]catalog.ERC20BalanceObservation, error) {
	if reconciler == nil || reconciler.pool == nil || reconciler.canonical == nil {
		return nil, erc20RPCUnavailable()
	}
	if len(candidates) == 0 {
		return []catalog.ERC20BalanceObservation{}, nil
	}
	if len(candidates) > 256 {
		return nil, errors.New("too many ERC-20 balance candidates")
	}
	reference, _, err := validateNFTSnapshot(snapshot)
	if err != nil {
		return nil, errors.New("invalid ERC-20 state snapshot")
	}
	owner, err := ethrpc.ParseAddress(ownerAddressText)
	if err != nil {
		return nil, errors.New("invalid ERC-20 owner address")
	}
	ownerBytes := owner.Bytes()
	seen := make(map[common.Address]struct{}, len(candidates))
	results := make([]hexutil.Bytes, len(candidates))
	elements := make([]rpc.BatchElem, len(candidates))
	for index, candidate := range candidates {
		contract, parseErr := ethrpc.ParseAddress(candidate.TokenAddress)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid ERC-20 candidate address %d", index)
		}
		if _, duplicate := seen[contract]; duplicate {
			return nil, errors.New("duplicate ERC-20 balance candidate")
		}
		seen[contract] = struct{}{}
		callData := make([]byte, len(erc20BalanceOfSelector)+32)
		copy(callData, erc20BalanceOfSelector)
		copy(callData[len(callData)-len(ownerBytes):], ownerBytes)
		elements[index] = rpc.BatchElem{
			Method: "eth_call",
			Args: []any{
				map[string]any{"to": contract, "data": hexutil.Bytes(callData)},
				canonicalSelector(reference),
			},
			Result: &results[index],
		}
	}
	endpoint, err := reconciler.pool.Acquire(ethrpc.PurposeState)
	if err != nil {
		return nil, erc20RPCUnavailable()
	}
	if err := endpoint.BatchCallContext(ctx, elements); err != nil {
		reconciler.pool.ReportFailure(endpoint.Name)
		return nil, erc20RPCUnavailable()
	}
	observations := make([]catalog.ERC20BalanceObservation, len(results))
	for index, element := range elements {
		if element.Error != nil || len(results[index]) != 32 {
			reconciler.pool.ReportFailure(endpoint.Name)
			return nil, erc20RPCUnavailable()
		}
		observations[index] = catalog.ERC20BalanceObservation{
			Balance:    new(big.Int).SetBytes(results[index]).String(),
			Confidence: catalog.NFTStateConfidenceRPCExact,
		}
	}
	if err := reconciler.requireCanonicalERC20(ctx, reference); err != nil {
		return nil, err
	}
	reconciler.pool.ReportSuccess(endpoint.Name)
	return observations, nil
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
		return fmt.Errorf("%w: canonical block changed during ERC-20 state reconciliation", httpapi.ErrNotReady)
	}
	return nil
}

func erc20RPCUnavailable() error {
	return fmt.Errorf("%w: exact ERC-20 state RPC is unavailable", httpapi.ErrUnavailable)
}
