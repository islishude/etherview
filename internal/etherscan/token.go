package etherscan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common/hexutil"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func (b *PostgresBackend) accountTokenTransfers(ctx context.Context, action string, values url.Values) ([]tokenTransfer, error) {
	selector, err := tokenTransferSelector(values)
	if err != nil {
		return nil, invalidParameter("%v", err)
	}
	standard := map[string]string{
		"tokentx": "erc20", "tokennfttx": "erc721", "token1155tx": "erc1155",
	}[action]
	if standard == "" {
		return nil, invalidParameter("unsupported token transfer action %q", action)
	}
	contractArgument, err := optionalAddressBytes(values.Get("contractaddress"), "contractaddress")
	if err != nil {
		return nil, err
	}
	page, err := parsePagination(values)
	if err != nil {
		return nil, err
	}
	start, end, err := decimalRange(values)
	if err != nil {
		return nil, err
	}
	tx, err := b.beginEnrichmentSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer dbaccess.Rollback(ctx, tx)
	tip, err := b.requireCanonicalStageRange(ctx, tx, tokenStage, start, end, ErrTokenUnavailable)
	if err != nil {
		return nil, err
	}
	queries := dbgen.New(b.db).WithTx(tx)
	var rows []dbgen.EtherscanTokenTransfersRow
	if selector.mode == selectorLegacyAddress {
		_, address, parseErr := parseAddressParameter(values.Get("address"), "address")
		if parseErr != nil {
			return nil, parseErr
		}
		rows, err = queries.EtherscanTokenTransfers(ctx, dbgen.EtherscanTokenTransfersParams{ChainID: b.chain, Address: address, Standard: standard, FromBlock: start, ToBlock: end, ContractAddress: contractArgument, Limit: int64(page.limit), Offset: page.offset, Direction: page.direction})
	} else {
		from, parseErr := optionalAddressBytes(values.Get("from"), "from")
		if parseErr != nil {
			return nil, parseErr
		}
		to, parseErr := optionalAddressBytes(values.Get("to"), "to")
		if parseErr != nil {
			return nil, parseErr
		}
		operator := strings.ToUpper(selector.op)
		if operator == "" {
			operator = "AND"
		}
		var advanced []dbgen.EtherscanTokenTransfersAdvancedRow
		advanced, err = queries.EtherscanTokenTransfersAdvanced(ctx, dbgen.EtherscanTokenTransfersAdvancedParams{ChainID: b.chain, Standard: standard, ContractAddress: contractArgument, FromAddress: from, ToAddress: to, Operator: operator, FromBlock: start, ToBlock: end, Limit: int64(page.limit), Offset: page.offset, Direction: page.direction})
		rows = make([]dbgen.EtherscanTokenTransfersRow, len(advanced))
		for index, row := range advanced {
			rows[index] = dbgen.EtherscanTokenTransfersRow(row)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("query %s token transfers: %w", standard, err)
	}

	result := make([]tokenTransfer, 0, page.limit)
	for _, storedRow := range rows {
		item, scanErr := scanTokenTransfer(storedRow, standard, tip)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}

	if len(result) == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit %s token transfer snapshot: %w", standard, err)
	}
	return result, nil
}

