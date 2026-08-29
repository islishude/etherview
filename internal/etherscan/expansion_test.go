package etherscan

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/db/gen"
)

func TestAdvancedFiltersUseDedicatedQueries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, action string
		values       url.Values
		stage        string
		query        string
		arguments    int
		check        func([]driver.NamedValue) error
	}{
		{
			name: "normal", action: "txlist",
			values: url.Values{"from": {testSender}, "to": {testRecipient}, "fromto_opr": {"and"}},
			query:  "EtherscanAccountTransactionsAdvanced", arguments: 9,
			check: func(arguments []driver.NamedValue) error {
				if arguments[1].Value != strings.ToLower(testSender) || arguments[2].Value != strings.ToLower(testRecipient) || arguments[3].Value != "AND" {
					return fmt.Errorf("normal filter arguments=%v", arguments)
				}
				return nil
			},
		},
		{
			name: "token", action: "tokentx", stage: tokenStage,
			values: url.Values{"contractaddress": {testContract}, "from": {testSender}, "fromto_opr": {"or"}},
			query:  "EtherscanTokenTransfersAdvanced", arguments: 11,
			check: func(arguments []driver.NamedValue) error {
				if !reflect.DeepEqual(arguments[2].Value, testAddressBytes(testContract)) ||
					!reflect.DeepEqual(arguments[3].Value, testAddressBytes(testSender)) || arguments[4].Value != nil || arguments[5].Value != "OR" {
					return fmt.Errorf("token filter arguments=%v", arguments)
				}
				return nil
			},
		},
		{
			name: "internal", action: "txlistinternal", stage: traceStage,
			values: url.Values{"to": {testRecipient}, "fromto_opr": {"and"}},
			query:  "EtherscanInternalTransactionsAdvanced", arguments: 9,
			check: func(arguments []driver.NamedValue) error {
				if arguments[1].Value != nil || !reflect.DeepEqual(arguments[2].Value, testAddressBytes(testRecipient)) || arguments[3].Value != "AND" {
					return fmt.Errorf("internal filter arguments=%v", arguments)
				}
				return nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			expectations := []sqlExpectation{completeCoreCoverageExpectation("0", "", "12")}
			if test.stage != "" {
				expectations = append(expectations, completedStageExpectation(test.stage, "0", ""))
			}
			expectations = append(expectations, sqlExpectation{
				contains: test.query, columns: fakeColumns(map[string]int{"normal": 8, "token": 19, "internal": 16}[test.name]),
				check: func(arguments []driver.NamedValue) error {
					if len(arguments) != test.arguments {
						return fmt.Errorf("arguments=%v", arguments)
					}
					return test.check(arguments)
				},
			})
			backend := testPostgresBackend(t, fakeDatabase(t, expectations...), PostgresOptions{ChainID: 1})
			_, err := backend.Execute(context.Background(), Request{Module: "account", Action: test.action, Values: test.values})
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("error=%v, want no records", err)
			}
		})
	}
}

