package ens

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

type sourceCaller struct {
	t       *testing.T
	chainID *big.Int
	genesis common.Hash
	block   BlockRef
	codes   map[common.Address][]byte
}

func (caller sourceCaller) CallContext(_ context.Context, result any, method string, args ...any) error {
	caller.t.Helper()
	switch method {
	case "eth_chainId":
		target := result.(*hexutil.Big)
		*target = hexutil.Big(*new(big.Int).Set(caller.chainID))
	case "eth_getBlockByNumber":
		target := result.(*blockHeader)
		tag := args[0].(string)
		if tag == "0x0" {
			target.Hash = caller.genesis
			number := new(big.Int)
			target.Number = (*hexutil.Big)(number)
			return nil
		}
		if tag != "finalized" {
			caller.t.Fatalf("block tag = %q", tag)
		}
		setTestHeader(target, caller.block)
	case "eth_getBlockByHash":
		if args[0].(common.Hash) != caller.block.Hash {
			caller.t.Fatalf("block hash = %v", args[0])
		}
		setTestHeader(result.(*blockHeader), caller.block)
	case "eth_getCode":
		address := args[0].(common.Address)
		target := result.(*hexutil.Bytes)
		*target = append((*target)[:0], caller.codes[address]...)
	default:
		caller.t.Fatalf("unexpected method %q", method)
	}
	return nil
}

func setTestHeader(header *blockHeader, reference BlockRef) {
	number := new(big.Int).SetUint64(reference.Number)
	header.Number = (*hexutil.Big)(number)
	header.Hash = reference.Hash
}

func TestFinalizedMainnetRefRequiresExactChainIdentity(t *testing.T) {
	reference := BlockRef{Number: 100, Hash: testBlockHash}
	caller := sourceCaller{
		t: t, chainID: big.NewInt(1), genesis: EthereumMainnetGenesisHash, block: reference,
	}
	got, err := FinalizedMainnetRef(t.Context(), caller)
	if err != nil || got != reference {
		t.Fatalf("reference = %+v, %v", got, err)
	}
	caller.genesis = common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	_, err = FinalizedMainnetRef(t.Context(), caller)
	var resolution *ResolutionError
	if !errors.As(err, &resolution) || resolution.Code != CodeSourceIdentity {
		t.Fatalf("identity error = %v", err)
	}
}

func TestVerifyCustomDeploymentRequiresCodeAtExactBlock(t *testing.T) {
	registry := common.HexToAddress("0x1111111111111111111111111111111111111111")
	universal := common.HexToAddress("0x2222222222222222222222222222222222222222")
	profile := Profile{
		Source: SourceCustom, Registry: registry, UniversalResolver: universal,
		CoinType: big.NewInt(60), Block: BlockRef{Number: 10, Hash: testBlockHash},
	}
	caller := sourceCaller{t: t, codes: map[common.Address][]byte{registry: {1}, universal: {2}}}
	if err := VerifyCustomDeployment(t.Context(), caller, profile); err != nil {
		t.Fatal(err)
	}
	caller.codes[universal] = nil
	err := VerifyCustomDeployment(t.Context(), caller, profile)
	var resolution *ResolutionError
	if !errors.As(err, &resolution) || resolution.Code != CodeCustomDeployment {
		t.Fatalf("deployment error = %v", err)
	}
}
