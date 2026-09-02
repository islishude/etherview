package query

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
)

type blockCursor struct {
	ChainID        string `json:"chain_id"`
	SnapshotNumber uint64 `json:"snapshot_number"`
	SnapshotHash   string `json:"snapshot_hash"`
	BeforeNumber   uint64 `json:"before_number"`
	BeforeHash     string `json:"before_hash"`
}

type searchCursor struct {
	ChainID                   string `json:"chain_id"`
	SnapshotNumber            uint64 `json:"snapshot_number"`
	SnapshotHash              string `json:"snapshot_hash"`
	Generation                int64  `json:"generation"`
	Query                     string `json:"query"`
	UserOperationDigest       string `json:"user_operation_digest,omitempty"`
	UserOperationSnapshot     bool   `json:"user_operation_snapshot,omitempty"`
	UserOperationSnapshotEnd  uint64 `json:"user_operation_snapshot_end,omitempty"`
	UserOperationSnapshotHash string `json:"user_operation_snapshot_hash,omitempty"`
	ResolvedName              string `json:"resolved_name,omitempty"`
	ResolvedNameAddress       string `json:"resolved_name_address,omitempty"`
	ResolvedNameObservationID int64  `json:"resolved_name_observation_id,omitempty"`
	ResolvedNameSource        string `json:"resolved_name_source,omitempty"`
	AfterRank                 int    `json:"after_rank"`
	AfterKind                 string `json:"after_kind"`
	AfterKey                  string `json:"after_key"`
}

