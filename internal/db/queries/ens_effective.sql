-- name: RegisterENSEffectivePolicy :exec
INSERT INTO ens_effective_policies(target_chain_id,policy_digest,mode,official_source_id,custom_source_id,unsupported_coin,coin_type,custom_coin_type)
VALUES (@target_chain_id,@policy_digest,@mode,sqlc.narg(official_source_id),sqlc.narg(custom_source_id),@unsupported_coin,@coin_type,@custom_coin_type)
ON CONFLICT(target_chain_id,policy_digest) DO UPDATE SET mode=EXCLUDED.mode,official_source_id=EXCLUDED.official_source_id,
 custom_source_id=EXCLUDED.custom_source_id,unsupported_coin=EXCLUDED.unsupported_coin,coin_type=EXCLUDED.coin_type,custom_coin_type=EXCLUDED.custom_coin_type;

-- name: GetENSEffectivePolicy :one
SELECT * FROM ens_effective_policies WHERE target_chain_id= @target_chain_id AND policy_digest= @policy_digest;

-- name: ListENSEffectivePoliciesForSource :many
SELECT * FROM ens_effective_policies WHERE official_source_id= @source_id OR custom_source_id= @source_id ORDER BY target_chain_id,policy_digest LIMIT 65 FOR UPDATE;

-- name: PutENSEffectivePublication :one
INSERT INTO ens_effective_publications(target_chain_id,policy_digest,direction,lookup_key,official_publication_id,custom_publication_id,selected_publication_id,state,code,name,address,namespace,valid_until,retain_until)
VALUES (@target_chain_id,@policy_digest,@direction,@lookup_key,sqlc.narg(official_publication_id),sqlc.narg(custom_publication_id),sqlc.narg(selected_publication_id),@state,sqlc.narg(code),sqlc.narg(name),@address,sqlc.narg(namespace),@valid_until,@retain_until)
RETURNING id;

-- name: ReadENSEffectivePublication :one
SELECT * FROM ens_effective_publications WHERE target_chain_id= @target_chain_id AND policy_digest= @policy_digest
AND direction= @direction AND lookup_key= @lookup_key AND (@snapshot::bigint=-1 OR id<= @snapshot::bigint)
ORDER BY id DESC LIMIT 1;

-- name: GetENSEffectiveSnapshot :one
SELECT COALESCE(MAX(id),0)::bigint FROM ens_effective_publications WHERE target_chain_id= @target_chain_id AND policy_digest= @policy_digest;

-- name: EmitENSNamesEvent :exec
INSERT INTO runtime_events(chain_id,event_type,payload)
VALUES (@target_chain_id,'names',jsonb_build_object('publication',CAST(sqlc.arg(publication) AS text)));

-- name: EmitENSInvalidation :exec
INSERT INTO runtime_events(chain_id,event_type,payload)
SELECT DISTINCT target_chain_id,'names',jsonb_build_object('source',CAST(sqlc.arg(source_id) AS text),'invalidated',true)
FROM ens_effective_policies WHERE official_source_id=sqlc.arg(source_id)::bigint OR custom_source_id=sqlc.arg(source_id)::bigint;

-- name: GetENSEffectiveRetentionFloor :one
SELECT COALESCE((SELECT min_publication FROM ens_effective_retention WHERE target_chain_id= @target_chain_id AND policy_digest= @policy_digest),0)::bigint;

-- name: DeleteExpiredENSEffectivePublications :one
WITH expired AS (
 SELECT e.id FROM ens_effective_publications e WHERE e.target_chain_id= @target_chain_id AND e.retain_until< @expired_before
 AND NOT EXISTS(SELECT 1 FROM search_catalog_documents d WHERE d.name_publication_id=e.id)
 AND EXISTS(SELECT 1 FROM ens_effective_publications newer WHERE newer.target_chain_id=e.target_chain_id AND newer.policy_digest=e.policy_digest AND newer.direction=e.direction AND newer.lookup_key=e.lookup_key AND newer.id>e.id)
 ORDER BY e.id LIMIT @row_limit FOR UPDATE SKIP LOCKED
), deleted AS (
 DELETE FROM ens_effective_publications e USING expired WHERE e.id=expired.id RETURNING e.*
), floors AS (
 INSERT INTO ens_effective_retention(target_chain_id,policy_digest,min_publication)
 SELECT target_chain_id,policy_digest,MAX(id)+1 FROM deleted GROUP BY target_chain_id,policy_digest
 ON CONFLICT(target_chain_id,policy_digest) DO UPDATE SET min_publication=GREATEST(ens_effective_retention.min_publication,EXCLUDED.min_publication)
) SELECT count(*)::bigint FROM deleted;

-- name: DeleteExpiredENSSyncObservations :one
WITH expired AS (
 SELECT o.id,o.job_id FROM ens_sync_observations o
 JOIN ens_sync_sources s ON s.id=o.source_id
 JOIN ens_sync_publications pub ON pub.observation_id=o.id
 WHERE s.target_chain_id= @target_chain_id AND pub.retain_until< @expired_before
 AND NOT EXISTS(SELECT 1 FROM ens_effective_publications e WHERE e.official_publication_id=pub.id OR e.custom_publication_id=pub.id OR e.selected_publication_id=pub.id)
 AND EXISTS(SELECT 1 FROM ens_sync_observations newer WHERE newer.source_id=o.source_id AND newer.candidate_id=o.candidate_id AND newer.id>o.id)
 AND (o.evidence<>'projection' OR EXISTS(SELECT 1 FROM ens_sync_observations newer WHERE newer.source_id=o.source_id AND newer.candidate_id=o.candidate_id AND newer.evidence='projection' AND newer.id>o.id))
 ORDER BY o.id LIMIT @row_limit FOR UPDATE OF o SKIP LOCKED
), publications AS (
 DELETE FROM ens_sync_publications p USING expired WHERE p.observation_id=expired.id RETURNING p.observation_id
), observations AS (
 DELETE FROM ens_sync_observations o USING publications WHERE o.id=publications.observation_id RETURNING o.job_id
), dependencies AS (
 DELETE FROM ens_sync_job_dependencies d USING observations WHERE d.job_id=observations.job_id
), jobs AS (
 DELETE FROM ens_sync_jobs j USING observations WHERE j.id=observations.job_id
) SELECT count(*)::bigint FROM observations;