func scanTokenTransfer(scanner dbgen.EtherscanTokenTransfersRow, expectedStandard, tipText string) (tokenTransfer, error) {
	var (
		blockNumberText, standard, eventKind  string
		blockTimestampText                    string
		blockHashBytes, transactionHashBytes  []byte
		tokenAddressBytes, fromBytes, toBytes []byte
		transactionJSON, receiptJSON          []byte
		logIndex, subIndex, transactionIndex  int64
		tokenID, amount, name, symbol         pgtype.Text
		blockBaseFeeText                      pgtype.Text
		decimals                              pgtype.Int8
	)
	if err := func() error {
		blockNumberText = scanner.BlockNumber
		blockHashBytes = scanner.BlockHash
		logIndex = scanner.LogIndex
		subIndex = int64(scanner.SubIndex)
		transactionHashBytes = scanner.TransactionHash
		tokenAddressBytes = scanner.TokenAddress
		standard = scanner.Standard
		eventKind = scanner.EventKind
		fromBytes = scanner.FromAddress
		toBytes = scanner.ToAddress
		queryValue10, err := dbaccess.NumericText(scanner.TokenID)
		if err != nil {
			return err
		}
		tokenID = queryValue10
		queryValue12, err := dbaccess.NumericText(scanner.Amount)
		if err != nil {
			return err
		}
		amount = queryValue12
		transactionJSON = scanner.TransactionRaw
		receiptJSON = scanner.ReceiptRaw
		blockTimestampText = scanner.BlockTimestamp
		var queryValue17 pgtype.Text
		if scanner.BlockBaseFee != nil {
			queryValue17 = pgtype.Text{String: *scanner.BlockBaseFee, Valid: true}
		}
		blockBaseFeeText = queryValue17
		transactionIndex = scanner.TransactionIndex
		var queryValue20 pgtype.Text
		if scanner.Name != nil {
			queryValue20 = pgtype.Text{String: *scanner.Name, Valid: true}
		}
		name = queryValue20
		var queryValue22 pgtype.Text
		if scanner.Symbol != nil {
			queryValue22 = pgtype.Text{String: *scanner.Symbol, Valid: true}
		}
		symbol = queryValue22
		var queryValue24 pgtype.Int8
		if scanner.Decimals != nil {
			queryValue24 = pgtype.Int8{Int64: int64(*scanner.Decimals), Valid: true}
		}
		decimals = queryValue24
		return nil
	}(); err != nil {
		return tokenTransfer{}, fmt.Errorf("scan token transfer: %w", err)
	}
	if standard != expectedStandard {
		return tokenTransfer{}, errors.New("stored token transfer standard does not match query")
	}
	if eventKind != "transfer" && eventKind != "mint" && eventKind != "burn" {
		return tokenTransfer{}, errors.New("stored token transfer event kind is invalid")
	}
	if logIndex < 0 || subIndex < 0 || transactionIndex < 0 {
		return tokenTransfer{}, errors.New("stored token transfer index is negative")
	}
	blockNumber, err := storedUint256(blockNumberText, "token transfer block number")
	if err != nil {
		return tokenTransfer{}, err
	}
	tip, err := storedUint256(tipText, "canonical tip")
	if err != nil || tip.Cmp(blockNumber) < 0 {
		return tokenTransfer{}, errors.New("stored token transfer canonical tip is invalid")
	}
	blockHash, err := hashFromBytes(blockHashBytes)
	if err != nil {
		return tokenTransfer{}, err
	}
	transactionHash, err := hashFromBytes(transactionHashBytes)
	if err != nil {
		return tokenTransfer{}, err
	}
	tokenAddress, err := addressFromBytes(tokenAddressBytes)
	if err != nil {
		return tokenTransfer{}, err
	}

	transaction, _, err := decodeStoredTransaction(transactionJSON, blockHash, blockNumber, transactionIndex)
	if err != nil {
		return tokenTransfer{}, fmt.Errorf("decode token transaction raw JSON: %w", err)
	}
	if transaction.Hash() != transactionHash {
		return tokenTransfer{}, errors.New("stored token transaction raw identity does not match event")
	}
	block, err := decodeStoredBlockContext(blockTimestampText, blockBaseFeeText)
	if err != nil {
		return tokenTransfer{}, err
	}

	receipt, err := decodeStoredReceiptWithBlockContext(
		receiptJSON,
		transaction,
		blockHash,
		blockNumber,
		transactionIndex,
		block.BaseFee,
	)
	if err != nil {
		return tokenTransfer{}, fmt.Errorf("decode token receipt raw JSON: %w", err)
	}

	item := tokenTransfer{
		BlockNumber: blockNumber.String(), Hash: strings.ToLower(transactionHash.Hex()),
		BlockHash: strings.ToLower(blockHash.Hex()), TransactionIndex: strconv.FormatInt(transactionIndex, 10),
		Input: "deprecated", FunctionName: "",
	}
	item.ContractAddress, err = checksumAddress(tokenAddress)
	if err != nil {
		return tokenTransfer{}, fmt.Errorf("checksum token contract: %w", err)
	}
	if name.Valid {
		if len(name.String) > 1<<20 {
			return tokenTransfer{}, errors.New("stored token name is too large")
		}
		item.TokenName = name.String
	}
	if symbol.Valid {
		if len(symbol.String) > 1<<20 {
			return tokenTransfer{}, errors.New("stored token symbol is too large")
		}
		item.TokenSymbol = symbol.String
	}
	if decimals.Valid {
		if decimals.Int64 < 0 || decimals.Int64 > 255 {
			return tokenTransfer{}, errors.New("stored token decimals are invalid")
		}
		item.TokenDecimal = strconv.FormatInt(decimals.Int64, 10)
	}
	if standard == "erc721" && !decimals.Valid {
		item.TokenDecimal = "0"
	}
	if item.From, err = optionalChecksumAddress(fromBytes); err != nil {
		return tokenTransfer{}, fmt.Errorf("checksum token transfer sender: %w", err)
	}
	if item.To, err = optionalChecksumAddress(toBytes); err != nil {
		return tokenTransfer{}, fmt.Errorf("checksum token transfer recipient: %w", err)
	}
	item.TimeStamp = decimalUint64(block.Timestamp)
	item.Nonce = decimalUint64(transaction.Nonce())
	item.Gas = decimalUint64(transaction.Gas())
	gasPrice, err := effectiveGasPrice(transaction, receipt)
	if err != nil {
		return tokenTransfer{}, err
	}
	if item.GasPrice, err = decimalBig(gasPrice); err != nil {
		return tokenTransfer{}, fmt.Errorf("decode token transaction gas price: %w", err)
	}
	item.CumulativeGasUsed = decimalUint64(receipt.CumulativeGasUsed)
	item.GasUsed = decimalUint64(receipt.GasUsed)
	input := hexutil.Encode(transaction.Data())
	if len(input) >= 10 {
		item.MethodID = strings.ToLower(input[:10])
	}
	confirmations := new(big.Int).Sub(tip, blockNumber)
	confirmations.Add(confirmations, big.NewInt(1))
	item.Confirmations = confirmations.String()

	if tokenID.Valid {
		parsed, parseErr := storedUint256(tokenID.String, "token ID")
		if parseErr != nil {
			return tokenTransfer{}, parseErr
		}
		item.TokenID = parsed.String()
	}
	if amount.Valid {
		parsed, parseErr := storedUint256(amount.String, "token transfer amount")
		if parseErr != nil {
			return tokenTransfer{}, parseErr
		}
		switch standard {
		case "erc20":
			item.Value = parsed.String()
		case "erc1155":
			item.TokenValue = parsed.String()
		}
	}
	switch standard {
	case "erc20":
		if item.Value == "" || item.TokenID != "" {
			return tokenTransfer{}, errors.New("stored ERC-20 transfer shape is invalid")
		}
	case "erc721":
		if item.TokenID == "" {
			return tokenTransfer{}, errors.New("stored ERC-721 transfer has no token ID")
		}
	case "erc1155":
		if item.TokenID == "" || item.TokenValue == "" {
			return tokenTransfer{}, errors.New("stored ERC-1155 transfer shape is invalid")
		}
	}
	return item, nil
}

