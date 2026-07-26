//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/enrich"
	genesisstate "github.com/islishude/etherview/internal/genesis"
	"github.com/islishude/etherview/internal/query"
)

const integrationGenesisFixture = `{
  "config":{"chainId":777,"homesteadBlock":0,"eip150Block":0,"eip155Block":0,"eip158Block":0,"byzantiumBlock":0,"constantinopleBlock":0,"petersburgBlock":0,"istanbulBlock":0,"berlinBlock":0,"londonBlock":0},
  "nonce":"0x0",
  "timestamp":"0x0",
  "extraData":"0x",
  "gasLimit":"0x1c9c380",
  "difficulty":"0x1",
  "mixHash":"0x0000000000000000000000000000000000000000000000000000000000000000",
  "coinbase":"0x0000000000000000000000000000000000000000",
  "alloc":{
    "1000000000000000000000000000000000000001":{"balance":"0x2a"},
    "2000000000000000000000000000000000000002":{"balance":"1000000000000000000","nonce":"0x3","code":"0x6001600055","storage":{"0x00":"0x07","0x02":"0x09"}}
  }
}`

const (
	integrationGenesisBlockHash = "01ea13d00d2698ff2d67208c43b4f0bfd2051a1b5af8566c395831a57b47a414"
	integrationGenesisStateRoot = "1ed58eaa9fa5ebfe410f6f13d27380e59ba5fbf03bc4f7f6276921721558c102"
)

func TestGenesisStateImportIsAtomicAndPersistsExactPredeployCode(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	seedIntegrationGenesisZero(t, ctx, db)

	path := writeIntegrationGenesisFixture(t)
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatalf("construct genesis proxy queue: %v", err)
	}
	importer, err := genesisstate.NewImporter(db, config.ChainConfig{
		ID:          777,
		GenesisHash: "0x" + integrationGenesisBlockHash,
		GenesisFile: path,
		StartBlock:  0,
	}, queue, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("construct genesis importer: %v", err)
	}
	runContext, stop := context.WithCancel(ctx)
	result := make(chan error, 1)
	finished := false
	go func() {
		result <- importer.Run(runContext)
	}()
	t.Cleanup(func() {
		stop()
		if finished {
			return
		}
		select {
		case err := <-result:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("genesis importer shutdown: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("genesis importer did not stop")
		}
	})

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var state string
		var count int
		err := db.QueryRowContext(ctx, `
			SELECT state, account_count::int
			FROM genesis_state_imports
			WHERE chain_id = 777`,
		).Scan(&state, &count)
		if err == nil && state == "complete" && count == 2 {
			break
		}
		select {
		case runErr := <-result:
			finished = true
			t.Fatalf("genesis importer exited before publication: %v", runErr)
		case <-deadline.C:
			t.Fatalf("timed out waiting for genesis import: state=%q count=%d err=%v", state, count, err)
		case <-ticker.C:
		}
	}

	var (
		balance, nonce                   string
		code, codeHash, storageRootBytes []byte
	)
	if err := db.QueryRowContext(ctx, `
		SELECT balance::text, nonce::text, code, code_hash, storage_root
		FROM genesis_account_observations
		WHERE chain_id = 777
		  AND address = decode('2000000000000000000000000000000000000002', 'hex')`,
	).Scan(&balance, &nonce, &code, &codeHash, &storageRootBytes); err != nil {
		t.Fatalf("read imported predeploy: %v", err)
	}
	if balance != "1000000000000000000" || nonce != "3" ||
		string(code) != string([]byte{0x60, 0x01, 0x60, 0x00, 0x55}) ||
		len(codeHash) != 32 || len(storageRootBytes) != 32 {
		t.Fatalf(
			"imported predeploy balance=%s nonce=%s code=%x codeHash=%x storageRoot=%x",
			balance, nonce, code, codeHash, storageRootBytes,
		)
	}
	var codeObservations int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM contract_code_observations
		WHERE chain_id = 777
		  AND block_hash = decode($1, 'hex')
		  AND address = decode('2000000000000000000000000000000000000002', 'hex')
		  AND block_number = 0
		  AND canonical
		  AND code = decode('6001600055', 'hex')`,
		integrationGenesisBlockHash,
	).Scan(&codeObservations); err != nil {
		t.Fatalf("read predeploy code observation: %v", err)
	}
	if codeObservations != 1 {
		t.Fatalf("predeploy code observation count = %d, want 1", codeObservations)
	}
	var proxyJobs, requestedGeneration int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*), max(requested_generation)::int
		FROM durable_jobs
		WHERE chain_id = 777
		  AND stage = 'proxy'
		  AND stage_version = 1
		  AND payload ->> 'block_hash' = '0x' || $1`,
		integrationGenesisBlockHash,
	).Scan(&proxyJobs, &requestedGeneration); err != nil {
		t.Fatalf("read genesis proxy wake: %v", err)
	}
	if proxyJobs != 1 || requestedGeneration != 1 {
		t.Fatalf(
			"genesis proxy jobs=%d requested generation=%d, want one generation-one job",
			proxyJobs, requestedGeneration,
		)
	}

	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 777})
	if err != nil {
		t.Fatalf("construct genesis account reader: %v", err)
	}
	firstPage, cursor, err := reader.GenesisAccounts(ctx, "", 1)
	if err != nil {
		t.Fatalf("read first genesis account page: %v", err)
	}
	if len(firstPage) != 1 || firstPage[0].Type != "eoa" ||
		firstPage[0].Balance != "42" || cursor == "" {
		t.Fatalf("first genesis account page = %+v cursor=%q", firstPage, cursor)
	}
	secondPage, next, err := reader.GenesisAccounts(ctx, cursor, 1)
	if err != nil {
		t.Fatalf("read second genesis account page: %v", err)
	}
	if len(secondPage) != 1 || secondPage[0].Type != "contract" ||
		secondPage[0].Nonce != "3" || next != "" {
		t.Fatalf("second genesis account page = %+v cursor=%q", secondPage, next)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE genesis_account_observations
		SET balance = 43
		WHERE chain_id = 777
		  AND address = decode('1000000000000000000000000000000000000001', 'hex')`,
	); err == nil {
		t.Fatal("mutating an exact genesis account observation succeeded")
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE genesis_state_imports
		SET document_sha256 = decode(repeat('aa', 32), 'hex')
		WHERE chain_id = 777`,
	); err == nil {
		t.Fatal("mutating a completed genesis import identity succeeded")
	}
}

