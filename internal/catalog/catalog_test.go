package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var catalogDriverSequence atomic.Uint64

type catalogQueryStep struct {
	contains string
	rows     driver.Rows
	check    func([]driver.NamedValue) error
}

type catalogSQLBackend struct {
	mu      sync.Mutex
	steps   []catalogQueryStep
	begins  []driver.TxOptions
	queries []string
}

type catalogSQLDriver struct{ backend *catalogSQLBackend }
type catalogSQLConn struct{ backend *catalogSQLBackend }
type catalogSQLTx struct{}

func (database catalogSQLDriver) Open(string) (driver.Conn, error) {
	return &catalogSQLConn{backend: database.backend}, nil
}

func (connection *catalogSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are unsupported by the catalog SQL fake")
}

func (connection *catalogSQLConn) Close() error { return nil }
func (connection *catalogSQLConn) Begin() (driver.Tx, error) {
	return connection.BeginTx(context.Background(), driver.TxOptions{})
}

func (connection *catalogSQLConn) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	connection.backend.mu.Lock()
	defer connection.backend.mu.Unlock()
	connection.backend.begins = append(connection.backend.begins, options)
	return catalogSQLTx{}, nil
}

func (connection *catalogSQLConn) QueryContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
	connection.backend.mu.Lock()
	defer connection.backend.mu.Unlock()
	connection.backend.queries = append(connection.backend.queries, query)
	if len(connection.backend.steps) == 0 {
		return nil, fmt.Errorf("unexpected query: %s", query)
	}
	step := connection.backend.steps[0]
	connection.backend.steps = connection.backend.steps[1:]
	if !strings.Contains(query, step.contains) {
		return nil, fmt.Errorf("query %q does not contain %q", query, step.contains)
	}
	if step.check != nil {
		if err := step.check(arguments); err != nil {
			return nil, err
		}
	}
	return step.rows, nil
}

func (catalogSQLTx) Commit() error   { return nil }
func (catalogSQLTx) Rollback() error { return nil }

type catalogSQLRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (rows *catalogSQLRows) Columns() []string { return rows.columns }
func (rows *catalogSQLRows) Close() error      { return nil }
func (rows *catalogSQLRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.values) {
		return io.EOF
	}
	copy(destination, rows.values[rows.index])
	rows.index++
	return nil
}

func catalogRows(columnCount int, values ...[]driver.Value) driver.Rows {
	columns := make([]string, columnCount)
	for index := range columns {
		columns[index] = fmt.Sprintf("column_%d", index)
	}
	return &catalogSQLRows{columns: columns, values: values}
}

func openCatalog(t *testing.T, steps ...catalogQueryStep) (*Postgres, *catalogSQLBackend) {
	return openCatalogWithOptions(t, Options{}, steps...)
}

func openCatalogWithOptions(t *testing.T, options Options, steps ...catalogQueryStep) (*Postgres, *catalogSQLBackend) {
	t.Helper()
	backend := &catalogSQLBackend{steps: steps}
	name := fmt.Sprintf("etherview_catalog_fake_%d", catalogDriverSequence.Add(1))
	sql.Register(name, catalogSQLDriver{backend: backend})
	database, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	options.DefaultPageSize = 2
	options.MaxPageSize = 10
	options.MaxChartPoints = 10
	options.MaxTraceFrames = 10
	options.MaxTextBytes = 1024
	catalog, err := NewPostgres(database, options)
	if err != nil {
		t.Fatal(err)
	}
	return catalog, backend
}

type fakeNFTState struct {
	owner        NFTOwnerObservation
	balances     []NFTBalanceObservation
	ownerCalls   int
	balanceCalls int
	snapshot     Snapshot
	candidates   []NFTBalanceCandidate
}

func (state *fakeNFTState) Owner(_ context.Context, snapshot Snapshot, _, _ string) (NFTOwnerObservation, error) {
	state.ownerCalls++
	state.snapshot = snapshot
	return state.owner, nil
}

func (state *fakeNFTState) Balances(
	_ context.Context,
	snapshot Snapshot,
	_ string,
	candidates []NFTBalanceCandidate,
) ([]NFTBalanceObservation, error) {
	state.balanceCalls++
	state.snapshot = snapshot
	state.candidates = append([]NFTBalanceCandidate(nil), candidates...)
	return append([]NFTBalanceObservation(nil), state.balances...), nil
}

