//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestProxyCodeEpochPlanUsesCodeOnlyPartialIndex(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	codeIndexes := inheritedIndexNames(
		t, ctx, db, "transaction_state_changes_code_epoch_idx",
	)
	if len(codeIndexes) == 0 {
		t.Fatal("code-epoch parent index has no attached partition indexes")
	}
	ordinaryIndexes := inheritedIndexNames(
		t, ctx, db, "transaction_state_changes_address_idx",
	)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `
		SET LOCAL enable_seqscan = off;
		SET LOCAL enable_bitmapscan = off`); err != nil {
		t.Fatal(err)
	}
	var plan []byte
	if err := tx.QueryRowContext(ctx, `
		EXPLAIN (FORMAT JSON, COSTS OFF)
		SELECT COALESCE(max(change.block_number), 0::numeric)
		FROM transaction_state_changes AS change
		JOIN canonical_blocks AS canonical
		  ON canonical.chain_id = change.chain_id
		 AND canonical.number = change.block_number
		 AND canonical.block_hash = change.block_hash
		WHERE change.chain_id = 1::numeric
		  AND change.address = $1
		  AND change.field_kind = 'code'
		  AND change.canonical = TRUE
		  AND change.block_number <= 999999::numeric
		  AND lower(change.before_value) IS DISTINCT FROM lower(change.after_value)`,
		testAddress(9_699).Bytes(),
	).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	text := string(plan)
	usedCodeIndex := false
	for _, name := range codeIndexes {
		usedCodeIndex = usedCodeIndex || strings.Contains(text, name)
	}
	if !usedCodeIndex {
		t.Fatalf("code epoch plan does not use a code-only partition index %v: %s", codeIndexes, text)
	}
	for _, name := range ordinaryIndexes {
		if strings.Contains(text, name) {
			t.Fatalf("code epoch plan fell back to broad address index %q: %s", name, text)
		}
	}
}

func inheritedIndexNames(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	parentName string,
) []string {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT child.relname
		FROM pg_inherits AS inheritance
		JOIN pg_class AS parent ON parent.oid = inheritance.inhparent
		JOIN pg_class AS child ON child.oid = inheritance.inhrelid
		WHERE parent.relname = $1
		ORDER BY child.relname`, parentName,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close() //nolint:errcheck
	result := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		result = append(result, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
