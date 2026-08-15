//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func TestProxyVerificationIsDurableIdempotentAndCodeChangeSafe(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(95_000), testHash(0), testHash(95_100), "proxy-verification-genesis")
	blockOne := testBundle(1, testHash(95_001), genesis.Block.Hash(), testHash(95_101), "proxy-verification-one")
	commitCanonical(t, ctx, core, genesis)
	commitCanonical(t, ctx, core, blockOne)
	blockOneRef := mustBlockRef(t, blockOne)

	proxy := testAddress(9_500)
	implementationOne := testAddress(9_501)
	implementationTwo := testAddress(9_502)
	implementationThree := testAddress(9_503)
	implementationCodeOne := []byte{0x60, 0x02}
	implementationCodeTwo := []byte{0x60, 0x03}
	implementationCodeThree := []byte{0x60, 0x04}
	proxyCodeOne := cloneRuntime(implementationOne)
	proxyCodeTwo := cloneRuntime(implementationTwo)
	proxyCodeThree := cloneRuntime(implementationThree)
	proxyHashOne := common.BytesToHash(crypto.Keccak256(proxyCodeOne))
	proxyHashTwo := common.BytesToHash(crypto.Keccak256(proxyCodeTwo))
	proxyHashThree := common.BytesToHash(crypto.Keccak256(proxyCodeThree))
	implementationHashOne := common.BytesToHash(crypto.Keccak256(implementationCodeOne))
	implementationHashTwo := common.BytesToHash(crypto.Keccak256(implementationCodeTwo))
	implementationHashThree := common.BytesToHash(crypto.Keccak256(implementationCodeThree))

	insertProxyVerificationCode(t, ctx, db, blockOneRef, proxy, proxyHashOne, proxyCodeOne)
	insertProxyVerificationCode(t, ctx, db, blockOneRef, implementationOne, implementationHashOne, implementationCodeOne)
	insertProxyVerificationObservation(
		t, ctx, db, blockOneRef, proxy, proxyHashOne, implementationOne, implementationHashOne,
	)
	publishProxyVerificationObservation(
		t, ctx, db, blockOneRef, proxy, proxyCodeOne, implementationOne, implementationCodeOne,
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM proxy_observations
		WHERE chain_id = 1 AND proxy_address = $1 AND block_hash = $2
		  AND stage_version = 2 AND canonical
		  AND proxy_pattern = 'clone' AND evidence_state = 'exact'`, 1,
		proxy.Bytes(), blockOneRef.Hash.Bytes())
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM proxy_observation_generations AS witness
		WHERE witness.chain_id = 1 AND witness.proxy_address = $1
		  AND witness.observation_block_hash = $2`, 1,
		proxy.Bytes(), blockOneRef.Hash.Bytes())
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM proxy_observation_generations AS witness
		JOIN published_block_stage_results AS published
		  ON published.chain_id = witness.chain_id
		 AND published.block_hash = witness.observation_block_hash
		 AND published.stage = 'proxy'
		 AND published.stage_version = witness.observation_stage_version
		 AND published.durable_job_id = witness.durable_job_id
		 AND published.job_generation = witness.job_generation
		WHERE witness.chain_id = 1 AND witness.proxy_address = $1
		  AND witness.observation_block_hash = $2`, 1,
		proxy.Bytes(), blockOneRef.Hash.Bytes())
	insertProxyVerificationSource(t, ctx, db, proxy, proxyHashOne, "Proxy")
	insertProxyVerificationSource(t, ctx, db, implementationOne, implementationHashOne, "Implementation")

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

	submit := func(expected common.Address) (string, error) {
		result, executeErr := backend.Execute(ctx, etherscan.Request{
			Module: "contract", Action: "verifyproxycontract",
			Values: url.Values{
				"address":                {proxy.Hex()},
				"expectedimplementation": {expected.Hex()},
			},
		})
		if executeErr != nil {
			return "", executeErr
		}
		guid, _ := result.(string)
		return guid, nil
	}

	before := proxyVerificationJobCount(t, ctx, db)
	if _, err := submit(implementationTwo); !errors.Is(err, etherscan.ErrProxyExpectedImplementationMismatch) {
		t.Fatalf("wrong expected implementation error = %v", err)
	}
	if got := proxyVerificationJobCount(t, ctx, db); got != before {
		t.Fatalf("wrong expected implementation created a job: before=%d after=%d", before, got)
	}
	guid, err := submit(implementationOne)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := submit(implementationOne)
	if err != nil || duplicate != guid {
		t.Fatalf("duplicate submission = %q, error=%v, want %q", duplicate, err, guid)
	}
	completeProxyVerification(t, ctx, repository, guid)

	status, err := backend.Execute(ctx, etherscan.Request{
		Module: "contract", Action: "checkproxyverification",
		Values: url.Values{"guid": {guid}},
	})
	if err != nil || status != "Pass - Verified" {
		t.Fatalf("proxy status = %#v, error=%v", status, err)
	}
	assertProxyVerificationSource(t, ctx, backend, proxy, implementationOne, true)
	if _, err := db.ExecContext(ctx, `
		UPDATE verified_proxy_bindings SET proxy_kind = 'beacon'
		WHERE verification_job_id = $1::uuid`, guid); err == nil {
		t.Fatal("immutable proxy verification publication accepted an update")
	}

	blockTwo := testBundle(2, testHash(95_002), blockOne.Block.Hash(), testHash(95_102), "proxy-verification-two")
	commitCanonical(t, ctx, core, blockTwo)
	blockTwoRef := mustBlockRef(t, blockTwo)
	insertProxyVerificationCode(t, ctx, db, blockTwoRef, proxy, proxyHashTwo, proxyCodeTwo)
	insertProxyVerificationCode(t, ctx, db, blockTwoRef, implementationTwo, implementationHashTwo, implementationCodeTwo)
	insertProxyVerificationObservation(
		t, ctx, db, blockTwoRef, proxy, proxyHashTwo, implementationTwo, implementationHashTwo,
	)
	publishProxyVerificationObservation(
		t, ctx, db, blockTwoRef, proxy, proxyCodeTwo, implementationTwo, implementationCodeTwo,
	)
	insertProxyVerificationSource(t, ctx, db, proxy, proxyHashTwo, "ProxyV2")
	assertProxyVerificationSource(t, ctx, backend, proxy, common.Address{}, false)
	if _, err := submit(implementationTwo); !errors.Is(err, etherscan.ErrProxyImplementationUnverified) {
		t.Fatalf("unverified upgraded implementation error = %v", err)
	}
	insertProxyVerificationSource(t, ctx, db, implementationTwo, implementationHashTwo, "ImplementationV2")
	upgradedGUID, err := submit(implementationTwo)
	if err != nil {
		t.Fatal(err)
	}
	completeProxyVerification(t, ctx, repository, upgradedGUID)
	assertProxyVerificationSource(t, ctx, backend, proxy, implementationTwo, true)

	blockThree := testBundle(3, testHash(95_003), blockTwo.Block.Hash(), testHash(95_103), "proxy-verification-three")
	commitCanonical(t, ctx, core, blockThree)
	blockThreeRef := mustBlockRef(t, blockThree)
	insertProxyVerificationCode(t, ctx, db, blockThreeRef, proxy, proxyHashThree, proxyCodeThree)
	insertProxyVerificationCode(t, ctx, db, blockThreeRef, implementationThree, implementationHashThree, implementationCodeThree)
	insertProxyVerificationObservation(
		t, ctx, db, blockThreeRef, proxy, proxyHashThree, implementationThree, implementationHashThree,
	)
	publishProxyVerificationObservation(
		t, ctx, db, blockThreeRef, proxy, proxyCodeThree, implementationThree, implementationCodeThree,
	)
	insertProxyVerificationSource(t, ctx, db, proxy, proxyHashThree, "ProxyV3")
	insertProxyVerificationSource(t, ctx, db, implementationThree, implementationHashThree, "ImplementationV3")
	reorgGUID, err := submit(implementationThree)
	if err != nil {
		t.Fatal(err)
	}
	lease, found, err := repository.Claim(ctx, "proxy-verification-reorg", time.Minute)
	if err != nil || !found || lease.Job.ID != reorgGUID {
		t.Fatalf("claim reorg proxy job: lease=%+v found=%t error=%v", lease, found, err)
	}
	execFixture(t, ctx, db, `
		UPDATE proxy_observations SET canonical = FALSE
		WHERE chain_id = 1 AND proxy_address = $1 AND block_hash = $2`,
		proxy.Bytes(), blockThreeRef.Hash.Bytes())
	execFixture(t, ctx, db, `
		UPDATE contract_code_observations SET canonical = FALSE
		WHERE chain_id = 1 AND block_hash = $1 AND address IN ($2, $3)`,
		blockThreeRef.Hash.Bytes(), proxy.Bytes(), implementationThree.Bytes())
	execFixture(t, ctx, db, `
		UPDATE transaction_state_changes SET canonical = FALSE
		WHERE chain_id = 1 AND block_hash = $1 AND address = $2`,
		blockThreeRef.Hash.Bytes(), proxy.Bytes())
	if err := repository.CompleteProxyV2(ctx, lease); !errors.Is(err, verify.ErrTargetNotCanonical) {
		t.Fatalf("completion after reorg error = %v", err)
	}
	if err := repository.Fail(ctx, lease, verify.ErrorTargetNotCanonical); err != nil {
		t.Fatalf("fail stale proxy job: %v", err)
	}
	if _, err := backend.Execute(ctx, etherscan.Request{
		Module: "contract", Action: "checkproxyverification",
		Values: url.Values{"guid": {reorgGUID}},
	}); !errors.Is(err, etherscan.ErrProxyVerificationFailed) {
		t.Fatalf("reorged proxy status error = %v", err)
	}
	assertProxyVerificationSource(t, ctx, backend, proxy, implementationTwo, true)

	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verification_results
		WHERE outcome_kind = 'proxy_verification_success'`, 2)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM verified_proxy_bindings`, 2)
}

func TestProxyVerificationAtoBtoACreatesFreshBindingIdentity(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(95_200), testHash(0), testHash(95_300), "proxy-binding-aba-genesis")
	blockA := testBundle(1, testHash(95_201), genesis.Block.Hash(), testHash(95_301), "proxy-binding-a")
	blockB := testBundle(2, testHash(95_202), blockA.Block.Hash(), testHash(95_302), "proxy-binding-b")
	blockAReturn := testBundle(3, testHash(95_203), blockB.Block.Hash(), testHash(95_303), "proxy-binding-a-return")
	commitCanonical(t, ctx, core, genesis)
	commitCanonical(t, ctx, core, blockA)

	proxy := testAddress(9_520)
	implementationA := testAddress(9_521)
	implementationB := testAddress(9_522)
	implementationCodeA := []byte{0x60, 0x11}
	implementationCodeB := []byte{0x60, 0x12}
	implementationHashA := common.BytesToHash(crypto.Keccak256(implementationCodeA))
	implementationHashB := common.BytesToHash(crypto.Keccak256(implementationCodeB))
	proxyCode := []byte{0x60, 0x21, 0x60, 0x00}
	proxyCodeHash := common.BytesToHash(crypto.Keccak256(proxyCode))

	blockARef := mustBlockRef(t, blockA)
	blockBRef := mustBlockRef(t, blockB)
	blockAReturnRef := mustBlockRef(t, blockAReturn)
	insertProxyVerificationCode(t, ctx, db, blockARef, implementationA, implementationHashA, implementationCodeA)
	insertProxyVerificationSource(t, ctx, db, implementationA, implementationHashA, "ImplementationA")
	insertProxyVerificationSource(t, ctx, db, implementationB, implementationHashB, "ImplementationB")
	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	artifactJob := insertAuthenticatedProxyArtifactFixture(
		t, ctx, db, blockARef, generation, compilerDigest, executorDigest,
		proxy, proxyCodeHash, proxyCode, "erc1967_proxy", nil,
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
	submitAndComplete := func(
		block store.BlockRef,
		implementation common.Address,
		implementationHash common.Hash,
		implementationCode []byte,
	) string {
		t.Helper()
		execFixture(t, ctx, db, `
			INSERT INTO proxy_replay_targets (
				chain_id, block_number, block_hash, address, target_kind,
				source_kind, source_verification_job_id
			) VALUES (
				1, $1::numeric, $2, $3, 'proxy',
				'verification_publication', $4::uuid
			) ON CONFLICT DO NOTHING`,
			block.Number, block.Hash.Bytes(), proxy.Bytes(), artifactJob,
		)
		publishAuthenticatedProxyState(t, ctx, db, block, map[common.Address]proxyVerificationRPCState{
			proxy:          {code: proxyCode, implementation: &implementation},
			implementation: {code: implementationCode},
		})
		assertRowCount(t, ctx, db, `
			SELECT count(*) FROM proxy_artifact_resolutions
			WHERE chain_id = 1 AND proxy_address = $1
			  AND observation_block_hash = $2
			  AND proxy_code_hash = $3
			  AND proxy_pattern = 'erc1967'
			  AND implementation_address = $4
			  AND implementation_code_hash = $5`,
			1, proxy.Bytes(), block.Hash.Bytes(), proxyCodeHash.Bytes(),
			implementation.Bytes(), implementationHash.Bytes(),
		)
		result, executeErr := backend.Execute(ctx, etherscan.Request{
			Module: "contract", Action: "verifyproxycontract",
			Values: url.Values{
				"address":                {proxy.Hex()},
				"expectedimplementation": {implementation.Hex()},
			},
		})
		if executeErr != nil {
			t.Fatalf("submit proxy binding at block %d: %v", block.Number, executeErr)
		}
		guid, _ := result.(string)
		completeProxyVerification(t, ctx, repository, guid)
		return guid
	}

	firstA := submitAndComplete(blockARef, implementationA, implementationHashA, implementationCodeA)
	commitCanonical(t, ctx, core, blockB)
	insertProxyVerificationCode(t, ctx, db, blockBRef, implementationB, implementationHashB, implementationCodeB)
	bindingB := submitAndComplete(blockBRef, implementationB, implementationHashB, implementationCodeB)
	commitCanonical(t, ctx, core, blockAReturn)
	returnA := submitAndComplete(blockAReturnRef, implementationA, implementationHashA, implementationCodeA)
	if firstA == bindingB || firstA == returnA || bindingB == returnA {
		t.Fatalf("A -> B -> A reused binding UUID: first=%s middle=%s return=%s", firstA, bindingB, returnA)
	}

	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_proxy_bindings
		WHERE chain_id = 1 AND proxy_address = $1`, 3, proxy.Bytes())
	assertRowCount(t, ctx, db, `
		SELECT count(DISTINCT verification_job_id) FROM verified_proxy_bindings
		WHERE chain_id = 1 AND proxy_address = $1`, 3, proxy.Bytes())
	assertRowCount(t, ctx, db, `
		SELECT count(DISTINCT request_digest) FROM verified_proxy_bindings
		WHERE chain_id = 1 AND proxy_address = $1`, 3, proxy.Bytes())
	assertRowCount(t, ctx, db, `
		SELECT count(DISTINCT observation_generation_id) FROM verified_proxy_bindings
		WHERE chain_id = 1 AND proxy_address = $1`, 3, proxy.Bytes())
	assertRowCount(t, ctx, db, `
		SELECT count(DISTINCT artifact_resolution_id) FROM verified_proxy_bindings
		WHERE chain_id = 1 AND proxy_address = $1`, 3, proxy.Bytes())
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_proxy_bindings
		WHERE chain_id = 1 AND proxy_address = $1
		  AND implementation_address = $2`, 2, proxy.Bytes(), implementationA.Bytes())

	var currentBinding string
	if err := db.QueryRowContext(ctx, `
		WITH latest_observation AS (
			SELECT observation.*
			FROM proxy_observations AS observation
			JOIN canonical_blocks AS canonical
			  ON canonical.chain_id = observation.chain_id
			 AND canonical.number = observation.block_number
			 AND canonical.block_hash = observation.block_hash
			WHERE observation.chain_id = 1
			  AND observation.proxy_address = $1
			  AND observation.canonical
			  AND observation.stage_version = 2
			ORDER BY observation.block_number DESC, observation.block_hash DESC
			LIMIT 1
		), published_generation AS (
			SELECT generation.id
			FROM latest_observation AS observation
			JOIN proxy_observation_generations AS generation
			  ON generation.chain_id = observation.chain_id
			 AND generation.proxy_address = observation.proxy_address
			 AND generation.observation_block_hash = observation.block_hash
			 AND generation.observation_stage_version = observation.stage_version
			JOIN published_block_stage_results AS published
			  ON published.chain_id = generation.chain_id
			 AND published.block_hash = generation.observation_block_hash
			 AND published.stage = 'proxy'
			 AND published.stage_version = generation.observation_stage_version
			 AND published.durable_job_id = generation.durable_job_id
			 AND published.job_generation = generation.job_generation
			ORDER BY generation.id DESC
			LIMIT 1
		)
		SELECT binding.verification_job_id::text
		FROM verified_proxy_bindings AS binding
		JOIN latest_observation AS observation
		  ON binding.chain_id = observation.chain_id
		 AND binding.proxy_address = observation.proxy_address
		 AND binding.observation_block_hash = observation.block_hash
		 AND binding.observation_stage_version = observation.stage_version
		JOIN published_generation AS generation
		  ON generation.id = binding.observation_generation_id
		WHERE binding.chain_id = 1 AND binding.proxy_address = $1`,
		proxy.Bytes(),
	).Scan(&currentBinding); err != nil {
		t.Fatalf("query current A -> B -> A binding: %v", err)
	}
	if currentBinding != returnA {
		t.Fatalf("current binding = %s, want fresh return-to-A binding %s", currentBinding, returnA)
	}
	if currentBinding == firstA {
		t.Fatalf("the original A binding became current again: %s", firstA)
	}
	assertProxyVerificationSource(t, ctx, backend, proxy, implementationA, true)
}

func TestProxyVerificationManagementCodeEpochRejectsAtoBtoA(t *testing.T) {
	for _, test := range []struct {
		name                 string
		completeBeforeABA    bool
		withdrawCoverage     bool
		withdrawDuringInsert bool
		serializeCoverage    bool
		serializeReplayStage enrich.StageID
		serializeTipAdvance  bool
	}{
		{name: "published binding never revives", completeBeforeABA: true},
		{name: "queued request cannot complete", completeBeforeABA: false},
		{name: "queued request loses stage coverage", withdrawCoverage: true},
		{name: "binding insert rechecks concurrently withdrawn coverage", withdrawDuringInsert: true},
		{name: "binding insert serializes with coverage refresh", serializeCoverage: true},
		{name: "completion serializes with state diff replay", serializeReplayStage: enrich.StateDiffStage},
		{name: "completion serializes with proxy generation replay", serializeReplayStage: enrich.ProxyStage},
		{name: "completion observes queued canonical tip advance", serializeTipAdvance: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			core, err := store.NewPostgresRepository(db)
			if err != nil {
				t.Fatal(err)
			}
			seed := uint64(95_800)
			if test.withdrawCoverage {
				seed += 200
			} else if test.withdrawDuringInsert {
				seed += 300
			} else if test.serializeCoverage {
				seed += 400
			} else if test.serializeReplayStage == enrich.StateDiffStage {
				seed += 500
			} else if test.serializeReplayStage == enrich.ProxyStage {
				seed += 600
			} else if test.serializeTipAdvance {
				seed += 700
			} else if !test.completeBeforeABA {
				seed += 100
			}
			genesis := testBundle(0, testHash(seed), testHash(0), testHash(seed+10), "proxy-code-epoch-genesis")
			blockA := testBundle(1, testHash(seed+1), genesis.Block.Hash(), testHash(seed+11), "proxy-code-epoch-a")
			growth := testBundle(2, testHash(seed+2), blockA.Block.Hash(), testHash(seed+12), "proxy-code-epoch-growth")
			blockB := testBundle(3, testHash(seed+3), growth.Block.Hash(), testHash(seed+13), "proxy-code-epoch-b")
			blockAReturn := testBundle(4, testHash(seed+4), blockB.Block.Hash(), testHash(seed+14), "proxy-code-epoch-a-return")
			coverageGap := testBundle(5, testHash(seed+5), blockAReturn.Block.Hash(), testHash(seed+15), "proxy-coverage-gap")
			coverageRecovery := testBundle(6, testHash(seed+6), coverageGap.Block.Hash(), testHash(seed+16), "proxy-coverage-recovery")
			commitCanonical(t, ctx, core, genesis)
			commitCanonical(t, ctx, core, blockA)
			blockARef := mustBlockRef(t, blockA)

			proxy := testAddress(seed + 20)
			implementation := testAddress(seed + 21)
			admin := testAddress(seed + 22)
			proxyCode := []byte{0x60, 0x71, 0x60, 0x00}
			implementationCode := []byte{0x60, 0x72}
			adminCodeA := []byte{0x60, 0x73}
			adminCodeB := []byte{0x60, 0x74}
			proxyCodeHash := common.BytesToHash(crypto.Keccak256(proxyCode))
			implementationCodeHash := common.BytesToHash(crypto.Keccak256(implementationCode))
			adminCodeHashA := common.BytesToHash(crypto.Keccak256(adminCodeA))
			adminCodeHashB := common.BytesToHash(crypto.Keccak256(adminCodeB))
			insertProxyVerificationCode(
				t, ctx, db, blockARef, implementation, implementationCodeHash, implementationCode,
			)
			insertProxyVerificationSource(
				t, ctx, db, implementation, implementationCodeHash, "Implementation",
			)
			generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
			insertAuthenticatedProxyArtifactFixture(
				t, ctx, db, blockARef, generation, compilerDigest, executorDigest,
				proxy, proxyCodeHash, proxyCode, "transparent_proxy", &admin,
			)
			adminArtifactJob := insertAuthenticatedProxyArtifactFixture(
				t, ctx, db, blockARef, generation, compilerDigest, executorDigest,
				admin, adminCodeHashA, adminCodeA, "proxy_admin", nil,
			)
			publishAuthenticatedProxyState(t, ctx, db, blockARef, map[common.Address]proxyVerificationRPCState{
				proxy:          {code: proxyCode, implementation: &implementation, admin: &admin},
				implementation: {code: implementationCode},
				admin:          {code: adminCodeA},
			})
			assertRowCount(t, ctx, db, `
				SELECT count(*) FROM proxy_observations
				WHERE chain_id = 1 AND proxy_address = $1 AND block_hash = $2`,
				1, proxy.Bytes(), blockARef.Hash.Bytes(),
			)
			assertRowCount(t, ctx, db, `
				SELECT count(*) FROM proxy_artifact_resolutions
				WHERE chain_id = 1 AND proxy_address = $1 AND observation_block_hash = $2`,
				1, proxy.Bytes(), blockARef.Hash.Bytes(),
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
			result, err := backend.Execute(ctx, etherscan.Request{
				Module: "contract", Action: "verifyproxycontract",
				Values: url.Values{
					"address":                {proxy.Hex()},
					"expectedimplementation": {implementation.Hex()},
				},
			})
			if err != nil {
				t.Fatalf("submit management code epoch binding: %v", err)
			}
			guid, _ := result.(string)
			if test.serializeTipAdvance {
				blocker, beginErr := db.BeginTx(ctx, nil)
				if beginErr != nil {
					t.Fatal(beginErr)
				}
				defer blocker.Rollback() //nolint:errcheck
				if _, lockErr := blocker.ExecContext(ctx, `
					SELECT pg_advisory_xact_lock(hashtextextended(
					    'etherview:proxy-interaction-coverage:' || '1', 0
					))`); lockErr != nil {
					t.Fatal(lockErr)
				}
				growthRef := mustBlockRef(t, growth)
				advanced := make(chan error, 1)
				go func() {
					advanced <- core.CommitCanonical(
						ctx, "1", growth, store.NewCoreCheckpoint(growthRef),
					)
				}()
				waitForProxyCoverageLockWaiters(t, ctx, db, 1)
				lease, found, claimErr := repository.Claim(ctx, "proxy-tip-advance-race", time.Minute)
				if claimErr != nil || !found || lease.Job.ID != guid {
					t.Fatalf("claim tip-advance binding: lease=%+v found=%t error=%v", lease, found, claimErr)
				}
				completed := make(chan error, 1)
				go func() {
					completed <- repository.CompleteProxyV2(ctx, lease)
				}()
				waitForProxyCoverageLockWaiters(t, ctx, db, 2)
				if commitErr := blocker.Commit(); commitErr != nil {
					t.Fatal(commitErr)
				}
				if advanceErr := <-advanced; advanceErr != nil {
					t.Fatalf("commit queued canonical tip: %v", advanceErr)
				}
				if completeErr := <-completed; !errors.Is(completeErr, verify.ErrTargetNotCanonical) {
					t.Fatalf("completion after queued canonical tip advance error = %v", completeErr)
				}
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM verification_results WHERE job_id = $1::uuid`, 0, guid)
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM verified_proxy_bindings WHERE verification_job_id = $1::uuid`, 0, guid)
				if failErr := repository.Fail(ctx, lease, verify.ErrorTargetNotCanonical); failErr != nil {
					t.Fatalf("fail tip-advance binding: %v", failErr)
				}
				return
			}
			if test.serializeReplayStage.Name != "" {
				blocker, beginErr := db.BeginTx(ctx, nil)
				if beginErr != nil {
					t.Fatal(beginErr)
				}
				defer blocker.Rollback() //nolint:errcheck
				if _, lockErr := blocker.ExecContext(ctx, `
					SELECT pg_advisory_xact_lock(hashtextextended(
					    'etherview:proxy-interaction-coverage:' || '1', 0
					))`); lockErr != nil {
					t.Fatal(lockErr)
				}
				queue, queueErr := enrich.NewPostgresJobQueue(db)
				if queueErr != nil {
					t.Fatal(queueErr)
				}
				word, parseErr := enrich.ParseWord(blockARef.Hash.String())
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				type replayOutcome struct {
					result enrich.EnqueueResult
					err    error
				}
				replayed := make(chan replayOutcome, 1)
				go func() {
					value, replayErr := queue.Enqueue(ctx, enrich.EnqueueRequest{
						Stage: test.serializeReplayStage, ChainID: "1",
						BlockHash: word, BlockNumber: blockARef.Number,
						Replay: enrich.ReplaySource{
							Kind: "proxy-binding-race", Key: test.serializeReplayStage.Name,
						},
					})
					replayed <- replayOutcome{result: value, err: replayErr}
				}()
				waitForProxyCoverageLockWaiters(t, ctx, db, 1)

				lease, found, claimErr := repository.Claim(ctx, "proxy-stage-replay-race", time.Minute)
				if claimErr != nil || !found || lease.Job.ID != guid {
					t.Fatalf("claim stage-replay binding: lease=%+v found=%t error=%v", lease, found, claimErr)
				}
				completed := make(chan error, 1)
				go func() {
					completed <- repository.CompleteProxyV2(ctx, lease)
				}()
				waitForProxyCoverageLockWaiters(t, ctx, db, 2)
				if commitErr := blocker.Commit(); commitErr != nil {
					t.Fatal(commitErr)
				}
				replay := <-replayed
				if replay.err != nil || !replay.result.Replayed {
					t.Fatalf("race replay %s: result=%+v error=%v", test.serializeReplayStage.Name, replay.result, replay.err)
				}
				if completeErr := <-completed; !errors.Is(completeErr, verify.ErrTargetNotCanonical) {
					t.Fatalf("completion after serialized %s replay error = %v", test.serializeReplayStage.Name, completeErr)
				}
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM verification_results WHERE job_id = $1::uuid`, 0, guid)
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM verified_proxy_bindings WHERE verification_job_id = $1::uuid`, 0, guid)
				if failErr := repository.Fail(ctx, lease, verify.ErrorTargetNotCanonical); failErr != nil {
					t.Fatalf("fail stage-replay binding: %v", failErr)
				}
				return
			}
			if test.serializeCoverage {
				blocker, beginErr := db.BeginTx(ctx, nil)
				if beginErr != nil {
					t.Fatal(beginErr)
				}
				defer blocker.Rollback() //nolint:errcheck
				if _, lockErr := blocker.ExecContext(ctx, `
					SELECT pg_advisory_xact_lock(hashtextextended(
					    'etherview:proxy-interaction-coverage:' || '1', 0
					))`); lockErr != nil {
					t.Fatal(lockErr)
				}
				lease, found, claimErr := repository.Claim(ctx, "proxy-coverage-lock-race", time.Minute)
				if claimErr != nil || !found || lease.Job.ID != guid {
					t.Fatalf("claim coverage-lock binding: lease=%+v found=%t error=%v", lease, found, claimErr)
				}
				completed := make(chan error, 1)
				go func() {
					completed <- repository.CompleteProxyV2(ctx, lease)
				}()
				for {
					var waiters int
					waitErr := db.QueryRowContext(ctx, `
						SELECT count(*) FROM pg_locks
						WHERE locktype = 'advisory' AND granted = FALSE`).Scan(&waiters)
					if waitErr != nil {
						t.Fatal(waitErr)
					}
					if waiters > 0 {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatalf("wait for proxy binding advisory coverage lock: %v", ctx.Err())
					case <-time.After(10 * time.Millisecond):
					}
				}
				select {
				case earlyErr := <-completed:
					t.Fatalf("binding completion bypassed coverage lock: %v", earlyErr)
				default:
				}
				if _, deleteErr := blocker.ExecContext(ctx, `
					DELETE FROM proxy_interaction_covered_blocks
					WHERE chain_id = 1 AND block_number = $1::numeric AND block_hash = $2`,
					blockARef.Number, blockARef.Hash.Bytes(),
				); deleteErr != nil {
					t.Fatal(deleteErr)
				}
				if commitErr := blocker.Commit(); commitErr != nil {
					t.Fatal(commitErr)
				}
				completeErr := <-completed
				if !errors.Is(completeErr, verify.ErrTargetNotCanonical) {
					t.Fatalf("serialized coverage withdrawal completion error = %v", completeErr)
				}
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM verification_results WHERE job_id = $1::uuid`, 0, guid)
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM verified_proxy_bindings WHERE verification_job_id = $1::uuid`, 0, guid)
				if failErr := repository.Fail(ctx, lease, verify.ErrorTargetNotCanonical); failErr != nil {
					t.Fatalf("fail serialized coverage binding: %v", failErr)
				}
				return
			}
			if test.withdrawDuringInsert {
				functionName := "test_withdraw_proxy_coverage_" + strings.ReplaceAll(guid, "-", "_")
				execFixture(t, ctx, db, fmt.Sprintf(`
					CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
					BEGIN
					    DELETE FROM proxy_interaction_covered_blocks
					    WHERE chain_id = 1 AND block_number = %d::numeric
					      AND block_hash = decode('%s', 'hex');
					    RETURN NEW;
					END
					$body$;
					CREATE TRIGGER test_withdraw_proxy_coverage_after_result
					AFTER INSERT ON verification_results
					FOR EACH ROW EXECUTE FUNCTION %s()`,
					functionName, blockARef.Number,
					strings.TrimPrefix(blockARef.Hash.Hex(), "0x"), functionName,
				))
				lease, found, claimErr := repository.Claim(ctx, "proxy-coverage-insert-race", time.Minute)
				if claimErr != nil || !found || lease.Job.ID != guid {
					t.Fatalf("claim insert-race binding: lease=%+v found=%t error=%v", lease, found, claimErr)
				}
				completeErr := repository.CompleteProxyV2(ctx, lease)
				if completeErr == nil || !strings.Contains(
					completeErr.Error(), "continuous observation-to-context coverage",
				) {
					t.Fatalf("binding insert coverage race error = %v", completeErr)
				}
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM verification_results WHERE job_id = $1::uuid`, 0, guid)
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM verified_proxy_bindings WHERE verification_job_id = $1::uuid`, 0, guid)
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM proxy_interaction_covered_blocks
					WHERE chain_id = 1 AND block_number = $1::numeric AND block_hash = $2`,
					1, blockARef.Number, blockARef.Hash.Bytes())
				if failErr := repository.Fail(ctx, lease, verify.ErrorTargetNotCanonical); failErr != nil {
					t.Fatalf("fail insert-race binding: %v", failErr)
				}
				return
			}
			if test.withdrawCoverage {
				queue, queueErr := enrich.NewPostgresJobQueue(db)
				if queueErr != nil {
					t.Fatal(queueErr)
				}
				word, parseErr := enrich.ParseWord(blockARef.Hash.String())
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				replayed, replayErr := queue.Enqueue(ctx, enrich.EnqueueRequest{
					Stage: enrich.StateDiffStage, ChainID: "1",
					BlockHash: word, BlockNumber: blockARef.Number,
					Replay: enrich.ReplaySource{
						Kind: "proxy-binding-coverage-test", Key: "withdraw-state-diff",
					},
				})
				if replayErr != nil || !replayed.Replayed {
					t.Fatalf("withdraw state-diff coverage: result=%+v error=%v", replayed, replayErr)
				}
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM published_block_stage_results
					WHERE chain_id = 1 AND block_hash = $1
					  AND stage = 'state_diff' AND stage_version = 1`,
					0, blockARef.Hash.Bytes(),
				)
				lease, found, claimErr := repository.Claim(ctx, "proxy-coverage-withdrawn", time.Minute)
				if claimErr != nil || !found || lease.Job.ID != guid {
					t.Fatalf("claim withdrawn-coverage binding: lease=%+v found=%t error=%v", lease, found, claimErr)
				}
				if completeErr := repository.CompleteProxyV2(ctx, lease); !errors.Is(completeErr, verify.ErrTargetNotCanonical) {
					t.Fatalf("complete after coverage withdrawal error = %v", completeErr)
				}
				if failErr := repository.Fail(ctx, lease, verify.ErrorTargetNotCanonical); failErr != nil {
					t.Fatalf("fail withdrawn-coverage binding: %v", failErr)
				}
				return
			}
			if test.completeBeforeABA {
				completeProxyVerification(t, ctx, repository, guid)
				assertProxyVerificationSource(t, ctx, backend, proxy, implementation, true)
			}
			commitCanonical(t, ctx, core, growth)
			publishEmptyProxyVerificationCoverage(t, ctx, db, mustBlockRef(t, growth))
			if test.completeBeforeABA {
				insertVerifiedContractFixture(
					t, ctx, db, admin.Bytes(), adminCodeHashA.Bytes(), blockARef.Number, nil,
					"0.8.30+commit.73712a01", "OrdinaryManagementImpostor", `[]`,
					`{"Fixture.sol":{"content":"contract Fixture {}"}}`,
					`{"optimizer":{"enabled":false,"runs":200}}`,
				)
				execFixture(t, ctx, db, `
					UPDATE verified_contracts SET valid_to_block = $2::numeric
					WHERE verification_job_id = $1::uuid`, adminArtifactJob, blockARef.Number,
				)
				assertProxyVerificationSource(t, ctx, backend, proxy, common.Address{}, false)
				if _, artifactErr := backend.Execute(ctx, etherscan.Request{
					Module: "contract", Action: "verifyproxycontract",
					Values: url.Values{"address": {proxy.Hex()}},
				}); !errors.Is(artifactErr, etherscan.ErrProxyVerificationTargetUnavailable) {
					t.Fatalf("ordinary management row impersonated closed artifact source: %v", artifactErr)
				}
				execFixture(t, ctx, db, `
					UPDATE verified_contracts SET valid_to_block = NULL
					WHERE verification_job_id = $1::uuid`, adminArtifactJob,
				)
				assertProxyVerificationSource(t, ctx, backend, proxy, implementation, true)

				before := proxyVerificationJobCount(t, ctx, db)
				reused, reuseErr := backend.Execute(ctx, etherscan.Request{
					Module: "contract", Action: "verifyproxycontract",
					Values: url.Values{
						"address":                {proxy.Hex()},
						"expectedimplementation": {implementation.Hex()},
					},
				})
				if reuseErr != nil || reused != guid {
					t.Fatalf("ordinary tip growth binding reuse = %#v, error=%v, want %s", reused, reuseErr, guid)
				}
				if after := proxyVerificationJobCount(t, ctx, db); after != before {
					t.Fatalf("ordinary tip growth created a binding job: before=%d after=%d", before, after)
				}
			}

			commitCanonical(t, ctx, core, blockB)
			blockBRef := mustBlockRef(t, blockB)
			insertProxyVerificationCode(t, ctx, db, blockBRef, admin, adminCodeHashB, adminCodeB)
			publishProxyVerificationInteractionCoverage(t, ctx, db, blockBRef,
				proxyVerificationCodeChange{address: admin, before: adminCodeA, after: adminCodeB},
			)
			publishPendingEmptyProxyVerificationCoverage(t, ctx, db, blockBRef,
				map[common.Address]proxyVerificationRPCState{admin: {code: adminCodeB}},
			)
			commitCanonical(t, ctx, core, blockAReturn)
			blockAReturnRef := mustBlockRef(t, blockAReturn)
			insertProxyVerificationCode(t, ctx, db, blockAReturnRef, admin, adminCodeHashA, adminCodeA)
			publishProxyVerificationInteractionCoverage(t, ctx, db, blockAReturnRef,
				proxyVerificationCodeChange{address: admin, before: adminCodeB, after: adminCodeA},
			)
			publishPendingEmptyProxyVerificationCoverage(t, ctx, db, blockAReturnRef,
				map[common.Address]proxyVerificationRPCState{admin: {code: adminCodeA}},
			)

			if test.completeBeforeABA {
				assertProxyVerificationSource(t, ctx, backend, proxy, common.Address{}, false)
				if _, stalePublicationErr := backend.Execute(ctx, etherscan.Request{
					Module: "contract", Action: "verifyproxycontract",
					Values: url.Values{
						"address":                {proxy.Hex()},
						"expectedimplementation": {implementation.Hex()},
					},
				}); !errors.Is(stalePublicationErr, etherscan.ErrProxyVerificationTargetUnavailable) {
					t.Fatalf("stale management publication survived A -> B -> A epoch: %v", stalePublicationErr)
				}
				reauthenticatedAdminJob := insertAuthenticatedProxyArtifactFixture(
					t, ctx, db, blockAReturnRef, generation, compilerDigest, executorDigest,
					admin, adminCodeHashA, adminCodeA, "proxy_admin", nil,
				)
				enqueueProxyVerificationArtifactReplay(
					t, ctx, db, blockAReturnRef, reauthenticatedAdminJob,
				)
				publishPendingEmptyProxyVerificationCoverage(t, ctx, db, blockAReturnRef,
					map[common.Address]proxyVerificationRPCState{admin: {code: adminCodeA}},
				)
				freshResult, freshErr := backend.Execute(ctx, etherscan.Request{
					Module: "contract", Action: "verifyproxycontract",
					Values: url.Values{
						"address":                {proxy.Hex()},
						"expectedimplementation": {implementation.Hex()},
					},
				})
				if freshErr != nil {
					t.Fatalf("submit fresh post-epoch binding after reauthentication: %v", freshErr)
				}
				freshGUID, _ := freshResult.(string)
				if freshGUID == guid {
					t.Fatalf("A -> B -> A revived old binding UUID %s", guid)
				}
				completeProxyVerification(t, ctx, repository, freshGUID)
				assertProxyVerificationSource(t, ctx, backend, proxy, implementation, true)
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM verified_proxy_bindings
					WHERE verification_job_id IN ($1::uuid, $2::uuid)`, 2, guid, freshGUID)

				commitCanonical(t, ctx, core, coverageGap)
				coverageGapRef := mustBlockRef(t, coverageGap)
				insertProxyVerificationCode(
					t, ctx, db, coverageGapRef, admin, adminCodeHashB, adminCodeB,
				)
				publishProxyVerificationInteractionCoverage(t, ctx, db, coverageGapRef,
					proxyVerificationCodeChange{address: admin, before: adminCodeA, after: adminCodeB},
				)
				publishPendingEmptyProxyVerificationCoverage(t, ctx, db, coverageGapRef,
					map[common.Address]proxyVerificationRPCState{admin: {code: adminCodeB}},
				)

				commitCanonical(t, ctx, core, coverageRecovery)
				coverageRecoveryRef := mustBlockRef(t, coverageRecovery)
				insertProxyVerificationCode(
					t, ctx, db, coverageRecoveryRef, admin, adminCodeHashA, adminCodeA,
				)
				publishProxyVerificationInteractionCoverage(t, ctx, db, coverageRecoveryRef,
					proxyVerificationCodeChange{address: admin, before: adminCodeB, after: adminCodeA},
				)
				publishPendingEmptyProxyVerificationCoverage(t, ctx, db, coverageRecoveryRef,
					map[common.Address]proxyVerificationRPCState{admin: {code: adminCodeA}},
				)
				enqueueProxyVerificationCoverageReplay(
					t, ctx, db, coverageGapRef, "missing-middle-proxy-stage",
				)
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM published_block_stage_results
					WHERE chain_id = 1 AND block_hash = $1
					  AND stage = 'proxy' AND stage_version = 2`,
					0, coverageGapRef.Hash.Bytes(),
				)
				assertProxyVerificationSource(t, ctx, backend, proxy, common.Address{}, false)
				if _, gapErr := backend.Execute(ctx, etherscan.Request{
					Module: "contract", Action: "verifyproxycontract",
					Values: url.Values{"address": {proxy.Hex()}},
				}); !errors.Is(gapErr, etherscan.ErrProxyVerificationTargetUnavailable) {
					t.Fatalf("binding submission with uncovered middle block error = %v", gapErr)
				}
				publishPendingEmptyProxyVerificationCoverage(
					t, ctx, db, coverageGapRef,
					map[common.Address]proxyVerificationRPCState{
						admin: {code: adminCodeB},
					},
				)
				if _, staleRecoveryErr := backend.Execute(ctx, etherscan.Request{
					Module: "contract", Action: "verifyproxycontract",
					Values: url.Values{"address": {proxy.Hex()}},
				}); !errors.Is(staleRecoveryErr, etherscan.ErrProxyVerificationTargetUnavailable) {
					t.Fatalf("post-metamorphism binding reused pre-epoch publication: %v", staleRecoveryErr)
				}
				recoveredAdminJob := insertAuthenticatedProxyArtifactFixture(
					t, ctx, db, coverageRecoveryRef, generation, compilerDigest, executorDigest,
					admin, adminCodeHashA, adminCodeA, "proxy_admin", nil,
				)
				enqueueProxyVerificationArtifactReplay(
					t, ctx, db, coverageRecoveryRef, recoveredAdminJob,
				)
				publishPendingEmptyProxyVerificationCoverage(t, ctx, db, coverageRecoveryRef,
					map[common.Address]proxyVerificationRPCState{admin: {code: adminCodeA}},
				)
				recoveredResult, recoveredErr := backend.Execute(ctx, etherscan.Request{
					Module: "contract", Action: "verifyproxycontract",
					Values: url.Values{"address": {proxy.Hex()}},
				})
				if recoveredErr != nil {
					t.Fatalf("submit fresh binding after intermediate coverage gap: %v", recoveredErr)
				}
				recoveredGUID, _ := recoveredResult.(string)
				if recoveredGUID == freshGUID || recoveredGUID == guid {
					t.Fatalf("coverage gap reused stale binding UUID: old=%s fresh=%s recovered=%s", guid, freshGUID, recoveredGUID)
				}
				completeProxyVerification(t, ctx, repository, recoveredGUID)
				assertProxyVerificationSource(t, ctx, backend, proxy, implementation, true)
				return
			}
			lease, found, err := repository.Claim(ctx, "proxy-code-epoch", time.Minute)
			if err != nil || !found || lease.Job.ID != guid {
				t.Fatalf("claim queued code epoch binding: lease=%+v found=%t error=%v", lease, found, err)
			}
			if err := repository.CompleteProxyV2(ctx, lease); !errors.Is(err, verify.ErrTargetNotCanonical) {
				t.Fatalf("complete queued A -> B -> A binding error = %v", err)
			}
			if err := repository.Fail(ctx, lease, verify.ErrorTargetNotCanonical); err != nil {
				t.Fatalf("fail stale code epoch binding: %v", err)
			}
			assertRowCount(t, ctx, db, `
				SELECT count(*) FROM verified_proxy_bindings
				WHERE verification_job_id = $1::uuid`, 0, guid)
		})
	}
}

func TestProxyVerificationManagementRequiresOpenZeppelinAttestation(t *testing.T) {
	tests := []struct {
		name           string
		pattern        string
		proxyKind      string
		artifactKind   string
		managementKind string
		addressSeed    uint64
	}{
		{
			name: "transparent proxy admin", pattern: "transparent",
			proxyKind: "eip1967", artifactKind: "transparent_proxy",
			managementKind: "proxy_admin", addressSeed: 9_540,
		},
		{
			name: "beacon proxy upgradeable beacon", pattern: "beacon",
			proxyKind: "beacon", artifactKind: "beacon_proxy",
			managementKind: "upgradeable_beacon", addressSeed: 9_550,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			core, err := store.NewPostgresRepository(db)
			if err != nil {
				t.Fatal(err)
			}
			genesis := testBundle(
				0, testHash(95_400+uint64(index)*10), testHash(0),
				testHash(95_500+uint64(index)*10), "proxy-management-genesis",
			)
			block := testBundle(
				1, testHash(95_401+uint64(index)*10), genesis.Block.Hash(),
				testHash(95_501+uint64(index)*10), "proxy-management-block",
			)
			commitCanonical(t, ctx, core, genesis)
			commitCanonical(t, ctx, core, block)
			blockRef := mustBlockRef(t, block)

			proxy := testAddress(test.addressSeed)
			implementation := testAddress(test.addressSeed + 1)
			management := testAddress(test.addressSeed + 2)
			proxyCode := []byte{0x60, byte(0x30 + index), 0x60, 0x00}
			implementationCode := []byte{0x60, byte(0x40 + index)}
			managementCode := []byte{0x60, byte(0x50 + index)}
			proxyCodeHash := common.BytesToHash(crypto.Keccak256(proxyCode))
			implementationCodeHash := common.BytesToHash(crypto.Keccak256(implementationCode))
			managementCodeHash := common.BytesToHash(crypto.Keccak256(managementCode))
			insertProxyVerificationCode(
				t, ctx, db, blockRef, implementation, implementationCodeHash, implementationCode,
			)
			insertProxyVerificationCode(
				t, ctx, db, blockRef, management, managementCodeHash, managementCode,
			)
			generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
			insertAuthenticatedProxyArtifactFixture(
				t, ctx, db, blockRef, generation, compilerDigest, executorDigest,
				proxy, proxyCodeHash, proxyCode,
				test.artifactKind, &management,
			)
			insertProxyVerificationSource(
				t, ctx, db, implementation, implementationCodeHash, "Implementation",
			)
			// An ordinary source verification of the management contract is not
			// an OpenZeppelin 5.6.1 attestation and must never unlock writes.
			insertProxyVerificationSource(
				t, ctx, db, management, managementCodeHash, "Management",
			)

			states := map[common.Address]proxyVerificationRPCState{
				proxy: {
					code: proxyCode, implementation: &implementation,
				},
				implementation: {code: implementationCode},
				management:     {code: managementCode},
			}
			var admin, beacon *common.Address
			if test.pattern == "transparent" {
				admin = &management
			} else {
				beacon = &management
				states[proxy] = proxyVerificationRPCState{
					code: proxyCode, beacon: &management,
				}
				states[management] = proxyVerificationRPCState{
					code: managementCode, beaconImplementation: &implementation,
				}
			}
			publishAuthenticatedProxyState(t, ctx, db, blockRef, states)
			assertRowCount(t, ctx, db, `
				SELECT count(*) FROM proxy_artifact_resolutions
				WHERE chain_id = 1 AND proxy_address = $1
				  AND observation_block_hash = $2
				  AND proxy_pattern = $3 AND standard_version = '5.6.1'`,
				1, proxy.Bytes(), blockRef.Hash.Bytes(), test.pattern,
			)
			assertRowCount(t, ctx, db, `
				SELECT count(*) FROM verified_contract_proxy_artifacts
				WHERE chain_id = 1 AND address = $1 AND code_hash = $2`,
				0, management.Bytes(), managementCodeHash.Bytes(),
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
			before := proxyVerificationJobCount(t, ctx, db)
			if _, err := backend.Execute(ctx, etherscan.Request{
				Module: "contract", Action: "verifyproxycontract",
				Values: url.Values{
					"address":                {proxy.Hex()},
					"expectedimplementation": {implementation.Hex()},
				},
			}); !errors.Is(err, etherscan.ErrProxyVerificationTargetUnavailable) {
				t.Fatalf("ordinary verified %s submission error = %v", test.managementKind, err)
			}
			if got := proxyVerificationJobCount(t, ctx, db); got != before {
				t.Fatalf("unattested management submission created a job: before=%d after=%d", before, got)
			}

			direct := exactProxyVerificationSubmission(
				t, ctx, db, blockRef, proxy, proxyCodeHash,
				test.proxyKind, test.pattern, implementation, implementationCodeHash,
				admin, beacon, test.managementKind, &management,
			)
			job, created, err := repository.SubmitV2(ctx, direct)
			if err != nil || !created {
				t.Fatalf("submit direct %s fixture: created=%t error=%v", test.pattern, created, err)
			}
			lease, found, err := repository.Claim(ctx, "proxy-management-fail-closed", time.Minute)
			if err != nil || !found || lease.Job.ID != job.ID {
				t.Fatalf("claim direct %s fixture: lease=%+v found=%t error=%v", test.pattern, lease, found, err)
			}
			if err := repository.CompleteProxyV2(ctx, lease); !errors.Is(err, verify.ErrTargetNotCanonical) {
				t.Fatalf("complete with ordinary verified %s error = %v", test.managementKind, err)
			}
			if err := repository.Fail(ctx, lease, verify.ErrorTargetNotCanonical); err != nil {
				t.Fatalf("fail unattested %s proxy job: %v", test.managementKind, err)
			}
			assertRowCount(t, ctx, db, `
				SELECT count(*) FROM verification_results WHERE job_id = $1::uuid`, 0, job.ID)
			assertRowCount(t, ctx, db, `
				SELECT count(*) FROM verified_proxy_bindings WHERE verification_job_id = $1::uuid`, 0, job.ID)

			managementArtifactJob := insertAuthenticatedProxyArtifactFixture(
				t, ctx, db, blockRef, generation, compilerDigest, executorDigest,
				management, managementCodeHash, managementCode, test.managementKind, nil,
			)
			publishAuthenticatedProxyReplaySources(
				t, ctx, db, blockRef,
				&proxyVerificationRPCService{blockHash: blockRef.Hash, states: states},
				managementArtifactJob,
			)
			verifiedResult, err := backend.Execute(ctx, etherscan.Request{
				Module: "contract", Action: "verifyproxycontract",
				Values: url.Values{
					"address":                {proxy.Hex()},
					"expectedimplementation": {implementation.Hex()},
				},
			})
			if err != nil {
				t.Fatalf("submit exact %s binding: %v", test.pattern, err)
			}
			verifiedGUID, _ := verifiedResult.(string)
			completeProxyVerification(t, ctx, repository, verifiedGUID)

			reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
			if err != nil {
				t.Fatal(err)
			}
			detail, err := reader.Proxy(ctx, proxy.Hex())
			if err != nil {
				t.Fatalf("read exact %s proxy detail: %v", test.pattern, err)
			}
			if detail.Status != query.ProxyStatusVerified || detail.Management == nil ||
				detail.Management.Kind != test.managementKind {
				t.Fatalf("exact %s proxy detail=%+v", test.pattern, detail)
			}
			if test.pattern == "beacon" && detail.Management.AffectedProxyCount != "1" {
				t.Fatalf("Beacon affected proxy count=%q, want 1; detail=%+v", detail.Management.AffectedProxyCount, detail)
			}
			if test.pattern == "beacon" {
				sameIdentity := testBundle(
					2, testHash(95_552), block.Block.Hash(), testHash(95_652),
					"beacon-binding-same-identity",
				)
				commitCanonical(t, ctx, core, sameIdentity)
				sameIdentityRef := mustBlockRef(t, sameIdentity)
				insertProxyVerificationReplayTarget(
					t, ctx, db, sameIdentityRef, management, "beacon", managementArtifactJob,
				)
				publishAuthenticatedProxyState(t, ctx, db, sameIdentityRef,
					map[common.Address]proxyVerificationRPCState{
						management: {
							code: managementCode, beaconImplementation: &implementation,
						},
						implementation: {code: implementationCode},
					},
				)

				sameDetail, err := reader.Proxy(ctx, proxy.Hex())
				if err != nil {
					t.Fatalf("read Beacon binding after identical observation: %v", err)
				}
				if sameDetail.Status != query.ProxyStatusVerified ||
					sameDetail.BindingID != verifiedGUID || sameDetail.Implementation == nil ||
					common.HexToAddress(sameDetail.Implementation.Address) != implementation {
					t.Fatalf("identical later Beacon observation invalidated binding: %+v", sameDetail)
				}

				changedImplementationCode := []byte{0x60, 0x7f}
				changedIdentity := testBundle(
					3, testHash(95_553), sameIdentity.Block.Hash(), testHash(95_653),
					"beacon-binding-code-identity-change",
				)
				commitCanonical(t, ctx, core, changedIdentity)
				changedIdentityRef := mustBlockRef(t, changedIdentity)
				insertProxyVerificationReplayTarget(
					t, ctx, db, changedIdentityRef, management, "beacon", managementArtifactJob,
				)
				publishAuthenticatedProxyState(t, ctx, db, changedIdentityRef,
					map[common.Address]proxyVerificationRPCState{
						management: {
							code: managementCode, beaconImplementation: &implementation,
						},
						implementation: {code: changedImplementationCode},
					},
				)

				changedDetail, err := reader.Proxy(ctx, proxy.Hex())
				if err != nil {
					t.Fatalf("read Beacon binding after implementation code identity change: %v", err)
				}
				changedCodeHash := crypto.Keccak256Hash(changedImplementationCode)
				if changedDetail.Status != query.ProxyStatusDetectedUnverified ||
					changedDetail.BindingID != "" || changedDetail.Implementation == nil ||
					common.HexToAddress(changedDetail.Implementation.Address) != implementation ||
					common.HexToHash(changedDetail.Implementation.CodeHash) != changedCodeHash {
					t.Fatalf("changed Beacon implementation code identity retained binding: %+v", changedDetail)
				}
			}
		})
	}
}

func TestBadUUPSUUIDPersistsERC1967AndCannotFormUUPSBinding(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(95_600), testHash(0), testHash(95_700), "bad-uups-genesis")
	block := testBundle(1, testHash(95_601), genesis.Block.Hash(), testHash(95_701), "bad-uups-block")
	commitCanonical(t, ctx, core, genesis)
	commitCanonical(t, ctx, core, block)
	blockRef := mustBlockRef(t, block)

	proxy := testAddress(9_560)
	implementation := testAddress(9_561)
	proxyCode := []byte{0x60, 0x61, 0x60, 0x00}
	implementationCode := []byte{0x60, 0x62}
	proxyCodeHash := common.BytesToHash(crypto.Keccak256(proxyCode))
	implementationCodeHash := common.BytesToHash(crypto.Keccak256(implementationCode))
	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	insertAuthenticatedProxyArtifactFixture(
		t, ctx, db, blockRef, generation, compilerDigest, executorDigest,
		proxy, proxyCodeHash, proxyCode,
		"erc1967_proxy", nil,
	)
	uupsArtifactJob := insertAuthenticatedProxyArtifactFixture(
		t, ctx, db, blockRef, generation, compilerDigest, executorDigest,
		implementation, implementationCodeHash, implementationCode,
		"uups_implementation", &implementation,
	)
	wrongUUID := common.HexToHash("0xdeadbeef").Bytes()
	version := make([]byte, 96)
	version[31] = 32
	version[63] = 5
	copy(version[64:], []byte("5.0.0"))
	probeSelector := func(signature string) string {
		return hex.EncodeToString(crypto.Keccak256([]byte(signature))[:4])
	}
	publishAuthenticatedProxyState(t, ctx, db, blockRef, map[common.Address]proxyVerificationRPCState{
		proxy: {
			code: proxyCode, implementation: &implementation,
		},
		implementation: {
			code: implementationCode,
			probeResponses: map[string][]byte{
				probeSelector("proxiableUUID()"):             wrongUUID,
				probeSelector("UPGRADE_INTERFACE_VERSION()"): version,
			},
		},
	})
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_contract_proxy_artifacts
		WHERE chain_id = 1 AND address = $1 AND code_hash = $2
		  AND artifact_kind = 'uups_implementation'
		  AND runtime_immutable_address = $1`,
		1, implementation.Bytes(), implementationCodeHash.Bytes(),
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
		  AND observation.block_hash = $2
		  AND observation.verification_job_id = $3::uuid
		  AND observation.implementation_code_hash = $4
		  AND observation.probe_state = 'rejected'
		  AND observation.rejection_reason = 'proxiable_uuid_invalid'
		  AND observation.proxiable_uuid IS NULL
		  AND observation.upgrade_interface_version IS NULL`,
		1, implementation.Bytes(), blockRef.Hash.Bytes(), uupsArtifactJob,
		implementationCodeHash.Bytes(),
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_artifact_resolutions
		WHERE chain_id = 1 AND proxy_address = $1
		  AND observation_block_hash = $2
		  AND proxy_pattern = 'erc1967'
		  AND implementation_artifact_job_id IS NULL`,
		1, proxy.Bytes(), blockRef.Hash.Bytes(),
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_artifact_resolutions
		WHERE chain_id = 1 AND proxy_address = $1
		  AND observation_block_hash = $2
		  AND proxy_pattern = 'uups'`,
		0, proxy.Bytes(), blockRef.Hash.Bytes(),
	)
	enqueueProxyVerificationCoverageReplay(t, ctx, db, blockRef, "bad-uups-carry-forward")
	publishPendingEmptyProxyVerificationCoverage(t, ctx, db, blockRef)
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM uups_implementation_observations
		WHERE chain_id = 1
		  AND implementation_address = $1
		  AND block_hash = $2
		  AND verification_job_id = $3::uuid`,
		1, implementation.Bytes(), blockRef.Hash.Bytes(), uupsArtifactJob,
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM uups_implementation_observation_generations AS generation
		JOIN durable_stage_publications AS published
		  ON published.chain_id = generation.chain_id
		 AND published.block_hash = generation.observation_block_hash
		 AND published.stage = 'proxy'
		 AND published.stage_version = generation.observation_stage_version
		 AND published.job_id = generation.durable_job_id
		 AND published.job_generation = generation.job_generation
		 AND published.state = 'complete'
		WHERE generation.chain_id = 1
		  AND generation.implementation_address = $1
		  AND generation.observation_block_hash = $2
		  AND generation.verification_job_id = $3::uuid`,
		2, implementation.Bytes(), blockRef.Hash.Bytes(), uupsArtifactJob,
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
	result, err := backend.Execute(ctx, etherscan.Request{
		Module: "contract", Action: "verifyproxycontract",
		Values: url.Values{
			"address":                {proxy.Hex()},
			"expectedimplementation": {implementation.Hex()},
		},
	})
	if err != nil {
		t.Fatalf("submit ERC1967 binding after bad UUPS UUID: %v", err)
	}
	guid, _ := result.(string)
	completeProxyVerification(t, ctx, repository, guid)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_proxy_bindings
		WHERE verification_job_id = $1::uuid
		  AND proxy_pattern = 'erc1967'
		  AND proxy_kind = 'eip1967'
		  AND management_kind = 'none'`, 1, guid)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_proxy_bindings
		WHERE chain_id = 1 AND proxy_address = $1 AND proxy_pattern = 'uups'`,
		0, proxy.Bytes(),
	)
}

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

func TestPublishedNegativeDetectionShadowsCurrentProxyBindings(t *testing.T) {
	tests := []struct {
		name        string
		candidate   string
		pattern     string
		artifact    string
		addressSeed uint64
	}{
		{
			name: "proxy implementation slot becomes invalid", candidate: "proxy",
			pattern: "erc1967", artifact: "erc1967_proxy", addressSeed: 9_600,
		},
		{
			name: "beacon implementation becomes invalid", candidate: "beacon",
			pattern: "beacon", artifact: "beacon_proxy", addressSeed: 9_620,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			core, err := store.NewPostgresRepository(db)
			if err != nil {
				t.Fatal(err)
			}
			seed := uint64(96_000 + index*100)
			genesis := testBundle(0, testHash(seed), testHash(0), testHash(seed+10), "negative-shadow-genesis")
			blockOne := testBundle(1, testHash(seed+1), genesis.Block.Hash(), testHash(seed+11), "negative-shadow-one")
			oldTwo := testBundle(2, testHash(seed+2), blockOne.Block.Hash(), testHash(seed+12), "negative-shadow-old-two")
			newTwo := testBundle(2, testHash(seed+22), blockOne.Block.Hash(), testHash(seed+32), "negative-shadow-new-two")
			oldThree := testBundle(3, testHash(seed+3), newTwo.Block.Hash(), testHash(seed+13), "negative-shadow-old-three")
			newThree := testBundle(3, testHash(seed+23), newTwo.Block.Hash(), testHash(seed+33), "negative-shadow-new-three")
			commitCanonical(t, ctx, core, genesis)
			commitCanonical(t, ctx, core, blockOne)
			blockOneRef := mustBlockRef(t, blockOne)

			proxy := testAddress(test.addressSeed)
			implementation := testAddress(test.addressSeed + 1)
			beacon := testAddress(test.addressSeed + 2)
			proxyCode := []byte{0x60, byte(0x81 + index), 0x60, 0x00}
			implementationCode := []byte{0x60, byte(0x91 + index)}
			beaconCode := []byte{0x60, byte(0xa1 + index)}
			proxyCodeHash := common.BytesToHash(crypto.Keccak256(proxyCode))
			implementationCodeHash := common.BytesToHash(crypto.Keccak256(implementationCode))
			beaconCodeHash := common.BytesToHash(crypto.Keccak256(beaconCode))
			generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
			var proxyImmutable *common.Address
			if test.pattern == "beacon" {
				proxyImmutable = &beacon
			}
			proxyArtifactJob := insertAuthenticatedProxyArtifactFixture(
				t, ctx, db, blockOneRef, generation, compilerDigest, executorDigest,
				proxy, proxyCodeHash, proxyCode, test.artifact, proxyImmutable,
			)
			insertProxyVerificationCode(
				t, ctx, db, blockOneRef, implementation, implementationCodeHash, implementationCode,
			)
			insertProxyVerificationSource(
				t, ctx, db, implementation, implementationCodeHash, "Implementation",
			)
			negativeAddress := proxy
			negativeCode := proxyCode
			negativeCodeHash := proxyCodeHash
			negativeSourceJob := proxyArtifactJob
			states := map[common.Address]proxyVerificationRPCState{
				proxy:          {code: proxyCode, implementation: &implementation},
				implementation: {code: implementationCode},
			}
			if test.pattern == "beacon" {
				beaconArtifactJob := insertAuthenticatedProxyArtifactFixture(
					t, ctx, db, blockOneRef, generation, compilerDigest, executorDigest,
					beacon, beaconCodeHash, beaconCode, "upgradeable_beacon", nil,
				)
				states[proxy] = proxyVerificationRPCState{code: proxyCode, beacon: &beacon}
				states[beacon] = proxyVerificationRPCState{
					code: beaconCode, beaconImplementation: &implementation,
				}
				negativeAddress = beacon
				negativeCode = beaconCode
				negativeCodeHash = beaconCodeHash
				negativeSourceJob = beaconArtifactJob
			}
			publishAuthenticatedProxyState(t, ctx, db, blockOneRef, states)

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
			result, err := backend.Execute(ctx, etherscan.Request{
				Module: "contract", Action: "verifyproxycontract",
				Values: url.Values{
					"address": {proxy.Hex()}, "expectedimplementation": {implementation.Hex()},
				},
			})
			if err != nil {
				t.Fatalf("submit exact %s binding: %v", test.pattern, err)
			}
			guid, _ := result.(string)
			lease, found, err := repository.Claim(ctx, "negative-shadow-completion", time.Minute)
			if err != nil || !found || lease.Job.ID != guid {
				t.Fatalf("claim exact %s binding: lease=%+v found=%t error=%v", test.pattern, lease, found, err)
			}

			commitCanonical(t, ctx, core, oldTwo)
			oldTwoRef := mustBlockRef(t, oldTwo)
			publishProxyVerificationInteractionCoverage(t, ctx, db, oldTwoRef)
			insertProxyVerificationReplayTarget(
				t, ctx, db, oldTwoRef, negativeAddress, test.candidate, negativeSourceJob,
			)
			publishAuthenticatedProxyReplaySources(
				t, ctx, db, oldTwoRef,
				&proxyVerificationRPCService{
					blockHash: oldTwoRef.Hash,
					states: map[common.Address]proxyVerificationRPCState{
						negativeAddress: {code: negativeCode},
					},
				},
				negativeSourceJob,
			)
			assertRowCount(t, ctx, db, `
				SELECT count(*)
				FROM proxy_detection_evidence AS evidence
				JOIN published_block_stage_results AS published
				  ON published.chain_id = evidence.chain_id
				 AND published.block_hash = evidence.block_hash
				 AND published.stage = 'proxy'
				 AND published.stage_version = evidence.stage_version
				 AND published.durable_job_id = evidence.durable_job_id
				 AND published.job_generation = evidence.job_generation
				 AND published.state = 'complete'
				WHERE evidence.chain_id = 1 AND evidence.address = $1
				  AND evidence.block_hash = $2 AND evidence.code_hash = $3
				  AND evidence.candidate_kind = $4 AND evidence.canonical`,
				1, negativeAddress.Bytes(), oldTwoRef.Hash.Bytes(), negativeCodeHash.Bytes(), test.candidate,
			)
			if completeErr := repository.CompleteProxyV2(ctx, lease); !errors.Is(completeErr, verify.ErrTargetNotCanonical) {
				t.Fatalf("complete %s binding under newer negative error = %v", test.pattern, completeErr)
			}

			applyDerivedReorg(
				t, ctx, core, blockOne, []chainbundle.Bundle{oldTwo},
				[]chainbundle.Bundle{newTwo}, "remove published negative proxy evidence",
			)
			newTwoRef := mustBlockRef(t, newTwo)
			publishEmptyProxyVerificationCoverage(t, ctx, db, newTwoRef)
			if err := repository.CompleteProxyV2(ctx, lease); err != nil {
				t.Fatalf("complete %s binding after negative reorg: %v", test.pattern, err)
			}
			assertProxyVerificationSource(t, ctx, backend, proxy, implementation, true)

			commitCanonical(t, ctx, core, oldThree)
			oldThreeRef := mustBlockRef(t, oldThree)
			publishProxyVerificationInteractionCoverage(t, ctx, db, oldThreeRef)
			insertProxyVerificationReplayTarget(
				t, ctx, db, oldThreeRef, negativeAddress, test.candidate, negativeSourceJob,
			)
			publishAuthenticatedProxyReplaySources(
				t, ctx, db, oldThreeRef,
				&proxyVerificationRPCService{
					blockHash: oldThreeRef.Hash,
					states: map[common.Address]proxyVerificationRPCState{
						negativeAddress: {code: negativeCode},
					},
				},
				negativeSourceJob,
			)
			assertProxyVerificationSource(t, ctx, backend, proxy, common.Address{}, false)
			before := proxyVerificationJobCount(t, ctx, db)
			if _, err := backend.Execute(ctx, etherscan.Request{
				Module: "contract", Action: "verifyproxycontract",
				Values: url.Values{
					"address": {proxy.Hex()}, "expectedimplementation": {implementation.Hex()},
				},
			}); !errors.Is(err, etherscan.ErrProxyVerificationTargetUnavailable) {
				t.Fatalf("%s admission under newer negative error = %v", test.pattern, err)
			}
			if got := proxyVerificationJobCount(t, ctx, db); got != before {
				t.Fatalf("negative-shadowed %s admission created a job: before=%d after=%d", test.pattern, before, got)
			}

			applyDerivedReorg(
				t, ctx, core, newTwo, []chainbundle.Bundle{oldThree},
				[]chainbundle.Bundle{newThree}, "restore binding after negative evidence reorg",
			)
			newThreeRef := mustBlockRef(t, newThree)
			publishEmptyProxyVerificationCoverage(t, ctx, db, newThreeRef)
			assertProxyVerificationSource(t, ctx, backend, proxy, implementation, true)
			reused, err := backend.Execute(ctx, etherscan.Request{
				Module: "contract", Action: "verifyproxycontract",
				Values: url.Values{
					"address": {proxy.Hex()}, "expectedimplementation": {implementation.Hex()},
				},
			})
			if err != nil || reused != guid {
				t.Fatalf("%s binding after negative reorg = %#v, error=%v, want %s", test.pattern, reused, err, guid)
			}
		})
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

func insertProxyVerificationObservation(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	proxy common.Address,
	proxyHash common.Hash,
	implementation common.Address,
	implementationHash common.Hash,
) {
	t.Helper()
	execFixture(t, ctx, db, `
		INSERT INTO proxy_observations (
			chain_id, proxy_address, block_number, block_hash, stage_version,
			proxy_code_hash, proxy_kind, proxy_pattern, implementation_address,
			implementation_code_hash, confidence, evidence_state, canonical
		) VALUES (
			1, $1, $2::numeric, $3, 2, $4, 'eip1167', 'clone', $5,
			$6, 'high', 'exact', TRUE
		)`,
		proxy.Bytes(), block.Number, block.Hash.Bytes(), proxyHash.Bytes(),
		implementation.Bytes(), implementationHash.Bytes())
}

func cloneRuntime(implementation common.Address) []byte {
	runtime := common.FromHex("0x363d3d373d3d3d363d73")
	runtime = append(runtime, implementation.Bytes()...)
	return append(runtime, common.FromHex("0x5af43d82803e903d91602b57fd5bf3")...)
}

func publishProxyVerificationObservation(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	proxy common.Address,
	proxyCode []byte,
	implementation common.Address,
	implementationCode []byte,
) {
	t.Helper()
	publishProxyVerificationInteractionCoverage(t, ctx, db, block, proxyVerificationCodeChange{
		address: proxy,
		after:   proxyCode,
	})
	result, err := db.ExecContext(ctx, `
		UPDATE transactional_outbox
		SET published_at = clock_timestamp()
		WHERE chain_id = 1
		  AND topic = 'core.block.canonical'
		  AND message_key = $1
		`, block.Hash.String())
	if err != nil {
		t.Fatalf("publish proxy verification core outbox: %v", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatalf("publish proxy verification core outbox rows=%d error=%v", affected, err)
	}
	states := map[string]map[string]proxyContractState{
		block.Hash.String(): {
			proxy.String():          {code: proxyCode},
			implementation.String(): {code: implementationCode},
		},
	}
	var callMu sync.Mutex
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint(t, "proxy-verification-state", states, nil, &callMu, make(map[string][]string)),
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(block.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word, BlockNumber: block.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "proxy-verification-publication", LeaseDuration: 2 * time.Second,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, worker)
	assertJobStatus(t, ctx, db, enqueued.Job.ID, "succeeded")
}

func insertProxyVerificationSource(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	address common.Address,
	codeHash common.Hash,
	name string,
) {
	t.Helper()
	insertVerifiedContractFixture(
		t, ctx, db, address.Bytes(), codeHash.Bytes(), 0, nil,
		"0.8.30+commit.73712a01", name, `[]`,
		`{"Fixture.sol":{"content":"contract Fixture {}"}}`,
		`{"optimizer":{"enabled":false,"runs":200}}`,
	)
}

func completeProxyVerification(
	t *testing.T,
	ctx context.Context,
	repository *verify.PostgresRepository,
	wantGUID string,
) {
	t.Helper()
	lease, found, err := repository.Claim(ctx, "proxy-verification-test", time.Minute)
	if err != nil || !found || lease.Job.ID != wantGUID {
		t.Fatalf("claim proxy verification: lease=%+v found=%t error=%v", lease, found, err)
	}
	if err := repository.CompleteProxyV2(ctx, lease); err != nil {
		t.Fatalf("complete proxy verification: %v", err)
	}
}

func proxyVerificationJobCount(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	var count int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM verification_jobs WHERE kind = 'proxy'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func waitForProxyCoverageLockWaiters(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	want int,
) {
	t.Helper()
	for {
		var waiters int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*) FROM pg_locks
			WHERE locktype = 'advisory' AND granted = FALSE`).Scan(&waiters); err != nil {
			t.Fatal(err)
		}
		if waiters >= want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %d proxy coverage advisory lock waiters: %v", want, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func assertProxyVerificationSource(
	t *testing.T,
	ctx context.Context,
	backend etherscan.Backend,
	proxy, implementation common.Address,
	wantBound bool,
) {
	t.Helper()
	result, err := backend.Execute(ctx, etherscan.Request{
		Module: "contract", Action: "getsourcecode",
		Values: url.Values{"address": {proxy.Hex()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Proxy          string `json:"Proxy"`
		Implementation string `json:"Implementation"`
	}
	if err := json.Unmarshal(encoded, &rows); err != nil || len(rows) != 1 {
		t.Fatalf("decode proxy source: rows=%#v error=%v", rows, err)
	}
	if wantBound {
		if rows[0].Proxy != "1" || !strings.EqualFold(rows[0].Implementation, implementation.Hex()) {
			t.Fatalf("proxy source = %#v, want %s", rows[0], implementation.Hex())
		}
	} else if rows[0].Proxy != "0" || rows[0].Implementation != "" {
		t.Fatalf("stale proxy source remained public: %#v", rows[0])
	}
}

type proxyVerificationRPCState struct {
	code                 []byte
	implementation       *common.Address
	admin                *common.Address
	beacon               *common.Address
	beaconImplementation *common.Address
	probeResponses       map[string][]byte
}

type proxyVerificationRPCService struct {
	blockHash  common.Hash
	states     map[common.Address]proxyVerificationRPCState
	mu         sync.Mutex
	codeCalls  map[common.Address]int
	storeCalls map[common.Address]int
	callCalls  map[common.Address]int
}

func (service *proxyVerificationRPCService) record(
	calls *map[common.Address]int,
	address common.Address,
) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if *calls == nil {
		*calls = make(map[common.Address]int)
	}
	(*calls)[address]++
}

func (service *proxyVerificationRPCService) callCount(
	calls map[common.Address]int,
	address common.Address,
) int {
	service.mu.Lock()
	defer service.mu.Unlock()
	return calls[address]
}

func (service *proxyVerificationRPCService) GetCode(
	_ context.Context,
	address common.Address,
	blockReference rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	if err := service.requireBlock(blockReference); err != nil {
		return nil, err
	}
	service.record(&service.codeCalls, address)
	return hexutil.Bytes(common.CopyBytes(service.states[address].code)), nil
}

func (service *proxyVerificationRPCService) GetStorageAt(
	_ context.Context,
	address common.Address,
	slot common.Hash,
	blockReference rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	if err := service.requireBlock(blockReference); err != nil {
		return nil, err
	}
	service.record(&service.storeCalls, address)
	state := service.states[address]
	word := make([]byte, common.HashLength)
	var value *common.Address
	switch slot {
	case enrich.EIP1967ImplementationSlot:
		value = state.implementation
	case enrich.EIP1967AdminSlot:
		value = state.admin
	case enrich.EIP1967BeaconSlot:
		value = state.beacon
	}
	if value != nil {
		copy(word[12:], value.Bytes())
	}
	return hexutil.Bytes(word), nil
}

func (service *proxyVerificationRPCService) Call(
	_ context.Context,
	request map[string]any,
	blockReference rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	if err := service.requireBlock(blockReference); err != nil {
		return nil, err
	}
	addressText, ok := request["to"].(string)
	if !ok || !common.IsHexAddress(addressText) {
		return nil, errors.New("decode proxy verification call address")
	}
	address := common.HexToAddress(addressText)
	service.record(&service.callCalls, address)
	state := service.states[address]
	if state.beaconImplementation != nil {
		word := make([]byte, common.HashLength)
		copy(word[12:], state.beaconImplementation.Bytes())
		return hexutil.Bytes(word), nil
	}
	data, err := proxyVerificationCallData(request["data"])
	if err != nil {
		return nil, err
	}
	if len(data) < 4 {
		return nil, errors.New("proxy verification call selector is missing")
	}
	response, found := state.probeResponses[hex.EncodeToString(data[:4])]
	if !found {
		return nil, errors.New("execution reverted")
	}
	return hexutil.Bytes(common.CopyBytes(response)), nil
}

func (service *proxyVerificationRPCService) requireBlock(
	blockReference rpc.BlockNumberOrHash,
) error {
	if blockReference.BlockHash == nil || !blockReference.RequireCanonical ||
		*blockReference.BlockHash != service.blockHash {
		return errors.New("proxy verification RPC was not pinned to the canonical block hash")
	}
	return nil
}

func proxyVerificationCallData(value any) ([]byte, error) {
	switch typed := value.(type) {
	case hexutil.Bytes:
		return common.CopyBytes(typed), nil
	case string:
		decoded, err := hexutil.Decode(typed)
		if err != nil {
			return nil, errors.New("decode proxy verification call data")
		}
		return decoded, nil
	default:
		return nil, errors.New("proxy verification call data has an invalid type")
	}
}

func insertAuthenticatedProxyArtifactFixture(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	generation int64,
	compilerDigest [sha256.Size]byte,
	executorDigest [sha256.Size]byte,
	address common.Address,
	codeHash common.Hash,
	code []byte,
	artifactKind string,
	runtimeImmutable *common.Address,
) string {
	t.Helper()
	fixture := proxyArtifactAttestationFixture{
		Address: address, Runtime: code, Kind: artifactKind, Version: "5.6.1",
		Manifest: sha256Bytes("binding-fixture:" + artifactKind + ":" + address.Hex()),
	}
	if runtimeImmutable != nil {
		fixture.Immutable = runtimeImmutable.Bytes()
	}
	identity, err := insertProxyArtifactAttestationSource(
		ctx, db, block, generation, compilerDigest, executorDigest, fixture,
	)
	if err != nil {
		t.Fatalf("insert authenticated %s source: %v", artifactKind, err)
	}
	if common.BytesToHash(identity.CodeHash) != codeHash {
		t.Fatalf("authenticated %s code hash = %x, want %s", artifactKind, identity.CodeHash, codeHash)
	}
	if _, err := insertProxyArtifactPublication(
		ctx, db, fixture, identity, block.Number,
	); err != nil {
		t.Fatalf("publish authenticated %s artifact: %v", artifactKind, err)
	}
	targetKind := "proxy"
	switch artifactKind {
	case "upgradeable_beacon":
		targetKind = "beacon"
	case "uups_implementation":
		targetKind = "uups"
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO proxy_replay_targets (
			chain_id, block_number, block_hash, address, target_kind,
			source_kind, source_verification_job_id
		) VALUES (
			1, $1::numeric, $2, $3, $4,
			'verification_publication', $5::uuid
		)`, block.Number, block.Hash.Bytes(), address.Bytes(), targetKind, identity.JobID,
	); err != nil {
		t.Fatalf("insert proxy artifact replay target: %v", err)
	}
	return identity.JobID
}

func insertProxyVerificationReplayTarget(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	address common.Address,
	targetKind string,
	verificationJobID string,
) {
	t.Helper()
	execFixture(t, ctx, db, `
		INSERT INTO proxy_replay_targets (
			chain_id, block_number, block_hash, address, target_kind,
			source_kind, source_verification_job_id
		) VALUES (
			1, $1::numeric, $2, $3, $4,
			'verification_publication', $5::uuid
		)`, block.Number, block.Hash.Bytes(), address.Bytes(), targetKind, verificationJobID,
	)
}

func publishAuthenticatedProxyState(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	states map[common.Address]proxyVerificationRPCState,
) {
	t.Helper()
	publishProxyVerificationInteractionCoverage(t, ctx, db, block)
	result, err := db.ExecContext(ctx, `
		UPDATE transactional_outbox
		SET published_at = clock_timestamp()
		WHERE chain_id = 1
		  AND topic = 'core.block.canonical'
		  AND message_key = $1`, block.Hash.String())
	if err != nil {
		t.Fatalf("publish authenticated proxy fixture core outbox: %v", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatalf("publish authenticated proxy fixture core outbox rows=%d error=%v", affected, err)
	}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "authenticated-proxy-state",
		Client: newIntegrationRPCClient(t, "eth", &proxyVerificationRPCService{
			blockHash: block.Hash, states: states,
		}),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(block.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT source_verification_job_id::text
		FROM proxy_replay_targets
		WHERE chain_id = 1 AND block_number = $1::numeric AND block_hash = $2
		ORDER BY source_verification_job_id::text`, block.Number, block.Hash.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var job enrich.EnqueueResult
	for rows.Next() {
		var sourceJobID string
		if err := rows.Scan(&sourceJobID); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		job, err = queue.Enqueue(ctx, enrich.EnqueueRequest{
			Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word,
			BlockNumber: block.Number,
			Replay: enrich.ReplaySource{
				Kind: "verification-publication", Key: sourceJobID,
			},
		})
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if job.Job.ID == "" {
		job, err = queue.Enqueue(ctx, enrich.EnqueueRequest{
			Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word,
			BlockNumber: block.Number,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "authenticated-proxy-state", LeaseDuration: 2 * time.Second,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, worker)
	assertJobStatus(t, ctx, db, job.Job.ID, "succeeded")
}

func publishAuthenticatedProxyReplaySources(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	service *proxyVerificationRPCService,
	sourceJobIDs ...string,
) {
	t.Helper()
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name:     "authenticated-proxy-replay",
		Client:   newIntegrationRPCClient(t, "eth", service),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(block.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	var queued enrich.EnqueueResult
	for _, sourceJobID := range sourceJobIDs {
		queued, err = queue.Enqueue(ctx, enrich.EnqueueRequest{
			Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word,
			BlockNumber: block.Number,
			Replay: enrich.ReplaySource{
				Kind: "verification-publication", Key: sourceJobID,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if queued.Job.ID == "" {
		t.Fatal("authenticated proxy replay did not queue a durable job")
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "authenticated-proxy-replay", LeaseDuration: 2 * time.Second,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, worker)
	assertJobStatus(t, ctx, db, queued.Job.ID, "succeeded")
}

type proxyVerificationCodeChange struct {
	address common.Address
	before  []byte
	after   []byte
}

type proxyVerificationCoverageRPCService struct {
	db        *sql.DB
	stateDiff json.RawMessage
}

func (service *proxyVerificationCoverageRPCService) TraceBlockByHash(
	ctx context.Context,
	blockHash common.Hash,
	options map[string]any,
) (json.RawMessage, error) {
	if options["tracer"] == "prestateTracer" {
		return marshalDatabaseBlockTraceResults(ctx, service.db, blockHash, func(common.Hash) (json.RawMessage, error) {
			return service.stateDiff, nil
		})
	}
	return (&derivedTraceService{db: service.db}).TraceBlockByHash(ctx, blockHash, options)
}

func publishProxyVerificationInteractionCoverage(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	codeChanges ...proxyVerificationCodeChange,
) {
	t.Helper()
	pre := make(map[string]any)
	post := make(map[string]any)
	for _, change := range codeChanges {
		if change.address == (common.Address{}) {
			t.Fatal("proxy interaction coverage code change address is empty")
		}
		if change.before != nil {
			pre[change.address.Hex()] = map[string]any{"code": hexutil.Encode(change.before)}
		}
		if change.after != nil {
			post[change.address.Hex()] = map[string]any{"code": hexutil.Encode(change.after)}
		}
	}
	stateDiffPayload, err := json.Marshal(map[string]any{"pre": pre, "post": post})
	if err != nil {
		t.Fatalf("marshal proxy interaction coverage state diff: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE transactional_outbox
		SET published_at = clock_timestamp()
		WHERE chain_id = 1
		  AND topic = 'core.block.canonical'
		  AND message_key = $1`, block.Hash.String(),
	); err != nil {
		t.Fatalf("publish proxy interaction coverage core outbox: %v", err)
	}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "proxy-verification-coverage",
		Client: newIntegrationRPCClient(t, "debug", &proxyVerificationCoverageRPCService{
			db: db, stateDiff: stateDiffPayload,
		}),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeTrace: true},
		Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{
			ethrpc.CapabilityDebugTrace: ethrpc.AvailabilityAvailable,
		}},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	trace, err := enrich.NewTraceRPCProcessor(db, pool, enrich.TraceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	stateDiff, err := enrich.NewStateDiffRPCProcessor(db, pool, enrich.StateDiffLimits{})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(block.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	// StateDiff must publish before Trace so the latter consumes the exact
	// transaction-time execution identity without immediately needing a replay.
	for _, stage := range []enrich.StageID{enrich.StateDiffStage, enrich.TraceStage} {
		result, enqueueErr := queue.Enqueue(ctx, enrich.EnqueueRequest{
			Stage: stage, ChainID: "1", BlockHash: word, BlockNumber: block.Number,
		})
		if enqueueErr != nil || !result.Created {
			t.Fatalf("enqueue proxy interaction coverage %s: result=%+v error=%v", stage, result, enqueueErr)
		}
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{trace, stateDiff}, enrich.WorkerOptions{
		ID:            "proxy-verification-coverage-" + block.Hash.Hex()[2:10],
		LeaseDuration: 2 * time.Second,
		PollInterval:  time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		processOne(t, ctx, worker)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM published_block_stage_results
		WHERE chain_id = 1 AND block_hash = $1
		  AND state = 'complete'
		  AND (stage, stage_version) IN (('trace', 3), ('state_diff', 2))`,
		2, block.Hash.Bytes(),
	)
}

func publishEmptyProxyVerificationCoverage(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
) {
	t.Helper()
	publishProxyVerificationInteractionCoverage(t, ctx, db, block)
	publishPendingEmptyProxyVerificationCoverage(t, ctx, db, block)
}

func enqueueProxyVerificationCoverageReplay(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	key string,
) {
	t.Helper()
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(block.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word,
		BlockNumber: block.Number,
		Replay:      enrich.ReplaySource{Kind: "proxy-binding-coverage-test", Key: key},
	})
	if err != nil || !replayed.Replayed {
		t.Fatalf("enqueue proxy coverage replay: result=%+v error=%v", replayed, err)
	}
}

func enqueueProxyVerificationArtifactReplay(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	verificationJobID string,
) {
	t.Helper()
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(block.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word,
		BlockNumber: block.Number,
		Replay: enrich.ReplaySource{
			Kind: "verification-publication", Key: verificationJobID,
		},
	})
	if err != nil || !replayed.Replayed {
		t.Fatalf("enqueue proxy artifact replay: result=%+v error=%v", replayed, err)
	}
}

func publishPendingEmptyProxyVerificationCoverage(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	stateFixtures ...map[common.Address]proxyVerificationRPCState,
) {
	t.Helper()
	states := map[common.Address]proxyVerificationRPCState{}
	if len(stateFixtures) > 0 {
		states = stateFixtures[0]
	}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "empty-proxy-verification-coverage",
		Client: newIntegrationRPCClient(t, "eth", &proxyVerificationRPCService{
			blockHash: block.Hash,
			states:    states,
		}),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(block.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word, BlockNumber: block.Number,
	}); err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID:            "empty-proxy-coverage-" + block.Hash.Hex()[2:10],
		LeaseDuration: 2 * time.Second,
		PollInterval:  time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, worker)
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM published_block_stage_results
		WHERE chain_id = 1 AND block_hash = $1
		  AND stage = 'proxy' AND stage_version = 2 AND state = 'complete'`,
		1, block.Hash.Bytes(),
	)
}

func exactProxyVerificationSubmission(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	proxy common.Address,
	proxyCodeHash common.Hash,
	kind, pattern string,
	implementation common.Address,
	implementationCodeHash common.Hash,
	admin, beacon *common.Address,
	managementKind string,
	management *common.Address,
) verify.SubmissionV2 {
	t.Helper()
	var observationGeneration, artifactResolution int64
	var beaconGeneration sql.NullInt64
	if err := db.QueryRowContext(ctx, `
		SELECT observation_generation.id, resolution.id,
		       beacon_generation.id
		FROM proxy_observations AS observation
		JOIN proxy_observation_generations AS observation_generation
		  ON observation_generation.chain_id = observation.chain_id
		 AND observation_generation.proxy_address = observation.proxy_address
		 AND observation_generation.observation_block_hash = observation.block_hash
		 AND observation_generation.observation_stage_version = observation.stage_version
		JOIN published_block_stage_results AS published
		  ON published.chain_id = observation_generation.chain_id
		 AND published.block_hash = observation_generation.observation_block_hash
		 AND published.stage = 'proxy'
		 AND published.stage_version = observation_generation.observation_stage_version
		 AND published.durable_job_id = observation_generation.durable_job_id
		 AND published.job_generation = observation_generation.job_generation
		JOIN proxy_artifact_resolutions AS resolution
		  ON resolution.chain_id = observation.chain_id
		 AND resolution.proxy_address = observation.proxy_address
		 AND resolution.observation_block_hash = observation.block_hash
		 AND resolution.observation_stage_version = observation.stage_version
		 AND resolution.durable_job_id = observation_generation.durable_job_id
		 AND resolution.job_generation = observation_generation.job_generation
		LEFT JOIN beacon_observation_generations AS beacon_generation
		  ON resolution.proxy_pattern = 'beacon'
		 AND beacon_generation.chain_id = resolution.chain_id
		 AND beacon_generation.beacon_address = resolution.beacon_address
		 AND beacon_generation.observation_block_hash = resolution.observation_block_hash
		 AND beacon_generation.observation_stage_version = resolution.observation_stage_version
		 AND beacon_generation.durable_job_id = resolution.durable_job_id
		 AND beacon_generation.job_generation = resolution.job_generation
		WHERE observation.chain_id = 1
		  AND observation.proxy_address = $1
		  AND observation.block_hash = $2
		  AND resolution.proxy_pattern = $3
		ORDER BY observation_generation.id DESC, resolution.id DESC
		LIMIT 1`, proxy.Bytes(), block.Hash.Bytes(), pattern,
	).Scan(&observationGeneration, &artifactResolution, &beaconGeneration); err != nil {
		t.Fatalf("query exact %s proxy generation: %v", pattern, err)
	}
	proxyTarget := &verify.ProxyVerificationTarget{
		Kind:                         kind,
		Pattern:                      pattern,
		StandardVersion:              "5.6.1",
		SubmissionContextBlockNumber: strconv.FormatUint(block.Number, 10),
		SubmissionContextBlockHash:   strings.ToLower(block.Hash.String()),
		ImplementationAddress:        strings.ToLower(implementation.Hex()),
		ImplementationCodeHash:       strings.ToLower(implementationCodeHash.Hex()),
		ManagementKind:               managementKind,
		ObservationGenerationID:      strconv.FormatInt(observationGeneration, 10),
		ArtifactResolutionID:         strconv.FormatInt(artifactResolution, 10),
		ExpectedImplementation:       strings.ToLower(implementation.Hex()),
	}
	if admin != nil {
		var codeHash []byte
		if err := db.QueryRowContext(ctx, `
			SELECT code_hash FROM contract_code_observations
			WHERE chain_id = 1 AND address = $1 AND block_hash = $2`,
			admin.Bytes(), block.Hash.Bytes(),
		).Scan(&codeHash); err != nil {
			t.Fatalf("query proxy admin code hash: %v", err)
		}
		proxyTarget.AdminAddress = strings.ToLower(admin.Hex())
		proxyTarget.AdminCodeHash = "0x" + hex.EncodeToString(codeHash)
	}
	if beacon != nil {
		var codeHash []byte
		if err := db.QueryRowContext(ctx, `
			SELECT code_hash FROM contract_code_observations
			WHERE chain_id = 1 AND address = $1 AND block_hash = $2`,
			beacon.Bytes(), block.Hash.Bytes(),
		).Scan(&codeHash); err != nil {
			t.Fatalf("query beacon code hash: %v", err)
		}
		proxyTarget.BeaconAddress = strings.ToLower(beacon.Hex())
		proxyTarget.BeaconCodeHash = "0x" + hex.EncodeToString(codeHash)
	}
	if management != nil {
		var codeHash []byte
		if err := db.QueryRowContext(ctx, `
			SELECT code_hash FROM contract_code_observations
			WHERE chain_id = 1 AND address = $1 AND block_hash = $2`,
			management.Bytes(), block.Hash.Bytes(),
		).Scan(&codeHash); err != nil {
			t.Fatalf("query proxy management code hash: %v", err)
		}
		proxyTarget.ManagementAddress = strings.ToLower(management.Hex())
		proxyTarget.ManagementCodeHash = "0x" + hex.EncodeToString(codeHash)
	}
	if beaconGeneration.Valid {
		proxyTarget.BeaconGenerationID = strconv.FormatInt(beaconGeneration.Int64, 10)
	}
	return verify.SubmissionV2{
		Kind: verify.JobProxy,
		Target: &verify.VerificationTarget{
			ChainID: 1, Address: strings.ToLower(proxy.Hex()),
			CodeHash:    strings.ToLower(proxyCodeHash.Hex()),
			AtBlockHash: strings.ToLower(block.Hash.Hex()),
		},
		ProxyTarget: proxyTarget,
	}
}
