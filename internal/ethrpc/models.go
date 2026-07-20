package ethrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// TransactionRef preserves the two shapes accepted by eth_getBlockBy*: a
// transaction hash or a full transaction object. Core ingestion requires the
// latter, while probes and lightweight reads can still decode the former.
type TransactionRef struct {
	Hash        Hash
	Transaction *Transaction
}

func (r TransactionRef) IsFull() bool { return r.Transaction != nil }

func (r TransactionRef) TransactionHash() Hash {
	if r.Transaction != nil {
		return r.Transaction.Hash
	}
	return r.Hash
}

func (r *TransactionRef) UnmarshalJSON(data []byte) error {
	if r == nil {
		return errors.New("ethrpc.TransactionRef: UnmarshalJSON on nil receiver")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return errors.New("decode transaction reference: empty JSON")
	}
	if data[0] == '"' {
		var hash Hash
		if err := json.Unmarshal(data, &hash); err != nil {
			return err
		}
		*r = TransactionRef{Hash: hash}
		return nil
	}
	if data[0] != '{' {
		return fmt.Errorf("decode transaction reference: expected string or object")
	}
	var tx Transaction
	if err := json.Unmarshal(data, &tx); err != nil {
		return err
	}
	*r = TransactionRef{Hash: tx.Hash, Transaction: &tx}
	return nil
}

func (r TransactionRef) MarshalJSON() ([]byte, error) {
	if r.Transaction != nil {
		return json.Marshal(r.Transaction)
	}
	return json.Marshal(r.Hash)
}

type Block struct {
	Number           *Quantity                  `json:"number"`
	Hash             *Hash                      `json:"hash"`
	ParentHash       Hash                       `json:"parentHash"`
	Nonce            *Data                      `json:"nonce,omitempty"`
	Sha3Uncles       Hash                       `json:"sha3Uncles"`
	LogsBloom        *Data                      `json:"logsBloom,omitempty"`
	TransactionsRoot Hash                       `json:"transactionsRoot"`
	StateRoot        Hash                       `json:"stateRoot"`
	ReceiptsRoot     Hash                       `json:"receiptsRoot"`
	Miner            *Address                   `json:"miner,omitempty"`
	Difficulty       *Quantity                  `json:"difficulty,omitempty"`
	TotalDifficulty  *Quantity                  `json:"totalDifficulty,omitempty"`
	ExtraData        Data                       `json:"extraData"`
	Size             *Quantity                  `json:"size,omitempty"`
	GasLimit         Quantity                   `json:"gasLimit"`
	GasUsed          Quantity                   `json:"gasUsed"`
	Timestamp        Quantity                   `json:"timestamp"`
	BaseFeePerGas    *Quantity                  `json:"baseFeePerGas,omitempty"`
	WithdrawalsRoot  *Hash                      `json:"withdrawalsRoot,omitempty"`
	BlobGasUsed      *Quantity                  `json:"blobGasUsed,omitempty"`
	ExcessBlobGas    *Quantity                  `json:"excessBlobGas,omitempty"`
	ParentBeaconRoot *Hash                      `json:"parentBeaconBlockRoot,omitempty"`
	RequestsHash     *Hash                      `json:"requestsHash,omitempty"`
	Transactions     []TransactionRef           `json:"transactions"`
	Uncles           []Hash                     `json:"uncles"`
	Withdrawals      []Withdrawal               `json:"withdrawals,omitempty"`
	Extra            map[string]json.RawMessage `json:"-"`
}

