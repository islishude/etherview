//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
	"strings"
	"testing"
	"time"
)

func TestWasmMigrationRequiresDrainAndPreservesHistory(t *testing.T) {
	db := newIsolatedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	applyMigrationsThrough(t, ctx, db, "0065_vyper_verification")
	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`)
	submission := verify.SubmissionV2{Kind: verify.JobSolidityStandardJSON, Language: verify.LanguageSolidity, CompilerVersion: "0.8.30+commit.73712a01", StandardJSON: input, StandardJSONVariants: []json.RawMessage{input}, Bytecodes: []verify.BytecodePair{{Runtime: "0x6001"}}}
	job, _, err := repository.SubmitV2(ctx, submission)
	if err != nil {
		t.Fatal(err)
	}
	lease, found, err := repository.Claim(ctx, "old-executor", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim: %t %v", found, err)
	}
	execFixture(t, ctx, db, `UPDATE verification_jobs SET compiler_platform='emscripten-wasm32', catalog_generation_id=$2, compiler_digest=$3, executor_kind='node_solcjs_v1', execution_policy='trusted_subprocess', executor_digest=$4 WHERE id=$1::uuid`, job.ID, generation, compilerDigest[:], executorDigest[:])
	if err = store.RunMigrations(ctx, db); err == nil || !strings.Contains(err.Error(), "drain old compiler-bound jobs") {
		t.Fatalf("migration did not require drain: %v", err)
	}
	if err = repository.Fail(ctx, lease, verify.ErrorCompileFailed); err != nil {
		t.Fatal(err)
	}
	if err = store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	previous, found, err := repository.Job(ctx, job.ID)
	if err != nil || !found {
		t.Fatalf("history: %t %v", found, err)
	}
	if previous.Compiler == nil || previous.Compiler.ExecutorKind != verify.SolcJSExecutorKind || previous.Compiler.ExecutorDigest != executorDigest || previous.Status != verify.JobFailed {
		t.Fatal("migration changed terminal identity")
	}
	// A new request can bind only the new runtime; old identities cannot be reused.
	submission.Bytecodes[0].Runtime = "0x6002"
	_, _, err = repository.SubmitV2(ctx, submission)
	if err != nil {
		t.Fatal(err)
	}
	next, found, err := repository.Claim(ctx, "new-executor", time.Minute)
	if err != nil || !found {
		t.Fatalf("new claim: %t %v", found, err)
	}
	legacy := solcJSProvenance(generation, compilerDigest, executorDigest)
	legacy.Kind = verify.CompilerSolcJS
	legacy.ExecutorKind = verify.SolcJSExecutorKind
	legacy.ExecutionPolicy = verify.TrustedSubprocessPolicy
	if err = repository.BindCompiler(ctx, next, legacy); err == nil {
		t.Fatal("accepted new legacy binding")
	}
	current := solcJSProvenance(generation, compilerDigest, executorDigest)
	if err = repository.BindCompiler(ctx, next, current); err != nil {
		t.Fatal(err)
	}
}
