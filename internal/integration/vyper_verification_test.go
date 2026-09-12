//go:build integration

package integration_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

type vyperFixtureCompiler struct {
	first, second []byte
	generation    int64
	after         func()
}

func (compiler *vyperFixtureCompiler) Provenance(verify.Language, string) (verify.CompilerProvenance, error) {
	digest, _ := hex.DecodeString(verify.VyperCompilerSHA256)
	p := verify.CompilerProvenance{Kind: verify.CompilerVyper, Platform: verify.CompilerPlatformPythonWheel, ExecutorKind: verify.VyperDynamicExecutorKind, CatalogGeneration: compiler.generation, ExecutionPolicy: verify.TrustedSubprocessPolicy, ExecutorDigest: sha256.Sum256([]byte("fixture-vyper-runtime"))}
	copy(p.Digest[:], digest)
	return p, nil
}
func (compiler *vyperFixtureCompiler) Compile(_ context.Context, _ verify.Language, _ string, input []byte) ([]byte, error) {
	var doc struct {
		Sources map[string]struct {
			Content string `json:"content"`
		} `json:"sources"`
	}
	_ = json.Unmarshal(input, &doc)
	if compiler.after != nil {
		compiler.after()
		compiler.after = nil
	}
	if strings.HasSuffix(doc.Sources["A.vy"].Content, " ") {
		return compiler.second, nil
	}
	return compiler.first, nil
}

func TestVyperDurablePublicationAndReorgFence(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "publish", true: "stale"}[stale], func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			core, err := store.NewPostgresRepository(db)
			if err != nil {
				t.Fatal(err)
			}
			commitCanonical(t, ctx, core, testBundle(0, testHash(77000), testHash(0), testHash(77010), "vyper-genesis"))
			commitCanonical(t, ctx, core, testBundle(1, testHash(77001), testHash(77000), testHash(77011), "vyper-block"))
			first, err := os.ReadFile("../verify/testdata/compiler/vyper/immutable.input.output.json")
			if err != nil {
				t.Fatal(err)
			}
			second, err := os.ReadFile("../verify/testdata/compiler/vyper/immutable.modified.output.json")
			if err != nil {
				t.Fatal(err)
			}
			input, err := os.ReadFile("../verify/testdata/compiler/vyper/immutable.input.json")
			if err != nil {
				t.Fatal(err)
			}
			candidates, err := verify.ExtractCandidatesV2(first, second, verify.LanguageVyper, verify.VyperCompilerVersion)
			if err != nil {
				t.Fatal(err)
			}
			candidate := candidates[0]
			word := strings.Repeat("00", 12) + strings.Repeat("11", 20)
			runtimeHex := candidate.RuntimeBytecode[:len(candidate.RuntimeBytecode)-64] + word
			runtime, err := hex.DecodeString(strings.TrimPrefix(runtimeHex, "0x"))
			if err != nil {
				t.Fatal(err)
			}
			codeHash := keccak256(runtime)
			address := mustBytes(t, testAddress(770))
			blockHash := mustBytes(t, testHash(77001))
			execFixture(t, ctx, db, `INSERT INTO contract_code_observations(chain_id,address,block_number,block_hash,code_hash,code,canonical) VALUES(1,$1,1,$2,$3,$4,TRUE)`, address, blockHash, codeHash, runtime)
			repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20})
			if err != nil {
				t.Fatal(err)
			}
			service, err := verify.NewService(repository, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			request := verify.SubmissionV2{Kind: verify.JobAddress, Language: verify.LanguageVyper, CompilerVersion: verify.VyperCompilerVersion, TargetFile: "A.vy", StandardJSON: input, Bytecodes: []verify.BytecodePair{{Creation: candidate.CreationBytecode + word, Runtime: runtimeHex}}, Target: &verify.VerificationTarget{ChainID: 1, Address: testAddress(770).Hex(), CodeHash: "0x" + hex.EncodeToString(codeHash), AtBlockHash: testHash(77001).Hex(), CreationBytecode: candidate.CreationBytecode + word, RuntimeBytecode: runtimeHex}}
			job, created, err := service.SubmitV2(ctx, request)
			if err != nil || !created {
				t.Fatalf("submit created=%v err=%v", created, err)
			}
			if _, found, err := repository.ClaimRunnable(ctx, "unavailable", time.Minute, verify.CompilerAvailability{SolcJS: true, Geas: true}); err != nil || found {
				t.Fatalf("wrong family claimed job: %v", err)
			}
			compiler := &vyperFixtureCompiler{first: first, second: second, generation: seedVyperRuntime(t, db)}
			if stale {
				compiler.after = func() {
					execFixture(t, ctx, db, `UPDATE contract_code_observations SET canonical=FALSE WHERE address=$1`, address)
				}
			}
			worker, err := verify.NewWorker(repository, compiler, verify.WorkerOptions{WorkerID: "vyper-integration"})
			if err != nil {
				t.Fatal(err)
			}
			if found, err := worker.ProcessOne(ctx); err != nil || !found {
				t.Fatalf("process found=%v err=%v", found, err)
			}
			result, found, err := repository.Job(ctx, job.ID)
			if err != nil || !found {
				t.Fatal(err)
			}
			if stale {
				if result.Status == verify.JobSucceeded {
					t.Fatalf("stale job=%+v", result)
				}
				assertRowCount(t, ctx, db, `SELECT count(*) FROM verified_contracts`, 0)
				assertRowCount(t, ctx, db, `SELECT count(*) FROM verification_results`, 0)
				return
			}
			if result.Status != verify.JobSucceeded || result.Compiler == nil || result.Compiler.Kind != verify.CompilerVyper {
				t.Fatalf("job=%+v", result)
			}
			assertRowCount(t, ctx, db, `SELECT count(*) FROM verified_contracts WHERE language='vyper' AND match_type='partial'`, 1)
			assertRowCount(t, ctx, db, `SELECT count(*) FROM verification_compilation_units`, 0)
			if _, err := db.ExecContext(ctx, `UPDATE verification_jobs SET executor_digest=$1 WHERE id=$2`, make([]byte, 32), job.ID); err == nil {
				t.Fatal("bound provenance was mutable")
			}
		})
	}
}

