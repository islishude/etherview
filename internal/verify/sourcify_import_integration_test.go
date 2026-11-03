//go:build integration

package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSourcifyImportPostgresLocalPublication(t *testing.T) {
	for _, scenario := range []string{"match", "restart", "catalog wait", "compiler mismatch", "reorg"} {
		t.Run(scenario, func(t *testing.T) {
			db := bytecodeDBPostgres(t)
			address := "0x" + strings.Repeat("11", 20)
			bytecodeDBSeedGenesis(t, db, address)
			block := sha256.Sum256([]byte("genesis"))
			target := VerificationTarget{ChainID: 1, Address: address, AtBlockHash: "0x" + hex.EncodeToString(block[:]), CodeHash: "0x" + hex.EncodeToString(keccak256Bytes([]byte{0x60, 2})), RuntimeBytecode: "0x6002", GenesisPredeploy: true}
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/v2/contract/11155111/"+address {
					t.Error("import must never upload sources")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(string(sourcifyContractFixture(address)), `"chainId":"1"`, `"chainId":"11155111"`), "0x6001", "0x6002")))
			}))
			defer server.Close()
			client, err := newSourcifyClient(SourcifyOptions{BaseURL: server.URL}, server.Client(), nil, true, true)
			if err != nil {
				t.Fatal(err)
			}
			request := SubmissionV2{Kind: JobSourcifyImport, SourceChainID: 11155111, Target: &target}
			repository, err := NewPostgresRepository(db, RepositoryOptions{})
			if err != nil {
				t.Fatal(err)
			}
			service, _ := NewService(repository, 5<<20)
			job, created, err := service.SubmitV2(t.Context(), request)
			if err != nil || !created {
				t.Fatalf("enqueue created=%t err=%v", created, err)
			}
			if _, found, err := repository.VerifiedContract(t.Context(), 1, address); err != nil || found {
				t.Fatal("external match published without local compilation")
			}
			p := testSolcJSProvenance()
			digest := sha256.Sum256([]byte("sourcify-import-catalog"))
			bytecodeDBExec(t, db, `INSERT INTO compiler_catalog_generations(language,source_url,catalog_digest,entry_count) VALUES('solidity','https://example.com/list.json',$1,1)`, digest[:])
			bytecodeDBExec(t, db, `INSERT INTO compiler_catalog_entries(generation_id,language,version,platform,artifact_url,artifact_sha256,max_bytes) VALUES(1,'solidity','0.8.30','emscripten-wasm32','https://example.com/soljson.js',$1,209715200)`, p.Digest[:])
			bytecodeDBExec(t, db, `INSERT INTO compiler_catalog_heads(language,generation_id) VALUES('solidity',1)`)
			runtime := []byte{0x60, 2}
			if scenario == "compiler mismatch" {
				runtime = []byte{0x60, 3}
			}
			compiler := &verifyTestCompiler{provenance: p, output: candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{"A.sol": {"A": {creation: []byte{0x60, 1}, runtime: runtime}}})}
			if scenario == "reorg" {
				bytecodeDBExec(t, db, `DELETE FROM canonical_blocks WHERE chain_id=1 AND number=0`)
			}
			if scenario == "catalog wait" {
				waiting, err := NewWorker(repository, unavailableImportCompiler{compiler}, WorkerOptions{WorkerID: "import-before-catalog", Importer: client})
				if err != nil {
					t.Fatal(err)
				}
				if found, err := waiting.ProcessOne(t.Context()); err != nil || !found {
					t.Fatalf("source stage: %v", err)
				}
				saved, found, err := repository.Job(t.Context(), job.ID)
				if err != nil || !found || saved.Status != JobQueued || saved.AttemptCount != 0 || saved.RequestV2.Kind != JobAddress {
					t.Fatalf("catalog wait did not preserve input: %+v %v", saved, err)
				}
				server.Close()
			}
			if scenario == "restart" {
				lease, found, err := repository.Claim(t.Context(), "first-instance", time.Minute)
				if err != nil || !found {
					t.Fatalf("claim: %v", err)
				}
				input, err := client.ImportV2(t.Context(), 11155111, target)
				if err != nil {
					t.Fatal(err)
				}
				assertImmutableImportRace(t, repository, lease, input)
				bytecodeDBExec(t, db, `UPDATE verification_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1::uuid`, job.ID)
				if _, err := repository.SaveImportInput(t.Context(), lease, input); !errors.Is(err, ErrLeaseLost) {
					t.Fatalf("stale source writer: %v", err)
				}
				// The replacement needs no remote service; the first input is durable.
				server.Close()
			}
			worker, err := NewWorker(repository, compiler, WorkerOptions{WorkerID: "sourcify-import", Importer: client})
			if err != nil {
				t.Fatal(err)
			}
			if found, err := worker.ProcessOne(t.Context()); err != nil || !found {
				t.Fatalf("process found=%t err=%v", found, err)
			}
			result, found, err := repository.Job(t.Context(), job.ID)
			if err != nil || !found {
				t.Fatal("durable local result missing")
			}
			var published int
			if err := db.QueryRow(t.Context(), `SELECT count(*) FROM verified_contracts`).Scan(&published); err != nil {
				t.Fatal(err)
			}
			if scenario == "match" || scenario == "restart" || scenario == "catalog wait" {
				artifact, found, err := repository.VerifiedContract(t.Context(), 1, address)
				if err != nil || !found || published != 1 || result.Status != JobSucceeded || len(artifact.Sources) == 0 || len(artifact.ABI) == 0 || compiler.compileCalls != 2 {
					t.Fatalf("local publication missing: status=%s found=%t compiled=%d err=%v", result.Status, found, compiler.compileCalls, err)
				}
			} else if published != 0 {
				t.Fatal("mismatch or orphan target published locally")
			}
			if calls != 1 {
				t.Fatal("worker repeated external retrieval instead of using durable sources")
			}
		})
	}
}

