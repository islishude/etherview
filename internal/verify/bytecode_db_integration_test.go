//go:build integration

package verify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/testpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func bytecodeDBPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("ETHERVIEW_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("ETHERVIEW_TEST_DATABASE_URL is not set")
	}
	cfg, err := pgx.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin := testpgx.Pool(t, cfg, 2)
	suffix, err := randomToken(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	schema := "bytecode_db_it_" + hex.EncodeToString([]byte(suffix)[:8])
	if _, err = admin.Exec(t.Context(), `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams = maps.Clone(cfg.RuntimeParams)
	cfg.RuntimeParams["search_path"] = schema
	db := testpgx.Pool(t, cfg, 8)
	t.Cleanup(func() {
		db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := admin.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
		if err != nil {
			t.Error(err)
		}
	})
	if err = store.RunMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func bytecodeDBExec(t *testing.T, db *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := db.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func bytecodeDBIntegrationWorker(t *testing.T, db *pgxpool.Pool, handler http.HandlerFunc) (*BytecodeDBWorker, *verifyTestCompiler) {
	t.Helper()
	client := bytecodeDBTestClient(t, handler)
	repository, err := NewPostgresRepository(db, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	compiler := &verifyTestCompiler{provenance: testSolcJSProvenance(), output: candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{"A.sol": {"A": {creation: []byte{0x60, 1}, runtime: []byte{0x60, 2}}}})}
	p := compiler.provenance
	digest := sha256.Sum256([]byte("catalog"))
	bytecodeDBExec(t, db, `INSERT INTO compiler_catalog_generations(language,source_url,catalog_digest,entry_count) VALUES('solidity','https://example.com/list.json',$1,1)`, digest[:])
	bytecodeDBExec(t, db, `INSERT INTO compiler_catalog_entries(generation_id,language,version,platform,artifact_url,artifact_sha256,max_bytes) VALUES(1,'solidity','0.8.30+commit.73712a01','emscripten-wasm32','https://example.com/soljson.js',$1,209715200)`, p.Digest[:])
	bytecodeDBExec(t, db, `INSERT INTO compiler_catalog_heads(language,generation_id) VALUES('solidity',1)`)
	worker, err := NewBytecodeDBWorker(repository, client, compiler, BytecodeDBWorkerOptions{Name: "bytecode-test", ChainID: 1, MaxConcurrency: 2, RequestInterval: time.Second, Cooldown: 24 * time.Hour, MaxInputBytes: 5 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if err = dbgen.New(db).BytecodeDBInit(t.Context(), worker.service); err != nil {
		t.Fatal(err)
	}
	return worker, compiler
}

func bytecodeDBSeedGenesis(t *testing.T, db *pgxpool.Pool, addresses ...string) {
	t.Helper()
	hash := sha256.Sum256([]byte("genesis"))
	code := []byte{0x60, 2}
	codeHash := keccak256Bytes(code)
	bytecodeDBExec(t, db, `INSERT INTO chains(chain_id,genesis_hash) VALUES(1,$1)`, hash[:])
	bytecodeDBExec(t, db, `INSERT INTO blocks(chain_id,number,hash,parent_hash,timestamp,raw) VALUES(1,0,$1,$1,0,'{}')`, hash[:])
	bytecodeDBExec(t, db, `INSERT INTO canonical_blocks(chain_id,number,block_hash) VALUES(1,0,$1)`, hash[:])
	bytecodeDBExec(t, db, `INSERT INTO genesis_state_imports(chain_id,state,block_hash,state_root,document_sha256,account_count,imported_at) VALUES(1,'complete',$1,$1,$1,$2,now())`, hash[:], len(addresses))
	for _, address := range addresses {
		bytes := common.HexToAddress(address).Bytes()
		bytecodeDBExec(t, db, `INSERT INTO genesis_account_observations(chain_id,address,block_hash,balance,nonce,code_hash,code,storage_root) VALUES(1,$1,$2,0,0,$3,$4,$2)`, bytes, hash[:], codeHash, code)
		bytecodeDBExec(t, db, `INSERT INTO contract_code_observations(chain_id,address,block_number,block_hash,code_hash,code,canonical) VALUES(1,$1,0,$2,$3,$4,true)`, bytes, hash[:], codeHash, code)
	}
}

func TestBytecodeDBDiscoveryVerificationAndOrigin(t *testing.T) {
	db := bytecodeDBPostgres(t)
	var calls atomic.Int32
	w, compiler := bytecodeDBIntegrationWorker(t, db, func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(response).Encode(map[string]any{"sources": []BytecodeDBSource{bytecodeDBSourceFixture()}})
	})
	address := "0x" + strings.Repeat("11", 20)
	bytecodeDBSeedGenesis(t, db, address)
	if err := w.tick(t.Context()); err != nil {
		t.Fatal(err)
	} // discover and search
	bytecodeDBExec(t, db, `UPDATE bytecode_db_targets SET next_attempt_at=now()`)
	if err := w.tick(t.Context()); err != nil {
		t.Fatal(err)
	} // enqueue verification
	local, err := NewWorker(w.repository, compiler, WorkerOptions{WorkerID: "local", LeaseDuration: time.Minute, PollInterval: time.Second, MaxOutputBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if found, err := local.ProcessOne(t.Context()); err != nil || !found {
		t.Fatalf("local verification found=%t err=%v", found, err)
	}
	var status, outcome string
	if err = db.QueryRow(t.Context(), `SELECT status,coalesce(outcome_kind,error_code,'') FROM verification_jobs`).Scan(&status, &outcome); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || outcome != "verification_success" {
		t.Fatalf("status=%s outcome=%s", status, outcome)
	}
	artifact, found, err := w.repository.VerifiedContract(t.Context(), 1, address)
	if err != nil || !found || artifact.VerificationOrigin != VerificationOriginBytecodeDB {
		t.Fatalf("found=%t origin=%s err=%v", found, artifact.VerificationOrigin, err)
	}
	if compiler.compileCalls != 2 {
		t.Fatalf("local dual compilation calls=%d", compiler.compileCalls)
	}
	bytecodeDBExec(t, db, `UPDATE bytecode_db_targets SET next_attempt_at=now()`)
	if err = w.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || compiler.compileCalls != 2 {
		t.Fatal("published source was queried or compiled again")
	}
	// A second process must see the same immutable result and source origin.
	reader, err := NewPostgresRepository(db, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	other, found, err := reader.VerifiedContract(t.Context(), 1, address)
	if err != nil || !found || other.VerificationOrigin != VerificationOriginBytecodeDB {
		t.Fatal("split reader lost origin")
	}
}

func TestBytecodeDBGlobalAdmissionCooldownAndLeaseFencing(t *testing.T) {
	db := bytecodeDBPostgres(t)
	w, _ := bytecodeDBIntegrationWorker(t, db, func(response http.ResponseWriter, request *http.Request) { response.WriteHeader(429) })
	q := dbgen.New(db)
	for i := byte(1); i <= 4; i++ {
		code := []byte{i}
		digest := sha256.Sum256(code)
		if err := q.BytecodeDBEnqueueLookup(t.Context(), dbgen.BytecodeDBEnqueueLookupParams{ServiceID: w.service, BytecodeType: "DEPLOYED_BYTECODE", Digest: digest[:], Bytecode: code}); err != nil {
			t.Fatal(err)
		}
	}
	var group sync.WaitGroup
	var admitted atomic.Int32
	for range 8 {
		group.Go(func() {
			_, found, err := w.claimLookup(t.Context())
			if err != nil {
				t.Error(err)
			}
			if found {
				admitted.Add(1)
			}
		})
	}
	group.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("global rate admitted=%d", admitted.Load())
	}
	bytecodeDBExec(t, db, `UPDATE bytecode_db_services SET next_request_at=now()`)
	lookup, found, err := w.claimLookup(t.Context())
	if err != nil || !found {
		t.Fatalf("second claim found=%t err=%v", found, err)
	}
	bytecodeDBExec(t, db, `UPDATE bytecode_db_services SET next_request_at=now()`)
	if _, found, err = w.claimLookup(t.Context()); err != nil || found {
		t.Fatalf("concurrency exceeded: found=%t err=%v", found, err)
	}
	bytecodeDBExec(t, db, `UPDATE bytecode_db_lookups SET lease_expires_at=now()-interval '1 second' WHERE digest=$1`, lookup.Digest)
	if err = w.search(t.Context(), lookup); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale result accepted: %v", err)
	}
	var sources []byte
	if err = db.QueryRow(t.Context(), `SELECT sources FROM bytecode_db_lookups WHERE digest=$1`, lookup.Digest).Scan(&sources); err != nil || len(sources) > 0 {
		t.Fatal("stale search published")
	}
}

func TestBytecodeDBManualPriorityAndSingleAutomaticCompiler(t *testing.T) {
	db := bytecodeDBPostgres(t)
	w, _ := bytecodeDBIntegrationWorker(t, db, func(http.ResponseWriter, *http.Request) {})
	addresses := []string{"0x" + strings.Repeat("11", 20), "0x" + strings.Repeat("22", 20)}
	bytecodeDBSeedGenesis(t, db, addresses...)
	q := dbgen.New(db)
	for _, address := range addresses {
		if err := q.BytecodeDBEnqueueTarget(t.Context(), w.service, w.chain, common.HexToAddress(address).Bytes()); err != nil {
			t.Fatal(err)
		}
		task, err := q.BytecodeDBClaimTarget(t.Context(), new(address), w.service, w.chain)
		if err != nil {
			t.Fatal(err)
		}
		target, err := ResolveTarget(t.Context(), db, 1, common.BytesToAddress(task.Address).Hex(), 5<<20)
		if err != nil {
			t.Fatal(err)
		}
		state, err := w.tryCandidate(t.Context(), task, target, bytecodeDBSourceFixture())
		if err != nil || state != "verification_pending" {
			t.Fatalf("state=%s err=%v", state, err)
		}
	}
	target, err := ResolveTarget(t.Context(), db, 1, addresses[0], 5<<20)
	if err != nil {
		t.Fatal(err)
	}
	request, err := bytecodeDBSubmission(bytecodeDBSourceFixture(), target, 5<<20)
	if err != nil {
		t.Fatal(err)
	}
	service, _ := NewService(w.repository, 5<<20)
	manual, _, err := service.SubmitV2(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	lease, found, err := w.repository.Claim(t.Context(), "manual", time.Minute)
	if err != nil || !found || lease.Job.ID != manual.ID {
		t.Fatalf("manual priority lost: %s %t %v", lease.Job.ID, found, err)
	}
	lease, found, err = w.repository.Claim(t.Context(), "auto-1", time.Minute)
	if err != nil || !found || lease.Job.RequestV2.BytecodeDB == nil {
		t.Fatalf("auto claim failed: %t %v", found, err)
	}
	if _, found, err = w.repository.Claim(t.Context(), "auto-2", time.Minute); err != nil || found {
		t.Fatalf("two auto compilers claimed: %t %v", found, err)
	}
	var id pgtype.UUID
	_ = id.Scan(lease.Job.ID)
	bytecodeDBExec(t, db, `UPDATE verification_jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, id)
	if _, found, err = w.repository.Claim(t.Context(), "recovery", time.Minute); err != nil || !found {
		t.Fatalf("expired auto lease not reclaimed: %t %v", found, err)
	}
}