-- name: ReadENSEffectiveName :one
SELECT e.*,ens_effective_publication_current(e.id)::boolean AS current,
 CASE
 WHEN s.state IN ('halted','unsupported') THEN 'unsupported_source'
 WHEN s.state<>'active' OR s.target_hash IS DISTINCT FROM s.scanned_hash THEN 'sync_pending'
 WHEN s.healthy_until<=clock_timestamp() OR e.valid_until<=clock_timestamp() OR e.retain_until<=clock_timestamp() THEN 'stale'
 ELSE COALESCE(e.code,'resolution_pending') END::text AS unavailable_code
FROM ens_effective_publications e
JOIN ens_effective_policies policy ON policy.target_chain_id=e.target_chain_id AND policy.policy_digest=e.policy_digest
LEFT JOIN ens_sync_progress s ON s.source_id=CASE WHEN policy.mode='custom_only' OR policy.unsupported_coin THEN policy.custom_source_id ELSE policy.official_source_id END
WHERE e.target_chain_id= @target_chain_id AND e.policy_digest= @policy_digest AND e.direction= @direction AND e.lookup_key= @lookup_key AND e.id<= @snapshot
ORDER BY e.id DESC LIMIT 1;

-- name: ReadENSEffectiveSnapshot :one
SELECT COALESCE(MAX(e.id),0)::bigint AS version,MAX(e.created_at)::timestamptz AS created_at,
 COALESCE((SELECT min_publication FROM ens_effective_retention r WHERE r.target_chain_id= @target_chain_id AND r.policy_digest= @policy_digest),0)::bigint AS minimum
FROM ens_effective_publications e WHERE e.target_chain_id= @target_chain_id AND e.policy_digest= @policy_digest;

-- name: ReadENSNameSourceStatus :many
SELECT s.namespace,p.state,p.failure_code,p.target_number,p.scanned_number,p.healthy_until,
 ARRAY(SELECT DISTINCT protocol FROM ens_sync_deployments d WHERE d.source_id=s.id ORDER BY protocol)::text[] AS protocols,
 (SELECT count(*) FROM ens_sync_candidates c LEFT JOIN ens_sync_jobs j ON j.source_id=c.source_id AND j.candidate_id=c.id AND j.generation=c.requested_generation
  WHERE c.source_id=s.id AND (j.id IS NULL OR j.state<>'complete'))::bigint AS pending,
 (SELECT MAX(o.observed_at) FROM ens_sync_observations o WHERE o.source_id=s.id)::timestamptz AS last_success,
 (SELECT MAX(b.number) FROM ens_sync_observations o JOIN ens_sync_blocks b ON b.source_id=o.source_id AND b.hash=o.block_hash AND b.canonical
  WHERE o.source_id=s.id)::numeric AS published
FROM ens_effective_policies policy JOIN ens_sync_sources s ON s.id=policy.official_source_id OR s.id=policy.custom_source_id
JOIN ens_sync_progress p ON p.source_id=s.id WHERE policy.target_chain_id= @target_chain_id AND policy.policy_digest= @policy_digest
ORDER BY s.namespace LIMIT 2;

-- name: ReadENSPendingNameCode :one
SELECT (CASE WHEN p.state IN ('unsupported','halted') THEN 'unsupported_source'
 WHEN p.state<>'active' OR p.target_hash IS DISTINCT FROM p.scanned_hash THEN 'sync_pending'
 WHEN p.healthy_until<=clock_timestamp() THEN 'stale'
 WHEN c.id IS NULL THEN 'not_indexed'
 ELSE COALESCE(j.failure_code,'resolution_pending') END)::text AS code
FROM ens_effective_policies policy
JOIN ens_sync_progress p ON p.source_id=CASE WHEN policy.mode='custom_only' OR policy.unsupported_coin THEN policy.custom_source_id ELSE policy.official_source_id END
LEFT JOIN ens_sync_candidates c ON c.source_id=p.source_id AND c.direction= @direction AND c.lookup_key= @lookup_key AND c.coin_type=CASE WHEN p.source_id=policy.custom_source_id THEN policy.custom_coin_type ELSE policy.coin_type END
LEFT JOIN ens_sync_jobs j ON j.source_id=c.source_id AND j.candidate_id=c.id AND j.generation=c.requested_generation
WHERE policy.target_chain_id= @target_chain_id AND policy.policy_digest= @policy_digest;

-- name: ValidateENSEffectiveSnapshotVersion :one
SELECT EXISTS(SELECT 1 FROM ens_effective_publications e WHERE e.target_chain_id= @target_chain_id AND e.policy_digest= @policy_digest AND e.id= @version AND e.retain_until>clock_timestamp())::boolean;
