package etherscan

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/db/gen"
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

func TestStoredBlockProjectionAcceptsNonStringFormatExtension(t *testing.T) {
	t.Parallel()
	raw := testBlockJSON(2, 1)
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["format"] = map[string]any{"vendor": 1}
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := decodeStoredBlockProjection(
		raw,
		common.HexToHash(testHash(3)),
		big.NewInt(2),
	)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Number == nil || projection.Number.ToInt().Uint64() != 2 ||
		projection.Hash == nil || *projection.Hash != common.HexToHash(testHash(3)) {
		t.Fatalf("projection = %#v", projection)
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
	db := fakeDatabase(t,
		completeCoreCoverageExpectation("10", "20", "12"),
		sqlExpectation{
			contains: "inclusion.block_number <= $4::numeric ORDER BY inclusion.block_number DESC, inclusion.tx_index DESC, inclusion.tx_hash DESC LIMIT $5 OFFSET $6",
			columns:  fakeColumns(8),
			rows: [][]driver.Value{{
				testTransactionJSON(7, testRecipient),
				testReceiptJSON("0x1", ""),
				testBlockJSON(10, 2),
				"10", testHashBytes(3), int64(1), testTransactionHashBytes(testRecipient), "12",
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
		},
	)
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
	want := `{"blockNumber":"10","timeStamp":"100","hash":"` + testTransactionHash(7, testRecipient).Hex() + `","nonce":"15","blockHash":"` + testHash(3) + `","transactionIndex":"1","from":"` + testTransactionSender().Hex() + `","to":"0xde709f2102306220921060314715629080e2fb77","value":"16","gas":"21000","gasPrice":"2000000000","isError":"0","txreceipt_status":"1","input":"0xdeadbeef00","contractAddress":"","cumulativeGasUsed":"42000","gasUsed":"21000","confirmations":"3","methodId":"0xdeadbeef","functionName":""}`
	if string(encoded) != want {
		t.Fatalf("transaction JSON\n got: %s\nwant: %s", encoded, want)
	}
}

func TestAccountTransactionsRejectRawIdentityMismatch(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t,
		completeCoreCoverageExpectation("0", "", "12"),
		sqlExpectation{
			contains: "FROM transaction_inclusions AS inclusion",
			columns:  fakeColumns(8),
			rows: [][]driver.Value{{
				testTransactionJSON(99, testRecipient),
				testReceiptJSON("0x1", ""), testBlockJSON(10, 2),
				"10", testHashBytes(3), int64(1), testTransactionHashBytes(testRecipient), "12",
			}},
		},
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	_, err := backend.Execute(context.Background(), Request{Module: "account", Action: "txlist", Values: url.Values{"address": {testSender}}})
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("error=%v", err)
	}
}

func TestMinedBlocksOmitsUnknownReward(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t,
		completeCoreCoverageExpectation("0", "", "10"),
		sqlExpectation{
			contains: "lower(block.raw->>'miner') = $2 ORDER BY block.number ASC, block.hash ASC",
			columns:  fakeColumns(3),
			rows:     [][]driver.Value{{testBlockJSON(10, 2), "10", testHashBytes(3)}},
		},
	)
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
	row := []driver.Value{testReceiptJSON("0x0", ""), testTransactionHashBytes(testRecipient), testHashBytes(3), "10", int64(1)}
	db := fakeDatabase(t,
		sqlExpectation{contains: "JOIN canonical_blocks AS canonical", columns: fakeColumns(5), rows: [][]driver.Value{row}},
		sqlExpectation{contains: "JOIN canonical_blocks AS canonical", columns: fakeColumns(5), rows: [][]driver.Value{row}},
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	values := url.Values{"txhash": {testTransactionHash(7, testRecipient).Hex()}}
	status, err := backend.Execute(context.Background(), Request{Module: "transaction", Action: "getstatus", Values: values})
	if err != nil || status != (transactionErrorStatus{IsError: "1", ErrDescription: "execution failed"}) {
		t.Fatalf("status=%#v error=%v", status, err)
	}
	receiptStatus, err := backend.Execute(context.Background(), Request{Module: "transaction", Action: "gettxreceiptstatus", Values: values})
	if err != nil || receiptStatus != (transactionReceiptStatus{Status: "0"}) {
		t.Fatalf("receipt status=%#v error=%v", receiptStatus, err)
	}
}

func TestLogsUseParameterizedTopicExpressionAndHexWireModel(t *testing.T) {
	t.Parallel()
	topic0, topic2 := testHash(21), testHash(23)
	db := fakeDatabase(t,
		completeCoreCoverageExpectation("5", "12", "12"),
		sqlExpectation{
			contains: "log.address = $4 AND (log.topic0 = $5 OR lower(log.raw->'topics'->>2) = $6) ORDER BY log.block_number DESC, log.log_index DESC, log.block_hash DESC LIMIT $7 OFFSET $8",
			columns:  fakeColumns(10),
			rows: [][]driver.Value{{
				testLogJSON(10, 3, 7, 1, 4, testContract, []string{topic0, testHash(22), topic2}),
				testReceiptJSON("0x1", ""),
				testTransactionJSON(7, testRecipient),
				testBlockJSON(10, 2),
				"10", testHashBytes(3), int64(4), int64(1), testTransactionHashBytes(testRecipient), testAddressBytes(testContract),
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
		},
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{Module: "logs", Action: "getLogs", Values: url.Values{
		"fromBlock": {"5"}, "toBlock": {"12"}, "address": {testContract},
		"topic0": {topic0}, "topic2": {topic2}, "topic0_2_opr": {"or"}, "sort": {"desc"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	logs := result.([]logEntry)
	if len(logs) != 1 || logs[0].BlockNumber != "0xa" || logs[0].BlockHash != testHash(3) ||
		logs[0].TimeStamp != "0x64" || logs[0].LogIndex != "0x4" ||
		logs[0].TransactionIndex != "0x1" || logs[0].GasPrice != "0x77359400" || logs[0].GasUsed != "0x5208" {
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
		completeCoreCoverageExpectation("0", "", "10"),
		sqlExpectation{contains: "block.timestamp <= $2::numeric ORDER BY block.timestamp DESC, block.number DESC", columns: fakeColumns(4), rows: [][]driver.Value{{testBlockJSON(10, 2), "10", testHashBytes(3), "100"}}},
		sqlExpectation{contains: "tip_coverage AS", columns: fakeColumns(8), rows: [][]driver.Value{{"10", "100", "2", "20", "9", "0", "0", "10"}}},
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
		contains: "tip_coverage AS", columns: fakeColumns(8), rows: [][]driver.Value{{"10", "100", "2", "20", "9", "0", "0", "10"}},
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
	expectations := verifiedArtifactExpectations(codeHash, codeHash, abi, sources, settings)
	expectations = append(expectations, verifiedArtifactExpectations(codeHash, codeHash, abi, sources, settings)...)
	expectations = append(expectations, sqlExpectation{contains: "JOIN verified_proxy_bindings AS binding", columns: fakeColumns(1)})
	db := fakeDatabase(t, expectations...)
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
	if len(source) != 1 || source[0].SourceCode != string(sources) || source[0].CompilerType != "solc" ||
		source[0].ContractFileName != "" || source[0].OptimizationUsed != "1" || source[0].Runs != "200" ||
		source[0].EVMVersion != "paris" || source[0].MatchKind != "full" {
		t.Fatalf("source=%+v", source)
	}
}

func TestVerifiedGeasContractExposesEmptyABIAndRuntimeFile(t *testing.T) {
	t.Parallel()
	codeHash := testHashBytes(19)
	abi := []byte(`[]`)
	sources := []byte(`{"withdrawals/main.eas":{"content":"push 1"}}`)
	settings := []byte(`{"runtime_entrypoint":"withdrawals/main.eas","stack_check":true}`)
	artifact := func() []sqlExpectation {
		return []sqlExpectation{
			currentArtifactTargetExpectation(codeHash),
			{
				contains: "FROM verified_contracts AS verified", columns: fakeColumns(24),
				rows: [][]driver.Value{{
					true, testAddressBytes(testContract), codeHash, "7", nil,
					"123e4567-e89b-42d3-a456-426614174000", testHashBytes(8),
					"withdrawals/main.eas", "Withdrawals", "geas", "0.3.3", "full",
					abi, sources, settings, []byte(`{}`), []byte(`{}`), []byte(`{}`),
					nil, []byte(`{"match_type":"full","transformations":[],"values":{}}`),
					nil, []byte(`{}`), false, time.Unix(100, 0).UTC(),
				}},
			},
		}
	}
	expectations := append(artifact(), artifact()...)
	expectations = append(expectations, sqlExpectation{
		contains: "JOIN verified_proxy_bindings AS binding", columns: fakeColumns(1),
	})
	db := fakeDatabase(t, expectations...)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	values := url.Values{"address": {testContract}}
	abiResult, err := backend.Execute(context.Background(), Request{
		Module: "contract", Action: "getabi", Values: values,
	})
	if err != nil || abiResult != "[]" {
		t.Fatalf("ABI=%#v error=%v", abiResult, err)
	}
	sourceAny, err := backend.Execute(context.Background(), Request{
		Module: "contract", Action: "getsourcecode", Values: values,
	})
	if err != nil {
		t.Fatal(err)
	}
	source := sourceAny.([]sourceCodeResult)
	if len(source) != 1 || source[0].CompilerType != "geas" ||
		source[0].ContractFileName != "withdrawals/main.eas" || source[0].ABI != "[]" ||
		source[0].SourceCode != string(sources) || source[0].CompilerVersion != "0.3.3" ||
		source[0].OptimizationUsed != "0" || source[0].Runs != "0" {
		t.Fatalf("source=%+v", source)
	}
}

func verifiedArtifactExpectations(
	targetCodeHash []byte,
	sourceCodeHash []byte,
	abi []byte,
	sources []byte,
	settings []byte,
) []sqlExpectation {
	return []sqlExpectation{
		currentArtifactTargetExpectation(targetCodeHash),
		verifiedArtifactSourceExpectation(true, sourceCodeHash, abi, sources, settings),
	}
}

func currentArtifactTargetExpectation(codeHash []byte) sqlExpectation {
	return sqlExpectation{
		contains: "FROM contract_code_observations AS candidate", columns: fakeColumns(4),
		rows: [][]driver.Value{{codeHash, "7", testHashBytes(3), "10"}},
	}
}

func verifiedArtifactSourceExpectation(
	exact bool,
	codeHash []byte,
	abi []byte,
	sources []byte,
	settings []byte,
) sqlExpectation {
	return sqlExpectation{
		contains: "FROM verified_contracts AS verified", columns: fakeColumns(24),
		rows: [][]driver.Value{{
			exact, testAddressBytes(testContract), codeHash, "7", nil,
			"123e4567-e89b-42d3-a456-426614174000", testHashBytes(8),
			"A.sol", "A", "solidity", "v0.8.30+commit.73712a01", "full",
			abi, sources, settings, []byte(`{}`), []byte(`{}`), []byte(`{}`),
			nil, []byte(`{"match_type":"full","transformations":[],"values":{}}`),
			nil, []byte(`{}`), false, time.Unix(100, 0).UTC(),
		}},
	}
}

func TestVerifiedProxyQueryRequiresCurrentExactV2Binding(t *testing.T) {
	t.Parallel()
	query := compactSQL(dbgen.EtherscanVerifiedProxy)
	for _, required := range []string{
		"observation.stage_version = 2",
		"JOIN published_block_stage_results AS published",
		"raw.proxy_pattern = 'clone' AND raw.evidence_state = 'exact'",
		"evidence.reason = 'immutable_args_creation_unverified' AND raw.proxy_pattern = 'clone' AND raw.evidence_state = 'exact' AND octet_length(raw.immutable_args) > 0 AND raw.details->>'immutable_args_creation_authenticated' = 'true'",
		"candidate.proxy_code_hash = raw.proxy_code_hash",
		"resolution.id IS NOT NULL",
		"FROM beacon_implementation_observations AS observation",
		"observation.beacon_code_hash = proxy.effective_beacon_hash",
		"observation.confidence IN ('verified', 'high')",
		"JOIN verified_proxy_bindings AS binding",
		"JOIN canonical_blocks AS binding_context",
		"binding_context.block_hash = binding.context_block_hash",
		"binding.observation_generation_id = current_proxy.observation_generation_id",
		"binding.artifact_resolution_id IS NOT DISTINCT FROM current_proxy.artifact_resolution_id",
		"binding.beacon_generation_id IS NOT DISTINCT FROM current_proxy.beacon_generation_id",
		"binding.uups_generation_id IS NOT DISTINCT FROM current_proxy.uups_generation_id",
		"ORDER BY observation.block_number DESC, observation.block_hash DESC, generation.id DESC, observation.verification_job_id DESC LIMIT 1 ) AS candidate WHERE NOT EXISTS",
		"conflict.block_number = candidate.block_number",
		"conflict.probe_state || ':' || COALESCE(conflict.rejection_reason, '')",
		"probe.probe_state = 'compatible'",
		"probe.block_number >= proxy.implementation_epoch_block",
		"published.state = 'complete'",
		"FROM proxy_detection_evidence AS evidence",
		"evidence.candidate_kind = 'proxy'",
		"evidence.job_generation >= raw.observation_job_generation",
		"evidence.candidate_kind = 'beacon'",
		"evidence.job_generation >= beacon.beacon_job_generation",
		"binding.standard_version IS NOT DISTINCT FROM current_proxy.standard_version",
		"binding.admin_address IS NOT DISTINCT FROM current_proxy.admin_address",
		"binding.beacon_address IS NOT DISTINCT FROM current_proxy.beacon_address",
		"proxy_interaction_coverage_contains( binding.chain_id, binding.observation_block_number, binding.observation_block_hash, current_proxy.context_number, current_proxy.context_hash )",
		"FROM contract_code_observations AS observation",
		"identity.current_code_hash IS DISTINCT FROM identity.code_hash",
		"observation.block_number > binding.context_block_number",
		"observation.block_number <= current_proxy.context_number",
		"observation.code_hash IS DISTINCT FROM identity.code_hash",
		"FROM transaction_state_changes AS change",
		"change.block_number > binding.context_block_number",
		"change.block_number <= current_proxy.context_number",
		"change.field_kind = 'code'",
		"lower(change.before_value) IS DISTINCT FROM lower(change.after_value)",
		"COALESCE(code_epoch.block_number, 0::numeric)",
		"FROM required_publication AS publication",
		"binding.proxy_pattern <> 'clone'",
		"verified.address = publication.address",
		"verified.valid_from_block >= publication.epoch_block",
		"FROM verified_contract_proxy_artifacts AS artifact",
		"verified.verification_job_id = artifact.verification_job_id",
		"verified.request_digest = artifact.request_digest",
		"artifact.valid_from_block >= identity.epoch_block",
		"artifact.verification_job_id = binding.proxy_artifact_job_id",
		"binding.implementation_artifact_job_id",
		"artifact.standard_version = '5.6.1'",
	} {
		if !strings.Contains(query, compactSQL(required)) {
			t.Fatalf("verified proxy query lacks %q: %s", compactSQL(required), query)
		}
	}
	for _, forbidden := range []string{"required_interaction_stage", "canonical_blocks AS coverage_block", "generate_series"} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("verified proxy query retains height-dependent coverage scan %q: %s", forbidden, query)
		}
	}
	if joins, complete := strings.Count(query, "published_block_stage_results AS"), strings.Count(query, "state = 'complete'"); joins != complete {
		t.Fatalf("verified proxy query complete-publication guards=%d, want one for each of %d joins: %s", complete, joins, query)
	}
}

func TestProxyVerificationTargetQueryFencesAllCurrentIdentities(t *testing.T) {
	t.Parallel()
	query := compactSQL(dbgen.EtherscanProxyVerificationTarget)
	for _, required := range []string{
		"observation.stage_version = 2",
		"JOIN published_block_stage_results AS published",
		"raw.proxy_pattern = 'clone' AND raw.evidence_state = 'exact'",
		"candidate.proxy_code_hash = raw.proxy_code_hash",
		"resolution.id IS NOT NULL",
		"FROM beacon_implementation_observations AS observation",
		"observation.beacon_code_hash = proxy.effective_beacon_hash",
		"observation.confidence IN ('verified', 'high')",
		"WHEN 'transparent' THEN 'proxy_admin'",
		"WHEN 'beacon' THEN 'upgradeable_beacon'",
		"current_proxy.management_kind = 'none' OR EXISTS",
		"FROM verified_contract_proxy_artifacts AS artifact",
		"artifact.standard_version = '5.6.1'",
		"FROM contract_code_observations AS observation",
		"identity.current_code_hash IS DISTINCT FROM identity.code_hash",
		"current_proxy.observation_generation_id",
		"current_proxy.context_number::text",
		"current_proxy.context_hash",
		"current_proxy.artifact_resolution_id",
		"current_proxy.beacon_generation_id",
		"current_proxy.uups_generation_id",
		"binding.uups_generation_id IS NOT DISTINCT FROM current_proxy.uups_generation_id",
		"candidate.proxy_pattern <> 'uups'",
		"ORDER BY observation.block_number DESC, observation.block_hash DESC, generation.id DESC, observation.verification_job_id DESC LIMIT 1 ) AS candidate WHERE NOT EXISTS",
		"conflict.block_number = candidate.block_number",
		"probe.probe_state = 'compatible'",
		"probe.block_number, probe.block_hash, proxy.context_number, proxy.context_hash",
		"current_proxy.proxy_pattern = 'clone' OR ( EXISTS",
		"proxy_interaction_coverage_contains( $1::numeric, current_proxy.block_number, current_proxy.block_hash, current_proxy.context_number, current_proxy.context_hash )",
		"proxy_interaction_coverage_contains( binding.chain_id, binding.observation_block_number, binding.observation_block_hash, current_proxy.context_number, current_proxy.context_hash )",
		"reusable_binding AS",
		"binding.observation_generation_id = current_proxy.observation_generation_id",
		"observation.block_number > binding.context_block_number",
		"FROM transaction_state_changes AS change",
		"change.block_number > binding.context_block_number",
		"change.block_number <= current_proxy.context_number",
		"lower(change.before_value) IS DISTINCT FROM lower(change.after_value)",
		"COALESCE(code_epoch.block_number, 0::numeric)",
		"verified.valid_from_block >= identity.epoch_block",
		"artifact.valid_from_block >= identity.epoch_block",
		"artifact.verification_job_id = current_proxy.proxy_artifact_job_id",
		"current_proxy.implementation_artifact_job_id",
		"binding.verification_job_id::text",
		"FROM proxy_detection_evidence AS evidence",
		"evidence.candidate_kind = 'proxy'",
		"evidence.candidate_kind = 'beacon'",
		"verified.verification_job_id = artifact.verification_job_id",
		"verified.request_digest = artifact.request_digest",
	} {
		if !strings.Contains(query, compactSQL(required)) {
			t.Fatalf("proxy target query lacks %q: %s", compactSQL(required), query)
		}
	}
	if count := strings.Count(query, "SELECT binding.verification_job_id FROM current_proxy"); count != 1 {
		t.Fatalf("reusable binding target projection count=%d, want 1: %s", count, query)
	}
	if joins, complete := strings.Count(query, "published_block_stage_results AS"), strings.Count(query, "state = 'complete'"); joins != complete {
		t.Fatalf("proxy target query complete-publication guards=%d, want one for each of %d joins: %s", complete, joins, query)
	}
	for _, forbidden := range []string{"required_interaction_stage", "canonical_blocks AS coverage_block", "generate_series"} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("proxy target query retains height-dependent coverage scan %q: %s", forbidden, query)
		}
	}
}

func TestVerifiedContractQueryBindsCanonicalCodeHashAndCurrentRange(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	if backend.artifacts == nil {
		t.Fatal("verified contract backend has no shared artifact resolver")
	}
}

func TestUnverifiedContractIsNotAnEmptySuccess(t *testing.T) {
	t.Parallel()
	currentCodeHash := testHashBytes(10)
	db := fakeDatabase(t,
		currentArtifactTargetExpectation(currentCodeHash),
		sqlExpectation{contains: "FROM verified_contracts AS verified", columns: fakeColumns(24)},
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{Module: "contract", Action: "getabi", Values: url.Values{"address": {testContract}}})
	if result != "" || !errors.Is(err, ErrContractUnverified) {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestVerifiedContractWithoutCanonicalCodeIsUnavailable(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t, sqlExpectation{
		contains: "FROM contract_code_observations AS candidate", columns: fakeColumns(4),
	})
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{Module: "contract", Action: "getabi", Values: url.Values{"address": {testContract}}})
	if result != "" || !errors.Is(err, ErrStateUnavailable) {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestVerifiedContractRejectsMismatchedStoredCodeHash(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t,
		currentArtifactTargetExpectation(testHashBytes(11)),
		verifiedArtifactSourceExpectation(false, testHashBytes(12), []byte(`[]`), []byte(`{}`), []byte(`{}`)),
	)
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{Module: "contract", Action: "getabi", Values: url.Values{"address": {testContract}}})
	if result != "" || err == nil || errors.Is(err, ErrContractUnverified) || errors.Is(err, ErrStateUnavailable) {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestContractCreationPreservesInputOrderAndChecksums(t *testing.T) {
	t.Parallel()
	created := crypto.CreateAddress(testTransactionSender(), 15).Hex()
	db := fakeDatabase(t, sqlExpectation{
		contains: "trace.call_type IN ('CREATE', 'CREATE2')", columns: fakeColumns(13),
		rows: [][]driver.Value{{
			"top_level", testReceiptJSON("0x1", created),
			testTransactionJSON(7, ""), testTransactionHashBytes(""), testHashBytes(3),
			"10", "100", int64(1), nil, nil, nil, nil, nil,
		}},
	})
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	result, err := backend.Execute(context.Background(), Request{Module: "contract", Action: "getcontractcreation", Values: url.Values{"contractaddresses": {created}}})
	if err != nil {
		t.Fatal(err)
	}
	items := result.([]contractCreationResult)
	if len(items) != 1 || items[0].ContractAddress != created || items[0].ContractCreator != testTransactionSender().Hex() || items[0].TxHash != testTransactionHash(7, "").Hex() || items[0].BlockNumber != "10" || items[0].Timestamp != "100" || items[0].ContractFactory != "" || items[0].CreationBytecode != "0xdeadbeef00" {
		t.Fatalf("items=%+v", items)
	}
}

func TestContractCreationIncludesFactoryCreateFacts(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t, sqlExpectation{
		contains: "trace.created_address = $2", columns: fakeColumns(13),
		rows: [][]driver.Value{{
			"trace", nil, testTransactionJSON(7, testRecipient),
			testTransactionHashBytes(testRecipient), testHashBytes(3), "10", "100", int64(1),
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
	if len(items) != 1 || items[0].ContractCreator != testTransactionSender().Hex() ||
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
				coreCoverageExpectation("0", "", "10", "0", nil, nil),
			},
			want: ErrCoreUnavailable,
		},
		{
			name: "trace coverage complete",
			expectations: []sqlExpectation{
				{contains: "WITH candidates AS", columns: fakeColumns(13)},
				completeCoreCoverageExpectation("0", "", "10"),
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
		db := fakeDatabase(t,
			completeCoreCoverageExpectation("0", "", "10"),
			sqlExpectation{contains: test.contains, columns: fakeColumns(test.columns)},
		)
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
	return address.Bytes()
}

func testBlockJSON(number uint64, parent byte) []byte {
	return mustJSON(map[string]any{
		"number": fmt.Sprintf("0x%x", number), "hash": testHash(3), "parentHash": testHash(parent),
		"timestamp": "0x64", "miner": testSender,
		"gasUsed": "0x5208", "gasLimit": "0x1c9c380", "transactions": []any{},
	})
}

func testTransactionJSON(transactionHash byte, to string) []byte {
	transaction := testSignedTransaction(transactionHash, to)
	encoded, err := transaction.MarshalJSON()
	if err != nil {
		panic(err)
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		panic(err)
	}
	value["blockHash"] = testHash(3)
	value["blockNumber"] = "0xa"
	value["transactionIndex"] = "0x1"
	value["from"] = testTransactionSender().Hex()
	return mustJSON(value)
}

func testReceiptJSON(status, contract string) []byte {
	to := testRecipient
	if contract != "" {
		to = ""
	}
	value := map[string]any{
		"transactionHash": testTransactionHash(7, to).Hex(), "transactionIndex": "0x1",
		"blockHash": testHash(3), "blockNumber": "0xa",
		"cumulativeGasUsed": "0xa410", "gasUsed": "0x5208", "effectiveGasPrice": "0x77359400",
		"logs": []any{}, "logsBloom": "0x" + strings.Repeat("00", types.BloomByteLength), "status": status,
		"type": "0x0", "contractAddress": nil,
	}
	if contract != "" {
		value["contractAddress"] = contract
	}
	return mustJSON(value)
}

func testLogJSON(blockNumber uint64, blockHash, transactionHash byte, transactionIndex, logIndex uint64, address string, topics []string) []byte {
	return mustJSON(map[string]any{
		"removed": false, "blockHash": testHash(blockHash), "blockNumber": fmt.Sprintf("0x%x", blockNumber),
		"transactionHash": testTransactionHash(transactionHash, testRecipient).Hex(), "transactionIndex": fmt.Sprintf("0x%x", transactionIndex),
		"logIndex": fmt.Sprintf("0x%x", logIndex), "address": address, "topics": topics, "data": "0x1234",
	})
}

func testSignedTransaction(seed byte, recipient string) *types.Transaction {
	key, err := crypto.HexToECDSA("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		panic(err)
	}
	var to *common.Address
	if recipient != "" {
		address := common.HexToAddress(recipient)
		to = &address
	}
	transaction := types.NewTx(&types.LegacyTx{
		Nonce: uint64(seed) + 8, GasPrice: big.NewInt(2_000_000_000),
		Gas: 21_000, To: to, Value: big.NewInt(16), Data: []byte{0xde, 0xad, 0xbe, 0xef, 0},
	})
	signed, err := types.SignTx(transaction, types.LatestSignerForChainID(big.NewInt(1)), key)
	if err != nil {
		panic(err)
	}
	return signed
}

func testTransactionHash(seed byte, recipient string) common.Hash {
	return testSignedTransaction(seed, recipient).Hash()
}

func testTransactionHashBytes(recipient string) []byte {
	return testTransactionHash(7, recipient).Bytes()
}

func testTransactionSender() common.Address {
	key, err := crypto.HexToECDSA("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		panic(err)
	}
	return crypto.PubkeyToAddress(key.PublicKey)
}

func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
