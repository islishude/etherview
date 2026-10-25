//go:build integration

package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSourcifyImportPostgresLocalPublication(t *testing.T) {
	for _, scenario := range []string{"match", "compiler mismatch", "reorg"} {
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
			request, err := client.ImportV2(t.Context(), 11155111, target)
			if err != nil {
				t.Fatal(err)
			}
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
			worker, err := NewWorker(repository, compiler, WorkerOptions{WorkerID: "sourcify-import"})
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
			if scenario == "match" {
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
