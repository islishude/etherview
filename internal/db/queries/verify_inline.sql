-- name: VerifyInlineBindCompilerStatement1 :execrows
UPDATE verification_jobs
		SET compiler_platform = sqlc.arg('compiler_platform'), catalog_generation_id = sqlc.narg('catalog_generation_id'),
		    compiler_digest = sqlc.arg('compiler_digest'), executor_kind = sqlc.arg('executor_kind'),
		    execution_policy = sqlc.arg('execution_policy'), executor_digest = sqlc.arg('executor_digest'),
		    updated_at = clock_timestamp()
		WHERE id = sqlc.arg('id')::uuid AND status = 'running' AND lease_token = sqlc.arg('lease_token')
		  AND lease_expires_at > clock_timestamp()
		  AND (
		    (compiler_platform IS NULL AND catalog_generation_id IS NULL
		     AND compiler_digest IS NULL AND executor_kind IS NULL
		     AND execution_policy IS NULL AND executor_digest IS NULL)
		    OR
		    (compiler_platform = sqlc.arg('compiler_platform') AND catalog_generation_id IS NOT DISTINCT FROM sqlc.narg('catalog_generation_id')
		     AND compiler_digest = sqlc.arg('compiler_digest') AND executor_kind = sqlc.arg('executor_kind')
		     AND execution_policy = sqlc.arg('execution_policy') AND executor_digest = sqlc.arg('executor_digest'))
		  );

-- name: VerifyInlineBindCompilerStatement2 :one
SELECT TRUE AS bound FROM verification_jobs
		WHERE id = sqlc.arg('id')::uuid AND status = 'running' AND lease_token = sqlc.arg('lease_token')
		  AND lease_expires_at > clock_timestamp();

-- name: VerifyInlineCompleteProxyV2Statement1 :exec
SELECT pg_advisory_xact_lock(hashtextextended(
		    'etherview:proxy-interaction-coverage:' || sqlc.arg('chain_id')::numeric::text,
		    0
		));

-- name: VerifyInlineCompleteProxyV2Statement2 :exec
UPDATE verification_jobs
		SET status = 'succeeded', outcome_kind = 'proxy_verification_success',
		    outcome = sqlc.arg('outcome')::jsonb, error_code = NULL, leased_by = NULL,
		    lease_token = NULL, lease_expires_at = NULL,
		    updated_at = clock_timestamp()
		WHERE id = sqlc.arg('id')::uuid AND lease_token = sqlc.arg('lease_token');

-- name: VerifyInlineCompleteProxyV2Statement3 :exec
INSERT INTO verification_results (
			job_id, request_digest, outcome_kind, outcome
		) VALUES (sqlc.arg('job_id')::uuid, sqlc.arg('request_digest'), 'proxy_verification_success', sqlc.arg('outcome')::jsonb);

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
			sqlc.arg('chain_id')::numeric, sqlc.arg('proxy_address'), sqlc.arg('proxy_code_hash'), sqlc.arg('observation_block_number')::numeric, sqlc.arg('observation_block_hash'), 2, sqlc.arg('proxy_kind'), sqlc.arg('proxy_pattern'), sqlc.narg('standard_version'), sqlc.arg('implementation_address'), sqlc.arg('implementation_code_hash'),
			sqlc.arg('admin_address'), sqlc.arg('admin_code_hash'), sqlc.arg('beacon_address'), sqlc.arg('beacon_code_hash'), sqlc.arg('management_kind'), sqlc.arg('management_address'), sqlc.arg('management_code_hash'),
			sqlc.arg('observation_generation_id')::bigint, sqlc.narg('artifact_resolution_id')::bigint, sqlc.narg('beacon_generation_id')::bigint, sqlc.narg('uups_generation_id')::bigint,
			sqlc.arg('context_block_number')::numeric, sqlc.arg('context_block_hash'), sqlc.arg('verification_job_id')::uuid, sqlc.arg('request_digest')
		);

