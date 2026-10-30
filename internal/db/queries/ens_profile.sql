-- name: ReadENSKnownName :one
SELECT ens_name_known(p.source_id,@name::text)::boolean FROM ens_effective_policies p
WHERE p.target_chain_id= @chain_id AND p.policy_digest= @policy_digest;

-- name: ReadENSProfilePublications :many
SELECT e.id,e.direction,e.state,e.address,e.valid_until,o.profile_data,o.block_hash,b.number
FROM ens_effective_publications e
JOIN ens_sync_publications pub ON pub.id=e.selected_publication_id
JOIN ens_sync_observations o ON o.id=pub.observation_id
JOIN ens_sync_blocks b ON b.source_id=o.source_id AND b.hash=o.block_hash
WHERE e.target_chain_id= @chain_id AND e.policy_digest= @policy_digest AND e.lookup_key= @name
AND e.direction IN ('profile','forward') AND ens_effective_publication_current(e.id)
AND ((e.direction='profile' AND e.id= @profile_id) OR (e.direction='forward' AND e.id= @forward_id))
ORDER BY e.id;

-- name: ReadENSLatestProfileIDs :one
SELECT COALESCE(max(id) FILTER(WHERE direction='profile'),0)::bigint AS profile_id,
 COALESCE(max(id) FILTER(WHERE direction='forward'),0)::bigint AS forward_id
FROM ens_effective_publications WHERE target_chain_id= @chain_id AND policy_digest= @policy_digest AND lookup_key= @name;

-- name: ReadENSDirectoryFence :one
SELECT COALESCE((SELECT max(id) FROM runtime_events r WHERE r.chain_id= @chain_id AND event_type IN ('names','reorg')),0)::bigint AS version,
 (SELECT min(LEAST(e.valid_until,
   CASE WHEN o.profile_data->>'registration_state'='registered'
     AND o.profile_data#>>'{expiration_time,value}' ~ '^[0-9]{1,20}$'
   THEN CASE WHEN (o.profile_data#>>'{expiration_time,value}')::numeric
     BETWEEN extract(epoch FROM now()) AND extract(epoch FROM e.valid_until)
     THEN to_timestamp((o.profile_data#>>'{expiration_time,value}')::double precision) END END))
 FROM ens_effective_publications e
 JOIN ens_sync_publications p ON p.id=e.selected_publication_id
 JOIN ens_sync_observations o ON o.id=p.observation_id WHERE e.target_chain_id= @chain_id AND e.policy_digest= @policy_digest AND e.direction IN ('profile','forward') AND ens_effective_publication_current(e.id))::timestamptz AS valid_until,
 COALESCE((SELECT max(l.block_number) FROM logs l JOIN canonical_blocks cb ON cb.chain_id=l.chain_id AND cb.number=l.block_number AND cb.block_hash=l.block_hash
 WHERE l.chain_id= @chain_id AND EXISTS(SELECT 1 FROM ens_sync_job_watches w JOIN ens_effective_policies p ON p.source_id=w.source_id WHERE p.target_chain_id= @chain_id AND p.policy_digest= @policy_digest AND w.address=l.address)),0)::numeric AS head;
