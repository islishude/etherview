//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/testpgx"

	pgx "github.com/jackc/pgx/v5"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/ethereum/go-ethereum/common"
)

func TestCompatibilityQueriesExposeIndexablePlans(t *testing.T) {
	db := newMigratedPostgres(t)
	for _, index := range []string{"blocks_miner_number_idx", "blocks_timestamp_number_idx", "logs_topic0_idx"} {
		var exists bool
		if err := db.QueryRow(t.Context(), `
			SELECT EXISTS (
			    SELECT 1 FROM pg_indexes
			    WHERE schemaname = current_schema() AND indexname = $1
			)`, index).Scan(&exists); err != nil || !exists {
			t.Fatalf("index %s exists=%v error=%v", index, exists, err)
		}
	}

	requireQueryPlanIndex(t, db, testpgx.Statement("EtherscanMinedBlocksDesc"), "miner",
		"1", strings.ToLower(testAddress(1).Hex()), int64(0), int64(100),
	)
	requireQueryPlanIndex(t, db, testpgx.Statement("EtherscanBlockNumberByTimeBefore"), "timestamp",
		"1", "100",
	)
	topic := common.HexToHash("0x01").Bytes()
	requireQueryPlanIndex(t, db, testpgx.Statement("EtherscanLogsAsc"), "topic0",
		[]byte(`[{"index":0,"value":"0x0000000000000000000000000000000000000000000000000000000000000001","operator":"AND"}]`), nil, int64(0), int64(100), true, "1", "0", "100", topic,
	)
}

func requireQueryPlanIndex(
	t *testing.T,
	db *pgxpool.Pool,
	query string,
	indexFragment string,
	arguments ...any,
) {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	if _, err := tx.Exec(t.Context(), `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(t.Context(), "EXPLAIN (COSTS OFF) "+query, arguments...)
	if err != nil {
		t.Fatalf("explain query: %v", err)
	}
	defer rows.Close() //nolint:errcheck
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(lines, "\n")
	if !strings.Contains(strings.ToLower(plan), strings.ToLower(indexFragment)) {
		t.Fatalf("query plan does not use %q index:\n%s", indexFragment, plan)
	}
}
