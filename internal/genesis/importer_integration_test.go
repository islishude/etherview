//go:build integration

package genesis

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	genesisIntegrationDatabaseEnvironment = "ETHERVIEW_TEST_DATABASE_URL"
	genesisIntegrationBlockHash           = "01ea13d00d2698ff2d67208c43b4f0bfd2051a1b5af8566c395831a57b47a414"
	genesisIntegrationStateRoot           = "1ed58eaa9fa5ebfe410f6f13d27380e59ba5fbf03bc4f7f6276921721558c102"
)

type genesisRemoteRoundTripper struct {
	calls       atomic.Int32
	body        []byte
	contentType string
	statusCode  int
}

func (transport *genesisRemoteRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	transport.calls.Add(1)
	body := append([]byte(nil), transport.body...)
	statusCode := transport.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	header := make(http.Header)
	header.Set("Content-Type", transport.contentType)
	return &http.Response{
		StatusCode:    statusCode,
		Status:        http.StatusText(statusCode),
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       request,
	}, nil
}

type genesisImportSnapshot struct {
	BlockHash      string
	StateRoot      string
	DocumentSHA256 string
	AccountCount   string
	Accounts       []genesisAccountSnapshot
	Code           []genesisCodeSnapshot
	ProxyJobs      int
	Generation     int
}

type genesisAccountSnapshot struct {
	Address     string
	Balance     string
	Nonce       string
	CodeHash    string
	Code        string
	StorageRoot string
}

type genesisCodeSnapshot struct {
	Address  string
	CodeHash string
	Code     string
}

func TestRemoteGenesisImportMatchesFileAndCoordinatesReplicas(t *testing.T) {
	fileDB := newGenesisIntegrationPostgres(t)
	remoteDB := newGenesisIntegrationPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	seedGenesisIntegrationBlockZero(t, ctx, fileDB, genesisIntegrationStateRoot)
	seedGenesisIntegrationBlockZero(t, ctx, remoteDB, genesisIntegrationStateRoot)

	filePath := filepath.Join(t.TempDir(), "genesis.json")
	if err := os.WriteFile(filePath, []byte(genesisFixture), 0o600); err != nil {
		t.Fatalf("write local genesis fixture: %v", err)
	}
	fileImporter := newGenesisIntegrationImporter(t, fileDB, config.ChainConfig{
		ID:          777,
		StartBlock:  0,
		GenesisHash: "0x" + genesisIntegrationBlockHash,
		GenesisFile: filePath,
	})
	fileCancel, fileResult := startGenesisIntegrationImporter(ctx, fileImporter)
	waitForGenesisIntegrationComplete(t, ctx, fileDB, fileResult)
	stopGenesisIntegrationImporter(t, fileCancel, fileResult)

	documentDigest := sha256.Sum256([]byte(genesisFixture))
	transport := &genesisRemoteRoundTripper{
		body: []byte(genesisFixture), contentType: "application/json",
	}
	remoteChain := config.ChainConfig{
		ID:                  777,
		StartBlock:          0,
		GenesisHash:         "0x" + genesisIntegrationBlockHash,
		GenesisURL:          "https://genesis.example/genesis.json",
		GenesisSHA256:       hex.EncodeToString(documentDigest[:]),
		GenesisFetchTimeout: time.Second,
	}
	first := newGenesisIntegrationImporter(t, remoteDB, remoteChain)
	second := newGenesisIntegrationImporter(t, remoteDB, remoteChain)
	setGenesisIntegrationTransport(first, transport)
	setGenesisIntegrationTransport(second, transport)
	firstCancel, firstResult := startGenesisIntegrationImporter(ctx, first)
	secondCancel, secondResult := startGenesisIntegrationImporter(ctx, second)
	waitForGenesisIntegrationComplete(t, ctx, remoteDB, firstResult, secondResult)
	stopGenesisIntegrationImporter(t, firstCancel, firstResult)
	stopGenesisIntegrationImporter(t, secondCancel, secondResult)
	if got := transport.calls.Load(); got != 1 {
		t.Fatalf("remote Genesis requests = %d, want 1", got)
	}

	fileSnapshot := readGenesisIntegrationSnapshot(t, ctx, fileDB)
	remoteSnapshot := readGenesisIntegrationSnapshot(t, ctx, remoteDB)
	if !reflect.DeepEqual(remoteSnapshot, fileSnapshot) {
		t.Fatalf("remote snapshot = %#v, want file snapshot %#v", remoteSnapshot, fileSnapshot)
	}

	offline := &genesisRemoteRoundTripper{
		body: []byte(`{"must":"not be requested"}`), contentType: "application/json",
	}
	restartChain := remoteChain
	restartChain.GenesisURL = "https://offline.example/replaced-genesis.json"
	restart := newGenesisIntegrationImporter(t, remoteDB, restartChain)
	setGenesisIntegrationTransport(restart, offline)
	restartCancel, restartResult := startGenesisIntegrationImporter(ctx, restart)
	time.Sleep(50 * time.Millisecond)
	stopGenesisIntegrationImporter(t, restartCancel, restartResult)
	if got := offline.calls.Load(); got != 0 {
		t.Fatalf("completed restart made %d remote requests, want 0", got)
	}

	conflictingChain := restartChain
	conflictingChain.GenesisSHA256 = strings.Repeat("a", sha256.Size*2)
	conflicting := newGenesisIntegrationImporter(t, remoteDB, conflictingChain)
	setGenesisIntegrationTransport(conflicting, offline)
	err := conflicting.Run(ctx)
	if err == nil ||
		err.Error() != "stored completed genesis import conflicts with configured SHA-256" {
		t.Fatalf("persisted digest conflict error = %v", err)
	}
	if got := offline.calls.Load(); got != 0 {
		t.Fatalf("digest conflict made %d remote requests, want 0", got)
	}

	racing := newGenesisIntegrationImporter(t, fileDB, conflictingChain)
	if err := racing.prepareDocument([]byte(genesisFixture), documentDigest); err != nil {
		t.Fatalf("prepare racing remote document: %v", err)
	}
	if _, _, err := racing.importOnce(ctx); !errors.Is(err, errStoredGenesisDigestMismatch) {
		t.Fatalf("racing persisted digest conflict error = %v", err)
	}
}

