package etherscan

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestEnrichmentStageAbsenceIsNeverAnEmptySuccess(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, module, action string
		values               url.Values
		stage                string
		want                 error
	}{
		{"trace", "account", "txlistinternal", url.Values{"address": {testSender}}, traceStage, ErrTraceUnavailable},
		{"token", "account", "tokentx", url.Values{"address": {testSender}}, tokenStage, ErrTokenUnavailable},
		{"token info", "token", "tokeninfo", url.Values{"contractaddress": {testContract}}, tokenStage, ErrTokenUnavailable},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := fakeDatabase(t,
				completeCoreCoverageExpectation("0", "", "12"),
				sqlExpectation{
					contains: "latest.state IS DISTINCT FROM 'complete'",
					columns:  fakeColumns(4),
					rows:     [][]driver.Value{{"12", "10", testHashBytes(3), nil}},
					check: func(arguments []driver.NamedValue) error {
						if len(arguments) != 4 || arguments[3].Value != test.stage {
							return fmt.Errorf("stage arguments=%v", arguments)
						}
						return nil
					},
				},
			)
			backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
			_, err := backend.Execute(context.Background(), Request{
				Module: test.module, Action: test.action, Values: test.values,
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v, want %v", err, test.want)
			}
		})
	}
}

func TestInternalTransactionsAreCanonicalPagedAndGolden(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t,
		completeCoreCoverageExpectation("10", "20", "12"),
		completedStageExpectation("trace", "10", "20"),
		sqlExpectation{
			contains: "JOIN canonical_blocks AS canonical ON canonical.chain_id = trace.chain_id AND canonical.number = trace.block_number AND canonical.block_hash = trace.block_hash",
			columns:  fakeColumns(16),
			rows: [][]driver.Value{{
				"10", testHashBytes(3), testHashBytes(7), "100", "0.1", int64(2), "CREATE",
				testAddressBytes(testSender), nil, testAddressBytes(testContract),
				"16", "21000", "20000", []byte{0xde, 0xad}, nil, false,
			}},
			check: func(arguments []driver.NamedValue) error {
				if len(arguments) != 7 || arguments[0].Value != "1" ||
					!reflect.DeepEqual(arguments[1].Value, testAddressBytes(testSender)) || arguments[2].Value != nil ||
					arguments[3].Value != "10" || arguments[4].Value != "20" ||
					fmt.Sprint(arguments[5].Value) != "2" || fmt.Sprint(arguments[6].Value) != "2" {
					return fmt.Errorf("internal transaction arguments=%v", arguments)
				}
				return nil
			},
		},
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{
		Module: "account", Action: "txlistinternal", Values: url.Values{
			"address": {testSender}, "startblock": {"10"}, "endblock": {"20"},
			"page": {"2"}, "offset": {"2"}, "sort": {"desc"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"blockNumber":"10","timeStamp":"100","hash":"` + testHash(7) + `","from":"0x52908400098527886E0F7030069857D2E4169EE7","to":"","value":"16","contractAddress":"` + testContract + `","input":"0xdead","type":"create","gas":"21000","gasUsed":"20000","traceId":"0_1","isError":"0","errCode":""}]`
	if string(encoded) != want {
		t.Fatalf("internal transaction JSON\n got: %s\nwant: %s", encoded, want)
	}
}

func TestInternalTransactionsSupportHashAndRangeModes(t *testing.T) {
	t.Parallel()
	row := []driver.Value{
		"10", testHashBytes(3), testHashBytes(7), "100", "0", int64(1), "CALL",
		testAddressBytes(testSender), testAddressBytes(testRecipient), nil,
		"7", "21000", "20000", []byte{}, nil, false,
	}
	for _, test := range []struct {
		name         string
		values       url.Values
		expectations []sqlExpectation
	}{
		{
			name:   "transaction hash",
			values: url.Values{"txhash": {testHash(7)}},
			expectations: []sqlExpectation{
				{
					contains: "FROM transaction_inclusions AS inclusion JOIN canonical_blocks AS canonical",
					columns:  fakeColumns(1), rows: [][]driver.Value{{"10"}},
					check: func(arguments []driver.NamedValue) error {
						if len(arguments) != 2 || arguments[0].Value != "1" || !reflect.DeepEqual(arguments[1].Value, testHashBytes(7)) {
							return fmt.Errorf("transaction block arguments=%v", arguments)
						}
						return nil
					},
				},
				completeCoreCoverageExpectation("10", "10", "12"),
				completedStageExpectation("trace", "10", "10"),
				{
					contains: "($2::bytea IS NULL OR trace.from_address = $2 OR trace.to_address = $2 OR trace.created_address = $2)",
					columns:  fakeColumns(16), rows: [][]driver.Value{row},
					check: func(arguments []driver.NamedValue) error {
						if len(arguments) != 7 || arguments[1].Value != nil || !reflect.DeepEqual(arguments[2].Value, testHashBytes(7)) ||
							arguments[3].Value != "10" || arguments[4].Value != "10" {
							return fmt.Errorf("hash-mode arguments=%v", arguments)
						}
						return nil
					},
				},
			},
		},
		{
			name:   "block range",
			values: url.Values{"startblock": {"10"}, "endblock": {"20"}},
			expectations: []sqlExpectation{
				completeCoreCoverageExpectation("10", "20", "12"),
				completedStageExpectation("trace", "10", "20"),
				{
					contains: "($2::bytea IS NULL OR trace.from_address = $2 OR trace.to_address = $2 OR trace.created_address = $2)",
					columns:  fakeColumns(16), rows: [][]driver.Value{row},
					check: func(arguments []driver.NamedValue) error {
						if len(arguments) != 7 || arguments[1].Value != nil || arguments[2].Value != nil ||
							arguments[3].Value != "10" || arguments[4].Value != "20" {
							return fmt.Errorf("range-mode arguments=%v", arguments)
						}
						return nil
					},
				},
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := fakeDatabase(t, test.expectations...)
			backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
			result, err := backend.Execute(context.Background(), Request{
				Module: "account", Action: "txlistinternal", Values: test.values,
			})
			if err != nil {
				t.Fatal(err)
			}
			items := result.([]internalTransaction)
			if len(items) != 1 || items[0].Value != "7" {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestInternalTransactionsRejectAmbiguousOrUnboundedSelectors(t *testing.T) {
	t.Parallel()
	for _, values := range []url.Values{
		{},
		{"startblock": {"10"}},
		{"address": {testSender}, "txhash": {testHash(7)}},
		{"txhash": {testHash(7)}, "startblock": {"10"}, "endblock": {"10"}},
	} {
		db := fakeDatabase(t)
		backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
		_, err := backend.Execute(context.Background(), Request{
			Module: "account", Action: "txlistinternal", Values: values,
		})
		if !errors.Is(err, ErrInvalidParameter) {
			t.Fatalf("values=%v error=%v", values, err)
		}
	}
}

func TestERC20TransfersUseCanonicalRowsAndPreserveUint256(t *testing.T) {
	t.Parallel()
	maximum := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)).String()
	db := fakeDatabase(t,
		completeCoreCoverageExpectation("10", "20", "12"),
		completedStageExpectation("token", "10", "20"),
		sqlExpectation{
			contains: "event.canonical = TRUE AND (event.from_address = $2 OR event.to_address = $2) AND event.standard = $3",
			columns:  fakeColumns(19),
			rows: [][]driver.Value{{
				"10", testHashBytes(3), int64(4), int64(0), testHashBytes(7), testAddressBytes(testContract),
				"erc20", "transfer", testAddressBytes(testSender), testAddressBytes(testRecipient), nil, maximum,
				testTransactionJSON(10, 3, 7, 1, testRecipient), testReceiptJSON(10, 3, 7, 1, "0x1", ""),
				testBlockJSON(10, 3, 2, 100, testSender), int64(1), "Example", "TOK", int64(18),
			}},
			check: func(arguments []driver.NamedValue) error {
				if len(arguments) != 8 || arguments[0].Value != "1" ||
					!reflect.DeepEqual(arguments[1].Value, testAddressBytes(testSender)) || arguments[2].Value != "erc20" ||
					arguments[3].Value != "10" || arguments[4].Value != "20" ||
					!reflect.DeepEqual(arguments[5].Value, testAddressBytes(testContract)) ||
					fmt.Sprint(arguments[6].Value) != "2" || fmt.Sprint(arguments[7].Value) != "2" {
					return fmt.Errorf("token transfer arguments=%v", arguments)
				}
				return nil
			},
		},
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{
		Module: "account", Action: "tokentx", Values: url.Values{
			"address": {testSender}, "contractaddress": {testContract},
			"startblock": {"10"}, "endblock": {"20"}, "page": {"2"}, "offset": {"2"}, "sort": {"desc"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"blockNumber":"10","timeStamp":"100","hash":"` + testHash(7) + `","nonce":"15","blockHash":"` + testHash(3) + `","from":"0x52908400098527886E0F7030069857D2E4169EE7","contractAddress":"` + testContract + `","to":"` + testRecipient + `","value":"` + maximum + `","tokenName":"Example","tokenSymbol":"TOK","tokenDecimal":"18","transactionIndex":"1","gas":"21000","gasPrice":"2000000000","gasUsed":"21000","cumulativeGasUsed":"42000","input":"deprecated","methodId":"0xdeadbeef","functionName":"","confirmations":"3"}]`
	if string(encoded) != want {
		t.Fatalf("token transfer JSON\n got: %s\nwant: %s", encoded, want)
	}
}

func TestNFTTransferActionsKeepStandardSpecificQuantities(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		action, standard, tokenID, amount string
		check                             func(t *testing.T, transfer tokenTransfer)
	}{
		{
			action: "tokennfttx", standard: "erc721", tokenID: "42", amount: "1",
			check: func(t *testing.T, transfer tokenTransfer) {
				if transfer.TokenID != "42" || transfer.TokenValue != "" || transfer.Value != "" || transfer.TokenDecimal != "0" {
					t.Fatalf("ERC-721 transfer=%+v", transfer)
				}
			},
		},
		{
			action: "token1155tx", standard: "erc1155", tokenID: "43", amount: "7",
			check: func(t *testing.T, transfer tokenTransfer) {
				if transfer.TokenID != "43" || transfer.TokenValue != "7" || transfer.Value != "" || transfer.TokenDecimal != "" {
					t.Fatalf("ERC-1155 transfer=%+v", transfer)
				}
			},
		},
	} {
		test := test
		t.Run(test.action, func(t *testing.T) {
			t.Parallel()
			db := fakeDatabase(t,
				completeCoreCoverageExpectation("0", "", "12"),
				completedStageExpectation("token", "0", ""),
				sqlExpectation{
					contains: "event.standard = $3", columns: fakeColumns(19),
					rows: [][]driver.Value{{
						"10", testHashBytes(3), int64(4), int64(0), testHashBytes(7), testAddressBytes(testContract),
						test.standard, "transfer", testAddressBytes(testSender), testAddressBytes(testRecipient), test.tokenID, test.amount,
						testTransactionJSON(10, 3, 7, 1, testRecipient), testReceiptJSON(10, 3, 7, 1, "0x1", ""),
						testBlockJSON(10, 3, 2, 100, testSender), int64(1), "Collectible", "NFT", nil,
					}},
				},
			)
			backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
			result, err := backend.Execute(context.Background(), Request{
				Module: "account", Action: test.action, Values: url.Values{"address": {testSender}},
			})
			if err != nil {
				t.Fatal(err)
			}
			transfers, ok := result.([]tokenTransfer)
			if !ok || len(transfers) != 1 {
				t.Fatalf("result=%#v", result)
			}
			test.check(t, transfers[0])
		})
	}
}

