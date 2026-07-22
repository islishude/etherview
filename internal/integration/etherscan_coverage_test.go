//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"

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
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{block0}); err != nil {
		t.Fatalf("commit genesis coverage island: %v", err)
	}
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{block2}); err != nil {
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

	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{block1}); err != nil {
		t.Fatalf("fill core coverage gap: %v", err)
	}

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
}

func etherscanCoverageBundle(number uint64, blockHash, parentHash, transactionHash ethrpc.Hash) ethrpc.Bundle {
	bundle := testBundle(number, blockHash, parentHash, transactionHash, "etherscan-coverage")
	miner := testAddress(1)
	gasPrice := ethrpc.QuantityFromUint64(2_000_000_000)
	bundle.Block.Miner = &miner
	bundle.Block.Transactions[0].Transaction.GasPrice = &gasPrice
	bundle.Receipts[0].EffectiveGasPrice = &gasPrice
	return bundle
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
