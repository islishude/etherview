package ethrpc

import (
	"encoding/json"
	"fmt"
)

func testHash(value byte) Hash {
	hash, err := ParseHash(fmt.Sprintf("0x%064x", value))
	if err != nil {
		panic(err)
	}
	return hash
}

func testAddress(value byte) Address {
	address, err := ParseAddress(fmt.Sprintf("0x%040x", value))
	if err != nil {
		panic(err)
	}
	return address
}

func testBundle(number uint64, hash, parent Hash, transactionCount int) Bundle {
	quantityNumber := QuantityFromUint64(number)
	zeroHash := testHash(0)
	block := Block{
		Number:           &quantityNumber,
		Hash:             new(hash),
		ParentHash:       parent,
		Sha3Uncles:       zeroHash,
		TransactionsRoot: zeroHash,
		StateRoot:        zeroHash,
		ReceiptsRoot:     zeroHash,
		ExtraData:        Data("0x"),
		GasLimit:         QuantityFromUint64(30_000_000),
		GasUsed:          QuantityFromUint64(21_000 * uint64(transactionCount)),
		Timestamp:        QuantityFromUint64(1_700_000_000 + number),
		Uncles:           []Hash{},
	}
	bundle := Bundle{Block: block, Receipts: make([]Receipt, transactionCount)}
	status := QuantityFromUint64(1)
	for index := range transactionCount {
		txHash := testHash(byte(number*16 + uint64(index) + 1))
		txIndex := QuantityFromUint64(uint64(index))
		typeValue := QuantityFromUint64(uint64(index % 5))
		tx := &Transaction{
			Hash:             txHash,
			Type:             &typeValue,
			BlockHash:        new(hash),
			BlockNumber:      new(quantityNumber),
			TransactionIndex: new(txIndex),
			From:             testAddress(1),
			To:               new(testAddress(2)),
			Nonce:            QuantityFromUint64(uint64(index)),
			Gas:              QuantityFromUint64(21_000),
			Value:            QuantityFromUint64(0),
			Input:            Data("0x"),
		}
		bundle.Block.Transactions = append(bundle.Block.Transactions, TransactionRef{Hash: txHash, Transaction: tx})
		bundle.Receipts[index] = Receipt{
			TransactionHash:   txHash,
			TransactionIndex:  txIndex,
			BlockHash:         hash,
			BlockNumber:       quantityNumber,
			CumulativeGasUsed: QuantityFromUint64(21_000 * uint64(index+1)),
			Logs:              []Log{},
			LogsBloom:         Data("0x"),
			Status:            new(status),
		}
	}
	return bundle
}

func assignJSON(destination any, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, destination)
}
