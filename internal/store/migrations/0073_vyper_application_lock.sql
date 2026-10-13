-- ADR-0054 / P30-T128: no rebind or backfill of historical tasks.
LOCK TABLE verification_jobs IN SHARE ROW EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM verification_jobs WHERE language='vyper' AND status IN ('queued','running')) THEN
  RAISE EXCEPTION 'drain Vyper jobs before application-lock deployment';
 END IF;
END $$;
CREATE OR REPLACE FUNCTION check_vyper_runtime_binding() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.executor_kind = 'node_vyper_wasm_v1' AND NOT EXISTS (
    SELECT 1 FROM compiler_catalog_entries e,
       jsonb_array_elements(e.vyper_runtimes) r,
       jsonb_each_text(r->'executor_digests') executor
    WHERE e.generation_id = NEW.catalog_generation_id AND e.language = 'vyper'
      AND e.version = NEW.compiler_version AND e.artifact_sha256 = NEW.compiler_digest
      AND executor.value = encode(NEW.executor_digest, 'hex')
      AND r->>'platform' = 'emscripten-wasm32'
      AND r->>'protocol' = 'etherview-vyper-wasm-package-v1'
      AND r->>'recipe_sha256' = NEW.request->>'vyper_lock_digest'
      AND e.artifact_url = 'embedded:vyper/' || (r->>'recipe_sha256')
 ) THEN RAISE EXCEPTION 'Vyper executor does not match application lock binding'; END IF;
 RETURN NEW;
END $$;
