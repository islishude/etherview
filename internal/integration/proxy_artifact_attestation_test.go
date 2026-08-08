//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func TestOpenZeppelinArtifactPublicationRequiresImmutableResultAttestation(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(99_000), testHash(0), testHash(99_100), "artifact-attestation-genesis")
	block := testBundle(1, testHash(99_001), genesis.Block.Hash(), testHash(99_101), "artifact-attestation-one")
	commitCanonical(t, ctx, core, genesis)
	commitCanonical(t, ctx, core, block)
	blockRef := mustBlockRef(t, block)
	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)

	ordinaryAddress := testAddress(9_900)
	ordinaryRuntime := []byte{0x60, 0x01}
	ordinaryHash := crypto.Keccak256(ordinaryRuntime)
	insertArtifactAttestationCode(
		t, ctx, db, blockRef, ordinaryAddress, ordinaryHash, ordinaryRuntime,
	)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	job, created, err := repository.SubmitV2(ctx, verifierV2AddressSubmission(
		ordinaryAddress.Bytes(), ordinaryHash, blockRef.Hash.Bytes(), ordinaryRuntime,
	))
	if err != nil || !created {
		t.Fatalf("submit ordinary verification: created=%t error=%v", created, err)
	}
	lease, found, err := repository.Claim(ctx, "artifact-attestation", time.Minute)
	if err != nil || !found || lease.Job.ID != job.ID {
		t.Fatalf("claim ordinary verification: found=%t lease=%+v error=%v", found, lease, err)
	}
	if err := repository.BindCompiler(
		ctx, lease, solcJSProvenance(generation, compilerDigest, executorDigest),
	); err != nil {
		t.Fatalf("bind ordinary verification compiler: %v", err)
	}
	if err := repository.CompleteV2(
		ctx, lease, "verification_success", verifierV2SuccessOutcome(t, "partial"),
	); err != nil {
		t.Fatalf("complete ordinary verification: %v", err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verification_results
		WHERE job_id = $1::uuid
		  AND proxy_artifact_kind IS NULL
		  AND proxy_standard_version IS NULL
		  AND proxy_runtime_immutable_address IS NULL
		  AND proxy_source_manifest_sha256 IS NULL`, 1, job.ID)
	manifest := sha256.Sum256([]byte("forged-manifest"))
	if _, err := db.ExecContext(ctx, `
		INSERT INTO verified_contract_proxy_artifacts (
			chain_id, address, code_hash, valid_from_block,
			verification_job_id, request_digest, artifact_kind,
			standard_version, runtime_immutable_address,
			source_manifest_sha256
		) VALUES (1, $1, $2, 1, $3::uuid, $4,
			'erc1967_proxy', '5.6.1', NULL, $5)`,
		ordinaryAddress.Bytes(), ordinaryHash, job.ID, job.RequestDigest[:], manifest[:],
	); err == nil {
		t.Fatal("ordinary verified contract was promoted to an OpenZeppelin artifact")
	}

	validAddress := testAddress(9_901)
	validManifest := sha256.Sum256([]byte("valid-uups-manifest"))
	valid := proxyArtifactAttestationFixture{
		Address: validAddress, Runtime: []byte{0x60, 0x02}, Kind: "uups_implementation",
		Version: "5.6.1", Immutable: validAddress.Bytes(), Manifest: validManifest[:],
	}
	validIdentity, err := insertProxyArtifactAttestationSource(
		ctx, db, blockRef, generation, compilerDigest, executorDigest, valid,
	)
	if err != nil {
		t.Fatalf("insert exact UUPS attestation source: %v", err)
	}
	if _, err := insertProxyArtifactPublication(ctx, db, valid, validIdentity, 1); err != nil {
		t.Fatalf("publish exact UUPS artifact: %v", err)
	}

	tests := []struct {
		name      string
		fixture   proxyArtifactAttestationFixture
		mutate    func(*proxyArtifactAttestationFixture)
		validFrom uint64
	}{
		{
			name: "kind", fixture: proxyArtifactAttestationFixture{
				Address: testAddress(9_902), Runtime: []byte{0x60, 0x03}, Kind: "uups_implementation",
				Version: "5.6.1", Manifest: sha256Bytes("kind-attestation"),
			},
			mutate: func(publication *proxyArtifactAttestationFixture) {
				publication.Kind = "beacon_proxy"
			},
			validFrom: 1,
		},
		{
			name: "version", fixture: proxyArtifactAttestationFixture{
				Address: testAddress(9_903), Runtime: []byte{0x60, 0x04}, Kind: "erc1967_proxy",
				Version: "5.6.1", Manifest: sha256Bytes("version-attestation"),
			},
			mutate: func(publication *proxyArtifactAttestationFixture) {
				publication.Version = "5.6.0"
			},
			validFrom: 1,
		},
		{
			name: "immutable", fixture: proxyArtifactAttestationFixture{
				Address: testAddress(9_904), Runtime: []byte{0x60, 0x05}, Kind: "uups_implementation",
				Version: "5.6.1", Manifest: sha256Bytes("immutable-attestation"),
			},
			mutate: func(publication *proxyArtifactAttestationFixture) {
				publication.Immutable = testAddress(19_904).Bytes()
			},
			validFrom: 1,
		},
		{
			name: "manifest", fixture: proxyArtifactAttestationFixture{
				Address: testAddress(9_905), Runtime: []byte{0x60, 0x06}, Kind: "erc1967_proxy",
				Version: "5.6.1", Manifest: sha256Bytes("manifest-attestation"),
			},
			mutate: func(publication *proxyArtifactAttestationFixture) {
				publication.Manifest = sha256Bytes("different-manifest")
			},
			validFrom: 1,
		},
		{
			name: "uups self", fixture: proxyArtifactAttestationFixture{
				Address: testAddress(9_906), Runtime: []byte{0x60, 0x07}, Kind: "uups_implementation",
				Version: "5.6.1", Immutable: testAddress(19_906).Bytes(),
				Manifest: sha256Bytes("wrong-self-attestation"),
			},
			validFrom: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := test.fixture
			if fixture.Kind == "uups_implementation" && fixture.Immutable == nil {
				fixture.Immutable = fixture.Address.Bytes()
			}
			identity, err := insertProxyArtifactAttestationSource(
				ctx, db, blockRef, generation, compilerDigest, executorDigest, fixture,
			)
			if err != nil {
				t.Fatalf("insert attestation source: %v", err)
			}
			publication := fixture
			if test.mutate != nil {
				test.mutate(&publication)
			}
			if _, err := insertProxyArtifactPublication(
				ctx, db, publication, identity, test.validFrom,
			); err == nil {
				t.Fatalf("accepted %s-tampered artifact publication", test.name)
			}
		})
	}

	wrongBlock := proxyArtifactAttestationFixture{
		Address: testAddress(9_908), Runtime: []byte{0x60, 0x09},
		Kind: "erc1967_proxy", Version: "5.6.1",
		Manifest: sha256Bytes("wrong-block-attestation"),
	}
	wrongBlockIdentity, err := insertProxyArtifactAttestationSource(
		ctx, db, blockRef, generation, compilerDigest, executorDigest, wrongBlock,
	)
	if err != nil {
		t.Fatalf("insert target-block attestation source: %v", err)
	}
	if err := insertProxyArtifactVerifiedContractAt(
		ctx, db, wrongBlock, wrongBlockIdentity, 0,
	); err != nil {
		t.Fatalf("insert deliberately mis-scoped verified contract: %v", err)
	}
	if _, err := insertProxyArtifactPublication(
		ctx, db, wrongBlock, wrongBlockIdentity, 0,
	); err == nil {
		t.Fatal("artifact publication accepted valid_from_block outside its canonical target")
	}

	zero := proxyArtifactAttestationFixture{
		Address: testAddress(9_907), Runtime: []byte{0x60, 0x08}, Kind: "uups_implementation",
		Version: "5.6.1", Immutable: make([]byte, common.AddressLength),
		Manifest: sha256Bytes("zero-attestation"),
	}
	if _, err := insertProxyArtifactAttestationSource(
		ctx, db, blockRef, generation, compilerDigest, executorDigest, zero,
	); err == nil {
		t.Fatal("verification result accepted a zero runtime immutable attestation")
	}
}

func TestOpenZeppelinArtifactPublicationCanFollowOrdinaryVerification(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(99_200), testHash(0), testHash(99_300), "artifact-order-genesis")
	block := testBundle(1, testHash(99_201), genesis.Block.Hash(), testHash(99_301), "artifact-order-one")
	commitCanonical(t, ctx, core, genesis)
	commitCanonical(t, ctx, core, block)
	blockRef := mustBlockRef(t, block)
	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)

	address := testAddress(9_920)
	runtime := []byte{0x60, 0x20}
	codeHash := crypto.Keccak256(runtime)
	insertArtifactAttestationCode(t, ctx, db, blockRef, address, codeHash, runtime)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	ordinary, created, err := repository.SubmitV2(ctx, verifierV2AddressSubmission(
		address.Bytes(), codeHash, blockRef.Hash.Bytes(), runtime,
	))
	if err != nil || !created {
		t.Fatalf("submit ordinary verification: created=%t error=%v", created, err)
	}
	lease, found, err := repository.Claim(ctx, "artifact-order-ordinary", time.Minute)
	if err != nil || !found || lease.Job.ID != ordinary.ID {
		t.Fatalf("claim ordinary verification: found=%t lease=%+v error=%v", found, lease, err)
	}
	if err := repository.BindCompiler(
		ctx, lease, solcJSProvenance(generation, compilerDigest, executorDigest),
	); err != nil {
		t.Fatalf("bind ordinary compiler: %v", err)
	}
	if err := repository.CompleteV2(
		ctx, lease, "verification_success", verifierV2SuccessOutcome(t, "full"),
	); err != nil {
		t.Fatalf("complete ordinary verification: %v", err)
	}

	fixture := proxyArtifactAttestationFixture{
		Address: address, Runtime: runtime, Kind: "proxy_admin", Version: "5.6.1",
		Manifest: sha256Bytes("official-after-ordinary"),
	}
	exact, err := insertProxyArtifactAttestationSource(
		ctx, db, blockRef, generation, compilerDigest, executorDigest, fixture,
	)
	if err != nil {
		t.Fatalf("publish exact source after ordinary verification: %v", err)
	}
	if exact.JobID == ordinary.ID {
		t.Fatal("exact OpenZeppelin attestation reused the ordinary verification job")
	}
	if _, err := insertProxyArtifactPublication(ctx, db, fixture, exact, blockRef.Number); err != nil {
		t.Fatalf("publish exact artifact after ordinary verification: %v", err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_contracts
		WHERE chain_id = 1 AND address = $1 AND code_hash = $2
		  AND valid_from_block = $3::numeric`, 2,
		address.Bytes(), codeHash, fmt.Sprint(blockRef.Number))
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM verified_contract_proxy_artifacts AS artifact
		JOIN verified_contracts AS verified
		  ON verified.chain_id = artifact.chain_id
		 AND verified.address = artifact.address
		 AND verified.code_hash = artifact.code_hash
		 AND verified.valid_from_block = artifact.valid_from_block
		 AND verified.verification_job_id = artifact.verification_job_id
		 AND verified.request_digest = artifact.request_digest
		WHERE artifact.verification_job_id = $1::uuid
		  AND verified.file_name = 'Artifact.sol'`, 1, exact.JobID)
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM search_catalog_documents
		WHERE chain_id = 1
		  AND source_kind = 'verified_contract'
		  AND valid_to_generation IS NULL
		  AND logical_identity = jsonb_build_array(
		        encode($1::bytea, 'hex'), encode($2::bytea, 'hex'), $3::text
			      )::text`, 2, address.Bytes(), codeHash, fmt.Sprint(blockRef.Number))
	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
	if err != nil {
		t.Fatalf("construct search reader: %v", err)
	}
	searchResults, _, err := reader.Search(ctx, address.Hex(), "", 20)
	if err != nil {
		t.Fatalf("search multiply verified code identity: %v", err)
	}
	if len(searchResults) != 1 || searchResults[0].Label != "Artifact" {
		t.Fatalf("search winner = %#v, want full exact Artifact publication", searchResults)
	}

	// Each verification job owns an independent search source identity. Removing
	// the ordinary publication must not retire the exact artifact's document.
	if _, err := db.ExecContext(ctx, `
		DELETE FROM verified_contracts
		WHERE verification_job_id = $1::uuid`, ordinary.ID); err != nil {
		t.Fatalf("delete ordinary publication: %v", err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM search_catalog_documents
		WHERE chain_id = 1
		  AND source_kind = 'verified_contract'
		  AND valid_to_generation IS NULL
		  AND source_identity = jsonb_build_array(
		        encode($1::bytea, 'hex'), encode($2::bytea, 'hex'), $3::text,
		        $4::uuid::text
		      )::text`, 1, address.Bytes(), codeHash, fmt.Sprint(blockRef.Number), exact.JobID)
}

type proxyArtifactAttestationFixture struct {
	Address   common.Address
	Runtime   []byte
	Kind      string
	Version   string
	Immutable []byte
	Manifest  []byte
}

type proxyArtifactAttestationIdentity struct {
	JobID         string
	CodeHash      []byte
	RequestDigest []byte
}

func insertProxyArtifactAttestationSource(
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	generation int64,
	compilerDigest [sha256.Size]byte,
	executorDigest [sha256.Size]byte,
	fixture proxyArtifactAttestationFixture,
) (proxyArtifactAttestationIdentity, error) {
	codeHash := crypto.Keccak256(fixture.Runtime)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES (1, $1, $2::numeric, $3, $4, $5, TRUE)
		ON CONFLICT (chain_id, address, block_hash) DO NOTHING`,
		fixture.Address.Bytes(), fmt.Sprint(block.Number), block.Hash.Bytes(), codeHash,
		fixture.Runtime,
	); err != nil {
		return proxyArtifactAttestationIdentity{}, err
	}
	outcome, err := json.Marshal(map[string]any{
		"kind": "verification_success", "file_name": "Artifact.sol",
		"contract_name": "Artifact", "language": "solidity",
		"compiler_version": "0.8.30+commit.73712a01", "abi": []any{},
		"sources": map[string]any{}, "settings": map[string]any{},
		"compilation_artifacts":   map[string]any{},
		"creation_code_artifacts": map[string]any{},
		"runtime_code_artifacts":  map[string]any{},
		"runtime_match": map[string]any{
			"match_type": "full", "transformations": []any{}, "values": map[string]any{},
		},
		"libraries": map[string]any{}, "is_blueprint": false,
	})
	if err != nil {
		return proxyArtifactAttestationIdentity{}, err
	}
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20,
	})
	if err != nil {
		return proxyArtifactAttestationIdentity{}, err
	}
	submission := verifierV2AddressSubmission(
		fixture.Address.Bytes(), codeHash, block.Hash.Bytes(), fixture.Runtime,
	)
	submission.ContractNameHint = "openzeppelin-5.6.1:" + fixture.Kind
	job, created, err := repository.SubmitV2(ctx, submission)
	if err != nil || !created {
		return proxyArtifactAttestationIdentity{}, fmt.Errorf(
			"submit attested verification: created=%t: %w", created, err,
		)
	}
	lease, found, err := repository.Claim(ctx, "proxy-artifact-attestation", time.Minute)
	if err != nil || !found || lease.Job.ID != job.ID {
		return proxyArtifactAttestationIdentity{}, fmt.Errorf(
			"claim attested verification: found=%t claimed=%s want=%s: %w",
			found, lease.Job.ID, job.ID, err,
		)
	}
	if err := repository.BindCompiler(
		ctx, lease, solcJSProvenance(generation, compilerDigest, executorDigest),
	); err != nil {
		return proxyArtifactAttestationIdentity{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return proxyArtifactAttestationIdentity{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `
		UPDATE verification_jobs
		SET status = 'succeeded', outcome_kind = 'verification_success',
		    outcome = $3::jsonb, error_code = NULL, leased_by = NULL,
		    lease_token = NULL, lease_expires_at = NULL,
		    updated_at = clock_timestamp()
		WHERE id = $1::uuid AND status = 'running' AND lease_token = $2`,
		job.ID, lease.Token, outcome,
	); err != nil {
		return proxyArtifactAttestationIdentity{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verification_results (
			job_id, request_digest, outcome_kind, outcome, file_name,
			contract_name, language, compiler_version, match_type, abi,
			sources, settings, compilation_artifacts, creation_code_artifacts,
			runtime_code_artifacts, libraries, is_blueprint,
			proxy_artifact_kind, proxy_standard_version,
			proxy_runtime_immutable_address, proxy_source_manifest_sha256
		) VALUES (
			$1::uuid, $2, 'verification_success', $3::jsonb, 'Artifact.sol',
			'Artifact', 'solidity', '0.8.30+commit.73712a01', 'full', '[]'::jsonb,
			'{}'::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb,
			'{}'::jsonb, '{}'::jsonb, FALSE, $4, $5, $6, $7
		)`, job.ID, job.RequestDigest[:], outcome, fixture.Kind,
		fixture.Version, fixture.Immutable, fixture.Manifest,
	); err != nil {
		return proxyArtifactAttestationIdentity{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verified_contracts (
			chain_id, address, code_hash, valid_from_block,
			verification_job_id, request_digest, file_name, contract_name,
			language, compiler_version, match_type, abi, sources, settings,
			compilation_artifacts, creation_code_artifacts,
			runtime_code_artifacts, libraries, is_blueprint
		) VALUES (
			1, $1, $2, $3::numeric, $4::uuid, $5, 'Artifact.sol', 'Artifact',
			'solidity', '0.8.30+commit.73712a01', 'full', '[]'::jsonb,
			'{}'::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb,
			'{}'::jsonb, '{}'::jsonb, FALSE
		)`, fixture.Address.Bytes(), codeHash, fmt.Sprint(block.Number), job.ID,
		job.RequestDigest[:],
	); err != nil {
		return proxyArtifactAttestationIdentity{}, err
	}
	if err := tx.Commit(); err != nil {
		return proxyArtifactAttestationIdentity{}, err
	}
	return proxyArtifactAttestationIdentity{
		JobID: job.ID, CodeHash: codeHash, RequestDigest: job.RequestDigest[:],
	}, nil
}

func insertProxyArtifactPublication(
	ctx context.Context,
	db *sql.DB,
	fixture proxyArtifactAttestationFixture,
	identity proxyArtifactAttestationIdentity,
	validFrom uint64,
) (sql.Result, error) {
	return db.ExecContext(ctx, `
		INSERT INTO verified_contract_proxy_artifacts (
			chain_id, address, code_hash, valid_from_block,
			verification_job_id, request_digest, artifact_kind,
			standard_version, runtime_immutable_address,
			source_manifest_sha256
		) VALUES (1, $1, $2, $3::numeric, $4::uuid, $5, $6, $7, $8, $9)`,
		fixture.Address.Bytes(), identity.CodeHash, fmt.Sprint(validFrom), identity.JobID,
		identity.RequestDigest, fixture.Kind, fixture.Version, fixture.Immutable,
		fixture.Manifest,
	)
}

func insertProxyArtifactVerifiedContractAt(
	ctx context.Context,
	db *sql.DB,
	fixture proxyArtifactAttestationFixture,
	identity proxyArtifactAttestationIdentity,
	validFrom uint64,
) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO verified_contracts (
			chain_id, address, code_hash, valid_from_block,
			verification_job_id, request_digest, file_name, contract_name,
			language, compiler_version, match_type, abi, sources, settings,
			compilation_artifacts, creation_code_artifacts,
			runtime_code_artifacts, libraries, is_blueprint
		) VALUES (
			1, $1, $2, $3::numeric, $4::uuid, $5, 'Artifact.sol', 'Artifact',
			'solidity', '0.8.30+commit.73712a01', 'full', '[]'::jsonb,
			'{}'::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb,
			'{}'::jsonb, '{}'::jsonb, FALSE
		)`, fixture.Address.Bytes(), identity.CodeHash, fmt.Sprint(validFrom),
		identity.JobID, identity.RequestDigest,
	)
	return err
}

func insertArtifactAttestationCode(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	address common.Address,
	codeHash []byte,
	code []byte,
) {
	t.Helper()
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES (1, $1, $2::numeric, $3, $4, $5, TRUE)`,
		address.Bytes(), fmt.Sprint(block.Number), block.Hash.Bytes(), codeHash, code,
	)
}

func sha256Bytes(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
}