func TestGenesisStateImportRollsBackOnExactCodeConflict(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	seedIntegrationGenesisZero(t, ctx, db)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO contract_code_observations (
		    chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES (
		    777,
		    decode('2000000000000000000000000000000000000002', 'hex'),
		    0,
		    decode($1, 'hex'),
		    decode($2, 'hex'),
		    decode('ff', 'hex'),
		    TRUE
		)`,
		integrationGenesisBlockHash,
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	); err != nil {
		t.Fatalf("seed conflicting exact code observation: %v", err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatalf("construct genesis proxy queue: %v", err)
	}
	importer, err := genesisstate.NewImporter(db, config.ChainConfig{
		ID:          777,
		GenesisHash: "0x" + integrationGenesisBlockHash,
		GenesisFile: writeIntegrationGenesisFixture(t),
		StartBlock:  0,
	}, queue, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("construct genesis importer: %v", err)
	}
	err = importer.Run(ctx)
	if err == nil || err.Error() != "genesis code observation conflicts with an existing exact fact" {
		t.Fatalf("conflicting import error = %v", err)
	}
	var imports, accounts, jobs int
	if err := db.QueryRowContext(ctx, `
		SELECT
		    (SELECT count(*) FROM genesis_state_imports WHERE chain_id = 777),
		    (SELECT count(*) FROM genesis_account_observations WHERE chain_id = 777),
		    (SELECT count(*) FROM durable_jobs WHERE chain_id = 777)`,
	).Scan(&imports, &accounts, &jobs); err != nil {
		t.Fatalf("count rows after conflicting import: %v", err)
	}
	if imports != 0 || accounts != 0 || jobs != 0 {
		t.Fatalf(
			"rows after rollback imports=%d accounts=%d jobs=%d, want all zero",
			imports, accounts, jobs,
		)
	}
}

func seedIntegrationGenesisZero(t *testing.T, ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}) {
	t.Helper()
	zeroHash := make([]byte, 32)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO chains (chain_id, genesis_hash)
		VALUES (777, decode($1, 'hex'))`,
		integrationGenesisBlockHash,
	); err != nil {
		t.Fatalf("seed genesis chain identity: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO blocks (
		    chain_id, number, hash, parent_hash, timestamp, raw
		) VALUES (
		    777, 0, decode($1, 'hex'), $2, 0,
		    jsonb_build_object('stateRoot', '0x' || $3)
		)`,
		integrationGenesisBlockHash, zeroHash, integrationGenesisStateRoot,
	); err != nil {
		t.Fatalf("seed block zero: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO canonical_blocks (chain_id, number, block_hash)
		VALUES (777, 0, decode($1, 'hex'))`,
		integrationGenesisBlockHash,
	); err != nil {
		t.Fatalf("make block zero canonical: %v", err)
	}
}

func writeIntegrationGenesisFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "genesis.json")
	if err := os.WriteFile(path, []byte(integrationGenesisFixture), 0o600); err != nil {
		t.Fatalf("write genesis fixture: %v", err)
	}
	return path
}
