//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

func TestEtherscanCoreCoverageDistinguishesGapsFromAuthoritativeResults(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatalf("create PostgreSQL repository: %v", err)
	}
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatalf("configure index: %v", err)
	}

	block0 := etherscanCoverageBundle(0, testHash(30_000), testHash(0), testHash(31_000))
	block1 := etherscanCoverageBundle(1, testHash(30_001), testHash(30_000), testHash(31_001))
	block2 := etherscanCoverageBundle(2, testHash(30_002), testHash(30_001), testHash(31_002))
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{block0}); err != nil {
		t.Fatalf("commit genesis coverage island: %v", err)
	}
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{block2}); err != nil {
		t.Fatalf("commit live coverage island: %v", err)
	}

	backend, err := etherscan.NewPostgresBackend(db, etherscan.PostgresOptions{ChainID: 1})
	if err != nil {
		t.Fatalf("create Etherscan backend: %v", err)
	}
	for _, request := range []etherscan.Request{
		{
			Module: "account", Action: "txlist",
			Values: url.Values{"address": {testAddress(1).String()}, "startblock": {"0"}, "endblock": {"2"}},
		},
		{
			Module: "logs", Action: "getLogs",
			Values: url.Values{"fromBlock": {"0"}, "toBlock": {"2"}},
		},
		{
			Module: "account", Action: "getminedblocks",
			Values: url.Values{"address": {testAddress(1).String()}},
		},
		{
			Module: "account", Action: "txsBeaconWithdrawal",
			Values: url.Values{"startblock": {"0"}, "endblock": {"2"}},
		},
		{
			Module: "block", Action: "getblocknobytime",
			Values: url.Values{"timestamp": {"1700000001"}, "closest": {"before"}},
		},
		{
			Module: "transaction", Action: "getstatus",
			Values: url.Values{"txhash": {testHash(31_001).String()}},
		},
	} {
		if _, err := backend.Execute(ctx, request); !errors.Is(err, etherscan.ErrCoreUnavailable) {
			t.Fatalf("%s.%s gap error = %v, want core unavailable", request.Module, request.Action, err)
		}
	}

	if _, err := backend.Execute(ctx, etherscan.Request{
		Module: "account", Action: "txlist",
		Values: url.Values{"address": {testAddress(1).String()}, "startblock": {"3"}, "endblock": {"4"}},
	}); !errors.Is(err, etherscan.ErrNotFound) {
		t.Fatalf("future-only range error = %v, want no records", err)
	}
	if _, err := backend.Execute(ctx, etherscan.Request{
		Module: "block", Action: "getblockcountdown",
		Values: url.Values{"blockno": {"4"}},
	}); !errors.Is(err, etherscan.ErrEstimateUnavailable) {
		t.Fatalf("single-block tip island countdown error = %v, want estimate unavailable", err)
	}

	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{block1}); err != nil {
		t.Fatalf("fill core coverage gap: %v", err)
	}
	for _, block := range []chainbundle.Bundle{block0, block1, block2} {
		markTokenStageComplete(t, ctx, db, block)
		markTraceStageComplete(t, ctx, db, block)
	}
	blockOneReference := mustBlockRef(t, block1)
	execFixture(t, ctx, db, `
		INSERT INTO normalized_traces (
			chain_id, block_number, block_hash, transaction_hash,
			transaction_index, trace_path, parent_path, depth, call_type,
			from_address, to_address, value, gas, gas_used, input, output,
			error, reverted, canonical
		) VALUES (1, 1, $1, $2, 0, '0', '', 1, 'CALL',
			$3, $4, 5, 21000, 20000, ''::bytea, ''::bytea,
			NULL, FALSE, TRUE)`,
		mustBytes(t, blockOneReference.Hash), mustBytes(t, block1.Block.Transactions()[0].Hash()),
		mustBytes(t, testAddress(10)), mustBytes(t, testAddress(11)),
	)

	transactions, err := backend.Execute(ctx, etherscan.Request{
		Module: "account", Action: "txlist",
		Values: url.Values{"address": {testAddress(1).String()}, "startblock": {"0"}, "endblock": {"2"}},
	})
	if err != nil {
		t.Fatalf("query covered transactions: %v", err)
	}
	transactionRows := etherscanResultRows(t, transactions)
	if len(transactionRows) != 3 || transactionRows[0]["blockNumber"] != "0" || transactionRows[2]["blockNumber"] != "2" {
		t.Fatalf("covered transactions = %#v", transactionRows)
	}
	advancedTransactions, err := backend.Execute(ctx, etherscan.Request{
		Module: "account", Action: "txlist",
		Values: url.Values{
			"to": {testAddress(1).String()}, "fromto_opr": {"and"},
			"startblock": {"0"}, "endblock": {"latest"},
		},
	})
	if err != nil {
		t.Fatalf("query covered advanced transactions: %v", err)
	}
	if rows := etherscanResultRows(t, advancedTransactions); len(rows) != 3 {
		t.Fatalf("covered advanced transactions = %#v", rows)
	}
	advancedInternal, err := backend.Execute(ctx, etherscan.Request{
		Module: "account", Action: "txlistinternal",
		Values: url.Values{
			"from": {testAddress(10).String()}, "to": {testAddress(11).String()},
			"fromto_opr": {"and"}, "startblock": {"0"}, "endblock": {"latest"},
		},
	})
	if err != nil {
		t.Fatalf("query covered advanced internal transactions: %v", err)
	}
	if rows := etherscanResultRows(t, advancedInternal); len(rows) != 1 || rows[0]["value"] != "5" {
		t.Fatalf("covered advanced internal transactions = %#v", rows)
	}

	withdrawals, err := backend.Execute(ctx, etherscan.Request{
		Module: "account", Action: "txsBeaconWithdrawal",
		Values: url.Values{"address": {testAddress(2).String()}, "startblock": {"0"}, "endblock": {"2"}},
	})
	if err != nil {
		t.Fatalf("query covered beacon withdrawals: %v", err)
	}
	withdrawalRows := etherscanResultRows(t, withdrawals)
	if len(withdrawalRows) != 1 || withdrawalRows[0]["withdrawalIndex"] != "1" ||
		withdrawalRows[0]["amount"] != "3200000000" || withdrawalRows[0]["blockNumber"] != "1" {
		t.Fatalf("covered beacon withdrawals = %#v", withdrawalRows)
	}

	logs, err := backend.Execute(ctx, etherscan.Request{
		Module: "logs", Action: "getLogs",
		Values: url.Values{"fromBlock": {"0"}, "toBlock": {"2"}},
	})
	if err != nil {
		t.Fatalf("query covered logs: %v", err)
	}
	logRows := etherscanResultRows(t, logs)
	if len(logRows) != 3 || logRows[0]["blockNumber"] != "0x0" || logRows[2]["blockNumber"] != "0x2" {
		t.Fatalf("covered logs = %#v", logRows)
	}
	topic0 := testHash(9_000).String()
	topic2 := testHash(9_002).String()
	filteredLogs, err := backend.Execute(ctx, etherscan.Request{
		Module: "logs", Action: "getLogs",
		Values: url.Values{
			"fromBlock": {"0"}, "toBlock": {"2"},
			"topic0": {topic0}, "topic2": {topic2}, "topic0_2_opr": {"or"},
		},
	})
	if err != nil {
		t.Fatalf("query fixed OR topic filter: %v", err)
	}
	if rows := etherscanResultRows(t, filteredLogs); len(rows) != 3 {
		t.Fatalf("fixed OR topic filter rows = %#v", rows)
	}
	if _, err := backend.Execute(ctx, etherscan.Request{
		Module: "logs", Action: "getLogs",
		Values: url.Values{
			"fromBlock": {"0"}, "toBlock": {"2"},
			"topic0": {topic0}, "topic2": {topic2}, "topic0_2_opr": {"and"},
		},
	}); !errors.Is(err, etherscan.ErrNotFound) {
		t.Fatalf("fixed AND topic filter error = %v, want no records", err)
	}

	mined, err := backend.Execute(ctx, etherscan.Request{
		Module: "account", Action: "getminedblocks",
		Values: url.Values{"address": {testAddress(1).String()}},
	})
	if err != nil {
		t.Fatalf("query covered mined blocks: %v", err)
	}
	minedRows := etherscanResultRows(t, mined)
	if len(minedRows) != 3 || minedRows[0]["blockNumber"] != "0" || minedRows[2]["blockNumber"] != "2" {
		t.Fatalf("covered mined blocks = %#v", minedRows)
	}
	if _, exists := minedRows[0]["blockReward"]; exists {
		t.Fatalf("mined block fabricated an unknown reward: %#v", minedRows[0])
	}

	byTime, err := backend.Execute(ctx, etherscan.Request{
		Module: "block", Action: "getblocknobytime",
		Values: url.Values{"timestamp": {"1700000001"}, "closest": {"before"}},
	})
	if err != nil || byTime != "1" {
		t.Fatalf("covered block by time = %#v, error = %v", byTime, err)
	}
	if _, err := backend.Execute(ctx, etherscan.Request{
		Module: "block", Action: "getblockcountdown",
		Values: url.Values{"blockno": {"4"}},
	}); err != nil {
		t.Fatalf("query covered block countdown: %v", err)
	}

	status, err := backend.Execute(ctx, etherscan.Request{
		Module: "transaction", Action: "getstatus",
		Values: url.Values{"txhash": {testHash(31_001).String()}},
	})
	if err != nil {
		t.Fatalf("query covered transaction status: %v", err)
	}
	statusPayload, err := json.Marshal(status)
	if err != nil || string(statusPayload) != `{"isError":"0","errDescription":""}` {
		t.Fatalf("covered transaction status = %s, error = %v", statusPayload, err)
	}

	countResult, err := backend.Execute(ctx, etherscan.Request{
		Module: "block", Action: "getblocktxnscount", Values: url.Values{"blockno": {"1"}},
	})
	if err != nil {
		t.Fatalf("query covered block transaction counts: %v", err)
	}
	countPayload, _ := json.Marshal(countResult)
	if string(countPayload) != `{"block":"1","txsCount":"1","internalTxsCount":"1","erc20TxsCount":"0","erc721TxsCount":"0","erc1155TxsCount":"0"}` {
		t.Fatalf("covered block counts = %s", countPayload)
	}

	blockZeroReference := mustBlockRef(t, block0)
	execFixture(t, ctx, db, `
		UPDATE receipts
		SET raw = (raw - 'status') || jsonb_build_object('root', $3::text)
		WHERE chain_id = 1 AND block_number = 0 AND block_hash = $1 AND tx_hash = $2`,
		mustBytes(t, blockZeroReference.Hash), mustBytes(t, block0.Block.Transactions()[0].Hash()),
		"0x"+strings.Repeat("00", common.HashLength),
	)
	tipReference := mustBlockRef(t, block2)
	stateProvider := &integrationEtherscanState{
		db: db, blockNumber: fmt.Sprint(tipReference.Number), blockHash: tipReference.Hash.String(),
	}
	fundingBackend, err := etherscan.NewPostgresBackend(db, etherscan.PostgresOptions{ChainID: 1, State: stateProvider})
	if err != nil {
		t.Fatal(err)
	}
	funding, err := fundingBackend.Execute(ctx, etherscan.Request{
		Module: "account", Action: "fundedby", Values: url.Values{"address": {testAddress(1).String()}},
	})
	if err != nil {
		t.Fatalf("query covered funding origin: %v", err)
	}
	fundingPayload, _ := json.Marshal(funding)
	var fundingFields map[string]string
	if err := json.Unmarshal(fundingPayload, &fundingFields); err != nil ||
		fundingFields["block"] != "0" || fundingFields["value"] != "1" ||
		fundingFields["fundingAddress"] == "" || fundingFields["fundingTxn"] == "" {
		t.Fatalf("covered funding origin = %s, error = %v", fundingPayload, err)
	}
}

