package ethrpc

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestQuantityPreservesUint256(t *testing.T) {
	t.Parallel()
	const maximum = "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	quantity, err := ParseQuantity(maximum)
	if err != nil {
		t.Fatal(err)
	}
	value, err := quantity.Big()
	if err != nil {
		t.Fatal(err)
	}
	if value.BitLen() != 256 {
		t.Fatalf("bit length = %d, want 256", value.BitLen())
	}
	if _, err := quantity.Uint64(); !errors.Is(err, ErrInvalidQuantity) {
		t.Fatalf("Uint64 error = %v, want ErrInvalidQuantity", err)
	}
}

func TestQuantityRejectsNonCanonicalForms(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"0x", "0x00", "0X1", "0xA", "1"} {
		if _, err := ParseQuantity(input); !errors.Is(err, ErrInvalidQuantity) {
			t.Errorf("ParseQuantity(%q) error = %v", input, err)
		}
	}
}

func TestWireModelsPreserveUnknownFieldsAndTransactionTypes(t *testing.T) {
	t.Parallel()
	hash := testHash(8)
	blockHash := testHash(9)
	payload := `{
		"hash":"` + hash.String() + `",
		"type":"0x7f",
		"blockHash":"` + blockHash.String() + `",
		"blockNumber":"0x1",
		"transactionIndex":"0x0",
		"from":"` + testAddress(1).String() + `",
		"to":null,
		"nonce":"0x0",
		"gas":"0x5208",
		"value":"0x0",
		"input":"0x",
		"futureEnvelope":{"version":6,"opaque":"kept"}
	}`
	var transaction Transaction
	if err := json.Unmarshal([]byte(payload), &transaction); err != nil {
		t.Fatal(err)
	}
	typeNumber, err := transaction.Type.Uint64()
	if err != nil || typeNumber != 127 {
		t.Fatalf("transaction type = %d, %v", typeNumber, err)
	}
	if _, exists := transaction.Extra["futureEnvelope"]; !exists {
		t.Fatal("futureEnvelope was not retained")
	}
	roundTrip, err := json.Marshal(transaction)
	if err != nil {
		t.Fatal(err)
	}
	var roundTripObject map[string]json.RawMessage
	if err := json.Unmarshal(roundTrip, &roundTripObject); err != nil {
		t.Fatal(err)
	}
	var futureEnvelope struct {
		Version int    `json:"version"`
		Opaque  string `json:"opaque"`
	}
	if err := json.Unmarshal(roundTripObject["futureEnvelope"], &futureEnvelope); err != nil {
		t.Fatalf("round-trip JSON lost unknown field: %s", roundTrip)
	}
	if futureEnvelope.Version != 6 || futureEnvelope.Opaque != "kept" {
		t.Fatalf("round-trip future envelope = %+v", futureEnvelope)
	}
}

func TestWireModelsPreserveTransactionTypesZeroThroughFour(t *testing.T) {
	t.Parallel()
	address := testAddress(1).String()
	blockHash := testHash(9).String()
	blobHash := testHash(10).String()
	tests := []struct {
		name      string
		typeValue string
		fields    string
		keys      []string
	}{
		{name: "legacy", typeValue: "0x0", fields: `"gasPrice":"0x3b9aca00","v":"0x25","r":"0x1","s":"0x2"`, keys: []string{"gasPrice", "v", "r", "s"}},
		{name: "access-list", typeValue: "0x1", fields: `"chainId":"0x1","gasPrice":"0x3b9aca00","accessList":[{"address":"` + address + `","storageKeys":[]}],"yParity":"0x1"`, keys: []string{"chainId", "gasPrice", "accessList", "yParity"}},
		{name: "dynamic-fee", typeValue: "0x2", fields: `"chainId":"0x1","maxPriorityFeePerGas":"0x1","maxFeePerGas":"0x2","accessList":[]`, keys: []string{"chainId", "maxPriorityFeePerGas", "maxFeePerGas", "accessList"}},
		{name: "blob", typeValue: "0x3", fields: `"chainId":"0x1","maxPriorityFeePerGas":"0x1","maxFeePerGas":"0x2","maxFeePerBlobGas":"0x3","blobVersionedHashes":["` + blobHash + `"]`, keys: []string{"maxFeePerBlobGas", "blobVersionedHashes"}},
		{name: "set-code", typeValue: "0x4", fields: `"chainId":"0x1","maxPriorityFeePerGas":"0x1","maxFeePerGas":"0x2","authorizationList":[{"chainId":"0x1","address":"` + address + `","nonce":"0x0","yParity":"0x1","r":"0x1","s":"0x2"}]`, keys: []string{"authorizationList"}},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload := `{"hash":"` + testHash(byte(index+20)).String() + `","type":"` + test.typeValue + `","blockHash":"` + blockHash + `","blockNumber":"0x1","transactionIndex":"0x0","from":"` + address + `","to":null,"nonce":"0x0","gas":"0x5208","value":"0x0","input":"0x",` + test.fields + `}`
			var transaction Transaction
			if err := json.Unmarshal([]byte(payload), &transaction); err != nil {
				t.Fatal(err)
			}
			typeNumber, err := transaction.Type.Uint64()
			if err != nil || typeNumber != uint64(index) {
				t.Fatalf("transaction type=%d error=%v", typeNumber, err)
			}
			roundTrip, err := json.Marshal(transaction)
			if err != nil {
				t.Fatal(err)
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(roundTrip, &object); err != nil {
				t.Fatal(err)
			}
			for _, key := range test.keys {
				value, exists := object[key]
				if !exists || string(value) == "null" {
					t.Fatalf("type %s lost %s: %s", test.typeValue, key, roundTrip)
				}
			}
		})
	}
}