type unavailableImportCompiler struct{ *verifyTestCompiler }

func (unavailableImportCompiler) Availability(context.Context) CompilerAvailability {
	return CompilerAvailability{}
}

// Competing deliveries may observe different upstream contents. Only the first
// normalized input is retained; neither the request identity nor its digest moves.
func assertImmutableImportRace(t *testing.T, repository *PostgresRepository, lease VerificationLease, input SubmissionV2) {
	t.Helper()
	var originalPayload, originalDigest []byte
	if err := repository.db.QueryRow(t.Context(), `SELECT request_payload, request_digest FROM verification_jobs WHERE id=$1::uuid`, lease.Job.ID).Scan(&originalPayload, &originalDigest); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan []byte, 2)
	for _, name := range []string{"first", "changed remote source"} {
		candidate := input
		var standard map[string]any
		if err := json.Unmarshal(input.StandardJSON, &standard); err != nil {
			t.Fatal(err)
		}
		standard["sources"] = map[string]any{"A.sol": map[string]string{"content": "contract A {} // " + name}}
		candidate.StandardJSON, _ = json.Marshal(standard)
		wg.Go(func() {
			saved, err := repository.SaveImportInput(t.Context(), lease, candidate)
			if err != nil {
				t.Error(err)
				return
			}
			results <- saved.RequestV2.StandardJSON
		})
	}
	wg.Wait()
	close(results)
	if len(results) != 2 || !bytes.Equal(<-results, <-results) {
		t.Fatal("competing import inputs were not immutable")
	}
	var payload, digest []byte
	if err := repository.db.QueryRow(t.Context(), `SELECT request_payload, request_digest FROM verification_jobs WHERE id=$1::uuid`, lease.Job.ID).Scan(&payload, &digest); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, originalPayload) || !bytes.Equal(digest, originalDigest) {
		t.Fatal("source import changed the original request identity")
	}
}
