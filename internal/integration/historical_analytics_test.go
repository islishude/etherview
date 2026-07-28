//go:build integration

package integration_test

import (
	"context"
	"math/big"
	"sort"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/analytics"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/store"
)

func TestHistoricalAnalyticsRecomputesNewestFirstAndCorrectsReorgs(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
	genesis := analyticsBundle(t, 0, common.Hash{}, uint64(base.Unix()), 1, 1, "analytics-genesis")
	oldBlock := analyticsBundle(
		t, 1, genesis.Block.Hash(), uint64(base.Add(time.Hour).Unix()), 1, 2, "analytics-old",
	)
	replacement := analyticsBundle(
		t, 1, genesis.Block.Hash(), uint64(base.Add(time.Hour).Unix()), 2, 3, "analytics-new",
	)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{genesis, oldBlock}); err != nil {
		t.Fatalf("commit analytics branch: %v", err)
	}
	published := newDerivedPublicationHarness(t, db, newDerivedProcessors(t, db))
	published.process(t, ctx, genesis)
	published.process(t, ctx, oldBlock)

	now := base.Add(24 * time.Hour)
	worker, err := analytics.NewRollupWorker(db, analytics.RollupWorkerOptions{
		ChainID: 1, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	newest, err := worker.RunOnce(ctx)
	if err != nil || !newest.Published || !newest.BucketStart.Equal(base.Add(time.Hour)) {
		t.Fatalf("newest rollup=%+v error=%v", newest, err)
	}
	older, err := worker.RunOnce(ctx)
	if err != nil || !older.Published || !older.BucketStart.Equal(base) {
		t.Fatalf("older rollup=%+v error=%v", older, err)
	}
	reader, err := analytics.NewReader(db)
	if err != nil {
		t.Fatal(err)
	}
	before := readAnalyticsTransactions(t, ctx, reader, base, base.Add(2*time.Hour), now)
	if len(before.Points) != 2 || before.Points[0].Value != "1" || before.Points[1].Value != "1" {
		t.Fatalf("old branch series=%+v", before.Points)
	}
	if !before.Coverage.Complete || before.Coverage.Progress != "100" {
		t.Fatalf("complete coverage=%+v", before.Coverage)
	}

	applyDerivedReorg(
		t, ctx, repository, genesis,
		[]chainbundle.Bundle{oldBlock}, []chainbundle.Bundle{replacement},
		"historical analytics reorg",
	)
	published.process(t, ctx, replacement)
	recomputed, err := worker.RunOnce(ctx)
	if err != nil || !recomputed.Published || !recomputed.BucketStart.Equal(base.Add(time.Hour)) {
		t.Fatalf("reorg rollup=%+v error=%v", recomputed, err)
	}
	after := readAnalyticsTransactions(t, ctx, reader, base, base.Add(2*time.Hour), now)
	if len(after.Points) != 2 || after.Points[1].Value != "2" {
		t.Fatalf("replacement branch series=%+v", after.Points)
	}
	idle, err := worker.RunOnce(ctx)
	if err != nil || idle.Published || !idle.BucketStart.IsZero() {
		t.Fatalf("idempotent idle rollup=%+v error=%v", idle, err)
	}
}

func TestHistoricalAnalyticsTenYearHourlyRollupQueryStaysBounded(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(95_000), common.Hash{}, testHash(95_001), "analytics-performance")
	commitCanonical(t, ctx, repository, genesis)
	from := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(10, 0, 0)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO chart_hourly_rollups (
		    chain_id, bucket_start, source_generation, from_block, to_block,
		    block_count, transaction_count, failed_transaction_count,
		    contract_creation_count, gas_used, gas_limit,
		    block_interval_seconds, block_interval_samples,
		    base_fee_per_gas_sum, base_fee_samples, execution_gas_fee_wei,
		    priority_fee_wei, burned_wei, blob_gas_used,
		    blob_base_fee_per_gas_sum, blob_base_fee_samples, blob_burned_wei,
		    erc20_transfer_count, nft_transfer_count
		)
		SELECT 1, bucket, 1, 0, 0, 1, 1, 0, 0, 21000, 30000000,
		       12, 1, 1, 1, 21000, 0, 21000, 0, 0, 0, 0, 0, 0
		FROM generate_series(
		    $1::timestamptz,
		    $2::timestamptz - interval '1 hour',
		    interval '1 hour'
		) AS bucket`, from, to); err != nil {
		t.Fatalf("seed ten-year hourly rollups: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO chart_rollup_backfill (
		    chain_id, available_from, available_to, completed_blocks,
		    total_blocks, complete
		) VALUES (1, $1, $2::timestamptz - interval '1 hour', 1, 1, true)
		ON CONFLICT (chain_id) DO UPDATE SET
		    available_from = EXCLUDED.available_from,
		    available_to = EXCLUDED.available_to,
		    completed_blocks = 1, total_blocks = 1, complete = true`,
		from, to,
	); err != nil {
		t.Fatalf("seed analytics coverage: %v", err)
	}
	reader, err := analytics.NewReader(db)
	if err != nil {
		t.Fatal(err)
	}
	durations := make([]time.Duration, 5)
	for index := range durations {
		started := time.Now()
		series, readErr := reader.Detail(ctx, analytics.DetailRequest{
			ChainID: "1", Metric: analytics.MetricExecutionFees,
			From: from, To: to, Interval: analytics.IntervalAuto,
			Now: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
		})
		durations[index] = time.Since(started)
		if readErr != nil {
			t.Fatalf("read ten-year analytics: %v", readErr)
		}
		if len(series.Points) == 0 || len(series.Points) > 500 ||
			series.Interval != analytics.IntervalMonth {
			t.Fatalf("ten-year series interval=%s points=%d", series.Interval, len(series.Points))
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[len(durations)-1]
	t.Logf("ten-year hourly rollup query p95=%s samples=%v", p95, durations)
	if p95 >= 500*time.Millisecond {
		t.Fatalf("ten-year hourly rollup query p95=%s, want <500ms; samples=%v", p95, durations)
	}
}

func readAnalyticsTransactions(
	t *testing.T,
	ctx context.Context,
	reader *analytics.Reader,
	from time.Time,
	to time.Time,
	now time.Time,
) analytics.Series {
	t.Helper()
	series, err := reader.Detail(ctx, analytics.DetailRequest{
		ChainID: "1", Metric: analytics.MetricTransactions,
		From: from, To: to, Interval: analytics.IntervalHour, Now: now,
	})
	if err != nil {
		t.Fatalf("read transaction analytics: %v", err)
	}
	return series
}

func analyticsBundle(
	t *testing.T,
	number uint64,
	parent common.Hash,
	timestamp uint64,
	transactionCount int,
	gasPrice int64,
	variant string,
) chainbundle.Bundle {
	t.Helper()
	to := testAddress(90)
	transactions := make([]integrationTransactionOptions, transactionCount)
	for index := range transactions {
		transactions[index] = integrationTransactionOptions{
			Type: types.DynamicFeeTxType, To: &to,
			Data:     []byte{byte(number), byte(index)},
			GasPrice: big.NewInt(gasPrice),
		}
	}
	bundle, err := newIntegrationBundle(integrationBundleOptions{
		Number: number, ParentHash: parent, Timestamp: timestamp,
		ExtraData: []byte(variant), Transactions: transactions,
	})
	if err != nil {
		t.Fatalf("build analytics bundle: %v", err)
	}
	return bundle
}
