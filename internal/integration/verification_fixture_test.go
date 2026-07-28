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
	jobID := uuid.NewString()
	blockHash := sha256.Sum256([]byte("etherview:verification-fixture:block:" + jobID))
	compilerDigest := sha256.Sum256([]byte("etherview:verification-fixture:compiler"))
	runnerDigest := sha256.Sum256([]byte("etherview:verification-fixture:runner"))
	catalogDigest := sha256.Sum256([]byte("etherview:verification-fixture:catalog"))
	fileName := "Fixture.sol"
	request := map[string]any{
		"kind":                   "address",
		"language":               "solidity",
		"compiler_version":       compilerVersion,
		"standard_json":          json.RawMessage(`{"language":"Solidity","sources":{"Fixture.sol":{"content":"contract Fixture {}"}},"settings":{}}`),
		"standard_json_variants": []json.RawMessage{json.RawMessage(`{"language":"Solidity","sources":{"Fixture.sol":{"content":"contract Fixture {}"}},"settings":{}}`)},
		"bytecodes":              []map[string]string{{"runtime_bytecode": "0x00"}},
		"target": map[string]any{
			"chain_id": 1, "address": "0x" + hex.EncodeToString(address),
			"code_hash":        "0x" + hex.EncodeToString(codeHash),
			"at_block_hash":    "0x" + hex.EncodeToString(blockHash[:]),
			"runtime_bytecode": "0x00",
		},
		"catalog_generation_id": int64(1),
		"compiler_platform":     "linux-amd64",
		"compiler_sha256":       hex.EncodeToString(compilerDigest[:]),
		"runner_sha256":         hex.EncodeToString(runnerDigest[:]),
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
	var abiValue any
	if err := json.Unmarshal([]byte(abi), &abiValue); err != nil {
		t.Fatalf("decode fixture ABI: %v", err)
	}
	outcomeObject := map[string]any{
		"kind": "verification_success", "file_name": fileName,
		"contract_name": contractName, "language": "solidity",
		"compiler_version": compilerVersion, "abi": abiValue,
		"sources": sourceObject, "settings": settingsObject,
		"compilation_artifacts":   map[string]any{},
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
	request["catalog_generation_id"] = generationID
	requestPayload, err = json.Marshal(request)
	if err != nil {
		t.Fatalf("remarshal verifier-v2 fixture request: %v", err)
	}
	requestDigest = sha256.Sum256(append([]byte("etherview:verification-request:v2\x00"), requestPayload...))
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO compiler_catalog_entries (
			generation_id, language, version, platform, artifact_url, artifact_sha256, max_bytes
		) VALUES ($1, 'solidity', $2, 'linux-amd64', 'https://compiler.example/solc', $3, 209715200)
		ON CONFLICT (generation_id, version) DO NOTHING`,
		generationID, compilerVersion, compilerDigest[:],
	); err != nil {
		t.Fatalf("insert fixture compiler entry: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verification_jobs (
			id, kind, language, catalog_language, compiler_version,
			compiler_platform, catalog_generation_id, compiler_digest, runner_digest,
			chain_id, address, code_hash, block_hash, request, request_payload,
			request_digest, requires_hard_isolation, status, attempt_count,
			max_attempts, outcome_kind, outcome
		) VALUES (
			$1::uuid, 'address', 'solidity', 'solidity', $2, 'linux-amd64', $3, $4, $5,
			1, $6, $7, $8, $9::jsonb, $10, $11, TRUE, 'succeeded', 1, 3,
			'verification_success', $12::jsonb
		)`, jobID, compilerVersion, generationID, compilerDigest[:], runnerDigest[:],
		address, codeHash, blockHash[:], string(requestPayload), requestPayload,
		requestDigest[:], string(outcome),
	); err != nil {
		t.Fatalf("insert verifier-v2 fixture job: %v", err)
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
			'{}'::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb, FALSE
		)`, jobID, requestDigest[:], string(outcome), fileName, contractName,
		compilerVersion, abi, sources, settings,
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
			'{}'::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb, FALSE
		)`, address, codeHash, int64(validFrom), validToValue, jobID,
		requestDigest[:], fileName, contractName, compilerVersion, abi, sources, settings,
	); err != nil {
		t.Fatalf("insert sourced verified-contract fixture: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit verified-contract fixture: %v", err)
	}
}
