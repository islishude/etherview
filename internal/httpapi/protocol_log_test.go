package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/catalog"
)

func TestProtocolLogModelHasProtocolWithoutABIIdentity(t *testing.T) {
	model := transactionLogDecodingModel(catalog.TransactionLogDecoding{
		Status: "decoded", Protocol: "eip7708", EventName: "Transfer", Signature: "Transfer(address,address,uint256)",
		Arguments:   []catalog.TransactionLogArgument{{Name: "value", Type: "uint256", Value: "9007199254740993"}},
		Attribution: catalog.TransactionLogAttribution{Mode: "protocol", TracePath: []uint32{}},
	})
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"protocol":"eip7708"`, `"mode":"protocol"`, `"value":"9007199254740993"`, `"trace_path":[]`} {
		if !strings.Contains(string(raw), expected) {
			t.Fatalf("missing %s in %s", expected, raw)
		}
	}
	for _, absent := range []string{`"abi_source"`, `"execution_address"`, `"confidence"`} {
		if strings.Contains(string(raw), absent) {
			t.Fatalf("fabricated %s in %s", absent, raw)
		}
	}
}