-- name: VerifyInlineCompleteV2Statement1 :one
SELECT code
			FROM contract_code_observations
			WHERE chain_id = sqlc.arg('chain_id')::numeric AND address = sqlc.arg('address')
			  AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')
			  AND code_hash = sqlc.arg('code_hash') AND canonical = TRUE;

-- name: VerifyInlineCompleteV2Statement2 :exec
UPDATE verification_jobs
		SET status = 'succeeded', outcome_kind = sqlc.arg('outcome_kind'), outcome = sqlc.arg('outcome')::jsonb,
		    error_code = NULL, leased_by = NULL, lease_token = NULL,
		    lease_expires_at = NULL, updated_at = clock_timestamp()
		WHERE id = sqlc.arg('id')::uuid AND lease_token = sqlc.arg('lease_token');

-- name: VerifyInlineCompleteV2Statement3 :exec
INSERT INTO verification_results (
			job_id, request_digest, outcome_kind, outcome, file_name, contract_name,
			language, compiler_version, match_type, abi, sources, settings,
			compilation_artifacts, creation_code_artifacts, runtime_code_artifacts,
			constructor_arguments, libraries, is_blueprint,
			proxy_artifact_kind, proxy_standard_version,
			proxy_runtime_immutable_address, proxy_source_manifest_sha256
		) VALUES (
			sqlc.arg('job_id')::uuid, sqlc.arg('request_digest'), sqlc.arg('outcome_kind'), sqlc.arg('outcome')::jsonb, sqlc.narg('file_name'), sqlc.narg('contract_name'), sqlc.narg('language'), sqlc.narg('compiler_version'), sqlc.narg('match_type'), sqlc.arg('abi')::jsonb,
			sqlc.arg('sources')::jsonb, sqlc.arg('settings')::jsonb, sqlc.arg('compilation_artifacts')::jsonb, sqlc.arg('creation_code_artifacts')::jsonb, sqlc.arg('runtime_code_artifacts')::jsonb,
			sqlc.arg('constructor_arguments'), sqlc.arg('libraries')::jsonb, sqlc.narg('is_blueprint'), sqlc.narg('proxy_artifact_kind'), sqlc.narg('proxy_standard_version'), sqlc.arg('proxy_runtime_immutable_address'), sqlc.arg('proxy_source_manifest_sha256')
		);

-- name: VerifyInlineCompleteV2Statement4 :exec
INSERT INTO verified_contracts (
				chain_id, address, code_hash, valid_from_block, verification_job_id,
				request_digest, file_name, contract_name, language, compiler_version,
				match_type, abi, sources, settings, compilation_artifacts,
				creation_code_artifacts, runtime_code_artifacts, constructor_arguments,
				libraries, is_blueprint
			) VALUES (
				sqlc.arg('chain_id')::numeric, sqlc.arg('address'), sqlc.arg('code_hash'), sqlc.arg('valid_from_block')::numeric, sqlc.arg('verification_job_id')::uuid, sqlc.arg('request_digest'), sqlc.narg('file_name'), sqlc.narg('contract_name'), sqlc.narg('language'), sqlc.narg('compiler_version'),
				sqlc.arg('match_type'), sqlc.arg('abi')::jsonb, sqlc.arg('sources')::jsonb, sqlc.arg('settings')::jsonb, sqlc.arg('compilation_artifacts')::jsonb, sqlc.arg('creation_code_artifacts')::jsonb,
				sqlc.arg('runtime_code_artifacts')::jsonb, sqlc.arg('constructor_arguments'), sqlc.arg('libraries')::jsonb, sqlc.narg('is_blueprint')
			);

