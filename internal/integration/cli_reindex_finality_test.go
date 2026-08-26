//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCLIReindexRequiresAuditedFinalizedOverride(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("read integration schema: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "etherview.yaml")
	configBody := fmt.Sprintf("database:\n  url: %s\n", strconv.Quote(isolatedDatabaseURL(t, schema)))
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatalf("write reindex integration config: %v", err)
	}

	finalizedHash := testHash(20)
	if _, err := db.ExecContext(ctx, `INSERT INTO chains (chain_id) VALUES (1)`); err != nil {
		t.Fatalf("bind reindex chain: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO chain_finality (chain_id, finalized_number, finalized_hash)
		VALUES (1, 20, $1)`, finalizedHash.Bytes()); err != nil {
		t.Fatalf("record finalized height: %v", err)
	}

	const reason = "audited finalized proxy replay"
	runner := newCLIRunner()
	code, stdout, stderr := runner.run(ctx,
		"reindex", "--config", configPath,
		"--from", "10", "--to", "20", "--stage", "proxy",
		"--reason", reason,
	)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "pass --allow-finalized with an audit reason") {
		t.Fatalf("unapproved finalized reindex code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM repair_requests WHERE chain_id = 1`, 0)

	code, stdout, stderr = runner.run(ctx,
		"reindex", "--config", configPath,
		"--from", "10", "--to", "20", "--stage", "proxy",
		"--reason", reason, "--allow-finalized",
	)
	var accepted struct {
		Operation      string `json:"operation"`
		Stage          string `json:"stage"`
		FromBlock      uint64 `json:"from_block"`
		ToBlock        uint64 `json:"to_block"`
		AllowFinalized bool   `json:"allow_finalized"`
		Reason         string `json:"reason"`
		Status         string `json:"status"`
	}
	if err := json.Unmarshal([]byte(stdout), &accepted); code != 0 || err != nil || stderr != "" {
		t.Fatalf("approved finalized reindex code=%d stdout=%q stderr=%q error=%v", code, stdout, stderr, err)
	}
	if accepted.Operation != "reindex" || accepted.Stage != "proxy" ||
		accepted.FromBlock != 10 || accepted.ToBlock != 20 ||
		!accepted.AllowFinalized || accepted.Reason != reason || accepted.Status != "queued" {
		t.Fatalf("approved finalized reindex = %+v", accepted)
	}

	var persistedAllow bool
	var persistedReason string
	if err := db.QueryRowContext(ctx, `
		SELECT allow_finalized, reason
		FROM repair_requests
		WHERE chain_id = 1 AND operation = 'reindex' AND stage = 'proxy'`,
	).Scan(&persistedAllow, &persistedReason); err != nil {
		t.Fatalf("read persisted reindex request: %v", err)
	}
	if !persistedAllow || persistedReason != reason {
		t.Fatalf("persisted finalized audit allow=%t reason=%q", persistedAllow, persistedReason)
	}
}
