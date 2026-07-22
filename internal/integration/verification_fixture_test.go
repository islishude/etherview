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

// insertVerifiedContractFixture creates the immutable result provenance that a
// production verified-contract projection requires. Tests that only exercise
// readers still use a coherent publication boundary instead of bypassing it.
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
	request := map[string]any{
		"chain_id":            1,
		"address":             "0x" + hex.EncodeToString(address),
		"code_hash":           "0x" + hex.EncodeToString(codeHash),
		"at_block_hash":       "0x" + hex.EncodeToString(blockHash[:]),
		"language":            "solidity",
		"compiler_version":    compilerVersion,
		"contract_identifier": "Fixture.sol:" + contractName,
		"standard_json": map[string]any{
			"language": "Solidity",
			"sources": map[string]any{
				"Fixture.sol": map[string]any{"content": "contract " + contractName + " {}"},
			},
			"settings": map[string]any{},
		},
		"creation_bytecode": "0x00",
		"runtime_bytecode":  "0x00",
	}
	requestPayload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal verification fixture request: %v", err)
	}
	digestInput := append([]byte("etherview:verification-request:v1\x00"), requestPayload...)
	requestDigest := sha256.Sum256(digestInput)
	result := `{"match":{"creation":"exact","runtime":"exact"},"published":true}`

	var validToValue any
	if validTo != nil {
		validToValue = int64(*validTo)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin verified-contract fixture: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verification_jobs (
			id, chain_id, address, code_hash, block_hash, language,
			compiler_version, request, request_payload, request_digest,
			requires_hard_isolation, attempt_count, max_attempts,
			compiler_kind, compiler_digest, compiler_hard_isolated,
			status, result_kind, result
		) VALUES (
			$1::uuid, 1, $2, $3, $4, 'solidity', $5,
			$6::jsonb, $7, $8, FALSE, 1, 3,
			'container', $9, TRUE, 'succeeded', 'exact', $10::jsonb
		)`, jobID, address, codeHash, blockHash[:], compilerVersion,
		string(requestPayload), requestPayload, requestDigest[:], compilerDigest[:], result); err != nil {
		t.Fatalf("insert verification fixture job: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verification_results (
			job_id, chain_id, address, code_hash, block_hash, block_number,
			request_digest, compiler_kind, compiler_digest, compiler_hard_isolated,
			result_kind, result, contract_name, abi, sources, settings
		) VALUES (
			$1::uuid, 1, $2, $3, $4, $5, $6, 'container', $7, TRUE,
			'exact', $8::jsonb, $9, $10::jsonb, $11::jsonb, $12::jsonb
		)`, jobID, address, codeHash, blockHash[:], int64(validFrom), requestDigest[:],
		compilerDigest[:], result, contractName, abi, sources, settings); err != nil {
		t.Fatalf("insert immutable verification fixture result: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verified_contracts (
			chain_id, address, code_hash, valid_from_block, valid_to_block,
			language, compiler_version, match_kind, contract_name, abi,
			sources, settings, verification_job_id, request_digest
		) VALUES (
			1, $1, $2, $3, $4, 'solidity', $5, 'exact', $6,
			$7::jsonb, $8::jsonb, $9::jsonb, $10::uuid, $11
		)`, address, codeHash, int64(validFrom), validToValue, compilerVersion,
		contractName, abi, sources, settings, jobID, requestDigest[:]); err != nil {
		t.Fatalf("insert sourced verified-contract fixture: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit verified-contract fixture: %v", err)
	}
}