func etherscanCoverageBundle(number uint64, blockHash, parentHash, transactionHash common.Hash) chainbundle.Bundle {
	miner := testAddress(1)
	gasPrice := big.NewInt(2_000_000_000)
	withdrawals := []*types.Withdrawal{}
	if number == 1 {
		withdrawals = append(withdrawals, &types.Withdrawal{
			Index: 1, Validator: 117823, Address: testAddress(2), Amount: 3_200_000_000,
		})
	}
	bundle, err := newIntegrationBundle(integrationBundleOptions{
		Number:     number,
		ParentHash: parentHash,
		ExtraData:  []byte("etherscan-coverage"),
		Coinbase:   miner,
		Transactions: []integrationTransactionOptions{{
			Type:     types.LegacyTxType,
			To:       &miner,
			GasPrice: gasPrice,
			Value:    new(big.Int).SetUint64(number + 1),
			Data:     transactionHash.Bytes(),
			Logs: []*types.Log{{
				Address: testAddress(3),
				Topics:  []common.Hash{testHash(9_000)},
				Data:    []byte("etherscan-coverage"),
			}},
		}},
		Withdrawals: withdrawals,
		RawExtra:    map[string]any{"integrationVariant": "etherscan-coverage"},
	})
	if err != nil {
		panic(fmt.Sprintf("build Etherscan coverage bundle: %v", err))
	}
	registerFixtureIdentities(blockHash, bundle.Block.Hash(), transactionHash, bundle.Block.Transactions()[0].Hash())
	return bundle
}

