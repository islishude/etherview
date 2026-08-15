package catalog

import (
	"context"
	"database/sql/driver"
	"errors"
	"math/big"
	"strings"
	"testing"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestTransactionFailureDecodesBuiltinPanicWithoutContractABI(t *testing.T) {
	transactionHash := common.BytesToHash(bytesOf(0x11, common.HashLength))
	blockHash := bytesOf(0x22, common.HashLength)
	from := common.HexToAddress("0x1000000000000000000000000000000000000001")
	to := common.HexToAddress("0x2000000000000000000000000000000000000002")
	revertData := append(crypto.Keccak256([]byte("Panic(uint256)"))[:4], make([]byte, 32)...)
	revertData[len(revertData)-1] = 0x12
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(3,
			[]driver.Value{"100", blockHash, "3"},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(3,
			[]driver.Value{"complete", int64(7), int64(2)},
		)},
		catalogQueryStep{contains: "SELECT receipt.raw->>'status'", rows: catalogRows(1,
			[]driver.Value{"0x0"},
		)},
		catalogQueryStep{contains: "AND trace_path = ''", rows: catalogRows(18,
			failureRootRow(from, to, revertData, nil, nil, "unavailable"),
		)},
	)

	result, err := catalog.TransactionFailure(context.Background(), "1", transactionHash.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "execution reverted" || result.RevertData == nil ||
		*result.RevertData != "0x"+common.Bytes2Hex(revertData) ||
		result.Decoding.Status != "decoded" || result.Decoding.Signature != "Panic(uint256)" ||
		result.Decoding.Reason == nil || *result.Decoding.Reason != "division or modulo by zero" ||
		len(result.Decoding.Arguments) != 1 || result.Decoding.Arguments[0].Name != "code" ||
		result.Decoding.Arguments[0].Type != "uint256" || result.Decoding.Arguments[0].Value != "18" ||
		result.Decoding.ABISource == nil || result.Decoding.ABISource.Kind != "builtin" {
		t.Fatalf("failure=%+v", result)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionFailureProjectsExactCustomErrorShape(t *testing.T) {
	transactionHash := common.BytesToHash(bytesOf(0x33, common.HashLength))
	blockHash := bytesOf(0x44, common.HashLength)
	from := common.HexToAddress("0x3000000000000000000000000000000000000003")
	target := common.HexToAddress("0x4000000000000000000000000000000000000004")
	codeHash := bytesOf(0x55, common.HashLength)
	abiJSON := `[{"type":"error","name":"Complex","inputs":[
		{"name":"sender","type":"address"},
		{"name":"amount","type":"uint256"},
		{"name":"pair","type":"tuple","internalType":"struct Fixture.Pair","components":[
			{"name":"owner","type":"address"},{"name":"values","type":"uint16[]"}
		]},
		{"name":"items","type":"uint256[][]"}
	]}]`
	parsed, err := gethabi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		t.Fatal(err)
	}
	type pair struct {
		Owner  common.Address
		Values []uint16
	}
	errorABI := parsed.Errors["Complex"]
	payload, err := errorABI.Inputs.Pack(
		from, big.NewInt(42), pair{Owner: target, Values: []uint16{7, 8}},
		[][]*big.Int{{big.NewInt(9)}, {big.NewInt(10), big.NewInt(11), big.NewInt(12)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	revertData := append(append([]byte{}, errorABI.ID[:4]...), payload...)
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(3,
			[]driver.Value{"101", blockHash, "4"},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(3,
			[]driver.Value{"complete", int64(8), int64(3)},
		)},
		catalogQueryStep{contains: "SELECT receipt.raw->>'status'", rows: catalogRows(1,
			[]driver.Value{"0x0"},
		)},
		catalogQueryStep{contains: "AND trace_path = ''", rows: catalogRows(18,
			failureRootRow(from, target, revertData, target[:], codeHash, "direct"),
		)},
		catalogQueryStep{contains: "WITH target_code AS", rows: catalogRows(9,
			[]driver.Value{codeHash, []byte(abiJSON), "verified", "exact_address", target[:], codeHash, make([]byte, 32), "0", nil},
		)},
	)

	result, err := catalog.TransactionFailure(context.Background(), "1", transactionHash.Hex())
	if err != nil {
		t.Fatal(err)
	}
	arguments := result.Decoding.Arguments
	if result.Decoding.Status != "decoded" || result.Decoding.Signature != "Complex(address,uint256,(address,uint16[]),uint256[][])" ||
		result.Decoding.Reason != nil || len(arguments) != 4 || arguments[0].Name != "sender" ||
		arguments[1].Value != "42" || arguments[2].InternalType != "struct Fixture.Pair" ||
		len(arguments[2].Components) != 2 || arguments[2].Components[1].Type != "uint16[]" ||
		arguments[3].Type != "uint256[][]" || arguments[3].Components == nil ||
		result.Decoding.ABISource == nil || result.Decoding.ABISource.Kind != "exact_address" {
		t.Fatalf("failure=%+v", result)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionFailureRejectsSuccessfulReceipt(t *testing.T) {
	transactionHash := common.BytesToHash(bytesOf(0x66, common.HashLength))
	blockHash := bytesOf(0x77, common.HashLength)
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(3,
			[]driver.Value{"102", blockHash, "5"},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(3,
			[]driver.Value{"complete", int64(9), int64(4)},
		)},
		catalogQueryStep{contains: "SELECT receipt.raw->>'status'", rows: catalogRows(1,
			[]driver.Value{"0x1"},
		)},
	)
	_, err := catalog.TransactionFailure(context.Background(), "1", transactionHash.Hex())
	if !errors.Is(err, ErrNotApplicable) {
		t.Fatalf("error=%v", err)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionFailureDoesNotGuessCustomErrorFromSignatureDatabase(t *testing.T) {
	transactionHash := common.BytesToHash(bytesOf(0x88, common.HashLength))
	blockHash := bytesOf(0x99, common.HashLength)
	from := common.HexToAddress("0x5000000000000000000000000000000000000005")
	target := common.HexToAddress("0x6000000000000000000000000000000000000006")
	codeHash := bytesOf(0xaa, common.HashLength)
	abiJSON := `[{"type":"error","name":"Unauthorized","inputs":[{"name":"caller","type":"address"}]}]`
	selector := crypto.Keccak256([]byte("Unauthorized(address)"))[:4]
	revertData := append(append([]byte(nil), selector...), common.LeftPadBytes(from[:], 32)...)
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(3,
			[]driver.Value{"103", blockHash, "6"},
		)},
		catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(3,
			[]driver.Value{"complete", int64(10), int64(5)},
		)},
		catalogQueryStep{contains: "SELECT receipt.raw->>'status'", rows: catalogRows(1,
			[]driver.Value{"0x0"},
		)},
		catalogQueryStep{contains: "AND trace_path = ''", rows: catalogRows(18,
			failureRootRow(from, target, revertData, target[:], codeHash, "direct"),
		)},
		catalogQueryStep{contains: "WITH target_code AS", rows: catalogRows(9,
			[]driver.Value{codeHash, []byte(abiJSON), "signature_database", "selector_guess", target[:], codeHash, make([]byte, 32), "0", nil},
		)},
	)

	result, err := catalog.TransactionFailure(context.Background(), "1", transactionHash.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if result.Decoding.Status != "unknown" || result.Decoding.Signature != "" ||
		len(result.Decoding.Arguments) != 0 || result.Decoding.ABISource != nil {
		t.Fatalf("signature database custom failure was guessed: %+v", result.Decoding)
	}
	assertCatalogConsumed(t, backend)
}

func failureRootRow(
	from, to common.Address,
	output, executionAddress, codeHash []byte,
	resolution string,
) []driver.Value {
	return []driver.Value{
		"", nil, int64(0), "CALL", from[:], to[:], nil,
		"0", "100000", "21000", []byte{}, output, "execution reverted", true, true,
		executionAddress, codeHash, resolution,
	}
}
