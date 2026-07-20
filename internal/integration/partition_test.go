//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

const (
	testPartitionLower = uint64(1_000_000)
	testPartitionUpper = uint64(2_000_000)
)

var partitionTableNames = map[string]string{
	"transaction_inclusions": "etherview_p_txi_1000000_2000000",
	"receipts":               "etherview_p_rcp_1000000_2000000",
	"logs":                   "etherview_p_log_1000000_2000000",
	"withdrawals":            "etherview_p_wdr_1000000_2000000",
	"token_events":           "etherview_p_tev_1000000_2000000",
	"token_balance_deltas":   "etherview_p_tbd_1000000_2000000",
	"normalized_traces":      "etherview_p_trc_1000000_2000000",
	"address_activities":     "etherview_p_act_1000000_2000000",
}

func TestPostgresPartitionLifecycleCrossesFixedBoundary(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	if err := repository.ConfigureIndex(ctx, "1", testPartitionLower-1); err != nil {
		t.Fatalf("configure boundary index: %v", err)
	}
	lower := testBundle(
		testPartitionLower-1, testHash(40_000), testHash(39_999), testHash(50_000), "partition-lower",
	)
	upper := testBundle(
		testPartitionLower, testHash(40_001), testHash(40_000), testHash(50_001), "partition-upper",
	)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{lower, upper}); err != nil {
		t.Fatalf("commit cross-boundary segment: %v", err)
	}
	if err := repository.EnsureBlockPartitions(ctx, testPartitionLower-1, testPartitionLower+1); err != nil {
		t.Fatalf("repeat boundary provisioning: %v", err)
	}

	for parent, child := range partitionTableNames {
		assertAttachedPartition(t, ctx, db, parent, child, testPartitionLower, testPartitionUpper)
		assertPartitionRangeCount(t, ctx, db, parent+"_default", testPartitionLower, testPartitionUpper, 0)
	}
	for _, table := range []string{
		"transaction_inclusions", "receipts", "logs", "withdrawals",
	} {
		assertPartitionRangeCount(t, ctx, db, partitionTableNames[table], testPartitionLower, testPartitionUpper, 1)
	}
}

func TestPostgresPartitionLifecycleIsConcurrentAndIdempotent(t *testing.T) {
	db := newMigratedPostgres(t)
	repositories := make([]*store.PostgresRepository, 2)
	for index := range repositories {
		repository, err := store.NewPostgresRepository(db)
		if err != nil {
			t.Fatal(err)
		}
		repositories[index] = repository
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	start := make(chan struct{})
	errorsByWorker := make(chan error, 8)
	var workers sync.WaitGroup
	for worker := 0; worker < cap(errorsByWorker); worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			<-start
			errorsByWorker <- repositories[worker%len(repositories)].EnsureBlockPartitions(
				ctx, testPartitionLower, testPartitionLower+1,
			)
		}(worker)
	}
	close(start)
	workers.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		if err != nil {
			t.Fatalf("concurrent partition provisioning: %v", err)
		}
	}
	for parent, child := range partitionTableNames {
		assertAttachedPartition(t, ctx, db, parent, child, testPartitionLower, testPartitionUpper)
	}
}

func TestPostgresPartitionLifecycleEvacuatesDefaultRowsAtomically(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	insertDefaultPartitionFixtures(t, ctx, db, testPartitionLower)

	for parent := range partitionTableNames {
		assertPartitionRangeCount(t, ctx, db, parent+"_default", testPartitionLower, testPartitionUpper, 1)
	}
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.EnsureBlockPartitions(ctx, testPartitionLower, testPartitionLower+1); err != nil {
		t.Fatalf("evacuate DEFAULT partition rows: %v", err)
	}

	for parent, child := range partitionTableNames {
		assertAttachedPartition(t, ctx, db, parent, child, testPartitionLower, testPartitionUpper)
		assertPartitionRangeCount(t, ctx, db, parent+"_default", testPartitionLower, testPartitionUpper, 0)
		assertPartitionRangeCount(t, ctx, db, child, testPartitionLower, testPartitionUpper, 1)
		assertPartitionRangeCount(t, ctx, db, parent, testPartitionLower, testPartitionUpper, 1)
	}
}

func TestPostgresPartitionLifecycleReportsRecoverablePartialState(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	const partialReceiptPartition = "operator_receipts_1000000_2000000"
	execFixture(t, ctx, db, fmt.Sprintf(
		"CREATE TABLE %s PARTITION OF receipts FOR VALUES FROM (%d) TO (%d)",
		quoteIdentifier(partialReceiptPartition), testPartitionLower, testPartitionUpper,
	))
	insertDefaultPartitionFixtures(t, ctx, db, testPartitionLower)

	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	err = repository.EnsureBlockPartitions(ctx, testPartitionLower, testPartitionLower+1)
	var recovery *store.PartitionRecoveryError
	if !errors.As(err, &recovery) {
		t.Fatalf("partial partition error = %v, want PartitionRecoveryError", err)
	}
	if recovery.Table != "transaction_inclusions" ||
		recovery.Lower != testPartitionLower || recovery.Upper != testPartitionUpper {
		t.Fatalf("partition recovery coordinates = %+v", recovery)
	}
	assertPartitionRangeCount(
		t, ctx, db, "transaction_inclusions_default", testPartitionLower, testPartitionUpper, 1,
	)
	assertPartitionRangeCount(
		t, ctx, db, partialReceiptPartition, testPartitionLower, testPartitionUpper, 1,
	)
}