func TestWireModelsPreservePoWWithdrawalAndBlobEraBlockFields(t *testing.T) {
	t.Parallel()
	payload := `{
		"number":"0x1","hash":"` + testHash(1).String() + `","parentHash":"` + testHash(0).String() + `",
		"nonce":"0x0000000000000001","sha3Uncles":"` + testHash(2).String() + `","logsBloom":"0x00",
		"transactionsRoot":"` + testHash(3).String() + `","stateRoot":"` + testHash(4).String() + `","receiptsRoot":"` + testHash(5).String() + `",
		"miner":"` + testAddress(2).String() + `","difficulty":"0x2","totalDifficulty":"0x3","extraData":"0x",
		"size":"0x1","gasLimit":"0x1c9c380","gasUsed":"0x5208","timestamp":"0x1",
		"baseFeePerGas":"0x7","withdrawalsRoot":"` + testHash(6).String() + `","blobGasUsed":"0x20000","excessBlobGas":"0x40000",
		"parentBeaconBlockRoot":"` + testHash(7).String() + `","requestsHash":"` + testHash(8).String() + `",
		"transactions":[],"uncles":[],"withdrawals":[{"index":"0x0","validatorIndex":"0x1","address":"` + testAddress(3).String() + `","amount":"0x2","futureWithdrawal":"kept"}],
		"futureBlock":{"opaque":true}
	}`
	var block Block
	if err := json.Unmarshal([]byte(payload), &block); err != nil {
		t.Fatal(err)
	}
	if block.Nonce == nil || block.Difficulty == nil || block.TotalDifficulty == nil || block.Miner == nil ||
		block.WithdrawalsRoot == nil || block.BlobGasUsed == nil || block.ExcessBlobGas == nil ||
		block.ParentBeaconRoot == nil || block.RequestsHash == nil || len(block.Withdrawals) != 1 {
		t.Fatalf("block lost era-specific fields: %+v", block)
	}
	if _, exists := block.Extra["futureBlock"]; !exists {
		t.Fatal("future block field was not retained")
	}
	if _, exists := block.Withdrawals[0].Extra["futureWithdrawal"]; !exists {
		t.Fatal("future withdrawal field was not retained")
	}
	roundTrip, err := json.Marshal(block)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"\"nonce\"", "\"difficulty\"", "\"totalDifficulty\"", "\"withdrawals\"", "\"blobGasUsed\""} {
		if !strings.Contains(string(roundTrip), expected) {
			t.Fatalf("round-trip block lost %s: %s", expected, roundTrip)
		}
	}
}

func TestTransactionReferenceAcceptsHashAndObject(t *testing.T) {
	t.Parallel()
	hash := testHash(1)
	var reference TransactionRef
	if err := json.Unmarshal([]byte(`"`+hash.String()+`"`), &reference); err != nil {
		t.Fatal(err)
	}
	if reference.IsFull() || !reference.TransactionHash().Equal(hash) {
		t.Fatalf("unexpected hash reference: %+v", reference)
	}
}

func TestQuantityUint64Boundary(t *testing.T) {
	t.Parallel()
	quantity, err := ParseQuantity("0xffffffffffffffff")
	if err != nil {
		t.Fatal(err)
	}
	value, err := quantity.Uint64()
	if err != nil || value != math.MaxUint64 {
		t.Fatalf("value = %d, error = %v", value, err)
	}
}
