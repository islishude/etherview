//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func TestVerificationPublicationRequiresCanonicalCodeObservation(t *testing.T) {
	tests := []struct {
		name                string
		observationAddress  uint64
		observationBlock    uint64
		observationHash     uint64
		observationCodeHash []byte
		canonical           bool
		wantPublished       bool
	}{
		{name: "exact canonical identity", observationAddress: 720, observationBlock: 1, observationHash: 7_201, canonical: true, wantPublished: true},
		{name: "stale block observation", observationAddress: 720, observationBlock: 0, observationHash: 7_200, canonical: true},
		{name: "noncanonical observation flag", observationAddress: 720, observationBlock: 1, observationHash: 7_201, canonical: false},
		{name: "mismatched code hash", observationAddress: 720, observationBlock: 1, observationHash: 7_201, observationCodeHash: make([]byte, 32), canonical: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()

			coreRepository, err := store.NewPostgresRepository(db)
			if err != nil {
				t.Fatalf("create core repository: %v", err)
			}
			genesis := testBundle(0, testHash(7_200), testHash(0), testHash(8_200), "verification-binding-genesis")
			block := testBundle(1, testHash(7_201), testHash(7_200), testHash(8_201), "verification-binding-block")
			commitCanonical(t, ctx, coreRepository, genesis)
			commitCanonical(t, ctx, coreRepository, block)

			runtimeBytecode := []byte{0x60, 0x01}
			codeHash := keccak256(runtimeBytecode)
			observationCodeHash := test.observationCodeHash
			if observationCodeHash == nil {
				observationCodeHash = codeHash
			}
			execFixture(t, ctx, db, `
				INSERT INTO contract_code_observations (
					chain_id, address, block_number, block_hash, code_hash, code, canonical
				) VALUES (1, $1, $2, $3, $4, $5, $6)`,
				mustBytes(t, testAddress(test.observationAddress)), test.observationBlock,
				mustBytes(t, testHash(test.observationHash)), observationCodeHash, runtimeBytecode, test.canonical,
			)

			repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20})
			if err != nil {
				t.Fatalf("create verification repository: %v", err)
			}
			request := verify.Request{
				ChainID: 1, Address: testAddress(720).String(),
				CodeHash: "0x" + hex.EncodeToString(codeHash), AtBlockHash: testHash(7_201).String(),
				Language: verify.LanguageSolidity, CompilerVersion: "v0.8.30+commit.73712a01",
				ContractIdentifier: "A.sol:A",
				StandardJSON:       json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
				CreationBytecode:   "0x6001", RuntimeBytecode: "0x6001",
			}
			if _, _, err := repository.Submit(ctx, request); err != nil {
				t.Fatalf("submit verification job: %v", err)
			}
			lease, found, err := repository.Claim(ctx, "integration-worker", time.Minute)
			if err != nil || !found {
				t.Fatalf("claim verification job: found=%t error=%v", found, err)
			}
			completion := verify.Completion{
				Kind: verify.MatchExact, Match: verify.MatchResult{Creation: verify.MatchExact, Runtime: verify.MatchExact},
				Artifact: verify.Artifact{ABI: json.RawMessage(`[]`)},
				Sources:  json.RawMessage(`{"A.sol":{"content":"contract A {}"}}`),
				Settings: json.RawMessage(`{}`),
			}
			err = repository.Complete(ctx, lease, completion)
			if test.wantPublished {
				if err != nil {
					t.Fatalf("publish exact canonical verification: %v", err)
				}
				assertRowCount(t, ctx, db, `SELECT count(*) FROM verified_contracts`, 1)
				return
			}
			if !errors.Is(err, sql.ErrNoRows) || !strings.Contains(err.Error(), "canonical verification target") {
				t.Fatalf("unbound publication error=%v", err)
			}
			assertRowCount(t, ctx, db, `SELECT count(*) FROM verified_contracts`, 0)
			assertRowCount(t, ctx, db, `SELECT count(*) FROM verification_jobs WHERE status = 'running'`, 1)
		})
	}
}
