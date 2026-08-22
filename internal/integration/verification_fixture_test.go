//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/verifiedselector"
)

// insertVerifiedContractFixture creates a coherent verifier-v2 job, immutable
// result, and publication. Reader tests use the same provenance and source
// trigger boundary as production instead of inserting an orphan projection.
func insertVerifiedContractFixture(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	address, codeHash []byte,
	validFrom uint64,
	validTo *uint64,
	compilerVersion, contractName, abi, sources, settings string,
) {
	t.Helper()
	insertVerifiedContractFixtureWithCompilationArtifacts(
		t, ctx, db, address, codeHash, validFrom, validTo,
		compilerVersion, contractName, abi, sources, settings, `{}`,
	)
}

func insertVerifiedContractFixtureWithCompilationArtifacts(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	address, codeHash []byte,
	validFrom uint64,
	validTo *uint64,
	compilerVersion, contractName, abi, sources, settings, compilationArtifacts string,
) {
	t.Helper()
	jobID := uuid.NewString()
	blockHash := sha256.Sum256([]byte("etherview:verification-fixture:block:" + jobID))
	compilerDigest := sha256.Sum256([]byte("etherview:verification-fixture:compiler"))
	executorDigest := sha256.Sum256([]byte("etherview:verification-fixture:solcjs-executor"))
	catalogDigest := sha256.Sum256([]byte("etherview:verification-fixture:catalog"))
	fileName := "Fixture.sol"
	request := map[string]any{
		"kind":                      "address",
		"language":                  "solidity",
		"solidity_analysis_version": 1,
		"compiler_version":          compilerVersion,
		"standard_json":             json.RawMessage(`{"language":"Solidity","sources":{"Fixture.sol":{"content":"contract Fixture {}"}},"settings":{}}`),
		"standard_json_variants":    []json.RawMessage{json.RawMessage(`{"language":"Solidity","sources":{"Fixture.sol":{"content":"contract Fixture {}"}},"settings":{}}`)},
		"bytecodes":                 []map[string]string{{"runtime_bytecode": "0x00"}},
		"target": map[string]any{
			"chain_id": 1, "address": "0x" + hex.EncodeToString(address),
			"code_hash":        "0x" + hex.EncodeToString(codeHash),
			"at_block_hash":    "0x" + hex.EncodeToString(blockHash[:]),
			"runtime_bytecode": "0x00",
		},
	}
	requestPayload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal verifier-v2 fixture request: %v", err)
	}
	requestDigest := sha256.Sum256(append([]byte("etherview:verification-request:v2\x00"), requestPayload...))
	var sourceObject, settingsObject any
	if err := json.Unmarshal([]byte(sources), &sourceObject); err != nil {
		t.Fatalf("decode fixture sources: %v", err)
	}
	if err := json.Unmarshal([]byte(settings), &settingsObject); err != nil {
		t.Fatalf("decode fixture settings: %v", err)
	}
	var compilationObject any
	if err := json.Unmarshal([]byte(compilationArtifacts), &compilationObject); err != nil {
		t.Fatalf("decode fixture compilation artifacts: %v", err)
	}
	var abiValue any
	if err := json.Unmarshal([]byte(abi), &abiValue); err != nil {
		t.Fatalf("decode fixture ABI: %v", err)
	}
	outcomeObject := map[string]any{
		"kind": "verification_success", "file_name": fileName,
		"contract_name": contractName, "language": "solidity",
		"compiler_version": compilerVersion, "abi": abiValue,
		"sources": sourceObject, "settings": settingsObject,
		"compilation_artifacts":   compilationObject,
		"creation_code_artifacts": map[string]any{},
		"runtime_code_artifacts":  map[string]any{},
		"runtime_match": map[string]any{
			"match_type": "full", "transformations": []any{}, "values": map[string]any{},
		},
		"libraries": map[string]any{}, "is_blueprint": false,
	}
	outcome, err := json.Marshal(outcomeObject)
	if err != nil {
		t.Fatalf("marshal verifier-v2 fixture outcome: %v", err)
	}
	var validToValue any
	if validTo != nil {
		validToValue = int64(*validTo)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin verified-contract fixture: %v", err)
	}
	defer tx.Rollback() //nolint:errcheck
	var generationID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO compiler_catalog_generations (
			language, source_url, catalog_digest, entry_count
		) VALUES ('solidity', 'https://compiler.example/list.json', $1, 1)
		ON CONFLICT (language, catalog_digest) DO UPDATE
		SET source_url = compiler_catalog_generations.source_url
		RETURNING id`, catalogDigest[:]).Scan(&generationID); err != nil {
		t.Fatalf("insert fixture compiler generation: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO compiler_catalog_entries (
			generation_id, language, version, platform, artifact_url, artifact_sha256, max_bytes
		) VALUES ($1, 'solidity', $2, 'emscripten-wasm32', 'https://compiler.example/soljson.js', $3, 209715200)
		ON CONFLICT (generation_id, version) DO NOTHING`,
		generationID, compilerVersion, compilerDigest[:],
	); err != nil {
		t.Fatalf("insert fixture compiler entry: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verification_jobs (
			id, kind, language, catalog_language, compiler_version,
			chain_id, address, code_hash, block_hash, request, request_payload,
			request_digest, status, leased_by, lease_token, lease_expires_at,
			attempt_count, max_attempts
		) VALUES (
			$1::uuid, 'address', 'solidity', 'solidity', $2,
			1, $3, $4, $5, $6::jsonb, $7, $8, 'running',
			'fixture-worker', 'fixture-lease', clock_timestamp() + interval '1 hour',
			1, 3
		)`, jobID, compilerVersion, address, codeHash, blockHash[:],
		string(requestPayload), requestPayload, requestDigest[:],
	); err != nil {
		t.Fatalf("insert verifier-v2 fixture job: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE verification_jobs
		SET compiler_platform = 'emscripten-wasm32',
		    catalog_generation_id = $2,
		    compiler_digest = $3,
		    executor_kind = 'node_solcjs_v1',
		    execution_policy = 'trusted_subprocess',
		    executor_digest = $4
		WHERE id = $1::uuid`,
		jobID, generationID, compilerDigest[:], executorDigest[:],
	); err != nil {
		t.Fatalf("bind verifier-v2 fixture compiler: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verification_results (
			job_id, request_digest, outcome_kind, outcome, file_name,
			contract_name, language, compiler_version, match_type, abi, sources,
			settings, compilation_artifacts, creation_code_artifacts,
			runtime_code_artifacts, libraries, is_blueprint
		) VALUES (
			$1::uuid, $2, 'verification_success', $3::jsonb, $4, $5,
			'solidity', $6, 'full', $7::jsonb, $8::jsonb, $9::jsonb,
			$10::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb, FALSE
		)`, jobID, requestDigest[:], string(outcome), fileName, contractName,
		compilerVersion, abi, sources, settings, compilationArtifacts,
	); err != nil {
		t.Fatalf("insert verifier-v2 fixture result: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verified_contracts (
			chain_id, address, code_hash, valid_from_block, valid_to_block,
			verification_job_id, request_digest, file_name, contract_name,
			language, compiler_version, match_type, abi, sources, settings,
			compilation_artifacts, creation_code_artifacts,
			runtime_code_artifacts, libraries, is_blueprint
		) VALUES (
			1, $1, $2, $3, $4, $5::uuid, $6, $7, $8, 'solidity', $9,
			'full', $10::jsonb, $11::jsonb, $12::jsonb,
			$13::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb, FALSE
		)`, address, codeHash, int64(validFrom), validToValue, jobID,
		requestDigest[:], fileName, contractName, compilerVersion, abi, sources, settings,
		compilationArtifacts,
	); err != nil {
		t.Fatalf("insert sourced verified-contract fixture: %v", err)
	}
	if err := verifiedselector.Persist(ctx, tx, verifiedselector.Identity{
		JobID: jobID, RequestDigest: requestDigest[:], ChainID: "1",
		Address: address, CodeHash: codeHash, ValidFromBlock: validFrom,
	}, []byte(abi)); err != nil {
		t.Fatalf("insert verified selector fixture: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE verification_jobs
		SET status = 'succeeded', leased_by = NULL, lease_token = NULL,
		    lease_expires_at = NULL, outcome_kind = 'verification_success',
		    outcome = $2::jsonb
		WHERE id = $1::uuid`,
		jobID, string(outcome),
	); err != nil {
		t.Fatalf("complete verifier-v2 fixture job: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit verified-contract fixture: %v", err)
	}
}