func assertCatalogConsumed(t *testing.T, backend *catalogSQLBackend) {
	t.Helper()
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.steps) != 0 {
		t.Fatalf("%d SQL expectations were not consumed", len(backend.steps))
	}
	for _, options := range backend.begins {
		if !options.ReadOnly || options.Isolation != driver.IsolationLevel(sql.LevelRepeatableRead) {
			t.Fatalf("catalog transaction options=%+v", options)
		}
	}
}

func bytesOf(value byte, length int) []byte {
	return []byte(strings.Repeat(string([]byte{value}), length))
}

func snapshotStep(number string, hash []byte) catalogQueryStep {
	return catalogQueryStep{contains: "ORDER BY number DESC", rows: catalogRows(2, []driver.Value{number, hash})}
}

func stageStep(state string) catalogQueryStep {
	return catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(1, []driver.Value{state})}
}

func traceStageStep(state string) catalogQueryStep {
	return catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(3, []driver.Value{state, int64(42), int64(3)})}
}

func tokenRow(address []byte, standard, blockNumber string) []driver.Value {
	return []driver.Value{
		"1", address, bytesOf(0x44, 32), standard, "high", "Token", "TKN", int64(18),
		"115792089237316195423570985008687907853269984665640564039457584007913129639935",
		"complete", blockNumber, bytesOf(0x33, 32), time.Unix(1_700_000_000, 0).UTC(),
	}
}

