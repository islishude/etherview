-- Additive, generation-fenced proxy detector output. Existing proxy/beacon
-- negative evidence and every legacy reader retain their current semantics.
ALTER TABLE proxy_detection_evidence
    DROP CONSTRAINT proxy_detection_evidence_candidate_kind_check,
    DROP CONSTRAINT proxy_detection_evidence_detection_state_check,
    DROP CONSTRAINT proxy_detection_evidence_reason_check;

ALTER TABLE proxy_detection_evidence
    ADD CONSTRAINT proxy_detection_evidence_candidate_kind_check
        CHECK (candidate_kind IN ('proxy', 'beacon', 'proxy_v2')),
    ADD CONSTRAINT proxy_detection_evidence_detection_state_check
        CHECK (detection_state IN (
            'not_detected', 'rejected', 'confirmed', 'candidate',
            'inconsistent', 'unknown'
        )),
    ADD CONSTRAINT proxy_detection_evidence_reason_check
        CHECK (reason IN (
            'empty_code', 'not_proxy', 'minimal_zero_implementation',
            'immutable_args_too_large', 'immutable_args_creation_unverified',
            'self_implementation',
            'implementation_has_no_code', 'invalid_slot_address',
            'ambiguous_slots', 'beacon_has_no_code',
            'invalid_beacon_implementation', 'resolver'
        )),
    ADD CONSTRAINT proxy_detection_evidence_v2_shape_check
        CHECK (
            (candidate_kind = 'proxy_v2' AND reason = 'resolver' AND
             detection_state IN (
                 'confirmed', 'candidate', 'inconsistent',
                 'not_detected', 'unknown'
             )) OR
            (candidate_kind <> 'proxy_v2' AND reason <> 'resolver' AND
             detection_state IN ('not_detected', 'rejected'))
        );
