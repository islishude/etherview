//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func TestUUPSBindingFencingRejectsNewerPublishedRejection(t *testing.T) {
	fixture := newUUPSBindingFencingFixture(t, 171_000)

	currentGUID := fixture.submit(t, fixture.proxies[0])
	completeProxyVerification(t, fixture.ctx, fixture.repository, currentGUID)
	assertProxyVerificationSource(
		t, fixture.ctx, fixture.backend, fixture.proxies[0], fixture.implementation, true,
	)
	if reused := fixture.submit(t, fixture.proxies[0]); reused != currentGUID {
		t.Fatalf("current UUPS binding reuse = %s, want %s", reused, currentGUID)
	}

	pendingGUID := fixture.submit(t, fixture.proxies[1])
	pendingLease := fixture.claim(t, pendingGUID, "uups-newer-rejection")
	blockTwo := testBundle(
		2, testHash(171_002), fixture.initial.Block.Hash(), testHash(172_002),
		"uups-newer-rejection",
	)
	commitCanonical(t, fixture.ctx, fixture.core, blockTwo)
	blockTwoRef := mustBlockRef(t, blockTwo)
	publishProxyVerificationInteractionCoverage(t, fixture.ctx, fixture.db, blockTwoRef)
	insertProxyVerificationReplayTarget(
		t, fixture.ctx, fixture.db, blockTwoRef, fixture.implementation, "uups",
		fixture.uupsArtifactJob,
	)
	publishAuthenticatedProxyReplaySources(
		t, fixture.ctx, fixture.db, blockTwoRef,
		fixture.uupsProbeService(blockTwoRef, false), fixture.uupsArtifactJob,
	)

	assertRowCount(t, fixture.ctx, fixture.db, `
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
		  AND observation.implementation_code_hash = $3
		  AND observation.probe_state = 'rejected'
		  AND observation.rejection_reason = 'proxiable_uuid_invalid'`,
		1, fixture.implementation.Bytes(), blockTwoRef.Hash.Bytes(),
		fixture.implementationCodeHash.Bytes(),
	)
	if err := fixture.repository.CompleteProxyV2(fixture.ctx, pendingLease); !errors.Is(
		err, verify.ErrTargetNotCanonical,
	) {
		t.Fatalf("complete UUPS binding shadowed by newer rejection error = %v", err)
	}
	if err := fixture.repository.Fail(
		fixture.ctx, pendingLease, verify.ErrorTargetNotCanonical,
	); err != nil {
		t.Fatalf("fail UUPS job shadowed by newer rejection: %v", err)
	}
	assertProxyVerificationSource(
		t, fixture.ctx, fixture.backend, fixture.proxies[0], common.Address{}, false,
	)
	fixture.assertGenericFallback(t, fixture.proxies[0], currentGUID)
}

