package enrich

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
)

func TestStatsV2AllowsExactNonZeroConfiguredStartWithoutParent(t *testing.T) {
	t.Parallel()
	job, raw := statsTestJobAndRaw(t, "stats-start", 7, 100, nil, nil)
	var statsArguments []driver.NamedValue
	backend := statsBackend(t, raw, nil, nil, false, nil, func(query string, arguments []driver.NamedValue) {
		if strings.Contains(query, "INSERT INTO block_statistics") {
			statsArguments = append([]driver.NamedValue(nil), arguments...)
		}
	})
	processor, err := NewPostgresStatsProcessor(openFakeSQLDB(t, backend))
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.Process(context.Background(), job)
	if err != nil || result.State != ResultComplete {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if len(statsArguments) != 19 || statsArguments[10].Value != nil || statsArguments[11].Value != nil ||
		statsArguments[13].Value != nil || statsArguments[14].Value != "0" ||
		statsArguments[15].Value != "0" || statsArguments[16].Value != "0" {
		t.Fatalf("stats arguments=%+v", statsArguments)
	}
}

func TestStatsV2ConfiguredStartIgnoresRetainedCanonicalParent(t *testing.T) {
	t.Parallel()
	job, raw := statsTestJobAndRaw(t, "stats-start-parent", 7, 100, nil, nil)
	var statsArguments []driver.NamedValue
	backend := statsBackend(t, raw, "6", "99", true, nil, func(query string, arguments []driver.NamedValue) {
		if strings.Contains(query, "INSERT INTO block_statistics") {
			statsArguments = append([]driver.NamedValue(nil), arguments...)
		}
	})
	processor, err := NewPostgresStatsProcessor(openFakeSQLDB(t, backend))
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.Process(context.Background(), job)
	if err != nil || result.State != ResultComplete {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if len(statsArguments) != 19 || statsArguments[10].Value != nil || statsArguments[11].Value != nil {
		t.Fatalf("stats arguments=%+v", statsArguments)
	}
}

func TestStatsV2RejectsMissingCanonicalParentAboveConfiguredStart(t *testing.T) {
	t.Parallel()
	job, raw := statsTestJobAndRaw(t, "stats-gap", 8, 101, nil, nil)
	processor, err := NewPostgresStatsProcessor(openFakeSQLDB(t, statsBackend(t, raw, nil, nil, false, nil, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), job); err == nil || !strings.Contains(err.Error(), "canonical parent fact") {
		t.Fatalf("error=%v", err)
	}
}

func TestStatsV2RejectsReceiptBlobGasMissingFromHeader(t *testing.T) {
	t.Parallel()
	job, raw := statsTestJobAndRaw(t, "stats-blob", 8, 101, nil, nil)
	receipt := statsTestReceipt(t, job, 0x20000, 3)
	processor, err := NewPostgresStatsProcessor(openFakeSQLDB(t, statsBackend(t, raw, "7", "100", true, [][]byte{receipt}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), job); err == nil || !strings.Contains(err.Error(), "absent from the block header") {
		t.Fatalf("error=%v", err)
	}
}

func TestStatsV2RejectsIncompleteBlobHeaderFields(t *testing.T) {
	t.Parallel()
	blobGasUsed := uint64(0x20000)
	job, raw := statsTestJobAndRaw(t, "stats-blob-header", 8, 101, &blobGasUsed, nil)
	receipt := statsTestReceipt(t, job, blobGasUsed, 3)
	processor, err := NewPostgresStatsProcessor(openFakeSQLDB(t, statsBackend(t, raw, "7", "100", true, [][]byte{receipt}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), job); err == nil || !strings.Contains(err.Error(), "incomplete blob header") {
		t.Fatalf("error=%v", err)
	}
}

func TestStatsV2RejectsNonPositiveReceiptBlobFacts(t *testing.T) {
	t.Parallel()
	blobGasUsed, excessBlobGas := uint64(0), uint64(1)
	job, raw := statsTestJobAndRaw(t, "stats-blob-zero", 8, 101, &blobGasUsed, &excessBlobGas)
	receipt := statsTestReceipt(t, job, 0, 3)
	processor, err := NewPostgresStatsProcessor(openFakeSQLDB(t, statsBackend(t, raw, "7", "100", true, [][]byte{receipt}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), job); err == nil || !strings.Contains(err.Error(), "non-positive blob fee facts") {
		t.Fatalf("error=%v", err)
	}
}

func TestStatsV3DerivesAuthenticatedExecutionFeePriorityFailureAndCreation(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{
		Number:             8,
		Timestamp:          101,
		ExtraData:          []byte("stats-v3"),
		TransactionTypes:   []uint8{types.LegacyTxType, types.AccessListTxType, types.DynamicFeeTxType, types.SetCodeTxType},
		ContractCreations:  []bool{false, false, true, false},
		FailedTransactions: []bool{false, true, false, false},
		BaseFee:            big.NewInt(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := chainbundle.EncodeStoredBlock(bundle)
	if err != nil {
		t.Fatal(err)
	}
	job := Job{
		ID: "stats-v3", Stage: StatsStage, ChainID: "1",
		BlockHash: bundle.Block.Hash(), BlockNumber: 8,
	}
	receipts := make([][]byte, len(bundle.RawReceipts))
	for index := range bundle.RawReceipts {
		receipts[index] = bundle.RawReceipts[index]
	}
	var statsArguments []driver.NamedValue
	backend := statsBackend(t, raw, "7", "100", true, receipts, func(query string, arguments []driver.NamedValue) {
		if strings.Contains(query, "INSERT INTO block_statistics") {
			statsArguments = append([]driver.NamedValue(nil), arguments...)
		}
	})
	processor, err := NewPostgresStatsProcessor(openFakeSQLDB(t, backend))
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.Process(context.Background(), job)
	if err != nil || result.State != ResultComplete {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if len(statsArguments) != 19 {
		t.Fatalf("stats arguments=%+v", statsArguments)
	}
	if got := statsArguments[15].Value; got != "126000" {
		t.Errorf("execution fee=%v want=126000", got)
	}
	if got := statsArguments[16].Value; got != "42000" {
		t.Errorf("priority fee=%v want=42000", got)
	}
	if got := statsArguments[17].Value; got != int64(1) {
		t.Errorf("failed transactions=%v want=1", got)
	}
	if got := statsArguments[18].Value; got != int64(1) {
		t.Errorf("contract creations=%v want=1", got)
	}
}

func TestStatsV3RejectsIncompleteReceiptSet(t *testing.T) {
	t.Parallel()
	job, raw := statsTestJobAndRaw(t, "stats-receipt-gap", 8, 101, nil, nil)
	receipt := statsTestReceipt(t, job, 0, 0)
	var receiptFields map[string]json.RawMessage
	if err := json.Unmarshal(receipt, &receiptFields); err != nil {
		t.Fatal(err)
	}
	delete(receiptFields, "blobGasUsed")
	delete(receiptFields, "blobGasPrice")
	receipt, err := json.Marshal(receiptFields)
	if err != nil {
		t.Fatal(err)
	}
	// Override the source count while returning no authenticated receipt rows.
	backend := statsBackend(t, raw, "7", "100", true, [][]byte{receipt}, nil)
	backend.query = func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
		if strings.Contains(query, "GROUP BY block.raw") {
			return &fakeSQLRows{
				columns: []string{"raw", "count", "configured_start", "parent_number", "parent_timestamp", "canonical_parent"},
				values:  [][]driver.Value{{raw, int64(2), "7", "7", "100", true}},
			}, nil
		}
		if strings.Contains(query, "FOR KEY SHARE") {
			return &fakeSQLRows{columns: []string{"one"}, values: [][]driver.Value{{int64(1)}}}, nil
		}
		if strings.Contains(query, "FROM receipts AS receipt") {
			return &fakeSQLRows{
				columns: []string{"raw"},
				values:  [][]driver.Value{{receipt}},
			}, nil
		}
		return nil, fmt.Errorf("unexpected query with %d arguments: %s", len(arguments), query)
	}
	processor, err := NewPostgresStatsProcessor(openFakeSQLDB(t, backend))
	if err != nil {
		t.Fatal(err)
	}
	_, processErr := processor.Process(context.Background(), job)
	var classified stageError
	if processErr == nil || !strings.Contains(processErr.Error(), "receipt count") ||
		!errors.As(processErr, &classified) || classified.kind != "permanent" {
		t.Fatalf("error=%v", processErr)
	}
}

func TestStatsV3RejectsExecutionFeeAboveUint256(t *testing.T) {
	t.Parallel()
	job, raw := statsTestJobAndRaw(t, "stats-fee-overflow", 8, 101, nil, nil)
	receipt := statsTestReceipt(t, job, 0, 0)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(receipt, &fields); err != nil {
		t.Fatal(err)
	}
	fields["effectiveGasPrice"] = json.RawMessage(`"0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"`)
	receipt, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := NewPostgresStatsProcessor(openFakeSQLDB(
		t,
		statsBackend(t, raw, "7", "100", true, [][]byte{receipt}, nil),
	))
	if err != nil {
		t.Fatal(err)
	}
	_, processErr := processor.Process(context.Background(), job)
	var classified stageError
	if processErr == nil || !strings.Contains(processErr.Error(), "execution fee exceeds uint256") ||
		!errors.As(processErr, &classified) || classified.kind != "permanent" {
		t.Fatalf("error=%v", processErr)
	}
}

func statsTestJobAndRaw(
	t *testing.T,
	id string,
	number uint64,
	timestamp uint64,
	blobGasUsed *uint64,
	excessBlobGas *uint64,
) (Job, []byte) {
	t.Helper()
	bundle, err := testfixture.New(testfixture.Options{
		Number:        number,
		Timestamp:     timestamp,
		ExtraData:     []byte(id),
		BlobGasUsed:   blobGasUsed,
		ExcessBlobGas: excessBlobGas,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := chainbundle.EncodeStoredBlock(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return Job{
		ID: id, Stage: StatsStage, ChainID: "1",
		BlockHash: bundle.Block.Hash(), BlockNumber: number,
	}, raw
}

func statsTestReceipt(t *testing.T, job Job, blobGasUsed, blobGasPrice uint64) []byte {
	t.Helper()
	receipt := &types.Receipt{
		Type:              types.BlobTxType,
		Status:            types.ReceiptStatusSuccessful,
		CumulativeGasUsed: 21_000,
		Logs:              []*types.Log{},
		TxHash:            uintWord(job.BlockNumber + 10_000),
		GasUsed:           21_000,
		EffectiveGasPrice: big.NewInt(1),
		BlockHash:         job.BlockHash,
		BlockNumber:       new(big.Int).SetUint64(job.BlockNumber),
		TransactionIndex:  0,
	}
	receipt.Bloom = types.CreateBloom(receipt)
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["blobGasUsed"], err = json.Marshal(hexutil.EncodeUint64(blobGasUsed))
	if err != nil {
		t.Fatal(err)
	}
	fields["blobGasPrice"], err = json.Marshal(hexutil.EncodeUint64(blobGasPrice))
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func statsBackend(
	t *testing.T,
	raw []byte,
	parentNumber any,
	parentTimestamp any,
	canonicalParent bool,
	receipts [][]byte,
	onExec func(string, []driver.NamedValue),
) *fakeSQLBackend {
	t.Helper()
	return &fakeSQLBackend{
		query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
			switch {
			case strings.Contains(query, "FOR KEY SHARE"):
				return &fakeSQLRows{columns: []string{"one"}, values: [][]driver.Value{{int64(1)}}}, nil
			case strings.Contains(query, "GROUP BY block.raw"):
				return &fakeSQLRows{
					columns: []string{"raw", "count", "configured_start", "parent_number", "parent_timestamp", "canonical_parent"},
					values:  [][]driver.Value{{raw, int64(len(receipts)), "7", parentNumber, parentTimestamp, canonicalParent}},
				}, nil
			case strings.Contains(query, "FROM receipts AS receipt"):
				values := make([][]driver.Value, len(receipts))
				for index := range receipts {
					values[index] = []driver.Value{receipts[index]}
				}
				return &fakeSQLRows{columns: []string{"raw"}, values: values}, nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, arguments []driver.NamedValue) (driver.Result, error) {
			if onExec != nil {
				onExec(query, arguments)
			}
			return driver.RowsAffected(1), nil
		},
	}
}
