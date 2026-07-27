package ethrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chainbundle"
)

type ReceiptStrategy string

const (
	ReceiptStrategyAuto         ReceiptStrategy = "auto"
	ReceiptStrategyBlockMethod  ReceiptStrategy = "block_method"
	ReceiptStrategyTransactions ReceiptStrategy = "transactions"
)

type Fetcher struct {
	ReceiptStrategy  ReceiptStrategy
	ReceiptBatchSize int
}

func (f Fetcher) ByNumber(ctx context.Context, endpoint *Endpoint, number uint64) (chainbundle.Bundle, error) {
	bundle, err := f.fetch(ctx, endpoint, "eth_getBlockByNumber", hexutil.EncodeUint64(number), true)
	if err != nil {
		return chainbundle.Bundle{}, err
	}
	if actual := bundle.Block.NumberU64(); actual != number {
		return chainbundle.Bundle{}, fmt.Errorf("RPC returned block %d for requested height %d", actual, number)
	}
	return bundle, nil
}

func (f Fetcher) ByHash(ctx context.Context, endpoint *Endpoint, hash common.Hash) (chainbundle.Bundle, error) {
	bundle, err := f.fetch(ctx, endpoint, "eth_getBlockByHash", hash, true)
	if err != nil {
		return chainbundle.Bundle{}, err
	}
	if actual := bundle.Block.Hash(); actual != hash {
		return chainbundle.Bundle{}, fmt.Errorf("RPC returned block %s for requested hash %s", actual.Hex(), hash.Hex())
	}
	return bundle, nil
}

func (f Fetcher) fetch(ctx context.Context, endpoint *Endpoint, method string, params ...any) (chainbundle.Bundle, error) {
	if endpoint == nil || endpoint.Client == nil {
		return chainbundle.Bundle{}, errors.New("fetch block: nil RPC endpoint")
	}
	var rawBlock json.RawMessage
	if err := endpoint.CallContext(ctx, &rawBlock, method, params...); err != nil {
		return chainbundle.Bundle{}, fmt.Errorf("fetch block from %q: %w", endpoint.Name, SanitizeError(err))
	}
	if len(bytes.TrimSpace(rawBlock)) == 0 || bytes.Equal(bytes.TrimSpace(rawBlock), []byte("null")) {
		return chainbundle.Bundle{}, fmt.Errorf("fetch block from %q: block not found", endpoint.Name)
	}
	header, err := chainbundle.DecodeHeader(rawBlock)
	if err != nil {
		return chainbundle.Bundle{}, fmt.Errorf("decode block header from %q: %w", endpoint.Name, err)
	}
	uncleHashes, err := chainbundle.UncleHashes(rawBlock)
	if err != nil {
		return chainbundle.Bundle{}, fmt.Errorf("decode block from %q: %w", endpoint.Name, err)
	}
	rawUncles, err := fetchUncles(ctx, endpoint, header.Hash(), uncleHashes)
	if err != nil {
		return chainbundle.Bundle{}, err
	}
	bundle, err := chainbundle.DecodeBlock(rawBlock, rawUncles)
	if err != nil {
		return chainbundle.Bundle{}, fmt.Errorf("decode block from %q: %w", endpoint.Name, err)
	}
	rawReceipts, err := f.fetchReceipts(ctx, endpoint, bundle.Block)
	if err != nil {
		return chainbundle.Bundle{}, err
	}
	bundle, err = bundle.WithReceipts(rawReceipts)
	if err != nil {
		return chainbundle.Bundle{}, fmt.Errorf("validate block bundle from %q: %w", endpoint.Name, err)
	}
	return bundle, nil
}