func seedVyperRuntime(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var generation int64
	digest := sha256.Sum256([]byte("fixture-vyper-catalog"))
	if err := db.QueryRow(`INSERT INTO compiler_catalog_generations (language, source_url, catalog_digest, entry_count) VALUES ('vyper', 'https://compilers.example/catalog.json', $1, 1) RETURNING id`, digest[:]).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	executor := sha256.Sum256([]byte("fixture-vyper-runtime"))
	artifacts, _ := json.Marshal([]map[string]any{{"platform": runtime.GOOS + "-" + runtime.GOARCH, "manifest_sha256": hex.EncodeToString(executor[:]), "protocol": "etherview-vyper-runtime-v3"}})
	if _, err := db.Exec(`INSERT INTO compiler_catalog_entries (generation_id, language, version, platform, artifact_url, artifact_sha256, max_bytes, vyper_runtimes) VALUES ($1, 'vyper', '0.4.3', 'python-wheel', 'https://compilers.example/catalog.json', decode($2,'hex'), 1024, $3)`, generation, verify.VyperCompilerSHA256, artifacts); err != nil {
		t.Fatal(err)
	}
	return generation
}

func TestVyperCatalogExpiryAndGenerationRetention(t *testing.T) {
	db := newMigratedPostgres(t)
	generation := seedVyperRuntime(t, db)
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `UPDATE compiler_catalog_entries SET expires_at=now()+interval '30 minutes' WHERE generation_id=$1`, generation); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO compiler_catalog_heads(language,generation_id) VALUES ('vyper',$1)`, generation); err != nil {
		t.Fatal(err)
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := verify.NewCompilerCatalog(db, verify.CompilerCatalogOptions{VyperPublicKey: public, Sources: map[verify.Language]string{verify.LanguageSolidity: "https://compilers.example/emscripten-wasm32/list.json", verify.LanguageVyper: "https://compilers.example/catalog.json"}, AllowedOrigins: []string{"https://compilers.example"}, Freshness: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := catalog.Lookup(ctx, verify.LanguageVyper, "0.4.3")
	if err != nil || entry.GenerationID != generation {
		t.Fatalf("fresh short-lived signed generation: %v", err)
	}
	versions, err := catalog.Versions(ctx, verify.LanguageVyper)
	if err != nil || len(versions) != 1 || versions[0] != "0.4.3" {
		t.Fatalf("versions=%v error=%v", versions, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE compiler_catalog_entries SET expires_at=now()-interval '1 second' WHERE generation_id=$1`, generation); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Lookup(ctx, verify.LanguageVyper, "0.4.3"); !errors.Is(err, verify.ErrCompilerCatalogStale) {
		t.Fatalf("expired entry: %v", err)
	}
	if _, err := catalog.Versions(ctx, verify.LanguageVyper); !errors.Is(err, verify.ErrCompilerCatalogStale) {
		t.Fatalf("expired versions: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM compiler_catalog_entries WHERE generation_id=$1`, generation).Scan(&count); err != nil || count != 1 {
		t.Fatalf("expired generation removed: count=%d error=%v", count, err)
	}
}

func TestVyperBoundRetryDoesNotRequireFreshCatalog(t *testing.T) {
	db := newMigratedPostgres(t)
	generation := seedVyperRuntime(t, db)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	for _, source := range []string{"first", "second"} {
		input, _ := json.Marshal(map[string]any{"language": "Vyper", "sources": map[string]any{"A.vy": map[string]string{"content": source}}, "settings": map[string]any{}})
		_, _, err := repository.SubmitV2(ctx, verify.SubmissionV2{Kind: verify.JobVyperStandardJSON, Language: verify.LanguageVyper, CompilerVersion: "0.4.3", TargetFile: "A.vy", StandardJSON: input, Bytecodes: []verify.BytecodePair{{Runtime: "0x6000"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	unbound, ok, err := repository.Claim(ctx, "unbound", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim unbound: %v", err)
	}
	bound, ok, err := repository.Claim(ctx, "bound", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim bound: %v", err)
	}
	compiler := &vyperFixtureCompiler{generation: generation}
	provenance, err := compiler.Provenance(verify.LanguageVyper, "0.4.3")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.BindCompiler(ctx, bound, provenance); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE verification_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id IN ($1::uuid,$2::uuid)`, unbound.Job.ID, bound.Job.ID); err != nil {
		t.Fatal(err)
	}
	retry, ok, err := repository.ClaimRunnable(ctx, "offline-retry", time.Minute, verify.CompilerAvailability{VyperBound: true})
	if err != nil || !ok || retry.Job.ID != bound.Job.ID || retry.Job.Compiler == nil || retry.Job.Compiler.CatalogGeneration != generation {
		t.Fatalf("bound retry=%+v error=%v", retry.Job, err)
	}
	if err := repository.BindCompiler(ctx, retry, provenance); err != nil {
		t.Fatal(err)
	}
	changed := provenance
	changed.ExecutorDigest[0] ^= 1
	if err := repository.BindCompiler(ctx, retry, changed); !errors.Is(err, verify.ErrCompilerProvenanceConflict) {
		t.Fatalf("runtime rebind: %v", err)
	}
}
