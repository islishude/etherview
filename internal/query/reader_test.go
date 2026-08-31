package query

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/db/gen"
	ensresolver "github.com/islishude/etherview/internal/ens"
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

func TestPublicQueriesDoNotTransferFullBlockRaw(t *testing.T) {
	t.Parallel()
	for name, query := range map[string]string{
		"block by hash":        dbgen.QueryBlockByHash,
		"block by number":      dbgen.QueryBlockByNumber,
		"block list":           dbgen.QueryListBlocks,
		"block list first":     dbgen.QueryListBlocksFirst,
		"transaction":          dbgen.QueryTransactionByHash,
		"transaction list":     dbgen.QueryListTransactionsWithMethod,
		"transaction first":    dbgen.QueryListTransactionsWithMethodFirst,
		"block transactions":   dbgen.ListBlockTransactions,
		"address transactions": dbgen.QueryListAddressTransactions,
	} {
		if strings.Contains(query, "block.raw") {
			t.Errorf("%s query transfers block.raw", name)
		}
	}
}

func TestStatusReportsGapFreeCheckpointAndUpstreamHead(t *testing.T) {
	t.Parallel()
	tipHash := testHashBytes(3)
	db := testDatabase(t,
		queryExpectation{contains: "configuration.configured_start::text", columns: columns(10), rows: [][]driver.Value{{
			"0", "2", tipHash, "2", tipHash, "2", tipHash, "1", "0", nil,
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
		queryExpectation{contains: "configuration.configured_start::text", columns: columns(10), rows: [][]driver.Value{{
			"0", "0", genesisHash, "0", genesisHash, "2", tipHash, nil, nil, nil,
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
		queryExpectation{contains: "configuration.configured_start::text", columns: columns(10), rows: [][]driver.Value{{
			"0", "8", tipHash, "8", tipHash, "8", tipHash, nil, nil, nil,
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
		queryExpectation{contains: "configuration.configured_start::text", columns: columns(10), rows: [][]driver.Value{{
			"0", "2", tipHash, "2", tipHash, "2", tipHash, nil, nil, nil,
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
		contains: "configuration.configured_start::text", columns: columns(10),
		rows: [][]driver.Value{{"0", nil, nil, nil, nil, "10", tipHash, nil, nil, nil}},
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

func TestStatusReportsCurrentTracePublication(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		indexed   bool
		stored    any
		want      gen.StageState
		wantError bool
	}{
		{name: "no indexed tip", want: gen.StageStatePending},
		{name: "missing publication", indexed: true, want: gen.StageStatePending},
		{name: "complete", indexed: true, stored: "complete", want: gen.StageStateComplete},
		{name: "unavailable", indexed: true, stored: "unavailable", want: gen.StageStateUnavailable},
		{name: "failed", indexed: true, stored: "failed", want: gen.StageStateFailed},
		{name: "invalid", indexed: true, stored: "pending", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var contiguousEnd any
			var contiguousHash any
			if test.indexed {
				contiguousEnd = "169"
				contiguousHash = testHashBytes(169)
			}
			db := testDatabase(t, queryExpectation{
				contains: "trace_result.block_hash = contiguous_block.block_hash",
				columns:  columns(10),
				rows: [][]driver.Value{{
					"0", contiguousEnd, contiguousHash,
					contiguousEnd, contiguousHash,
					contiguousEnd, contiguousHash,
					nil, nil, test.stored,
				}},
			})
			reader := testReader(t, db, Options{
				ChainID: 1,
				OptionalStages: gen.Completeness{
					Trace: gen.StageStatePending,
					State: gen.StageStateComplete,
				},
				LatestBlock: func(context.Context) (uint64, error) {
					if test.indexed {
						return 169, nil
					}
					return 0, nil
				},
			})
			snapshot, err := reader.Status(t.Context())
			if test.wantError {
				if err == nil {
					t.Fatal("invalid published trace state was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Completeness.Trace != test.want ||
				snapshot.Completeness.State != gen.StageStateComplete {
				t.Fatalf("completeness = %+v, want trace=%s state=complete", snapshot.Completeness, test.want)
			}
		})
	}
}

func TestBlocksUseSnapshotBoundOpaqueCursor(t *testing.T) {
	t.Parallel()
	db := testDatabase(t,
		queryExpectation{contains: "ORDER BY canonical.number DESC", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{
			contains: "canonical.number <= $2::numeric",
			columns:  columns(16),
			rows: [][]driver.Value{
				testBlockProjectionRow(2, 3, 2, 2, true, "1", "0"),
				testBlockProjectionRow(1, 2, 1, 1, true, "1", "0"),
				testBlockProjectionRow(0, 1, 0, 0, true, "1", "0"),
			},
		},
		queryExpectation{contains: "SELECT EXISTS", columns: columns(1), rows: [][]driver.Value{{true}}},
		queryExpectation{contains: "canonical.number < $2::numeric", columns: columns(16), rows: [][]driver.Value{
			testBlockProjectionRow(0, 1, 0, 0, true, "1", "0"),
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
		queryExpectation{contains: "ORDER BY canonical.number DESC", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{
			contains: "inclusion.block_number <= $2::numeric",
			columns:  columns(18),
			rows: [][]driver.Value{
				{testTransactionRawAt(2, 3, 102, 1), testReceiptRawAt(2, 3, 102, 1, "0x1"), "2", testHashBytes(3), int64(1), testTransactionHashBytes(102), true, "1", "0", "100", "0x3b9aca00", true, "direct", testAddressBytes(1), testHashBytes(4), "transfer(address,uint256)", "verified", "verified"},
				{testTransactionRawAt(2, 3, 101, 0), testReceiptRawAt(2, 3, 101, 0, "0x1"), "2", testHashBytes(3), int64(0), testTransactionHashBytes(101), true, "1", "0", "100", "0x3b9aca00", true, "empty", nil, nil, nil, nil, nil},
				{testTransactionRawAt(1, 2, 100, 0), testReceiptRawAt(1, 2, 100, 0, "0x1"), "1", testHashBytes(2), int64(0), testTransactionHashBytes(100), true, "1", "0", "100", "0x3b9aca00", false, nil, nil, nil, nil, nil, nil},
			},
		},
		queryExpectation{contains: "SELECT EXISTS", columns: columns(1), rows: [][]driver.Value{{true}}},
		queryExpectation{
			contains: "inclusion.tx_index < $3",
			columns:  columns(18),
			rows: [][]driver.Value{
				{testTransactionRawAt(1, 2, 100, 0), testReceiptRawAt(1, 2, 100, 0, "0x1"), "1", testHashBytes(2), int64(0), testTransactionHashBytes(100), true, "1", "0", "100", "0x3b9aca00", true, "eip7702_delegate", testAddressBytes(2), testHashBytes(5), "setValue(uint256)", "code_hash", "high"},
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
	if first[0].Method == nil || *first[0].Method != "transfer" ||
		first[0].MethodSignature == nil || *first[0].MethodSignature != "transfer(address,uint256)" ||
		first[1].Method == nil || *first[1].Method != "0xdeadbeef" || first[1].MethodSignature != nil {
		t.Fatalf("first transaction methods = %+v", first)
	}
	second, next, err := reader.Transactions(context.Background(), cursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || next != "" || second[0].BlockNumber == nil || *second[0].BlockNumber != "1" || second[0].Finality != gen.FinalitySafe {
		t.Fatalf("second=%+v next=%q", second, next)
	}
	if second[0].Method == nil || *second[0].Method != "setValue" ||
		second[0].MethodSignature == nil || *second[0].MethodSignature != "setValue(uint256)" {
		t.Fatalf("second transaction method = %+v", second[0])
	}
}

func TestProjectTransactionMethodUsesExactPriorityAndFallbacks(t *testing.T) {
	t.Parallel()
	to := gen.Address("0xde709f2102306220921060314715629080e2fb77")
	tests := []struct {
		name        string
		transaction gen.Transaction
		resolution  sql.NullString
		signature   sql.NullString
		wantMethod  string
		wantFull    string
	}{
		{
			name: "contract creation wins", transaction: gen.Transaction{Input: "0x6000"},
			resolution: sql.NullString{String: "direct", Valid: true},
			signature:  sql.NullString{String: "ignored(uint256)", Valid: true},
			wantMethod: "Contract Creation",
		},
		{
			name: "unique direct decode", transaction: gen.Transaction{To: &to, Input: "0xa9059cbb"},
			resolution: sql.NullString{String: "direct", Valid: true},
			signature:  sql.NullString{String: "transfer(address,uint256)", Valid: true},
			wantMethod: "transfer", wantFull: "transfer(address,uint256)",
		},
		{
			name: "unique delegated decode", transaction: gen.Transaction{To: &to, Input: "0x55241077"},
			resolution: sql.NullString{String: "eip7702_delegate", Valid: true},
			signature:  sql.NullString{String: "setValue(uint256)", Valid: true},
			wantMethod: "setValue", wantFull: "setValue(uint256)",
		},
		{
			name: "native transfer", transaction: gen.Transaction{To: &to, Input: "0x"},
			resolution: sql.NullString{String: "empty", Valid: true}, wantMethod: "Native Transfer",
		},
		{
			name: "empty calldata contract call", transaction: gen.Transaction{To: &to, Input: "0x"},
			resolution: sql.NullString{String: "direct", Valid: true}, wantMethod: "0x",
		},
		{
			name: "unknown selector", transaction: gen.Transaction{To: &to, Input: "0xDEADBEEF0102"},
			wantMethod: "0xdeadbeef",
		},
		{
			name: "malformed decoded signature falls back", transaction: gen.Transaction{To: &to, Input: "0xDEADBEEF0102"},
			signature: sql.NullString{String: "(uint256)", Valid: true}, wantMethod: "0xdeadbeef",
		},
		{
			name: "short calldata", transaction: gen.Transaction{To: &to, Input: "0x1234"},
			wantMethod: "0x1234",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := test.transaction
			projectTransactionMethod(&model, test.resolution, test.signature)
			if model.Method == nil || *model.Method != test.wantMethod {
				t.Fatalf("method = %v, want %q", model.Method, test.wantMethod)
			}
			if test.wantFull == "" {
				if model.MethodSignature != nil {
					t.Fatalf("method_signature = %v, want nil", model.MethodSignature)
				}
			} else if model.MethodSignature == nil || *model.MethodSignature != test.wantFull {
				t.Fatalf("method_signature = %v, want %q", model.MethodSignature, test.wantFull)
			}
		})
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

func TestBlockTransactionsUseExactBlockIdentityAndStableIndexCursor(t *testing.T) {
	t.Parallel()
	blockHash := testHash(3)
	db := testDatabase(t,
		queryExpectation{contains: "ORDER BY canonical.number DESC", columns: columns(2), rows: [][]driver.Value{{"9", testHashBytes(10)}}},
		queryExpectation{contains: "FROM blocks WHERE chain_id", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{contains: "inclusion.block_hash = $3", columns: columns(11), rows: [][]driver.Value{
			{testTransactionRawAt(2, 3, 7, 0), testReceiptRawAt(2, 3, 7, 0, "0x1"), "2", testHashBytes(3), int64(0), testTransactionHashBytes(7), false, "8", "7", "100", "0x3b9aca00"},
			{testTransactionRawAt(2, 3, 8, 1), testReceiptRawAt(2, 3, 8, 1, "0x1"), "2", testHashBytes(3), int64(1), testTransactionHashBytes(8), false, "8", "7", "100", "0x3b9aca00"},
		}},
		queryExpectation{contains: "ORDER BY canonical.number DESC", columns: columns(2), rows: [][]driver.Value{{"9", testHashBytes(10)}}},
		queryExpectation{contains: "FROM blocks WHERE chain_id", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{contains: "SELECT EXISTS ( SELECT 1 FROM blocks", columns: columns(1), rows: [][]driver.Value{{true}}},
		queryExpectation{contains: "inclusion.block_hash = $3", columns: columns(11), rows: [][]driver.Value{
			{testTransactionRawAt(2, 3, 8, 1), testReceiptRawAt(2, 3, 8, 1, "0x1"), "2", testHashBytes(3), int64(1), testTransactionHashBytes(8), false, "8", "7", "100", "0x3b9aca00"},
		}},
	)
	reader := testReader(t, db, Options{ChainID: 1})
	first, cursor, err := reader.BlockTransactions(context.Background(), blockHash, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || cursor == "" || first[0].TransactionIndex == nil || *first[0].TransactionIndex != 0 || first[0].Canonical {
		t.Fatalf("first=%+v cursor=%q", first, cursor)
	}
	second, next, err := reader.BlockTransactions(context.Background(), blockHash, cursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || next != "" || second[0].TransactionIndex == nil || *second[0].TransactionIndex != 1 || second[0].Canonical {
		t.Fatalf("second=%+v next=%q", second, next)
	}
}

func TestBlockHashLookupCanReturnRetainedOrphan(t *testing.T) {
	t.Parallel()
	db := testDatabase(t, queryExpectation{
		contains: "block.hash = $2", columns: columns(16),
		rows: [][]driver.Value{testBlockProjectionRow(2, 3, 2, 0, false, "5", "4")},
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

func TestBlockRejectsNormalizedTransactionCountMismatch(t *testing.T) {
	t.Parallel()
	row := testBlockProjectionRow(2, 3, 2, 0, true, nil, nil)
	row[9] = int64(1)
	db := testDatabase(t, queryExpectation{
		contains: "canonical.number = $2::numeric", columns: columns(16),
		rows: [][]driver.Value{row},
	})
	reader := testReader(t, db, Options{ChainID: 1})
	if _, err := reader.Block(context.Background(), "2"); err == nil || !strings.Contains(err.Error(), "normalized inclusions") {
		t.Fatalf("error = %v", err)
	}
}

func TestTransactionDecodesDecimalQuantitiesChecksumAndReceipt(t *testing.T) {
	t.Parallel()
	db := testDatabase(t, queryExpectation{
		contains: "SELECT canonical.number::text, canonical.block_hash", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}},
	}, queryExpectation{
		contains: "FROM transaction_inclusions AS inclusion", columns: columns(11),
		rows: [][]driver.Value{{
			testTransactionRaw(2, 3, 7), testReceiptRaw(2, 3, 7, "0x1"),
			"2", testHashBytes(3), int64(0), testTransactionHashBytes(7), true, "2", "1", "100", "0x3b9aca00",
		}},
	})
	reader := testReader(t, db, Options{ChainID: 1})
	transaction, err := reader.Transaction(context.Background(), testTransactionHash(7).Hex())
	if err != nil {
		t.Fatal(err)
	}
	const maxUint256 = "115792089237316195423570985008687907853269984665640564039457584007913129639935"
	if transaction.Value != maxUint256 || transaction.Nonce != "15" || transaction.Gas != "21000" {
		t.Fatalf("decimal transaction quantities = %+v", transaction)
	}
	if transaction.From != testTransactionSender().Hex() || transaction.To == nil || *transaction.To != "0xde709f2102306220921060314715629080e2fb77" {
		t.Fatalf("transaction addresses = %s -> %v", transaction.From, transaction.To)
	}
	if transaction.ContractAddress != nil {
		t.Fatalf("contract_address = %v, want nil for non-creation transaction", transaction.ContractAddress)
	}
	if transaction.Type == nil || *transaction.Type != "2" || transaction.Status == nil || *transaction.Status != gen.TransactionStatusSuccess {
		t.Fatalf("transaction type/status = %v/%v", transaction.Type, transaction.Status)
	}
	if transaction.Finality != gen.FinalitySafe || !transaction.Canonical {
		t.Fatalf("transaction canonicality = %+v", transaction)
	}

	if transaction.EffectiveGasPrice == nil || *transaction.EffectiveGasPrice != "2000000000" {
		t.Fatalf("effective_gas_price = %v, want %s", transaction.EffectiveGasPrice, "2000000000")
	}
	if transaction.TxFeeWei == nil || *transaction.TxFeeWei != "42000000000000" {
		t.Fatalf("tx_fee_wei = %v, want %s", transaction.TxFeeWei, "42000000000000")
	}
	if transaction.BurnedWei == nil || *transaction.BurnedWei != "21000000000000" {
		t.Fatalf("burned_wei = %v, want %s", transaction.BurnedWei, "21000000000000")
	}
	if transaction.BaseFeePerGas == nil || *transaction.BaseFeePerGas != "1000000000" {
		t.Fatalf("base_fee_per_gas = %v, want %s", transaction.BaseFeePerGas, "1000000000")
	}
	if transaction.BlobBaseFeePerGas != nil {
		t.Fatalf("blob_base_fee_per_gas = %v, want nil", transaction.BlobBaseFeePerGas)
	}
	if transaction.GasUsed == nil || *transaction.GasUsed != "21000" {
		t.Fatalf("gas_used = %v, want %s", transaction.GasUsed, "21000")
	}
	if transaction.BlockTimestamp == nil || transaction.BlockTimestamp.Unix() != 100 {
		t.Fatalf("block_timestamp = %v", transaction.BlockTimestamp)
	}
	if transaction.Confirmations == nil || *transaction.Confirmations != "1" {
		t.Fatalf("confirmations = %v", transaction.Confirmations)
	}
}

func TestTransactionReturnsOnlySuccessfulReceiptContractAddress(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name            string
		status          string
		wantContract    bool
		transactionSeed byte
	}{
		{name: "successful creation", status: "0x1", wantContract: true, transactionSeed: 21},
		{name: "failed creation", status: "0x0", transactionSeed: 22},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			transaction := testContractCreationTransaction(testCase.transactionSeed)
			db := testDatabase(t, queryExpectation{
				contains: "SELECT canonical.number::text, canonical.block_hash", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}},
			}, queryExpectation{
				contains: "FROM transaction_inclusions AS inclusion", columns: columns(11),
				rows: [][]driver.Value{{
					testContractCreationTransactionRaw(2, 3, testCase.transactionSeed, 0),
					testContractCreationReceiptRaw(2, 3, transaction, 0, testCase.status),
					"2", testHashBytes(3), int64(0), transaction.Hash().Bytes(), true, "2", "1",
					"100", "0x3b9aca00",
				}},
			})
			reader := testReader(t, db, Options{ChainID: 1})
			model, err := reader.Transaction(context.Background(), transaction.Hash().Hex())
			if err != nil {
				t.Fatal(err)
			}
			if model.To != nil {
				t.Fatalf("to = %v, want nil for contract creation", model.To)
			}
			if !testCase.wantContract {
				if model.ContractAddress != nil {
					t.Fatalf("contract_address = %v, want nil", model.ContractAddress)
				}
				return
			}
			expected := crypto.CreateAddress(testTransactionSender(), transaction.Nonce()).Hex()
			if model.ContractAddress == nil || *model.ContractAddress != expected {
				t.Fatalf("contract_address = %v, want %s", model.ContractAddress, expected)
			}
		})
	}
}

func TestTransactionLegacyTransactionRetainsGasPriceAndClearsBurnedWithoutBaseFee(t *testing.T) {
	t.Parallel()
	db := testDatabase(t, queryExpectation{
		contains: "SELECT canonical.number::text, canonical.block_hash", columns: columns(2), rows: [][]driver.Value{{"1", testHashBytes(3)}},
	}, queryExpectation{
		contains: "FROM transaction_inclusions AS inclusion", columns: columns(11),
		rows: [][]driver.Value{{
			testLegacyTransactionRaw(1, 3, 11), testLegacyReceiptRawAt(1, 3, 11, 0, "0x1"),
			"1", testHashBytes(3), int64(0), testLegacyTransactionHashBytes(11), true, "1", "0", "100", nil,
		}},
	})
	reader := testReader(t, db, Options{ChainID: 1})
	transaction, err := reader.Transaction(context.Background(), testLegacyTransactionHash(11).Hex())
	if err != nil {
		t.Fatal(err)
	}
	if transaction.EffectiveGasPrice == nil || *transaction.EffectiveGasPrice != "1000000000" {
		t.Fatalf("effective_gas_price = %v, want %s", transaction.EffectiveGasPrice, "1000000000")
	}
	if transaction.TxFeeWei == nil || *transaction.TxFeeWei != "21000000000000" {
		t.Fatalf("tx_fee_wei = %v, want %s", transaction.TxFeeWei, "21000000000000")
	}
	if transaction.BurnedWei == nil || *transaction.BurnedWei != "0" {
		t.Fatalf("burned_wei = %v, want %s", transaction.BurnedWei, "0")
	}
	if transaction.BaseFeePerGas != nil || transaction.BlobBaseFeePerGas != nil {
		t.Fatalf("pre-London base fees = %v/%v, want nil/nil", transaction.BaseFeePerGas, transaction.BlobBaseFeePerGas)
	}
}

func TestTransactionDoesNotReturnConfirmationsForOrphan(t *testing.T) {
	t.Parallel()
	db := testDatabase(t, queryExpectation{
		contains: "SELECT canonical.number::text, canonical.block_hash", columns: columns(2), rows: [][]driver.Value{{"3", testHashBytes(3)}},
	}, queryExpectation{
		contains: "FROM transaction_inclusions AS inclusion", columns: columns(11),
		rows: [][]driver.Value{{
			testTransactionRaw(1, 2, 5), testReceiptRawAt(1, 2, 5, 0, "0x1"),
			"1", testHashBytes(2), int64(0), testTransactionHashBytes(5), false, "1", "0", "100", "0x3b9aca00",
		}},
	})
	reader := testReader(t, db, Options{ChainID: 1})
	transaction, err := reader.Transaction(context.Background(), testTransactionHash(5).Hex())
	if err != nil {
		t.Fatal(err)
	}
	if transaction.Confirmations != nil {
		t.Fatalf("confirmations = %v", transaction.Confirmations)
	}
	if transaction.BaseFeePerGas == nil || *transaction.BaseFeePerGas != "1000000000" {
		t.Fatalf("orphan base_fee_per_gas = %v, want %s", transaction.BaseFeePerGas, "1000000000")
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
		queryExpectation{contains: "ORDER BY canonical.number DESC", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{contains: "search_catalog_generations", columns: columns(2), rows: [][]driver.Value{{int64(7), int64(1)}}},
		queryExpectation{contains: "FROM search_catalog_documents AS document", columns: columns(6)},
		queryExpectation{contains: "ORDER BY canonical.number DESC", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{contains: "search_catalog_generations", columns: columns(2), rows: [][]driver.Value{{int64(7), int64(1)}}},
		queryExpectation{contains: "canonical.number = $2::numeric", columns: columns(4), rows: [][]driver.Value{{
			"2", testHashBytes(3), "Canonical block two", int64(110),
		}}},
		queryExpectation{contains: "ORDER BY canonical.number DESC", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{contains: "search_catalog_generations", columns: columns(2), rows: [][]driver.Value{{int64(7), int64(1)}}},
		queryExpectation{contains: "SELECT kind, key, label, rank, canonical", columns: columns(5), rows: [][]driver.Value{
			{"block", testHash(3), "Block hash label", int64(110), false},
			{"transaction", testHash(3), "Transaction hash label", int64(110), true},
		}},
	)
	reader := testReader(t, db, Options{ChainID: 1})
	addressResults, _, err := reader.Search(context.Background(), "0x52908400098527886e0f7030069857d2e4169ee7", "", 20)
	if err != nil || len(addressResults) != 1 || addressResults[0].Kind != gen.SearchResultKindAddress {
		t.Fatalf("address search = %+v, error = %v", addressResults, err)
	}
	blockResults, _, err := reader.Search(context.Background(), "2", "", 20)
	if err != nil || len(blockResults) != 1 || blockResults[0].Key != testHash(3) ||
		blockResults[0].Label != "Canonical block two" || blockResults[0].Rank != 110 {
		t.Fatalf("block search = %+v, error = %v", blockResults, err)
	}
	hashResults, _, err := reader.Search(context.Background(), testHash(3), "", 20)
	if err != nil || len(hashResults) != 2 || hashResults[0].Label != "Block hash label" ||
		hashResults[1].Label != "Transaction hash label" || hashResults[0].Canonical == nil || *hashResults[0].Canonical {
		t.Fatalf("hash search = %+v, error = %v", hashResults, err)
	}
}

func TestSearchTreatsLowNumericAddressAsAddressBeforeBlockNumber(t *testing.T) {
	t.Parallel()
	address := "0x00000000000000000000000000000000000026c0"
	db := testDatabase(t,
		queryExpectation{contains: "ORDER BY canonical.number DESC", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{contains: "search_catalog_generations", columns: columns(2), rows: [][]driver.Value{{int64(7), int64(1)}}},
		queryExpectation{
			contains: "FROM search_catalog_documents AS document", columns: columns(6),
			rows: [][]driver.Value{{"contract", address, "Artifact", int64(104), nil, nil}},
		},
	)
	reader := testReader(t, db, Options{ChainID: 1})
	results, _, err := reader.Search(context.Background(), address, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Kind != gen.SearchResultKindContract ||
		results[0].Label != "Artifact" || results[0].Rank != 104 {
		t.Fatalf("low numeric address search = %+v", results)
	}
}

func TestSearchCoversCanonicalNamesTokensContractsAndLabels(t *testing.T) {
	t.Parallel()
	tokenAddress := "0x5aAe" + "b6053F3E94C9b9A09f33669435E7Ef1BeAed"
	db := testDatabase(t,
		queryExpectation{contains: "ORDER BY canonical.number DESC", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{contains: "search_catalog_generations", columns: columns(2), rows: [][]driver.Value{{int64(7), int64(1)}}},
		queryExpectation{
			contains: "FROM search_catalog_documents AS document", columns: columns(6),
			rows: [][]driver.Value{
				{"contract", "0x52908400098527886e0f7030069857d2e4169ee7", "Treasury", int64(110), nil, nil},
				{"address", "0xde709f2102306220921060314715629080e2fb77", "alice.eth", int64(100), true, "ens"},
				{"token", tokenAddress, "Example Token", int64(65), true, nil},
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

func TestSearchVerifiedContractWinnerUsesCanonicalPublicationOrder(t *testing.T) {
	t.Parallel()
	query := compactSQL(dbgen.QuerySearchText)
	for _, required := range []string{
		"LEFT JOIN verified_contract_proxy_artifacts AS proxy_artifact",
		"proxy_artifact.verification_job_id IS NOT NULL AS verification_proxy_artifact",
		"verification_proxy_artifact DESC",
		"document.verification_match_type",
		"document.verification_request_digest",
		"document.verification_job_id",
		"document.verification_job_id IS NOT NULL",
		"(verification_match_type = 'full') DESC NULLS LAST",
		"verification_valid_from_block DESC NULLS LAST",
		"verification_request_digest ASC NULLS LAST",
		"verification_job_id ASC NULLS LAST",
	} {
		if !strings.Contains(query, compactSQL(required)) {
			t.Fatalf("search verified-contract ordering lacks %q: %s", required, query)
		}
	}
}

func TestSearchRejectsMalformedPersistedEntityKey(t *testing.T) {
	t.Parallel()
	db := testDatabase(t,
		queryExpectation{contains: "ORDER BY canonical.number DESC", columns: columns(2), rows: [][]driver.Value{{"2", testHashBytes(3)}}},
		queryExpectation{contains: "search_catalog_generations", columns: columns(2), rows: [][]driver.Value{{int64(7), int64(1)}}},
		queryExpectation{
			contains: "FROM search_catalog_documents AS document", columns: columns(6),
			rows: [][]driver.Value{{"transaction", "not-a-hash", "bad", int64(80), nil, nil}},
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
		contains: "FROM search_catalog_documents AS document", columns: columns(6),
		rows: [][]driver.Value{{"transaction", "not-a-hash", "bad", int64(80), nil, nil}},
	})
	reader := testReader(t, db, Options{ChainID: 1})
	results, err := reader.searchText(context.Background(), db, "bad", 2, 7, 0, nil, 20)
	if err == nil || !strings.Contains(err.Error(), "invalid transaction") {
		t.Fatalf("results=%+v error=%v", results, err)
	}
}

type controlledNameResolverError struct {
	capability string
	state      string
	code       string
	message    string
}

func (err controlledNameResolverError) Error() string { return err.message }

func (err controlledNameResolverError) CapabilityDetails() (string, string, string) {
	return err.capability, err.state, err.code
}

type failingNameResolver struct{ err error }

func (resolver failingNameResolver) ResolveForward(context.Context, string) (ensresolver.ForwardResolution, error) {
	return ensresolver.ForwardResolution{}, resolver.err
}

func TestSearchMapsOnlyControlledNameCapabilityDetails(t *testing.T) {
	t.Parallel()
	secret := "https://operator:secret@example.invalid/private"
	for _, test := range []struct {
		name     string
		resolver NameResolver
		state    string
		code     string
	}{
		{name: "not configured", state: "unavailable", code: "not_configured"},
		{
			name: "controlled adapter failure",
			resolver: failingNameResolver{err: controlledNameResolverError{
				capability: "name", state: "unavailable", code: "unsafe_url", message: secret,
			}},
			state: "unavailable", code: "unsafe_url",
		},
		{
			name:     "arbitrary resolver failure",
			resolver: failingNameResolver{err: errors.New(secret)},
			state:    "failed", code: "resolver_failure",
		},
		{
			name: "unrecognized controlled code",
			resolver: failingNameResolver{err: controlledNameResolverError{
				capability: "name", state: "failed", code: "upstream_credential", message: secret,
			}},
			state: "failed", code: "resolver_failure",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader := testReader(t, testDatabase(t), Options{ChainID: 1, NameResolver: test.resolver})
			_, _, err := reader.Search(context.Background(), "alice.eth", "", 20)
			var capability *httpapi.CapabilityUnavailableError
			if !errors.As(err, &capability) || capability.Capability != "name" ||
				capability.State != test.state || capability.Code != test.code {
				t.Fatalf("capability error=%#v", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("resolver text leaked through error: %q", err)
			}
		})
	}
}

func TestSearchBoundaryUsesNormalizedIdentityInsteadOfChecksumOrdering(t *testing.T) {
	t.Parallel()
	first, second := checksumOrderInversion(t)
	if (strings.ToLower(first) >= strings.ToLower(second)) || (first <= second) {
		t.Fatalf("test addresses do not invert ordering: first=%s second=%s", first, second)
	}
	cursor := searchCursor{AfterRank: 100, AfterKind: string(gen.SearchResultKindAddress), AfterKey: first}
	result := gen.SearchResult{Rank: 100, Kind: gen.SearchResultKindAddress, Key: second}
	if !afterSearchBoundary(result, cursor) {
		t.Fatalf("normalized address %s was skipped after %s", second, first)
	}
}

func checksumOrderInversion(t *testing.T) (string, string) {
	t.Helper()
	type candidate struct {
		normalized string
		checksum   string
	}
	candidates := make([]candidate, 0, 512)
	for value := uint64(1); value <= 512; value++ {
		normalized := fmt.Sprintf("0x%040x", value)
		checksum, err := ChecksumAddress(normalized)
		if err != nil {
			t.Fatal(err)
		}
		for _, previous := range candidates {
			if previous.normalized < normalized && previous.checksum > checksum {
				return previous.checksum, checksum
			}
		}
		candidates = append(candidates, candidate{normalized: normalized, checksum: checksum})
	}
	t.Fatal("failed to find EIP-55 checksum ordering inversion")
	return "", ""
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

func testAddressBytes(value byte) []byte {
	result := make([]byte, common.AddressLength)
	result[len(result)-1] = value
	return result
}

func testBlockProjectionRow(
	number uint64,
	hash, parent byte,
	transactionCount int64,
	canonical bool,
	safe, finalized any,
) []driver.Value {
	return []driver.Value{
		strconv.FormatUint(number, 10), testHashBytes(hash), testHashBytes(parent), "100",
		"0x52908400098527886e0f7030069857d2e4169ee7",
		"0x5208", "0x1c9c380", "0x3b9aca00",
		transactionCount, transactionCount,
		false, nil, []byte("[]"), canonical, safe, finalized,
	}
}

func testTransactionRaw(blockNumber uint64, blockHash, transactionHash byte) []byte {
	return testTransactionRawAt(blockNumber, blockHash, transactionHash, 0)
}

func testTransactionRawAt(blockNumber uint64, blockHash, transactionHash byte, transactionIndex uint64) []byte {
	transaction := testSignedTransaction(transactionHash)
	return testTransactionRawFor(transaction, blockNumber, blockHash, transactionIndex)
}

func testContractCreationTransactionRaw(blockNumber uint64, blockHash, transactionSeed byte, transactionIndex uint64) []byte {
	return testTransactionRawFor(
		testContractCreationTransaction(transactionSeed),
		blockNumber,
		blockHash,
		transactionIndex,
	)
}

func testTransactionRawFor(transaction *types.Transaction, blockNumber uint64, blockHash byte, transactionIndex uint64) []byte {
	encoded, err := transaction.MarshalJSON()
	if err != nil {
		panic(err)
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		panic(err)
	}
	value["blockHash"] = testHash(blockHash)
	value["blockNumber"] = fmt.Sprintf("0x%x", blockNumber)
	value["transactionIndex"] = fmt.Sprintf("0x%x", transactionIndex)
	value["from"] = testTransactionSender().Hex()
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
	return testReceiptRawAtHash(blockNumber, blockHash, testTransactionHash(transactionHash).Hex(), "0x2", transactionIndex, status)
}

func testLegacyReceiptRawAt(blockNumber uint64, blockHash, transactionHash byte, transactionIndex uint64, status string) []byte {
	return testReceiptRawAtHash(blockNumber, blockHash, testLegacyTransactionHash(transactionHash).Hex(), "0x0", transactionIndex, status)
}

func testReceiptRawAtHash(blockNumber uint64, blockHash byte, transactionHash string, transactionType string, transactionIndex uint64, status string) []byte {
	value := map[string]any{
		"transactionHash":   transactionHash,
		"transactionIndex":  fmt.Sprintf("0x%x", transactionIndex),
		"blockHash":         testHash(blockHash),
		"blockNumber":       fmt.Sprintf("0x%x", blockNumber),
		"cumulativeGasUsed": "0x5208",
		"gasUsed":           "0x5208",
		"contractAddress":   nil,
		"logs":              []any{},
		"logsBloom":         "0x" + strings.Repeat("00", types.BloomByteLength),
		"status":            status,
		"type":              transactionType,
	}
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func testContractCreationReceiptRaw(
	blockNumber uint64,
	blockHash byte,
	transaction *types.Transaction,
	transactionIndex uint64,
	status string,
) []byte {
	data := testReceiptRawAtHash(
		blockNumber,
		blockHash,
		transaction.Hash().Hex(),
		"0x2",
		transactionIndex,
		status,
	)
	if status != "0x1" {
		return data
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		panic(err)
	}
	value["contractAddress"] = crypto.CreateAddress(
		testTransactionSender(),
		transaction.Nonce(),
	).Hex()
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func testSignedTransaction(seed byte) *types.Transaction {
	key, err := crypto.HexToECDSA("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		panic(err)
	}
	to := common.HexToAddress("0xde709f2102306220921060314715629080e2fb77")
	transaction := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(1),
		Nonce:     uint64(seed) + 8,
		GasTipCap: big.NewInt(1_000_000_000),
		GasFeeCap: big.NewInt(2_000_000_000),
		Gas:       21_000,
		To:        &to,
		Value:     new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)),
		Data:      []byte{0xde, 0xad, 0xbe, 0xef},
	})
	signed, err := types.SignTx(transaction, types.LatestSignerForChainID(big.NewInt(1)), key)
	if err != nil {
		panic(err)
	}
	return signed
}

func testContractCreationTransaction(seed byte) *types.Transaction {
	key, err := crypto.HexToECDSA("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		panic(err)
	}
	transaction := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(1),
		Nonce:     uint64(seed) + 8,
		GasTipCap: big.NewInt(1_000_000_000),
		GasFeeCap: big.NewInt(2_000_000_000),
		Gas:       21_000,
		Value:     big.NewInt(0),
		Data:      []byte{0x60, 0x00},
	})
	signed, err := types.SignTx(transaction, types.LatestSignerForChainID(big.NewInt(1)), key)
	if err != nil {
		panic(err)
	}
	return signed
}

func testLegacyTransaction(seed byte) *types.Transaction {
	key, err := crypto.HexToECDSA("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		panic(err)
	}
	to := common.HexToAddress("0xde709f2102306220921060314715629080e2fb77")
	transaction := types.NewTx(&types.LegacyTx{
		Nonce:    uint64(seed) + 9,
		GasPrice: big.NewInt(1_000_000_000),
		Gas:      21_000,
		To:       &to,
		Value:    new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)),
		Data:     []byte{0xde, 0xad, 0xbe, 0xef},
	})
	signed, err := types.SignTx(transaction, types.LatestSignerForChainID(big.NewInt(1)), key)
	if err != nil {
		panic(err)
	}
	return signed
}

func testLegacyTransactionRaw(blockNumber uint64, blockHash, transactionHash byte) []byte {
	return testLegacyTransactionRawAt(blockNumber, blockHash, transactionHash, 0)
}

func testLegacyTransactionRawAt(blockNumber uint64, blockHash, transactionHash byte, transactionIndex uint64) []byte {
	transaction := testLegacyTransaction(transactionHash)
	encoded, err := transaction.MarshalJSON()
	if err != nil {
		panic(err)
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		panic(err)
	}
	value["blockHash"] = testHash(blockHash)
	value["blockNumber"] = fmt.Sprintf("0x%x", blockNumber)
	value["transactionIndex"] = fmt.Sprintf("0x%x", transactionIndex)
	value["from"] = testTransactionSender().Hex()
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func testTransactionHash(seed byte) common.Hash {
	return testSignedTransaction(seed).Hash()
}

func testTransactionHashBytes(seed byte) []byte {
	return testTransactionHash(seed).Bytes()
}

func testLegacyTransactionHash(seed byte) common.Hash {
	return testLegacyTransaction(seed).Hash()
}

func testLegacyTransactionHashBytes(seed byte) []byte {
	return testLegacyTransactionHash(seed).Bytes()
}

func testTransactionSender() common.Address {
	key, err := crypto.HexToECDSA("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		panic(err)
	}
	return crypto.PubkeyToAddress(key.PublicKey)
}
