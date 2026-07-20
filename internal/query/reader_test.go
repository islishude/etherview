package query

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/httpapi"
)

func TestChecksumAddressEIP55Vectors(t *testing.T) {
	t.Parallel()
	for _, expected := range []string{
		"0x52908400098527886E0F7030069857D2E4169EE7",
		"0xde709f2102306220921060314715629080e2fb77",
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
	} {
		actual, err := ChecksumAddress(strings.ToLower(expected))
		if err != nil {
			t.Fatal(err)
		}
		if actual != expected {
			t.Errorf("ChecksumAddress(%q) = %q, want %q", strings.ToLower(expected), actual, expected)
		}
	}
}

func TestStatusReportsGapFreeCheckpointAndUpstreamHead(t *testing.T) {
	t.Parallel()
	tipHash := testHashBytes(3)
	db := testDatabase(t,
		queryExpectation{contains: "configuration.configured_start::text", columns: columns(9), rows: [][]driver.Value{{
			"0", "2", tipHash, "2", tipHash, "2", tipHash, "1", "0",
		}}},
	)
	reader := testReader(t, db, Options{
		ChainID:     1,
		LatestBlock: func(context.Context) (uint64, error) { return 2, nil },
	})
	snapshot, err := reader.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.CoreReady || !snapshot.BackfillComplete || snapshot.IndexedBlock != 2 ||
		snapshot.LatestBlock != 2 || !snapshot.HighestCoveredKnown ||
		snapshot.HighestCoveredBlock != 2 || snapshot.CoverageEnd != 2 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.SafeBlock == nil || *snapshot.SafeBlock != 1 || snapshot.FinalizedBlock == nil || *snapshot.FinalizedBlock != 0 {
		t.Fatalf("finality snapshot = %+v", snapshot)
	}
	if snapshot.Completeness.Core != gen.StageStateComplete || snapshot.Completeness.Trace != gen.StageStateUnavailable {
		t.Fatalf("completeness = %+v", snapshot.Completeness)
	}
}

