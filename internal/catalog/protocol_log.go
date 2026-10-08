package catalog

import (
	"bytes"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/nativetransfer"
)

type protocolLogEvidence struct {
	amsterdam, published bool
	from, to             []byte
	amount               string
}

// Protocol provenance comes only from authenticated post-fork receipts and the
// matching atomically published native transfer stage in this read snapshot.
func decodeProtocolLog(log *types.Log, evidence protocolLogEvidence) (TransactionLogDecoding, bool, error) {
	if !evidence.amsterdam {
		return TransactionLogDecoding{}, false, nil
	}
	transfer, recognized, err := nativetransfer.Parse(log)
	if !recognized {
		return TransactionLogDecoding{}, false, nil
	}
	if err != nil {
		return TransactionLogDecoding{}, true, ErrCorruptData
	}
	result := emptyLogDecoding("unavailable", "protocol transfer publication is unavailable")
	result.Protocol = "eip7708"
	result.Attribution = TransactionLogAttribution{Mode: "protocol", TracePath: []uint32{}}
	if !evidence.published {
		return result, true, nil
	}
	if !bytes.Equal(evidence.from, transfer.From[:]) || !bytes.Equal(evidence.to, transfer.To[:]) || evidence.amount != transfer.Amount {
		return TransactionLogDecoding{}, true, ErrCorruptData
	}
	result.Status, result.Warning = "decoded", ""
	result.EventName, result.Signature = "Transfer", "Transfer(address,address,uint256)"
	result.Arguments = []TransactionLogArgument{
		{Name: "from", Type: "address", Indexed: true, Value: transfer.From.Hex()},
		{Name: "to", Type: "address", Indexed: true, Value: transfer.To.Hex()},
		{Name: "value", Type: "uint256", Value: transfer.Amount},
	}
	return result, true, nil
}