func TestUUPSBindingFencingRejectsSameBlockConflictingOutcomes(t *testing.T) {
	fixture := newUUPSBindingFencingFixture(t, 173_000)
	currentGUID := fixture.submit(t, fixture.proxies[0])
	completeProxyVerification(t, fixture.ctx, fixture.repository, currentGUID)

	blockTwo := testBundle(
		2, testHash(173_002), fixture.initial.Block.Hash(), testHash(174_002),
		"uups-same-block-conflict",
	)
	commitCanonical(t, fixture.ctx, fixture.core, blockTwo)
	blockTwoRef := mustBlockRef(t, blockTwo)
	secondArtifactJob := fixture.insertUUPSArtifact(t, blockTwoRef, "same-block-conflict")
	publishEmptyProxyVerificationCoverage(t, fixture.ctx, fixture.db, blockTwoRef)
	pendingGUID := fixture.submit(t, fixture.proxies[1])
	pendingJob := fixture.job(t, pendingGUID)
	if pendingJob.RequestV2.ProxyTarget.Pattern != "uups" ||
		pendingJob.RequestV2.ProxyTarget.UUPSGenerationID == "" {
		t.Fatalf("pre-conflict UUPS request = %+v", pendingJob.RequestV2.ProxyTarget)
	}
	pendingLease := fixture.claim(t, pendingGUID, "uups-same-block-conflict")
	blockThree := testBundle(
		3, testHash(173_003), blockTwo.Block.Hash(), testHash(174_003),
		"uups-same-block-conflict-publication",
	)
	commitCanonical(t, fixture.ctx, fixture.core, blockThree)
	blockThreeRef := mustBlockRef(t, blockThree)
	fixture.installSameLeaseConflictInjector(
		t, blockThreeRef, fixture.uupsArtifactJob, secondArtifactJob,
	)
	publishProxyVerificationInteractionCoverage(t, fixture.ctx, fixture.db, blockThreeRef)
	insertProxyVerificationReplayTarget(
		t, fixture.ctx, fixture.db, blockThreeRef, fixture.implementation, "uups",
		fixture.uupsArtifactJob,
	)
	publishAuthenticatedProxyReplaySources(
		t, fixture.ctx, fixture.db, blockThreeRef,
		fixture.uupsProbeService(blockThreeRef, true), fixture.uupsArtifactJob,
	)

	assertRowCount(t, fixture.ctx, fixture.db, `
		SELECT count(DISTINCT observation.probe_state)
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
		  AND observation.implementation_code_hash = $3
		  AND observation.canonical`,
		2, fixture.implementation.Bytes(), blockThreeRef.Hash.Bytes(),
		fixture.implementationCodeHash.Bytes(),
	)
	if err := fixture.repository.CompleteProxyV2(fixture.ctx, pendingLease); !errors.Is(
		err, verify.ErrTargetNotCanonical,
	) {
		t.Fatalf("complete UUPS binding under same-block conflict error = %v", err)
	}
	if err := fixture.repository.Fail(
		fixture.ctx, pendingLease, verify.ErrorTargetNotCanonical,
	); err != nil {
		t.Fatalf("fail same-block-conflicted UUPS job: %v", err)
	}
	assertProxyVerificationSource(
		t, fixture.ctx, fixture.backend, fixture.proxies[0], common.Address{}, false,
	)
	fixture.assertGenericFallback(t, fixture.proxies[0], currentGUID)
}

