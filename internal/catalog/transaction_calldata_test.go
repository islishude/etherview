package catalog

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"testing"

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
	wire, raw := catalogDynamicFeeTransaction(t, contextAddress)
	blockHash := bytesOf(0xaa, common.HashLength)
	codeHash := bytesOf(0xcc, common.HashLength)
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
		catalogQueryStep{contains: "decoding.object_kind = 'transaction_calldata'", rows: catalogRows(13)},
		catalogQueryStep{contains: "WITH target_code AS", rows: catalogRows(9,
			[]driver.Value{codeHash, abiJSON, "verified", "exact_address", contextAddress[:], codeHash, make([]byte, 32), "0", nil},
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
	wire := types.NewTx(&types.SetCodeTx{
		ChainID: uint256.NewInt(1), Nonce: 4, GasTipCap: uint256.NewInt(1),
		GasFeeCap: uint256.NewInt(2), Gas: 100_000, To: authority,
		Value: uint256.NewInt(0), Data: append([]byte{0x55, 0x24, 0x10, 0x77}, make([]byte, 32)...),
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
