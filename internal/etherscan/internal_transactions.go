package etherscan

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"
)

func (b *PostgresBackend) internalTransactions(ctx context.Context, values url.Values) ([]internalTransaction, error) {
	selector, err := internalTransactionSelector(values)
	if err != nil {
		return nil, invalidParameter("%v", err)
	}
	var addressBytes []byte
	rawAddress := strings.TrimSpace(values.Get("address"))
	if rawAddress != "" {
		_, parsed, err := parseAddressParameter(rawAddress, "address")
		if err != nil {
			return nil, err
		}
		addressBytes = parsed
	}
	var transactionHashBytes []byte
	rawHash := strings.TrimSpace(values.Get("txhash"))
	if rawHash != "" {
		_, hashBytes, parseErr := parseHashParameter(rawHash, "txhash")
		if parseErr != nil {
			return nil, parseErr
		}
		transactionHashBytes = hashBytes
	}
	page, err := parsePagination(values)
	if err != nil {
		return nil, err
	}
	tx, err := b.beginEnrichmentSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer dbaccess.Rollback(ctx, tx)
	var start string
	var end *string
	if rawHash != "" {
		block, err := b.canonicalTransactionBlock(ctx, tx, transactionHashBytes)
		if errors.Is(err, ErrNotFound) {
			if _, coverageErr := b.requireCanonicalCoreRange(ctx, tx, "0", nil); coverageErr != nil {
				return nil, coverageErr
			}
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		start, end = block, &block
	} else {
		start, end, err = decimalRange(values)
		if err != nil {
			return nil, err
		}
	}
	if _, err := b.requireCanonicalStageRange(ctx, tx, traceStage, start, end, ErrTraceUnavailable); err != nil {
		return nil, err
	}
	queries := dbgen.New(b.db).WithTx(tx)
	var rows []dbgen.EtherscanInternalTransactionsRow
	if selector.mode == selectorDirectional {
		from, parseErr := optionalAddressBytes(values.Get("from"), "from")
		if parseErr != nil {
			return nil, parseErr
		}
		to, parseErr := optionalAddressBytes(values.Get("to"), "to")
		if parseErr != nil {
			return nil, parseErr
		}
		var advanced []dbgen.EtherscanInternalTransactionsAdvancedRow
		advanced, err = queries.EtherscanInternalTransactionsAdvanced(ctx, dbgen.EtherscanInternalTransactionsAdvancedParams{ChainID: b.chain, FromAddress: from, ToAddress: to, Operator: strings.ToUpper(selector.op), FromBlock: start, ToBlock: end, Limit: int64(page.limit), Offset: page.offset, Direction: page.direction})
		rows = make([]dbgen.EtherscanInternalTransactionsRow, len(advanced))
		for index, row := range advanced {
			rows[index] = dbgen.EtherscanInternalTransactionsRow(row)
		}
	} else {
		rows, err = queries.EtherscanInternalTransactions(ctx, dbgen.EtherscanInternalTransactionsParams{ChainID: b.chain, Address: addressBytes, TransactionHash: transactionHashBytes, FromBlock: start, ToBlock: end, Limit: int64(page.limit), Offset: page.offset, Direction: page.direction})
	}

	if err != nil {
		return nil, fmt.Errorf("query internal transactions: %w", err)
	}

	result := make([]internalTransaction, 0, page.limit)
	for _, storedRow := range rows {
		item, scanErr := scanInternalTransaction(storedRow)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}

	if len(result) == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit internal transaction snapshot: %w", err)
	}
	return result, nil
}

func (b *PostgresBackend) canonicalTransactionBlock(
	ctx context.Context,
	queryer enrichmentQueryer,
	hash []byte,
) (string, error) {
	var block string
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(b.chain); err != nil {
			return err
		}
		queryRow, err := dbgen.New(queryer).EtherscanCanonicalTransactionBlock(ctx, queryValue0, hash)
		if err != nil {
			return err
		}
		block = queryRow
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("query canonical transaction block: %w", err)
	}
	if _, err := storedUint256(block, "transaction block number"); err != nil {
		return "", err
	}
	return block, nil
}

