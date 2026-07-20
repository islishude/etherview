package etherscan

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
)

const (
	testSender    = "0x52908400098527886e0f7030069857d2e4169ee7"
	testRecipient = "0xde709f2102306220921060314715629080e2fb77"
	testContract  = "0x27b1fdb04752bbc536007a920d24acb045561c26"
)

func TestNewPostgresBackendValidatesConfiguration(t *testing.T) {
	t.Parallel()
	if _, err := NewPostgresBackend(nil, PostgresOptions{ChainID: 1}); err == nil {
		t.Fatal("nil database was accepted")
	}
	db := fakeDatabase(t)
	if _, err := NewPostgresBackend(db, PostgresOptions{}); err == nil {
		t.Fatal("zero chain ID was accepted")
	}
}

func TestUnavailableActionsNeverReturnEmptySuccess(t *testing.T) {
	t.Parallel()
	backend := testPostgresBackend(t, fakeDatabase(t), PostgresOptions{ChainID: 1})
	tests := []struct {
		module string
		action string
		want   error
		values url.Values
	}{
		{"account", "balance", ErrStateUnavailable, url.Values{"address": {testSender}}},
		{"account", "balancemulti", ErrStateUnavailable, url.Values{"address": {testSender + "," + testRecipient}}},
		{"stats", "ethprice", ErrPriceUnavailable, nil},
		{"stats", "ethsupply", ErrSupplyUnavailable, nil},
		{"contract", "verifysourcecode", ErrVerificationUnavailable, nil},
		{"contract", "checkverifystatus", ErrVerificationUnavailable, nil},
	}
	for _, test := range tests {
		result, err := backend.Execute(context.Background(), Request{Module: test.module, Action: test.action, Values: test.values})
		if !errors.Is(err, test.want) {
			t.Fatalf("%s.%s result=%#v error=%v, want %v", test.module, test.action, result, err, test.want)
		}
	}
}