func TestTokenInformationSupplyBalanceAndHolders(t *testing.T) {
	t.Parallel()
	contractRow := canonicalTokenRow("erc20", "1000")
	db := fakeDatabase(t,
		completeCoreCoverageExpectation("0", "", "12"),
		completedStageExpectation("token", "0", ""),
		tokenContractExpectation(contractRow),
	)
	provider := &testStateProvider{tokenSupply: "1200", tokenBalance: "900"}
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1, State: provider})
	contractValues := url.Values{"contractaddress": {testContract}}

	info, err := backend.Execute(context.Background(), Request{Module: "token", Action: "tokeninfo", Values: contractValues})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(info)
	if want := `[{"contractAddress":"` + testContract + `","tokenName":"Example","symbol":"TOK","divisor":"18","tokenType":"ERC20","totalSupply":"1200"}]`; string(encoded) != want {
		t.Fatalf("token info=%s want=%s", encoded, want)
	}
	supply, err := backend.Execute(context.Background(), Request{Module: "stats", Action: "tokensupply", Values: contractValues})
	if err != nil || supply != "1200" {
		t.Fatalf("supply=%#v error=%v", supply, err)
	}
	balance, err := backend.Execute(context.Background(), Request{Module: "account", Action: "tokenbalance", Values: url.Values{
		"contractaddress": {testContract}, "address": {testSender}, "tag": {"latest"},
	}})
	if err != nil || balance != "900" {
		t.Fatalf("balance=%#v error=%v", balance, err)
	}
	_, err = backend.Execute(context.Background(), Request{Module: "token", Action: "tokenholderlist", Values: url.Values{
		"contractaddress": {testContract}, "page": {"2"}, "offset": {"2"}, "sort": {"desc"},
	}})
	if !errors.Is(err, ErrStateUnavailable) {
		t.Fatalf("holders error=%v", err)
	}
	if provider.supplyCall != testContract || !reflect.DeepEqual(provider.balanceCall, []string{testContract, testSender}) {
		t.Fatalf("supply call=%q balance call=%v", provider.supplyCall, provider.balanceCall)
	}
}