func TestRemoteGenesisCompletedStateReauthenticatesCanonicalRoot(t *testing.T) {
	db := newGenesisIntegrationPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	seedGenesisIntegrationBlockZero(t, ctx, db, genesisIntegrationStateRoot)
	documentDigest := sha256.Sum256([]byte(genesisFixture))
	if _, err := db.ExecContext(ctx, `
		INSERT INTO genesis_state_imports (
		    chain_id, block_hash, state_root, document_sha256, state,
		    account_count, imported_at, updated_at
		) VALUES (
		    777,
		    decode($1, 'hex'),
		    decode(repeat('aa', 32), 'hex'),
		    $2,
		    'complete',
		    0,
		    clock_timestamp(),
		    clock_timestamp()
		)`,
		genesisIntegrationBlockHash,
		documentDigest[:],
	); err != nil {
		t.Fatalf("seed invalid completed Genesis identity: %v", err)
	}
	transport := &genesisRemoteRoundTripper{
		body: []byte(genesisFixture), contentType: "application/json",
	}
	importer := newGenesisIntegrationImporter(t, db, config.ChainConfig{
		ID:                  777,
		StartBlock:          0,
		GenesisHash:         "0x" + genesisIntegrationBlockHash,
		GenesisURL:          "https://genesis.example/genesis.json",
		GenesisSHA256:       hex.EncodeToString(documentDigest[:]),
		GenesisFetchTimeout: time.Second,
	})
	setGenesisIntegrationTransport(importer, transport)
	err := importer.Run(ctx)
	if err == nil ||
		err.Error() != "stored completed genesis import conflicts with canonical block zero state root" {
		t.Fatalf("completed canonical root error = %v", err)
	}
	if got := transport.calls.Load(); got != 0 {
		t.Fatalf("invalid completed identity made %d remote requests, want 0", got)
	}
}

