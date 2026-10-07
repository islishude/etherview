// Package nativetransfer recognizes the execution protocol's EIP-7708 logs.
package nativetransfer

import (
	"bytes"
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

var Topic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

type Transfer struct {
	From, To common.Address
	Amount   string
}

// Parse is called only with authenticated post-Amsterdam receipts. The boolean
// distinguishes unrelated logs from malformed protocol transfers.
func Parse(log *types.Log) (Transfer, bool, error) {
	if log == nil || log.Address != params.SystemAddress || len(log.Topics) == 0 || log.Topics[0] != Topic {
		return Transfer{}, false, nil
	}
	if len(log.Topics) != 3 || len(log.Data) != 32 || !bytes.Equal(log.Topics[1][:12], make([]byte, 12)) || !bytes.Equal(log.Topics[2][:12], make([]byte, 12)) {
		return Transfer{}, true, errors.New("invalid native transfer log layout")
	}
	value := new(big.Int).SetBytes(log.Data)
	result := Transfer{From: common.BytesToAddress(log.Topics[1][12:]), To: common.BytesToAddress(log.Topics[2][12:]), Amount: value.String()}
	if value.Sign() == 0 || result.From == result.To {
		return Transfer{}, true, errors.New("invalid native transfer value or participants")
	}
	return result, true, nil
}
