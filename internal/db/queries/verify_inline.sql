-- name: VerifyInlineBindCompilerStatement1 :exec
UPDATE verification_jobs
		SET compiler_platform = $3, catalog_generation_id = $4,
		    compiler_digest = $5, executor_kind = $6,
		    execution_policy = $7, executor_digest = $8,
		    updated_at = clock_timestamp()
		WHERE id = $1::uuid AND status = 'running' AND lease_token = $2
		  AND lease_expires_at > clock_timestamp()
		  AND (
		    (compiler_platform IS NULL AND catalog_generation_id IS NULL
		     AND compiler_digest IS NULL AND executor_kind IS NULL
		     AND execution_policy IS NULL AND executor_digest IS NULL)
		    OR
		    (compiler_platform = $3 AND catalog_generation_id IS NOT DISTINCT FROM $4
		     AND compiler_digest = $5 AND executor_kind = $6
		     AND execution_policy = $7 AND executor_digest = $8)
		  );

-- name: VerifyInlineBindCompilerStatement2 :many
SELECT TRUE FROM verification_jobs
		WHERE id = $1::uuid AND status = 'running' AND lease_token = $2
		  AND lease_expires_at > clock_timestamp();

-- name: VerifyInlineCompleteProxyV2Statement1 :many
SELECT pg_advisory_xact_lock(hashtextextended(
		    'etherview:proxy-interaction-coverage:' || $1::numeric::text,
		    0
		));

-- name: VerifyInlineCompleteProxyV2Statement2 :exec
UPDATE verification_jobs
		SET status = 'succeeded', outcome_kind = 'proxy_verification_success',
		    outcome = $3::jsonb, error_code = NULL, leased_by = NULL,
		    lease_token = NULL, lease_expires_at = NULL,
		    updated_at = clock_timestamp()
		WHERE id = $1::uuid AND lease_token = $2;

-- name: VerifyInlineCompleteProxyV2Statement3 :exec
INSERT INTO verification_results (
			job_id, request_digest, outcome_kind, outcome
		) VALUES ($1::uuid, $2, 'proxy_verification_success', $3::jsonb);

-- name: VerifyInlineCompleteProxyV2Statement4 :exec
INSERT INTO verified_proxy_bindings (
			chain_id, proxy_address, proxy_code_hash, observation_block_number,
			observation_block_hash, observation_stage_version, proxy_kind,
			proxy_pattern, standard_version, implementation_address,
			implementation_code_hash, admin_address, admin_code_hash,
			beacon_address, beacon_code_hash, management_kind,
			management_address, management_code_hash,
			observation_generation_id, artifact_resolution_id,
			beacon_generation_id, uups_generation_id,
			context_block_number, context_block_hash,
			verification_job_id, request_digest
		) VALUES (
			$1::numeric, $2, $3, $4::numeric, $5, 2, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16, $17,
			$18::bigint, $19::bigint, $20::bigint, $21::bigint,
			$22::numeric, $23, $24::uuid, $25
		);

-- name: VerifyInlineCompleteV2Statement1 :many
SELECT code
			FROM contract_code_observations
			WHERE chain_id = $1::numeric AND address = $2
			  AND block_number = $3::numeric AND block_hash = $4
			  AND code_hash = $5 AND canonical = TRUE;

-- name: VerifyInlineCompleteV2Statement2 :exec
UPDATE verification_jobs
		SET status = 'succeeded', outcome_kind = $3, outcome = $4::jsonb,
		    error_code = NULL, leased_by = NULL, lease_token = NULL,
		    lease_expires_at = NULL, updated_at = clock_timestamp()
		WHERE id = $1::uuid AND lease_token = $2;