func TestRemoteGenesisFailurePublishesNoPartialFacts(t *testing.T) {
	tests := []struct {
		name          string
		document      string
		expectedSHA   string
		canonicalRoot string
		statusCode    int
		wantKind      remoteFailureKind
		wantState     string
		wantCode      string
	}{
		{
			name:          "checksum mismatch",
			document:      genesisFixture,
			expectedSHA:   strings.Repeat("a", sha256.Size*2),
			canonicalRoot: genesisIntegrationStateRoot,
			wantKind:      remoteFailureFailed,
			wantState:     "failed",
			wantCode:      "genesis_remote_checksum_mismatch",
		},
		{
			name: "canonical block hash mismatch",
			document: strings.Replace(
				genesisFixture,
				`"extraData":"0x"`,
				`"extraData":"0x01"`,
				1,
			),
			canonicalRoot: genesisIntegrationStateRoot,
			wantKind:      remoteFailureFailed,
			wantState:     "failed",
			wantCode:      "genesis_remote_block_hash_mismatch",
		},
		{
			name:          "canonical state root mismatch",
			document:      genesisFixture,
			canonicalRoot: strings.Repeat("a", sha256.Size*2),
			wantKind:      remoteFailureFailed,
			wantState:     "failed",
			wantCode:      "genesis_remote_state_root_mismatch",
		},
		{
			name:          "temporary HTTP failure",
			document:      `{"hostile":"response"}`,
			canonicalRoot: genesisIntegrationStateRoot,
			statusCode:    http.StatusServiceUnavailable,
			wantKind:      remoteFailureUnavailable,
			wantState:     "unavailable",
			wantCode:      "genesis_remote_http_unavailable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := newGenesisIntegrationPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			seedGenesisIntegrationBlockZero(t, ctx, db, test.canonicalRoot)
			transport := &genesisRemoteRoundTripper{
				body:        []byte(test.document),
				contentType: "application/json",
				statusCode:  test.statusCode,
			}
			importer := newGenesisIntegrationImporter(t, db, config.ChainConfig{
				ID:                  777,
				StartBlock:          0,
				GenesisURL:          "https://genesis.example/genesis.json",
				GenesisSHA256:       test.expectedSHA,
				GenesisFetchTimeout: time.Second,
			})
			setGenesisIntegrationTransport(importer, transport)
			err := importer.Run(ctx)
			assertRemoteFailure(t, err, test.wantKind, test.wantCode)
			if got := transport.calls.Load(); got != 1 {
				t.Fatalf("failed remote Genesis requests = %d, want 1", got)
			}

			var state, code string
			if err := db.QueryRowContext(ctx, `
				SELECT state, last_error_code
				FROM genesis_state_imports
				WHERE chain_id = 777`,
			).Scan(&state, &code); err != nil {
				t.Fatalf("read remote Genesis failure: %v", err)
			}
			if state != test.wantState || code != test.wantCode {
				t.Fatalf(
					"remote failure = (%q, %q), want (%q, %q)",
					state,
					code,
					test.wantState,
					test.wantCode,
				)
			}
			var accounts, codeObservations, jobs int
			if err := db.QueryRowContext(ctx, `
				SELECT
				    (SELECT count(*) FROM genesis_account_observations WHERE chain_id = 777),
				    (SELECT count(*) FROM contract_code_observations WHERE chain_id = 777),
				    (SELECT count(*) FROM durable_jobs WHERE chain_id = 777)`,
			).Scan(&accounts, &codeObservations, &jobs); err != nil {
				t.Fatalf("count partial remote Genesis facts: %v", err)
			}
			if accounts != 0 || codeObservations != 0 || jobs != 0 {
				t.Fatalf(
					"partial facts accounts=%d code=%d jobs=%d, want all zero",
					accounts,
					codeObservations,
					jobs,
				)
			}
		})
	}
}

