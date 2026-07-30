//go:build integration

package integration_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/billing"
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
					'kind', 'address',
					'language', 'solidity',
					'compiler_version', $1::text,
					'standard_json', '{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}'::jsonb,
					'standard_json_variants', jsonb_build_array('{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}'::jsonb),
					'bytecodes', jsonb_build_array(jsonb_build_object('runtime_bytecode', '0x00')),
					'target', jsonb_build_object(
						'chain_id', chain_id::bigint,
						'address', '0x' || encode(address, 'hex'),
						'code_hash', '0x' || encode(code_hash, 'hex'),
						'at_block_hash', '0x' || encode(block_hash, 'hex'),
						'runtime_bytecode', '0x00'
					)
				) AS request
			FROM fixture
		), encoded AS (
			SELECT requests.*, convert_to(request::text, 'UTF8') AS request_payload
			FROM requests
		)
		INSERT INTO verification_jobs (
			id, kind, chain_id, address, code_hash, block_hash, language,
			catalog_language, compiler_version,
			request, request_payload, request_digest, status, leased_by, lease_token,
			lease_expires_at, attempt_count
		)
		SELECT id, 'address', chain_id, address, code_hash, block_hash, 'solidity',
			'solidity', $1, request, request_payload,
			sha256(convert_to('etherview:verification-request:v2', 'UTF8') || decode('00', 'hex') || request_payload),
			status,
			CASE WHEN status = 'running' THEN 'worker' END,
			CASE WHEN status = 'running' THEN 'lease' END,
			CASE WHEN status = 'running' THEN now() + interval '1 hour' END,
			CASE WHEN status = 'running' THEN 1 ELSE 0 END
		FROM encoded`,
		"0.8.30+commit.73712a01",
	); err != nil {
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

func TestX402SettlingMetricIsChainScopedAgedAndClearedByReconciliation(
	t *testing.T,
) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO chains (chain_id) VALUES (1), (2)`,
	); err != nil {
		t.Fatalf("insert billing metric fixture chains: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO billing_payments (
			id, chain_id, fingerprint, reservation_owner, method, operation,
			resource_digest, requirement_digest, protocol_version, scheme,
			network, asset, amount_atomic, recipient, payer, facilitator_digest,
			state, failure_code, reservation_expires_at, handler_started_at,
			verified_at, settling_at, created_at, updated_at
		) VALUES
			(
				'00000000-0000-4000-8000-000000000101', 1,
				decode(repeat('a1', 32), 'hex'),
				'00000000-0000-4000-8000-000000000201', 'GET', 'listBlocks',
				decode(repeat('b1', 32), 'hex'), decode(repeat('c1', 32), 'hex'),
				2, 'exact', 'eip155:1', decode(repeat('11', 20), 'hex'), 1,
				decode(repeat('22', 20), 'hex'), decode(repeat('33', 20), 'hex'),
				decode(repeat('d1', 32), 'hex'),
				'settling', 'settlement_unknown', now() + interval '1 hour',
				now() - interval '20 seconds', now() - interval '20 seconds',
				now() - interval '10 seconds', now() - interval '10 minutes',
				now() - interval '10 seconds'
			),
			(
				'00000000-0000-4000-8000-000000000102', 1,
				decode(repeat('a2', 32), 'hex'),
				'00000000-0000-4000-8000-000000000202', 'GET', 'getTransaction',
				decode(repeat('b2', 32), 'hex'), decode(repeat('c2', 32), 'hex'),
				2, 'exact', 'eip155:1', decode(repeat('11', 20), 'hex'), 1,
				decode(repeat('22', 20), 'hex'), decode(repeat('33', 20), 'hex'),
				decode(repeat('d2', 32), 'hex'),
				'settling', NULL, now() + interval '1 hour',
				now() - interval '4 minutes', now() - interval '4 minutes',
				now() - interval '3 minutes', now() - interval '10 minutes',
				now() - interval '3 minutes'
			),
			(
				'00000000-0000-4000-8000-000000000103', 1,
				decode(repeat('a3', 32), 'hex'),
				'00000000-0000-4000-8000-000000000203', 'GET', 'search',
				decode(repeat('b3', 32), 'hex'), decode(repeat('c3', 32), 'hex'),
				2, 'exact', 'eip155:1', decode(repeat('11', 20), 'hex'), 1,
				decode(repeat('22', 20), 'hex'), decode(repeat('33', 20), 'hex'),
				decode(repeat('d3', 32), 'hex'),
				'settling', NULL, now() + interval '1 hour',
				now() - interval '40 seconds', now() - interval '40 seconds',
				now() - interval '30 seconds', now() - interval '10 minutes',
				now() - interval '30 seconds'
			),
			(
				'00000000-0000-4000-8000-000000000104', 2,
				decode(repeat('a4', 32), 'hex'),
				'00000000-0000-4000-8000-000000000204', 'GET', 'listTransactions',
				decode(repeat('b4', 32), 'hex'), decode(repeat('c4', 32), 'hex'),
				2, 'exact', 'eip155:2', decode(repeat('11', 20), 'hex'), 1,
				decode(repeat('22', 20), 'hex'), decode(repeat('33', 20), 'hex'),
				decode(repeat('d4', 32), 'hex'),
				'settling', 'settlement_unknown', now() + interval '1 hour',
				now() - interval '20 seconds', now() - interval '20 seconds',
				now() - interval '10 seconds', now() - interval '10 minutes',
				now() - interval '10 seconds'
			)`); err != nil {
		t.Fatalf("insert billing metric fixtures: %v", err)
	}

	var (
		indexValid     bool
		indexPredicate string
	)
	if err := db.QueryRowContext(ctx, `
		SELECT index_state.indisvalid,
		       pg_get_expr(index_state.indpred, index_state.indrelid)
		FROM pg_index AS index_state
		JOIN pg_class AS index_relation
		  ON index_relation.oid = index_state.indexrelid
		WHERE index_relation.relnamespace = current_schema()::regnamespace
		  AND index_relation.relname = 'billing_payments_settling_metrics_idx'`).
		Scan(&indexValid, &indexPredicate); err != nil {
		t.Fatalf("read billing settling metric index: %v", err)
	}
	if !indexValid || !strings.Contains(indexPredicate, "settling") {
		t.Fatalf(
			"billing settling index valid=%t predicate=%q",
			indexValid,
			indexPredicate,
		)
	}

	source, err := observability.NewPostgresMetricSource(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	registry := observability.NewRegistry("integration", "api")
	collector, err := observability.NewDurableCollector(
		source,
		registry,
		observability.DurableCollectorOptions{
			Interval: 25 * time.Millisecond,
			Timeout:  20 * time.Millisecond,
		},
	)
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
			t.Error("billing metric collector did not stop")
		}
	})

	initial := waitForMetrics(t, registry, func(metrics string) bool {
		return strings.Contains(
			metrics,
			`etherview_x402_stale_settling_payments{operation="listBlocks",reason="settlement_unknown"} 1`,
		) && strings.Contains(
			metrics,
			`etherview_x402_stale_settling_payments{operation="getTransaction",reason="unmarked_after_timeout"} 1`,
		)
	})
	if strings.Contains(initial, `operation="search"`) ||
		strings.Contains(initial, `operation="listTransactions"`) {
		t.Fatalf("fresh or other-chain settlement leaked into metrics:\n%s", initial)
	}

	ledger, err := billing.NewPostgresLedger(db, 1, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	operationAt := time.Now().UTC().Add(time.Second)
	for _, id := range []string{
		"00000000-0000-4000-8000-000000000101",
		"00000000-0000-4000-8000-000000000102",
	} {
		if _, err := ledger.ReconcileFailed(ctx, id, operationAt); err != nil {
			t.Fatalf("reconcile billing metric fixture %s: %v", id, err)
		}
	}
	afterReconcile := waitForMetrics(t, registry, func(metrics string) bool {
		return !strings.Contains(
			metrics,
			"etherview_x402_stale_settling_payments{",
		)
	})
	if strings.Contains(
		afterReconcile,
		"etherview_x402_stale_settling_payments{",
	) {
		t.Fatalf(
			"reconciled settlements remained in metrics:\n%s",
			afterReconcile,
		)
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