func TestBeaconWithdrawalsAreCanonicalAndGolden(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t,
		completeCoreCoverageExpectation("10", "20", "12"),
		sqlExpectation{
			contains: "EtherscanBeaconWithdrawals", columns: fakeColumns(6),
			rows: [][]driver.Value{{"13", "117823", testAddressBytes(testRecipient), "3402931175", "10", "1681338599"}},
			check: func(arguments []driver.NamedValue) error {
				if len(arguments) != 7 || !reflect.DeepEqual(arguments[1].Value, testAddressBytes(testRecipient)) ||
					arguments[2].Value != "10" || arguments[3].Value != "20" || arguments[6].Value != "DESC" {
					return fmt.Errorf("withdrawal arguments=%v", arguments)
				}
				return nil
			},
		},
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{Module: "account", Action: "txsBeaconWithdrawal", Values: url.Values{
		"address": {testRecipient}, "startblock": {"10"}, "endblock": {"20"}, "sort": {"desc"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	want := `[{"withdrawalIndex":"13","validatorIndex":"117823","address":"0xde709f2102306220921060314715629080e2fb77","amount":"3402931175","blockNumber":"10","timestamp":"1681338599"}]`
	if string(encoded) != want {
		t.Fatalf("withdrawals=%s want=%s", encoded, want)
	}
}

func TestBlockTransactionCountsRequireTraceAndToken(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t,
		completeCoreCoverageExpectation("10", "10", "12"),
		completedStageExpectation(traceStage, "10", "10"),
		completeCoreCoverageExpectation("10", "10", "12"),
		completedStageExpectation(tokenStage, "10", "10"),
		sqlExpectation{
			contains: "EtherscanBlockTransactionCounts", columns: fakeColumns(6),
			rows: [][]driver.Value{{"10", "2", "3", "4", "5", "6"}},
		},
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{
		Module: "block", Action: "getblocktxnscount", Values: url.Values{"blockno": {"10"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if string(encoded) != `{"block":"10","txsCount":"2","internalTxsCount":"3","erc20TxsCount":"4","erc721TxsCount":"5","erc1155TxsCount":"6"}` {
		t.Fatalf("block counts=%s", encoded)
	}
}

func TestFundedByRequiresEOAAndCompleteTraceHistory(t *testing.T) {
	t.Parallel()
	state := &testStateProvider{accountBlock: "12", accountHash: testHash(3)}
	db := fakeDatabase(t,
		sqlExpectation{contains: "EtherscanCanonicalReference", columns: fakeColumns(1), rows: [][]driver.Value{{true}}},
		sqlExpectation{
			contains: "EtherscanFirstFunding", columns: fakeColumns(6),
			rows: [][]driver.Value{{"10", testAddressBytes(testRecipient), testHashBytes(7), "0x10", nil, "1700000000"}},
		},
		completeCoreCoverageExpectation("0", "10", "12"),
		completedStageExpectation(traceStage, "0", "10"),
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1, State: state})
	result, err := backend.Execute(context.Background(), Request{
		Module: "account", Action: "fundedby", Values: url.Values{"address": {testSender}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	want := `{"block":"10","timeStamp":"1700000000","fundingAddress":"0xde709f2102306220921060314715629080e2fb77","fundingTxn":"` + testHash(7) + `","value":"16"}`
	if string(encoded) != want {
		t.Fatalf("funding=%s want=%s", encoded, want)
	}

	contractBackend := testPostgresBackend(t, fakeDatabase(t), PostgresOptions{
		ChainID: 1, State: &testStateProvider{accountKind: "contract"},
	})
	_, err = contractBackend.Execute(context.Background(), Request{
		Module: "account", Action: "fundedby", Values: url.Values{"address": {testSender}},
	})
	if !errors.Is(err, ErrFundedByEOARequired) {
		t.Fatalf("contract fundedby error=%v", err)
	}

	emptyBackend := testPostgresBackend(t, fakeDatabase(t,
		sqlExpectation{contains: "EtherscanCanonicalReference", columns: fakeColumns(1), rows: [][]driver.Value{{true}}},
		sqlExpectation{contains: "EtherscanFirstFunding", columns: fakeColumns(6)},
		completeCoreCoverageExpectation("0", "12", "12"),
		completedStageExpectation(traceStage, "0", "12"),
	), PostgresOptions{ChainID: 1, State: state})
	_, err = emptyBackend.Execute(context.Background(), Request{
		Module: "account", Action: "fundedby", Values: url.Values{"address": {testSender}},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty fundedby error=%v", err)
	}
}

func TestHoldingClassificationAndFundingSQLPreserveCurrentAuthority(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, query, standard string
	}{
		{"ERC-20", dbgen.EtherscanERC20HoldingCandidates, "erc20"},
		{"ERC-721", dbgen.EtherscanERC721HoldingCandidates, "erc721"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sql := compactSQL(test.query)
			if !strings.Contains(sql, "SELECT token.standard") ||
				!strings.Contains(sql, "WHERE metadata.standard = '"+test.standard+"'") ||
				strings.Contains(sql, "AND token.standard = '"+test.standard+"'") {
				t.Fatalf("holding query filters before selecting the newest observation: %s", sql)
			}
		})
	}
	funding := compactSQL(dbgen.EtherscanFirstFunding)
	if strings.Contains(funding, "receipt.raw->>'status'") ||
		!strings.Contains(funding, "root_trace.trace_path = ''") ||
		!strings.Contains(funding, "root_trace.reverted = FALSE") {
		t.Fatalf("funding query does not use the authoritative root Trace outcome: %s", funding)
	}
}

type fakeERC20HoldingState struct {
	observations []catalog.ERC20BalanceObservation
	snapshot     catalog.Snapshot
}

func (state *fakeERC20HoldingState) ERC20Balances(
	_ context.Context,
	snapshot catalog.Snapshot,
	_ string,
	candidates []catalog.ERC20BalanceCandidate,
) ([]catalog.ERC20BalanceObservation, error) {
	state.snapshot = snapshot
	return append([]catalog.ERC20BalanceObservation(nil), state.observations[:len(candidates)]...), nil
}

type fakeNFTHoldingState struct {
	observations []catalog.NFTBalanceObservation
	snapshot     catalog.Snapshot
}

func (*fakeNFTHoldingState) Owner(context.Context, catalog.Snapshot, string, string) (catalog.NFTOwnerObservation, error) {
	return catalog.NFTOwnerObservation{}, errors.New("unexpected owner lookup")
}

func (state *fakeNFTHoldingState) Balances(
	_ context.Context,
	snapshot catalog.Snapshot,
	_ string,
	candidates []catalog.NFTBalanceCandidate,
) ([]catalog.NFTBalanceObservation, error) {
	state.snapshot = snapshot
	return append([]catalog.NFTBalanceObservation(nil), state.observations[:len(candidates)]...), nil
}

func TestExactAddressHoldingsUseFixedSnapshotAndDenseResults(t *testing.T) {
	t.Parallel()
	state := &fakeERC20HoldingState{observations: []catalog.ERC20BalanceObservation{
		{Balance: "0", Confidence: catalog.NFTStateConfidenceRPCExact},
		{Balance: "5", Confidence: catalog.NFTStateConfidenceRPCExact},
	}}
	db := fakeDatabase(t,
		completeCoreCoverageExpectation("0", "", "12"),
		completedStageExpectation(tokenStage, "0", ""),
		sqlExpectation{contains: "EtherscanCanonicalSnapshot", columns: fakeColumns(2), rows: [][]driver.Value{{"12", testHashBytes(3)}}},
		sqlExpectation{
			contains: "EtherscanERC20HoldingCandidates", columns: fakeColumns(4),
			rows: [][]driver.Value{
				{testAddressBytes(testContract), "Zero", "ZERO", int64(18)},
				{testAddressBytes(testRecipient), "Held", "HLD", int64(6)},
			},
		},
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1, ERC20State: state})
	result, err := backend.Execute(context.Background(), Request{
		Module: "account", Action: "addresstokenbalance", Values: url.Values{"address": {testSender}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	want := `[{"TokenAddress":"0xde709f2102306220921060314715629080e2fb77","TokenName":"Held","TokenSymbol":"HLD","TokenQuantity":"5","TokenDivisor":"6"}]`
	if string(encoded) != want || state.snapshot.BlockNumber != "12" || state.snapshot.BlockHash != testHash(3) {
		t.Fatalf("holdings=%s snapshot=%+v", encoded, state.snapshot)
	}
}

func TestExactERC721HoldingsAggregateByContract(t *testing.T) {
	t.Parallel()
	state := &fakeNFTHoldingState{observations: []catalog.NFTBalanceObservation{
		{Balance: "1", Confidence: catalog.NFTStateConfidenceRPCExact},
		{Balance: "1", Confidence: catalog.NFTStateConfidenceRPCExact},
		{Balance: "0", Confidence: catalog.NFTStateConfidenceRPCExact},
	}}
	db := fakeDatabase(t,
		completeCoreCoverageExpectation("0", "", "12"),
		completedStageExpectation(tokenStage, "0", ""),
		sqlExpectation{contains: "EtherscanCanonicalSnapshot", columns: fakeColumns(2), rows: [][]driver.Value{{"12", testHashBytes(3)}}},
		sqlExpectation{
			contains: "EtherscanERC721HoldingCandidates", columns: fakeColumns(4),
			rows: [][]driver.Value{
				{testAddressBytes(testContract), "1", "Collection", "NFT"},
				{testAddressBytes(testContract), "2", "Collection", "NFT"},
				{testAddressBytes(testRecipient), "3", "Other", "OTH"},
			},
		},
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1, NFTState: state})
	result, err := backend.Execute(context.Background(), Request{
		Module: "account", Action: "addresstokennftbalance", Values: url.Values{"address": {testSender}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	want := `[{"TokenAddress":"` + testContract + `","TokenName":"Collection","TokenSymbol":"NFT","TokenQuantity":"2"}]`
	if string(encoded) != want {
		t.Fatalf("NFT holdings=%s want=%s", encoded, want)
	}
}

func TestHoldingCandidateCapFailsClosed(t *testing.T) {
	t.Parallel()
	rows := make([][]driver.Value, maxHoldingCandidates+1)
	for index := range rows {
		address := make([]byte, 20)
		binary.BigEndian.PutUint32(address[16:], uint32(index+1))
		rows[index] = []driver.Value{address, nil, nil, nil}
	}
	observations := make([]catalog.ERC20BalanceObservation, holdingStateBatch)
	for index := range observations {
		observations[index] = catalog.ERC20BalanceObservation{
			Balance: "0", Confidence: catalog.NFTStateConfidenceRPCExact,
		}
	}
	state := &fakeERC20HoldingState{observations: observations}
	db := fakeDatabase(t,
		completeCoreCoverageExpectation("0", "", "12"),
		completedStageExpectation(tokenStage, "0", ""),
		sqlExpectation{contains: "EtherscanCanonicalSnapshot", columns: fakeColumns(2), rows: [][]driver.Value{{"12", testHashBytes(3)}}},
		sqlExpectation{contains: "EtherscanERC20HoldingCandidates", columns: fakeColumns(4), rows: rows},
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1, ERC20State: state})
	_, err := backend.Execute(context.Background(), Request{
		Module: "account", Action: "addresstokenbalance", Values: url.Values{"address": {testSender}},
	})
	if !errors.Is(err, ErrHoldingWindowUnavailable) {
		t.Fatalf("candidate-cap error=%v", err)
	}
}