func newGenesisIntegrationImporter(
	t *testing.T,
	db *sql.DB,
	chain config.ChainConfig,
) *Importer {
	t.Helper()
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatalf("construct Genesis proxy queue: %v", err)
	}
	importer, err := NewImporter(db, chain, queue, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("construct Genesis importer: %v", err)
	}
	return importer
}

func setGenesisIntegrationTransport(
	importer *Importer,
	transport http.RoundTripper,
) {
	importer.remote.http = &http.Client{
		Transport: transport,
		Timeout:   time.Second,
	}
}

func startGenesisIntegrationImporter(
	parent context.Context,
	importer *Importer,
) (context.CancelFunc, <-chan error) {
	ctx, cancel := context.WithCancel(parent)
	result := make(chan error, 1)
	go func() {
		result <- importer.Run(ctx)
	}()
	return cancel, result
}

func waitForGenesisIntegrationComplete(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	results ...<-chan error,
) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var state string
		err := db.QueryRowContext(ctx, `
			SELECT state
			FROM genesis_state_imports
			WHERE chain_id = 777`,
		).Scan(&state)
		if err == nil && state == "complete" {
			return
		}
		for _, result := range results {
			select {
			case runErr := <-result:
				t.Fatalf("Genesis importer exited before completion: %v", runErr)
			default:
			}
		}
		select {
		case <-timer.C:
			t.Fatalf("timed out waiting for Genesis import: state=%q error=%v", state, err)
		case <-ticker.C:
		}
	}
}

func stopGenesisIntegrationImporter(
	t *testing.T,
	cancel context.CancelFunc,
	result <-chan error,
) {
	t.Helper()
	cancel()
	select {
	case err := <-result:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("stop Genesis importer: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Genesis importer did not stop")
	}
}

func readGenesisIntegrationSnapshot(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
) genesisImportSnapshot {
	t.Helper()
	var snapshot genesisImportSnapshot
	if err := db.QueryRowContext(ctx, `
		SELECT
		    encode(block_hash, 'hex'),
		    encode(state_root, 'hex'),
		    encode(document_sha256, 'hex'),
		    account_count::text
		FROM genesis_state_imports
		WHERE chain_id = 777 AND state = 'complete'`,
	).Scan(
		&snapshot.BlockHash,
		&snapshot.StateRoot,
		&snapshot.DocumentSHA256,
		&snapshot.AccountCount,
	); err != nil {
		t.Fatalf("read Genesis import identity: %v", err)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT
		    encode(address, 'hex'),
		    balance::text,
		    nonce::text,
		    encode(code_hash, 'hex'),
		    encode(code, 'hex'),
		    encode(storage_root, 'hex')
		FROM genesis_account_observations
		WHERE chain_id = 777
		ORDER BY address`)
	if err != nil {
		t.Fatalf("read Genesis accounts: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var account genesisAccountSnapshot
		if err := rows.Scan(
			&account.Address,
			&account.Balance,
			&account.Nonce,
			&account.CodeHash,
			&account.Code,
			&account.StorageRoot,
		); err != nil {
			t.Fatalf("scan Genesis account: %v", err)
		}
		snapshot.Accounts = append(snapshot.Accounts, account)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate Genesis accounts: %v", err)
	}
	codeRows, err := db.QueryContext(ctx, `
		SELECT encode(address, 'hex'), encode(code_hash, 'hex'), encode(code, 'hex')
		FROM contract_code_observations
		WHERE chain_id = 777 AND block_number = 0
		ORDER BY address`)
	if err != nil {
		t.Fatalf("read Genesis code observations: %v", err)
	}
	defer codeRows.Close()
	for codeRows.Next() {
		var code genesisCodeSnapshot
		if err := codeRows.Scan(&code.Address, &code.CodeHash, &code.Code); err != nil {
			t.Fatalf("scan Genesis code observation: %v", err)
		}
		snapshot.Code = append(snapshot.Code, code)
	}
	if err := codeRows.Err(); err != nil {
		t.Fatalf("iterate Genesis code observations: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT count(*), COALESCE(max(requested_generation), 0)::int
		FROM durable_jobs
		WHERE chain_id = 777
		  AND stage = 'proxy'
		  AND stage_version = 1`,
	).Scan(&snapshot.ProxyJobs, &snapshot.Generation); err != nil {
		t.Fatalf("read Genesis proxy job: %v", err)
	}
	return snapshot
}