type storedTokenContract struct {
	address, codeHash, observedHash []byte
	standard, confidence            string
	name, symbol, totalSupply       pgtype.Text
	decimals                        pgtype.Int8
	metadataState, observedBlock    string
}

func (b *PostgresBackend) canonicalTokenContract(
	ctx context.Context,
	queryer enrichmentQueryer,
	addressBytes []byte,
) (storedTokenContract, error) {
	var token storedTokenContract
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(b.chain); err != nil {
			return err
		}
		queryRow, err := dbgen.New(queryer).EtherscanCanonicalTokenContract(ctx, queryValue0, addressBytes)
		if err != nil {
			return err
		}
		token.address = queryRow.Address
		token.codeHash = queryRow.CodeHash
		token.standard = queryRow.Standard
		token.confidence = queryRow.Confidence
		var resultValue4 pgtype.Text
		if queryRow.Name != nil {
			resultValue4 = pgtype.Text{String: *queryRow.Name, Valid: true}
		}
		token.name = resultValue4
		var resultValue6 pgtype.Text
		if queryRow.Symbol != nil {
			resultValue6 = pgtype.Text{String: *queryRow.Symbol, Valid: true}
		}
		token.symbol = resultValue6
		var resultValue8 pgtype.Int8
		if queryRow.Decimals != nil {
			resultValue8 = pgtype.Int8{Int64: int64(*queryRow.Decimals), Valid: true}
		}
		token.decimals = resultValue8
		resultValue10, err := dbaccess.NumericText(queryRow.TotalSupply)
		if err != nil {
			return err
		}
		token.totalSupply = resultValue10
		token.metadataState = queryRow.MetadataState
		token.observedBlock = queryRow.TokenObservedBlockNumber
		token.observedHash = queryRow.ObservedBlockHash
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return storedTokenContract{}, ErrNotFound
	}
	if err != nil {
		return storedTokenContract{}, fmt.Errorf("query canonical token contract: %w", err)
	}
	if !bytes.Equal(token.address, addressBytes) || len(token.codeHash) != 32 || len(token.observedHash) != 32 {
		return storedTokenContract{}, errors.New("stored token contract identity is invalid")
	}
	if _, err := storedUint256(token.observedBlock, "token observation block"); err != nil {
		return storedTokenContract{}, err
	}
	switch token.standard {
	case "erc20", "erc721", "erc1155", "unknown":
	default:
		return storedTokenContract{}, errors.New("stored token standard is invalid")
	}
	switch token.confidence {
	case "verified", "high", "inferred", "guess":
	default:
		return storedTokenContract{}, errors.New("stored token confidence is invalid")
	}
	switch token.metadataState {
	case "pending", "complete", "unavailable", "failed":
	default:
		return storedTokenContract{}, errors.New("stored token metadata state is invalid")
	}
	if token.name.Valid && len(token.name.String) > 1<<20 || token.symbol.Valid && len(token.symbol.String) > 1<<20 {
		return storedTokenContract{}, errors.New("stored token metadata is too large")
	}
	if token.decimals.Valid && (token.decimals.Int64 < 0 || token.decimals.Int64 > 255) {
		return storedTokenContract{}, errors.New("stored token decimals are invalid")
	}
	if token.totalSupply.Valid {
		if _, err := storedUint256(token.totalSupply.String, "token total supply"); err != nil {
			return storedTokenContract{}, err
		}
	}
	return token, nil
}