func insertDefaultPartitionFixtures(t *testing.T, ctx context.Context, db *sql.DB, number uint64) {
	t.Helper()
	blockHash := mustBytes(t, testHash(60_000))
	parentHash := mustBytes(t, testHash(59_999))
	transactionHash := mustBytes(t, testHash(70_000))
	fromAddress := mustBytes(t, testAddress(61))
	toAddress := mustBytes(t, testAddress(62))
	tokenAddress := mustBytes(t, testAddress(63))
	topic := mustBytes(t, testHash(60_001))
	height := fmt.Sprint(number)

	execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)
	execFixture(t, ctx, db, `
		INSERT INTO blocks (chain_id, number, hash, parent_hash, timestamp, raw)
		VALUES (1, $1::numeric, $2, $3, 1, '{}'::jsonb)`, height, blockHash, parentHash)
	execFixture(t, ctx, db, `
		INSERT INTO transactions (chain_id, hash, tx_type, raw)
		VALUES (1, $1, 2, '{}'::jsonb)`, transactionHash)
	execFixture(t, ctx, db, `
		INSERT INTO transaction_inclusions (
			chain_id, block_number, block_hash, tx_index, tx_hash, raw
		) VALUES (1, $1::numeric, $2, 0, $3, '{}'::jsonb)`, height, blockHash, transactionHash)
	execFixture(t, ctx, db, `
		INSERT INTO receipts (
			chain_id, block_number, block_hash, tx_index, tx_hash, raw
		) VALUES (1, $1::numeric, $2, 0, $3, '{}'::jsonb)`, height, blockHash, transactionHash)
	execFixture(t, ctx, db, `
		INSERT INTO logs (
			chain_id, block_number, block_hash, log_index, tx_index,
			tx_hash, address, topic0, raw
		) VALUES (1, $1::numeric, $2, 0, 0, $3, $4, $5, '{}'::jsonb)`,
		height, blockHash, transactionHash, tokenAddress, topic)
	execFixture(t, ctx, db, `
		INSERT INTO withdrawals (
			chain_id, block_number, block_hash, withdrawal_index,
			validator_index, address, amount, raw
		) VALUES (1, $1::numeric, $2, 0, 1, $3, 1, '{}'::jsonb)`,
		height, blockHash, toAddress)
	execFixture(t, ctx, db, `
		INSERT INTO token_events (
			chain_id, block_number, block_hash, log_index, sub_index,
			transaction_hash, token_address, standard, event_kind,
			from_address, to_address, amount, canonical, confidence, raw
		) VALUES (1, $1::numeric, $2, 0, 0, $3, $4, 'erc20', 'transfer',
			$5, $6, 1, TRUE, 'high', '{}'::jsonb)`,
		height, blockHash, transactionHash, tokenAddress, fromAddress, toAddress)
	execFixture(t, ctx, db, `
		INSERT INTO token_balance_deltas (
			chain_id, block_number, block_hash, log_index, sub_index,
			token_address, owner_address, delta, canonical
		) VALUES (1, $1::numeric, $2, 0, 0, $3, $4, 1, TRUE)`,
		height, blockHash, tokenAddress, toAddress)
	execFixture(t, ctx, db, `
		INSERT INTO normalized_traces (
			chain_id, block_number, block_hash, transaction_hash, transaction_index,
			trace_path, depth, call_type, from_address, to_address, value,
			reverted, canonical
		) VALUES (1, $1::numeric, $2, $3, 0, '0', 0, 'CALL', $4, $5, 1, FALSE, TRUE)`,
		height, blockHash, transactionHash, fromAddress, toAddress)
	execFixture(t, ctx, db, `
		INSERT INTO address_activities (
			chain_id, block_number, block_hash, transaction_hash, activity_index,
			address, activity_kind, canonical, details
		) VALUES (1, $1::numeric, $2, $3, 0, $4, 'transaction', TRUE, '{}'::jsonb)`,
		height, blockHash, transactionHash, fromAddress)
}

func assertAttachedPartition(
	t *testing.T,
	ctx context.Context,
	db queryRowContext,
	parent, child string,
	lower, upper uint64,
) {
	t.Helper()
	var bound string
	err := db.QueryRowContext(ctx, `
		SELECT pg_get_expr(child.relpartbound, child.oid)
		FROM pg_inherits inheritance
		JOIN pg_class parent ON parent.oid = inheritance.inhparent
		JOIN pg_namespace parent_namespace ON parent_namespace.oid = parent.relnamespace
		JOIN pg_class child ON child.oid = inheritance.inhrelid
		WHERE parent_namespace.nspname = current_schema()
		  AND parent.relname = $1 AND child.relname = $2`, parent, child).Scan(&bound)
	if err != nil {
		t.Fatalf("query partition %s of %s: %v", child, parent, err)
	}
	normalized := strings.NewReplacer(" ", "", "'", "", "::numeric", "").Replace(bound)
	want := fmt.Sprintf("FORVALUESFROM(%d)TO(%d)", lower, upper)
	if strings.ToUpper(normalized) != want {
		t.Fatalf("partition %s bound = %q, want %q", child, bound, want)
	}
}

type queryRowContext interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func assertPartitionRangeCount(
	t *testing.T,
	ctx context.Context,
	db queryRowContext,
	table string,
	lower, upper uint64,
	want int,
) {
	t.Helper()
	query := fmt.Sprintf(
		"SELECT count(*) FROM %s WHERE block_number >= $1::numeric AND block_number < $2::numeric",
		quoteIdentifier(table),
	)
	var got int
	if err := db.QueryRowContext(ctx, query, fmt.Sprint(lower), fmt.Sprint(upper)).Scan(&got); err != nil {
		t.Fatalf("count partition range in %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("partition range count in %s = %d, want %d", table, got, want)
	}
}
