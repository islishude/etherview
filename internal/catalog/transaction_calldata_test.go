package catalog

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"
)

func TestTransactionCalldataUsesFinalEIP7702ExecutionIdentity(t *testing.T) {
	delegateA := common.HexToAddress("0x1000000000000000000000000000000000000001")
	delegateB := common.HexToAddress("0x2000000000000000000000000000000000000002")
	wire, raw := catalogSetCodeTransaction(t, []common.Address{delegateA, delegateB})
	blockHash := bytesOf(0xaa, common.HashLength)
	codeHash := bytesOf(0xbb, common.HashLength)
	arguments := []byte(`[{"name":"value","type":"uint256","value":"42"}]`)
	abiJSON := []byte(`[{"type":"function","name":"setValue","inputs":[{"name":"value","type":"uint256"}],"outputs":[]}]`)
	contextAddress := *wire.To()

	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(4,
			[]driver.Value{"100", blockHash, int64(3), raw},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(2,
			[]driver.Value{"complete", int64(7)},
		)},
		catalogQueryStep{contains: "FROM transaction_execution_code_resolutions", rows: catalogRows(5,
			[]driver.Value{contextAddress[:], delegateB[:], codeHash, "eip7702_delegate", "prestate_tracer"},
		)},
		catalogQueryStep{
			contains: "decoding.object_kind = 'transaction_calldata'",
			check: func(arguments []driver.NamedValue) error {
				if got, ok := arguments[3].Value.([]byte); !ok || common.BytesToAddress(got) != delegateB {
					return errors.New("persisted decoding did not use final delegate")
				}
				return nil
			},
			rows: catalogRows(13, []driver.Value{
				"decoded", "setValue(uint256)", "verified", "verified",
				arguments, []byte(`[]`), "", delegateB[:], codeHash,
				delegateB[:], codeHash, "not_applicable", []byte(`[]`),
			}),
		},
		catalogQueryStep{contains: "WITH target_code AS", rows: catalogRows(9,
			[]driver.Value{codeHash, abiJSON, "verified", "exact_address", delegateB[:], codeHash, make([]byte, 32), "0", nil},
		)},
	)

	result, err := catalog.TransactionCalldata(context.Background(), "1", wire.Hash().Hex())
	if err != nil {
		t.Fatal(err)
	}
	if result.Execution.Resolution != "eip7702_delegate" || result.Execution.Address != delegateB.Hex() ||
		result.Decoding.Status != "decoded" || result.Decoding.Signature != "setValue(uint256)" ||
		len(result.Decoding.Inputs) != 1 || result.Decoding.Inputs[0].Value != "42" {
		t.Fatalf("calldata=%+v", result)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionCalldataUsesDelegatedExecutionForTypeTwoCall(t *testing.T) {
	contextAddress := common.HexToAddress("0x3000000000000000000000000000000000000003")
	delegate := common.HexToAddress("0x4000000000000000000000000000000000000004")
	wire, raw := catalogDynamicFeeTransactionWithData(t, contextAddress, []byte{0x3f, 0xa4, 0xf2, 0x45})
	blockHash := bytesOf(0xaa, common.HashLength)
	codeHash := bytesOf(0xcc, common.HashLength)
	abiJSON := []byte(`[{"type":"function","name":"value","inputs":[],"outputs":[]}]`)
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(4,
			[]driver.Value{"100", blockHash, int64(0), raw},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(2,
			[]driver.Value{"complete", int64(8)},
		)},
		catalogQueryStep{contains: "FROM transaction_execution_code_resolutions", rows: catalogRows(5,
			[]driver.Value{contextAddress[:], delegate[:], codeHash, "eip7702_delegate", "prestate_tracer"},
		)},
		catalogQueryStep{contains: "decoding.object_kind = 'transaction_calldata'", rows: catalogRows(13,
			[]driver.Value{
				"decoded", "value()", "verified", "verified", []byte(`[]`), []byte(`[]`), "",
				delegate[:], codeHash, delegate[:], codeHash, "not_applicable", []byte(`[]`),
			},
		)},
		catalogQueryStep{contains: "WITH target_code AS", rows: catalogRows(9,
			[]driver.Value{codeHash, abiJSON, "verified", "exact_address", delegate[:], codeHash, make([]byte, 32), "0", nil},
		)},
	)
	result, err := catalog.TransactionCalldata(context.Background(), "1", wire.Hash().Hex())
	if err != nil {
		t.Fatal(err)
	}
	if result.Execution.Address != delegate.Hex() || result.Decoding.Signature != "value()" {
		t.Fatalf("calldata=%+v", result)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionCalldataClearedBindingHasNoExecutableCode(t *testing.T) {
	wire, raw := catalogSetCodeTransaction(t, []common.Address{{}})
	blockHash := bytesOf(0xaa, common.HashLength)
	contextAddress := *wire.To()
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(4,
			[]driver.Value{"100", blockHash, int64(3), raw},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(2,
			[]driver.Value{"complete", int64(1)},
		)},
		catalogQueryStep{contains: "FROM transaction_execution_code_resolutions", rows: catalogRows(5,
			[]driver.Value{contextAddress[:], nil, nil, "empty", "prestate_tracer"},
		)},
	)
	result, err := catalog.TransactionCalldata(context.Background(), "1", wire.Hash().Hex())
	if err != nil {
		t.Fatal(err)
	}
	if result.Execution.Resolution != "empty" || result.Decoding.Status != "not_applicable" ||
		result.Execution.Address != "" || result.Execution.CodeHash != "" {
		t.Fatalf("calldata=%+v", result)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionCalldataRequiresExactStateDiffPublication(t *testing.T) {
	wire, raw := catalogDynamicFeeTransaction(t, common.HexToAddress("0x3000000000000000000000000000000000000003"))
	blockHash := bytesOf(0xaa, common.HashLength)
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(4,
			[]driver.Value{"100", blockHash, int64(0), raw},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(2)},
	)
	_, err := catalog.TransactionCalldata(context.Background(), "1", wire.Hash().Hex())
	var stageErr StageUnavailableError
	if !errors.As(err, &stageErr) || stageErr.Stage != StageStateDiff || stageErr.State != StageMissing {
		t.Fatalf("error=%#v", err)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionCalldataMissingExecutionResolutionFailsClosed(t *testing.T) {
	contextAddress := common.HexToAddress("0x3000000000000000000000000000000000000003")
	wire, raw := catalogDynamicFeeTransaction(t, contextAddress)
	blockHash := bytesOf(0xaa, common.HashLength)
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(4,
			[]driver.Value{"100", blockHash, int64(0), raw},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(2,
			[]driver.Value{"complete", int64(8)},
		)},
		catalogQueryStep{contains: "FROM transaction_execution_code_resolutions", rows: catalogRows(5)},
		catalogQueryStep{contains: "FROM verified_function_selector_sets AS indexed", rows: catalogRows(3)},
	)
	result, err := catalog.TransactionCalldata(context.Background(), "1", wire.Hash().Hex())
	if err != nil {
		t.Fatal(err)
	}
	if result.Execution.Resolution != "unavailable" || result.Execution.ContextAddress != contextAddress.Hex() ||
		result.Decoding.Status != "unavailable" || result.Execution.Address != "" || result.Execution.CodeHash != "" {
		t.Fatalf("calldata=%+v", result)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionCalldataUsesVerifiedAddressSelectorWhenExecutionResolutionIsMissing(t *testing.T) {
	contextAddress := common.HexToAddress("0x3000000000000000000000000000000000000003")
	input := append([]byte{0x55, 0x24, 0x10, 0x77}, make([]byte, 32)...)
	input[len(input)-1] = 42
	wire, raw := catalogDynamicFeeTransactionWithData(t, contextAddress, input)
	blockHash := bytesOf(0xaa, common.HashLength)
	codeHash := bytesOf(0xdd, common.HashLength)
	abiEntry := []byte(`{"type":"function","name":"setValue","inputs":[{"name":"value","type":"uint256"}],"outputs":[]}`)
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(4,
			[]driver.Value{"100", blockHash, int64(0), raw},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(2,
			[]driver.Value{"complete", int64(8)},
		)},
		catalogQueryStep{contains: "FROM transaction_execution_code_resolutions", rows: catalogRows(5)},
		catalogQueryStep{contains: "FROM verified_function_selector_sets AS indexed", rows: catalogRows(3,
			[]driver.Value{codeHash, "setValue(uint256)", abiEntry},
		)},
	)
	result, err := catalog.TransactionCalldata(context.Background(), "1", wire.Hash().Hex())
	if err != nil {
		t.Fatal(err)
	}
	if result.Execution.Resolution != "unavailable" || result.Decoding.Status != "decoded" ||
		result.Decoding.Signature != "setValue(uint256)" || result.Decoding.FunctionName != "setValue" ||
		result.Decoding.Confidence != "verified" || len(result.Decoding.Inputs) != 1 ||
		result.Decoding.Inputs[0].Name != "value" || result.Decoding.Inputs[0].Value != "42" {
		t.Fatalf("calldata=%+v", result)
	}
	if result.Decoding.ABISource == nil || result.Decoding.ABISource.Kind != "exact_address" ||
		result.Decoding.ABISource.Address != contextAddress.Hex() ||
		result.Decoding.ABISource.CodeHash != common.BytesToHash(codeHash).Hex() {
		t.Fatalf("ABI source=%+v", result.Decoding.ABISource)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionCalldataPreservesPublishedDecodingStates(t *testing.T) {
	tests := []struct {
		status     string
		candidates []byte
	}{
		{status: "ambiguous", candidates: []byte(`["first(uint256)","second(bytes32)"]`)},
		{status: "unknown", candidates: []byte(`[]`)},
		{status: "malformed", candidates: []byte(`[]`)},
	}
	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			contextAddress := common.HexToAddress("0x3000000000000000000000000000000000000003")
			wire, raw := catalogDynamicFeeTransaction(t, contextAddress)
			blockHash := bytesOf(0xaa, common.HashLength)
			codeHash := bytesOf(0xdd, common.HashLength)
			catalog, backend := openCatalog(t,
				catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(4,
					[]driver.Value{"100", blockHash, int64(0), raw},
				)},
				catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(2,
					[]driver.Value{"complete", int64(8)},
				)},
				catalogQueryStep{contains: "FROM transaction_execution_code_resolutions", rows: catalogRows(5,
					[]driver.Value{contextAddress[:], contextAddress[:], codeHash, "direct", "prestate_tracer"},
				)},
				catalogQueryStep{contains: "decoding.object_kind = 'transaction_calldata'", rows: catalogRows(13,
					[]driver.Value{
						test.status, nil, nil, "high", []byte(`[]`), test.candidates, "stable warning",
						contextAddress[:], codeHash, nil, nil, "not_applicable", []byte(`[]`),
					},
				)},
				catalogQueryStep{contains: "WITH target_code AS", rows: catalogRows(9)},
			)
			result, err := catalog.TransactionCalldata(context.Background(), "1", wire.Hash().Hex())
			if err != nil {
				t.Fatal(err)
			}
			if result.Execution.Resolution != "direct" || result.Decoding.Status != test.status {
				t.Fatalf("calldata=%+v", result)
			}
			if test.status == "ambiguous" && len(result.Decoding.Candidates) != 2 {
				t.Fatalf("candidates=%v", result.Decoding.Candidates)
			}
			assertCatalogConsumed(t, backend)
		})
	}
}

func TestTransactionCalldataUsesExactReadTimeABIWhenPublicationIsWeak(t *testing.T) {
	contextAddress := common.HexToAddress("0x3000000000000000000000000000000000000003")
	input := append([]byte{0x55, 0x24, 0x10, 0x77}, make([]byte, 32)...)
	input[len(input)-1] = 42
	wire, raw := catalogDynamicFeeTransactionWithData(t, contextAddress, input)
	blockHash := bytesOf(0xaa, common.HashLength)
	codeHash := bytesOf(0xee, common.HashLength)
	abiJSON := []byte(`[{"type":"function","name":"setValue","inputs":[{"name":"value","type":"uint256"}],"outputs":[]}]`)
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(4,
			[]driver.Value{"100", blockHash, int64(0), raw},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(2,
			[]driver.Value{"complete", int64(8)},
		)},
		catalogQueryStep{contains: "FROM transaction_execution_code_resolutions", rows: catalogRows(5,
			[]driver.Value{contextAddress[:], contextAddress[:], codeHash, "direct", "prestate_tracer"},
		)},
		catalogQueryStep{contains: "decoding.object_kind = 'transaction_calldata'", rows: catalogRows(13,
			[]driver.Value{
				"decoded", "setValue(uint256)", "signature_database", "guess",
				[]byte(`[{"name":"value","type":"uint256","value":"42"}]`), []byte(`[]`), "",
				contextAddress[:], codeHash, nil, nil, "not_applicable", []byte(`[]`),
			},
		)},
		catalogQueryStep{contains: "WITH target_code AS", rows: catalogRows(9,
			[]driver.Value{codeHash, abiJSON, "verified", "exact_address", contextAddress[:], codeHash, make([]byte, 32), "0", nil},
			[]driver.Value{codeHash, abiJSON, "signature_database", "signature_database", contextAddress[:], codeHash, make([]byte, 32), "0", nil},
		)},
	)
	result, err := catalog.TransactionCalldata(context.Background(), "1", wire.Hash().Hex())
	if err != nil {
		t.Fatal(err)
	}
	if result.Decoding.Status != "decoded" || result.Decoding.Signature != "setValue(uint256)" ||
		len(result.Decoding.Inputs) != 1 || result.Decoding.Inputs[0].Value != "42" {
		t.Fatalf("calldata=%+v", result)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionCalldataProjectsExactRecursiveParameterStructure(t *testing.T) {
	contextAddress := common.HexToAddress("0x3000000000000000000000000000000000000003")
	abiJSON := `[{"type":"function","name":"configure","inputs":[
		{"name":"config","type":"tuple","internalType":"struct Fixture.Config","components":[
			{"name":"owner","type":"address"},{"name":"","type":"uint16[]"}
		]},
		{"name":"matrix","type":"uint256[][2]"}
	],"outputs":[]}]`
	packingABI := strings.Replace(abiJSON, `{"name":"","type":"uint16[]"}`, `{"name":"values","type":"uint16[]"}`, 1)
	type config struct {
		Owner  common.Address
		Values []uint16
	}
	input := catalogABIInput(t, packingABI, "configure",
		config{Owner: contextAddress, Values: []uint16{7, 8}},
		[2][]*big.Int{{}, {big.NewInt(9), big.NewInt(10)}},
	)
	wire, raw := catalogDynamicFeeTransactionWithData(t, contextAddress, input)
	blockHash := bytesOf(0xaa, common.HashLength)
	codeHash := bytesOf(0xee, common.HashLength)
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(4,
			[]driver.Value{"100", blockHash, int64(0), raw},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(2,
			[]driver.Value{"complete", int64(8)},
		)},
		catalogQueryStep{contains: "FROM transaction_execution_code_resolutions", rows: catalogRows(5,
			[]driver.Value{contextAddress[:], contextAddress[:], codeHash, "direct", "prestate_tracer"},
		)},
		catalogQueryStep{contains: "decoding.object_kind = 'transaction_calldata'", rows: catalogRows(13)},
		catalogQueryStep{contains: "WITH target_code AS", rows: catalogRows(9,
			[]driver.Value{codeHash, []byte(abiJSON), "verified", "exact_address", contextAddress[:], codeHash, make([]byte, 32), "0", nil},
		)},
	)
	result, err := catalog.TransactionCalldata(context.Background(), "1", wire.Hash().Hex())
	if err != nil {
		t.Fatal(err)
	}
	if result.Decoding.Status != "decoded" || len(result.Decoding.Inputs) != 2 {
		t.Fatalf("calldata=%+v", result)
	}
	configInput := result.Decoding.Inputs[0]
	if configInput.InternalType != "struct Fixture.Config" || len(configInput.Components) != 2 ||
		configInput.Components[0].Name != "owner" || configInput.Components[0].Components == nil ||
		configInput.Components[1].Name != "" || configInput.Components[1].Type != "uint16[]" {
		t.Fatalf("config input=%+v", configInput)
	}
	if result.Decoding.Inputs[1].Type != "uint256[][2]" ||
		result.Decoding.Inputs[1].Components == nil || len(result.Decoding.Inputs[1].Components) != 0 {
		t.Fatalf("matrix input=%+v", result.Decoding.Inputs[1])
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionCalldataSelectorFallbackProjectsStoredABIEntryStructure(t *testing.T) {
	contextAddress := common.HexToAddress("0x3000000000000000000000000000000000000003")
	abiEntry := `{"type":"function","name":"setConfig","inputs":[{"name":"config","type":"tuple","internalType":"struct Fixture.Config","components":[{"name":"value","type":"uint256"},{"name":"owner","type":"address"}]}],"outputs":[]}`
	type config struct {
		Value *big.Int
		Owner common.Address
	}
	input := catalogABIInput(t, "["+abiEntry+"]", "setConfig", config{Value: big.NewInt(42), Owner: contextAddress})
	wire, raw := catalogDynamicFeeTransactionWithData(t, contextAddress, input)
	blockHash := bytesOf(0xaa, common.HashLength)
	codeHash := bytesOf(0xdd, common.HashLength)
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(4,
			[]driver.Value{"100", blockHash, int64(0), raw},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(2,
			[]driver.Value{"complete", int64(8)},
		)},
		catalogQueryStep{contains: "FROM transaction_execution_code_resolutions", rows: catalogRows(5)},
		catalogQueryStep{contains: "FROM verified_function_selector_sets AS indexed", rows: catalogRows(3,
			[]driver.Value{codeHash, "setConfig((uint256,address))", []byte(abiEntry)},
		)},
	)
	result, err := catalog.TransactionCalldata(context.Background(), "1", wire.Hash().Hex())
	if err != nil {
		t.Fatal(err)
	}
	if result.Decoding.Status != "decoded" || len(result.Decoding.Inputs) != 1 ||
		result.Decoding.Inputs[0].InternalType != "struct Fixture.Config" ||
		len(result.Decoding.Inputs[0].Components) != 2 ||
		result.Decoding.Inputs[0].Components[1].Name != "owner" {
		t.Fatalf("calldata=%+v", result)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionCalldataFailsClosedWhenPersistedDecodeSourceHasNoExactABI(t *testing.T) {
	contextAddress := common.HexToAddress("0x3000000000000000000000000000000000000003")
	input := append([]byte{0x55, 0x24, 0x10, 0x77}, make([]byte, 32)...)
	input[len(input)-1] = 42
	wire, raw := catalogDynamicFeeTransactionWithData(t, contextAddress, input)
	blockHash := bytesOf(0xaa, common.HashLength)
	codeHash := bytesOf(0xee, common.HashLength)
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(4,
			[]driver.Value{"100", blockHash, int64(0), raw},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(2,
			[]driver.Value{"complete", int64(8)},
		)},
		catalogQueryStep{contains: "FROM transaction_execution_code_resolutions", rows: catalogRows(5,
			[]driver.Value{contextAddress[:], contextAddress[:], codeHash, "direct", "prestate_tracer"},
		)},
		catalogQueryStep{contains: "decoding.object_kind = 'transaction_calldata'", rows: catalogRows(13,
			[]driver.Value{
				"decoded", "setValue(uint256)", "signature_database", "guess",
				[]byte(`[{"name":"value","type":"uint256","value":"42"}]`), []byte(`[]`), "",
				contextAddress[:], codeHash, nil, nil, "not_applicable", []byte(`[]`),
			},
		)},
		catalogQueryStep{contains: "WITH target_code AS", rows: catalogRows(9,
			[]driver.Value{
				codeHash,
				[]byte(`[{"type":"function","name":"setValue","inputs":[{"name":"value","type":"uint256"}],"outputs":[]}]`),
				"verified", "exact_address", contextAddress[:], codeHash, make([]byte, 32), "0", nil,
			},
		)},
	)
	_, err := catalog.TransactionCalldata(context.Background(), "1", wire.Hash().Hex())
	if !errors.Is(err, ErrCorruptData) {
		t.Fatalf("error=%v", err)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionCalldataFailsClosedOnSameSourceValueContradiction(t *testing.T) {
	contextAddress := common.HexToAddress("0x3000000000000000000000000000000000000003")
	input := append([]byte{0x55, 0x24, 0x10, 0x77}, make([]byte, 32)...)
	input[len(input)-1] = 42
	wire, raw := catalogDynamicFeeTransactionWithData(t, contextAddress, input)
	blockHash := bytesOf(0xaa, common.HashLength)
	codeHash := bytesOf(0xee, common.HashLength)
	abiJSON := []byte(`[{"type":"function","name":"setValue","inputs":[{"name":"value","type":"uint256"}],"outputs":[]}]`)
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(4,
			[]driver.Value{"100", blockHash, int64(0), raw},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(2,
			[]driver.Value{"complete", int64(8)},
		)},
		catalogQueryStep{contains: "FROM transaction_execution_code_resolutions", rows: catalogRows(5,
			[]driver.Value{contextAddress[:], contextAddress[:], codeHash, "direct", "prestate_tracer"},
		)},
		catalogQueryStep{contains: "decoding.object_kind = 'transaction_calldata'", rows: catalogRows(13,
			[]driver.Value{
				"decoded", "setValue(uint256)", "verified", "verified",
				[]byte(`[{"name":"value","type":"uint256","value":"41"}]`), []byte(`[]`), "",
				contextAddress[:], codeHash, contextAddress[:], codeHash, "not_applicable", []byte(`[]`),
			},
		)},
		catalogQueryStep{contains: "WITH target_code AS", rows: catalogRows(9,
			[]driver.Value{codeHash, abiJSON, "verified", "exact_address", contextAddress[:], codeHash, make([]byte, 32), "0", nil},
		)},
	)
	_, err := catalog.TransactionCalldata(context.Background(), "1", wire.Hash().Hex())
	if !errors.Is(err, ErrCorruptData) {
		t.Fatalf("error=%v", err)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionCalldataDecodesSelectorlessReceiveFromExactABI(t *testing.T) {
	contextAddress := common.HexToAddress("0x3000000000000000000000000000000000000003")
	wire, raw := catalogDynamicFeeTransactionWithData(t, contextAddress, nil)
	blockHash := bytesOf(0xaa, common.HashLength)
	codeHash := bytesOf(0xef, common.HashLength)
	abiJSON := []byte(`[{"type":"receive","stateMutability":"payable"}]`)
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(4,
			[]driver.Value{"100", blockHash, int64(0), raw},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(2,
			[]driver.Value{"complete", int64(8)},
		)},
		catalogQueryStep{contains: "FROM transaction_execution_code_resolutions", rows: catalogRows(5,
			[]driver.Value{contextAddress[:], contextAddress[:], codeHash, "direct", "prestate_tracer"},
		)},
		catalogQueryStep{contains: "decoding.object_kind = 'transaction_calldata'", rows: catalogRows(13)},
		catalogQueryStep{contains: "WITH target_code AS", rows: catalogRows(9,
			[]driver.Value{codeHash, abiJSON, "verified", "exact_address", contextAddress[:], codeHash, make([]byte, 32), "0", nil},
		)},
	)
	result, err := catalog.TransactionCalldata(context.Background(), "1", wire.Hash().Hex())
	if err != nil {
		t.Fatal(err)
	}
	if result.Input != "0x" || result.Decoding.Status != "decoded" ||
		result.Decoding.FunctionName != "receive" || result.Decoding.Signature != "receive()" ||
		result.Decoding.Confidence != "verified" || len(result.Decoding.Inputs) != 0 {
		t.Fatalf("calldata=%+v", result)
	}
	assertCatalogConsumed(t, backend)
}

func catalogSetCodeTransaction(t *testing.T, delegates []common.Address) (*types.Transaction, []byte) {
	t.Helper()
	authorityKey, err := crypto.HexToECDSA("1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	authority := crypto.PubkeyToAddress(authorityKey.PublicKey)
	authorizations := make([]types.SetCodeAuthorization, len(delegates))
	for index, delegate := range delegates {
		authorizations[index], err = types.SignSetCode(authorityKey, types.SetCodeAuthorization{
			ChainID: *uint256.NewInt(1), Address: delegate, Nonce: uint64(index),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	data := append([]byte{0x55, 0x24, 0x10, 0x77}, make([]byte, 32)...)
	data[len(data)-1] = 42
	wire := types.NewTx(&types.SetCodeTx{
		ChainID: uint256.NewInt(1), Nonce: 4, GasTipCap: uint256.NewInt(1),
		GasFeeCap: uint256.NewInt(2), Gas: 100_000, To: authority,
		Value: uint256.NewInt(0), Data: data,
		AuthList: authorizations,
	})
	return catalogSignAndEncodeTransaction(t, wire, 100, common.BytesToHash(bytesOf(0xaa, common.HashLength)), 3)
}

func catalogDynamicFeeTransaction(t *testing.T, to common.Address) (*types.Transaction, []byte) {
	return catalogDynamicFeeTransactionWithData(t, to, []byte{0x55, 0x24, 0x10, 0x77})
}

func catalogDynamicFeeTransactionWithData(t *testing.T, to common.Address, data []byte) (*types.Transaction, []byte) {
	t.Helper()
	wire := types.NewTx(&types.DynamicFeeTx{
		ChainID: big.NewInt(1), Nonce: 4, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2),
		Gas: 100_000, To: &to, Value: big.NewInt(0), Data: data,
	})
	return catalogSignAndEncodeTransaction(t, wire, 100, common.BytesToHash(bytesOf(0xaa, common.HashLength)), 0)
}

func catalogSignAndEncodeTransaction(
	t *testing.T,
	wire *types.Transaction,
	blockNumber uint64,
	blockHash common.Hash,
	transactionIndex uint64,
) (*types.Transaction, []byte) {
	t.Helper()
	key, err := crypto.HexToECDSA("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	signed, err := types.SignTx(wire, types.LatestSignerForChainID(big.NewInt(1)), key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := signed.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	value["blockHash"] = blockHash.Hex()
	value["blockNumber"] = fmt.Sprintf("0x%x", blockNumber)
	value["transactionIndex"] = "0x" + new(big.Int).SetUint64(transactionIndex).Text(16)
	value["from"] = crypto.PubkeyToAddress(key.PublicKey).Hex()
	encoded, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return signed, encoded
}

func catalogABIInput(t *testing.T, abiJSON, methodName string, values ...any) []byte {
	t.Helper()
	parsed, err := gethabi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		t.Fatal(err)
	}
	method, exists := parsed.Methods[methodName]
	if !exists {
		t.Fatalf("ABI method %s is missing", methodName)
	}
	payload, err := method.Inputs.Pack(values...)
	if err != nil {
		t.Fatal(err)
	}
	return append(append([]byte(nil), method.ID...), payload...)
}