type integrationEtherscanState struct {
	db                     *sql.DB
	blockNumber, blockHash string
}

func (*integrationEtherscanState) NativeBalances(context.Context, []string) ([]string, error) {
	return nil, errors.New("unexpected balance request")
}

func (*integrationEtherscanState) ERC20Balance(context.Context, string, string) (string, error) {
	return "", errors.New("unexpected token balance request")
}

func (*integrationEtherscanState) ERC20TotalSupply(context.Context, string) (string, error) {
	return "", errors.New("unexpected token supply request")
}

func (state *integrationEtherscanState) AccountKind(context.Context, string) (string, string, string, error) {
	return "eoa", state.blockNumber, state.blockHash, nil
}

func (state *integrationEtherscanState) IsCanonical(ctx context.Context, number, hash string) (bool, error) {
	var canonical bool
	err := state.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM canonical_blocks
			WHERE chain_id = 1 AND number = $1::numeric
			  AND block_hash = decode(substr($2, 3), 'hex')
		)`, number, hash).Scan(&canonical)
	return canonical, err
}

func markTraceStageComplete(t *testing.T, ctx context.Context, db *sql.DB, block chainbundle.Bundle) {
	t.Helper()
	reference := mustBlockRef(t, block)
	if _, err := db.ExecContext(ctx, `
		UPDATE transactional_outbox
		SET published_at = clock_timestamp()
		WHERE chain_id = 1 AND topic = 'core.block.canonical' AND message_key = $1`,
		reference.Hash.String(),
	); err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	hashes := make([]common.Hash, len(block.Block.Transactions()))
	for index, transaction := range block.Block.Transactions() {
		hashes[index] = transaction.Hash()
	}
	service := &traceStageService{
		blockHash: block.Block.Hash(), hashes: hashes,
		raw: traceStageCallTracerResponse(t, block.Block.Transactions()[0]),
	}
	client := newIntegrationRPCClient(t, "debug", service)
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "etherscan-trace", Client: client,
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeTrace: true},
		Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{
			ethrpc.CapabilityDebugTrace: ethrpc.AvailabilityAvailable,
		}},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewTraceRPCProcessor(db, pool, enrich.TraceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.TraceStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
	}); err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "etherscan-trace-" + fmt.Sprint(reference.Number), LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("publish trace stage for block %d: processed=%t error=%v", reference.Number, processed, err)
	}
}

func etherscanResultRows(t *testing.T, result any) []map[string]any {
	t.Helper()
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal Etherscan result: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(payload, &rows); err != nil {
		t.Fatalf("decode Etherscan rows %s: %v", payload, err)
	}
	return rows
}
