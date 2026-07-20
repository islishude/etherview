package state

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
)

var (
	erc20BalanceOfSelector   = []byte{0x70, 0xa0, 0x82, 0x31}
	erc20TotalSupplySelector = []byte{0x18, 0x16, 0x0d, 0xdd}
)

// NativeBalance returns one account balance from a fixed canonical block. The
// canonical hash is checked again after the RPC response so a concurrent reorg
// can never turn an old observation into a current success.
func (r *Reader) NativeBalance(ctx context.Context, address string) (string, error) {
	balances, err := r.NativeBalances(ctx, []string{address})
	if err != nil {
		return "", err
	}
	return balances[0], nil
}

// NativeBalances returns one coherent fixed-block observation for every
// address. A batch-capable endpoint is preferred, but the fallback still uses
// the same EIP-1898 selector and performs one final canonicality check.
func (r *Reader) NativeBalances(ctx context.Context, addresses []string) ([]string, error) {
	if len(addresses) == 0 {
		return nil, errors.New("native balance address list is empty")
	}
	parsed := make([]ethrpc.Address, len(addresses))
	for index, address := range addresses {
		value, err := ethrpc.ParseAddress(address)
		if err != nil {
			return nil, fmt.Errorf("invalid native balance address %d: %w", index, err)
		}
		parsed[index] = value
	}
	reference, endpoint, err := r.fixedStateEndpoint(ctx)
	if err != nil {
		return nil, err
	}
	selector := canonicalSelector(reference)
	results := make([]ethrpc.Quantity, len(parsed))
	elements := make([]ethrpc.BatchElem, len(parsed))
	for index := range parsed {
		elements[index] = ethrpc.BatchElem{
			Method: "eth_getBalance", Params: []any{parsed[index].String(), selector}, Result: &results[index],
		}
	}
	if batch, ok := endpoint.Client.(ethrpc.BatchCaller); ok {
		if err := batch.BatchCall(ctx, elements); err != nil {
			r.Pool.ReportFailure(endpoint.Name)
			return nil, stateUnavailable(err)
		}
		for _, element := range elements {
			if element.Error != nil {
				r.Pool.ReportFailure(endpoint.Name)
				return nil, stateUnavailable(element.Error)
			}
		}
	} else {
		for index := range elements {
			if err := endpoint.Client.Call(ctx, elements[index].Method, elements[index].Params, elements[index].Result); err != nil {
				r.Pool.ReportFailure(endpoint.Name)
				return nil, stateUnavailable(err)
			}
		}
	}
	if err := r.confirmCanonical(ctx, endpoint, reference); err != nil {
		return nil, err
	}
	balances := make([]string, len(results))
	for index, result := range results {
		value, err := decimal(result)
		if err != nil {
			return nil, CapabilityError{Code: "malformed_response"}
		}
		balances[index] = value
	}
	return balances, nil
}

// ERC20Balance returns balanceOf(owner) at one fixed canonical block.
func (r *Reader) ERC20Balance(ctx context.Context, contract, owner string) (string, error) {
	contractAddress, err := ethrpc.ParseAddress(contract)
	if err != nil {
		return "", fmt.Errorf("invalid token contract address: %w", err)
	}
	ownerAddress, err := ethrpc.ParseAddress(owner)
	if err != nil {
		return "", fmt.Errorf("invalid token owner address: %w", err)
	}
	ownerBytes, err := ownerAddress.Bytes()
	if err != nil {
		return "", err
	}
	callData := make([]byte, len(erc20BalanceOfSelector)+32)
	copy(callData, erc20BalanceOfSelector)
	copy(callData[len(callData)-len(ownerBytes):], ownerBytes)
	return r.erc20Uint256Call(ctx, contractAddress, callData)
}

// ERC20TotalSupply returns totalSupply() at one fixed canonical block.
func (r *Reader) ERC20TotalSupply(ctx context.Context, contract string) (string, error) {
	contractAddress, err := ethrpc.ParseAddress(contract)
	if err != nil {
		return "", fmt.Errorf("invalid token contract address: %w", err)
	}
	return r.erc20Uint256Call(ctx, contractAddress, erc20TotalSupplySelector)
}

func (r *Reader) erc20Uint256Call(ctx context.Context, contract ethrpc.Address, callData []byte) (string, error) {
	reference, endpoint, err := r.fixedStateEndpoint(ctx)
	if err != nil {
		return "", err
	}
	selector := canonicalSelector(reference)
	call := map[string]any{"to": contract.String(), "data": ethrpc.DataFromBytes(callData).String()}
	var result ethrpc.Data
	if err := endpoint.Client.Call(ctx, "eth_call", []any{call, selector}, &result); err != nil {
		r.Pool.ReportFailure(endpoint.Name)
		return "", stateUnavailable(err)
	}
	if err := r.confirmCanonical(ctx, endpoint, reference); err != nil {
		return "", err
	}
	bytes, err := result.Bytes()
	if err != nil || len(bytes) != 32 {
		return "", fmt.Errorf("decode fixed-block ERC-20 uint256 result")
	}
	return new(big.Int).SetBytes(bytes).String(), nil
}

func (r *Reader) fixedStateEndpoint(ctx context.Context) (CanonicalRef, *ethrpc.Endpoint, error) {
	if r == nil || r.Canonical == nil || r.Pool == nil {
		return CanonicalRef{}, nil, CapabilityError{Code: "not_configured"}
	}
	reference, err := r.Canonical.Tip(ctx)
	if err != nil {
		return CanonicalRef{}, nil, err
	}
	endpoint, err := r.Pool.Acquire(ethrpc.PurposeState)
	if err != nil {
		return CanonicalRef{}, nil, CapabilityError{Code: "endpoint_unavailable"}
	}
	return reference, endpoint, nil
}

func (r *Reader) confirmCanonical(ctx context.Context, endpoint *ethrpc.Endpoint, reference CanonicalRef) error {
	canonical, err := r.Canonical.IsCanonical(ctx, reference)
	if err != nil {
		return fmt.Errorf("recheck fixed state block: %w", err)
	}
	if !canonical {
		return fmt.Errorf("%w: canonical block changed during state query", httpapi.ErrNotReady)
	}
	r.Pool.ReportSuccess(endpoint.Name)
	return nil
}

func canonicalSelector(reference CanonicalRef) map[string]any {
	return map[string]any{"blockHash": reference.Hash.String(), "requireCanonical": true}
}