func TestChecksumAddressAndCursorRejectsUnknownFields(t *testing.T) {
	address, _, err := checksumInputAddress("0x52908400098527886e0f7030069857d2e4169ee7")
	if err != nil {
		t.Fatal(err)
	}
	checksummed, err := checksumAddressBytes(address)
	if err != nil || checksummed != "0x52908400098527886E0F7030069857D2E4169EE7" {
		t.Fatalf("checksummed=%q err=%v", checksummed, err)
	}
	raw := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"unknown":true}`))
	var cursor tokenListCursor
	if err := decodeCursor(raw, &cursor); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cursor error=%v", err)
	}
}

func TestTokenContractsUseCanonicalSnapshotAndOpaqueCursor(t *testing.T) {
	addresses := [][]byte{bytesOf(0x11, 20), bytesOf(0x22, 20), bytesOf(0x33, 20)}
	catalog, backend := openCatalog(t,
		snapshotStep("100", bytesOf(0xaa, 32)),
		stageStep("complete"),
		catalogQueryStep{
			contains: "WITH current_tokens AS",
			rows:     catalogRows(13, tokenRow(addresses[0], "erc20", "98"), tokenRow(addresses[1], "erc721", "99"), tokenRow(addresses[2], "erc1155", "100")),
			check: func(arguments []driver.NamedValue) error {
				if len(arguments) != 5 || arguments[2].Value != false || arguments[4].Value != int64(3) {
					return fmt.Errorf("unexpected token list arguments: %v", arguments)
				}
				return nil
			},
		},
	)
	page, err := catalog.TokenContracts(context.Background(), TokenListRequest{ChainID: "1", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.NextCursor == "" || page.Snapshot.BlockNumber != "100" {
		t.Fatalf("page=%+v", page)
	}
	if page.Items[0].Address != "0x1111111111111111111111111111111111111111" || page.Items[0].TotalSupply == nil ||
		*page.Items[0].TotalSupply != "115792089237316195423570985008687907853269984665640564039457584007913129639935" {
		t.Fatalf("token=%+v", page.Items[0])
	}
	backend.mu.Lock()
	query := backend.queries[len(backend.queries)-1]
	backend.mu.Unlock()
	if !strings.Contains(query, "JOIN canonical_blocks") || !strings.Contains(query, "observed_block_number <= $2") {
		t.Fatalf("token list is not canonical/snapshot bound: %s", query)
	}
	assertCatalogConsumed(t, backend)

	var decoded tokenListCursor
	if err := decodeCursor(page.NextCursor, &decoded); err != nil || decoded.AfterAddress != "0x"+strings.Repeat("22", 20) {
		t.Fatalf("cursor=%+v err=%v", decoded, err)
	}
}

func TestTokenContractDistinguishesMissingStageFromNotFound(t *testing.T) {
	address := "0x" + strings.Repeat("11", 20)
	t.Run("missing stage", func(t *testing.T) {
		catalog, backend := openCatalog(t,
			snapshotStep("10", bytesOf(0xaa, 32)),
			catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(1)},
		)
		_, err := catalog.TokenContract(context.Background(), "1", address)
		var stageError StageUnavailableError
		if !errors.Is(err, ErrUnavailable) || !errors.As(err, &stageError) || stageError.Stage != StageToken || stageError.State != StageMissing {
			t.Fatalf("error=%#v", err)
		}
		assertCatalogConsumed(t, backend)
	})
	t.Run("completed stage", func(t *testing.T) {
		catalog, backend := openCatalog(t,
			snapshotStep("10", bytesOf(0xaa, 32)), stageStep("complete"),
			catalogQueryStep{contains: "FROM token_contracts", rows: catalogRows(13)},
		)
		_, err := catalog.TokenContract(context.Background(), "1", address)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("error=%v", err)
		}
		assertCatalogConsumed(t, backend)
	})
}

func tokenEventRow(blockNumber, logIndex, subIndex string, tokenAddress []byte) []driver.Value {
	return []driver.Value{
		"1", blockNumber, bytesOf(byte(blockNumber[0]), 32), logIndex, subIndex,
		bytesOf(0xbb, 32), tokenAddress, "erc721", "transfer", nil,
		bytesOf(0x11, 20), bytesOf(0x22, 20), "99", "1", "high",
	}
}

func TestTokenEventsAreCanonicalAndCursorBecomesStaleAfterReorg(t *testing.T) {
	token := bytesOf(0xcc, 20)
	catalog, backend := openCatalog(t,
		snapshotStep("100", bytesOf(0xaa, 32)), stageStep("complete"),
		catalogQueryStep{
			contains: "FROM token_events AS e",
			rows: catalogRows(15,
				tokenEventRow("100", "7", "1", token),
				tokenEventRow("99", "6", "0", token),
				tokenEventRow("98", "5", "0", token),
			),
			check: func(arguments []driver.NamedValue) error {
				if arguments[3].Value != false || arguments[4].Value != "0" || arguments[5].Value != "0" {
					return fmt.Errorf("first page has unsafe cursor defaults: %v", arguments)
				}
				return nil
			},
		},
	)
	page, err := catalog.TokenEvents(context.Background(), TokenEventRequest{
		ChainID: "1", TokenAddress: "0x" + strings.Repeat("cc", 20), Limit: 2,
	})
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if page.Items[0].BlockNumber != "100" || page.Items[0].TokenID == nil || *page.Items[0].TokenID != "99" {
		t.Fatalf("event=%+v", page.Items[0])
	}
	backend.mu.Lock()
	query := backend.queries[len(backend.queries)-1]
	backend.mu.Unlock()
	if !strings.Contains(query, "e.canonical = true") || !strings.Contains(query, "JOIN canonical_blocks") {
		t.Fatalf("event query lacks canonical guards: %s", query)
	}
	assertCatalogConsumed(t, backend)

	reorged, reorgBackend := openCatalog(t,
		catalogQueryStep{contains: "SELECT EXISTS", rows: catalogRows(1, []driver.Value{false})},
	)
	_, err = reorged.TokenEvents(context.Background(), TokenEventRequest{
		ChainID: "1", TokenAddress: "0x" + strings.Repeat("cc", 20), Cursor: page.NextCursor, Limit: 2,
	})
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("reorg cursor error=%v", err)
	}
	assertCatalogConsumed(t, reorgBackend)
}

func TestNFTOwnerAndBalancesRequireExactCanonicalReconciliation(t *testing.T) {
	token := bytesOf(0xcc, 20)
	t.Run("owner", func(t *testing.T) {
		state := &fakeNFTState{owner: NFTOwnerObservation{
			Exists: true, Owner: "0x" + strings.Repeat("12", 20), Confidence: NFTStateConfidenceRPCExact,
		}}
		catalog, backend := openCatalogWithOptions(t, Options{NFTState: state},
			snapshotStep("100", bytesOf(0xaa, 32)), stageStep("complete"),
			catalogQueryStep{contains: "FROM token_contracts", rows: catalogRows(13, tokenRow(token, "erc721", "90"))},
		)
		ownership, err := catalog.NFTOwner(context.Background(), "1", "0x"+strings.Repeat("cc", 20), "99")
		if err != nil || ownership.Balance != "1" || ownership.Owner == "" ||
			ownership.Confidence != NFTStateConfidenceRPCExact || state.ownerCalls != 1 || state.snapshot.BlockHash != "0x"+strings.Repeat("aa", 32) {
			t.Fatalf("ownership=%+v err=%v", ownership, err)
		}
		for _, query := range backend.queries {
			if strings.Contains(query, "token_balance_deltas") {
				t.Fatalf("ERC-721 owner was reconstructed from event deltas: %s", query)
			}
		}
		assertCatalogConsumed(t, backend)
	})
	t.Run("event candidates are replaced by exact balances", func(t *testing.T) {
		state := &fakeNFTState{balances: []NFTBalanceObservation{
			{Balance: "0", Confidence: NFTStateConfidenceRPCExact},
			{Balance: "340282366920938463463374607431768211455", Confidence: NFTStateConfidenceRPCExact},
		}}
		catalog, backend := openCatalogWithOptions(t, Options{NFTState: state},
			snapshotStep("100", bytesOf(0xaa, 32)), stageStep("complete"),
			catalogQueryStep{
				contains: "SELECT d.token_address",
				rows: catalogRows(2,
					[]driver.Value{token, "99"},
					[]driver.Value{bytesOf(0xdd, 20), "5"},
				),
			},
			catalogQueryStep{contains: "FROM token_contracts", rows: catalogRows(13, tokenRow(token, "erc721", "90"))},
			catalogQueryStep{contains: "FROM token_contracts", rows: catalogRows(13, tokenRow(bytesOf(0xdd, 20), "erc1155", "95"))},
		)
		page, err := catalog.NFTBalances(context.Background(), NFTBalanceRequest{
			ChainID: "1", Owner: "0x" + strings.Repeat("12", 20), Limit: 2,
		})
		if err != nil || len(page.Items) != 1 || page.Items[0].Balance != "340282366920938463463374607431768211455" ||
			page.Items[0].Confidence != NFTStateConfidenceRPCExact || state.balanceCalls != 1 || len(state.candidates) != 2 {
			t.Fatalf("page=%+v err=%v", page, err)
		}
		backend.mu.Lock()
		candidateQuery := backend.queries[2]
		backend.mu.Unlock()
		if !strings.Contains(candidateQuery, "d.canonical = true") || strings.Contains(candidateQuery, "HAVING SUM(d.delta)") {
			t.Fatalf("candidate query lacks canonical guards: %s", candidateQuery)
		}
		assertCatalogConsumed(t, backend)
	})
	t.Run("candidate deltas without reconciler are unavailable", func(t *testing.T) {
		catalog, backend := openCatalog(t,
			snapshotStep("100", bytesOf(0xaa, 32)), stageStep("complete"),
			catalogQueryStep{contains: "SELECT d.token_address", rows: catalogRows(2, []driver.Value{token, "99"})},
			catalogQueryStep{contains: "FROM token_contracts", rows: catalogRows(13, tokenRow(token, "erc721", "90"))},
		)
		_, err := catalog.NFTBalances(context.Background(), NFTBalanceRequest{
			ChainID: "1", Owner: "0x" + strings.Repeat("12", 20), Limit: 2,
		})
		var stageError StageUnavailableError
		if !errors.As(err, &stageError) || stageError.Stage != StageToken || stageError.State != StageUnavailable {
			t.Fatalf("error=%#v", err)
		}
		assertCatalogConsumed(t, backend)
	})
}

func statRow(block string) []driver.Value {
	return []driver.Value{
		"1", block, bytesOf(byte(block[0]), 32), "9007199254740993", "30000000", "60000000",
		"1000000000", "131072", "393216", "1000000001", "30000000000000000", "131072000131072",
		"1700000000", "12", "750599937895082.75", "8", "5", "3",
		time.Unix(1_700_000_000, 0).UTC(),
	}
}

func TestBlockStatsRequireCompleteCanonicalRange(t *testing.T) {
	catalog, backend := openCatalog(t,
		snapshotStep("101", bytesOf(0xaa, 32)),
		catalogQueryStep{contains: "WITH heights AS", rows: catalogRows(3)},
		catalogQueryStep{contains: "WITH heights AS", rows: catalogRows(3)},
		catalogQueryStep{contains: "FROM block_statistics AS stats", rows: catalogRows(19, statRow("100"), statRow("101"))},
	)
	stats, err := catalog.BlockStats(context.Background(), BlockStatsRequest{ChainID: "1", FromBlock: "100", ToBlock: "101"})
	if err != nil || len(stats) != 2 || stats[0].TransactionCount != "9007199254740993" ||
		stats[0].BaseFeePerGas == nil || stats[0].TransactionsPerSecond == nil || stats[0].NFTTransferCount != "3" {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	assertCatalogConsumed(t, backend)

	missing, missingBackend := openCatalog(t,
		snapshotStep("101", bytesOf(0xaa, 32)),
		catalogQueryStep{contains: "WITH heights AS", rows: catalogRows(3, []driver.Value{"100", bytesOf(0x64, 32), nil})},
	)
	_, err = missing.BlockStats(context.Background(), BlockStatsRequest{ChainID: "1", FromBlock: "100", ToBlock: "101"})
	var stageError StageUnavailableError
	if !errors.As(err, &stageError) || stageError.Stage != StageStats || stageError.State != StageMissing {
		t.Fatalf("missing stats error=%#v", err)
	}
	assertCatalogConsumed(t, missingBackend)
}

func traceRow(path string, parent driver.Value, depth int64, callType string) []driver.Value {
	return []driver.Value{
		path, parent, depth, callType, bytesOf(0x11, 20), bytesOf(0x22, 20), nil,
		"1", "1", "1", []byte{0x12, 0x34}, []byte{}, nil, false,
	}
}

func TestTransactionTraceSortsAndValidatesNormalizedTree(t *testing.T) {
	txHash, blockHash := bytesOf(0xbb, 32), bytesOf(0xaa, 32)
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(3, []driver.Value{"100", blockHash, "9007199254740993"})},
		traceStageStep("complete"),
		catalogQueryStep{contains: "FROM normalized_traces", rows: catalogRows(14,
			traceRow("", nil, 0, "CALL"),
			traceRow("10", "", 1, "CREATE2"),
			traceRow("2", "", 1, "DELEGATECALL"),
		)},
	)
	trace, err := catalog.TransactionTrace(context.Background(), "1", "0x"+strings.Repeat("bb", 32))
	if err != nil || len(trace.Frames) != 3 || trace.Frames[1].Path[0] != 2 || trace.Frames[2].Path[0] != 10 {
		t.Fatalf("trace=%+v err=%v", trace, err)
	}
	if trace.TransactionIndex != "9007199254740993" || trace.Frames[0].Value == nil || trace.Frames[0].Gas == nil || trace.Frames[0].GasUsed == nil ||
		*trace.Frames[0].Value != "1" || trace.Frames[0].Input == nil || *trace.Frames[0].Input != "0x1234" || trace.Frames[0].Output == nil || *trace.Frames[0].Output != "0x" {
		t.Fatalf("root frame=%+v", trace.Frames[0])
	}
	if trace.TransactionHash != "0x"+strings.Repeat("bb", 32) || !bytes.Equal(txHash, bytesOf(0xbb, 32)) {
		t.Fatalf("transaction hash=%s", trace.TransactionHash)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionTraceStageStateIsNotAnEmptyTrace(t *testing.T) {
	for _, state := range []StageState{StageMissing, StageUnavailable, StageFailed} {
		t.Run(string(state), func(t *testing.T) {
			stage := catalogQueryStep{contains: "FROM published_block_stage_results", rows: catalogRows(1)}
			if state != StageMissing {
				stage = traceStageStep(string(state))
			}
			catalog, backend := openCatalog(t,
				catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(3, []driver.Value{"100", bytesOf(0xaa, 32), "0"})},
				stage,
			)
			_, err := catalog.TransactionTrace(context.Background(), "1", "0x"+strings.Repeat("bb", 32))
			var stageError StageUnavailableError
			if !errors.Is(err, ErrUnavailable) || !errors.As(err, &stageError) || stageError.Stage != StageTrace || stageError.State != state {
				t.Fatalf("error=%#v", err)
			}
			assertCatalogConsumed(t, backend)
		})
	}
}

func TestTransactionTraceCompletedEmptyTreeIsCorrupt(t *testing.T) {
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(3, []driver.Value{"100", bytesOf(0xaa, 32), "0"})},
		traceStageStep("complete"),
		catalogQueryStep{contains: "FROM normalized_traces", rows: catalogRows(14)},
	)
	_, err := catalog.TransactionTrace(context.Background(), "1", "0x"+strings.Repeat("bb", 32))
	if !errors.Is(err, ErrCorruptData) {
		t.Fatalf("err=%v", err)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionTraceRootOnlyIsACompleteEmptyInternalCallTree(t *testing.T) {
	catalog, backend := openCatalog(t,
		catalogQueryStep{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(3, []driver.Value{"100", bytesOf(0xaa, 32), "0"})},
		traceStageStep("complete"),
		catalogQueryStep{contains: "FROM normalized_traces", rows: catalogRows(14, traceRow("", nil, 0, "CALL"))},
	)
	trace, err := catalog.TransactionTrace(context.Background(), "1", "0x"+strings.Repeat("bb", 32))
	if err != nil || trace.State != StageComplete || len(trace.Frames) != 1 || trace.Frames[0].Depth != 0 {
		t.Fatalf("trace=%+v err=%v", trace, err)
	}
	assertCatalogConsumed(t, backend)
}
