//go:build integration

package integration_test

import (
	"context"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"

	pgtype "github.com/jackc/pgx/v5/pgtype"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func exactProxyVerificationSubmission(
	t *testing.T,
	ctx context.Context,
	db *pgxpool.Pool,
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
	var beaconGeneration pgtype.Int8
	if err := db.QueryRow(ctx, `
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
		if err := db.QueryRow(ctx, `
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
		if err := db.QueryRow(ctx, `
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
		if err := db.QueryRow(ctx, `
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
