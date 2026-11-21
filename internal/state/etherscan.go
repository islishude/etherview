package state

import (
	"context"
	"errors"
	"fmt"

	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/ethrpc"
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
	reference, err := r.stateReference(ctx)
	if err != nil {
		return "", "", "", err
	}
	input, err := r.accountInput(ctx, reference, accountCode, parsed, nil)
	if err != nil {
		return "", "", "", err
	}
	kind, _ := classifyCode(input.Code)
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
// address. Published balances are read first; only missing proof triggers an
// EIP-1898 batch and persistence of exact baselines.
func (r *Reader) NativeBalances(ctx context.Context, addresses []string) ([]string, error) {
	if r == nil || r.Canonical == nil {
		return nil, CapabilityError{Code: "not_configured"}
	}
	if len(addresses) == 0 || len(addresses) > maxBalanceCandidates {
		return nil, errors.New("invalid native balance batch size")
	}
	parsed := make([]common.Address, len(addresses))
	for i, a := range addresses {
		value, err := ethrpc.ParseAddress(a)
		if err != nil {
			return nil, errors.New("invalid native balance address")
		}
		parsed[i] = value
	}
	reference, err := r.Canonical.Tip(ctx)
	if err != nil {
		return nil, err
	}
	if r.BalanceDemand != nil {
		return r.BalanceDemand.Read(ctx, reference, parsed)
	}
	return r.nativeStateBatch(ctx, reference, parsed, nil, nil)
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
	return r.erc20Uint256Call(ctx, contractAddress, tokenBalance, ownerAddress[:])
}

// ERC20TotalSupply returns totalSupply() at one fixed canonical block.
func (r *Reader) ERC20TotalSupply(ctx context.Context, contract string) (string, error) {
	contractAddress, err := ethrpc.ParseAddress(contract)
	if err != nil {
		return "", fmt.Errorf("invalid token contract address: %w", err)
	}
	return r.erc20Uint256Call(ctx, contractAddress, tokenSupply, nil)
}

func (r *Reader) erc20Uint256Call(ctx context.Context, contract common.Address, op string, arg []byte) (string, error) {
	reference, err := r.stateReference(ctx)
	if err != nil {
		return "", err
	}
	input, err := r.accountInput(ctx, reference, op, contract, arg)
	return input.TokenValue, err
}

func (r *Reader) stateReference(ctx context.Context) (CanonicalRef, error) {
	if r == nil || r.Canonical == nil || (r.Pool == nil && r.BalanceDemand == nil) {
		return CanonicalRef{}, CapabilityError{Code: "not_configured"}
	}
	return r.Canonical.Tip(ctx)
}

func canonicalSelector(reference CanonicalRef) rpc.BlockNumberOrHash {
	return rpc.BlockNumberOrHashWithHash(reference.Hash, true)
}
