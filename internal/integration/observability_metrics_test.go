//go:build integration

package integration_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/observability"
	"github.com/islishude/etherview/internal/store"
)

const activeRepairMetricsIndex = "repair_requests_active_metrics_idx"

func TestObservabilityActiveRepairIndexUpgradesWithoutChangingRows(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `INSERT INTO chains (chain_id) VALUES (1)`); err != nil {
		t.Fatalf("insert upgrade fixture chain: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO repair_requests (
			chain_id, operation, stage, from_block, to_block, reason, status,
			requested_at, started_at, completed_at
		) VALUES
			(1, 'repair', 'core', 1, 1, 'queued', 'queued', now(), NULL, NULL),
			(1, 'reindex', 'trace', 2, 2, 'running', 'running', now(), now(), NULL),
			(1, 'repair', 'core', 3, 3, 'done', 'done', now(), now(), now())`); err != nil {
		t.Fatalf("insert pre-upgrade repair rows: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP INDEX `+activeRepairMetricsIndex); err != nil {
		t.Fatalf("restore pre-0020 index state: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM etherview_schema_migrations WHERE version = '0020_observability_active_repair_index'`); err != nil {
		t.Fatalf("restore pre-0020 migration ledger: %v", err)
	}
	before, err := store.ReadSchemaStatus(ctx, db)
	if err != nil {
		t.Fatalf("read pre-upgrade status: %v", err)
	}
	if len(before.Pending) != 1 || before.Pending[0] != "0020_observability_active_repair_index" {
		t.Fatalf("pre-upgrade pending migrations=%v", before.Pending)
	}
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatalf("upgrade through observability index migration: %v", err)
	}
	var (
		valid     bool
		predicate string
	)
	if err := db.QueryRowContext(ctx, `
		SELECT index_state.indisvalid,
		       pg_get_expr(index_state.indpred, index_state.indrelid)
		FROM pg_index AS index_state
		JOIN pg_class AS index_relation ON index_relation.oid = index_state.indexrelid
		WHERE index_relation.relnamespace = current_schema()::regnamespace
		  AND index_relation.relname = $1`, activeRepairMetricsIndex).Scan(&valid, &predicate); err != nil {
		t.Fatalf("read active repair metric index: %v", err)
	}
	if !valid || !strings.Contains(predicate, "status") || !strings.Contains(predicate, "queued") || !strings.Contains(predicate, "running") {
		t.Fatalf("active repair metric index valid=%t predicate=%q", valid, predicate)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM repair_requests WHERE chain_id = 1`).Scan(&count); err != nil {
		t.Fatalf("count post-upgrade repair rows: %v", err)
	}
	if count != 3 {
		t.Fatalf("post-upgrade repair row count=%d want=3", count)
	}
}

func TestPostgresMetricSnapshotIsChainScopedAndRetainedAfterRefreshFailure(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `INSERT INTO chains (chain_id) VALUES (1), (2)`); err != nil {
		t.Fatalf("insert metric fixture chains: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO durable_jobs (
			chain_id, kind, stage, stage_version, idempotency_key, status,
			leased_by, lease_token, lease_expires_at, leased_generation
		) VALUES
			(1, 'enrichment', 'trace', 1, 'chain-1-queued', 'queued', NULL, NULL, NULL, NULL),
			(1, 'enrichment', 'trace', 1, 'chain-1-leased', 'leased', 'worker', 'lease', now() + interval '1 hour', 1),
			(1, 'enrichment', 'trace', 99, 'chain-1-unsupported-version', 'queued', NULL, NULL, NULL, NULL),
			(1, 'enrichment', 'trace', 1, 'chain-1-terminal', 'cancelled', NULL, NULL, NULL, NULL),
			(2, 'enrichment', 'trace', 1, 'chain-2-queued-a', 'queued', NULL, NULL, NULL, NULL),
			(2, 'enrichment', 'trace', 1, 'chain-2-queued-b', 'queued', NULL, NULL, NULL, NULL)`); err != nil {
		t.Fatalf("insert durable metric fixtures: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		WITH fixture(id, chain_id, address, code_hash, block_hash, status) AS (
			VALUES
			('00000000-0000-4000-8000-000000000001'::uuid, 1::numeric,
			 decode(repeat('11', 20), 'hex'), decode(repeat('21', 32), 'hex'), decode(repeat('31', 32), 'hex'), 'queued'::text),
			('00000000-0000-4000-8000-000000000002'::uuid, 1::numeric,
			 decode(repeat('12', 20), 'hex'), decode(repeat('22', 32), 'hex'), decode(repeat('32', 32), 'hex'), 'running'::text),
			('00000000-0000-4000-8000-000000000003'::uuid, 2::numeric,
			 decode(repeat('13', 20), 'hex'), decode(repeat('23', 32), 'hex'), decode(repeat('33', 32), 'hex'), 'queued'::text),
			('00000000-0000-4000-8000-000000000004'::uuid, 1::numeric,
			 decode(repeat('14', 20), 'hex'), decode(repeat('24', 32), 'hex'), decode(repeat('34', 32), 'hex'), 'cancelled'::text)
		), requests AS (
			SELECT fixture.*,
				jsonb_build_object(
					'chain_id', chain_id::text,
					'address', '0x' || encode(address, 'hex'),
					'code_hash', '0x' || encode(code_hash, 'hex'),
					'at_block_hash', '0x' || encode(block_hash, 'hex'),
					'language', 'solidity',
					'compiler_version', '0.8.30'
				) AS request
			FROM fixture
		), encoded AS (
			SELECT requests.*, convert_to(request::text, 'UTF8') AS request_payload
			FROM requests
		)
		INSERT INTO verification_jobs (
			id, chain_id, address, code_hash, block_hash, language, compiler_version,
			request, request_payload, request_digest, status, leased_by, lease_token,
			lease_expires_at, attempt_count
		)
		SELECT id, chain_id, address, code_hash, block_hash, 'solidity', '0.8.30',
			request, request_payload,
			sha256(convert_to('etherview:verification-request:v1', 'UTF8') || decode('00', 'hex') || request_payload),
			status,
			CASE WHEN status = 'running' THEN 'worker' END,
			CASE WHEN status = 'running' THEN 'lease' END,
			CASE WHEN status = 'running' THEN now() + interval '1 hour' END,
			CASE WHEN status = 'running' THEN 1 ELSE 0 END
		FROM encoded`); err != nil {
		t.Fatalf("insert verification metric fixtures: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO repair_requests (
			chain_id, operation, stage, from_block, to_block, reason, status,
			requested_at, started_at, completed_at, last_error
		) VALUES
			(1, 'repair', 'core', 1, 1, 'queued', 'queued', now() - interval '120 seconds', NULL, NULL, NULL),
			(1, 'reindex', 'trace', 2, 2, 'running', 'running', now() - interval '30 seconds', now(), NULL, NULL),
			(1, 'repair', 'core', 3, 3, 'done', 'done', now(), now(), now(), NULL),
			(1, 'repair', 'core', 4, 4, 'failed', 'failed', now(), now(), now(), 'nested upstream text'),
			(1, 'reindex', 'trace', 5, 5, 'cancelled', 'cancelled', now(), NULL, now(), NULL),
			(2, 'repair', 'core', 1, 1, 'other chain', 'queued', now() - interval '3600 seconds', NULL, NULL, NULL)`); err != nil {
		t.Fatalf("insert repair metric fixtures: %v", err)
	}

	source, err := observability.NewPostgresMetricSource(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	registry := observability.NewRegistry("integration", "maintenance")
	collector, err := observability.NewDurableCollector(source, registry, observability.DurableCollectorOptions{
		Interval: 25 * time.Millisecond, Timeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- collector.Run(runCtx) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("durable metric collector did not stop")
		}
	})

	initial := waitForMetrics(t, registry, func(metrics string) bool {
		return strings.Contains(metrics, `etherview_durable_jobs{stage="trace",status="queued"} 1`) &&
			strings.Contains(metrics, `etherview_durable_jobs{stage="trace",status="leased"} 1`) &&
			strings.Contains(metrics, `etherview_durable_jobs{stage="other",status="queued"} 1`) &&
			strings.Contains(metrics, `etherview_verification_jobs{status="queued"} 1`) &&
			strings.Contains(metrics, `etherview_verification_jobs{status="running"} 1`) &&
			strings.Contains(metrics, `etherview_repair_requests{operation="repair",status="queued"} 1`) &&
			strings.Contains(metrics, `etherview_repair_requests{operation="reindex",status="running"} 1`)
	})
	oldest := scalarMetric(t, initial, "etherview_repair_oldest_queued_seconds")
	if oldest < 100 || oldest > 300 {
		t.Fatalf("oldest chain-1 repair age=%v; chain scope or fixture age is wrong\n%s", oldest, initial)
	}
	if strings.Contains(initial, `status="queued"} 2`) || strings.Contains(initial, "3600") {
		t.Fatalf("chain-2 control-plane state leaked into chain-1 metrics:\n%s", initial)
	}
	for _, terminal := range []string{
		`etherview_durable_jobs{stage="trace",status="cancelled"}`,
		`etherview_verification_jobs{status="cancelled"}`,
		`etherview_repair_requests{operation="repair",status="done"}`,
		`etherview_repair_requests{operation="repair",status="failed"}`,
		`etherview_repair_requests{operation="reindex",status="cancelled"}`,
	} {
		if strings.Contains(initial, terminal) {
			t.Fatalf("terminal history leaked into active backlog gauge %q:\n%s", terminal, initial)
		}
	}

	if _, err := db.ExecContext(ctx, `ALTER TABLE durable_jobs RENAME TO durable_jobs_refresh_unavailable`); err != nil {
		t.Fatalf("make metric source unavailable: %v", err)
	}
	afterFailure := waitForMetrics(t, registry, func(metrics string) bool {
		return scalarMetricValue(metrics, "etherview_observability_refresh_failures_total") >= 1
	})
	lastSuccess := scalarMetric(t, afterFailure, "etherview_observability_last_refresh_timestamp_seconds")
	afterSecondFailure := waitForMetrics(t, registry, func(metrics string) bool {
		return scalarMetricValue(metrics, "etherview_observability_refresh_failures_total") >= 2
	})
	if got := scalarMetric(t, afterSecondFailure, "etherview_observability_last_refresh_timestamp_seconds"); got != lastSuccess {
		t.Fatalf("failed refresh advanced last-success timestamp: got=%v want=%v", got, lastSuccess)
	}
	if !strings.Contains(afterSecondFailure, `etherview_durable_jobs{stage="trace",status="queued"} 1`) ||
		!strings.Contains(afterSecondFailure, `etherview_verification_jobs{status="running"} 1`) {
		t.Fatalf("failed refresh cleared the last PostgreSQL snapshot:\n%s", afterSecondFailure)
	}
}

func waitForMetrics(t *testing.T, registry *observability.Registry, ready func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		metrics := registry.Gather()
		if ready(metrics) {
			return metrics
		}
		time.Sleep(10 * time.Millisecond)
	}
	metrics := registry.Gather()
	t.Fatalf("timed out waiting for metric snapshot:\n%s", metrics)
	return ""
}

func scalarMetric(t *testing.T, metrics, name string) float64 {
	t.Helper()
	value := scalarMetricValue(metrics, name)
	if value < 0 {
		t.Fatalf("metric %s is missing:\n%s", name, metrics)
	}
	return value
}

func scalarMetricValue(metrics, name string) float64 {
	prefix := name + " "
	for _, line := range strings.Split(metrics, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimPrefix(line, prefix), 64)
		if err != nil {
			return -1
		}
		return value
	}
	return -1
}
