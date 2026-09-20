package etherscan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/islishude/etherview/internal/catalog"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

const (
	maxHoldingCandidates = 1000
	holdingStateBatch    = 200
)

type holdingWindow struct {
	skip   int
	limit  int
	target int
}

type erc20HoldingCandidate struct {
	address  []byte
	checksum string
	name     string
	symbol   string
	divisor  string
}

type erc721HoldingCandidate struct {
	address  []byte
	checksum string
	tokenID  string
	name     string
	symbol   string
}

func parseHoldingWindow(values url.Values) (holdingWindow, error) {
	if values.Get("sort") != "" {
		return holdingWindow{}, invalidParameter("sort is not supported for holding actions")
	}
	page, err := parsePagination(values)
	if err != nil {
		return holdingWindow{}, err
	}
	if page.offset < 0 || page.offset > maxHoldingCandidates ||
		int64(page.limit) > int64(maxHoldingCandidates)-page.offset {
		return holdingWindow{}, ErrHoldingWindowUnavailable
	}
	return holdingWindow{
		skip: int(page.offset), limit: page.limit,
		target: int(page.offset) + page.limit,
	}, nil
}

func holdingWindowExceeded(values url.Values) bool {
	_, err := parseHoldingWindow(values)
	return errors.Is(err, ErrHoldingWindowUnavailable)
}

func (b *PostgresBackend) addressTokenHoldings(ctx context.Context, values url.Values) ([]addressTokenHolding, error) {
	window, err := parseHoldingWindow(values)
	if err != nil {
		return nil, err
	}
	if b.erc20State == nil {
		return nil, ErrStateUnavailable
	}
	_, ownerBytes, err := parseAddressParameter(values.Get("address"), "address")
	if err != nil {
		return nil, err
	}
	snapshot, candidates, hasMore, err := b.erc20HoldingCandidates(ctx, ownerBytes)
	if err != nil {
		return nil, err
	}
	items := make([]addressTokenHolding, 0, min(window.target, len(candidates)))
	for start := 0; start < len(candidates) && len(items) < window.target; start += holdingStateBatch {
		end := min(start+holdingStateBatch, len(candidates))
		stateCandidates := make([]catalog.ERC20BalanceCandidate, end-start)
		for index := start; index < end; index++ {
			stateCandidates[index-start] = catalog.ERC20BalanceCandidate{TokenAddress: candidates[index].checksum}
		}
		observations, stateErr := b.erc20State.ERC20Balances(ctx, snapshot, values.Get("address"), stateCandidates)
		if stateErr != nil {
			return nil, ErrStateUnavailable
		}
		if len(observations) != len(stateCandidates) {
			return nil, errors.New("ERC-20 holding state result length is invalid")
		}
		for index, observation := range observations {
			balance, parseErr := storedUint256(observation.Balance, "ERC-20 holding balance")
			if parseErr != nil || observation.Confidence != catalog.NFTStateConfidenceRPCExact {
				return nil, errors.New("ERC-20 holding state result is invalid")
			}
			if balance.Sign() == 0 {
				continue
			}
			candidate := candidates[start+index]
			items = append(items, addressTokenHolding{
				TokenAddress: candidate.checksum, TokenName: candidate.name,
				TokenSymbol: candidate.symbol, TokenQuantity: balance.String(),
				TokenDivisor: candidate.divisor,
			})
			if len(items) == window.target {
				break
			}
		}
	}
	if len(items) < window.target && hasMore {
		return nil, ErrHoldingWindowUnavailable
	}
	return holdingPage(items, window)
}