func TestUUPSBindingFencingCarriesCompatibleGenerationOnReplay(t *testing.T) {
	fixture := newUUPSBindingFencingFixture(t, 175_000)
	oldGUID := fixture.submit(t, fixture.proxies[0])
	oldJob := fixture.job(t, oldGUID)
	completeProxyVerification(t, fixture.ctx, fixture.repository, oldGUID)

	enqueueProxyVerificationCoverageReplay(
		t, fixture.ctx, fixture.db, fixture.initialRef, "compatible-uups-carry",
	)
	publishPendingEmptyProxyVerificationCoverage(
		t, fixture.ctx, fixture.db, fixture.initialRef,
	)
	assertRowCount(t, fixture.ctx, fixture.db, `
		SELECT count(*)
		FROM uups_implementation_observations
		WHERE chain_id = 1
		  AND implementation_address = $1
		  AND block_hash = $2
		  AND verification_job_id = $3::uuid
		  AND probe_state = 'compatible'`,
		1, fixture.implementation.Bytes(), fixture.initialRef.Hash.Bytes(),
		fixture.uupsArtifactJob,
	)
	assertRowCount(t, fixture.ctx, fixture.db, `
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
		2, fixture.implementation.Bytes(), fixture.initialRef.Hash.Bytes(),
		fixture.uupsArtifactJob,
	)

	assertProxyVerificationSource(
		t, fixture.ctx, fixture.backend, fixture.proxies[0], common.Address{}, false,
	)
	freshGUID := fixture.submit(t, fixture.proxies[0])
	if freshGUID == oldGUID {
		t.Fatalf("carried UUPS generation reused stale binding %s", oldGUID)
	}
	freshJob := fixture.job(t, freshGUID)
	if freshJob.RequestV2.ProxyTarget.Pattern != "uups" ||
		freshJob.RequestV2.ProxyTarget.UUPSGenerationID == "" ||
		freshJob.RequestV2.ProxyTarget.UUPSGenerationID ==
			oldJob.RequestV2.ProxyTarget.UUPSGenerationID {
		t.Fatalf(
			"carried UUPS request target = %+v, old generation=%s",
			freshJob.RequestV2.ProxyTarget,
			oldJob.RequestV2.ProxyTarget.UUPSGenerationID,
		)
	}
	completeProxyVerification(t, fixture.ctx, fixture.repository, freshGUID)
	assertProxyVerificationSource(
		t, fixture.ctx, fixture.backend, fixture.proxies[0], fixture.implementation, true,
	)
	if reused := fixture.submit(t, fixture.proxies[0]); reused != freshGUID {
		t.Fatalf("carried-generation UUPS binding reuse = %s, want %s", reused, freshGUID)
	}
}

func TestUUPSBindingFencingRejectsProbeBeforeReturnedCodeEpoch(t *testing.T) {
	fixture := newUUPSBindingFencingFixture(t, 177_000)
	currentGUID := fixture.submit(t, fixture.proxies[0])
	completeProxyVerification(t, fixture.ctx, fixture.repository, currentGUID)
	pendingGUID := fixture.submit(t, fixture.proxies[1])
	pendingLease := fixture.claim(t, pendingGUID, "uups-code-epoch")

	implementationCodeB := []byte{0x60, 0xb2, 0x60, 0x00}
	implementationCodeHashB := common.BytesToHash(crypto.Keccak256(implementationCodeB))
	blockTwo := testBundle(
		2, testHash(177_002), fixture.initial.Block.Hash(), testHash(178_002),
		"uups-code-epoch-b",
	)
	commitCanonical(t, fixture.ctx, fixture.core, blockTwo)
	blockTwoRef := mustBlockRef(t, blockTwo)
	insertProxyVerificationCode(
		t, fixture.ctx, fixture.db, blockTwoRef, fixture.implementation,
		implementationCodeHashB, implementationCodeB,
	)
	publishProxyVerificationInteractionCoverage(
		t, fixture.ctx, fixture.db, blockTwoRef, proxyVerificationCodeChange{
			address: fixture.implementation,
			before:  fixture.implementationCode,
			after:   implementationCodeB,
		},
	)
	publishPendingEmptyProxyVerificationCoverage(
		t, fixture.ctx, fixture.db, blockTwoRef,
		map[common.Address]proxyVerificationRPCState{
			fixture.implementation: {code: implementationCodeB},
		},
	)

	blockThree := testBundle(
		3, testHash(177_003), blockTwo.Block.Hash(), testHash(178_003),
		"uups-code-epoch-a-return",
	)
	commitCanonical(t, fixture.ctx, fixture.core, blockThree)
	blockThreeRef := mustBlockRef(t, blockThree)
	insertProxyVerificationCode(
		t, fixture.ctx, fixture.db, blockThreeRef, fixture.implementation,
		fixture.implementationCodeHash, fixture.implementationCode,
	)
	publishProxyVerificationInteractionCoverage(
		t, fixture.ctx, fixture.db, blockThreeRef, proxyVerificationCodeChange{
			address: fixture.implementation,
			before:  implementationCodeB,
			after:   fixture.implementationCode,
		},
	)
	publishPendingEmptyProxyVerificationCoverage(
		t, fixture.ctx, fixture.db, blockThreeRef,
		map[common.Address]proxyVerificationRPCState{
			fixture.implementation: {code: fixture.implementationCode},
		},
	)

	assertRowCount(t, fixture.ctx, fixture.db, `
		SELECT count(*)
		FROM transaction_state_changes AS change
		JOIN canonical_blocks AS canonical
		  ON canonical.chain_id = change.chain_id
		 AND canonical.number = change.block_number
		 AND canonical.block_hash = change.block_hash
		WHERE change.chain_id = 1 AND change.address = $1
		  AND change.field_kind = 'code' AND change.canonical
		  AND change.block_number IN (2, 3)
		  AND lower(change.before_value) IS DISTINCT FROM lower(change.after_value)`,
		2, fixture.implementation.Bytes(),
	)
	if err := fixture.repository.CompleteProxyV2(fixture.ctx, pendingLease); !errors.Is(
		err, verify.ErrTargetNotCanonical,
	) {
		t.Fatalf("complete UUPS binding after code A -> B -> A error = %v", err)
	}
	if err := fixture.repository.Fail(
		fixture.ctx, pendingLease, verify.ErrorTargetNotCanonical,
	); err != nil {
		t.Fatalf("fail code-epoch-stale UUPS job: %v", err)
	}
	assertProxyVerificationSource(
		t, fixture.ctx, fixture.backend, fixture.proxies[0], common.Address{}, false,
	)
	fixture.assertNoStaleUUPSAdmission(t, fixture.proxies[0], currentGUID)
}

func TestUUPSBindingFencingRejectsOrphanProbeGeneration(t *testing.T) {
	fixture := newUUPSBindingFencingFixtureWithInitialProbe(t, 179_000, false)
	oldTwo := testBundle(
		2, testHash(179_002), fixture.initial.Block.Hash(), testHash(180_002),
		"uups-probe-old-two",
	)
	commitCanonical(t, fixture.ctx, fixture.core, oldTwo)
	oldTwoRef := mustBlockRef(t, oldTwo)
	publishProxyVerificationInteractionCoverage(t, fixture.ctx, fixture.db, oldTwoRef)
	insertProxyVerificationReplayTarget(
		t, fixture.ctx, fixture.db, oldTwoRef, fixture.implementation, "uups",
		fixture.uupsArtifactJob,
	)
	publishAuthenticatedProxyReplaySources(
		t, fixture.ctx, fixture.db, oldTwoRef,
		fixture.uupsProbeService(oldTwoRef, true), fixture.uupsArtifactJob,
	)

	currentGUID := fixture.submit(t, fixture.proxies[0])
	completeProxyVerification(t, fixture.ctx, fixture.repository, currentGUID)
	assertProxyVerificationSource(
		t, fixture.ctx, fixture.backend, fixture.proxies[0], fixture.implementation, true,
	)
	pendingGUID := fixture.submit(t, fixture.proxies[1])
	pendingLease := fixture.claim(t, pendingGUID, "uups-probe-reorg")

	newTwo := testBundle(
		2, testHash(179_022), fixture.initial.Block.Hash(), testHash(180_022),
		"uups-probe-new-two",
	)
	applyDerivedReorg(
		t, fixture.ctx, fixture.core, fixture.initial,
		[]chainbundle.Bundle{oldTwo}, []chainbundle.Bundle{newTwo},
		"orphan UUPS probe generation",
	)
	newTwoRef := mustBlockRef(t, newTwo)
	publishEmptyProxyVerificationCoverage(t, fixture.ctx, fixture.db, newTwoRef)
	assertRowCount(t, fixture.ctx, fixture.db, `
		SELECT count(*)
		FROM uups_implementation_observations
		WHERE chain_id = 1
		  AND implementation_address = $1
		  AND block_hash = $2
		  AND verification_job_id = $3::uuid
		  AND canonical = FALSE`,
		1, fixture.implementation.Bytes(), oldTwoRef.Hash.Bytes(),
		fixture.uupsArtifactJob,
	)
	if err := fixture.repository.CompleteProxyV2(fixture.ctx, pendingLease); !errors.Is(
		err, verify.ErrTargetNotCanonical,
	) {
		t.Fatalf("complete UUPS binding after probe reorg error = %v", err)
	}
	if err := fixture.repository.Fail(
		fixture.ctx, pendingLease, verify.ErrorTargetNotCanonical,
	); err != nil {
		t.Fatalf("fail orphan-probe UUPS job: %v", err)
	}
	assertProxyVerificationSource(
		t, fixture.ctx, fixture.backend, fixture.proxies[0], common.Address{}, false,
	)
	fixture.assertGenericFallback(t, fixture.proxies[0], currentGUID)
}

type uupsBindingFencingFixture struct {
	ctx context.Context
	db  *sql.DB

	core       *store.PostgresRepository
	initial    chainbundle.Bundle
	initialRef store.BlockRef
	proxies    [2]common.Address

	implementation         common.Address
	implementationCode     []byte
	implementationCodeHash common.Hash
	uupsArtifactJob        string
	compilerGeneration     int64
	compilerDigest         [sha256.Size]byte
	executorDigest         [sha256.Size]byte
	repository             *verify.PostgresRepository
	backend                etherscan.Backend
}

func newUUPSBindingFencingFixture(t *testing.T, seed uint64) *uupsBindingFencingFixture {
	t.Helper()
	return newUUPSBindingFencingFixtureWithInitialProbe(t, seed, true)
}

func newUUPSBindingFencingFixtureWithInitialProbe(
	t *testing.T,
	seed uint64,
	initialCompatible bool,
) *uupsBindingFencingFixture {
	t.Helper()
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(
		0, testHash(seed), testHash(0), testHash(seed+1_000), "uups-fencing-genesis",
	)
	initial := testBundle(
		1, testHash(seed+1), genesis.Block.Hash(), testHash(seed+1_001),
		"uups-fencing-initial",
	)
	commitCanonical(t, ctx, core, genesis)
	commitCanonical(t, ctx, core, initial)
	initialRef := mustBlockRef(t, initial)

	proxies := [2]common.Address{testAddress(seed + 100), testAddress(seed + 101)}
	implementation := testAddress(seed + 102)
	proxyCode := []byte{0x60, byte(seed), 0x60, 0x00}
	implementationCode := []byte{0x60, byte(seed >> 8), 0x60, 0x00}
	proxyCodeHash := common.BytesToHash(crypto.Keccak256(proxyCode))
	implementationCodeHash := common.BytesToHash(crypto.Keccak256(implementationCode))
	compilerGeneration, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	for _, proxy := range proxies {
		insertAuthenticatedProxyArtifactFixture(
			t, ctx, db, initialRef, compilerGeneration, compilerDigest, executorDigest,
			proxy, proxyCodeHash, proxyCode, "erc1967_proxy", nil,
		)
	}
	uupsArtifactJob := insertAuthenticatedProxyArtifactFixture(
		t, ctx, db, initialRef, compilerGeneration, compilerDigest, executorDigest,
		implementation, implementationCodeHash, implementationCode,
		"uups_implementation", &implementation,
	)
	fixture := &uupsBindingFencingFixture{
		ctx: ctx, db: db, core: core, initial: initial, initialRef: initialRef,
		proxies:        proxies,
		implementation: implementation, implementationCode: implementationCode,
		implementationCodeHash: implementationCodeHash,
		uupsArtifactJob:        uupsArtifactJob, compilerGeneration: compilerGeneration,
		compilerDigest: compilerDigest, executorDigest: executorDigest,
	}
	states := map[common.Address]proxyVerificationRPCState{
		implementation: fixture.uupsState(initialCompatible),
	}
	for _, proxy := range proxies {
		states[proxy] = proxyVerificationRPCState{
			code: proxyCode, implementation: &implementation,
		}
	}
	publishAuthenticatedProxyState(t, ctx, db, initialRef, states)

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
	fixture.repository = repository
	fixture.backend = backend
	wantState := "rejected"
	if initialCompatible {
		wantState = "compatible"
	}
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
		  AND observation.probe_state = $4`,
		1, implementation.Bytes(), implementationCodeHash.Bytes(), uupsArtifactJob,
		wantState,
	)
	return fixture
}