func (b *PostgresBackend) tokenInformation(ctx context.Context, values url.Values) ([]tokenInfo, error) {
	address, addressBytes, err := parseAddressParameter(values.Get("contractaddress"), "contractaddress")
	if err != nil {
		return nil, err
	}
	tx, err := b.beginEnrichmentSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer dbaccess.Rollback(ctx, tx)
	if _, err := b.requireCanonicalStageRange(ctx, tx, tokenStage, "0", nil, ErrTokenUnavailable); err != nil {
		return nil, err
	}
	token, err := b.canonicalTokenContract(ctx, tx, addressBytes)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit token information snapshot: %w", err)
	}
	contractAddress, err := checksumAddress(address)
	if err != nil {
		return nil, fmt.Errorf("checksum token contract: %w", err)
	}
	item := tokenInfo{ContractAddress: contractAddress, TokenType: strings.ToUpper(token.standard)}
	if token.name.Valid {
		item.TokenName = token.name.String
	}
	if token.symbol.Valid {
		item.Symbol = token.symbol.String
	}
	if token.decimals.Valid {
		item.Divisor = strconv.FormatInt(token.decimals.Int64, 10)
	} else if token.standard == "erc721" {
		item.Divisor = "0"
	}
	if token.standard == "erc20" && b.state != nil {
		if supply, stateErr := b.state.ERC20TotalSupply(ctx, contractAddress); stateErr == nil {
			if _, parseErr := parseCanonicalDecimal(supply); parseErr != nil {
				return nil, ErrStateUnavailable
			}
			item.TotalSupply = supply
		}
	}
	return []tokenInfo{item}, nil
}

