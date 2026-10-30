package enssync

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/ethrpc"
)

type rpcSession struct {
	endpoint *ethrpc.Endpoint
	limits   CollectorOptions
}

func (s rpcSession) header(ctx context.Context, method string, arg any) (*types.Header, error) {
	var raw json.RawMessage
	if err := s.endpoint.CallContext(ctx, &raw, method, arg, false); err != nil {
		return nil, ErrRPCUnavailable
	}
	if len(raw) > 65536 {
		return nil, ErrCollectionLimit
	}
	header, err := chainbundle.DecodeHeader(raw)
	if err != nil || !header.Number.IsUint64() {
		return nil, ErrSourceIdentity
	}
	return header, nil
}
func (s rpcSession) verifyIdentity(ctx context.Context, m Manifest) error {
	var chain hexutil.Big
	if err := s.endpoint.CallContext(ctx, &chain, "eth_chainId"); err != nil {
		return ErrRPCUnavailable
	}
	if (*big.Int)(&chain).String() != m.SourceChainID {
		return ErrSourceIdentity
	}
	genesis, err := s.header(ctx, "eth_getBlockByHash", m.Genesis)
	if err != nil {
		return err
	}
	if genesis.Number.Sign() != 0 || genesis.Hash() != m.Genesis {
		return ErrSourceIdentity
	}
	return nil
}
func (s rpcSession) code(ctx context.Context, address common.Address, hash common.Hash) ([]byte, error) {
	var code hexutil.Bytes
	if err := s.endpoint.CallContext(ctx, &code, "eth_getCode", address, gethrpc.BlockNumberOrHashWithHash(hash, true)); err != nil {
		return nil, ErrRPCUnavailable
	}
	if len(code) == 0 || len(code) > 65536 {
		return nil, ErrUnsupportedDeployment
	}
	return code, nil
}
func (s rpcSession) verifyDeployment(ctx context.Context, d Deployment, target *types.Header) (*types.Header, error) {
	header, err := s.header(ctx, "eth_getBlockByHash", d.Hash)
	if err != nil {
		return nil, err
	}
	if header.Hash() != d.Hash || header.Number.Uint64() != d.Number {
		return nil, ErrSourceIdentity
	}
	for _, hash := range []common.Hash{d.Hash, target.Hash()} {
		if err := s.verifyCode(ctx, d, hash); err != nil {
			return nil, err
		}
	}
	return header, nil
}
func (s rpcSession) verifyCode(ctx context.Context, d Deployment, hash common.Hash) error {
	code, err := s.code(ctx, d.Address, hash)
	if err != nil {
		return err
	}
	if crypto.Keccak256Hash(code) != d.CodeHash {
		return ErrUnsupportedDeployment
	}
	if d.Implementation != (common.Address{}) {
		var slot hexutil.Bytes
		key := common.HexToHash("0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc")
		if err := s.endpoint.CallContext(ctx, &slot, "eth_getStorageAt", d.Address, key, gethrpc.BlockNumberOrHashWithHash(hash, true)); err != nil {
			return ErrRPCUnavailable
		}
		if len(slot) != 32 || !bytes.Equal(slot[:12], make([]byte, 12)) || common.BytesToAddress(slot) != d.Implementation {
			return ErrUnsupportedDeployment
		}
		implementation, err := s.code(ctx, d.Implementation, hash)
		if err != nil {
			return err
		}
		if crypto.Keccak256Hash(implementation) != d.ImplementationCodeHash {
			return ErrUnsupportedDeployment
		}
	}
	return nil
}
