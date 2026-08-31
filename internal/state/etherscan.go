package state

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
)

// AccountKind returns the current account classification bound to one exact
// canonical block. It is intentionally narrower than Address: compatibility
// callers that only need the EOA/contract boundary do not also trigger origin,
// delegation-history, balance, or nonce work.
func (r *Reader) AccountKind(ctx context.Context, address string) (string, string, string, error) {
	parsed, err := ethrpc.ParseAddress(address)
	if err != nil {
		return "", "", "", fmt.Errorf("invalid account address: %w", err)
	}
	reference, endpoint, err := r.fixedStateEndpoint(ctx)
	if err != nil {
		return "", "", "", err
	}
	selector := canonicalSelector(reference)
	var code hexutil.Bytes
	if err := endpoint.CallContext(ctx, &code, "eth_getCode", parsed, selector); err != nil {
		r.Pool.ReportFailure(endpoint.Name)
		return "", "", "", stateUnavailable(err)
	}
	if err := r.confirmCanonical(ctx, endpoint, reference); err != nil {
		return "", "", "", err
	}
	kind, _ := classifyCode(code)
	return string(kind), strconv.FormatUint(reference.Number, 10), strings.ToLower(reference.Hash.Hex()), nil
}

// IsCanonical rechecks a compatibility state reference against the writer
// authority after PostgreSQL projection work has completed.
func (r *Reader) IsCanonical(ctx context.Context, blockNumber, blockHash string) (bool, error) {
	if r == nil || r.Canonical == nil {
		return false, CapabilityError{Code: "not_configured"}
	}
	number, err := strconv.ParseUint(blockNumber, 10, 64)
	if err != nil || strconv.FormatUint(number, 10) != blockNumber {
		return false, errors.New("invalid canonical state block number")
	}
	hash, err := ethrpc.ParseHash(blockHash)
	if err != nil || strings.ToLower(hash.Hex()) != blockHash {
		return false, errors.New("invalid canonical state block hash")
	}
	return r.Canonical.IsCanonical(ctx, CanonicalRef{Number: number, Hash: hash})
}

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
	parsed := make([]common.Address, len(addresses))
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
	results := make([]hexutil.Big, len(parsed))
	elements := make([]rpc.BatchElem, len(parsed))
	for index := range parsed {
		elements[index] = rpc.BatchElem{
			Method: "eth_getBalance", Args: []any{parsed[index], selector}, Result: &results[index],
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
	if err := r.confirmCanonical(ctx, endpoint, reference); err != nil {
		return nil, err
	}
	balances := make([]string, len(results))
	for index, result := range results {
		balances[index] = decimal(result)
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
	ownerBytes := ownerAddress.Bytes()
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

func (r *Reader) erc20Uint256Call(ctx context.Context, contract common.Address, callData []byte) (string, error) {
	reference, endpoint, err := r.fixedStateEndpoint(ctx)
	if err != nil {
		return "", err
	}
	selector := canonicalSelector(reference)
	call := map[string]any{"to": contract, "data": hexutil.Bytes(callData)}
	var result hexutil.Bytes
	if err := endpoint.CallContext(ctx, &result, "eth_call", call, selector); err != nil {
		r.Pool.ReportFailure(endpoint.Name)
		return "", stateUnavailable(err)
	}
	if err := r.confirmCanonical(ctx, endpoint, reference); err != nil {
		return "", err
	}
	if len(result) != 32 {
		return "", fmt.Errorf("decode fixed-block ERC-20 uint256 result")
	}
	return new(big.Int).SetBytes(result).String(), nil
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
		return fmt.Errorf("%w: canonical block changed during state query", publicquery.ErrNotReady)
	}
	r.Pool.ReportSuccess(endpoint.Name)
	return nil
}

func canonicalSelector(reference CanonicalRef) rpc.BlockNumberOrHash {
	return rpc.BlockNumberOrHashWithHash(reference.Hash, true)
}