-- name: VerifyInlineCompleteV2Statement3 :exec
INSERT INTO verification_results (
			job_id, request_digest, outcome_kind, outcome, file_name, contract_name,
			language, compiler_version, match_type, abi, sources, settings,
			compilation_artifacts, creation_code_artifacts, runtime_code_artifacts,
			constructor_arguments, libraries, is_blueprint,
			proxy_artifact_kind, proxy_standard_version,
			proxy_runtime_immutable_address, proxy_source_manifest_sha256
		) VALUES (
			$1::uuid, $2, $3, $4::jsonb, $5, $6, $7, $8, $9, $10::jsonb,
			$11::jsonb, $12::jsonb, $13::jsonb, $14::jsonb, $15::jsonb,
			$16, $17::jsonb, $18, $19, $20, $21, $22
		);

-- name: VerifyInlineCompleteV2Statement4 :exec
INSERT INTO verified_contracts (
				chain_id, address, code_hash, valid_from_block, verification_job_id,
				request_digest, file_name, contract_name, language, compiler_version,
				match_type, abi, sources, settings, compilation_artifacts,
				creation_code_artifacts, runtime_code_artifacts, constructor_arguments,
				libraries, is_blueprint
			) VALUES (
				$1::numeric, $2, $3, $4::numeric, $5::uuid, $6, $7, $8, $9, $10,
				$11, $12::jsonb, $13::jsonb, $14::jsonb, $15::jsonb, $16::jsonb,
				$17::jsonb, $18, $19::jsonb, $20
			);

-- name: VerifyInlineCompleteV2Statement5 :exec
INSERT INTO verified_contract_proxy_artifacts (
					chain_id, address, code_hash, valid_from_block,
					verification_job_id, request_digest, artifact_kind,
					standard_version, runtime_immutable_address,
					source_manifest_sha256
				) VALUES (
					$1::numeric, $2, $3, $4::numeric,
					$5::uuid, $6, $7, $8, $9, $10
				);

-- name: VerifyInlineFailStatement1 :exec
UPDATE verification_jobs
		SET status = 'failed', outcome_kind = NULL, outcome = NULL, error_code = $3,
		    leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
		    updated_at = clock_timestamp()
		WHERE id = $1::uuid AND status = 'running' AND lease_token = $2
		  AND lease_expires_at > clock_timestamp();

-- name: VerifyInlineLookupStatement1 :many
SELECT entry.generation_id, entry.language, entry.version,
		       entry.platform, entry.artifact_url, entry.artifact_sha256,
		       entry.max_bytes, head.updated_at
		FROM compiler_catalog_heads AS head
		JOIN compiler_catalog_generations AS generation
		  ON generation.id = head.generation_id AND generation.language = head.language
		JOIN compiler_catalog_entries AS entry
		  ON entry.generation_id = head.generation_id AND entry.language = head.language
		WHERE head.language = $1 AND entry.version = $2;

-- name: VerifyInlineLookupStatement2 :many
SELECT EXISTS (SELECT 1 FROM compiler_catalog_heads WHERE language = $1);

-- name: VerifyInlinePersistStatement1 :many
INSERT INTO compiler_catalog_generations
			(language, source_url, catalog_digest, entry_count)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (language, catalog_digest) DO UPDATE
		SET source_url = compiler_catalog_generations.source_url
		RETURNING id;

-- name: VerifyInlinePersistStatement2 :exec
INSERT INTO compiler_catalog_entries
				(generation_id, language, version, platform, artifact_url, artifact_sha256, max_bytes)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (generation_id, version) DO NOTHING;

-- name: VerifyInlinePersistStatement3 :exec
INSERT INTO compiler_catalog_heads (language, generation_id)
		VALUES ($1, $2)
		ON CONFLICT (language) DO UPDATE
		SET generation_id = EXCLUDED.generation_id, updated_at = now();

-- name: VerifyInlineVersionsStatement1 :many
SELECT entry.version, head.updated_at
		FROM compiler_catalog_heads AS head
		JOIN compiler_catalog_generations AS generation
		  ON generation.id = head.generation_id AND generation.language = head.language
		JOIN compiler_catalog_entries AS entry
		  ON entry.generation_id = head.generation_id AND entry.language = head.language
		WHERE head.language = $1
			ORDER BY entry.version;
