//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/hex"
	"net/url"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func TestCompatibleSharedUUPSProbePromotesQuietERC1967Bindings(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(95_800), testHash(0), testHash(95_900), "shared-uups-genesis")
	block := testBundle(1, testHash(95_801), genesis.Block.Hash(), testHash(95_901), "shared-uups-block")
	commitCanonical(t, ctx, core, genesis)
	commitCanonical(t, ctx, core, block)
	blockRef := mustBlockRef(t, block)

	proxies := []common.Address{testAddress(9_580), testAddress(9_581)}
	implementation := testAddress(9_582)
	proxyCode := []byte{0x60, 0x71, 0x60, 0x00}
	implementationCode := []byte{0x60, 0x72}
	proxyCodeHash := common.BytesToHash(crypto.Keccak256(proxyCode))
	implementationCodeHash := common.BytesToHash(crypto.Keccak256(implementationCode))
	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	for _, proxy := range proxies {
		insertAuthenticatedProxyArtifactFixture(
			t, ctx, db, blockRef, generation, compilerDigest, executorDigest,
			proxy, proxyCodeHash, proxyCode, "erc1967_proxy", nil,
		)
	}
	initialStates := map[common.Address]proxyVerificationRPCState{
		implementation: {code: implementationCode},
	}
	for _, proxy := range proxies {
		initialStates[proxy] = proxyVerificationRPCState{
			code: proxyCode, implementation: &implementation,
		}
	}
	publishAuthenticatedProxyState(t, ctx, db, blockRef, initialStates)
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM proxy_artifact_resolutions AS resolution
		JOIN published_block_stage_results AS published
		  ON published.chain_id = resolution.chain_id
		 AND published.block_hash = resolution.observation_block_hash
		 AND published.stage = 'proxy'
		 AND published.stage_version = resolution.observation_stage_version
		 AND published.durable_job_id = resolution.durable_job_id
		 AND published.job_generation = resolution.job_generation
		 AND published.state = 'complete'
		WHERE resolution.chain_id = 1
		  AND resolution.observation_block_hash = $1
		  AND resolution.proxy_pattern = 'erc1967'
		  AND resolution.implementation_artifact_job_id IS NULL`,
		2, blockRef.Hash.Bytes(),
	)

	// Publish only the shared implementation artifact in the next proxy lease
	// generation. The two proxy resolutions are quiet and must be carried
	// forward without another proxy storage/code RPC fanout.
	uupsArtifactJob := insertAuthenticatedProxyArtifactFixture(
		t, ctx, db, blockRef, generation, compilerDigest, executorDigest,
		implementation, implementationCodeHash, implementationCode,
		"uups_implementation", &implementation,
	)
	version := make([]byte, 96)
	version[31] = 32
	version[63] = 5
	copy(version[64:], []byte("5.0.0"))
	selector := func(signature string) string {
		return hex.EncodeToString(crypto.Keccak256([]byte(signature))[:4])
	}
	probeRPC := &proxyVerificationRPCService{
		blockHash: blockRef.Hash,
		states: map[common.Address]proxyVerificationRPCState{
			implementation: {
				code: implementationCode,
				probeResponses: map[string][]byte{
					selector("proxiableUUID()"):             enrich.EIP1967ImplementationSlot.Bytes(),
					selector("UPGRADE_INTERFACE_VERSION()"): version,
				},
			},
		},
	}
	publishAuthenticatedProxyReplaySources(
		t, ctx, db, blockRef, probeRPC, uupsArtifactJob,
	)
	if got := probeRPC.callCount(probeRPC.codeCalls, implementation); got != 1 {
		t.Fatalf("shared UUPS implementation eth_getCode calls = %d, want 1", got)
	}
	if got := probeRPC.callCount(probeRPC.callCalls, implementation); got != 2 {
		t.Fatalf("shared UUPS implementation eth_call calls = %d, want 2", got)
	}
	for _, proxy := range proxies {
		if codeCalls := probeRPC.callCount(probeRPC.codeCalls, proxy); codeCalls != 0 {
			t.Fatalf("quiet proxy %s eth_getCode calls = %d, want 0", proxy, codeCalls)
		}
		if storageCalls := probeRPC.callCount(probeRPC.storeCalls, proxy); storageCalls != 0 {
			t.Fatalf("quiet proxy %s eth_getStorageAt calls = %d, want 0", proxy, storageCalls)
		}
		if callCalls := probeRPC.callCount(probeRPC.callCalls, proxy); callCalls != 0 {
			t.Fatalf("quiet proxy %s eth_call calls = %d, want 0", proxy, callCalls)
		}
	}

	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM proxy_artifact_resolutions AS resolution
		JOIN published_block_stage_results AS published
		  ON published.chain_id = resolution.chain_id
		 AND published.block_hash = resolution.observation_block_hash
		 AND published.stage = 'proxy'
		 AND published.stage_version = resolution.observation_stage_version
		 AND published.durable_job_id = resolution.durable_job_id
		 AND published.job_generation = resolution.job_generation
		 AND published.state = 'complete'
		WHERE resolution.chain_id = 1 AND resolution.observation_block_hash = $1
		  AND resolution.proxy_pattern = 'erc1967'
		  AND resolution.implementation_artifact_job_id IS NULL`,
		2, blockRef.Hash.Bytes(),
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM uups_implementation_observations AS observation
		JOIN uups_implementation_observation_generations AS generation
		  ON generation.chain_id = observation.chain_id
		 AND generation.implementation_address = observation.implementation_address
		 AND generation.observation_block_hash = observation.block_hash
		 AND generation.observation_stage_version = observation.stage_version
		 AND generation.verification_job_id = observation.verification_job_id
		JOIN published_block_stage_results AS published
		  ON published.chain_id = generation.chain_id
		 AND published.block_hash = generation.observation_block_hash
		 AND published.stage = 'proxy'
		 AND published.stage_version = generation.observation_stage_version
		 AND published.durable_job_id = generation.durable_job_id
		 AND published.job_generation = generation.job_generation
		 AND published.state = 'complete'
		WHERE observation.chain_id = 1
		  AND observation.implementation_address = $1
		  AND observation.implementation_code_hash = $2
		  AND observation.verification_job_id = $3::uuid
		  AND observation.probe_state = 'compatible'`,
		1, implementation.Bytes(), implementationCodeHash.Bytes(), uupsArtifactJob,
	)

	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := verify.NewService(repository, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := etherscan.NewPostgresBackend(db, etherscan.PostgresOptions{
		ChainID: 1, Verification: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, proxy := range proxies {
		result, executeErr := backend.Execute(ctx, etherscan.Request{
			Module: "contract", Action: "verifyproxycontract",
			Values: url.Values{
				"address":                {proxy.Hex()},
				"expectedimplementation": {implementation.Hex()},
			},
		})
		if executeErr != nil {
			t.Fatalf("submit dynamically promoted UUPS proxy %s: %v", proxy, executeErr)
		}
		guid, _ := result.(string)
		completeProxyVerification(t, ctx, repository, guid)
		assertRowCount(t, ctx, db, `
			SELECT count(*)
			FROM verified_proxy_bindings AS binding
			JOIN proxy_artifact_resolutions AS resolution
			  ON resolution.id = binding.artifact_resolution_id
			JOIN uups_implementation_observation_generations AS generation
			  ON generation.id = binding.uups_generation_id
			WHERE binding.verification_job_id = $1::uuid
			  AND binding.proxy_pattern = 'uups'
			  AND binding.proxy_kind = 'eip1967'
			  AND binding.management_kind = 'none'
			  AND binding.uups_generation_id IS NOT NULL
			  AND resolution.proxy_pattern = 'erc1967'
			  AND resolution.implementation_artifact_job_id IS NULL
			  AND generation.implementation_address = $2`,
			1, guid, implementation.Bytes(),
		)
	}
}

func insertProxyVerificationCode(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	address common.Address,
	codeHash common.Hash,
	code []byte,
) {
	t.Helper()
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES (1, $1, $2::numeric, $3, $4, $5, TRUE)`,
		address.Bytes(), block.Number, block.Hash.Bytes(), codeHash.Bytes(), code)
}
