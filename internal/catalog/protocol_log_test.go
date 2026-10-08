package catalog

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/islishude/etherview/internal/nativetransfer"
)

func TestDecodeProtocolLog(t *testing.T) {
	from, to := common.HexToAddress("0x1234"), common.HexToAddress("0x5678")
	amount := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	for _, tc := range []struct {
		name                string
		mutate              func(*types.Log, *protocolLogEvidence)
		recognized, corrupt bool
		status              string
	}{
		{name: "published", recognized: true, status: "decoded"},
		{name: "pending", recognized: true, status: "unavailable", mutate: func(_ *types.Log, e *protocolLogEvidence) { e.published = false }},
		{name: "pre fork", mutate: func(_ *types.Log, e *protocolLogEvidence) { e.amsterdam = false }},
		{name: "erc20 emitter", mutate: func(l *types.Log, _ *protocolLogEvidence) { l.Address = from }},
		{name: "other topic", mutate: func(l *types.Log, _ *protocolLogEvidence) { l.Topics[0] = common.Hash{} }},
		{name: "topic count", recognized: true, corrupt: true, mutate: func(l *types.Log, _ *protocolLogEvidence) { l.Topics = l.Topics[:2] }},
		{name: "data length", recognized: true, corrupt: true, mutate: func(l *types.Log, _ *protocolLogEvidence) { l.Data = l.Data[:31] }},
		{name: "padding", recognized: true, corrupt: true, mutate: func(l *types.Log, _ *protocolLogEvidence) { l.Topics[1][0] = 1 }},
		{name: "zero", recognized: true, corrupt: true, mutate: func(l *types.Log, _ *protocolLogEvidence) { l.Data = make([]byte, 32) }},
		{name: "self", recognized: true, corrupt: true, mutate: func(l *types.Log, _ *protocolLogEvidence) { l.Topics[2] = l.Topics[1] }},
		{name: "amount mismatch", recognized: true, corrupt: true, mutate: func(_ *types.Log, e *protocolLogEvidence) { e.amount = "1" }},
		{name: "sender mismatch", recognized: true, corrupt: true, mutate: func(_ *types.Log, e *protocolLogEvidence) { e.from = to[:] }},
		{name: "recipient mismatch", recognized: true, corrupt: true, mutate: func(_ *types.Log, e *protocolLogEvidence) { e.to = from[:] }},
		{name: "missing published row", recognized: true, corrupt: true, mutate: func(_ *types.Log, e *protocolLogEvidence) { e.from = nil; e.to = nil; e.amount = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &types.Log{Address: params.SystemAddress, Topics: []common.Hash{nativetransfer.Topic, common.BytesToHash(from[:]), common.BytesToHash(to[:])}, Data: amount.FillBytes(make([]byte, 32))}
			evidence := protocolLogEvidence{amsterdam: true, published: true, from: from[:], to: to[:], amount: amount.String()}
			if tc.mutate != nil {
				tc.mutate(log, &evidence)
			}
			result, recognized, err := decodeProtocolLog(log, evidence)
			if recognized != tc.recognized || errors.Is(err, ErrCorruptData) != tc.corrupt || result.Status != tc.status {
				t.Fatalf("result=%+v recognized=%t err=%v", result, recognized, err)
			}
			if recognized && err == nil {
				if result.Protocol != "eip7708" || result.Attribution.Mode != "protocol" || result.ABISource != nil || result.Attribution.ExecutionAddress != "" || len(result.Attribution.TracePath) != 0 {
					t.Fatalf("fabricated provenance: %+v", result)
				}
				if result.Status == "decoded" && (len(result.Arguments) != 3 || result.Arguments[2].Value != amount.String() || !result.Arguments[0].Indexed || !result.Arguments[1].Indexed || result.Arguments[2].Indexed) {
					t.Fatalf("arguments=%+v", result.Arguments)
				}
			}
		})
	}
}
