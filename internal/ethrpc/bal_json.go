package ethrpc

import (
	"encoding/json"
	"math"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rlp"
)

// These structs describe execution-apis JSON transport, whose field names
// differ from Geth's BAL encoding JSON. RLP decoding owns protocol semantics.
type balJSONChange struct {
	Index *hexutil.Uint64 `json:"index"`
	Value *hexutil.Big    `json:"value"`
}
type balJSONAccount struct {
	Address        *common.Address `json:"address"`
	BalanceChanges []balJSONChange `json:"balanceChanges"`
	NonceChanges   []balJSONChange `json:"nonceChanges"`
	CodeChanges    []struct {
		Index *hexutil.Uint64 `json:"index"`
		Code  *hexutil.Bytes  `json:"code"`
	} `json:"codeChanges"`
	StorageReads   []*hexutil.Big `json:"storageReads"`
	StorageChanges []struct {
		Key     *hexutil.Big `json:"key"`
		Changes []struct {
			Index *hexutil.Uint64 `json:"index"`
			Value *hexutil.Big    `json:"value"`
		} `json:"changes"`
	} `json:"storageChanges"`
}

func encodeBALJSON(raw json.RawMessage) ([]byte, error) {
	var accounts []balJSONAccount
	if json.Unmarshal(raw, &accounts) != nil || accounts == nil {
		return nil, ErrBALInvalid
	}
	encoded := make([]any, 0, len(accounts))
	validIndex := func(index *hexutil.Uint64) bool { return index != nil && uint64(*index) <= math.MaxUint32 }
	changes := func(input []balJSONChange, bits int) ([]any, error) {
		out := make([]any, 0, len(input))
		for _, c := range input {
			if !validIndex(c.Index) || c.Value == nil || (*big.Int)(c.Value).Sign() < 0 || (*big.Int)(c.Value).BitLen() > bits {
				return nil, ErrBALInvalid
			}
			out = append(out, []any{uint64(*c.Index), (*big.Int)(c.Value)})
		}
		return out, nil
	}
	for _, account := range accounts {
		if account.Address == nil {
			return nil, ErrBALInvalid
		}
		balances, err := changes(account.BalanceChanges, 256)
		if err != nil {
			return nil, err
		}
		nonces, err := changes(account.NonceChanges, 64)
		if err != nil {
			return nil, err
		}
		codes := make([]any, 0, len(account.CodeChanges))
		for _, c := range account.CodeChanges {
			if !validIndex(c.Index) || c.Code == nil {
				return nil, ErrBALInvalid
			}
			codes = append(codes, []any{uint64(*c.Index), []byte(*c.Code)})
		}
		reads := make([]any, 0, len(account.StorageReads))
		for _, key := range account.StorageReads {
			if !validBALWord(key) {
				return nil, ErrBALInvalid
			}
			reads = append(reads, (*big.Int)(key))
		}
		writes := make([]any, 0, len(account.StorageChanges))
		for _, slot := range account.StorageChanges {
			if !validBALWord(slot.Key) || len(slot.Changes) == 0 {
				return nil, ErrBALInvalid
			}
			values := make([]any, 0, len(slot.Changes))
			for _, c := range slot.Changes {
				if !validIndex(c.Index) || !validBALWord(c.Value) {
					return nil, ErrBALInvalid
				}
				values = append(values, []any{uint64(*c.Index), (*big.Int)(c.Value)})
			}
			writes = append(writes, []any{(*big.Int)(slot.Key), values})
		}
		encoded = append(encoded, []any{account.Address.Bytes(), writes, reads, balances, nonces, codes})
	}
	return rlp.EncodeToBytes(encoded)
}

func validBALWord(value *hexutil.Big) bool {
	return value != nil && (*big.Int)(value).Sign() >= 0 && (*big.Int)(value).BitLen() <= 256
}