func (b *PostgresBackend) addressNFTInventory(ctx context.Context, values url.Values) ([]addressNFTInventoryItem, error) {
	window, err := parseHoldingWindow(values)
	if err != nil {
		return nil, err
	}
	if b.nftState == nil {
		return nil, ErrStateUnavailable
	}
	_, ownerBytes, err := parseAddressParameter(values.Get("address"), "address")
	if err != nil {
		return nil, err
	}
	_, contractBytes, err := parseAddressParameter(values.Get("contractaddress"), "contractaddress")
	if err != nil {
		return nil, err
	}
	snapshot, candidates, hasMore, _, err := b.erc721HoldingCandidates(ctx, ownerBytes, contractBytes)
	if err != nil {
		return nil, err
	}
	items := make([]addressNFTInventoryItem, 0, min(window.target, len(candidates)))
	for start := 0; start < len(candidates) && len(items) < window.target; start += holdingStateBatch {
		end := min(start+holdingStateBatch, len(candidates))
		observations, stateErr := b.reconcileERC721Batch(ctx, snapshot, values.Get("address"), candidates[start:end])
		if stateErr != nil {
			return nil, stateErr
		}
		for index, observation := range observations {
			if observation.Balance == "0" {
				continue
			}
			candidate := candidates[start+index]
			items = append(items, addressNFTInventoryItem{TokenAddress: candidate.checksum, TokenID: candidate.tokenID})
			if len(items) == window.target {
				break
			}
		}
	}
	if len(items) < window.target && hasMore {
		return nil, ErrHoldingWindowUnavailable
	}
	return holdingPage(items, window)
}

func (b *PostgresBackend) addressNFTHoldings(ctx context.Context, values url.Values) ([]addressNFTHolding, error) {
	window, err := parseHoldingWindow(values)
	if err != nil {
		return nil, err
	}
	if b.nftState == nil {
		return nil, ErrStateUnavailable
	}
	_, ownerBytes, err := parseAddressParameter(values.Get("address"), "address")
	if err != nil {
		return nil, err
	}
	snapshot, candidates, hasMore, nextAddress, err := b.erc721HoldingCandidates(ctx, ownerBytes, nil)
	if err != nil {
		return nil, err
	}
	positive := make([]bool, len(candidates))
	for start := 0; start < len(candidates); start += holdingStateBatch {
		end := min(start+holdingStateBatch, len(candidates))
		observations, stateErr := b.reconcileERC721Batch(ctx, snapshot, values.Get("address"), candidates[start:end])
		if stateErr != nil {
			return nil, stateErr
		}
		for index, observation := range observations {
			positive[start+index] = observation.Balance == "1"
		}
	}

	items := make([]addressNFTHolding, 0)
	for index := 0; index < len(candidates); {
		end := index + 1
		for end < len(candidates) && bytes.Equal(candidates[end].address, candidates[index].address) {
			end++
		}
		quantity := 0
		for item := index; item < end; item++ {
			if positive[item] {
				quantity++
			}
		}
		if quantity > 0 {
			candidate := candidates[index]
			items = append(items, addressNFTHolding{
				TokenAddress: candidate.checksum, TokenName: candidate.name,
				TokenSymbol: candidate.symbol, TokenQuantity: strconv.Itoa(quantity),
			})
		}
		index = end
	}
	if hasMore && len(candidates) > 0 && bytes.Equal(candidates[len(candidates)-1].address, nextAddress) {
		lastAddress := candidates[len(candidates)-1].checksum
		if len(items) > 0 && items[len(items)-1].TokenAddress == lastAddress {
			items = items[:len(items)-1]
		}
	}
	if len(items) < window.target && hasMore {
		return nil, ErrHoldingWindowUnavailable
	}
	return holdingPage(items, window)
}

func holdingPage[T any](items []T, window holdingWindow) ([]T, error) {
	if window.skip >= len(items) {
		return nil, ErrNotFound
	}
	end := min(window.target, len(items))
	return append([]T(nil), items[window.skip:end]...), nil
}

