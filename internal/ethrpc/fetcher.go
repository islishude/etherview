package ethrpc

import (
	"context"
	"errors"
	"fmt"
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

func (f Fetcher) ByNumber(ctx context.Context, endpoint *Endpoint, number Quantity) (Bundle, error) {
	if _, err := ParseQuantity(number.String()); err != nil {
		return Bundle{}, err
	}
	bundle, err := f.fetch(ctx, endpoint, "eth_getBlockByNumber", []any{number.String(), true})
	if err != nil {
		return Bundle{}, err
	}
	actual, _ := bundle.Number()
	expected, _ := number.Uint64()
	if actual != expected {
		return Bundle{}, fmt.Errorf("RPC returned block %d for requested height %d", actual, expected)
	}
	return bundle, nil
}

func (f Fetcher) ByHash(ctx context.Context, endpoint *Endpoint, hash Hash) (Bundle, error) {
	if _, err := ParseHash(hash.String()); err != nil {
		return Bundle{}, err
	}
	bundle, err := f.fetch(ctx, endpoint, "eth_getBlockByHash", []any{hash.String(), true})
	if err != nil {
		return Bundle{}, err
	}
	actual, _ := bundle.BlockHash()
	if !actual.Equal(hash) {
		return Bundle{}, fmt.Errorf("RPC returned block %s for requested hash %s", actual, hash)
	}
	return bundle, nil
}

func (f Fetcher) fetch(ctx context.Context, endpoint *Endpoint, method string, params []any) (Bundle, error) {
	if endpoint == nil || endpoint.Client == nil {
		return Bundle{}, errors.New("fetch block: nil RPC endpoint")
	}
	var block *Block
	if err := endpoint.Client.Call(ctx, method, params, &block); err != nil {
		return Bundle{}, fmt.Errorf("fetch block from %q: %w", endpoint.Name, err)
	}
	if block == nil {
		return Bundle{}, fmt.Errorf("fetch block from %q: block not found", endpoint.Name)
	}
	if block.Hash == nil {
		return Bundle{}, fmt.Errorf("fetch block from %q: block hash is null", endpoint.Name)
	}
	receipts, err := f.fetchReceipts(ctx, endpoint, block)
	if err != nil {
		return Bundle{}, err
	}
	bundle := Bundle{Block: *block, Receipts: receipts}
	if err := ValidateBundle(bundle); err != nil {
		return Bundle{}, fmt.Errorf("validate block bundle from %q: %w", endpoint.Name, err)
	}
	return bundle, nil
}

func (f Fetcher) fetchReceipts(ctx context.Context, endpoint *Endpoint, block *Block) ([]Receipt, error) {
	strategy := f.ReceiptStrategy
	if strategy == "" {
		strategy = ReceiptStrategyAuto
	}
	if strategy != ReceiptStrategyTransactions && endpoint.Capabilities.Status(CapabilityBlockReceipts) != AvailabilityUnavailable {
		var receipts []Receipt
		err := endpoint.Client.Call(ctx, CapabilityBlockReceipts, []any{block.Hash.String()}, &receipts)
		if err == nil {
			return receipts, nil
		}
		if strategy == ReceiptStrategyBlockMethod || !IsMethodNotFound(err) {
			return nil, fmt.Errorf("fetch block receipts from %q: %w", endpoint.Name, err)
		}
	}
	return fetchTransactionReceipts(ctx, endpoint, block.Transactions, f.ReceiptBatchSize)
}

func fetchTransactionReceipts(ctx context.Context, endpoint *Endpoint, transactions []TransactionRef, batchSize int) ([]Receipt, error) {
	receiptPointers := make([]*Receipt, len(transactions))
	if batch, ok := endpoint.Client.(BatchCaller); ok && len(transactions) > 0 {
		if batchSize <= 0 {
			batchSize = 100
		}
		for start := 0; start < len(transactions); start += batchSize {
			end := start + batchSize
			if end > len(transactions) {
				end = len(transactions)
			}
			elements := make([]BatchElem, end-start)
			for index := start; index < end; index++ {
				elements[index-start] = BatchElem{
					Method: "eth_getTransactionReceipt",
					Params: []any{transactions[index].TransactionHash().String()},
					Result: &receiptPointers[index],
				}
			}
			if err := batch.BatchCall(ctx, elements); err != nil {
				return nil, fmt.Errorf("batch fetch transaction receipts from %q: %w", endpoint.Name, err)
			}
			for index := range elements {
				if elements[index].Error != nil {
					return nil, fmt.Errorf("fetch receipt %d from %q: %w", start+index, endpoint.Name, elements[index].Error)
				}
			}
		}
	} else {
		for index, transaction := range transactions {
			if err := endpoint.Client.Call(ctx, "eth_getTransactionReceipt", []any{transaction.TransactionHash().String()}, &receiptPointers[index]); err != nil {
				return nil, fmt.Errorf("fetch receipt %d from %q: %w", index, endpoint.Name, err)
			}
		}
	}
	receipts := make([]Receipt, len(receiptPointers))
	for index, receipt := range receiptPointers {
		if receipt == nil {
			return nil, fmt.Errorf("fetch receipt %d from %q: result is null", index, endpoint.Name)
		}
		receipts[index] = *receipt
	}
	return receipts, nil
}
