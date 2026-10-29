-- name: BrowsingStatus :one
WITH bounds AS (
    SELECT COALESCE(sqlc.narg(from_block)::numeric, (SELECT configured_start FROM core_index_configuration WHERE chain_id=sqlc.arg(chain_id)::numeric), 0)::numeric AS first_block,
           sqlc.arg(to_block)::numeric AS last_block
), coverage AS (
    SELECT COALESCE((SELECT covered FROM browsing_coverage
        WHERE chain_id=sqlc.arg(chain_id)::numeric AND stage=sqlc.arg(stage)::text
          AND stage_version=sqlc.arg(stage_version)::integer
          AND configuration_digest=sqlc.arg(configuration_digest)::text), '{}'::nummultirange) AS covered
)
SELECT first_block::text AS from_block,
       COALESCE((SELECT revision FROM browsing_blocks
           WHERE chain_id=sqlc.arg(chain_id)::numeric AND stage=sqlc.arg(stage)::text
             AND stage_version=sqlc.arg(stage_version)::integer
             AND block_number BETWEEN first_block AND last_block
           ORDER BY revision DESC LIMIT 1),0)::text AS revision,
       CASE WHEN first_block > last_block THEN 'complete'
            WHEN covered @> numrange(first_block, last_block + 1, '[)') THEN 'complete'
            WHEN covered && nummultirange(numrange(first_block, last_block + 1, '[)')) THEN 'partial'
            ELSE 'pending' END::text AS state
FROM bounds CROSS JOIN coverage;
