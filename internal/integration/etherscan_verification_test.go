//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
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
	address := integrationContractAddress(0)
	creationBytecode := []byte{0x60, 0x01}
	constructorArguments := []byte{0xaa, 0xbb}
	creationInput := append(append([]byte(nil), creationBytecode...), constructorArguments...)
	contractBlock, err := newIntegrationBundle(integrationBundleOptions{
		Number:     1,
		ParentHash: genesis.Block.Hash(),
		ExtraData:  []byte("verification-contract"),
		Transactions: []integrationTransactionOptions{{
			Type:             types.DynamicFeeTxType,
			ContractCreation: true,
			Data:             creationInput,
		}},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": "verification-contract"},
	})
	if err != nil {
		t.Fatalf("build verification contract block: %v", err)
	}
	registerFixtureIdentities(testHash(7_101), contractBlock.Block.Hash(), testHash(8_101), contractBlock.Block.Transactions()[0].Hash())
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
	if job.RequestV2 == nil {
		t.Fatal("durable verification job has no verifier-v2 request")
	}
	request := job.RequestV2
	if job.Status != verify.JobQueued || request.Kind != verify.JobAddress ||
		request.Target == nil || request.Target.ChainID != 1 ||
		request.Target.Address != strings.ToLower(address.String()) ||
		request.Target.CodeHash != wantCodeHash ||
		request.Target.AtBlockHash != testHash(7_101).String() ||
		request.Target.CreationBytecode != hexutil.Encode(creationInput) ||
		request.Target.RuntimeBytecode != hexutil.Encode(runtimeBytecode) ||
		request.CatalogGenerationID != 0 ||
		request.CompilerPlatform != "" ||
		request.CompilerDigest != "" ||
		request.ExecutorKind != "" ||
		request.ExecutionPolicy != "" ||
		request.ExecutorDigest != "" {
		t.Fatalf("durable verifier-v2 request = %+v", *request)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM verification_jobs WHERE id = $1::uuid`, 1, guid)

	status, err := backend.Execute(ctx, etherscan.Request{
		Module: "contract", Action: "checkverifystatus", Values: url.Values{"guid": {guid}},
	})
	if status != "" || !errors.Is(err, etherscan.ErrPending) {
		t.Fatalf("queued status = %#v, error=%v", status, err)
	}
}

func TestEtherscanVerificationSubmitsAuthenticatedGenesisRuntimeOnlyJob(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	coreRepository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(
		0, testHash(7_110), testHash(0), testHash(8_110), "verification-predeploy-genesis",
	)
	commitCanonical(t, ctx, coreRepository, genesis)
	address := testAddress(7_110)
	runtimeBytecode := []byte{0x60, 0x02}
	codeHash := keccak256(runtimeBytecode)
	insertAuthenticatedGenesisPredeploy(
		t, ctx, db, genesis.Block.Hash(), genesis.Block.Root(), address.Bytes(), runtimeBytecode,
	)

	verificationRepository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	verificationService, err := verify.NewService(verificationRepository, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := etherscan.NewPostgresBackend(db, etherscan.PostgresOptions{
		ChainID: 1, Verification: verificationService, VerificationMaxInputBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := backend.Execute(ctx, etherscan.Request{
		Module: "contract", Action: "verifysourcecode", Values: url.Values{
			"contractaddress": {address.Hex()}, "sourceCode": {"contract A {}"},
			"codeformat": {"solidity-single-file"}, "contractname": {"A"},
			"compilerversion": {"v0.8.30+commit.73712a01"},
		},
	})
	if err != nil {
		t.Fatalf("submit Genesis verification: %v", err)
	}
	guid, ok := result.(string)
	if !ok || guid == "" {
		t.Fatalf("verification GUID = %#v", result)
	}
	job, found, err := verificationService.Job(ctx, guid)
	if err != nil || !found || job.RequestV2 == nil || job.RequestV2.Target == nil {
		t.Fatalf("durable Genesis job: found=%t job=%+v error=%v", found, job, err)
	}
	target := job.RequestV2.Target
	if !target.GenesisPredeploy || target.CodeHash != "0x"+hex.EncodeToString(codeHash) ||
		target.AtBlockHash != strings.ToLower(genesis.Block.Hash().Hex()) ||
		target.CreationBytecode != "" || target.RuntimeBytecode != hexutil.Encode(runtimeBytecode) ||
		len(job.RequestV2.Bytecodes) != 1 || job.RequestV2.Bytecodes[0].Creation != "" ||
		job.RequestV2.Bytecodes[0].Runtime != hexutil.Encode(runtimeBytecode) {
		t.Fatalf("durable Genesis request = %+v", *job.RequestV2)
	}
}

func TestGenesisPredeployTargetRejectsDifferentCurrentRuntime(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	coreRepository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(
		0, testHash(7_120), testHash(0), testHash(8_120), "verification-predeploy-mismatch-genesis",
	)
	block := testBundle(
		1, testHash(7_121), genesis.Block.Hash(), testHash(8_121), "verification-predeploy-mismatch-block",
	)
	commitCanonical(t, ctx, coreRepository, genesis)
	commitCanonical(t, ctx, coreRepository, block)
	address := testAddress(7_120)
	insertAuthenticatedGenesisPredeploy(
		t, ctx, db, genesis.Block.Hash(), genesis.Block.Root(), address.Bytes(), []byte{0x60, 0x01},
	)
	currentRuntime := []byte{0x60, 0x02}
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES (1, $1, 1, $2, $3, $4, TRUE)`,
		address.Bytes(), block.Block.Hash().Bytes(), keccak256(currentRuntime), currentRuntime,
	)
	backend, err := etherscan.NewPostgresBackend(db, etherscan.PostgresOptions{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ResolveVerificationTarget(ctx, address.Hex()); !errors.Is(err, etherscan.ErrVerificationTargetUnavailable) {
		t.Fatalf("mismatched current runtime target error = %v", err)
	}
}

func insertAuthenticatedGenesisPredeploy(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	blockHash common.Hash,
	stateRoot common.Hash,
	address []byte,
	runtime []byte,
) {
	t.Helper()
	codeHash := keccak256(runtime)
	execFixture(t, ctx, db, `
		INSERT INTO genesis_state_imports (
			chain_id, block_hash, state_root, document_sha256, state,
			account_count, imported_at
		) VALUES (1, $1, $2, $3, 'complete', 1, now())`,
		blockHash.Bytes(), stateRoot.Bytes(), testHash(9_110).Bytes(),
	)
	execFixture(t, ctx, db, `
		INSERT INTO genesis_account_observations (
			chain_id, address, block_hash, balance, nonce, code_hash, code, storage_root
		) VALUES (1, $1, $2, 0, 0, $3, $4, $5)`,
		address, blockHash.Bytes(), codeHash, runtime, types.EmptyRootHash.Bytes(),
	)
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES (1, $1, 0, $2, $3, $4, TRUE)`,
		address, blockHash.Bytes(), codeHash, runtime,
	)
}

func keccak256(value []byte) []byte {
	return crypto.Keccak256(value)
}
