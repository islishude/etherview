//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/app"
	"github.com/islishude/etherview/internal/cli"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/indexer"
	"github.com/islishude/etherview/internal/maintenance"
	"github.com/islishude/etherview/internal/store"
)

func TestCLIBackendPersistsMigrationsMaintenanceAndAdminState(t *testing.T) {
	db := newIsolatedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("read integration schema: %v", err)
	}
	databaseURL := isolatedDatabaseURL(t, schema)
	configPath := filepath.Join(t.TempDir(), "etherview.yaml")
	configBody := fmt.Sprintf(
		"database:\n  url: %s\nsecurity:\n  api_key_pepper: %s\n",
		strconv.Quote(databaseURL), strconv.Quote(strings.Repeat("p", 32)),
	)
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatalf("write CLI integration config: %v", err)
	}

	runner := newCLIRunner()
	code, stdout, stderr := runner.run(ctx, "migrate", "status", "--config", configPath)
	if code != 1 || !strings.Contains(stdout, `"status": "incompatible"`) || !strings.Contains(stderr, "pending migrations") {
		t.Fatalf("pre-migration status code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runner.run(ctx, "migrate", "up", "--config="+configPath)
	if code != 0 || !strings.Contains(stdout, `"status": "compatible"`) || stderr != "" {
		t.Fatalf("migrate up code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runner.run(ctx, "migrate", "status", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, `"status": "compatible"`) || stderr != "" {
		t.Fatalf("post-migration status code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO chains (chain_id) VALUES (1)`); err != nil {
		t.Fatalf("bind integration chain: %v", err)
	}
	code, _, stderr = runner.run(ctx,
		"repair", "--from", "10", "--to", "20", "--reason", "restore canonical gap", "--config", configPath,
	)
	if code != 0 || stderr != "" {
		t.Fatalf("repair code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = runner.run(ctx,
		"reindex", "--config="+configPath, "--from", "10", "--to", "20", "--stage", "token", "--reason", "rebuild token state",
	)
	if code != 0 || stderr != "" {
		t.Fatalf("reindex code=%d stderr=%q", code, stderr)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT operation, stage, from_block::text, to_block::text, reason, status
		FROM repair_requests
		ORDER BY id`)
	if err != nil {
		t.Fatalf("query repair audit records: %v", err)
	}
	defer rows.Close()
	wantRepairs := [][]string{
		{"repair", "core", "10", "20", "restore canonical gap", "queued"},
		{"reindex", "token", "10", "20", "rebuild token state", "queued"},
	}
	for index, want := range wantRepairs {
		if !rows.Next() {
			t.Fatalf("repair audit row %d is missing", index)
		}
		got := make([]string, len(want))
		if err := rows.Scan(&got[0], &got[1], &got[2], &got[3], &got[4], &got[5]); err != nil {
			t.Fatalf("scan repair audit row %d: %v", index, err)
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("repair audit row %d = %v, want %v", index, got, want)
		}
	}
	if rows.Next() {
		t.Fatal("unexpected extra repair audit row")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate repair audit records: %v", err)
	}

	address := "0x0000000000000000000000000000000000000001"
	code, _, stderr = runner.run(ctx, "admin", "label", "set", "address", address, "treasury", "--config", configPath)
	if code != 0 || stderr != "" {
		t.Fatalf("label set code=%d stderr=%q", code, stderr)
	}
	code, stdout, stderr = runner.run(ctx, "admin", "label", "list", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, `"label": "treasury"`) || stderr != "" {
		t.Fatalf("label list code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = runner.run(ctx, "admin", "label", "delete", "address", address, "--config", configPath)
	if code != 0 || stderr != "" {
		t.Fatalf("label delete code=%d stderr=%q", code, stderr)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM operator_labels WHERE chain_id = 1`, 0)

	code, stdout, stderr = runner.run(ctx,
		"admin", "api-key", "create", "--name", "integration-reader", "--rate", "10", "--burst", "20", "--config", configPath,
	)
	if code != 0 || stderr != "" {
		t.Fatalf("API key create code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var issued struct {
		Token  string `json:"token"`
		Prefix string `json:"prefix"`
	}
	if err := json.Unmarshal([]byte(stdout), &issued); err != nil {
		t.Fatalf("decode API key create output: %v", err)
	}
	if !strings.HasPrefix(issued.Token, "evk_") || len(issued.Prefix) != 10 {
		t.Fatalf("issued API key output = %+v", issued)
	}
	var persistedDigest []byte
	if err := db.QueryRowContext(ctx, `SELECT digest FROM api_keys WHERE prefix = $1`, issued.Prefix).Scan(&persistedDigest); err != nil {
		t.Fatalf("read persisted API key: %v", err)
	}
	if len(persistedDigest) != 32 || bytes.Contains(persistedDigest, []byte(issued.Token)) {
		t.Fatalf("persisted API key is not a 32-byte digest")
	}
	code, stdout, stderr = runner.run(ctx, "admin", "api-key", "list", "--config", configPath)
	if code != 0 || strings.Contains(stdout, issued.Token) || !strings.Contains(stdout, issued.Prefix) || stderr != "" {
		t.Fatalf("API key list code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runner.run(ctx, "admin", "api-key", "rotate", issued.Prefix, "--config", configPath)
	if code != 0 || stderr != "" {
		t.Fatalf("API key rotate code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var replacement struct {
		Token       string `json:"token"`
		Prefix      string `json:"prefix"`
		RotatedFrom string `json:"rotated_from"`
		Rate        int    `json:"rate"`
		Burst       int    `json:"burst"`
	}
	if err := json.Unmarshal([]byte(stdout), &replacement); err != nil {
		t.Fatalf("decode API key rotation output: %v", err)
	}
	if replacement.RotatedFrom != issued.Prefix || replacement.Prefix == issued.Prefix ||
		!strings.HasPrefix(replacement.Token, "evk_") || replacement.Rate != 10 || replacement.Burst != 20 {
		t.Fatalf("rotated API key output = %+v", replacement)
	}
	var oldRevoked, replacementActive bool
	var replacementDigest []byte
	if err := db.QueryRowContext(ctx, `
		SELECT
			(SELECT revoked_at IS NOT NULL FROM api_keys WHERE prefix = $1),
			(SELECT revoked_at IS NULL FROM api_keys WHERE prefix = $2),
			(SELECT digest FROM api_keys WHERE prefix = $2)`,
		issued.Prefix, replacement.Prefix,
	).Scan(&oldRevoked, &replacementActive, &replacementDigest); err != nil {
		t.Fatalf("read rotated API key pair: %v", err)
	}
	if !oldRevoked || !replacementActive || len(replacementDigest) != 32 || bytes.Contains(replacementDigest, []byte(replacement.Token)) {
		t.Fatalf("rotation state old_revoked=%v replacement_active=%v digest_length=%d", oldRevoked, replacementActive, len(replacementDigest))
	}
	code, _, stderr = runner.run(ctx, "admin", "api-key", "rotate", issued.Prefix, "--config", configPath)
	if code != 1 || !strings.Contains(stderr, "revoked") {
		t.Fatalf("re-rotate revoked key code=%d stderr=%q", code, stderr)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM api_keys WHERE revoked_at IS NULL`, 1)
	code, _, stderr = runner.run(ctx, "admin", "api-key", "revoke", replacement.Prefix, "--config", configPath)
	if code != 0 || stderr != "" {
		t.Fatalf("API key revoke code=%d stderr=%q", code, stderr)
	}
	var revoked bool
	if err := db.QueryRowContext(ctx, `SELECT revoked_at IS NOT NULL FROM api_keys WHERE prefix = $1`, replacement.Prefix).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("API key revoked=%v error=%v", revoked, err)
	}
}

func TestCLIMaintenanceWorkerExecutesRepairAndReindex(t *testing.T) {
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
		t.Fatalf("write maintenance integration config: %v", err)
	}

	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatalf("create core repository: %v", err)
	}
	blockHash := testHash(80_000)
	parentHash := testHash(0)
	transactionHash := testHash(80_100)
	original := testBundle(0, blockHash, parentHash, transactionHash, "repair-original")
	commitCanonical(t, ctx, repository, original)
	canonicalBefore, found, err := repository.CanonicalBlock(ctx, "1", 0)
	if err != nil || !found {
		t.Fatalf("read canonical block before repair: found=%t error=%v", found, err)
	}
	checkpointBefore, found, err := repository.Checkpoint(ctx, "1", store.CoreCheckpoint)
	if err != nil || !found {
		t.Fatalf("read checkpoint before repair: found=%t error=%v", found, err)
	}

	runner := newCLIRunner()
	if code, _, stderr := runner.run(ctx,
		"repair", "--from", "0", "--to", "0", "--reason", "replace malformed receipt", "--config", configPath,
	); code != 0 || stderr != "" {
		t.Fatalf("enqueue repair code=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := runner.run(ctx,
		"reindex", "--from", "0", "--to", "0", "--stage", "token", "--reason", "rebuild repaired token facts", "--config", configPath,
	); code != 0 || stderr != "" {
		t.Fatalf("enqueue reindex code=%d stderr=%q", code, stderr)
	}

	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatalf("create enrichment queue: %v", err)
	}
	blockHashBytes, err := blockHash.Bytes()
	if err != nil {
		t.Fatalf("decode block hash: %v", err)
	}
	word, err := enrich.WordFromBytes(blockHashBytes)
	if err != nil {
		t.Fatalf("convert block hash: %v", err)
	}
	queued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.TokenStage, ChainID: "1", BlockHash: word, BlockNumber: 0,
	})
	if err != nil || !queued.Created {
		t.Fatalf("enqueue active token job: created=%t error=%v", queued.Created, err)
	}
	activeLease, found, err := queue.Claim(ctx, "active-enricher", []enrich.StageID{enrich.TokenStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim active token job: found=%t error=%v", found, err)
	}

	refreshed := testBundle(0, blockHash, parentHash, transactionHash, "repair-refreshed")
	source := &maintenanceBundleSource{bundle: refreshed}
	canonicalizer := &indexer.Canonicalizer{
		ChainID: "1", StartBlock: 0, MaxReorgDepth: 128, Repository: repository,
	}
	requestRepository, err := maintenance.NewPostgresRepository(db)
	if err != nil {
		t.Fatalf("create maintenance repository: %v", err)
	}
	executor, err := maintenance.NewExecutor("1", source, canonicalizer, repository, queue)
	if err != nil {
		t.Fatalf("create maintenance executor: %v", err)
	}
	worker, err := maintenance.NewWorker(requestRepository, executor, maintenance.WorkerOptions{WorkerID: "integration-maintenance"})
	if err != nil {
		t.Fatalf("create maintenance worker: %v", err)
	}
	for index := range 2 {
		processed, err := worker.ProcessOne(ctx)
		if err != nil || !processed {
			t.Fatalf("process maintenance request %d: processed=%t error=%v", index, processed, err)
		}
	}

	if len(source.purposes) != 1 || source.purposes[0] != ethrpc.PurposeHistory ||
		len(source.numbers) != 1 || source.numbers[0] != 0 {
		t.Fatalf("repair source purposes=%v numbers=%v", source.purposes, source.numbers)
	}
	assertBlockVariant(t, ctx, db, blockHash, "repair-refreshed")
	canonicalAfter, found, err := repository.CanonicalBlock(ctx, "1", 0)
	if err != nil || !found || canonicalAfter != canonicalBefore {
		t.Fatalf("canonical identity moved: before=%+v after=%+v found=%t error=%v", canonicalBefore, canonicalAfter, found, err)
	}
	checkpointAfter, found, err := repository.Checkpoint(ctx, "1", store.CoreCheckpoint)
	if err != nil || !found || checkpointAfter != checkpointBefore {
		t.Fatalf("checkpoint moved: before=%+v after=%+v found=%t error=%v", checkpointBefore, checkpointAfter, found, err)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT operation, status, started_at IS NOT NULL, completed_at IS NOT NULL, last_error IS NULL
		FROM repair_requests
		ORDER BY id`)
	if err != nil {
		t.Fatalf("query terminal maintenance audit: %v", err)
	}
	defer rows.Close()
	for _, wantOperation := range []string{"repair", "reindex"} {
		if !rows.Next() {
			t.Fatalf("terminal %s audit row is missing", wantOperation)
		}
		var operation, status string
		var started, completed, errorEmpty bool
		if err := rows.Scan(&operation, &status, &started, &completed, &errorEmpty); err != nil {
			t.Fatalf("scan terminal %s audit: %v", wantOperation, err)
		}
		if operation != wantOperation || status != "done" || !started || !completed || !errorEmpty {
			t.Fatalf("terminal audit operation=%q status=%q started=%t completed=%t error_empty=%t",
				operation, status, started, completed, errorEmpty)
		}
	}
	if rows.Next() {
		t.Fatal("unexpected extra terminal maintenance audit row")
	}

	var jobStatus, leasedBy, leaseToken string
	if err := db.QueryRowContext(ctx, `
		SELECT status, leased_by, lease_token
		FROM durable_jobs
		WHERE id = $1::bigint`, activeLease.Job.ID,
	).Scan(&jobStatus, &leasedBy, &leaseToken); err != nil {
		t.Fatalf("read active enrichment lease after reindex: %v", err)
	}
	if jobStatus != "leased" || leasedBy != "active-enricher" || leaseToken != activeLease.Token {
		t.Fatalf("reindex stole active lease: status=%q worker=%q token_match=%t", jobStatus, leasedBy, leaseToken == activeLease.Token)
	}
}

func isolatedDatabaseURL(t *testing.T, schema string) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(testDatabaseEnvironment))
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", testDatabaseEnvironment, err)
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return raw + " search_path=" + schema
}

type maintenanceBundleSource struct {
	bundle   ethrpc.Bundle
	purposes []ethrpc.Purpose
	numbers  []uint64
}

func (source *maintenanceBundleSource) BundleByNumber(
	_ context.Context,
	purpose ethrpc.Purpose,
	number uint64,
) (ethrpc.Bundle, error) {
	source.purposes = append(source.purposes, purpose)
	source.numbers = append(source.numbers, number)
	return source.bundle, nil
}

type cliRunner struct {
	stdout bytes.Buffer
	stderr bytes.Buffer
	app    *app.Backend
}

func newCLIRunner() *cliRunner {
	runner := &cliRunner{}
	runner.app = &app.Backend{Stdout: &runner.stdout, Stderr: &runner.stderr}
	return runner
}

func (r *cliRunner) run(ctx context.Context, args ...string) (int, string, string) {
	r.stdout.Reset()
	r.stderr.Reset()
	program := cli.Program{Backend: r.app, Stdout: &r.stdout, Stderr: &r.stderr}
	code := program.Run(ctx, args)
	return code, r.stdout.String(), r.stderr.String()
}
