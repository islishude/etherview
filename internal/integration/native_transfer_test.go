//go:build integration

package integration_test

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/nativetransfer"
	"github.com/islishude/etherview/internal/publicquery"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
)

func TestNativeTransferPublicationPaginationAndReorg(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, _ := store.NewPostgresRepository(db)
	genesis := testBundle(0, testHash(190000), testHash(0), testHash(190001), "native-pre-fork")
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{genesis}); err != nil {
		t.Fatal(err)
	}
	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	from, to := common.HexToAddress("0x1234"), common.HexToAddress("0x5678")
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	makeBlock := func(extra string, logs bool) chainbundle.Bundle {
		var entries []*types.Log
		if logs {
			for range 2 {
				entries = append(entries, &types.Log{Address: params.SystemAddress, Topics: []common.Hash{nativetransfer.Topic, common.BytesToHash(from[:]), common.BytesToHash(to[:])}, Data: max.FillBytes(make([]byte, 32))})
			}
		}
		bundle, err := newIntegrationBundle(integrationBundleOptions{Number: 1, ParentHash: genesis.Block.Hash(), ExtraData: []byte(extra), Amsterdam: true, GrossGasExtra: 1000, Transactions: []integrationTransactionOptions{{Type: 2, Logs: entries}}})
		if err != nil {
			t.Fatal(err)
		}
		return bundle
	}
	block := makeBlock("native", true)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{block}); err != nil {
		t.Fatal(err)
	}
	request := publicquery.NativeTransferRequest{Address: from.Hex(), Limit: 1}
	if _, err := reader.NativeTransfers(ctx, request); err == nil {
		t.Fatal("unpublished transfer coverage was readable")
	}
	queue, _ := enrich.NewPostgresJobQueue(db)
	processor, _ := enrich.NewPostgresNativeTransferProcessor(db)
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{ID: "native-test", LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	publish := func(bundle chainbundle.Bundle, replay string) {
		execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at=now()`)
		word, _ := enrich.ParseWord(bundle.Block.Hash().Hex())
		_, err := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.NativeTransferStage, ChainID: "1", BlockHash: word, BlockNumber: bundle.Block.NumberU64(), Replay: enrich.ReplaySource{Kind: "native-test", Key: replay}})
		if err != nil {
			t.Fatal(err)
		}
		if found, err := worker.ProcessOne(ctx); err != nil || !found {
			t.Fatalf("publish found=%t err=%v", found, err)
		}
	}
	wordBefore, _ := enrich.ParseWord(block.Block.Hash().Hex())
	queued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.NativeTransferStage, ChainID: "1", BlockHash: wordBefore, BlockNumber: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE durable_jobs SET status='succeeded' WHERE id=$1`, queued.Job.ID); err == nil {
		t.Fatal("database accepted unfenced native terminal publication")
	}
	publish(block, "initial")
	if _, err := processor.Process(ctx, queued.Job); !errors.Is(err, enrich.ErrAtomicPublicationRequired) {
		t.Fatalf("unleased process=%v", err)
	}
	page, err := reader.NativeTransfers(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !page.Applicable || len(page.Items) != 1 || page.NextCursor == "" || page.Items[0].Amount != max.String() || page.Items[0].LogIndex != "1" {
		t.Fatalf("first page=%+v", page)
	}
	originalCursor := page.NextCursor
	request.Cursor = page.NextCursor
	page, err = reader.NativeTransfers(ctx, request)
	if err != nil || len(page.Items) != 1 || page.Items[0].LogIndex != "0" || page.NextCursor != "" {
		t.Fatalf("next page=%+v err=%v", page, err)
	}
	request.Address = to.Hex()
	if _, err := reader.NativeTransfers(ctx, request); !errors.Is(err, publicquery.ErrInvalidCursor) {
		t.Fatalf("cross-address cursor=%v", err)
	}
	pre, err := reader.NativeTransfers(ctx, publicquery.NativeTransferRequest{TransactionHash: genesis.Block.Transactions()[0].Hash().Hex(), Limit: 10})
	if err != nil || pre.Applicable {
		t.Fatalf("pre-fork=%+v err=%v", pre, err)
	}
	txPage, err := reader.NativeTransfers(ctx, publicquery.NativeTransferRequest{TransactionHash: block.Block.Transactions()[0].Hash().Hex(), Limit: 10})
	if err != nil || len(txPage.Items) != 2 {
		t.Fatalf("transaction=%+v err=%v", txPage, err)
	}
	// A replay request immediately revokes availability, before any worker runs.
	word, _ := enrich.ParseWord(block.Block.Hash().Hex())
	if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.NativeTransferStage, ChainID: "1", BlockHash: word, BlockNumber: 1, Replay: enrich.ReplaySource{Kind: "native-test", Key: "replay"}}); err != nil {
		t.Fatal(err)
	}
	request.Cursor = ""
	request.Address = from.Hex()
	if _, err := reader.NativeTransfers(ctx, request); err == nil {
		t.Fatal("replay retained stale publication")
	}
	if found, err := worker.ProcessOne(ctx); err != nil || !found {
		t.Fatalf("replay found=%t err=%v", found, err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM native_transfers`, 2)
	replacement := makeBlock("native-replacement", false)
	applyDerivedReorg(t, ctx, repository, genesis, []chainbundle.Bundle{block}, []chainbundle.Bundle{replacement}, "native replacement")
	assertRowCount(t, ctx, db, `SELECT count(*) FROM native_transfers WHERE canonical`, 0)
	request.Cursor = originalCursor
	if _, err := reader.NativeTransfers(ctx, request); !errors.Is(err, publicquery.ErrInvalidCursor) {
		t.Fatalf("reorg cursor=%v", err)
	}
	request.Cursor = ""
	publish(replacement, "replacement")
	page, err = reader.NativeTransfers(ctx, request)
	if err != nil || !page.Applicable || len(page.Items) != 0 {
		t.Fatalf("empty replacement=%+v err=%v", page, err)
	}
	applyDerivedReorg(t, ctx, repository, genesis, []chainbundle.Bundle{replacement}, []chainbundle.Bundle{block}, "native reattach")
	if _, err := reader.NativeTransfers(ctx, request); err == nil {
		t.Fatal("reattach reused stale publication")
	}
	publish(block, "reattach")
	page, err = reader.NativeTransfers(ctx, request)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("reattached=%+v err=%v", page, err)
	}
}

func TestAmsterdamStatisticsSeparateGrossAndChargedGas(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repository, _ := store.NewPostgresRepository(db)
	bundle, err := newIntegrationBundle(integrationBundleOptions{Amsterdam: true, GrossGasExtra: 4000, BaseFee: big.NewInt(2), Transactions: []integrationTransactionOptions{{Type: 2, GasPrice: big.NewInt(3)}}})
	if err != nil {
		t.Fatal(err)
	}
	commitCanonical(t, ctx, repository, bundle)
	configureAtomicStatsStart(t, ctx, db)
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at=now()`)
	queue, _ := enrich.NewPostgresJobQueue(db)
	processor, _ := enrich.NewPostgresStatsProcessor(db)
	word, _ := enrich.ParseWord(bundle.Block.Hash().Hex())
	if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: 0}); err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{ID: "amsterdam-stats", LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if found, err := worker.ProcessOne(ctx); err != nil || !found {
		t.Fatalf("stats found=%t err=%v", found, err)
	}
	var gross, burn, fee, priority string
	if err := db.QueryRow(ctx, `SELECT gas_used::text,burned_wei::text,execution_gas_fee_wei::text,priority_fee_wei::text FROM block_statistics WHERE block_hash=$1`, bundle.Block.Hash().Bytes()).Scan(&gross, &burn, &fee, &priority); err != nil {
		t.Fatal(err)
	}
	if gross != "25000" || burn != "42000" || fee != "63000" || priority != "21000" {
		t.Fatalf("gross=%s burn=%s fee=%s priority=%s", gross, burn, fee, priority)
	}
}
