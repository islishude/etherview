-- Solady legacy LibCWIA is an additive immutable singular-clone mechanism in
-- proxy@2. Existing rows, publications, jobs, and bindings are retained; this
-- migration deliberately schedules no replay or historical backfill.

ALTER TABLE proxy_observations
    DROP CONSTRAINT proxy_observations_proxy_kind_check,
    ADD CONSTRAINT proxy_observations_proxy_kind_check CHECK (
        proxy_kind IN ('eip1167', 'cwia', 'eip1967', 'beacon', 'unknown')
    );

ALTER TABLE verified_proxy_bindings
    DROP CONSTRAINT verified_proxy_bindings_proxy_kind_check,
    DROP CONSTRAINT verified_proxy_bindings_management_semantics,
    ADD CONSTRAINT verified_proxy_bindings_proxy_kind_check CHECK (
        proxy_kind IN ('eip1167', 'cwia', 'eip1967', 'beacon')
    ),
    ADD CONSTRAINT verified_proxy_bindings_management_semantics CHECK (
        (
            proxy_pattern = 'transparent'
            AND proxy_kind = 'eip1967'
            AND admin_address IS NOT NULL
            AND admin_code_hash IS NOT NULL
            AND beacon_address IS NULL
            AND beacon_code_hash IS NULL
            AND management_kind = 'proxy_admin'
            AND management_address IS NOT DISTINCT FROM admin_address
            AND management_code_hash IS NOT DISTINCT FROM admin_code_hash
        ) OR (
            proxy_pattern = 'beacon'
            AND proxy_kind = 'beacon'
            AND admin_address IS NULL
            AND admin_code_hash IS NULL
            AND beacon_address IS NOT NULL
            AND beacon_code_hash IS NOT NULL
            AND management_kind = 'upgradeable_beacon'
            AND management_address IS NOT DISTINCT FROM beacon_address
            AND management_code_hash IS NOT DISTINCT FROM beacon_code_hash
        ) OR (
            proxy_pattern = 'clone'
            AND proxy_kind IN ('eip1167', 'cwia')
            AND admin_address IS NULL
            AND admin_code_hash IS NULL
            AND beacon_address IS NULL
            AND beacon_code_hash IS NULL
            AND management_kind = 'none'
        ) OR (
            proxy_pattern IN ('erc1967', 'uups')
            AND proxy_kind = 'eip1967'
            AND admin_address IS NULL
            AND admin_code_hash IS NULL
            AND beacon_address IS NULL
            AND beacon_code_hash IS NULL
            AND management_kind = 'none'
        )
    );

ALTER TABLE proxy_detection_evidence
    DROP CONSTRAINT proxy_detection_evidence_reason_check,
    ADD CONSTRAINT proxy_detection_evidence_reason_check CHECK (
        reason IN (
            'empty_code', 'not_proxy', 'minimal_zero_implementation',
            'immutable_args_too_large', 'immutable_args_creation_unverified',
            'self_implementation', 'cwia_invalid_length',
            'cwia_immutable_args_too_large', 'cwia_zero_implementation',
            'cwia_self_implementation', 'implementation_has_no_code',
            'invalid_slot_address', 'ambiguous_slots', 'beacon_has_no_code',
            'invalid_beacon_implementation', 'resolver'
        )
    );
