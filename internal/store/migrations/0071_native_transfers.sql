CREATE TABLE native_transfers (
 chain_id NUMERIC(78,0) NOT NULL REFERENCES chains(chain_id),
 block_number NUMERIC(78,0) NOT NULL,
 block_hash BYTEA NOT NULL CHECK(octet_length(block_hash)=32),
 transaction_hash BYTEA NOT NULL CHECK(octet_length(transaction_hash)=32),
 transaction_index BIGINT NOT NULL CHECK(transaction_index>=0),
 log_index BIGINT NOT NULL CHECK(log_index>=0),
 from_address BYTEA NOT NULL CHECK(octet_length(from_address)=20),
 to_address BYTEA NOT NULL CHECK(octet_length(to_address)=20),
 amount NUMERIC(78,0) NOT NULL CHECK(amount>0 AND amount<115792089237316195423570985008687907853269984665640564039457584007913129639936),
 canonical BOOLEAN NOT NULL,
 PRIMARY KEY(chain_id,block_number,block_hash,log_index),
 FOREIGN KEY(chain_id,block_number,block_hash,transaction_index,transaction_hash)
 REFERENCES transaction_inclusions(chain_id,block_number,block_hash,tx_index,tx_hash),
 CHECK(from_address<>to_address)
) PARTITION BY RANGE(block_number);
CREATE TABLE native_transfers_p_0_1000000 PARTITION OF native_transfers FOR VALUES FROM(0) TO(1000000);
CREATE TABLE native_transfers_default PARTITION OF native_transfers DEFAULT;
CREATE INDEX native_transfers_from_idx ON native_transfers(chain_id,from_address,block_number DESC,transaction_index DESC,log_index DESC) WHERE canonical;
CREATE INDEX native_transfers_to_idx ON native_transfers(chain_id,to_address,block_number DESC,transaction_index DESC,log_index DESC) WHERE canonical;
CREATE INDEX native_transfers_transaction_idx ON native_transfers(chain_id,transaction_hash,log_index) WHERE canonical;
CREATE INDEX blocks_amsterdam_idx ON blocks(chain_id,number,hash) WHERE slot_number_raw IS NOT NULL AND slot_number_raw<>'null'::jsonb;

CREATE TABLE native_transfer_coverage (
 chain_id NUMERIC(78,0) PRIMARY KEY REFERENCES chains(chain_id),
 covered NUMMULTIRANGE NOT NULL DEFAULT '{}'::nummultirange
);

-- Coverage is revoked on replay/lease changes as well as canonical detach. Only
-- the shared lease-fenced publication view can add an interval.
CREATE FUNCTION refresh_native_transfer_coverage(p_chain NUMERIC,p_number NUMERIC) RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE segment NUMMULTIRANGE; ready BOOLEAN;
BEGIN
 INSERT INTO native_transfer_coverage(chain_id) VALUES(p_chain) ON CONFLICT DO NOTHING;
 PERFORM 1 FROM native_transfer_coverage WHERE chain_id=p_chain FOR UPDATE;
 segment := nummultirange(numrange(p_number,p_number+1,'[)'));
 SELECT EXISTS(SELECT 1 FROM canonical_blocks c JOIN published_block_stage_results s
 ON s.chain_id=c.chain_id AND s.block_hash=c.block_hash AND s.block_number=c.number
 WHERE c.chain_id=p_chain AND c.number=p_number AND s.stage='native_transfer'
 AND s.stage_version=1 AND s.state='complete') INTO ready;
 UPDATE native_transfer_coverage SET covered=CASE WHEN ready THEN covered+segment ELSE covered-segment END WHERE chain_id=p_chain;
END $$;

CREATE FUNCTION native_transfer_job_coverage() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 -- Heartbeats run while holding the lease's in-process guard. They must not
 -- wait for the coverage lock held by a publisher before its final guarded
 -- CAS. Only changes to the publication view's job inputs can affect coverage;
 -- lease expiry matters by nullness, not by the renewed timestamp value.
 IF TG_OP='UPDATE' AND ROW(
  NEW.kind,NEW.chain_id,NEW.stage,NEW.stage_version,NEW.payload,NEW.status,
  NEW.requested_generation,NEW.claimed_generation,NEW.completed_generation,
  NEW.leased_generation,NEW.leased_by,NEW.lease_token,
  NEW.lease_expires_at IS NULL,NEW.result
 ) IS NOT DISTINCT FROM ROW(
  OLD.kind,OLD.chain_id,OLD.stage,OLD.stage_version,OLD.payload,OLD.status,
  OLD.requested_generation,OLD.claimed_generation,OLD.completed_generation,
  OLD.leased_generation,OLD.leased_by,OLD.lease_token,
  OLD.lease_expires_at IS NULL,OLD.result
 ) THEN RETURN NEW; END IF;
 IF NEW.kind='enrichment' AND NEW.stage='native_transfer' AND NEW.stage_version=1
 AND NEW.payload->>'block_number' ~ '^(0|[1-9][0-9]*)$' THEN
 PERFORM refresh_native_transfer_coverage(NEW.chain_id,(NEW.payload->>'block_number')::numeric);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER native_transfer_job_coverage_trigger AFTER INSERT OR UPDATE ON durable_jobs
