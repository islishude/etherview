package query

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/consensus"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/publicquery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type requestCursor struct {
	Chain      string `json:"chain"`
	Hash       string `json:"hash"`
	Filter     int32  `json:"filter"`
	Job        int64  `json:"job"`
	Generation int64  `json:"generation"`
	Type       int32  `json:"type"`
	Index      int32  `json:"index"`
}

func (r *PostgresReader) BlockRequests(ctx context.Context, id, filter, encoded string, limit int) (gen.BlockRequests, string, error) {
	result := gen.BlockRequests{State: gen.ConsensusStateDisabled, Items: []gen.ExecutionRequest{}}
	kind := int32(-1)
	if filter != "" {
		switch filter {
		case "0x00":
			kind = 0
		case "0x01":
			kind = 1
		case "0x02":
			kind = 2
		case "0x03":
			kind = 3
		case "0x04":
			kind = 4
		default:
			return result, "", publicquery.ErrInvalidInput
		}
	}
	if limit < 1 || limit > 100 {
		return result, "", publicquery.ErrInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, "", err
	}
	defer dbaccess.Rollback(ctx, tx)
	number, hash, err := r.resolveBlockTransactionTarget(ctx, tx, id)
	if err != nil {
		return result, "", err
	}
	result.BlockHash = hash.Hex()
	if !r.consensus.Enabled {
		return result, "", nil
	}
	chain, err := r.chainNumeric()
	if err != nil {
		return result, "", err
	}
	q := dbgen.New(tx)
	row, err := q.RequestsReadSet(ctx, chain, hash.Bytes())
	if errors.Is(err, pgx.ErrNoRows) {
		if encoded != "" {
			return result, "", ErrInvalidCursor
		}
		result.State = gen.ConsensusStatePending
		state, stateErr := q.ConsensusStageState(ctx, chain, hash.Bytes(), "requests")
		if errors.Is(stateErr, pgx.ErrNoRows) {
			return result, "", nil
		}
		if stateErr != nil {
			return result, "", stateErr
		}
		result.State = gen.ConsensusState(state.State.String)
		if state.Code != "" {
			result.Code = &state.Code
		}
		if state.Code == "not_applicable" {
			result.State = gen.ConsensusStateNotApplicable
		}
		return result, "", nil
	}
	if err != nil {
		return result, "", err
	}
	var manifest consensus.RequestSet
	if json.Unmarshal(row.Manifest, &manifest) != nil || manifest.BlockHash != hash.Hex() || row.BlockNumber != strconv.FormatUint(number, 10) || len(manifest.Counts) != 5 {
		return result, "", errors.New("invalid requests manifest")
	}
	cursor := requestCursor{Chain: r.chainID, Hash: hash.Hex(), Filter: kind, Job: row.DurableJobID, Generation: row.JobGeneration, Type: -1, Index: -1}
	if encoded != "" {
		var provided requestCursor
		if publicquery.DecodeCursor(encoded, &provided) != nil || provided.Chain != cursor.Chain || provided.Hash != cursor.Hash || provided.Filter != kind || provided.Job != cursor.Job || provided.Generation != cursor.Generation || provided.Type < 0 || provided.Type > 4 || provided.Index < 0 || provided.Index > 8191 || (kind >= 0 && provided.Type != kind) {
			return result, "", ErrInvalidCursor
		}
		cursor = provided
	}
	var blockNumber pgtype.Numeric
	if err := blockNumber.Scan(row.BlockNumber); err != nil {
		return result, "", err
	}
	rows, err := q.RequestsListItems(ctx, dbgen.RequestsListItemsParams{ChainID: chain, BlockNumber: blockNumber, BlockHash: hash.Bytes(), FilterType: kind, AfterType: cursor.Type, AfterIndex: cursor.Index, PageLimit: int32(limit + 1)})
	if err != nil {
		return result, "", err
	}
	result.State = gen.ConsensusStateComplete
	result.Total = &manifest.Total
	result.Counts = &manifest.Counts
	result.RequestsHash = &manifest.RequestsHash
	result.BeaconRoot = &manifest.BeaconRoot
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		cursor.Type = int32(last.RequestType)
		cursor.Index = last.SequenceIndex
		next, err = publicquery.EncodeCursor(cursor)
		if err != nil {
			return result, "", err
		}
	}
	for _, row := range rows {
		var record consensus.ExecutionRequest
		if json.Unmarshal(row.Record, &record) != nil {
			return result, "", errors.New("invalid request record")
		}
		if record.SourceAddress != "" {
			address, err := ChecksumAddress(record.SourceAddress)
			if err != nil {
				return result, "", errors.New("invalid request record")
			}
			// Keep protocol evidence normalized; checksum only the public projection.
			record.SourceAddress = address
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return result, "", errors.New("invalid request record")
		}
		var item gen.ExecutionRequest
		if json.Unmarshal(encoded, &item) != nil {
			return result, "", errors.New("invalid request record")
		}
		result.Items = append(result.Items, item)
	}
	return result, next, nil
}
