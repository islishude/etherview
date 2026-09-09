//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

type vyperFixtureCompiler struct {
	first, second []byte
	after         func()
}

func (*vyperFixtureCompiler) Provenance(verify.Language, string) (verify.CompilerProvenance, error) {
	digest, _ := hex.DecodeString(verify.VyperCompilerSHA256)
	p := verify.CompilerProvenance{Kind: verify.CompilerVyper, Platform: verify.CompilerPlatformPythonWheel, ExecutorKind: verify.WasmExecutorKind, ExecutionPolicy: verify.WasmSubprocessPolicy, ExecutorDigest: sha256.Sum256([]byte("fixture-vyper-runtime"))}
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
			compiler := &vyperFixtureCompiler{first: first, second: second}
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