func (b *PostgresBackend) tokenSupply(ctx context.Context, values url.Values) (string, error) {
	address, _, err := parseAddressParameter(values.Get("contractaddress"), "contractaddress")
	if err != nil {
		return "", err
	}
	if b.state == nil {
		return "", ErrStateUnavailable
	}
	supply, err := b.state.ERC20TotalSupply(ctx, address.String())
	if err != nil {
		return "", ErrStateUnavailable
	}
	if _, err := parseCanonicalDecimal(supply); err != nil {
		return "", ErrStateUnavailable
	}
	return supply, nil
}

func (b *PostgresBackend) tokenBalance(ctx context.Context, values url.Values) (string, error) {
	contract, _, err := parseAddressParameter(values.Get("contractaddress"), "contractaddress")
	if err != nil {
		return "", err
	}
	owner, _, err := parseAddressParameter(values.Get("address"), "address")
	if err != nil {
		return "", err
	}
	if tag := strings.TrimSpace(values.Get("tag")); tag != "" && tag != "latest" {
		return "", invalidParameter("tag must be latest")
	}
	if b.state == nil {
		return "", ErrStateUnavailable
	}
	balance, err := b.state.ERC20Balance(ctx, contract.String(), owner.String())
	if err != nil {
		return "", ErrStateUnavailable
	}
	if _, err := parseCanonicalDecimal(balance); err != nil {
		return "", ErrStateUnavailable
	}
	return balance, nil
}

func (b *PostgresBackend) tokenHolders(ctx context.Context, values url.Values) ([]tokenHolder, error) {
	_, tokenAddress, err := parseAddressParameter(values.Get("contractaddress"), "contractaddress")
	if err != nil {
		return nil, err
	}
	page, err := parsePagination(values)
	if err != nil {
		return nil, err
	}
	if sortValue := strings.TrimSpace(values.Get("sort")); sortValue != "" && !strings.EqualFold(sortValue, "asc") {
		return nil, invalidParameter("sort must be asc for tokenholderlist")
	}
	tx, err := b.beginEnrichmentSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer dbaccess.Rollback(ctx, tx)
	tip, err := b.requireCanonicalStageRange(ctx, tx, holderStage, "0", nil, ErrStateUnavailable)
	if err != nil {
		return nil, err
	}
	if err := requireEtherscanHolderDependencies(ctx, tx, b.chain, tip); err != nil {
		return nil, err
	}
	token, err := b.canonicalTokenContract(ctx, tx, tokenAddress)
	if err != nil {
		return nil, err
	}
	if token.standard != "erc20" || token.confidence != "high" && token.confidence != "verified" {
		return nil, ErrStateUnavailable
	}
	snapshot, err := etherscanHolderSnapshot(ctx, tx, b.chain, tokenAddress, tip)
	if err != nil {
		return nil, err
	}
	rows, err := func() ([]dbgen.EtherscanHolderPageRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(b.chain); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(snapshot.blockNumber); err != nil {
			return nil, err
		}
		return dbgen.New(tx).EtherscanHolderPage(ctx, dbgen.EtherscanHolderPageParams{RowOffset: page.offset, RowLimit: int64(page.limit), ChainID: queryValue0, TokenAddress: tokenAddress, BlockNumber: queryValue1})
	}()
	if err != nil {
		return nil, fmt.Errorf("query token holders: %w", err)
	}

	result := make([]tokenHolder, 0, page.limit)
	for _, storedRow := range rows {
		var holderBytes []byte
		var quantity string
		{
			holderBytes = storedRow.HolderAddress
			quantity = storedRow.LatestBalance
		}
		holderAddress, err := addressFromBytes(holderBytes)
		if err != nil {
			return nil, err
		}
		checksum, err := checksumAddress(holderAddress)
		if err != nil {
			return nil, err
		}
		if _, err := storedUint256(quantity, "token holder quantity"); err != nil {
			return nil, err
		}
		result = append(result, tokenHolder{TokenHolderAddress: checksum, TokenHolderQuantity: quantity})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit token holder snapshot: %w", err)
	}
	return result, nil
}