func TestBytecodeDBMismatchDedupReorgAndLateFacts(t *testing.T) {
	db := bytecodeDBPostgres(t)
	w, compiler := bytecodeDBIntegrationWorker(t, db, func(response http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(response).Encode(map[string]any{"sources": []BytecodeDBSource{bytecodeDBSourceFixture()}})
	})
	address := "0x" + strings.Repeat("11", 20)
	bytecodeDBSeedGenesis(t, db, address)
	// A bogus remote FULL match still must pass the local runtime matcher.
	compiler.output = candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{"A.sol": {"A": {creation: []byte{0x60, 1}, runtime: []byte{0x60, 9}}}})
	if err := w.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	bytecodeDBExec(t, db, `UPDATE bytecode_db_targets SET next_attempt_at=now()`)
	if err := w.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	local, err := NewWorker(w.repository, compiler, WorkerOptions{WorkerID: "local", LeaseDuration: time.Minute, PollInterval: time.Second, MaxOutputBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if found, err := local.ProcessOne(t.Context()); err != nil || !found {
		t.Fatalf("found=%t err=%v", found, err)
	}
	var publications int
	if err = db.QueryRow(t.Context(), `SELECT count(*) FROM verified_contracts`).Scan(&publications); err != nil || publications != 0 {
		t.Fatalf("remote FULL bypassed matcher: %d %v", publications, err)
	}
	for range 2 {
		bytecodeDBExec(t, db, `UPDATE bytecode_db_targets SET next_attempt_at=now()`)
		if err = w.tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var attempts int
	if err = db.QueryRow(t.Context(), `SELECT count(*) FROM bytecode_db_attempts`).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("deterministic attempt repeated: %d %v", attempts, err)
	}
	if compiler.compileCalls != 2 {
		t.Fatal("deterministic mismatch recompiled")
	}
	// Changing the supported executor identity permits exactly one new attempt.
	compiler.provenance.ExecutorDigest = sha256.Sum256([]byte("updated executor"))
	bytecodeDBExec(t, db, `UPDATE bytecode_db_targets SET next_attempt_at=now()`)
	if err = w.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(t.Context(), `SELECT count(*) FROM bytecode_db_attempts`).Scan(&attempts); err != nil || attempts != 2 {
		t.Fatalf("support change ignored: %d %v", attempts, err)
	}
	// Detaching the selected exact block after enqueue cannot publish source.
	compiler.output = candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{"A.sol": {"A": {creation: []byte{0x60, 1}, runtime: []byte{0x60, 2}}}})
	bytecodeDBExec(t, db, `DELETE FROM canonical_blocks WHERE chain_id=1`)
	_, _ = local.ProcessOne(t.Context())
	if err = db.QueryRow(t.Context(), `SELECT count(*) FROM verified_contracts`).Scan(&publications); err != nil || publications != 0 {
		t.Fatalf("orphan target published: %d %v", publications, err)
	}
	// Late facts behind the completed discovery cursor are picked up on a new sweep.
	hash := sha256.Sum256([]byte("genesis"))
	bytecodeDBExec(t, db, `INSERT INTO canonical_blocks(chain_id,number,block_hash) VALUES(1,0,$1)`, hash[:])
	newAddress := common.HexToAddress("0x" + strings.Repeat("01", 20)).Bytes()
	code := []byte{0x60, 2}
	bytecodeDBExec(t, db, `INSERT INTO contract_code_observations(chain_id,address,block_number,block_hash,code_hash,code,canonical) VALUES(1,$1,0,$2,$3,$4,true)`, newAddress, hash[:], keccak256Bytes(code), code)
	bytecodeDBExec(t, db, `UPDATE bytecode_db_scans SET scan_after=now(),cursor_address=''`)
	if err = w.discover(t.Context()); err != nil {
		t.Fatal(err)
	}
	target, err := ResolveTarget(t.Context(), db, 1, common.BytesToAddress(newAddress).Hex(), 5<<20)
	if !errors.Is(err, ErrTargetUnavailable) {
		t.Fatalf("ordinary missing creation accepted: %+v %v", target, err)
	}
	var discovered int
	if err = db.QueryRow(t.Context(), `SELECT count(*) FROM bytecode_db_targets WHERE address=$1`, newAddress).Scan(&discovered); err != nil || discovered != 1 {
		t.Fatalf("late target missed: %d %v", discovered, err)
	}
}

