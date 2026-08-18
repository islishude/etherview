package ens

import (
	"context"
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

var EthereumMainnetGenesisHash = common.HexToHash(
	"0xd4e56740f876aef8c010b86a40d5f56745a118d0906a34e69aec8c0db1cb8fa3",
)

type blockHeader struct {
	Number *hexutil.Big `json:"number"`
	Hash   common.Hash  `json:"hash"`
}

// FinalizedMainnetRef proves the auxiliary endpoint is Ethereum Mainnet and
// returns one exact finalized block identity suitable for all calls in a
// resolution generation.
func FinalizedMainnetRef(ctx context.Context, caller RPCCaller) (BlockRef, error) {
	if caller == nil {
		return BlockRef{}, errors.New("ENS mainnet RPC caller is nil")
	}
	var chainID hexutil.Big
	if err := caller.CallContext(ctx, &chainID, "eth_chainId"); err != nil {
		return BlockRef{}, resolutionError(CodeRPCUnavailable)
	}
	if (*big.Int)(&chainID).Cmp(big.NewInt(1)) != 0 {
		return BlockRef{}, resolutionError(CodeSourceIdentity)
	}
	var genesis blockHeader
	if err := caller.CallContext(ctx, &genesis, "eth_getBlockByNumber", "0x0", false); err != nil {
		return BlockRef{}, resolutionError(CodeRPCUnavailable)
	}
	if genesis.Hash != EthereumMainnetGenesisHash {
		return BlockRef{}, resolutionError(CodeSourceIdentity)
	}
	var finalized blockHeader
	if err := caller.CallContext(ctx, &finalized, "eth_getBlockByNumber", "finalized", false); err != nil {
		return BlockRef{}, resolutionError(CodeRPCUnavailable)
	}
	reference, err := decodeBlockHeader(finalized)
	if err != nil {
		return BlockRef{}, resolutionError(CodeInvalidResponse)
	}
	var exact blockHeader
	if err := caller.CallContext(ctx, &exact, "eth_getBlockByHash", reference.Hash, false); err != nil {
		return BlockRef{}, resolutionError(CodeRPCUnavailable)
	}
	confirmed, err := decodeBlockHeader(exact)
	if err != nil || confirmed != reference {
		return BlockRef{}, resolutionError(CodeSourceIdentity)
	}
	return reference, nil
}

func decodeBlockHeader(header blockHeader) (BlockRef, error) {
	if header.Number == nil || header.Hash == (common.Hash{}) {
		return BlockRef{}, errors.New("block header identity is missing")
	}
	number := (*big.Int)(header.Number)
	if !number.IsUint64() {
		return BlockRef{}, errors.New("block number exceeds uint64")
	}
	return BlockRef{Number: number.Uint64(), Hash: header.Hash}, nil
}

func VerifyCustomDeployment(ctx context.Context, caller RPCCaller, profile Profile) error {
	if err := validateProfile(caller, profile); err != nil {
		return err
	}
	if profile.Source != SourceCustom {
		return errors.New("custom ENS profile is required")
	}
	selector := gethrpc.BlockNumberOrHashWithHash(profile.Block.Hash, true)
	for _, address := range []common.Address{profile.Registry, profile.UniversalResolver} {
		var code hexutil.Bytes
		if err := caller.CallContext(ctx, &code, "eth_getCode", address, selector); err != nil {
			return resolutionError(CodeRPCUnavailable)
		}
		if len(code) == 0 {
			return resolutionError(CodeCustomDeployment)
		}
	}
	return nil
}
