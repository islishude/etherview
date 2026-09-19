package query

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	dbgen "github.com/islishude/etherview/internal/db/gen"
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

func (r *PostgresReader) currentBlockCursor(ctx context.Context, tx pgx.Tx) (blockCursor, error) {
	var numberText string
	var hashBytes []byte
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).GetCurrentQueryTip(ctx, queryValue0)
		if err != nil {
			return err
		}
		numberText = queryRow.CanonicalNumber
		hashBytes = queryRow.BlockHash
		return nil
	}(); err != nil {
		if err == pgx.ErrNoRows {
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

func (r *PostgresReader) validateBlockCursor(ctx context.Context, tx pgx.Tx, cursor blockCursor) error {
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(cursor.SnapshotNumber, 10)); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(strconv.FormatUint(cursor.BeforeNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).ValidateBlockCursor(ctx, dbgen.ValidateBlockCursorParams{ChainID: queryValue0, SnapshotNumber: queryValue1, SnapshotHash: snapshotHash.Bytes(), BoundaryNumber: queryValue2, BoundaryHash: beforeHash.Bytes()})
		if err != nil {
			return err
		}
		if queryRow == nil {
			return errors.New("invalid stored query value")
		}
		valid = *queryRow
		return nil
	}(); err != nil {
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
	rows, err := func() ([]dbgen.QuerySearchHashRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return nil, err
		}
		if limit < -2147483648 || limit > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(queryer).QuerySearchHash(ctx, dbgen.QuerySearchHashParams{ChainID: queryValue0, Hash: hash.Bytes(), ValidFromGeneration: generation, Limit: int32(limit)})
	}()
	if err != nil {
		return nil, fmt.Errorf("search hash: %w", err)
	}

	results := make([]gen.SearchResult, 0, 2)
	for _, storedRow := range rows {
		var kind, key, label string
		var rank int64
		var canonical bool
		{
			kind = storedRow.Kind
			key = storedRow.Key
			label = storedRow.Label
			rank = storedRow.Rank
			canonical = storedRow.Canonical
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

	if r.userOperations && userOperations.available {
		var userOpHash, sender []byte
		err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(r.chainID); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(strconv.FormatUint(userOperations.end, 10)); err != nil {
				return err
			}
			queryRow, err := dbgen.New(queryer).ERC4337SearchUserOperation(ctx, dbgen.ERC4337SearchUserOperationParams{ChainID: queryValue0, ConfigurationDigest: r.userOperationDigest, UserOpHash: hash[:], SnapshotNumber: queryValue1})
			if err != nil {
				return err
			}
			userOpHash = queryRow.UserOpHash
			sender = queryRow.Sender
			return nil
		}()
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
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
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(height, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(queryer).QuerySearchBlockNumber(ctx, queryValue0, queryValue1, generation)
		if err != nil {
			return err
		}
		numberText = queryRow.CanonicalNumber
		hashBytes = queryRow.BlockHash
		if queryRow.ResultLabel == nil {
			return errors.New("invalid stored query value")
		}
		label = *queryRow.ResultLabel
		rank = queryRow.Rank
		return nil
	}()
	if err == pgx.ErrNoRows {
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
	rows, err := func() ([]dbgen.QuerySearchTextRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(snapshotNumber, 10)); err != nil {
			return nil, err
		}
		if limit < -2147483648 || limit > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(queryer).QuerySearchText(ctx, dbgen.QuerySearchTextParams{ChainID: queryValue0, SearchTerm: strings.ToLower(value), MaxBlockNumber: queryValue1, ValidFromGeneration: generation, HasCursor: hasBoundary, MaxRank: int64(afterRank), MinKind: afterKind, MinKey: afterKey, Limit: int32(limit), NameObservationID: resolvedNameObservationID})
	}()
	if err != nil {
		return nil, fmt.Errorf("search indexed names and labels: %w", err)
	}

	results := make([]gen.SearchResult, 0, limit)
	for _, storedRow := range rows {
		var kind, key, label string
		var rank int64
		var canonical pgtype.Bool
		var nameSource pgtype.Text
		if err := func() error {
			if storedRow.Kind == nil {
				return errors.New("invalid stored query value")
			}
			kind = *storedRow.Kind
			key = storedRow.Key
			if storedRow.Label == nil {
				return errors.New("invalid stored query value")
			}
			label = *storedRow.Label
			rank = storedRow.Rank
			canonical = pgtype.Bool{Bool: storedRow.Canonical, Valid: storedRow.CanonicalKnown}
			var queryValue7 pgtype.Text
			if storedRow.NameSource != nil {
				queryValue7 = pgtype.Text{String: *storedRow.NameSource, Valid: true}
			}
			nameSource = queryValue7
			return nil
		}(); err != nil {
			return nil, fmt.Errorf("scan indexed search result: %w", err)
		}
		result, err := normalizeSearchResult(kind, key, label, rank, canonical, nameSource)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}

	return results, nil
}

type searchQueryer = dbgen.DBTX

func (r *PostgresReader) validateSearchCursor(ctx context.Context, tx pgx.Tx, cursor searchCursor, query string) error {
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(cursor.SnapshotNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).ValidateSearchCursor(ctx, dbgen.ValidateSearchCursorParams{ChainID: queryValue0, Number: queryValue1, BlockHash: hash.Bytes(), MinGeneration: cursor.Generation})
		if err != nil {
			return err
		}
		if queryRow == nil {
			return errors.New("invalid stored query value")
		}
		valid = *queryRow
		return nil
	}(); err != nil {
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
		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(strconv.FormatUint(r.userOperationStart, 10)); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(strconv.FormatUint(cursor.UserOperationSnapshotEnd, 10)); err != nil {
				return err
			}
			var queryValue2 pgtype.Numeric
			if err := queryValue2.Scan(r.chainID); err != nil {
				return err
			}
			queryRow, err := dbgen.New(tx).ERC4337ValidateSnapshot(ctx, dbgen.ERC4337ValidateSnapshotParams{IndexStart: queryValue0, SnapshotNumber: queryValue1, SnapshotHash: userOperationHash[:], ChainID: queryValue2, ConfigurationDigest: r.userOperationDigest})
			if err != nil {
				return err
			}
			valid = queryRow
			return nil
		}(); err != nil {
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
	tx pgx.Tx,
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).ValidateResolvedSearchName(ctx, dbgen.ValidateResolvedSearchNameParams{ChainID: queryValue0, ID: gate.ObservationID, LookupKey: gate.Name, Source: gate.Source, ValidFromGeneration: generation, Address: address})
		if err != nil {
			return err
		}
		visible = queryRow
		return nil
	}(); err != nil {
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
	canonical pgtype.Bool,
	nameSource pgtype.Text,
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
