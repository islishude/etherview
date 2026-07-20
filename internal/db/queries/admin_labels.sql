-- name: UpsertOperatorLabel :one
INSERT INTO operator_labels AS stored_label (
    chain_id, object_kind, object_key, label
) VALUES (
    sqlc.arg(chain_id)::numeric, sqlc.arg(object_kind),
    sqlc.arg(object_key), sqlc.arg(label)
)
ON CONFLICT (chain_id, object_kind, object_key)
DO UPDATE SET label = EXCLUDED.label, updated_at = now()
RETURNING stored_label.object_kind, stored_label.object_key,
          stored_label.label, stored_label.created_at, stored_label.updated_at;

-- name: DeleteOperatorLabel :one
DELETE FROM operator_labels
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND object_kind = sqlc.arg(object_kind)
  AND object_key = sqlc.arg(object_key)
RETURNING object_kind, object_key, label, created_at, updated_at;

-- name: ListOperatorLabels :many
SELECT object_kind, object_key, label, created_at, updated_at
FROM operator_labels
WHERE chain_id = sqlc.arg(chain_id)::numeric
ORDER BY object_kind, object_key;
