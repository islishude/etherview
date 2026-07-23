package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
)

type blockCursor struct {
	ChainID        string `json:"chain_id"`
	SnapshotNumber uint64 `json:"snapshot_number"`
	SnapshotHash   string `json:"snapshot_hash"`
	BeforeNumber   uint64 `json:"before_number"`
	BeforeHash     string `json:"before_hash"`
}

type searchCursor struct {
	ChainID             string `json:"chain_id"`
	SnapshotNumber      uint64 `json:"snapshot_number"`
	SnapshotHash        string `json:"snapshot_hash"`
	Generation          int64  `json:"generation"`
	Query               string `json:"query"`
	ResolvedNameAddress string `json:"resolved_name_address,omitempty"`
	AfterRank           int    `json:"after_rank"`
	AfterKind           string `json:"after_kind"`
	AfterKey            string `json:"after_key"`
}

func (r *PostgresReader) currentBlockCursor(ctx context.Context, tx *sql.Tx) (blockCursor, error) {
	var numberText string
	var hashBytes []byte
	if err := tx.QueryRowContext(ctx, currentTipSQL, r.chainID).Scan(&numberText, &hashBytes); err != nil {
		if err == sql.ErrNoRows {
			return blockCursor{}, httpUnavailableNotReady()
		}
		return blockCursor{}, fmt.Errorf("query canonical cursor snapshot: %w", err)
	}
	number, err := parseDecimalUint64(numberText)
	if err != nil {
		return blockCursor{}, fmt.Errorf("decode cursor snapshot number: %w", err)
	}
	hash, err := decodeHashBytes(hashBytes)
	if err != nil {
		return blockCursor{}, err
	}
	return blockCursor{
		ChainID: r.chainID, SnapshotNumber: number, SnapshotHash: hash.String(),
		BeforeNumber: number, BeforeHash: hash.String(),
	}, nil
}

func (r *PostgresReader) validateBlockCursor(ctx context.Context, tx *sql.Tx, cursor blockCursor) error {
	if cursor.ChainID != r.chainID || cursor.BeforeNumber > cursor.SnapshotNumber {
		return fmt.Errorf("%w: cursor chain or ordering is invalid", ErrInvalidCursor)
	}
	snapshotHash, err := ethrpc.ParseHash(cursor.SnapshotHash)
	if err != nil {
		return fmt.Errorf("%w: invalid snapshot hash", ErrInvalidCursor)
	}
	beforeHash, err := ethrpc.ParseHash(cursor.BeforeHash)
	if err != nil {
		return fmt.Errorf("%w: invalid boundary hash", ErrInvalidCursor)
	}
	snapshotHashBytes, err := snapshotHash.Bytes()
	if err != nil {
		return fmt.Errorf("%w: invalid snapshot hash", ErrInvalidCursor)
	}
	beforeHashBytes, err := beforeHash.Bytes()
	if err != nil {
		return fmt.Errorf("%w: invalid boundary hash", ErrInvalidCursor)
	}
	var valid bool
	if err := tx.QueryRowContext(ctx, validateCursorSQL,
		r.chainID,
		strconv.FormatUint(cursor.SnapshotNumber, 10), snapshotHashBytes,
		strconv.FormatUint(cursor.BeforeNumber, 10), beforeHashBytes,
	).Scan(&valid); err != nil {
		return fmt.Errorf("validate block cursor: %w", err)
	}
	if !valid {
		return fmt.Errorf("%w: canonical branch changed", ErrInvalidCursor)
	}
	return nil
}

