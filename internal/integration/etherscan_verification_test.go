//go:build integration

package integration_test

import (
	"context"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
	"golang.org/x/crypto/sha3"
)

func TestEtherscanVerificationSubmitsDurableCanonicalJob(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	coreRepository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatalf("create core PostgreSQL repository: %v", err)
	}
	genesis := testBundle(0, testHash(7_100), testHash(0), testHash(8_100), "verification-genesis")
	contractBlock := testBundle(1, testHash(7_101), testHash(7_100), testHash(8_101), "verification-contract")
	address := testAddress(710)
	creationBytecode := []byte{0x60, 0x01}
	constructorArguments := []byte{0xaa, 0xbb}
	creationInput := append(append([]byte(nil), creationBytecode...), constructorArguments...)
	contractBlock.Block.Transactions[0].Transaction.To = nil
	contractBlock.Block.Transactions[0].Transaction.Input = ethrpc.DataFromBytes(creationInput)
	contractBlock.Receipts[0].ContractAddress = &address
	commitCanonical(t, ctx, coreRepository, genesis)
	commitCanonical(t, ctx, coreRepository, contractBlock)

	runtimeBytecode := []byte{0x60, 0x02}
	codeHash := keccak256(runtimeBytecode)
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES (1, $1, 1, $2, $3, $4, TRUE)`,
		mustBytes(t, address), mustBytes(t, testHash(7_101)), codeHash, runtimeBytecode,
	)

	verificationRepository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20,
		MaxResultBytes:  1 << 20,
	})
	if err != nil {
		t.Fatalf("create verification repository: %v", err)
	}
	verificationService, err := verify.NewService(verificationRepository, 1<<20)
	if err != nil {
		t.Fatalf("create verification service: %v", err)
	}
	backend, err := etherscan.NewPostgresBackend(db, etherscan.PostgresOptions{
		ChainID: 1, Verification: verificationService, VerificationMaxInputBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("create Etherscan backend: %v", err)
	}

	values := url.Values{
		"contractaddress":      {address.String()},
		"sourceCode":           {"contract A {}"},
		"codeformat":           {"solidity-single-file"},
		"contractname":         {"A"},
		"compilerversion":      {"v0.8.30+commit.73712a01"},
		"optimizationUsed":     {"0"},
		"runs":                 {"200"},
		"constructorArguments": {hex.EncodeToString(constructorArguments)},
		"licenseType":          {"3"},
	}
	result, err := backend.Execute(ctx, etherscan.Request{Module: "contract", Action: "verifysourcecode", Values: values})
	if err != nil {
		t.Fatalf("submit Etherscan verification: %v", err)
	}
	guid, ok := result.(string)
	if !ok || guid == "" {
		t.Fatalf("verification GUID = %#v", result)
	}

	job, found, err := verificationService.Job(ctx, guid)
	if err != nil || !found {
		t.Fatalf("load durable verification job: found=%t error=%v", found, err)
	}
	wantCodeHash := "0x" + hex.EncodeToString(codeHash)
	if job.Status != verify.JobQueued || job.Request.ChainID != 1 || job.Request.Address != strings.ToLower(address.String()) ||
		job.Request.CodeHash != wantCodeHash || job.Request.AtBlockHash != testHash(7_101).String() ||
		job.Request.CreationBytecode != ethrpc.DataFromBytes(creationBytecode).String() ||
		job.Request.RuntimeBytecode != ethrpc.DataFromBytes(runtimeBytecode).String() ||
		job.Request.ConstructorArgs != hex.EncodeToString(constructorArguments) || job.Request.LicenseType != "3" {
		t.Fatalf("durable verification job = %+v", job)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM verification_jobs WHERE id = $1::uuid`, 1, guid)

	status, err := backend.Execute(ctx, etherscan.Request{
		Module: "contract", Action: "checkverifystatus", Values: url.Values{"guid": {guid}},
	})
	if status != "" || !errors.Is(err, etherscan.ErrPending) {
		t.Fatalf("queued status = %#v, error=%v", status, err)
	}
}

func keccak256(value []byte) []byte {
	hasher := sha3.NewLegacyKeccak256()
	_, _ = hasher.Write(value)
	return hasher.Sum(nil)
}