func (fixture *uupsBindingFencingFixture) submit(
	t *testing.T,
	proxy common.Address,
) string {
	t.Helper()
	result, err := fixture.backend.Execute(fixture.ctx, etherscan.Request{
		Module: "contract", Action: "verifyproxycontract",
		Values: url.Values{
			"address":                {proxy.Hex()},
			"expectedimplementation": {fixture.implementation.Hex()},
		},
	})
	if err != nil {
		t.Fatalf("submit UUPS proxy %s: %v", proxy, err)
	}
	guid, ok := result.(string)
	if !ok || strings.TrimSpace(guid) == "" {
		t.Fatalf("submit UUPS proxy %s result = %#v", proxy, result)
	}
	return guid
}

func (fixture *uupsBindingFencingFixture) job(
	t *testing.T,
	guid string,
) verify.VerificationJob {
	t.Helper()
	job, found, err := fixture.repository.Job(fixture.ctx, guid)
	if err != nil || !found || job.RequestV2 == nil || job.RequestV2.ProxyTarget == nil {
		t.Fatalf("query proxy verification job %s: found=%t job=%+v error=%v", guid, found, job, err)
	}
	return job
}

func (fixture *uupsBindingFencingFixture) claim(
	t *testing.T,
	guid, worker string,
) verify.VerificationLease {
	t.Helper()
	lease, found, err := fixture.repository.Claim(fixture.ctx, worker, time.Minute)
	if err != nil || !found || lease.Job.ID != guid {
		t.Fatalf("claim proxy verification %s: lease=%+v found=%t error=%v", guid, lease, found, err)
	}
	return lease
}