func (r *PostgresReader) searchHash(
	ctx context.Context,
	queryer searchQueryer,
	hash ethrpc.Hash,
	generation int64,
	limit int,
) ([]gen.SearchResult, error) {
	hashBytes, err := hash.Bytes()
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, searchHashSQL, r.chainID, hashBytes, generation, limit)
	if err != nil {
		return nil, fmt.Errorf("search hash: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	results := make([]gen.SearchResult, 0, 2)
	for rows.Next() {
		var kind, key, label string
		var rank int64
		var canonical bool
		if err := rows.Scan(&kind, &key, &label, &rank, &canonical); err != nil {
			return nil, fmt.Errorf("scan hash search result: %w", err)
		}
		if rank > int64(^uint(0)>>1) || rank < -int64(^uint(0)>>1)-1 {
			return nil, errors.New("search rank exceeds API integer range")
		}
		if label == "" || len(label) > 4096 {
			return nil, errors.New("database returned an invalid search label")
		}
		resultKind := gen.SearchResultKind(kind)
		if resultKind != gen.SearchResultKindBlock && resultKind != gen.SearchResultKindTransaction {
			return nil, fmt.Errorf("database returned unsupported core search kind %q", kind)
		}
		parsedKey, err := ethrpc.ParseHash(key)
		if err != nil {
			return nil, fmt.Errorf("database returned invalid search key: %w", err)
		}
		canonicalCopy := canonical
		results = append(results, gen.SearchResult{
			Kind: resultKind, Key: strings.ToLower(parsedKey.String()), Label: label,
			Rank: int(rank), Canonical: &canonicalCopy,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hash search results: %w", err)
	}
	return results, nil
}

func (r *PostgresReader) searchBlockNumber(
	ctx context.Context,
	queryer searchQueryer,
	height uint64,
	generation int64,
) ([]gen.SearchResult, error) {
	var numberText string
	var hashBytes []byte
	var label string
	var rank int64
	err := queryer.QueryRowContext(
		ctx, searchBlockNumberSQL, r.chainID, strconv.FormatUint(height, 10), generation,
	).Scan(&numberText, &hashBytes, &label, &rank)
	if err == sql.ErrNoRows {
		return []gen.SearchResult{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("search block number: %w", err)
	}
	if rank > int64(^uint(0)>>1) || rank < -int64(^uint(0)>>1)-1 {
		return nil, errors.New("search rank exceeds API integer range")
	}
	if label == "" || len(label) > 4096 {
		return nil, errors.New("database returned an invalid search label")
	}
	number, err := parseDecimalUint64(numberText)
	if err != nil || number != height {
		return nil, errors.New("block number search returned an inconsistent height")
	}
	hash, err := decodeHashBytes(hashBytes)
	if err != nil {
		return nil, err
	}
	canonical := true
	return []gen.SearchResult{{
		Kind: gen.SearchResultKindBlock,
		Key:  strings.ToLower(hash.String()), Label: label,
		Rank: int(rank), Canonical: &canonical,
	}}, nil
}

func (r *PostgresReader) searchText(
	ctx context.Context,
	queryer searchQueryer,
	value string,
	snapshotNumber uint64,
	generation int64,
	resolvedNameAddress string,
	boundary *searchCursor,
	limit int,
) ([]gen.SearchResult, error) {
	hasBoundary, afterRank, afterKind, afterKey := false, 0, "", ""
	if boundary != nil {
		hasBoundary, afterRank, afterKind, afterKey = true, boundary.AfterRank, boundary.AfterKind,
			canonicalSearchBoundaryKey(boundary.AfterKey)
	}
	rows, err := queryer.QueryContext(ctx, searchTextSQL,
		r.chainID, strings.ToLower(value), strconv.FormatUint(snapshotNumber, 10),
		generation, hasBoundary, afterRank, afterKind, afterKey, limit,
		strings.ToLower(resolvedNameAddress),
	)
	if err != nil {
		return nil, fmt.Errorf("search indexed names and labels: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	results := make([]gen.SearchResult, 0, limit)
	for rows.Next() {
		var kind, key, label string
		var rank int64
		var canonical sql.NullBool
		if err := rows.Scan(&kind, &key, &label, &rank, &canonical); err != nil {
			return nil, fmt.Errorf("scan indexed search result: %w", err)
		}
		result, err := normalizeSearchResult(kind, key, label, rank, canonical)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate indexed search results: %w", err)
	}
	return results, nil
}

type searchQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (r *PostgresReader) validateSearchCursor(ctx context.Context, tx *sql.Tx, cursor searchCursor, query string) error {
	if cursor.ChainID != r.chainID || cursor.Query != strings.ToLower(query) || cursor.Generation < 0 ||
		cursor.AfterKind == "" || cursor.AfterKey == "" {
		return fmt.Errorf("%w: search cursor identity is invalid", ErrInvalidCursor)
	}
	if externalNameQuery(query) {
		if _, err := ethrpc.ParseAddress(cursor.ResolvedNameAddress); err != nil {
			return fmt.Errorf("%w: search cursor name identity is invalid", ErrInvalidCursor)
		}
	} else if cursor.ResolvedNameAddress != "" {
		return fmt.Errorf("%w: unexpected search cursor name identity", ErrInvalidCursor)
	}
	hash, err := ethrpc.ParseHash(cursor.SnapshotHash)
	if err != nil {
		return fmt.Errorf("%w: search cursor hash is invalid", ErrInvalidCursor)
	}
	hashBytes, err := hash.Bytes()
	if err != nil {
		return fmt.Errorf("%w: search cursor hash is invalid", ErrInvalidCursor)
	}
	var valid bool
	if err := tx.QueryRowContext(ctx, validateSearchCursorSQL,
		r.chainID, strconv.FormatUint(cursor.SnapshotNumber, 10), hashBytes, cursor.Generation,
	).Scan(&valid); err != nil {
		return fmt.Errorf("validate search cursor: %w", err)
	}
	if !valid {
		return fmt.Errorf("%w: canonical branch changed", ErrInvalidCursor)
	}
	if cursor.ResolvedNameAddress != "" {
		visible, err := r.resolvedNameVisible(
			ctx, tx, query, cursor.ResolvedNameAddress, cursor.SnapshotNumber, cursor.Generation,
		)
		if err != nil {
			return err
		}
		if !visible {
			return fmt.Errorf("%w: resolved name left the cursor snapshot", ErrInvalidCursor)
		}
	}
	return nil
}

func (r *PostgresReader) resolvedNameVisible(
	ctx context.Context,
	tx *sql.Tx,
	name, address string,
	snapshotNumber uint64,
	generation int64,
) (bool, error) {
	parsed, err := ethrpc.ParseAddress(address)
	if err != nil {
		return false, fmt.Errorf("validate resolved name address: %w", err)
	}
	addressBytes, err := parsed.Bytes()
	if err != nil {
		return false, fmt.Errorf("validate resolved name address: %w", err)
	}
	var visible bool
	if err := tx.QueryRowContext(
		ctx,
		validateResolvedNameSQL,
		r.chainID,
		strings.ToLower(strings.TrimSpace(name)),
		strconv.FormatUint(snapshotNumber, 10),
		generation,
		addressBytes,
	).Scan(&visible); err != nil {
		return false, fmt.Errorf("validate resolved name snapshot: %w", err)
	}
	return visible, nil
}

func afterSearchBoundary(result gen.SearchResult, cursor searchCursor) bool {
	if result.Rank != cursor.AfterRank {
		return result.Rank < cursor.AfterRank
	}
	if string(result.Kind) != cursor.AfterKind {
		return string(result.Kind) > cursor.AfterKind
	}
	return canonicalSearchBoundaryKey(result.Key) > canonicalSearchBoundaryKey(cursor.AfterKey)
}

// Search documents use normalized external identities for deterministic SQL
// ordering, while address keys are rendered in EIP-55 form at the API boundary.
// Cursors must compare the normalized identity or checksum casing can reorder
// two otherwise adjacent address results and make a later page skip one.
func canonicalSearchBoundaryKey(value string) string {
	return strings.ToLower(value)
}

func normalizeSearchResult(kind, key, label string, rank int64, canonical sql.NullBool) (gen.SearchResult, error) {
	if label == "" || len(label) > 4096 {
		return gen.SearchResult{}, errors.New("database returned an invalid search label")
	}
	if rank > int64(^uint(0)>>1) || rank < -int64(^uint(0)>>1)-1 {
		return gen.SearchResult{}, errors.New("search rank exceeds API integer range")
	}
	resultKind := gen.SearchResultKind(kind)
	if !resultKind.Valid() || resultKind == gen.SearchResultKindLabel || resultKind == gen.SearchResultKindNft {
		return gen.SearchResult{}, fmt.Errorf("database returned unsupported indexed search kind %q", kind)
	}
	switch resultKind {
	case gen.SearchResultKindAddress, gen.SearchResultKindContract, gen.SearchResultKindToken:
		address, err := ethrpc.ParseAddress(key)
		if err != nil {
			return gen.SearchResult{}, fmt.Errorf("database returned invalid search address: %w", err)
		}
		key, err = ChecksumAddress(address.String())
		if err != nil {
			return gen.SearchResult{}, err
		}
	case gen.SearchResultKindBlock:
		if hash, err := ethrpc.ParseHash(key); err == nil {
			key = strings.ToLower(hash.String())
		} else if height, parseErr := parseDecimalUint64(key); parseErr != nil || strconv.FormatUint(height, 10) != key {
			return gen.SearchResult{}, errors.New("database returned invalid block search key")
		}
	case gen.SearchResultKindTransaction:
		hash, err := ethrpc.ParseHash(key)
		if err != nil {
			return gen.SearchResult{}, fmt.Errorf("database returned invalid transaction search key: %w", err)
		}
		key = strings.ToLower(hash.String())
	}
	result := gen.SearchResult{Kind: resultKind, Key: key, Label: label, Rank: int(rank)}
	if canonical.Valid {
		value := canonical.Bool
		result.Canonical = &value
	}
	return result, nil
}

func mergeSearchResults(results []gen.SearchResult, extra gen.SearchResult, limit int) []gen.SearchResult {
	for _, result := range results {
		if result.Kind == extra.Kind && strings.EqualFold(result.Key, extra.Key) {
			return results
		}
	}
	results = append(results, extra)
	sort.SliceStable(results, func(left, right int) bool {
		if results[left].Rank != results[right].Rank {
			return results[left].Rank > results[right].Rank
		}
		if results[left].Kind != results[right].Kind {
			return results[left].Kind < results[right].Kind
		}
		return canonicalSearchBoundaryKey(results[left].Key) < canonicalSearchBoundaryKey(results[right].Key)
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

func httpUnavailableNotReady() error {
	// Kept in this file to make the cursor path's empty-database behavior
	// explicit without treating an empty chain as a missing block.
	return fmt.Errorf("%w: canonical index is empty", httpapi.ErrNotReady)
}

const currentTipSQL = `
SELECT number::text, block_hash
FROM canonical_blocks
WHERE chain_id = $1::numeric
ORDER BY number DESC
LIMIT 1`

const validateCursorSQL = `
SELECT
    EXISTS (
        SELECT 1 FROM canonical_blocks
        WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
    )
AND EXISTS (
        SELECT 1 FROM canonical_blocks
        WHERE chain_id = $1::numeric AND number = $4::numeric AND block_hash = $5
    )`

const validateSearchCursorSQL = `
SELECT EXISTS (
    SELECT 1 FROM canonical_blocks
    WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
) AND COALESCE((
    SELECT min_generation <= $4 AND generation >= $4
    FROM search_catalog_generations WHERE chain_id = $1::numeric
), $4 = 0)`

const currentSearchGenerationSQL = `
SELECT COALESCE(generation, 0), COALESCE(min_generation, 0)
FROM (SELECT 1) AS singleton
LEFT JOIN search_catalog_generations ON chain_id = $1::numeric`

const validateResolvedNameSQL = `
WITH visible_documents AS (
    SELECT document.*
    FROM search_catalog_documents AS document
    WHERE document.chain_id = $1::numeric
      AND document.valid_from_generation <= $4
      AND (document.valid_to_generation IS NULL OR document.valid_to_generation > $4)
)
SELECT EXISTS (
    SELECT 1
    FROM visible_documents AS document
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = document.chain_id
     AND canonical.number = document.block_number
     AND canonical.block_hash = document.block_hash
    WHERE document.source_kind = 'name'
      AND lower(document.result_label) = $2
      AND document.target_address = $5
      AND document.block_number <= $3::numeric
      AND document.source_canonical IS TRUE
      AND document.id = (
          SELECT latest.id
          FROM visible_documents AS latest
          JOIN canonical_blocks AS latest_canonical
            ON latest_canonical.chain_id = latest.chain_id
           AND latest_canonical.number = latest.block_number
           AND latest_canonical.block_hash = latest.block_hash
          WHERE latest.source_kind = 'name'
            AND latest.logical_identity = document.logical_identity
            AND latest.block_number <= $3::numeric
            AND latest.source_canonical IS TRUE
          ORDER BY latest.block_number DESC, latest.valid_from_generation DESC, latest.id DESC
          LIMIT 1
      )
)`