func (b *PostgresBackend) tokenHolderCount(ctx context.Context, values url.Values) (string, error) {
	_, tokenAddress, err := parseAddressParameter(values.Get("contractaddress"), "contractaddress")
	if err != nil {
		return "", err
	}
	tx, err := b.beginEnrichmentSnapshot(ctx)
	if err != nil {
		return "", err
	}
	defer dbaccess.Rollback(ctx, tx)
	tip, err := b.requireCanonicalStageRange(ctx, tx, holderStage, "0", nil, ErrStateUnavailable)
	if err != nil {
		return "", err
	}
	if err := requireEtherscanHolderDependencies(ctx, tx, b.chain, tip); err != nil {
		return "", err
	}
	token, err := b.canonicalTokenContract(ctx, tx, tokenAddress)
	if err != nil {
		return "", err
	}
	if token.standard != "erc20" || token.confidence != "high" && token.confidence != "verified" {
		return "", ErrStateUnavailable
	}
	snapshot, err := etherscanHolderSnapshot(ctx, tx, b.chain, tokenAddress, tip)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit token holder count snapshot: %w", err)
	}
	return snapshot.holderCount, nil
}

type etherscanHolderState struct {
	blockNumber, state, holderCount, totalSupply, balanceSum string
	blockHash                                                []byte
	coherent                                                 bool
}

func requireEtherscanHolderDependencies(
	ctx context.Context,
	queryer enrichmentQueryer,
	chainID string,
	tip string,
) error {
	var configuredStart, holderBlocks, tokenBlocks, proxyBlocks, epoch string
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(tip); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(queryer).CatalogHolderCoverage(ctx, queryValue0, queryValue1)
		if err != nil {
			return err
		}
		configuredStart = queryRow.ConfigurationConfiguredStart
		holderBlocks = queryRow.CoveredBlocks
		tokenBlocks = queryRow.TokenBlocks
		proxyBlocks = queryRow.ProxyBlocks
		epoch = queryRow.PublicationEpoch
		return nil
	}(); err != nil {
		return fmt.Errorf("check token holder dependencies: %w", err)
	}
	want, err := storedUint256(tip, "holder dependency tip")
	if err != nil {
		return err
	}
	want.Add(want, big.NewInt(1))
	if configuredStart != "0" || holderBlocks != want.String() || tokenBlocks != want.String() ||
		proxyBlocks != want.String() {
		return ErrStateUnavailable
	}
	if _, err := storedUint256(epoch, "holder publication epoch"); err != nil {
		return err
	}
	return nil
}

func etherscanHolderSnapshot(
	ctx context.Context,
	queryer enrichmentQueryer,
	chainID string,
	tokenAddress []byte,
	tip string,
) (etherscanHolderState, error) {
	var snapshot etherscanHolderState
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(tip); err != nil {
			return err
		}
		queryRow, err := dbgen.New(queryer).CatalogHolderTokenSnapshot(ctx, queryValue0, tokenAddress, queryValue1)
		if err != nil {
			return err
		}
		snapshot.blockNumber = queryRow.SnapshotBlockNumber
		snapshot.blockHash = queryRow.BlockHash
		snapshot.state = queryRow.State
		snapshot.holderCount = queryRow.SnapshotHolderCount
		snapshot.totalSupply = queryRow.SnapshotTotalSupply
		snapshot.balanceSum = queryRow.SnapshotReconciledBalanceSum
		snapshot.coherent = queryRow.Coherent
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return etherscanHolderState{}, ErrStateUnavailable
	}
	if err != nil {
		return etherscanHolderState{}, fmt.Errorf("query token holder snapshot: %w", err)
	}
	if snapshot.state != "complete" || !snapshot.coherent || len(snapshot.blockHash) != 32 || snapshot.totalSupply != snapshot.balanceSum {
		return etherscanHolderState{}, ErrStateUnavailable
	}
	for name, value := range map[string]string{
		"snapshot block": snapshot.blockNumber, "holder count": snapshot.holderCount,
		"total supply": snapshot.totalSupply, "balance sum": snapshot.balanceSum,
	} {
		if _, err := storedUint256(value, name); err != nil {
			return etherscanHolderState{}, err
		}
	}
	return snapshot, nil
}