func (b *PostgresBackend) erc20HoldingCandidates(
	ctx context.Context,
	owner []byte,
) (catalog.Snapshot, []erc20HoldingCandidate, bool, error) {
	tx, snapshot, err := b.beginHoldingSnapshot(ctx)
	if err != nil {
		return catalog.Snapshot{}, nil, false, err
	}
	defer dbaccess.Rollback(ctx, tx)
	rows, err := func() ([]dbgen.EtherscanERC20HoldingCandidatesRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(b.chain); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(snapshot.BlockNumber); err != nil {
			return nil, err
		}
		if maxHoldingCandidates+1 < -2147483648 || maxHoldingCandidates+1 > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).EtherscanERC20HoldingCandidates(ctx, dbgen.EtherscanERC20HoldingCandidatesParams{ChainID: queryValue0, MaxBlockNumber: queryValue1, OwnerAddress: owner, Limit: int32(maxHoldingCandidates + 1)})
	}()
	if err != nil {
		return catalog.Snapshot{}, nil, false, fmt.Errorf("query ERC-20 holding candidates: %w", err)
	}

	candidates := make([]erc20HoldingCandidate, 0, maxHoldingCandidates+1)
	for _, storedRow := range rows {
		var candidate erc20HoldingCandidate
		var name, symbol pgtype.Text
		var decimals pgtype.Int8
		{
			candidate.address = storedRow.TokenAddress
			var queryValue1 pgtype.Text
			if storedRow.Name != nil {
				queryValue1 = pgtype.Text{String: *storedRow.Name, Valid: true}
			}
			name = queryValue1
			var queryValue3 pgtype.Text
			if storedRow.Symbol != nil {
				queryValue3 = pgtype.Text{String: *storedRow.Symbol, Valid: true}
			}
			symbol = queryValue3
			var queryValue5 pgtype.Int8
			if storedRow.Decimals != nil {
				queryValue5 = pgtype.Int8{Int64: int64(*storedRow.Decimals), Valid: true}
			}
			decimals = queryValue5
		}
		address, err := addressFromBytes(candidate.address)
		if err != nil {
			return catalog.Snapshot{}, nil, false, err
		}
		candidate.checksum, err = checksumAddress(address)
		if err != nil {
			return catalog.Snapshot{}, nil, false, err
		}
		if name.Valid {
			candidate.name = name.String
		}
		if symbol.Valid {
			candidate.symbol = symbol.String
		}
		if len(candidate.name) > 1<<20 || len(candidate.symbol) > 1<<20 {
			return catalog.Snapshot{}, nil, false, errors.New("stored token metadata is too large")
		}
		if decimals.Valid {
			if decimals.Int64 < 0 || decimals.Int64 > 255 {
				return catalog.Snapshot{}, nil, false, errors.New("stored token decimals are invalid")
			}
			candidate.divisor = strconv.FormatInt(decimals.Int64, 10)
		}
		candidates = append(candidates, candidate)
	}

	hasMore := len(candidates) > maxHoldingCandidates
	if hasMore {
		candidates = candidates[:maxHoldingCandidates]
	}
	if err := tx.Commit(ctx); err != nil {
		return catalog.Snapshot{}, nil, false, fmt.Errorf("commit ERC-20 holding candidate snapshot: %w", err)
	}
	return snapshot, candidates, hasMore, nil
}

func (b *PostgresBackend) erc721HoldingCandidates(
	ctx context.Context,
	owner, contract []byte,
) (catalog.Snapshot, []erc721HoldingCandidate, bool, []byte, error) {
	tx, snapshot, err := b.beginHoldingSnapshot(ctx)
	if err != nil {
		return catalog.Snapshot{}, nil, false, nil, err
	}
	defer dbaccess.Rollback(ctx, tx)
	var contractArgument []byte
	if contract != nil {
		contractArgument = contract
	}
	rows, err := func() ([]dbgen.EtherscanERC721HoldingCandidatesRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(b.chain); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(snapshot.BlockNumber); err != nil {
			return nil, err
		}
		if maxHoldingCandidates+1 < -2147483648 || maxHoldingCandidates+1 > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).EtherscanERC721HoldingCandidates(ctx, dbgen.EtherscanERC721HoldingCandidatesParams{ChainID: queryValue0, MaxBlockNumber: queryValue1, OwnerAddress: owner, TokenAddress: contractArgument, Limit: int32(maxHoldingCandidates + 1)})
	}()
	if err != nil {
		return catalog.Snapshot{}, nil, false, nil, fmt.Errorf("query ERC-721 holding candidates: %w", err)
	}

	candidates := make([]erc721HoldingCandidate, 0, maxHoldingCandidates+1)
	for _, storedRow := range rows {
		var candidate erc721HoldingCandidate
		var name, symbol pgtype.Text
		{
			candidate.address = storedRow.TokenAddress
			candidate.tokenID = storedRow.CandidatesTokenID
			var queryValue2 pgtype.Text
			if storedRow.Name != nil {
				queryValue2 = pgtype.Text{String: *storedRow.Name, Valid: true}
			}
			name = queryValue2
			var queryValue4 pgtype.Text
			if storedRow.Symbol != nil {
				queryValue4 = pgtype.Text{String: *storedRow.Symbol, Valid: true}
			}
			symbol = queryValue4
		}
		if _, err := storedUint256(candidate.tokenID, "ERC-721 token ID"); err != nil {
			return catalog.Snapshot{}, nil, false, nil, err
		}
		address, err := addressFromBytes(candidate.address)
		if err != nil {
			return catalog.Snapshot{}, nil, false, nil, err
		}
		candidate.checksum, err = checksumAddress(address)
		if err != nil {
			return catalog.Snapshot{}, nil, false, nil, err
		}
		if name.Valid {
			candidate.name = name.String
		}
		if symbol.Valid {
			candidate.symbol = symbol.String
		}
		if len(candidate.name) > 1<<20 || len(candidate.symbol) > 1<<20 {
			return catalog.Snapshot{}, nil, false, nil, errors.New("stored token metadata is too large")
		}
		candidates = append(candidates, candidate)
	}

	hasMore := len(candidates) > maxHoldingCandidates
	var nextAddress []byte
	if hasMore {
		nextAddress = append([]byte(nil), candidates[maxHoldingCandidates].address...)
		candidates = candidates[:maxHoldingCandidates]
	}
	if err := tx.Commit(ctx); err != nil {
		return catalog.Snapshot{}, nil, false, nil, fmt.Errorf("commit ERC-721 holding candidate snapshot: %w", err)
	}
	return snapshot, candidates, hasMore, nextAddress, nil
}