-- name: VerifyInlineCompleteV2Statement5 :exec
INSERT INTO verified_contract_proxy_artifacts (
					chain_id, address, code_hash, valid_from_block,
					verification_job_id, request_digest, artifact_kind,
					standard_version, runtime_immutable_address,
					source_manifest_sha256
				) VALUES (
					sqlc.arg('chain_id')::numeric, sqlc.arg('address'), sqlc.arg('code_hash'), sqlc.arg('valid_from_block')::numeric,
					sqlc.arg('verification_job_id')::uuid, sqlc.arg('request_digest'), sqlc.arg('artifact_kind'), sqlc.arg('standard_version'), sqlc.arg('runtime_immutable_address'), sqlc.arg('source_manifest_sha256')
				);

-- name: VerifyInlineCompleteV2Statement6 :one
INSERT INTO verification_compilation_units (
			id, source_job_id, request_digest, language, compiler_version,
			compiler_platform, catalog_generation_id, compiler_sha256,
			executor_kind, execution_policy, executor_sha256,
			standard_json, standard_json_payload
		) VALUES (
			sqlc.arg('id')::uuid, sqlc.arg('source_job_id')::uuid, sqlc.arg('request_digest'), sqlc.arg('language'), sqlc.arg('compiler_version'), sqlc.arg('compiler_platform'), sqlc.arg('catalog_generation_id')::bigint, sqlc.arg('compiler_sha256'), sqlc.arg('executor_kind'), sqlc.arg('execution_policy'), sqlc.arg('executor_sha256'),
			sqlc.arg('standard_json')::jsonb, sqlc.arg('standard_json_payload')
		)
		RETURNING id::text;

-- name: VerifyInlineCompleteV2Statement7 :exec
INSERT INTO verification_compilation_contracts (
			compilation_id, file_name, contract_name, abi, creation_bytecode,
			runtime_bytecode, compilation_artifacts, creation_code_artifacts,
			runtime_code_artifacts
		) VALUES (
			sqlc.arg('compilation_id')::uuid, sqlc.arg('file_name'), sqlc.arg('contract_name'), sqlc.arg('abi')::jsonb, sqlc.arg('creation_bytecode'), sqlc.arg('runtime_bytecode'), sqlc.arg('compilation_artifacts')::jsonb, sqlc.arg('creation_code_artifacts')::jsonb, sqlc.arg('runtime_code_artifacts')::jsonb
		);

-- name: VerifyInlineFailStatement1 :execrows
UPDATE verification_jobs
		SET status = 'failed', outcome_kind = NULL, outcome = NULL, error_code = sqlc.arg('error_code'),
		    leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
		    updated_at = clock_timestamp()
		WHERE id = sqlc.arg('id')::uuid AND status = 'running' AND lease_token = sqlc.arg('lease_token')
		  AND lease_expires_at > clock_timestamp();

-- name: VerifyInlineLookupStatement1 :one
SELECT entry.generation_id, entry.language, entry.version,
		       entry.platform, entry.artifact_url, entry.artifact_sha256,
		       entry.max_bytes, head.updated_at, entry.expires_at
		FROM compiler_catalog_heads AS head
		JOIN compiler_catalog_generations AS generation
		  ON generation.id = head.generation_id AND generation.language = head.language
		JOIN compiler_catalog_entries AS entry
		  ON entry.generation_id = head.generation_id AND entry.language = head.language
		WHERE head.language = $1 AND entry.version = $2;

-- name: VerifyInlineLookupStatement2 :one
SELECT EXISTS (SELECT 1 FROM compiler_catalog_heads WHERE language = $1);

-- name: VerifyInlinePersistStatement1 :one
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
SELECT entry.version, head.updated_at, entry.expires_at, entry.vyper_runtimes
		FROM compiler_catalog_heads AS head
		JOIN compiler_catalog_generations AS generation
		  ON generation.id = head.generation_id AND generation.language = head.language
		JOIN compiler_catalog_entries AS entry
		  ON entry.generation_id = head.generation_id AND entry.language = head.language
		WHERE head.language = $1
			ORDER BY entry.version;