func fetchUncles(
	ctx context.Context,
	endpoint *Endpoint,
	blockHash common.Hash,
	hashes []common.Hash,
) ([]json.RawMessage, error) {
	if len(hashes) == 0 {
		return []json.RawMessage{}, nil
	}
	raws := make([]json.RawMessage, len(hashes))
	elements := make([]rpc.BatchElem, len(hashes))
	for index := range hashes {
		elements[index] = rpc.BatchElem{
			Method: "eth_getUncleByBlockHashAndIndex",
			Args:   []any{blockHash, hexutil.EncodeUint64(uint64(index))},
			Result: &raws[index],
		}
	}
	if err := endpoint.BatchCallContext(ctx, elements); err != nil {
		return nil, fmt.Errorf("batch fetch uncle headers from %q: %w", endpoint.Name, SanitizeError(err))
	}
	for index := range elements {
		if elements[index].Error != nil {
			return nil, fmt.Errorf("fetch uncle %d from %q: %w", index, endpoint.Name, SanitizeError(elements[index].Error))
		}
		if len(bytes.TrimSpace(raws[index])) == 0 || bytes.Equal(bytes.TrimSpace(raws[index]), []byte("null")) {
			return nil, fmt.Errorf("fetch uncle %d from %q: result is null", index, endpoint.Name)
		}
		header, err := chainbundle.DecodeHeader(raws[index])
		if err != nil {
			return nil, fmt.Errorf("decode uncle %d from %q: %w", index, endpoint.Name, err)
		}
		if header.Hash() != hashes[index] {
			return nil, fmt.Errorf("decode uncle %d from %q: hash does not match block reference", index, endpoint.Name)
		}
	}
	return raws, nil
}

func (f Fetcher) fetchReceipts(
	ctx context.Context,
	endpoint *Endpoint,
	block *types.Block,
) ([]json.RawMessage, error) {
	strategy := f.ReceiptStrategy
	if strategy == "" {
		strategy = ReceiptStrategyAuto
	}
	switch strategy {
	case ReceiptStrategyAuto, ReceiptStrategyBlockMethod, ReceiptStrategyTransactions:
	default:
		return nil, fmt.Errorf("unsupported receipt strategy %q", strategy)
	}
	tryBlockMethod := strategy == ReceiptStrategyBlockMethod ||
		strategy == ReceiptStrategyAuto &&
			endpoint.Capabilities.Status(CapabilityBlockReceipts) != AvailabilityUnavailable
	if tryBlockMethod {
		var raw json.RawMessage
		err := endpoint.CallContext(ctx, &raw, CapabilityBlockReceipts, block.Hash())
		if err == nil {
			receipts, decodeErr := decodeRawArray(raw)
			if decodeErr != nil {
				return nil, fmt.Errorf("decode block receipts from %q: %w", endpoint.Name, decodeErr)
			}
			return receipts, nil
		}
		if strategy == ReceiptStrategyBlockMethod || !IsMethodNotFound(err) {
			return nil, fmt.Errorf("fetch block receipts from %q: %w", endpoint.Name, SanitizeError(err))
		}
	}
	return fetchTransactionReceipts(ctx, endpoint, block.Transactions(), f.ReceiptBatchSize)
}

func fetchTransactionReceipts(
	ctx context.Context,
	endpoint *Endpoint,
	transactions types.Transactions,
	batchSize int,
) ([]json.RawMessage, error) {
	raws := make([]json.RawMessage, len(transactions))
	if batchSize <= 0 {
		batchSize = 100
	}
	for start := 0; start < len(transactions); start += batchSize {
		end := min(start+batchSize, len(transactions))
		elements := make([]rpc.BatchElem, end-start)
		for index := start; index < end; index++ {
			elements[index-start] = rpc.BatchElem{
				Method: "eth_getTransactionReceipt",
				Args:   []any{transactions[index].Hash()},
				Result: &raws[index],
			}
		}
		if err := endpoint.BatchCallContext(ctx, elements); err != nil {
			return nil, fmt.Errorf("batch fetch transaction receipts from %q: %w", endpoint.Name, SanitizeError(err))
		}
		for index := range elements {
			if elements[index].Error != nil {
				return nil, fmt.Errorf("fetch receipt %d from %q: %w", start+index, endpoint.Name, SanitizeError(elements[index].Error))
			}
			raw := bytes.TrimSpace(raws[start+index])
			if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
				return nil, fmt.Errorf("fetch receipt %d from %q: result is null", start+index, endpoint.Name)
			}
		}
	}
	return raws, nil
}

func decodeRawArray(raw json.RawMessage) ([]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, chainbundle.ErrInvalidWireValue
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, chainbundle.ErrInvalidWireValue
	}
	return values, nil
}