func (r *PostgresReader) currentBlockCursor(ctx context.Context, tx *sql.Tx) (blockCursor, error) {
	var numberText string
	var hashBytes []byte
	if err := tx.QueryRowContext(ctx, dbgen.GetCurrentQueryTip, r.chainID).Scan(&numberText, &hashBytes); err != nil {
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
	var valid bool
	if err := tx.QueryRowContext(ctx, dbgen.ValidateBlockCursor,
		r.chainID,
		strconv.FormatUint(cursor.SnapshotNumber, 10), snapshotHash.Bytes(),
		strconv.FormatUint(cursor.BeforeNumber, 10), beforeHash.Bytes(),
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
	hash common.Hash,
	generation int64,
	limit int,
	userOperations userOperationSearchSnapshot,
) ([]gen.SearchResult, error) {
	rows, err := queryer.QueryContext(ctx, dbgen.QuerySearchHash, r.chainID, hash.Bytes(), generation, limit)
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
	if r.userOperations && userOperations.available {
		var userOpHash, sender []byte
		err := queryer.QueryRowContext(
			ctx, dbgen.ERC4337SearchUserOperation,
			r.chainID, r.userOperationDigest, hash[:],
			strconv.FormatUint(userOperations.end, 10),
		).Scan(&userOpHash, &sender)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("search UserOperation hash: %w", err)
		}
		if err == nil {
			if len(userOpHash) != common.HashLength || len(sender) != common.AddressLength || common.BytesToHash(userOpHash) != hash {
				return nil, errors.New("UserOperation hash search returned an invalid identity")
			}
			canonical := true
			results = mergeSearchResults(results, gen.SearchResult{
				Kind:  gen.SearchResultKindUserOperation,
				Key:   strings.ToLower(hash.Hex()),
				Label: "UserOperation · " + common.BytesToAddress(sender).Hex(),
				Rank:  95, Canonical: &canonical,
			}, limit)
		}
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
		ctx, dbgen.QuerySearchBlockNumber, r.chainID, strconv.FormatUint(height, 10), generation,
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
	resolvedNameObservationID int64,
	boundary *searchCursor,
	limit int,
) ([]gen.SearchResult, error) {
	hasBoundary, afterRank, afterKind, afterKey := false, 0, "", ""
	if boundary != nil {
		hasBoundary, afterRank, afterKind, afterKey = true, boundary.AfterRank, boundary.AfterKind,
			canonicalSearchBoundaryKey(boundary.AfterKey)
	}
	rows, err := queryer.QueryContext(ctx, dbgen.QuerySearchText, r.chainID, strings.ToLower(value), strconv.FormatUint(snapshotNumber, 10),
		generation, hasBoundary, afterRank, afterKind, afterKey, limit,
		resolvedNameObservationID,
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
		var nameSource sql.NullString
		if err := rows.Scan(&kind, &key, &label, &rank, &canonical, &nameSource); err != nil {
			return nil, fmt.Errorf("scan indexed search result: %w", err)
		}
		result, err := normalizeSearchResult(kind, key, label, rank, canonical, nameSource)
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
	expectedUserOperationDigest := ""
	if r.userOperations {
		expectedUserOperationDigest = hex.EncodeToString(r.userOperationDigest)
	}
	if cursor.ChainID != r.chainID || cursor.Query != strings.ToLower(query) || cursor.Generation < 0 ||
		cursor.UserOperationDigest != expectedUserOperationDigest ||
		cursor.AfterKind == "" || cursor.AfterKey == "" {
		return fmt.Errorf("%w: search cursor identity is invalid", ErrInvalidCursor)
	}
	if externalNameQuery(query) {
		if cursor.ResolvedNameObservationID <= 0 || cursor.ResolvedName == "" ||
			(cursor.ResolvedNameSource != "ens" && cursor.ResolvedNameSource != "custom_ens") {
			return fmt.Errorf("%w: search cursor name identity is invalid", ErrInvalidCursor)
		}
		if cursor.ResolvedNameAddress != "" {
			if _, err := ethrpc.ParseAddress(cursor.ResolvedNameAddress); err != nil {
				return fmt.Errorf("%w: search cursor name address is invalid", ErrInvalidCursor)
			}
		}
	} else if cursor.ResolvedNameAddress != "" || cursor.ResolvedName != "" ||
		cursor.ResolvedNameObservationID != 0 || cursor.ResolvedNameSource != "" {
		return fmt.Errorf("%w: unexpected search cursor name identity", ErrInvalidCursor)
	}
	hash, err := ethrpc.ParseHash(cursor.SnapshotHash)
	if err != nil {
		return fmt.Errorf("%w: search cursor hash is invalid", ErrInvalidCursor)
	}
	var valid bool
	if err := tx.QueryRowContext(ctx, dbgen.ValidateSearchCursor,
		r.chainID, strconv.FormatUint(cursor.SnapshotNumber, 10), hash.Bytes(), cursor.Generation,
	).Scan(&valid); err != nil {
		return fmt.Errorf("validate search cursor: %w", err)
	}
	if !valid {
		return fmt.Errorf("%w: canonical branch changed", ErrInvalidCursor)
	}
	if cursor.UserOperationSnapshot {
		if !r.userOperations || cursor.UserOperationSnapshotEnd < r.userOperationStart {
			return fmt.Errorf("%w: UserOperation snapshot identity is invalid", ErrInvalidCursor)
		}
		userOperationHash, err := ethrpc.ParseHash(cursor.UserOperationSnapshotHash)
		if err != nil {
			return fmt.Errorf("%w: UserOperation snapshot hash is invalid", ErrInvalidCursor)
		}
		if err := tx.QueryRowContext(
			ctx,
			dbgen.ERC4337ValidateSnapshot,
			strconv.FormatUint(r.userOperationStart, 10),
			strconv.FormatUint(cursor.UserOperationSnapshotEnd, 10),
			userOperationHash[:],
			r.chainID,
			r.userOperationDigest,
		).Scan(&valid); err != nil {
			return fmt.Errorf("validate search UserOperation snapshot: %w", err)
		}
		if !valid {
			return fmt.Errorf("%w: UserOperation coverage changed", ErrInvalidCursor)
		}
	} else if cursor.UserOperationSnapshotEnd != 0 || cursor.UserOperationSnapshotHash != "" {
		return fmt.Errorf("%w: unexpected UserOperation snapshot identity", ErrInvalidCursor)
	}
	if cursor.ResolvedNameObservationID > 0 {
		visible, err := r.resolvedNameVisible(
			ctx, tx, resolvedNameGate{
				Name: cursor.ResolvedName, Address: cursor.ResolvedNameAddress,
				Source: cursor.ResolvedNameSource, ObservationID: cursor.ResolvedNameObservationID,
			}, cursor.Generation,
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
	gate resolvedNameGate,
	generation int64,
) (bool, error) {
	var address []byte
	if gate.Address != "" {
		parsed, err := ethrpc.ParseAddress(gate.Address)
		if err != nil {
			return false, fmt.Errorf("validate resolved name address: %w", err)
		}
		address = parsed.Bytes()
	}
	var visible bool
	if err := tx.QueryRowContext(
		ctx,
		dbgen.ValidateResolvedSearchName,
		r.chainID,
		gate.ObservationID,
		gate.Name,
		gate.Source,
		generation,
		address,
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

func normalizeSearchResult(
	kind, key, label string,
	rank int64,
	canonical sql.NullBool,
	nameSource sql.NullString,
) (gen.SearchResult, error) {
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
	case gen.SearchResultKindTransaction, gen.SearchResultKindUserOperation:
		hash, err := ethrpc.ParseHash(key)
		if err != nil {
			return gen.SearchResult{}, fmt.Errorf("database returned invalid transaction search key: %w", err)
		}
		key = strings.ToLower(hash.String())
	}
	result := gen.SearchResult{Kind: resultKind, Key: key, Label: label, Rank: int(rank)}
	if nameSource.Valid {
		source := gen.SearchResultNameSource(nameSource.String)
		if resultKind != gen.SearchResultKindAddress || !source.Valid() {
			return gen.SearchResult{}, errors.New("database returned invalid search name source")
		}
		result.NameSource = &source
	}
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
	return fmt.Errorf("%w: canonical index is empty", publicquery.ErrNotReady)
}