FOR EACH ROW WHEN(NEW.stage='native_transfer') EXECUTE FUNCTION native_transfer_job_coverage();

CREATE FUNCTION native_transfer_result_coverage() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.stage='native_transfer' THEN PERFORM refresh_native_transfer_coverage(OLD.chain_id,OLD.block_number); END IF;
  RETURN OLD;
 END IF;
 IF NEW.stage='native_transfer' THEN PERFORM refresh_native_transfer_coverage(NEW.chain_id,NEW.block_number); END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER native_transfer_result_coverage_trigger AFTER INSERT OR UPDATE OR DELETE ON block_stage_results
FOR EACH ROW EXECUTE FUNCTION native_transfer_result_coverage();

CREATE FUNCTION native_transfer_canonical_coverage() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
 PERFORM refresh_native_transfer_coverage(OLD.chain_id,OLD.number); RETURN OLD;
 END IF;
 PERFORM refresh_native_transfer_coverage(NEW.chain_id,NEW.number); RETURN NEW;
END $$;
CREATE TRIGGER native_transfer_canonical_coverage_trigger AFTER INSERT OR UPDATE OR DELETE ON canonical_blocks
FOR EACH ROW EXECUTE FUNCTION native_transfer_canonical_coverage();

DO $$
DECLARE fn TEXT;
BEGIN
 FOREACH fn IN ARRAY ARRAY['refresh_native_transfer_coverage(numeric,numeric)','native_transfer_job_coverage()','native_transfer_result_coverage()','native_transfer_canonical_coverage()'] LOOP
 EXECUTE format('ALTER FUNCTION %s SET search_path = %I, pg_catalog',fn,current_schema());
 END LOOP;
END $$;

-- Pending canonical publication also gates the shared stage view. Revoking or
-- publishing that outbox entry must invalidate the same coverage interval.
CREATE FUNCTION native_transfer_outbox_coverage() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE number NUMERIC; chain NUMERIC; key TEXT;
BEGIN
 IF TG_OP='DELETE' THEN chain:=OLD.chain_id; key:=OLD.message_key;
 ELSE chain:=NEW.chain_id; key:=NEW.message_key; END IF;
 IF key !~ '^0x[0-9a-f]{64}$' THEN
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
 END IF;
 SELECT c.number INTO number FROM canonical_blocks c
 WHERE c.chain_id=chain AND c.block_hash=decode(substr(key,3),'hex');
 IF number IS NOT NULL THEN PERFORM refresh_native_transfer_coverage(chain,number); END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER native_transfer_outbox_coverage_trigger AFTER INSERT OR UPDATE OR DELETE ON transactional_outbox
FOR EACH ROW EXECUTE FUNCTION native_transfer_outbox_coverage();
DO $$ BEGIN EXECUTE format('ALTER FUNCTION native_transfer_outbox_coverage() SET search_path = %I, pg_catalog',current_schema()); END $$;

-- Native transfer jobs participate in the database-enforced publication fence,
-- not only the Go worker's guard.
DO $$
DECLARE target REGPROCEDURE; definition TEXT; upgraded TEXT;
BEGIN
 FOREACH target IN ARRAY ARRAY[
 'require_enrichment_publication_protocol()'::regprocedure,
 'enforce_enrichment_terminal_publication()'::regprocedure
 ] LOOP
 definition:=pg_get_functiondef(target);
 upgraded:=replace(definition, '(''proxy'', ''abi'', ''token'', ''stats'', ''trace'')', '(''proxy'', ''abi'', ''token'', ''stats'', ''trace'', ''native_transfer'')');
 IF definition=upgraded THEN RAISE EXCEPTION 'function % has no publication stage guard',target; END IF;
 EXECUTE upgraded;
 END LOOP;
END $$;
