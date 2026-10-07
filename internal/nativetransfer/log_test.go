package nativetransfer

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"math/big"
	"testing"
)

func TestParseProtocolTransfers(t *testing.T) {
	for _, tc := range []struct {
		name                string
		mutate              func(*types.Log)
		recognized, invalid bool
	}{
		{"valid", func(*types.Log) {}, true, false},
		{"token emitter", func(l *types.Log) { l.Address = common.HexToAddress("0x1234") }, false, false},
		{"other topic", func(l *types.Log) { l.Topics[0] = common.Hash{} }, false, false},
		{"missing topic", func(l *types.Log) { l.Topics = l.Topics[:2] }, true, true},
		{"short data", func(l *types.Log) { l.Data = l.Data[:31] }, true, true},
		{"padded address", func(l *types.Log) { l.Topics[1][0] = 1 }, true, true},
		{"zero", func(l *types.Log) { l.Data = make([]byte, 32) }, true, true},
		{"self", func(l *types.Log) { l.Topics[2] = l.Topics[1] }, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
			l := &types.Log{Address: params.SystemAddress, Topics: []common.Hash{Topic, common.HexToHash("0x1"), common.HexToHash("0x2")}, Data: max.FillBytes(make([]byte, 32))}
			tc.mutate(l)
			got, recognized, err := Parse(l)
			if recognized != tc.recognized || (err != nil) != tc.invalid {
				t.Fatalf("recognized=%t err=%v", recognized, err)
			}
			if recognized && err == nil && got.Amount != max.String() {
				t.Fatalf("lost amount precision: %s", got.Amount)
			}
		})
	}
}