func TestStatusDoesNotClaimReadyAcrossCanonicalGap(t *testing.T) {
	t.Parallel()
	genesisHash, tipHash := testHashBytes(1), testHashBytes(3)
	db := testDatabase(t,
		queryExpectation{contains: "configuration.configured_start::text", columns: columns(9), rows: [][]driver.Value{{
			"0", "0", genesisHash, "0", genesisHash, "2", tipHash, nil, nil,
		}}},
	)
	reader := testReader(t, db, Options{
		ChainID:     1,
		LatestBlock: func(context.Context) (uint64, error) { return 2, nil },
	})
	snapshot, err := reader.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CoreReady || snapshot.BackfillComplete || snapshot.IndexedBlock != 0 ||
		snapshot.HighestCoveredBlock != 2 || snapshot.Completeness.Core != gen.StageStatePending {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestStatusUsesDurableSplitRoleHeadAndReadiness(t *testing.T) {
	t.Parallel()
	tipHash := testHashBytes(9)
	db := testDatabase(t,
		queryExpectation{contains: "configuration.configured_start::text", columns: columns(9), rows: [][]driver.Value{{
			"0", "8", tipHash, "8", tipHash, "8", tipHash, nil, nil,
		}}},
	)
	reader := testReader(t, db, Options{
		ChainID: 1,
		RuntimeStatus: func(context.Context) (RuntimeStatus, bool, error) {
			return RuntimeStatus{
				Latest: 12, Indexed: 8, HighestCovered: 8,
				LatestKnown: true, IndexedKnown: true, HighestCoveredKnown: true,
			}, true, nil
		},
	})
	snapshot, err := reader.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LatestBlock != 12 || snapshot.IndexedBlock != 8 || snapshot.CoreReady || snapshot.Completeness.Core != gen.StageStatePending {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestStatusDoesNotSubstituteCanonicalTipWhenRuntimeStatusIsMissing(t *testing.T) {
	t.Parallel()
	tipHash := testHashBytes(3)
	db := testDatabase(t,
		queryExpectation{contains: "configuration.configured_start::text", columns: columns(9), rows: [][]driver.Value{{
			"0", "2", tipHash, "2", tipHash, "2", tipHash, nil, nil,
		}}},
	)
	reader := testReader(t, db, Options{
		ChainID: 1,
		RuntimeStatus: func(context.Context) (RuntimeStatus, bool, error) {
			return RuntimeStatus{}, false, nil
		},
	})
	snapshot, err := reader.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LatestBlock != 0 || snapshot.IndexedBlock != 2 || snapshot.CoreReady {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestStatusDoesNotTreatIsolatedLiveCoverageAsIndexedOrReady(t *testing.T) {
	t.Parallel()
	tipHash := testHashBytes(10)
	db := testDatabase(t, queryExpectation{
		contains: "configuration.configured_start::text", columns: columns(9),
		rows: [][]driver.Value{{"0", nil, nil, nil, nil, "10", tipHash, nil, nil}},
	})
	reader := testReader(t, db, Options{
		ChainID: 1,
		RuntimeStatus: func(context.Context) (RuntimeStatus, bool, error) {
			return RuntimeStatus{
				Latest: 10, HighestCovered: 10,
				LatestKnown: true, HighestCoveredKnown: true,
			}, true, nil
		},
	})
	snapshot, err := reader.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.IndexedBlock != 0 || snapshot.BackfillComplete || snapshot.CoreReady ||
		!snapshot.HighestCoveredKnown || snapshot.HighestCoveredBlock != 10 ||
		snapshot.Completeness.Core != gen.StageStatePending {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestBlocksUseSnapshotBoundOpaqueCursor(t *testing.T) {
	t.Parallel()
	db := testDatabase(t,
		queryExpectation{contains: "ORDER BY number DESC", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{
			contains: "canonical.number <= $2::numeric",
			columns:  columns(6),
			rows: [][]driver.Value{
				{testBlockRaw(2, 3, 2, 2), "2", testHashBytes(3), true, "1", "0"},
				{testBlockRaw(1, 2, 1, 1), "1", testHashBytes(2), true, "1", "0"},
				{testBlockRaw(0, 1, 0, 0), "0", testHashBytes(1), true, "1", "0"},
			},
		},
		queryExpectation{contains: "SELECT EXISTS", columns: columns(1), rows: [][]driver.Value{{true}}},
		queryExpectation{contains: "canonical.number < $2::numeric", columns: columns(6), rows: [][]driver.Value{
			{testBlockRaw(0, 1, 0, 0), "0", testHashBytes(1), true, "1", "0"},
		}},
	)
	reader := testReader(t, db, Options{ChainID: 1})
	first, cursor, err := reader.Blocks(context.Background(), "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || cursor == "" || first[0].Number != "2" || first[1].Number != "1" {
		t.Fatalf("first page = %+v, cursor = %q", first, cursor)
	}
	if first[0].Finality != gen.FinalityLatest || first[1].Finality != gen.FinalitySafe {
		t.Fatalf("first-page finality = %s, %s", first[0].Finality, first[1].Finality)
	}
	if first[0].Miner == nil || *first[0].Miner != "0x52908400098527886E0F7030069857D2E4169EE7" {
		t.Fatalf("checksummed miner = %v", first[0].Miner)
	}
	if first[0].GasLimit == nil || *first[0].GasLimit != "30000000" || first[0].BaseFeePerGas == nil || *first[0].BaseFeePerGas != "1000000000" {
		t.Fatalf("decimal block quantities = %+v", first[0])
	}
	second, next, err := reader.Blocks(context.Background(), cursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || next != "" || second[0].Number != "0" || second[0].Finality != gen.FinalityFinalized {
		t.Fatalf("second page = %+v, next = %q", second, next)
	}
}

func TestBlocksRejectCursorAfterReorg(t *testing.T) {
	t.Parallel()
	cursor, err := httpapi.EncodeCursor(blockCursor{
		ChainID: "1", SnapshotNumber: 10, SnapshotHash: testHash(10),
		BeforeNumber: 8, BeforeHash: testHash(8),
	})
	if err != nil {
		t.Fatal(err)
	}
	db := testDatabase(t,
		queryExpectation{contains: "SELECT EXISTS", columns: columns(1), rows: [][]driver.Value{{false}}},
	)
	reader := testReader(t, db, Options{ChainID: 1})
	_, _, err = reader.Blocks(context.Background(), cursor, 25)
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("error = %v, want ErrInvalidCursor", err)
	}
}

func TestTransactionsUseSnapshotBoundCompositeCursor(t *testing.T) {
	t.Parallel()
	db := testDatabase(t,
		queryExpectation{contains: "ORDER BY number DESC", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{
			contains: "inclusion.block_number <= $2::numeric",
			columns:  columns(9),
			rows: [][]driver.Value{
				{testTransactionRawAt(2, 3, 102, 1), testReceiptRawAt(2, 3, 102, 1, "0x1"), "2", testHashBytes(3), int64(1), testHashBytes(102), true, "1", "0"},
				{testTransactionRawAt(2, 3, 101, 0), testReceiptRawAt(2, 3, 101, 0, "0x1"), "2", testHashBytes(3), int64(0), testHashBytes(101), true, "1", "0"},
				{testTransactionRawAt(1, 2, 100, 0), testReceiptRawAt(1, 2, 100, 0, "0x1"), "1", testHashBytes(2), int64(0), testHashBytes(100), true, "1", "0"},
			},
		},
		queryExpectation{contains: "SELECT EXISTS", columns: columns(1), rows: [][]driver.Value{{true}}},
		queryExpectation{
			contains: "inclusion.tx_index < $3",
			columns:  columns(9),
			rows: [][]driver.Value{
				{testTransactionRawAt(1, 2, 100, 0), testReceiptRawAt(1, 2, 100, 0, "0x1"), "1", testHashBytes(2), int64(0), testHashBytes(100), true, "1", "0"},
			},
		},
	)
	reader := testReader(t, db, Options{ChainID: 1})
	first, cursor, err := reader.Transactions(context.Background(), "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || cursor == "" || first[0].TransactionIndex == nil || *first[0].TransactionIndex != 1 || first[1].TransactionIndex == nil || *first[1].TransactionIndex != 0 {
		t.Fatalf("first=%+v cursor=%q", first, cursor)
	}
	second, next, err := reader.Transactions(context.Background(), cursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || next != "" || second[0].BlockNumber == nil || *second[0].BlockNumber != "1" || second[0].Finality != gen.FinalitySafe {
		t.Fatalf("second=%+v next=%q", second, next)
	}
}

func TestTransactionsRejectCursorAfterCanonicalChange(t *testing.T) {
	t.Parallel()
	cursor, err := httpapi.EncodeCursor(transactionCursor{
		ChainID: "1", SnapshotNumber: 10, SnapshotHash: testHash(10),
		BeforeBlockNumber: 8, BeforeBlockHash: testHash(8), BeforeTxIndex: 1, BeforeTxHash: testHash(80),
	})
	if err != nil {
		t.Fatal(err)
	}
	db := testDatabase(t, queryExpectation{contains: "SELECT EXISTS", columns: columns(1), rows: [][]driver.Value{{false}}})
	reader := testReader(t, db, Options{ChainID: 1})
	_, _, err = reader.Transactions(context.Background(), cursor, 25)
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("error=%v, want ErrInvalidCursor", err)
	}
}

func TestBlockHashLookupCanReturnRetainedOrphan(t *testing.T) {
	t.Parallel()
	db := testDatabase(t, queryExpectation{
		contains: "block.hash = $2", columns: columns(6),
		rows: [][]driver.Value{{testBlockRaw(2, 3, 2, 0), "2", testHashBytes(3), false, "5", "4"}},
	})
	reader := testReader(t, db, Options{ChainID: 1})
	block, err := reader.Block(context.Background(), testHash(3))
	if err != nil {
		t.Fatal(err)
	}
	if block.Canonical || block.Finality != gen.FinalityOrphan {
		t.Fatalf("block = %+v", block)
	}
}

func TestBlockRejectsRawIdentityMismatch(t *testing.T) {
	t.Parallel()
	db := testDatabase(t, queryExpectation{
		contains: "canonical.number = $2::numeric", columns: columns(6),
		rows: [][]driver.Value{{testBlockRaw(2, 99, 2, 0), "2", testHashBytes(3), true, nil, nil}},
	})
	reader := testReader(t, db, Options{ChainID: 1})
	if _, err := reader.Block(context.Background(), "2"); err == nil || !strings.Contains(err.Error(), "raw hash") {
		t.Fatalf("error = %v", err)
	}
}

func TestTransactionDecodesDecimalQuantitiesChecksumAndReceipt(t *testing.T) {
	t.Parallel()
	db := testDatabase(t, queryExpectation{
		contains: "FROM transaction_inclusions AS inclusion", columns: columns(9),
		rows: [][]driver.Value{{
			testTransactionRaw(2, 3, 7), testReceiptRaw(2, 3, 7, "0x1"),
			"2", testHashBytes(3), int64(0), testHashBytes(7), true, "2", "1",
		}},
	})
	reader := testReader(t, db, Options{ChainID: 1})
	transaction, err := reader.Transaction(context.Background(), testHash(7))
	if err != nil {
		t.Fatal(err)
	}
	const maxUint256 = "115792089237316195423570985008687907853269984665640564039457584007913129639935"
	if transaction.Value != maxUint256 || transaction.Nonce != "15" || transaction.Gas != "21000" {
		t.Fatalf("decimal transaction quantities = %+v", transaction)
	}
	if transaction.From != "0x52908400098527886E0F7030069857D2E4169EE7" || transaction.To == nil || *transaction.To != "0xde709f2102306220921060314715629080e2fb77" {
		t.Fatalf("transaction addresses = %s -> %v", transaction.From, transaction.To)
	}
	if transaction.Type == nil || *transaction.Type != "2" || transaction.Status == nil || *transaction.Status != gen.TransactionStatusSuccess {
		t.Fatalf("transaction type/status = %v/%v", transaction.Type, transaction.Status)
	}
	if transaction.Finality != gen.FinalitySafe || !transaction.Canonical {
		t.Fatalf("transaction canonicality = %+v", transaction)
	}
}

func TestAddressIsHonestlyUnavailable(t *testing.T) {
	t.Parallel()
	db := testDatabase(t)
	reader := testReader(t, db, Options{ChainID: 1})
	_, err := reader.Address(context.Background(), "0x52908400098527886e0f7030069857d2e4169ee7")
	if !errors.Is(err, httpapi.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}

func TestCoreSearchCoversAddressBlockNumberAndHash(t *testing.T) {
	t.Parallel()
	db := testDatabase(t,
		queryExpectation{contains: "ORDER BY number DESC", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{contains: "search_catalog_generations", columns: columns(2), rows: [][]driver.Value{{int64(7), int64(1)}}},
		queryExpectation{contains: "FROM search_catalog_documents AS document", columns: columns(5)},
		queryExpectation{contains: "ORDER BY number DESC", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{contains: "search_catalog_generations", columns: columns(2), rows: [][]driver.Value{{int64(7), int64(1)}}},
		queryExpectation{contains: "canonical.number = $2::numeric", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{contains: "ORDER BY number DESC", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{contains: "search_catalog_generations", columns: columns(2), rows: [][]driver.Value{{int64(7), int64(1)}}},
		queryExpectation{contains: "SELECT kind, key, label, rank, canonical", columns: columns(5), rows: [][]driver.Value{
			{"block", testHash(3), "Block #2", int64(100), false},
			{"transaction", testHash(3), "Transaction " + testHash(3), int64(90), true},
		}},
	)
	reader := testReader(t, db, Options{ChainID: 1})
	addressResults, _, err := reader.Search(context.Background(), "0x52908400098527886e0f7030069857d2e4169ee7", "", 20)
	if err != nil || len(addressResults) != 1 || addressResults[0].Kind != gen.SearchResultKindAddress {
		t.Fatalf("address search = %+v, error = %v", addressResults, err)
	}
	blockResults, _, err := reader.Search(context.Background(), "2", "", 20)
	if err != nil || len(blockResults) != 1 || blockResults[0].Key != testHash(3) {
		t.Fatalf("block search = %+v, error = %v", blockResults, err)
	}
	hashResults, _, err := reader.Search(context.Background(), testHash(3), "", 20)
	if err != nil || len(hashResults) != 2 || hashResults[0].Canonical == nil || *hashResults[0].Canonical {
		t.Fatalf("hash search = %+v, error = %v", hashResults, err)
	}
}

func TestSearchCoversCanonicalNamesTokensContractsAndLabels(t *testing.T) {
	t.Parallel()
	tokenAddress := "0x5aAe" + "b6053F3E94C9b9A09f33669435E7Ef1BeAed"
	db := testDatabase(t,
		queryExpectation{contains: "ORDER BY number DESC", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{contains: "search_catalog_generations", columns: columns(2), rows: [][]driver.Value{{int64(7), int64(1)}}},
		queryExpectation{
			contains: "FROM search_catalog_documents AS document", columns: columns(5),
			rows: [][]driver.Value{
				{"contract", "0x52908400098527886e0f7030069857d2e4169ee7", "Treasury", int64(110), nil},
				{"address", "0xde709f2102306220921060314715629080e2fb77", "alice.eth", int64(100), true},
				{"token", tokenAddress, "Example Token", int64(65), true},
			},
		},
	)
	reader := testReader(t, db, Options{ChainID: 1})
	results, _, err := reader.Search(context.Background(), "Treasury", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results[0].Kind != gen.SearchResultKindContract || results[0].Key != "0x52908400098527886E0F7030069857D2E4169EE7" ||
		results[1].Kind != gen.SearchResultKindAddress || results[1].Canonical == nil || !*results[1].Canonical ||
		results[2].Kind != gen.SearchResultKindToken || results[2].Key != tokenAddress {
		t.Fatalf("results=%+v", results)
	}
}

func TestSearchRejectsMalformedPersistedEntityKey(t *testing.T) {
	t.Parallel()
	db := testDatabase(t,
		queryExpectation{contains: "ORDER BY number DESC", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{contains: "search_catalog_generations", columns: columns(2), rows: [][]driver.Value{{int64(7), int64(1)}}},
		queryExpectation{
			contains: "FROM search_catalog_documents AS document", columns: columns(5),
			rows: [][]driver.Value{{"transaction", "not-a-hash", "bad", int64(80), nil}},
		},
	)
	reader := testReader(t, db, Options{ChainID: 1})
	_, _, err := reader.Search(context.Background(), "bad", "", 20)
	if err == nil || !strings.Contains(err.Error(), "invalid transaction") {
		t.Fatalf("error=%v", err)
	}
}

func TestSearchTextRejectsMalformedPersistedEntityKey(t *testing.T) {
	t.Parallel()
	db := testDatabase(t, queryExpectation{
		contains: "FROM search_catalog_documents AS document", columns: columns(5),
		rows: [][]driver.Value{{"transaction", "not-a-hash", "bad", int64(80), nil}},
	})
	reader := testReader(t, db, Options{ChainID: 1})
	results, err := reader.searchText(context.Background(), db, "bad", 2, 7, nil, 20)
	if err == nil || !strings.Contains(err.Error(), "invalid transaction") {
		t.Fatalf("results=%+v error=%v", results, err)
	}
}

func TestDecodeRawObjectRejectsTrailingJSON(t *testing.T) {
	t.Parallel()
	var destination map[string]any
	if err := decodeRawObject([]byte(`{"ok":true}{"second":true}`), &destination); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
}

func testReader(t *testing.T, db interface{ Close() error }, options Options) *PostgresReader {
	t.Helper()
	sqlDB, ok := db.(*sql.DB)
	if !ok {
		t.Fatal("test database is not *sql.DB")
	}
	reader, err := NewPostgresReader(sqlDB, options)
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func testHash(value byte) string { return fmt.Sprintf("0x%064x", value) }

func testHashBytes(value byte) []byte {
	result := make([]byte, 32)
	result[len(result)-1] = value
	return result
}

func testBlockRaw(number uint64, hash, parent byte, transactionCount int) []byte {
	transactions := make([]string, transactionCount)
	for index := range transactions {
		transactions[index] = testHash(byte(100 + index))
	}
	value := map[string]any{
		"number":        fmt.Sprintf("0x%x", number),
		"hash":          testHash(hash),
		"parentHash":    testHash(parent),
		"timestamp":     "0x64",
		"miner":         "0x52908400098527886e0f7030069857d2e4169ee7",
		"transactions":  transactions,
		"gasUsed":       "0x5208",
		"gasLimit":      "0x1c9c380",
		"baseFeePerGas": "0x3b9aca00",
	}
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func testTransactionRaw(blockNumber uint64, blockHash, transactionHash byte) []byte {
	return testTransactionRawAt(blockNumber, blockHash, transactionHash, 0)
}

func testTransactionRawAt(blockNumber uint64, blockHash, transactionHash byte, transactionIndex uint64) []byte {
	value := map[string]any{
		"hash":                 testHash(transactionHash),
		"type":                 "0x2",
		"blockHash":            testHash(blockHash),
		"blockNumber":          fmt.Sprintf("0x%x", blockNumber),
		"transactionIndex":     fmt.Sprintf("0x%x", transactionIndex),
		"from":                 "0x52908400098527886e0f7030069857d2e4169ee7",
		"to":                   "0xde709f2102306220921060314715629080e2fb77",
		"nonce":                "0xf",
		"gas":                  "0x5208",
		"maxFeePerGas":         "0x77359400",
		"maxPriorityFeePerGas": "0x3b9aca00",
		"value":                "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		"input":                "0xdeadbeef",
	}
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func testReceiptRaw(blockNumber uint64, blockHash, transactionHash byte, status string) []byte {
	return testReceiptRawAt(blockNumber, blockHash, transactionHash, 0, status)
}

func testReceiptRawAt(blockNumber uint64, blockHash, transactionHash byte, transactionIndex uint64, status string) []byte {
	value := map[string]any{
		"transactionHash":   testHash(transactionHash),
		"transactionIndex":  fmt.Sprintf("0x%x", transactionIndex),
		"blockHash":         testHash(blockHash),
		"blockNumber":       fmt.Sprintf("0x%x", blockNumber),
		"cumulativeGasUsed": "0x5208",
		"logs":              []any{},
		"logsBloom":         "0x",
		"status":            status,
	}
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
