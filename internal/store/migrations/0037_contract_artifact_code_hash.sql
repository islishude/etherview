CREATE INDEX verified_contracts_code_hash_artifact_idx
    ON verified_contracts (
        chain_id, code_hash,
        (abi IS NOT NULL) DESC,
        (match_type = 'full') DESC,
        request_digest,
        verification_job_id,
        address,
        valid_from_block DESC
    );
