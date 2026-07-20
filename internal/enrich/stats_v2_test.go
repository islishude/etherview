package enrich

import (
	"context"
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"
)

func TestStatsV2AllowsExactNonZeroConfiguredStartWithoutParent(t *testing.T) {
	t.Parallel()
	job := Job{ID: "stats-start", Stage: StatsStage, ChainID: "1", BlockHash: uintWord(801), BlockNumber: 7}
	raw := []byte(fmt.Sprintf(`{"number":"0x7","hash":%q,"timestamp":"0x64","gasUsed":"0x5208","gasLimit":"0x1c9c380"}`, job.BlockHash.String()))
	var statsArguments []driver.NamedValue
	backend := statsBackend(t, raw, "7", nil, nil, false, nil, func(query string, arguments []driver.NamedValue) {
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
	if len(statsArguments) != 15 || statsArguments[10].Value != nil || statsArguments[11].Value != nil ||
		statsArguments[13].Value != nil || statsArguments[14].Value != "0" {
		t.Fatalf("stats arguments=%+v", statsArguments)
	}
}

func TestStatsV2ConfiguredStartIgnoresRetainedCanonicalParent(t *testing.T) {
	t.Parallel()
	job := Job{ID: "stats-start-parent", Stage: StatsStage, ChainID: "1", BlockHash: uintWord(804), BlockNumber: 7}
	raw := []byte(fmt.Sprintf(`{"number":"0x7","hash":%q,"timestamp":"0x64","gasUsed":"0x5208","gasLimit":"0x1c9c380"}`, job.BlockHash.String()))
	var statsArguments []driver.NamedValue
	backend := statsBackend(t, raw, "7", "6", "99", true, nil, func(query string, arguments []driver.NamedValue) {
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
	if len(statsArguments) != 15 || statsArguments[10].Value != nil || statsArguments[11].Value != nil {
		t.Fatalf("stats arguments=%+v", statsArguments)
	}
}

func TestStatsV2RejectsMissingCanonicalParentAboveConfiguredStart(t *testing.T) {
	t.Parallel()
	job := Job{ID: "stats-gap", Stage: StatsStage, ChainID: "1", BlockHash: uintWord(802), BlockNumber: 8}
	raw := []byte(fmt.Sprintf(`{"number":"0x8","hash":%q,"timestamp":"0x65","gasUsed":"0x5208","gasLimit":"0x1c9c380"}`, job.BlockHash.String()))
	processor, err := NewPostgresStatsProcessor(openFakeSQLDB(t, statsBackend(t, raw, "7", nil, nil, false, nil, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), job); err == nil || !strings.Contains(err.Error(), "canonical parent fact") {
		t.Fatalf("error=%v", err)
	}
}

func TestStatsV2RejectsReceiptBlobGasMissingFromHeader(t *testing.T) {
	t.Parallel()
	job := Job{ID: "stats-blob", Stage: StatsStage, ChainID: "1", BlockHash: uintWord(803), BlockNumber: 8}
	raw := []byte(fmt.Sprintf(`{"number":"0x8","hash":%q,"timestamp":"0x65","gasUsed":"0x5208","gasLimit":"0x1c9c380"}`, job.BlockHash.String()))
	receipt := []byte(`{"blobGasUsed":"0x20000","blobGasPrice":"0x3"}`)
	processor, err := NewPostgresStatsProcessor(openFakeSQLDB(t, statsBackend(t, raw, "7", "7", "100", true, [][]byte{receipt}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), job); err == nil || !strings.Contains(err.Error(), "absent from the block header") {
		t.Fatalf("error=%v", err)
	}
}

func TestStatsV2RejectsIncompleteBlobHeaderFields(t *testing.T) {
	t.Parallel()
	job := Job{ID: "stats-blob-header", Stage: StatsStage, ChainID: "1", BlockHash: uintWord(805), BlockNumber: 8}
	raw := []byte(fmt.Sprintf(`{"number":"0x8","hash":%q,"timestamp":"0x65","gasUsed":"0x5208","gasLimit":"0x1c9c380","blobGasUsed":"0x20000"}`, job.BlockHash.String()))
	receipt := []byte(`{"blobGasUsed":"0x20000","blobGasPrice":"0x3"}`)
	processor, err := NewPostgresStatsProcessor(openFakeSQLDB(t, statsBackend(t, raw, "7", "7", "100", true, [][]byte{receipt}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), job); err == nil || !strings.Contains(err.Error(), "incomplete blob header") {
		t.Fatalf("error=%v", err)
	}
}

func TestStatsV2RejectsNonPositiveReceiptBlobFacts(t *testing.T) {
	t.Parallel()
	job := Job{ID: "stats-blob-zero", Stage: StatsStage, ChainID: "1", BlockHash: uintWord(806), BlockNumber: 8}
	raw := []byte(fmt.Sprintf(`{"number":"0x8","hash":%q,"timestamp":"0x65","gasUsed":"0x5208","gasLimit":"0x1c9c380","blobGasUsed":"0x0","excessBlobGas":"0x1"}`, job.BlockHash.String()))
	receipt := []byte(`{"blobGasUsed":"0x0","blobGasPrice":"0x3"}`)
	processor, err := NewPostgresStatsProcessor(openFakeSQLDB(t, statsBackend(t, raw, "7", "7", "100", true, [][]byte{receipt}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), job); err == nil || !strings.Contains(err.Error(), "non-positive blob fee facts") {
		t.Fatalf("error=%v", err)
	}
}

func statsBackend(
	t *testing.T,
	raw []byte,
	configuredStart string,
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
					values:  [][]driver.Value{{raw, int64(0), configuredStart, parentNumber, parentTimestamp, canonicalParent}},
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
