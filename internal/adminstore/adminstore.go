// Package adminstore implements the operator-only label and repair command
// persistence used by the single Etherview CLI.
package adminstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/maintenance"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
	FailurePresent bool       `json:"failure_present"`
}

type Repository struct {
	db             *sql.DB
	chainID        string
	numericChainID pgtype.Numeric
}

func New(db *sql.DB, chainID uint64) (*Repository, error) {
	if db == nil {
		return nil, errors.New("admin repository database is nil")
	}
	if chainID == 0 {
		return nil, errors.New("admin repository chain ID is zero")
	}
	return &Repository{
		db: db, chainID: strconv.FormatUint(chainID, 10),
		numericChainID: pgtype.Numeric{Int: new(big.Int).SetUint64(chainID), Valid: true},
	}, nil
}

func (r *Repository) SetLabel(ctx context.Context, kind, key, label string) (Label, error) {
	kind, key, label, err := validateLabel(kind, key, label)
	if err != nil {
		return Label{}, err
	}
	var row dbgen.UpsertOperatorLabelRow
	err = dbaccess.WithQueries(ctx, r.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.UpsertOperatorLabel(ctx, dbgen.UpsertOperatorLabelParams{
			ChainID: r.numericChainID, ObjectKind: kind, ObjectKey: key, Label: label,
		})
		return queryErr
	})
	if err != nil {
		return Label{}, fmt.Errorf("set operator label: %w", err)
	}
	return storedLabel(row.ObjectKind, row.ObjectKey, row.Label, row.CreatedAt, row.UpdatedAt)
}

func (r *Repository) DeleteLabel(ctx context.Context, kind, key string) (Label, error) {
	var err error
	kind, key, err = normalizeLabelKey(kind, key)
	if err != nil {
		return Label{}, err
	}
	var row dbgen.DeleteOperatorLabelRow
	err = dbaccess.WithQueries(ctx, r.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.DeleteOperatorLabel(ctx, r.numericChainID, kind, key)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Label{}, errors.New("operator label not found")
	}
	if err != nil {
		return Label{}, fmt.Errorf("delete operator label: %w", err)
	}
	return storedLabel(row.ObjectKind, row.ObjectKey, row.Label, row.CreatedAt, row.UpdatedAt)
}

func (r *Repository) Labels(ctx context.Context) ([]Label, error) {
	var rows []dbgen.ListOperatorLabelsRow
	err := dbaccess.WithQueries(ctx, r.db, func(queries *dbgen.Queries) error {
		var queryErr error
		rows, queryErr = queries.ListOperatorLabels(ctx, r.numericChainID)
		return queryErr
	})
	if err != nil {
		return nil, fmt.Errorf("list operator labels: %w", err)
	}
	labels := make([]Label, 0, len(rows))
	for _, row := range rows {
		label, err := storedLabel(row.ObjectKind, row.ObjectKey, row.Label, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, err
		}
		labels = append(labels, label)
	}
	return labels, nil
}

func storedLabel(
	kind, key, label string,
	createdAt, updatedAt pgtype.Timestamptz,
) (Label, error) {
	if !createdAt.Valid || !updatedAt.Valid {
		return Label{}, errors.New("operator label has invalid timestamps")
	}
	return Label{
		Kind: kind, Key: key, Label: label,
		CreatedAt: createdAt.Time.UTC(), UpdatedAt: updatedAt.Time.UTC(),
	}, nil
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

// RepairRequests returns a bounded newest-first operator view. It deliberately
// exposes only whether a failure was recorded, never the stored nested error
// text, which may originate at an RPC or database boundary.
func (r *Repository) RepairRequests(ctx context.Context, limit int) ([]RepairRequest, error) {
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("repair request limit must be between 1 and 1000")
	}
	var rows []dbgen.ListRepairRequestsRow
	err := dbaccess.WithQueries(ctx, r.db, func(queries *dbgen.Queries) error {
		var queryErr error
		rows, queryErr = queries.ListRepairRequests(ctx, r.numericChainID, int32(limit))
		return queryErr
	})
	if err != nil {
		return nil, fmt.Errorf("list repair requests: %w", err)
	}
	requests := make([]RepairRequest, 0, len(rows))
	for _, row := range rows {
		request := RepairRequest{
			ID: row.ID, Operation: row.Operation, Stage: row.Stage,
			AllowFinalized: row.AllowFinalized, Reason: row.Reason,
			Status: row.Status, FailurePresent: row.FailurePresent,
		}
		request.FromBlock, err = strconv.ParseUint(row.FromBlock, 10, 64)
		if err != nil {
			return nil, errors.New("repair request start block exceeds uint64")
		}
		request.ToBlock, err = strconv.ParseUint(row.ToBlock, 10, 64)
		if err != nil {
			return nil, errors.New("repair request end block exceeds uint64")
		}
		if !row.RequestedAt.Valid {
			return nil, errors.New("repair request has invalid requested timestamp")
		}
		request.RequestedAt = row.RequestedAt.Time.UTC()
		if row.StartedAt.Valid {
			value := row.StartedAt.Time.UTC()
			request.StartedAt = &value
		}
		if row.CompletedAt.Valid {
			value := row.CompletedAt.Time.UTC()
			request.CompletedAt = &value
		}
		requests = append(requests, request)
	}
	return requests, nil
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
