-- state_diff@3 supplements diff-only prestate evidence with one exact
-- block-bound complete-prestate trace when an unchanged top-level execution
-- target is otherwise absent or unresolved. Existing history remains an
-- explicit bounded operator reindex; the migration never enqueues it.

-- Coverage proved with the superseded state_diff@2 witness must be rebuilt
-- only after state_diff@3 and its dependent trace/proxy generations publish.
TRUNCATE TABLE proxy_interaction_coverage_ranges, proxy_interaction_covered_blocks;

DO $$
DECLARE
    target REGPROCEDURE;
    definition TEXT;
    upgraded TEXT;
BEGIN
    FOREACH target IN ARRAY ARRAY[
        'refresh_proxy_interaction_coverage_block(numeric,numeric)'::regprocedure,
        'refresh_proxy_interaction_coverage_from_stage_result()'::regprocedure,
        'refresh_proxy_interaction_coverage_from_job()'::regprocedure,
        'refresh_proxy_interaction_coverage_from_journal()'::regprocedure
    ] LOOP
        definition := pg_get_functiondef(target);
        upgraded := replace(definition, '''state_diff''::text, 2', '''state_diff''::text, 3');
        upgraded := replace(upgraded, '''state_diff'' AND OLD.stage_version = 2', '''state_diff'' AND OLD.stage_version = 3');
        upgraded := replace(upgraded, '''state_diff'' AND NEW.stage_version = 2', '''state_diff'' AND NEW.stage_version = 3');
        upgraded := replace(upgraded, '''state_diff@2''', '''state_diff@3''');
        IF upgraded = definition THEN
            RAISE EXCEPTION 'function % has no state_diff@2 coverage witness', target;
        END IF;
        EXECUTE upgraded;
    END LOOP;
END
$$;
