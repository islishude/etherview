package enssync

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"sort"

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
func (s rpcSession) byNumber(ctx context.Context, number uint64) (*types.Header, error) {
	h, err := s.header(ctx, "eth_getBlockByNumber", hexutil.EncodeUint64(number))
	if err != nil {
		return nil, err
	}
	if h.Number.Uint64() != number {
		return nil, ErrSourceIdentity
	}
	return h, nil
}
func (s rpcSession) target(ctx context.Context, m Manifest) (*types.Header, error) {
	var chain hexutil.Big
	if err := s.endpoint.CallContext(ctx, &chain, "eth_chainId"); err != nil {
		return nil, ErrRPCUnavailable
	}
	if (*big.Int)(&chain).String() != m.SourceChainID {
		return nil, ErrSourceIdentity
	}
	genesis, err := s.byNumber(ctx, 0)
	if err != nil {
		return nil, err
	}
	if genesis.Hash() != m.Genesis {
		return nil, ErrSourceIdentity
	}
	target, err := s.header(ctx, "eth_getBlockByNumber", "finalized")
	if err != nil {
		return nil, err
	}
	exact, err := s.header(ctx, "eth_getBlockByHash", target.Hash())
	if err != nil {
		return nil, err
	}
	if exact.Hash() != target.Hash() {
		return nil, ErrSourceIdentity
	}
	return target, nil
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
	header, err := s.byNumber(ctx, d.Number)
	if err != nil {
		return nil, err
	}
	if header.Hash() != d.Hash {
		return nil, ErrSourceIdentity
	}
	for _, hash := range []common.Hash{d.Hash, target.Hash()} {
		code, err := s.code(ctx, d.Address, hash)
		if err != nil {
			return nil, err
		}
		if crypto.Keccak256Hash(code) != d.CodeHash {
			return nil, ErrUnsupportedDeployment
		}
		if d.Implementation != (common.Address{}) {
			var slot hexutil.Bytes
			key := common.HexToHash("0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc")
			if err := s.endpoint.CallContext(ctx, &slot, "eth_getStorageAt", d.Address, key, gethrpc.BlockNumberOrHashWithHash(hash, true)); err != nil {
				return nil, ErrRPCUnavailable
			}
			if len(slot) != 32 || !bytes.Equal(slot[:12], make([]byte, 12)) || common.BytesToAddress(slot) != d.Implementation {
				return nil, ErrUnsupportedDeployment
			}
			implementation, err := s.code(ctx, d.Implementation, hash)
			if err != nil {
				return nil, err
			}
			if crypto.Keccak256Hash(implementation) != d.ImplementationCodeHash {
				return nil, ErrUnsupportedDeployment
			}
		}
	}
	return header, nil
}
func (s rpcSession) block(ctx context.Context, m Manifest, number uint64) (collectedBlock, error) {
	header, err := s.byNumber(ctx, number)
	if err != nil {
		return collectedBlock{}, err
	}
	addresses := []common.Address{}
	for _, d := range m.Deployments {
		if d.Number <= number {
			addresses = append(addresses, d.Address)
		}
	}
	block := collectedBlock{Header: header}
	if len(addresses) == 0 {
		return block, nil
	}
	var raw json.RawMessage
	filter := map[string]any{"blockHash": header.Hash(), "address": addresses}
	if m.hasV2() {
		delete(filter, "address")
	}
	if err := s.endpoint.CallContext(ctx, &raw, "eth_getLogs", filter); err != nil {
		return block, ErrRPCUnavailable
	}
	if len(raw) > s.limits.MaxBytes {
		return block, ErrCollectionLimit
	}
	var logs []json.RawMessage
	if err := json.Unmarshal(raw, &logs); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return block, ErrInvalidEvent
	}
	if len(logs) > s.limits.MaxLogs {
		return block, ErrCollectionLimit
	}
	for _, encoded := range logs {
		var entry types.Log
		if err := json.Unmarshal(encoded, &entry); err != nil {
			return block, ErrInvalidEvent
		}
		decoded, err := chainbundle.DecodeLog(encoded, entry.TxHash, header.Hash(), number, uint64(entry.TxIndex), uint64(entry.Index))
		if err != nil {
			return block, ErrInvalidEvent
		}
		block.Logs = append(block.Logs, *decoded)
	}
	sort.Slice(block.Logs, func(i, j int) bool { return block.Logs[i].Index < block.Logs[j].Index })
	return block, nil
}