func scanInternalTransaction(scanner dbgen.EtherscanInternalTransactionsRow) (internalTransaction, error) {
	var (
		blockNumber, timestamp, tracePath, callType string
		blockHash, transactionHash                  []byte
		from, to, created, input                    []byte
		depth                                       int64
		value, gas, gasUsed, traceError             pgtype.Text
		reverted                                    bool
	)
	if err := func() error {
		blockNumber = scanner.BlockNumber
		blockHash = scanner.BlockHash
		transactionHash = scanner.TransactionHash
		timestamp = scanner.BlockTimestamp
		tracePath = scanner.TracePath
		depth = int64(scanner.Depth)
		callType = scanner.CallType
		from = scanner.FromAddress
		to = scanner.ToAddress
		created = scanner.CreatedAddress
		queryValue10, err := dbaccess.NumericText(scanner.Value)
		if err != nil {
			return err
		}
		value = queryValue10
		queryValue12, err := dbaccess.NumericText(scanner.Gas)
		if err != nil {
			return err
		}
		gas = queryValue12
		queryValue14, err := dbaccess.NumericText(scanner.GasUsed)
		if err != nil {
			return err
		}
		gasUsed = queryValue14
		input = scanner.Input
		var queryValue17 pgtype.Text
		if scanner.Error != nil {
			queryValue17 = pgtype.Text{String: *scanner.Error, Valid: true}
		}
		traceError = queryValue17
		reverted = scanner.Reverted
		return nil
	}(); err != nil {
		return internalTransaction{}, fmt.Errorf("scan internal transaction: %w", err)
	}
	if _, err := storedUint256(blockNumber, "trace block number"); err != nil {
		return internalTransaction{}, err
	}
	if _, err := storedUint256(timestamp, "trace block timestamp"); err != nil {
		return internalTransaction{}, err
	}
	if depth <= 0 {
		return internalTransaction{}, errors.New("stored internal transaction depth is invalid")
	}
	components, err := validateTracePath(tracePath)
	if err != nil || int64(components) != depth {
		return internalTransaction{}, errors.New("stored internal transaction trace path is invalid")
	}
	if _, err := hashFromBytes(blockHash); err != nil {
		return internalTransaction{}, err
	}
	indexedTransactionHash, err := hashFromBytes(transactionHash)
	if err != nil {
		return internalTransaction{}, err
	}
	item := internalTransaction{
		BlockNumber: blockNumber,
		TimeStamp:   timestamp,
		Hash:        strings.ToLower(indexedTransactionHash.String()),
		Value:       "0",
		Gas:         "0",
		GasUsed:     "0",
		Input:       "",
		Type:        strings.ToLower(strings.TrimSpace(callType)),
		TraceID:     strings.ReplaceAll(tracePath, ".", "_"),
		IsError:     "0",
	}
	if item.Type == "" {
		return internalTransaction{}, errors.New("stored internal transaction call type is empty")
	}
	switch item.Type {
	case "call", "callcode", "delegatecall", "staticcall", "create", "create2", "selfdestruct", "reward":
	default:
		return internalTransaction{}, errors.New("stored internal transaction call type is invalid")
	}
	if item.From, err = optionalChecksumAddress(from); err != nil {
		return internalTransaction{}, fmt.Errorf("checksum internal transaction sender: %w", err)
	}
	if item.To, err = optionalChecksumAddress(to); err != nil {
		return internalTransaction{}, fmt.Errorf("checksum internal transaction recipient: %w", err)
	}
	if item.ContractAddress, err = optionalChecksumAddress(created); err != nil {
		return internalTransaction{}, fmt.Errorf("checksum internal transaction created contract: %w", err)
	}
	if item.To == "" && item.ContractAddress == "" && item.Type != "selfdestruct" && item.Type != "reward" {
		return internalTransaction{}, errors.New("stored internal transaction has no recipient")
	}
	for name, source := range map[string]pgtype.Text{
		"value": value, "gas": gas, "gas used": gasUsed,
	} {
		if !source.Valid {
			continue
		}
		parsed, parseErr := storedUint256(source.String, "trace "+name)
		if parseErr != nil {
			return internalTransaction{}, parseErr
		}
		switch name {
		case "value":
			item.Value = parsed.String()
		case "gas":
			item.Gas = parsed.String()
		case "gas used":
			item.GasUsed = parsed.String()
		}
	}
	if input != nil {
		item.Input = "0x" + hex.EncodeToString(input)
	}
	if traceError.Valid {
		if len(traceError.String) > 1<<20 {
			return internalTransaction{}, errors.New("stored internal transaction error is too large")
		}
		if !reverted {
			return internalTransaction{}, errors.New("stored internal transaction error is inconsistent")
		}
		item.ErrCode = traceError.String
	}
	if reverted {
		item.IsError = "1"
		if item.ErrCode == "" {
			item.ErrCode = "execution reverted"
		}
	}
	return item, nil
}

func optionalChecksumAddress(raw []byte) (string, error) {
	if raw == nil {
		return "", nil
	}
	address, err := addressFromBytes(raw)
	if err != nil {
		return "", err
	}
	return checksumAddress(address)
}

func validateTracePath(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	parts := strings.Split(value, ".")
	if len(parts) > 128 {
		return 0, errors.New("trace path is too deep")
	}
	for _, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return 0, errors.New("trace path is not canonical")
		}
		if _, err := strconv.ParseUint(part, 10, 32); err != nil {
			return 0, errors.New("trace path component is invalid")
		}
	}
	return len(parts), nil
}