func TestBytecodeDBNegativeCacheRequiresDemandAndSurvivesRestart(t *testing.T) {
	db := bytecodeDBPostgres(t)
	var calls atomic.Int32
	w, _ := bytecodeDBIntegrationWorker(t, db, func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		_, _ = response.Write([]byte(`{"sources":[]}`))
	})
	bytecodeDBSeedGenesis(t, db, "0x"+strings.Repeat("11", 20))
	if err := w.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	copyWorker := *w
	bytecodeDBExec(t, db, `UPDATE bytecode_db_targets SET next_attempt_at=now()`)
	if err := copyWorker.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("restart erased negative cache")
	}
	bytecodeDBExec(t, db, `UPDATE bytecode_db_lookups SET next_attempt_at=now()`)
	bytecodeDBExec(t, db, `UPDATE bytecode_db_services SET next_request_at=now()`)
	if _, found, err := copyWorker.claimLookup(t.Context()); err != nil || found {
		t.Fatalf("unused cache refreshed: %t %v", found, err)
	}
	bytecodeDBExec(t, db, `UPDATE bytecode_db_targets SET next_attempt_at=now()`)
	if err := copyWorker.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("expired demanded lookup did not refresh")
	}
}

func TestBytecodeDBAutomaticPublicationPreservesSubmittedSource(t *testing.T) {
	db := bytecodeDBPostgres(t)
	w, compiler := bytecodeDBIntegrationWorker(t, db, func(http.ResponseWriter, *http.Request) {})
	address := "0x" + strings.Repeat("11", 20)
	bytecodeDBSeedGenesis(t, db, address)
	q := dbgen.New(db)
	if err := q.BytecodeDBEnqueueTarget(t.Context(), w.service, w.chain, common.HexToAddress(address).Bytes()); err != nil {
		t.Fatal(err)
	}
	task, err := q.BytecodeDBClaimTarget(t.Context(), new("test"), w.service, w.chain)
	if err != nil {
		t.Fatal(err)
	}
	target, err := ResolveTarget(t.Context(), db, 1, address, 5<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.tryCandidate(t.Context(), task, target, bytecodeDBSourceFixture()); err != nil {
		t.Fatal(err)
	}
	request, err := bytecodeDBSubmission(bytecodeDBSourceFixture(), target, 5<<20)
	if err != nil {
		t.Fatal(err)
	}
	service, _ := NewService(w.repository, 5<<20)
	if _, _, err = service.SubmitV2(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	local, err := NewWorker(w.repository, compiler, WorkerOptions{WorkerID: "local", LeaseDuration: time.Minute, PollInterval: time.Second, MaxOutputBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if found, err := local.ProcessOne(t.Context()); err != nil || !found {
			t.Fatalf("process found=%t err=%v", found, err)
		}
	}
	artifact, found, err := w.repository.VerifiedContract(t.Context(), 1, address)
	if err != nil || !found || artifact.VerificationOrigin != VerificationOriginSubmitted {
		t.Fatalf("existing source replaced: %t %s %v", found, artifact.VerificationOrigin, err)
	}
	var count int
	if err = db.QueryRow(t.Context(), `SELECT count(*) FROM verified_contracts`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate publication: %d %v", count, err)
	}
}

func TestBytecodeDBWaitsForCreationThenFallsBackWithoutSharingEvidence(t *testing.T) {
	db := bytecodeDBPostgres(t)
	var kinds []string
	var mu sync.Mutex
	w, compiler := bytecodeDBIntegrationWorker(t, db, func(response http.ResponseWriter, request *http.Request) {
		var search struct {
			Kind string `json:"bytecodeType"`
		}
		if err := json.NewDecoder(request.Body).Decode(&search); err != nil {
			t.Error(err)
		}
		mu.Lock()
		kinds = append(kinds, search.Kind)
		mu.Unlock()
		sources := []BytecodeDBSource{}
		if search.Kind == "CREATION_INPUT" {
			source := bytecodeDBSourceFixture()
			source.SourceType = "YUL"
			source.SourceFiles["A.sol"] = `object "A" { code { stop() } }`
			sources = append(sources, source)
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"sources": sources})
	})
	address := "0x" + strings.Repeat("11", 20)
	otherAddress := "0x" + strings.Repeat("22", 20)
	bytecodeDBSeedGenesis(t, db, address, otherAddress)
	// Same runtime at both addresses is not authenticated Genesis evidence.
	bytecodeDBExec(t, db, `DELETE FROM genesis_account_observations`)
	if err := w.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := db.QueryRow(t.Context(), `SELECT outcome FROM bytecode_db_targets WHERE address=$1`, common.HexToAddress(address).Bytes()).Scan(&state); err != nil || state != "waiting_facts" {
		t.Fatalf("missing creation state=%s err=%v", state, err)
	}
	hash := sha256.Sum256([]byte("genesis"))
	txHash := sha256.Sum256([]byte("creation"))
	bytecodeDBExec(t, db, `INSERT INTO transactions(chain_id,hash,raw) VALUES(1,$1,'{}')`, txHash[:])
	bytecodeDBExec(t, db, `INSERT INTO transaction_inclusions(chain_id,block_number,block_hash,tx_index,tx_hash,raw) VALUES(1,0,$1,0,$2,'{"input":"0x6001"}')`, hash[:], txHash[:])
	receipt, _ := json.Marshal(map[string]string{"contractAddress": address, "status": "0x1"})
	bytecodeDBExec(t, db, `INSERT INTO receipts(chain_id,block_number,block_hash,tx_index,tx_hash,raw) VALUES(1,0,$1,0,$2,$3)`, hash[:], txHash[:], receipt)
	for range 3 {
		bytecodeDBExec(t, db, `UPDATE bytecode_db_targets SET next_attempt_at=now() WHERE address=$1`, common.HexToAddress(address).Bytes())
		bytecodeDBExec(t, db, `UPDATE bytecode_db_targets SET next_attempt_at=now()+interval '1 hour' WHERE address=$1`, common.HexToAddress(otherAddress).Bytes())
		bytecodeDBExec(t, db, `UPDATE bytecode_db_services SET next_request_at=now()`)
		if err := w.tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	local, err := NewWorker(w.repository, compiler, WorkerOptions{WorkerID: "local", LeaseDuration: time.Minute, PollInterval: time.Second, MaxOutputBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if found, err := local.ProcessOne(t.Context()); err != nil || !found {
		t.Fatalf("local Yul verification found=%t err=%v", found, err)
	}
	artifact, found, err := w.repository.VerifiedContract(t.Context(), 1, address)
	if err != nil || !found || artifact.VerificationOrigin != VerificationOriginBytecodeDB {
		t.Fatalf("creation fallback publication found=%t err=%v", found, err)
	}
	if _, err := ResolveTarget(t.Context(), db, 1, otherAddress, 5<<20); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatalf("creation evidence transferred to other address: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(kinds, ",") != "DEPLOYED_BYTECODE,CREATION_INPUT" || compiler.compileCalls != 2 {
		t.Fatalf("search order=%v compilation calls=%d", kinds, compiler.compileCalls)
	}
}

func TestBytecodeDBTransientFailuresBackOffAndCoolDown(t *testing.T) {
	db := bytecodeDBPostgres(t)
	w, _ := bytecodeDBIntegrationWorker(t, db, func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	})
	code := []byte{0x60, 1}
	digest := sha256.Sum256(code)
	q := dbgen.New(db)
	if err := q.BytecodeDBEnqueueLookup(t.Context(), dbgen.BytecodeDBEnqueueLookupParams{ServiceID: w.service, BytecodeType: "DEPLOYED_BYTECODE", Digest: digest[:], Bytecode: code}); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		lookup, found, err := w.claimLookup(t.Context())
		if err != nil || !found {
			t.Fatalf("claim %d found=%t err=%v", attempt, found, err)
		}
		if err = w.search(t.Context(), lookup); err != nil {
			t.Fatal(err)
		}
		var failures int
		var requested bool
		var remaining float64
		if err = db.QueryRow(t.Context(), `SELECT failures,requested,extract(epoch FROM next_attempt_at-clock_timestamp())::float8 FROM bytecode_db_lookups`).Scan(&failures, &requested, &remaining); err != nil {
			t.Fatal(err)
		}
		if attempt < 3 {
			if failures != attempt || !requested || remaining <= 0 || remaining > float64(int(1)<<attempt) {
				t.Fatalf("retry %d: failures=%d requested=%t delay=%f", attempt, failures, requested, remaining)
			}
			bytecodeDBExec(t, db, `UPDATE bytecode_db_lookups SET next_attempt_at=now()`)
			bytecodeDBExec(t, db, `UPDATE bytecode_db_services SET next_request_at=now()`)
		} else if failures != 0 || requested || remaining < 23*60*60 {
			t.Fatalf("third failure did not cool down: failures=%d requested=%t delay=%f", failures, requested, remaining)
		}
	}
}

func TestBytecodeDBTargetClaimsRemainChainScoped(t *testing.T) {
	db := bytecodeDBPostgres(t)
	w, _ := bytecodeDBIntegrationWorker(t, db, func(http.ResponseWriter, *http.Request) {})
	address := "0x" + strings.Repeat("11", 20)
	bytecodeDBSeedGenesis(t, db, address)
	bytecodeDBExec(t, db, `INSERT INTO chains(chain_id,genesis_hash) VALUES(2,$1)`, sha256.New().Sum(nil))
	var otherChain pgtype.Numeric
	if err := otherChain.Scan("2"); err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(db)
	if err := q.BytecodeDBEnqueueTarget(t.Context(), w.service, otherChain, common.HexToAddress(address).Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := q.BytecodeDBClaimTarget(t.Context(), new("worker"), w.service, w.chain); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claimed another chain's target: %v", err)
	}
	if count, err := q.BytecodeDBBacklog(t.Context(), w.service, w.chain); err != nil || count != 0 {
		t.Fatalf("other chain in backlog: %d %v", count, err)
	}
}

func TestBytecodeDBDiscoveryCursorsAreIndependentAcrossChains(t *testing.T) {
	db := bytecodeDBPostgres(t)
	w, _ := bytecodeDBIntegrationWorker(t, db, func(http.ResponseWriter, *http.Request) {})
	bytecodeDBSeedGenesis(t, db, "0x"+strings.Repeat("11", 20))
	bytecodeDBExec(t, db, `INSERT INTO chains(chain_id,genesis_hash) SELECT 2,genesis_hash FROM chains WHERE chain_id=1`)
	bytecodeDBExec(t, db, `INSERT INTO blocks(chain_id,number,hash,parent_hash,timestamp,raw) SELECT 2,number,hash,parent_hash,timestamp,raw FROM blocks WHERE chain_id=1`)
	bytecodeDBExec(t, db, `INSERT INTO canonical_blocks(chain_id,number,block_hash) SELECT 2,number,block_hash FROM canonical_blocks WHERE chain_id=1`)
	bytecodeDBExec(t, db, `INSERT INTO contract_code_observations(chain_id,address,block_number,block_hash,code_hash,code,canonical) SELECT 2,address,block_number,block_hash,code_hash,code,canonical FROM contract_code_observations WHERE chain_id=1`)
	other := *w
	other.options.ChainID = 2
	if err := other.chain.Scan("2"); err != nil {
		t.Fatal(err)
	}
	for _, worker := range []*BytecodeDBWorker{w, &other} {
		if err := worker.discover(t.Context()); err != nil {
			t.Fatal(err)
		}
		if count, err := dbgen.New(db).BytecodeDBBacklog(t.Context(), worker.service, worker.chain); err != nil || count != 1 {
			t.Fatalf("chain %d discovery blocked by peer cursor: count=%d err=%v", worker.options.ChainID, count, err)
		}
	}
}