type Transaction struct {
	Hash                 Hash                       `json:"hash"`
	Type                 *Quantity                  `json:"type,omitempty"`
	BlockHash            *Hash                      `json:"blockHash,omitempty"`
	BlockNumber          *Quantity                  `json:"blockNumber,omitempty"`
	TransactionIndex     *Quantity                  `json:"transactionIndex,omitempty"`
	From                 Address                    `json:"from"`
	To                   *Address                   `json:"to"`
	Nonce                Quantity                   `json:"nonce"`
	Gas                  Quantity                   `json:"gas"`
	GasPrice             *Quantity                  `json:"gasPrice,omitempty"`
	MaxPriorityFeePerGas *Quantity                  `json:"maxPriorityFeePerGas,omitempty"`
	MaxFeePerGas         *Quantity                  `json:"maxFeePerGas,omitempty"`
	Value                Quantity                   `json:"value"`
	Input                Data                       `json:"input"`
	ChainID              *Quantity                  `json:"chainId,omitempty"`
	AccessList           json.RawMessage            `json:"accessList,omitempty"`
	MaxFeePerBlobGas     *Quantity                  `json:"maxFeePerBlobGas,omitempty"`
	BlobVersionedHashes  []Hash                     `json:"blobVersionedHashes,omitempty"`
	AuthorizationList    json.RawMessage            `json:"authorizationList,omitempty"`
	V                    *Quantity                  `json:"v,omitempty"`
	R                    *Quantity                  `json:"r,omitempty"`
	S                    *Quantity                  `json:"s,omitempty"`
	YParity              *Quantity                  `json:"yParity,omitempty"`
	Extra                map[string]json.RawMessage `json:"-"`
}

type Receipt struct {
	TransactionHash   Hash                       `json:"transactionHash"`
	TransactionIndex  Quantity                   `json:"transactionIndex"`
	BlockHash         Hash                       `json:"blockHash"`
	BlockNumber       Quantity                   `json:"blockNumber"`
	From              *Address                   `json:"from,omitempty"`
	To                *Address                   `json:"to,omitempty"`
	CumulativeGasUsed Quantity                   `json:"cumulativeGasUsed"`
	GasUsed           *Quantity                  `json:"gasUsed,omitempty"`
	ContractAddress   *Address                   `json:"contractAddress,omitempty"`
	Logs              []Log                      `json:"logs"`
	LogsBloom         Data                       `json:"logsBloom"`
	Root              *Data                      `json:"root,omitempty"`
	Status            *Quantity                  `json:"status,omitempty"`
	Type              *Quantity                  `json:"type,omitempty"`
	EffectiveGasPrice *Quantity                  `json:"effectiveGasPrice,omitempty"`
	BlobGasUsed       *Quantity                  `json:"blobGasUsed,omitempty"`
	BlobGasPrice      *Quantity                  `json:"blobGasPrice,omitempty"`
	Extra             map[string]json.RawMessage `json:"-"`
}

type Log struct {
	Removed          bool                       `json:"removed"`
	LogIndex         *Quantity                  `json:"logIndex,omitempty"`
	TransactionIndex *Quantity                  `json:"transactionIndex,omitempty"`
	TransactionHash  *Hash                      `json:"transactionHash,omitempty"`
	BlockHash        *Hash                      `json:"blockHash,omitempty"`
	BlockNumber      *Quantity                  `json:"blockNumber,omitempty"`
	Address          Address                    `json:"address"`
	Data             Data                       `json:"data"`
	Topics           []Hash                     `json:"topics"`
	Extra            map[string]json.RawMessage `json:"-"`
}

type Withdrawal struct {
	Index          Quantity                   `json:"index"`
	ValidatorIndex Quantity                   `json:"validatorIndex"`
	Address        Address                    `json:"address"`
	Amount         Quantity                   `json:"amount"`
	Extra          map[string]json.RawMessage `json:"-"`
}

type Bundle struct {
	Block    Block
	Receipts []Receipt
}

func (b Bundle) Number() (uint64, error) {
	if b.Block.Number == nil {
		return 0, errors.New("block number is null")
	}
	return b.Block.Number.Uint64()
}

func (b Bundle) BlockHash() (Hash, error) {
	if b.Block.Hash == nil {
		return "", errors.New("block hash is null")
	}
	return *b.Block.Hash, nil
}