func TestEnrichmentRowsRejectMalformedOrOverflowValues(t *testing.T) {
	t.Parallel()
	overflow := new(big.Int).Lsh(big.NewInt(1), 256).String()
	for _, test := range []struct {
		name   string
		values url.Values
		row    []driver.Value
	}{
		{
			name:   "internal trace path",
			values: url.Values{"address": {testSender}},
			row: []driver.Value{
				"10", testHashBytes(3), testHashBytes(7), "100", "01", int64(1), "CALL",
				testAddressBytes(testSender), testAddressBytes(testRecipient), nil,
				"1", "2", "3", []byte{}, nil, false,
			},
		},
		{
			name:   "internal uint256 overflow",
			values: url.Values{"address": {testSender}},
			row: []driver.Value{
				"10", testHashBytes(3), testHashBytes(7), "100", "0", int64(1), "CALL",
				testAddressBytes(testSender), testAddressBytes(testRecipient), nil,
				overflow, "2", "3", []byte{}, nil, false,
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := fakeDatabase(t,
				completeCoreCoverageExpectation("0", "", "12"),
				completedStageExpectation("trace", "0", ""),
				sqlExpectation{contains: "FROM normalized_traces AS trace", columns: fakeColumns(16), rows: [][]driver.Value{test.row}},
			)
			backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
			if _, err := backend.Execute(context.Background(), Request{
				Module: "account", Action: "txlistinternal", Values: test.values,
			}); err == nil {
				t.Fatal("malformed enrichment row was accepted")
			}
		})
	}
}