func (fixture *uupsBindingFencingFixture) insertUUPSArtifact(
	t *testing.T,
	block store.BlockRef,
	manifestSuffix string,
) string {
	t.Helper()
	artifact := proxyArtifactAttestationFixture{
		Address: fixture.implementation, Runtime: fixture.implementationCode,
		Kind: "uups_implementation", Version: "5.6.1",
		Immutable: fixture.implementation.Bytes(),
		Manifest:  sha256Bytes("uups-binding-fencing:" + manifestSuffix),
	}
	identity, err := insertProxyArtifactAttestationSource(
		fixture.ctx, fixture.db, block, fixture.compilerGeneration,
		fixture.compilerDigest, fixture.executorDigest, artifact,
	)
	if err != nil {
		t.Fatalf("insert second exact UUPS artifact: %v", err)
	}
	if _, err := insertProxyArtifactPublication(
		fixture.ctx, fixture.db, artifact, identity, block.Number,
	); err != nil {
		t.Fatalf("publish second exact UUPS artifact: %v", err)
	}
	insertProxyVerificationReplayTarget(
		t, fixture.ctx, fixture.db, block, fixture.implementation, "uups", identity.JobID,
	)
	return identity.JobID
}

func (fixture *uupsBindingFencingFixture) installSameLeaseConflictInjector(
	t *testing.T,
	block store.BlockRef,
	compatibleArtifactJob, rejectedArtifactJob string,
) {
	t.Helper()
	functionName := fmt.Sprintf("inject_uups_conflict_%d", block.Number)
	execFixture(t, fixture.ctx, fixture.db, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
		    IF NEW.verification_job_id = '%s'::uuid
		       AND NEW.observation_block_hash = decode('%s', 'hex') THEN
		        INSERT INTO uups_implementation_observations (
		            chain_id, implementation_address, block_number, block_hash,
		            implementation_code_hash, verification_job_id,
		            stage_version, standard_version, probe_state,
		            rejection_reason, proxiable_uuid,
		            upgrade_interface_version, canonical
		        )
		        SELECT observation.chain_id, observation.implementation_address,
		               observation.block_number, observation.block_hash,
		               observation.implementation_code_hash, '%s'::uuid,
		               observation.stage_version, observation.standard_version,
		               'rejected', 'proxiable_uuid_invalid', NULL, NULL, TRUE
		        FROM uups_implementation_observations AS observation
		        WHERE observation.chain_id = NEW.chain_id
		          AND observation.implementation_address = NEW.implementation_address
		          AND observation.block_hash = NEW.observation_block_hash
		          AND observation.stage_version = NEW.observation_stage_version
		          AND observation.verification_job_id = NEW.verification_job_id
		        ON CONFLICT DO NOTHING;

		        INSERT INTO uups_implementation_observation_generations (
		            chain_id, implementation_address, observation_block_hash,
		            observation_stage_version, verification_job_id,
		            durable_job_id, job_generation
		        ) VALUES (
		            NEW.chain_id, NEW.implementation_address,
		            NEW.observation_block_hash, NEW.observation_stage_version,
		            '%s'::uuid, NEW.durable_job_id, NEW.job_generation
		        ) ON CONFLICT DO NOTHING;
		    END IF;
		    RETURN NEW;
		END
		$body$;
		CREATE TRIGGER inject_uups_same_lease_conflict
		AFTER INSERT ON uups_implementation_observation_generations
		FOR EACH ROW EXECUTE FUNCTION %s()`,
		functionName, compatibleArtifactJob,
		strings.TrimPrefix(block.Hash.Hex(), "0x"), rejectedArtifactJob,
		rejectedArtifactJob, functionName,
	))
}

func (fixture *uupsBindingFencingFixture) uupsProbeService(
	block store.BlockRef,
	compatible bool,
) *proxyVerificationRPCService {
	return &proxyVerificationRPCService{
		blockHash: block.Hash,
		states: map[common.Address]proxyVerificationRPCState{
			fixture.implementation: fixture.uupsState(compatible),
		},
	}
}

func (fixture *uupsBindingFencingFixture) uupsState(
	compatible bool,
) proxyVerificationRPCState {
	uuid := enrich.EIP1967ImplementationSlot.Bytes()
	if !compatible {
		uuid = common.HexToHash("0xdeadbeef").Bytes()
	}
	version := make([]byte, 96)
	version[31] = 32
	version[63] = 5
	copy(version[64:], []byte("5.0.0"))
	selector := func(signature string) string {
		return hex.EncodeToString(crypto.Keccak256([]byte(signature))[:4])
	}
	return proxyVerificationRPCState{
		code: fixture.implementationCode,
		probeResponses: map[string][]byte{
			selector("proxiableUUID()"):             uuid,
			selector("UPGRADE_INTERFACE_VERSION()"): version,
		},
	}
}

func (fixture *uupsBindingFencingFixture) assertGenericFallback(
	t *testing.T,
	proxy common.Address,
	staleGUID string,
) {
	t.Helper()
	guid := fixture.submit(t, proxy)
	if guid == staleGUID {
		t.Fatalf("stale UUPS binding %s was reused", staleGUID)
	}
	job := fixture.job(t, guid)
	if job.RequestV2.ProxyTarget.Pattern != "erc1967" ||
		job.RequestV2.ProxyTarget.UUPSGenerationID != "" {
		t.Fatalf("unsafe UUPS fallback request = %+v", job.RequestV2.ProxyTarget)
	}
}

func (fixture *uupsBindingFencingFixture) assertNoStaleUUPSAdmission(
	t *testing.T,
	proxy common.Address,
	staleGUID string,
) {
	t.Helper()
	result, err := fixture.backend.Execute(fixture.ctx, etherscan.Request{
		Module: "contract", Action: "verifyproxycontract",
		Values: url.Values{
			"address":                {proxy.Hex()},
			"expectedimplementation": {fixture.implementation.Hex()},
		},
	})
	if err != nil {
		if !errors.Is(err, etherscan.ErrProxyVerificationTargetUnavailable) &&
			!errors.Is(err, etherscan.ErrProxyImplementationUnverified) {
			t.Fatalf("code-epoch-fenced UUPS admission error = %v", err)
		}
		return
	}
	guid, ok := result.(string)
	if !ok || guid == "" || guid == staleGUID {
		t.Fatalf("code-epoch-fenced UUPS admission = %#v, stale=%s", result, staleGUID)
	}
	job := fixture.job(t, guid)
	if job.RequestV2.ProxyTarget.Pattern == "uups" ||
		job.RequestV2.ProxyTarget.UUPSGenerationID != "" {
		t.Fatalf("old UUPS probe survived returned-code epoch: %+v", job.RequestV2.ProxyTarget)
	}
}