func TestEthPriceUsesBoundedProviderObservation(t *testing.T) {
	t.Parallel()
	observedAt := time.Unix(1_700_000_000, 0).UTC()
	backend := testPostgresBackend(t, fakeDatabase(t), PostgresOptions{
		ChainID: 1,
		Price: func(context.Context) (NativePrice, error) {
			return NativePrice{USD: "3500.25", BTC: "0.05", ObservedAt: observedAt}, nil
		},
	})
	result, err := backend.Execute(context.Background(), Request{Module: "stats", Action: "ethprice"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"ethbtc":"0.05","ethbtc_timestamp":"1700000000","ethusd":"3500.25","ethusd_timestamp":"1700000000"}`
	if string(encoded) != want {
		t.Fatalf("price=%s want=%s", encoded, want)
	}
}

func TestAccountTransactionsAreCanonicalDecimalAndStable(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t, sqlExpectation{
		contains: "inclusion.block_number <= $4::numeric ORDER BY inclusion.block_number DESC, inclusion.tx_index DESC, inclusion.tx_hash DESC LIMIT $5 OFFSET $6",
		columns:  fakeColumns(8),
		rows: [][]driver.Value{{
			testTransactionJSON(10, 3, 7, 1, testRecipient),
			testReceiptJSON(10, 3, 7, 1, "0x1", ""),
			testBlockJSON(10, 3, 2, 100, testSender),
			"10", testHashBytes(3), int64(1), testHashBytes(7), "12",
		}},
		check: func(arguments []driver.NamedValue) error {
			want := []string{"1", strings.ToLower(testSender), "10", "20", "2", "2"}
			if len(arguments) != len(want) {
				return fmt.Errorf("arguments=%v", arguments)
			}
			for index := range arguments {
				if fmt.Sprint(arguments[index].Value) != want[index] {
					return fmt.Errorf("argument %d=%v, want %s", index, arguments[index].Value, want[index])
				}
			}
			return nil
		},
	})
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{Module: "account", Action: "txlist", Values: url.Values{
		"address": {testSender}, "startblock": {"10"}, "endblock": {"20"},
		"page": {"2"}, "offset": {"2"}, "sort": {"desc"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	transactions, ok := result.([]accountTransaction)
	if !ok || len(transactions) != 1 {
		t.Fatalf("result=%#v", result)
	}
	encoded, err := json.Marshal(transactions[0])
	if err != nil {
		t.Fatal(err)
	}
	want := `{"blockNumber":"10","timeStamp":"100","hash":"` + testHash(7) + `","nonce":"15","blockHash":"` + testHash(3) + `","transactionIndex":"1","from":"0x52908400098527886E0F7030069857D2E4169EE7","to":"0xde709f2102306220921060314715629080e2fb77","value":"16","gas":"21000","gasPrice":"2000000000","isError":"0","txreceipt_status":"1","input":"0xdeadbeef00","contractAddress":"","cumulativeGasUsed":"42000","gasUsed":"21000","confirmations":"3","methodId":"0xdeadbeef","functionName":""}`
	if string(encoded) != want {
		t.Fatalf("transaction JSON\n got: %s\nwant: %s", encoded, want)
	}
}

func TestAccountTransactionsRejectRawIdentityMismatch(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t, sqlExpectation{
		contains: "FROM transaction_inclusions AS inclusion",
		columns:  fakeColumns(8),
		rows: [][]driver.Value{{
			testTransactionJSON(10, 3, 99, 1, testRecipient),
			testReceiptJSON(10, 3, 7, 1, "0x1", ""), testBlockJSON(10, 3, 2, 100, testSender),
			"10", testHashBytes(3), int64(1), testHashBytes(7), "12",
		}},
	})
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	_, err := backend.Execute(context.Background(), Request{Module: "account", Action: "txlist", Values: url.Values{"address": {testSender}}})
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("error=%v", err)
	}
}

func TestMinedBlocksOmitsUnknownReward(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t, sqlExpectation{
		contains: "lower(block.raw->>'miner') = $2 ORDER BY block.number ASC, block.hash ASC",
		columns:  fakeColumns(3),
		rows:     [][]driver.Value{{testBlockJSON(10, 3, 2, 100, testSender), "10", testHashBytes(3)}},
	})
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{Module: "account", Action: "getminedblocks", Values: url.Values{"address": {testSender}}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if string(encoded) != `[{"blockNumber":"10","timeStamp":"100"}]` {
		t.Fatalf("result=%s", encoded)
	}
}

func TestMinedUnclesAreExplicitlyUnavailable(t *testing.T) {
	t.Parallel()
	backend := testPostgresBackend(t, fakeDatabase(t), PostgresOptions{ChainID: 1})
	_, err := backend.Execute(context.Background(), Request{Module: "account", Action: "getminedblocks", Values: url.Values{
		"address": {testSender}, "blocktype": {"uncles"},
	}})
	if !errors.Is(err, ErrUncleUnavailable) {
		t.Fatalf("error=%v", err)
	}
}

func TestTransactionStatusUsesCanonicalReceipt(t *testing.T) {
	t.Parallel()
	row := []driver.Value{testReceiptJSON(10, 3, 7, 1, "0x0", ""), testHashBytes(7), testHashBytes(3), "10", int64(1)}
	db := fakeDatabase(t,
		sqlExpectation{contains: "JOIN canonical_blocks AS canonical", columns: fakeColumns(5), rows: [][]driver.Value{row}},
		sqlExpectation{contains: "JOIN canonical_blocks AS canonical", columns: fakeColumns(5), rows: [][]driver.Value{row}},
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	values := url.Values{"txhash": {testHash(7)}}
	status, err := backend.Execute(context.Background(), Request{Module: "transaction", Action: "getstatus", Values: values})
	if err != nil || status != (transactionErrorStatus{IsError: "1", ErrDescription: "execution failed"}) {
		t.Fatalf("status=%#v error=%v", status, err)
	}
	receiptStatus, err := backend.Execute(context.Background(), Request{Module: "transaction", Action: "gettxreceiptstatus", Values: values})
	if err != nil || receiptStatus != (transactionReceiptStatus{Status: "0"}) {
		t.Fatalf("receipt status=%#v error=%v", receiptStatus, err)
	}
}

func TestLogsUseParameterizedTopicExpressionAndDecimalWireModel(t *testing.T) {
	t.Parallel()
	topic0, topic2 := testHash(21), testHash(23)
	db := fakeDatabase(t, sqlExpectation{
		contains: "log.address = $4 AND (log.topic0 = $5 OR lower(log.raw->'topics'->>2) = $6) ORDER BY log.block_number DESC, log.log_index DESC, log.block_hash DESC LIMIT $7 OFFSET $8",
		columns:  fakeColumns(10),
		rows: [][]driver.Value{{
			testLogJSON(10, 3, 7, 1, 4, testContract, []string{topic0, testHash(22), topic2}),
			testReceiptJSON(10, 3, 7, 1, "0x1", ""),
			testTransactionJSON(10, 3, 7, 1, testRecipient),
			testBlockJSON(10, 3, 2, 100, testSender),
			"10", testHashBytes(3), int64(4), int64(1), testHashBytes(7), testAddressBytes(testContract),
		}},
		check: func(arguments []driver.NamedValue) error {
			if len(arguments) != 8 || fmt.Sprint(arguments[0].Value) != "1" || fmt.Sprint(arguments[1].Value) != "5" || fmt.Sprint(arguments[2].Value) != "12" {
				return fmt.Errorf("arguments=%v", arguments)
			}
			if !reflect.DeepEqual(arguments[3].Value, testAddressBytes(testContract)) || !reflect.DeepEqual(arguments[4].Value, testHashBytes(21)) || arguments[5].Value != topic2 {
				return fmt.Errorf("binary/topic arguments=%v", arguments)
			}
			return nil
		},
	})
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{Module: "logs", Action: "getLogs", Values: url.Values{
		"fromBlock": {"5"}, "toBlock": {"12"}, "address": {testContract},
		"topic0": {topic0}, "topic2": {topic2}, "topic0_2_opr": {"or"}, "sort": {"desc"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	logs := result.([]logEntry)
	if len(logs) != 1 || logs[0].BlockNumber != "10" || logs[0].BlockHash != testHash(3) || logs[0].LogIndex != "4" || logs[0].GasPrice != "2000000000" || logs[0].GasUsed != "21000" {
		t.Fatalf("logs=%+v", logs)
	}
	if logs[0].Address != "0x27b1fdb04752bbc536007a920d24acb045561c26" || !reflect.DeepEqual(logs[0].Topics, []string{topic0, testHash(22), topic2}) {
		t.Fatalf("log address/topics=%+v", logs[0])
	}
}

func TestTopicValidationRejectsIgnoredOrInjectedOperators(t *testing.T) {
	t.Parallel()
	backend := testPostgresBackend(t, fakeDatabase(t), PostgresOptions{ChainID: 1})
	for _, values := range []url.Values{
		{"topic0": {testHash(1)}, "topic1": {testHash(2)}, "topic2": {testHash(3)}, "topic0_2_opr": {"or"}},
		{"topic0": {testHash(1)}, "topic1": {testHash(2)}, "topic0_1_opr": {"or; drop table logs"}},
		{"topic0": {"0x1234"}},
		{"topic4": {testHash(4)}},
		{"topic0": {testHash(1), testHash(2)}},
	} {
		_, err := backend.Execute(context.Background(), Request{Module: "logs", Action: "getLogs", Values: values})
		if !errors.Is(err, ErrInvalidParameter) {
			t.Fatalf("values=%v error=%v", values, err)
		}
	}
}

func TestBlockTimeCountdownAndSupply(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t,
		sqlExpectation{contains: "block.timestamp <= $2::numeric ORDER BY block.timestamp DESC, block.number DESC", columns: fakeColumns(4), rows: [][]driver.Value{{testBlockJSON(10, 3, 2, 100, testSender), "10", testHashBytes(3), "100"}}},
		sqlExpectation{contains: "WITH recent AS", columns: fakeColumns(4), rows: [][]driver.Value{{"10", "100", "2", "20"}}},
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1, Supply: func(_ context.Context, chainID uint64) (string, error) {
		if chainID != 1 {
			t.Fatalf("chainID=%d", chainID)
		}
		return "120000000000000000000000000", nil
	}})
	number, err := backend.Execute(context.Background(), Request{Module: "block", Action: "getblocknobytime", Values: url.Values{"timestamp": {"100"}, "closest": {"before"}}})
	if err != nil || number != "10" {
		t.Fatalf("number=%#v error=%v", number, err)
	}
	countdownAny, err := backend.Execute(context.Background(), Request{Module: "block", Action: "getblockcountdown", Values: url.Values{"blockno": {"14"}}})
	if err != nil {
		t.Fatal(err)
	}
	countdown := countdownAny.(blockCountdown)
	if countdown != (blockCountdown{CurrentBlock: "10", CountdownBlock: "14", RemainingBlock: "4", EstimateTimeInSec: "40"}) {
		t.Fatalf("countdown=%+v", countdown)
	}
	supply, err := backend.Execute(context.Background(), Request{Module: "stats", Action: "ethsupply", Values: url.Values{}})
	if err != nil || supply != "120000000000000000000000000" {
		t.Fatalf("supply=%#v error=%v", supply, err)
	}
}

func TestCountdownRejectsAlreadyPassedBlock(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t, sqlExpectation{
		contains: "WITH recent AS", columns: fakeColumns(4), rows: [][]driver.Value{{"10", "100", "2", "20"}},
	})
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	_, err := backend.Execute(context.Background(), Request{Module: "block", Action: "getblockcountdown", Values: url.Values{"blockno": {"10"}}})
	if !errors.Is(err, ErrBlockAlreadyPassed) {
		t.Fatalf("error=%v", err)
	}
}

func TestVerifiedContractABIAndSource(t *testing.T) {
	t.Parallel()
	codeHash := testHashBytes(9)
	abi := []byte(`[ { "type": "function", "name": "x", "inputs": [] } ]`)
	sources := []byte(`{"A.sol":{"content":"contract A{}"}}`)
	settings := []byte(`{"optimizer":{"enabled":true,"runs":200},"evmVersion":"paris","libraries":{"A.sol":{"L":"0x0000000000000000000000000000000000000001"}},"constructorArguments":"00","licenseType":"MIT"}`)
	row := []driver.Value{codeHash, codeHash, abi, sources, settings, "solidity", "v0.8.30+commit.73712a01", "exact", "A"}
	db := fakeDatabase(t,
		sqlExpectation{contains: "FROM contract_code_observations AS observation", columns: fakeColumns(9), rows: [][]driver.Value{row}},
		sqlExpectation{contains: "FROM contract_code_observations AS observation", columns: fakeColumns(9), rows: [][]driver.Value{row}},
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	values := url.Values{"address": {testContract}}
	abiResult, err := backend.Execute(context.Background(), Request{Module: "contract", Action: "getabi", Values: values})
	if err != nil || abiResult != `[{"inputs":[],"name":"x","type":"function"}]` {
		t.Fatalf("ABI=%#v error=%v", abiResult, err)
	}
	sourceAny, err := backend.Execute(context.Background(), Request{Module: "contract", Action: "getsourcecode", Values: values})
	if err != nil {
		t.Fatal(err)
	}
	source := sourceAny.([]sourceCodeResult)
	if len(source) != 1 || source[0].SourceCode != string(sources) || source[0].OptimizationUsed != "1" || source[0].Runs != "200" || source[0].EVMVersion != "paris" || source[0].MatchKind != "exact" {
		t.Fatalf("source=%+v", source)
	}
}

func TestVerifiedContractQueryBindsCanonicalCodeHashAndCurrentRange(t *testing.T) {
	t.Parallel()
	query := compactSQL(verifiedContractSQL)
	for _, required := range []string{
		"JOIN canonical_blocks AS canonical ON canonical.chain_id = observation.chain_id AND canonical.number = observation.block_number AND canonical.block_hash = observation.block_hash",
		"observation.canonical = TRUE",
		"verified.code_hash = current_code.code_hash",
		"verified.valid_from_block <= current_code.context_number",
		"verified.valid_to_block >= current_code.context_number",
		"ORDER BY (verified.match_kind = 'exact') DESC, verified.valid_from_block DESC, verified.request_digest ASC NULLS LAST",
	} {
		if !strings.Contains(query, compactSQL(required)) {
			t.Fatalf("verified contract query does not contain %q: %s", compactSQL(required), query)
		}
	}
}

func TestUnverifiedContractIsNotAnEmptySuccess(t *testing.T) {
	t.Parallel()
	currentCodeHash := testHashBytes(10)
	db := fakeDatabase(t, sqlExpectation{
		contains: "verified.code_hash = current_code.code_hash", columns: fakeColumns(9),
		rows: [][]driver.Value{{currentCodeHash, nil, nil, nil, nil, nil, nil, nil, nil}},
	})
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{Module: "contract", Action: "getabi", Values: url.Values{"address": {testContract}}})
	if result != "" || !errors.Is(err, ErrContractUnverified) {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestVerifiedContractWithoutCanonicalCodeIsUnavailable(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t, sqlExpectation{
		contains: "FROM contract_code_observations AS observation", columns: fakeColumns(9),
	})
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{Module: "contract", Action: "getabi", Values: url.Values{"address": {testContract}}})
	if result != "" || !errors.Is(err, ErrStateUnavailable) {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestVerifiedContractRejectsMismatchedStoredCodeHash(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t, sqlExpectation{
		contains: "verified.code_hash = current_code.code_hash", columns: fakeColumns(9),
		rows: [][]driver.Value{{
			testHashBytes(11), testHashBytes(12), []byte(`[]`), []byte(`{}`), []byte(`{}`),
			"solidity", "v0.8.30+commit.73712a01", "exact", "A",
		}},
	})
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{Module: "contract", Action: "getabi", Values: url.Values{"address": {testContract}}})
	if result != "" || err == nil || errors.Is(err, ErrContractUnverified) || errors.Is(err, ErrStateUnavailable) {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestContractCreationPreservesInputOrderAndChecksums(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t, sqlExpectation{
		contains: "trace.call_type IN ('CREATE', 'CREATE2')", columns: fakeColumns(13),
		rows: [][]driver.Value{{
			"top_level", testReceiptJSON(10, 3, 7, 1, "0x1", testContract),
			testTransactionJSON(10, 3, 7, 1, ""), testHashBytes(7), testHashBytes(3),
			"10", "100", int64(1), nil, nil, nil, nil, nil,
		}},
	})
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{Module: "contract", Action: "getcontractcreation", Values: url.Values{"contractaddresses": {testContract}}})
	if err != nil {
		t.Fatal(err)
	}
	items := result.([]contractCreationResult)
	if len(items) != 1 || items[0].ContractAddress != testContract || items[0].ContractCreator != "0x52908400098527886E0F7030069857D2E4169EE7" || items[0].TxHash != testHash(7) || items[0].BlockNumber != "10" || items[0].Timestamp != "100" || items[0].ContractFactory != "" || items[0].CreationBytecode != "0xdeadbeef00" {
		t.Fatalf("items=%+v", items)
	}
}

func TestContractCreationIncludesFactoryCreateFacts(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t, sqlExpectation{
		contains: "trace.created_address = $2", columns: fakeColumns(13),
		rows: [][]driver.Value{{
			"trace", nil, testTransactionJSON(10, 3, 7, 1, testRecipient),
			testHashBytes(7), testHashBytes(3), "10", "100", int64(1),
			"0.2", int64(2), "CREATE2", testAddressBytes(testRecipient), []byte{0x60, 0x00, 0xff},
		}},
	})
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{
		Module: "contract", Action: "getcontractcreation",
		Values: url.Values{"contractaddresses": {testContract}},
	})
	if err != nil {
		t.Fatal(err)
	}
	items := result.([]contractCreationResult)
	if len(items) != 1 || items[0].ContractCreator != "0x52908400098527886E0F7030069857D2E4169EE7" ||
		items[0].ContractFactory != testRecipient || items[0].CreationBytecode != "0x6000ff" ||
		items[0].BlockNumber != "10" || items[0].Timestamp != "100" {
		t.Fatalf("items=%+v", items)
	}
}

func TestContractCreationAbsenceRequiresFullCoreAndTraceCoverage(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		expectations []sqlExpectation
		want         error
	}{
		{
			name: "core coverage unavailable",
			expectations: []sqlExpectation{
				{contains: "WITH candidates AS", columns: fakeColumns(13)},
				{contains: "FROM core_index_configuration AS configuration", columns: fakeColumns(4)},
			},
			want: ErrStateUnavailable,
		},
		{
			name: "trace coverage complete",
			expectations: []sqlExpectation{
				{contains: "WITH candidates AS", columns: fakeColumns(13)},
				{contains: "FROM core_index_configuration AS configuration", columns: fakeColumns(4), rows: [][]driver.Value{{"0", "0", "10", "10"}}},
				{contains: "FROM published_block_stage_results AS result", columns: fakeColumns(4), rows: [][]driver.Value{{"10", nil, nil, nil}}},
			},
			want: ErrNotFound,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := testPostgresBackend(t, fakeDatabase(t, test.expectations...), PostgresOptions{ChainID: 1})
			result, err := backend.Execute(context.Background(), Request{
				Module: "contract", Action: "getcontractcreation",
				Values: url.Values{"contractaddresses": {testContract}},
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("result=%#v error=%v, want %v", result, err, test.want)
			}
		})
	}
}

func TestListQueriesReturnNotFoundInsteadOfEmptySuccess(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		module, action string
		values         url.Values
		contains       string
		columns        int
	}{
		{"account", "txlist", url.Values{"address": {testSender}}, "FROM transaction_inclusions AS inclusion", 8},
		{"account", "getminedblocks", url.Values{"address": {testSender}}, "FROM blocks AS block", 3},
		{"logs", "getLogs", url.Values{}, "FROM logs AS log", 10},
	} {
		db := fakeDatabase(t, sqlExpectation{contains: test.contains, columns: fakeColumns(test.columns)})
		backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
		result, err := backend.Execute(context.Background(), Request{Module: test.module, Action: test.action, Values: test.values})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s.%s result=%#v error=%v", test.module, test.action, result, err)
		}
	}
}

func testPostgresBackend(t *testing.T, db *sql.DB, options PostgresOptions) *PostgresBackend {
	t.Helper()
	backend, err := NewPostgresBackend(db, options)
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

func testHash(value byte) string { return fmt.Sprintf("0x%064x", value) }

func testHashBytes(value byte) []byte {
	result := make([]byte, 32)
	result[len(result)-1] = value
	return result
}

func testAddressBytes(value string) []byte {
	address, err := ethrpc.ParseAddress(value)
	if err != nil {
		panic(err)
	}
	result, err := address.Bytes()
	if err != nil {
		panic(err)
	}
	return result
}

func testBlockJSON(number uint64, hash, parent byte, timestamp uint64, miner string) []byte {
	return mustJSON(map[string]any{
		"number": fmt.Sprintf("0x%x", number), "hash": testHash(hash), "parentHash": testHash(parent),
		"timestamp": fmt.Sprintf("0x%x", timestamp), "miner": miner,
		"gasUsed": "0x5208", "gasLimit": "0x1c9c380", "transactions": []any{},
	})
}

func testTransactionJSON(blockNumber uint64, blockHash, transactionHash byte, index uint64, to string) []byte {
	var recipient any
	if to != "" {
		recipient = to
	}
	return mustJSON(map[string]any{
		"hash": testHash(transactionHash), "blockHash": testHash(blockHash),
		"blockNumber": fmt.Sprintf("0x%x", blockNumber), "transactionIndex": fmt.Sprintf("0x%x", index),
		"from": testSender, "to": recipient, "nonce": "0xf", "gas": "0x5208",
		"gasPrice": "0x77359400", "value": "0x10", "input": "0xdeadbeef00", "type": "0x2",
	})
}

func testReceiptJSON(blockNumber uint64, blockHash, transactionHash byte, index uint64, status, contract string) []byte {
	value := map[string]any{
		"transactionHash": testHash(transactionHash), "transactionIndex": fmt.Sprintf("0x%x", index),
		"blockHash": testHash(blockHash), "blockNumber": fmt.Sprintf("0x%x", blockNumber),
		"cumulativeGasUsed": "0xa410", "gasUsed": "0x5208", "effectiveGasPrice": "0x77359400",
		"logs": []any{}, "logsBloom": "0x", "status": status,
	}
	if contract != "" {
		value["contractAddress"] = contract
	}
	return mustJSON(value)
}

func testLogJSON(blockNumber uint64, blockHash, transactionHash byte, transactionIndex, logIndex uint64, address string, topics []string) []byte {
	return mustJSON(map[string]any{
		"removed": false, "blockHash": testHash(blockHash), "blockNumber": fmt.Sprintf("0x%x", blockNumber),
		"transactionHash": testHash(transactionHash), "transactionIndex": fmt.Sprintf("0x%x", transactionIndex),
		"logIndex": fmt.Sprintf("0x%x", logIndex), "address": address, "topics": topics, "data": "0x1234",
	})
}

func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