func TestCompletedEnrichmentWithNoRowsReturnsNotFound(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, module, action, stage, query string
		values                             url.Values
		columns                            int
	}{
		{"trace", "account", "txlistinternal", traceStage, "FROM normalized_traces AS trace", url.Values{"address": {testSender}}, 16},
		{"token", "account", "tokentx", tokenStage, "FROM token_events AS event", url.Values{"address": {testSender}}, 19},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := fakeDatabase(t,
				completeCoreCoverageExpectation("0", "", "12"),
				completedStageExpectation(test.stage, "0", ""),
				sqlExpectation{contains: test.query, columns: fakeColumns(test.columns)},
			)
			backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
			_, err := backend.Execute(context.Background(), Request{Module: test.module, Action: test.action, Values: test.values})
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func completedStageExpectation(stage, start, end string) sqlExpectation {
	return sqlExpectation{
		contains: "latest.state IS DISTINCT FROM 'complete'",
		columns:  fakeColumns(4), rows: [][]driver.Value{{"12", nil, nil, nil}},
		check: func(arguments []driver.NamedValue) error {
			if len(arguments) != 4 || arguments[0].Value != "1" || arguments[1].Value != start || arguments[3].Value != stage {
				return fmt.Errorf("stage arguments=%v", arguments)
			}
			if end == "" && arguments[2].Value != nil || end != "" && arguments[2].Value != end {
				return fmt.Errorf("stage end argument=%v want=%q", arguments[2].Value, end)
			}
			return nil
		},
	}
}

func canonicalTokenRow(standard, supply string) []driver.Value {
	var supplyValue driver.Value
	if supply != "" {
		supplyValue = supply
	}
	return []driver.Value{
		testAddressBytes(testContract), testHashBytes(8), standard, "verified",
		"Example", "TOK", int64(18), supplyValue, "complete", "10", testHashBytes(3),
	}
}

func tokenContractExpectation(row []driver.Value) sqlExpectation {
	return sqlExpectation{
		contains: "JOIN canonical_blocks AS canonical ON canonical.chain_id = token.chain_id AND canonical.number = token.observed_block_number",
		columns:  fakeColumns(11), rows: [][]driver.Value{row},
		check: func(arguments []driver.NamedValue) error {
			if len(arguments) != 2 || arguments[0].Value != "1" || !reflect.DeepEqual(arguments[1].Value, testAddressBytes(testContract)) {
				return fmt.Errorf("token contract arguments=%v", arguments)
			}
			return nil
		},
	}
}

func TestStageRangeRejectsInvalidStoredState(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t,
		completeCoreCoverageExpectation("0", "", "12"),
		sqlExpectation{
			contains: "latest.state IS DISTINCT FROM 'complete'", columns: fakeColumns(4),
			rows: [][]driver.Value{{"12", "10", testHashBytes(3), "pending"}},
		},
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	_, err := backend.Execute(context.Background(), Request{
		Module: "token", Action: "tokeninfo", Values: url.Values{"contractaddress": {testContract}},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid state") || errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("error=%v", err)
	}
}

func TestTokenStateActionsRequireFixedCanonicalProvider(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, action string
		values       url.Values
	}{
		{"supply", "tokensupply", url.Values{"contractaddress": {testContract}}},
		{"balance", "tokenbalance", url.Values{"contractaddress": {testContract}, "address": {testSender}}},
		{"holder ledger", "tokenholderlist", url.Values{"contractaddress": {testContract}}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := fakeDatabase(t)
			backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
			_, err := backend.Execute(context.Background(), Request{Module: "token", Action: test.action, Values: test.values})
			if !errors.Is(err, ErrStateUnavailable) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