func (b *PostgresBackend) beginHoldingSnapshot(ctx context.Context) (pgx.Tx, catalog.Snapshot, error) {
	tx, err := b.beginEnrichmentSnapshot(ctx)
	if err != nil {
		return nil, catalog.Snapshot{}, err
	}
	tip, err := b.requireCanonicalStageRange(ctx, tx, tokenStage, "0", nil, ErrTokenUnavailable)
	if err != nil {
		dbaccess.Rollback(ctx, tx) //nolint:errcheck
		return nil, catalog.Snapshot{}, err
	}
	var number string
	var hashBytes []byte
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(b.chain); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).EtherscanCanonicalSnapshot(ctx, queryValue0)
		if err != nil {
			return err
		}
		number = queryRow.Number
		hashBytes = queryRow.BlockHash
		return nil
	}(); err != nil {
		dbaccess.Rollback(ctx, tx) //nolint:errcheck
		return nil, catalog.Snapshot{}, fmt.Errorf("read holding snapshot: %w", err)
	}
	if number != tip {
		dbaccess.Rollback(ctx, tx) //nolint:errcheck
		return nil, catalog.Snapshot{}, errors.New("holding snapshot and coverage tips differ")
	}
	hash, err := hashFromBytes(hashBytes)
	if err != nil {
		dbaccess.Rollback(ctx, tx) //nolint:errcheck
		return nil, catalog.Snapshot{}, err
	}
	return tx, catalog.Snapshot{
		ChainID: b.chain, BlockNumber: number, BlockHash: hash.Hex(),
	}, nil
}

func (b *PostgresBackend) reconcileERC721Batch(
	ctx context.Context,
	snapshot catalog.Snapshot,
	owner string,
	candidates []erc721HoldingCandidate,
) ([]catalog.NFTBalanceObservation, error) {
	stateCandidates := make([]catalog.NFTBalanceCandidate, len(candidates))
	for index, candidate := range candidates {
		stateCandidates[index] = catalog.NFTBalanceCandidate{
			Standard: "erc721", TokenAddress: candidate.checksum, TokenID: candidate.tokenID,
		}
	}
	observations, err := b.nftState.Balances(ctx, snapshot, owner, stateCandidates)
	if err != nil {
		return nil, ErrStateUnavailable
	}
	if len(observations) != len(candidates) {
		return nil, errors.New("ERC-721 holding state result length is invalid")
	}
	for _, observation := range observations {
		if (observation.Balance != "0" && observation.Balance != "1") ||
			observation.Confidence != catalog.NFTStateConfidenceRPCExact {
			return nil, errors.New("ERC-721 holding state result is invalid")
		}
	}
	return observations, nil
}