func (b *Block) UnmarshalJSON(data []byte) error {
	type plain Block
	return unmarshalObjectWithExtra(data, (*plain)(b), &b.Extra, blockKnownFields)
}

func (b Block) MarshalJSON() ([]byte, error) {
	type plain Block
	return marshalObjectWithExtra(plain(b), b.Extra)
}

func (t *Transaction) UnmarshalJSON(data []byte) error {
	type plain Transaction
	return unmarshalObjectWithExtra(data, (*plain)(t), &t.Extra, transactionKnownFields)
}

func (t Transaction) MarshalJSON() ([]byte, error) {
	type plain Transaction
	return marshalObjectWithExtra(plain(t), t.Extra)
}

func (r *Receipt) UnmarshalJSON(data []byte) error {
	type plain Receipt
	return unmarshalObjectWithExtra(data, (*plain)(r), &r.Extra, receiptKnownFields)
}

func (r Receipt) MarshalJSON() ([]byte, error) {
	type plain Receipt
	return marshalObjectWithExtra(plain(r), r.Extra)
}

func (l *Log) UnmarshalJSON(data []byte) error {
	type plain Log
	return unmarshalObjectWithExtra(data, (*plain)(l), &l.Extra, logKnownFields)
}

func (l Log) MarshalJSON() ([]byte, error) {
	type plain Log
	return marshalObjectWithExtra(plain(l), l.Extra)
}

func (w *Withdrawal) UnmarshalJSON(data []byte) error {
	type plain Withdrawal
	return unmarshalObjectWithExtra(data, (*plain)(w), &w.Extra, withdrawalKnownFields)
}

func (w Withdrawal) MarshalJSON() ([]byte, error) {
	type plain Withdrawal
	return marshalObjectWithExtra(plain(w), w.Extra)
}

func unmarshalObjectWithExtra(data []byte, destination any, extra *map[string]json.RawMessage, known []string) error {
	if err := json.Unmarshal(data, destination); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range known {
		delete(fields, name)
	}
	if len(fields) == 0 {
		fields = nil
	}
	*extra = fields
	return nil
}

func marshalObjectWithExtra(known any, extra map[string]json.RawMessage) ([]byte, error) {
	data, err := json.Marshal(known)
	if err != nil {
		return nil, err
	}
	if len(extra) == 0 {
		return data, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for name, value := range extra {
		if _, exists := fields[name]; exists {
			continue
		}
		fields[name] = append(json.RawMessage(nil), value...)
	}
	return json.Marshal(fields)
}

var blockKnownFields = []string{
	"number", "hash", "parentHash", "nonce", "sha3Uncles", "logsBloom",
	"transactionsRoot", "stateRoot", "receiptsRoot", "miner", "difficulty",
	"totalDifficulty", "extraData", "size", "gasLimit", "gasUsed", "timestamp",
	"baseFeePerGas", "withdrawalsRoot", "blobGasUsed", "excessBlobGas",
	"parentBeaconBlockRoot", "requestsHash", "transactions", "uncles", "withdrawals",
}

var transactionKnownFields = []string{
	"hash", "type", "blockHash", "blockNumber", "transactionIndex", "from", "to",
	"nonce", "gas", "gasPrice", "maxPriorityFeePerGas", "maxFeePerGas", "value",
	"input", "chainId", "accessList", "maxFeePerBlobGas", "blobVersionedHashes",
	"authorizationList", "v", "r", "s", "yParity",
}

var receiptKnownFields = []string{
	"transactionHash", "transactionIndex", "blockHash", "blockNumber", "from", "to",
	"cumulativeGasUsed", "gasUsed", "contractAddress", "logs", "logsBloom", "root",
	"status", "type", "effectiveGasPrice", "blobGasUsed", "blobGasPrice",
}

var logKnownFields = []string{
	"removed", "logIndex", "transactionIndex", "transactionHash", "blockHash",
	"blockNumber", "address", "data", "topics",
}

var withdrawalKnownFields = []string{"index", "validatorIndex", "address", "amount"}
