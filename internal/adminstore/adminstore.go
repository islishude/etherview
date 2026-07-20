// Package adminstore implements the operator-only label and repair command
// persistence used by the single Etherview CLI.
package adminstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/maintenance"
)

var labelKinds = map[string]bool{
	"address": true, "block": true, "transaction": true,
	"token": true, "contract": true,
}

type Label struct {
	Kind      string    `json:"kind"`
	Key       string    `json:"key"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type RepairRequest struct {
	ID             int64      `json:"id"`
	Operation      string     `json:"operation"`
	Stage          string     `json:"stage"`
	FromBlock      uint64     `json:"from_block"`
	ToBlock        uint64     `json:"to_block"`
	AllowFinalized bool       `json:"allow_finalized"`
	Reason         string     `json:"reason"`
	Status         string     `json:"status"`
	RequestedAt    time.Time  `json:"requested_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
}

type Repository struct {
	db      *sql.DB
	chainID string
}

func New(db *sql.DB, chainID uint64) (*Repository, error) {
	if db == nil {
		return nil, errors.New("admin repository database is nil")
	}
	if chainID == 0 {
		return nil, errors.New("admin repository chain ID is zero")
	}
	return &Repository{db: db, chainID: strconv.FormatUint(chainID, 10)}, nil
}

func (r *Repository) SetLabel(ctx context.Context, kind, key, label string) error {
	kind, key, label, err := validateLabel(kind, key, label)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO operator_labels (chain_id, object_kind, object_key, label)
		VALUES ($1::numeric, $2, $3, $4)
		ON CONFLICT (chain_id, object_kind, object_key)
		DO UPDATE SET label = EXCLUDED.label, updated_at = now()`, r.chainID, kind, key, label)
	if err != nil {
		return fmt.Errorf("set operator label: %w", err)
	}
	return nil
}

func (r *Repository) DeleteLabel(ctx context.Context, kind, key string) error {
	var err error
	kind, key, err = normalizeLabelKey(kind, key)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM operator_labels
		WHERE chain_id = $1::numeric AND object_kind = $2 AND object_key = $3`, r.chainID, kind, key)
	if err != nil {
		return fmt.Errorf("delete operator label: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted label count: %w", err)
	}
	if count == 0 {
		return errors.New("operator label not found")
	}
	return nil
}

func (r *Repository) Labels(ctx context.Context) ([]Label, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT object_kind, object_key, label, created_at, updated_at
		FROM operator_labels
		WHERE chain_id = $1::numeric
		ORDER BY object_kind, object_key`, r.chainID)
	if err != nil {
		return nil, fmt.Errorf("list operator labels: %w", err)
	}
	defer rows.Close()
	var labels []Label
	for rows.Next() {
		var label Label
		if err := rows.Scan(&label.Kind, &label.Key, &label.Label, &label.CreatedAt, &label.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan operator label: %w", err)
		}
		label.CreatedAt, label.UpdatedAt = label.CreatedAt.UTC(), label.UpdatedAt.UTC()
		labels = append(labels, label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operator labels: %w", err)
	}
	return labels, nil
}

func (r *Repository) EnqueueRepair(ctx context.Context, request RepairRequest) (RepairRequest, error) {
	request = normalizeRepairRequest(request)
	if err := validateRepairRequest(request); err != nil {
		return RepairRequest{}, err
	}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO repair_requests (
			chain_id, operation, stage, from_block, to_block, allow_finalized, reason
		) VALUES ($1::numeric, $2, $3, $4::numeric, $5::numeric, $6, $7)
		RETURNING id, status, requested_at`,
		r.chainID, request.Operation, request.Stage,
		strconv.FormatUint(request.FromBlock, 10), strconv.FormatUint(request.ToBlock, 10),
		request.AllowFinalized, request.Reason,
	).Scan(&request.ID, &request.Status, &request.RequestedAt)
	if err != nil {
		return RepairRequest{}, fmt.Errorf("enqueue repair request: %w", err)
	}
	request.RequestedAt = request.RequestedAt.UTC()
	return request, nil
}

func normalizeRepairRequest(request RepairRequest) RepairRequest {
	request.Operation = strings.ToLower(strings.TrimSpace(request.Operation))
	request.Stage = strings.ToLower(strings.TrimSpace(request.Stage))
	request.Reason = strings.TrimSpace(request.Reason)
	return request
}

func validateRepairRequest(request RepairRequest) error {
	if request.Stage == "" || len(request.Stage) > 64 {
		return errors.New("stage must contain 1 to 64 bytes")
	}
	if err := maintenance.ValidateOperationStage(maintenance.Operation(request.Operation), request.Stage); err != nil {
		return err
	}
	if request.ToBlock < request.FromBlock {
		return errors.New("repair range end is below its start")
	}
	if request.Reason == "" || len(request.Reason) > 1024 {
		return errors.New("repair reason must contain 1 to 1024 bytes")
	}
	return nil
}

func validateLabel(kind, key, label string) (string, string, string, error) {
	kind, key, err := normalizeLabelKey(kind, key)
	if err != nil {
		return "", "", "", err
	}
	label = strings.TrimSpace(label)
	if label == "" || len(label) > 256 {
		return "", "", "", errors.New("label value must contain 1 to 256 bytes")
	}
	return kind, key, label, nil
}

func normalizeLabelKey(kind, key string) (string, string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	key = strings.ToLower(strings.TrimSpace(key))
	if !labelKinds[kind] {
		return "", "", fmt.Errorf("unsupported label kind %q", kind)
	}
	if key == "" || len(key) > 256 {
		return "", "", errors.New("label key must contain 1 to 256 bytes")
	}
	switch kind {
	case "address", "token", "contract":
		address, err := ethrpc.ParseAddress(key)
		if err != nil {
			return "", "", fmt.Errorf("%s label key must be a 20-byte address", kind)
		}
		key = strings.ToLower(address.String())
	case "transaction":
		hash, err := ethrpc.ParseHash(key)
		if err != nil {
			return "", "", errors.New("transaction label key must be a 32-byte hash")
		}
		key = strings.ToLower(hash.String())
	case "block":
		if hash, err := ethrpc.ParseHash(key); err == nil {
			key = strings.ToLower(hash.String())
			break
		}
		height, err := strconv.ParseUint(key, 10, 64)
		if err != nil || strconv.FormatUint(height, 10) != key {
			return "", "", errors.New("block label key must be a canonical uint64 height or 32-byte hash")
		}
	}
	return kind, key, nil
}