func seedGenesisIntegrationBlockZero(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	stateRoot string,
) {
	t.Helper()
	zeroHash := make([]byte, sha256.Size)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO chains (chain_id, genesis_hash)
		VALUES (777, decode($1, 'hex'))`,
		genesisIntegrationBlockHash,
	); err != nil {
		t.Fatalf("seed Genesis chain identity: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO blocks (
		    chain_id, number, hash, parent_hash, timestamp, raw
		) VALUES (
		    777, 0, decode($1, 'hex'), $2, 0,
		    jsonb_build_object('stateRoot', '0x' || $3)
		)`,
		genesisIntegrationBlockHash,
		zeroHash,
		stateRoot,
	); err != nil {
		t.Fatalf("seed Genesis block zero: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO canonical_blocks (chain_id, number, block_hash)
		VALUES (777, 0, decode($1, 'hex'))`,
		genesisIntegrationBlockHash,
	); err != nil {
		t.Fatalf("make Genesis block zero canonical: %v", err)
	}
}

func newGenesisIntegrationPostgres(t *testing.T) *sql.DB {
	t.Helper()
	rawURL := strings.TrimSpace(os.Getenv(genesisIntegrationDatabaseEnvironment))
	if rawURL == "" {
		t.Skipf("%s is not set", genesisIntegrationDatabaseEnvironment)
	}
	adminConfig, err := pgx.ParseConfig(rawURL)
	if err != nil {
		t.Fatalf("parse %s: %v", genesisIntegrationDatabaseEnvironment, err)
	}
	adminConfig.RuntimeParams = cloneGenesisIntegrationRuntimeParams(adminConfig.RuntimeParams)
	adminConfig.RuntimeParams["application_name"] = "etherview-genesis-integration-admin"
	adminDB := stdlib.OpenDB(*adminConfig)
	adminDB.SetMaxOpenConns(2)
	adminDB.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := adminDB.PingContext(ctx); err != nil {
		_ = adminDB.Close()
		t.Fatalf("connect to %s: %v", genesisIntegrationDatabaseEnvironment, err)
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generate Genesis integration schema: %v", err)
	}
	schema := "etherview_genesis_it_" + hex.EncodeToString(suffix)
	quotedSchema := `"` + schema + `"`
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quotedSchema); err != nil {
		_ = adminDB.Close()
		t.Fatalf("create Genesis integration schema: %v", err)
	}

	testConfig := adminConfig.Copy()
	testConfig.RuntimeParams = cloneGenesisIntegrationRuntimeParams(testConfig.RuntimeParams)
	testConfig.RuntimeParams["application_name"] = "etherview-genesis-integration-test"
	testConfig.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*testConfig)
	db.SetMaxOpenConns(6)
	db.SetMaxIdleConns(2)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		_, _ = adminDB.ExecContext(context.Background(), `DROP SCHEMA `+quotedSchema+` CASCADE`)
		_ = adminDB.Close()
		t.Fatalf("connect to Genesis integration schema: %v", err)
	}
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run Genesis integration migrations: %v", err)
	}
	if err := store.CheckSchema(ctx, db); err != nil {
		t.Fatalf("check Genesis integration schema: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close Genesis integration database: %v", err)
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := adminDB.ExecContext(
			cleanupCtx,
			`DROP SCHEMA `+quotedSchema+` CASCADE`,
		); err != nil {
			t.Errorf("drop Genesis integration schema: %v", err)
		}
		if err := adminDB.Close(); err != nil {
			t.Errorf("close Genesis integration admin database: %v", err)
		}
	})
	return db
}

func cloneGenesisIntegrationRuntimeParams(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source)+2)
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
