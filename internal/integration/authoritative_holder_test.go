//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

func TestAuthoritativeHolderStagePublishesNativeAndCompatibilityReads(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(180_000), testHash(0), testHash(181_000), "holder-genesis")
	tip := testBundle(1, testHash(180_001), testHash(180_000), testHash(181_001), "holder-tip")
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{genesis, tip}); err != nil {
		t.Fatal(err)
	}
	for _, block := range []chainbundle.Bundle{genesis, tip} {
		markProxyStageComplete(t, ctx, db, block)
		markTokenStageComplete(t, ctx, db, block)
	}

	token, owner := testAddress(180_010), testAddress(180_011)
	insertTokenObservation(t, ctx, db, tip, token, testHash(180_012), "Holder Token")
	reference := mustBlockRef(t, tip)
	transactionHash := tip.Block.Transactions()[0].Hash()
	execFixture(t, ctx, db, `
		INSERT INTO token_events (
			chain_id, block_number, block_hash, log_index, sub_index,
			transaction_hash, token_address, standard, event_kind,
			from_address, to_address, amount, canonical, confidence, raw
		) VALUES (1, 1, $1, 0, 0, $2, $3, 'erc20', 'mint',
			$4, $5, 7, TRUE, 'high', '{}'::jsonb)`,
		mustBytes(t, reference.Hash), mustBytes(t, transactionHash), mustBytes(t, token),
		make([]byte, common.AddressLength), mustBytes(t, owner),
	)

	state := &holderIntegrationState{token: token, supply: big.NewInt(7), balances: map[common.Address]*big.Int{owner: big.NewInt(7)}}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "holder-state", Client: newIntegrationRPCClient(t, "eth", state),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresHolderProcessor(db, pool)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range []chainbundle.Bundle{genesis, tip} {
		blockRef := mustBlockRef(t, block)
		word, parseErr := enrich.ParseWord(blockRef.Hash.String())
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
			Stage: enrich.HolderStage, ChainID: "1", BlockHash: word, BlockNumber: blockRef.Number,
		}); err != nil {
			t.Fatal(err)
		}
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "holder-integration", LeaseDuration: 5 * time.Second, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, worker)
	processOne(t, ctx, worker)

	catalogReader, err := catalog.NewPostgres(db, catalog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := catalogReader.TokenHolders(ctx, catalog.TokenHolderRequest{
		ChainID: "1", TokenAddress: token.Hex(), Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].HolderAddress != owner.Hex() ||
		page.Items[0].Balance != "7" || page.Summary.HolderCount != "1" ||
		page.Summary.Snapshot.BlockNumber != "1" {
		t.Fatalf("native holder page=%+v", page)
	}

	backend, err := etherscan.NewPostgresBackend(db, etherscan.PostgresOptions{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	compatibility, err := backend.Execute(ctx, etherscan.Request{
		Module: "token", Action: "tokenholderlist",
		Values: url.Values{"contractaddress": {token.Hex()}, "offset": {"100"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := etherscanResultRows(t, compatibility)
	if len(rows) != 1 || rows[0]["TokenHolderAddress"] != owner.Hex() || rows[0]["TokenHolderQuantity"] != "7" {
		t.Fatalf("compatibility holders=%#v", rows)
	}
	count, err := backend.Execute(ctx, etherscan.Request{
		Module: "token", Action: "tokenholdercount",
		Values: url.Values{"contractaddress": {token.Hex()}},
	})
	if err != nil || count != "1" {
		t.Fatalf("compatibility holder count=%#v error=%v", count, err)
	}
	if state.calls != 2 {
		t.Fatalf("holder exact-state calls=%d, want totalSupply plus balanceOf", state.calls)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE erc20_holder_balances SET balance = 8
		WHERE chain_id = 1 AND token_address = $1 AND holder_address = $2`,
		mustBytes(t, token), mustBytes(t, owner)); err == nil {
		t.Fatal("direct mutation of an exact holder balance succeeded")
	}

	next := testBundle(2, testHash(180_002), testHash(180_001), testHash(181_002), "holder-incremental")
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{next}); err != nil {
		t.Fatal(err)
	}
	markProxyStageComplete(t, ctx, db, next)
	markTokenStageComplete(t, ctx, db, next)
	nextReference := mustBlockRef(t, next)
	execFixture(t, ctx, db, `
		INSERT INTO token_events (
			chain_id, block_number, block_hash, log_index, sub_index,
			transaction_hash, token_address, standard, event_kind,
			from_address, to_address, amount, canonical, confidence, raw
		) VALUES (1, 2, $1, 0, 0, $2, $3, 'erc20', 'burn',
			$4, $5, 2, TRUE, 'high', '{}'::jsonb)`,
		mustBytes(t, nextReference.Hash), mustBytes(t, next.Block.Transactions()[0].Hash()),
		mustBytes(t, token), mustBytes(t, owner), make([]byte, common.AddressLength),
	)
	state.supply, state.balances[owner] = big.NewInt(5), big.NewInt(5)
	nextWord, err := enrich.ParseWord(nextReference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.HolderStage, ChainID: "1", BlockHash: nextWord, BlockNumber: 2,
	}); err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, worker)
	incremental, err := catalogReader.TokenHolders(ctx, catalog.TokenHolderRequest{
		ChainID: "1", TokenAddress: token.Hex(), Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(incremental.Items) != 1 || incremental.Items[0].Balance != "5" ||
		incremental.Items[0].ObservedBlockNumber != "2" || incremental.Summary.TotalSupply != "5" ||
		state.calls != 4 {
		t.Fatalf("incremental holder page=%+v calls=%d", incremental, state.calls)
	}

	replacement := testBundle(2, testHash(180_003), testHash(180_001), testHash(181_003), "holder-replacement")
	applyDerivedReorg(t, ctx, repository, tip, []chainbundle.Bundle{next}, []chainbundle.Bundle{replacement}, "holder reorg")
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM erc20_holder_balances
		WHERE chain_id = 1 AND token_address = $1 AND holder_address = $2 AND canonical = FALSE`,
		1, mustBytes(t, token), mustBytes(t, owner))
	if _, err := catalogReader.TokenHolders(ctx, catalog.TokenHolderRequest{
		ChainID: "1", TokenAddress: token.Hex(), Limit: 50,
	}); !errors.Is(err, catalog.ErrUnavailable) {
		t.Fatalf("replacement holder query error=%v, want unavailable", err)
	}
}

func markProxyStageComplete(t *testing.T, ctx context.Context, db *sql.DB, block chainbundle.Bundle) {
	t.Helper()
	reference := mustBlockRef(t, block)
	word, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word,
		BlockNumber: reference.Number, MaxAttempts: 1,
	}); err != nil {
		t.Fatal(err)
	}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint(t, "holder-proxy-state", map[string]map[string]proxyContractState{
			reference.Hash.String(): {},
		}, nil, &sync.Mutex{}, make(map[string][]string)),
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "holder-proxy-fixture", LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, worker)
}

type holderIntegrationState struct {
	token    common.Address
	supply   *big.Int
	balances map[common.Address]*big.Int
	calls    int
}

func (state *holderIntegrationState) Call(
	_ context.Context,
	request map[string]any,
	selector rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	state.calls++
	if selector.BlockHash == nil || !selector.RequireCanonical {
		return nil, errors.New("holder fixture requires canonical block hash")
	}
	target, ok := request["to"].(string)
	if !ok || common.HexToAddress(target) != state.token {
		return nil, errors.New("holder fixture token mismatch")
	}
	dataText, ok := request["data"].(string)
	if !ok {
		return nil, errors.New("holder fixture calldata is not hexadecimal")
	}
	data, err := hexutil.Decode(dataText)
	if err != nil {
		return nil, err
	}
	value := state.supply
	if len(data) == 36 && fmt.Sprintf("%x", data[:4]) == "70a08231" {
		value = state.balances[common.BytesToAddress(data[16:])]
	} else if len(data) != 4 || fmt.Sprintf("%x", data) != "18160ddd" {
		return nil, errors.New("holder fixture selector is unsupported")
	}
	if value == nil {
		return nil, errors.New("holder fixture balance is missing")
	}
	result := make([]byte, 32)
	value.FillBytes(result)
	return result, nil
}
