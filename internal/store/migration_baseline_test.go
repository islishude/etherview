package store

import (
	"regexp"
	"strings"
	"testing"
)

// This source-form guard complements PostgreSQL integration tests. Freeze the
// complete exceptional statements so adding another ALTER requires review of
// the dependency, not merely choosing an already-allowed table or function.
var baselineAlterPattern = regexp.MustCompile(`(?is)\bALTER\s+(?:TABLE|INDEX|SEQUENCE|FUNCTION)\b[^;]*;`)

func unexpectedBaselineAlters(sql string) []string {
	allowed := make(map[string]bool)
	for line := range strings.SplitSeq(baselineAllowedAlters, "\n") {
		if line != "" {
			allowed[line] = true
		}
	}
	var unexpected []string
	for _, statement := range baselineAlterPattern.FindAllString(sql, -1) {
		normalized := strings.Join(strings.Fields(statement), " ")
		if !allowed[normalized] {
			unexpected = append(unexpected, normalized)
		}
	}
	return unexpected
}

func TestBaselineDeclaresFinalObjects(t *testing.T) {
	migrations, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		for _, statement := range unexpectedBaselineAlters(migration.SQL) {
			t.Errorf("%s: declare the final object directly or document a necessary exception: %s", migration.Version, statement)
		}
	}
}

func TestBaselineGuardRejectsScatteredDefinitions(t *testing.T) {
	for _, sql := range []string{
		"ALTER TABLE chains ADD COLUMN extra text;",
		"alter table chains alter column chain_id set default 1;",
		"ALTER TABLE chains ALTER COLUMN chain_id ADD GENERATED ALWAYS AS IDENTITY;",
		"ALTER TABLE chains ADD CONSTRAINT extra UNIQUE(chain_id);",
		"ALTER TABLE chains ATTACH PARTITION extra DEFAULT;",
		"ALTER INDEX parent_idx ATTACH PARTITION child_idx;",
		"ALTER SEQUENCE unexpected_seq OWNED BY chains.chain_id;",
		"ALTER FUNCTION unexpected() SET search_path = public;",
	} {
		if len(unexpectedBaselineAlters(sql)) != 1 {
			t.Errorf("guard accepted %s", sql)
		}
	}
}

// Three constraint exceptions preserve a circular FK, NOT VALID state and a
// named UNIQUE equivalent to the primary key. Seven sequence ownership statements need
// both sequence and table. Function bindings must capture the migration schema.
const baselineAllowedAlters = `
ALTER TABLE search_catalog_documents ADD CONSTRAINT search_catalog_documents_name_source_check CHECK ((source_kind='name' AND name_publication_id IS NOT NULL AND name_source IN ('ens','custom_ens')) OR (source_kind<>'name' AND name_publication_id IS NULL AND name_source IS NULL)) NOT VALID;
ALTER TABLE ONLY verified_contracts ADD CONSTRAINT verified_contracts_selector_identity_unique UNIQUE (chain_id, address, code_hash, valid_from_block, verification_job_id);
ALTER TABLE ONLY billing_topup_intents ADD CONSTRAINT billing_topup_intents_active_payment_fk FOREIGN KEY (active_payment_id) REFERENCES billing_payments(id);
ALTER SEQUENCE beacon_observation_generations_id_seq OWNED BY beacon_observation_generations.id;
ALTER SEQUENCE diamond_loupe_snapshots_id_seq OWNED BY diamond_loupe_snapshots.id;
ALTER SEQUENCE external_adapter_observations_id_seq OWNED BY external_adapter_observations.id;
ALTER SEQUENCE proxy_artifact_resolutions_id_seq OWNED BY proxy_artifact_resolutions.id;
ALTER SEQUENCE proxy_detection_evidence_id_seq OWNED BY proxy_detection_evidence.id;
ALTER SEQUENCE proxy_observation_generations_id_seq OWNED BY proxy_observation_generations.id;
ALTER SEQUENCE uups_implementation_observation_generations_id_seq OWNED BY uups_implementation_observation_generations.id;
ALTER FUNCTION %I.enforce_enrichment_terminal_publication() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.enforce_erc4337_canonical_user_op_hash() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.enforce_proxy_artifact_resolution() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.enforce_proxy_detection_generation() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.enforce_proxy_observation_generation() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.enforce_unbound_verification_job_insert() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.enforce_uups_implementation_observation() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.enforce_uups_implementation_observation_mutation() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.enforce_uups_implementation_observation_witness() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.enforce_verified_contract_proxy_artifact() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.enforce_verified_proxy_binding() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.enforce_verifier_v2_publication() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.enqueue_derived_forward_event_after_stage_publication() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.erc4337_add_covered_block(target_chain_id numeric, target_configuration_digest bytea, target_block_number numeric, target_block_hash bytea, target_durable_job_id bigint, target_job_generation bigint) SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.erc4337_remove_block_coverage(target_chain_id numeric, target_block_number numeric, target_block_hash bytea) SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.erc4337_remove_covered_block(target_chain_id numeric, target_configuration_digest bytea, target_block_number numeric) SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.erc4337_remove_detached_coverage() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.etherview_guard_erc20_balance_reconciliation() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.etherview_guard_erc20_holder_fact() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.etherview_guard_nft_metadata_update_observation() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.native_transfer_canonical_coverage() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.native_transfer_job_coverage() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.native_transfer_outbox_coverage() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.native_transfer_result_coverage() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.proxy_interaction_coverage_contains(target_chain_id numeric, target_start_block numeric, target_start_hash bytea, target_end_block numeric, target_end_hash bytea) SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.ens_sync_publication_current(bigint) SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.ens_effective_publication_current(bigint) SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.record_ens_effective_search_document() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.notify_ens_source_change() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.prune_ens_event_window() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.prune_search_catalog(requested_chain_id numeric, requested_retention_generations bigint) SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.record_search_catalog_document() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.record_verified_contract_search_catalog_document() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.refresh_native_transfer_coverage(p_chain numeric, p_number numeric) SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.refresh_proxy_interaction_coverage_block(target_chain_id numeric, target_block_number numeric) SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_canonical() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_journal() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_outbox() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_stage_result() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.reject_durable_stage_publication_mutation() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.reject_proxy_evidence_mutation() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.reject_verified_proxy_binding_mutation() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.reject_verifier_v2_result_mutation() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.require_enrichment_publication_protocol() SET search_path = %I, pg_catalog', current_schema(), current_schema());
ALTER FUNCTION %I.synchronize_derived_attempt_trace_canonicality() SET search_path = %I, pg_catalog', current_schema(), current_schema());
`
