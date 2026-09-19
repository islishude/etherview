package etherscan

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func (b *PostgresBackend) accountTransactions(ctx context.Context, values url.Values) ([]accountTransaction, error) {
	selector, err := normalTransactionSelector(values)
	if err != nil {
		return nil, invalidParameter("%v", err)
	}
	page, err := parsePagination(values)
	if err != nil {
		return nil, err
	}
	start, end, err := decimalRange(values)
	if err != nil {
		return nil, err
	}
	tx, err := b.beginCanonicalSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer dbaccess.Rollback(ctx, tx)
	if _, err := b.requireCanonicalCoreRange(ctx, tx, start, end); err != nil {
		return nil, err
	}

	queries := dbgen.New(b.db).WithTx(tx)
	var rows []dbgen.EtherscanAccountTransactionsRow
	if selector.mode == selectorLegacyAddress {
		address, _, parseErr := parseAddressParameter(values.Get("address"), "address")
		if parseErr != nil {
			return nil, parseErr
		}
		rows, err = queries.EtherscanAccountTransactions(ctx, dbgen.EtherscanAccountTransactionsParams{ChainID: b.chain, Address: strings.ToLower(address.Hex()), FromBlock: start, ToBlock: end, Limit: int64(page.limit), Offset: page.offset, Direction: page.direction})
	} else {
		from, parseErr := optionalAddressText(values.Get("from"), "from")
		if parseErr != nil {
			return nil, parseErr
		}
		to, parseErr := optionalAddressText(values.Get("to"), "to")
		if parseErr != nil {
			return nil, parseErr
		}
		var advanced []dbgen.EtherscanAccountTransactionsAdvancedRow
		advanced, err = queries.EtherscanAccountTransactionsAdvanced(ctx, dbgen.EtherscanAccountTransactionsAdvancedParams{ChainID: b.chain, FromAddress: from, ToAddress: to, Operator: strings.ToUpper(selector.op), FromBlock: start, ToBlock: end, Limit: int64(page.limit), Offset: page.offset, Direction: page.direction})
		rows = make([]dbgen.EtherscanAccountTransactionsRow, len(advanced))
		for index, row := range advanced {
			rows[index] = dbgen.EtherscanAccountTransactionsRow(row)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("query account transactions: %w", err)
	}

	result := make([]accountTransaction, 0, page.limit)
	for _, storedRow := range rows {
		item, err := scanAccountTransaction(storedRow)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}

	if len(result) == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit account transaction snapshot: %w", err)
	}
	return result, nil
}

func scanAccountTransaction(scanner dbgen.EtherscanAccountTransactionsRow) (accountTransaction, error) {
	var transactionJSON, receiptJSON []byte
	var blockTimestampText, blockNumberText, tipNumberText string
	var blockBaseFeeText pgtype.Text
	var blockHashBytes, transactionHashBytes []byte
	var transactionIndex int64
	{
		transactionJSON = scanner.TransactionRaw
		receiptJSON = scanner.ReceiptRaw
		blockTimestampText = scanner.BlockTimestamp
		var queryValue3 pgtype.Text
		if scanner.BlockBaseFee != nil {
			queryValue3 = pgtype.Text{String: *scanner.BlockBaseFee, Valid: true}
		}
		blockBaseFeeText = queryValue3
		blockNumberText = scanner.BlockNumber
		blockHashBytes = scanner.BlockHash
		transactionIndex = scanner.TransactionIndex
		transactionHashBytes = scanner.TransactionHash
		tipNumberText = scanner.TipNumber
	}
	if transactionIndex < 0 {
		return accountTransaction{}, errors.New("stored transaction index is negative")
	}
	blockNumber, ok := new(big.Int).SetString(blockNumberText, 10)
	if !ok || blockNumber.Sign() < 0 {
		return accountTransaction{}, errors.New("stored block number is invalid")
	}
	tipNumber, ok := new(big.Int).SetString(tipNumberText, 10)
	if !ok || tipNumber.Cmp(blockNumber) < 0 {
		return accountTransaction{}, errors.New("stored canonical tip is invalid")
	}
	blockHash, err := hashFromBytes(blockHashBytes)
	if err != nil {
		return accountTransaction{}, err
	}
	transactionHash, err := hashFromBytes(transactionHashBytes)
	if err != nil {
		return accountTransaction{}, err
	}

	transaction, sender, err := decodeStoredTransaction(transactionJSON, blockHash, blockNumber, transactionIndex)
	if err != nil {
		return accountTransaction{}, fmt.Errorf("decode transaction raw JSON: %w", err)
	}
	if transaction.Hash() != transactionHash {
		return accountTransaction{}, errors.New("stored transaction raw identity does not match inclusion")
	}
	block, err := decodeStoredBlockContext(blockTimestampText, blockBaseFeeText)
	if err != nil {
		return accountTransaction{}, err
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
		return accountTransaction{}, fmt.Errorf("decode receipt raw JSON: %w", err)
	}

	from, err := checksumAddress(sender)
	if err != nil {
		return accountTransaction{}, fmt.Errorf("checksum transaction sender: %w", err)
	}
	to := ""
	if transaction.To() != nil {
		to, err = checksumAddress(*transaction.To())
		if err != nil {
			return accountTransaction{}, fmt.Errorf("checksum transaction recipient: %w", err)
		}
	}
	contractAddress := ""
	if receipt.ContractAddress != (common.Address{}) {
		contractAddress, err = checksumAddress(receipt.ContractAddress)
		if err != nil {
			return accountTransaction{}, fmt.Errorf("checksum created contract: %w", err)
		}
	}

	item := accountTransaction{
		BlockNumber: blockNumber.String(), Hash: strings.ToLower(transactionHash.Hex()),
		BlockHash: strings.ToLower(blockHash.Hex()), TransactionIndex: strconv.FormatInt(transactionIndex, 10),
		From: from, To: to, ContractAddress: contractAddress, Input: hexutil.Encode(transaction.Data()),
		FunctionName: "",
	}
	item.TimeStamp = decimalUint64(block.Timestamp)
	item.Nonce = decimalUint64(transaction.Nonce())
	if item.Value, err = decimalBig(transaction.Value()); err != nil {
		return accountTransaction{}, fmt.Errorf("decode transaction value: %w", err)
	}
	item.Gas = decimalUint64(transaction.Gas())
	gasPrice, err := effectiveGasPrice(transaction, receipt)
	if err != nil {
		return accountTransaction{}, err
	}
	if item.GasPrice, err = decimalBig(gasPrice); err != nil {
		return accountTransaction{}, fmt.Errorf("decode transaction gas price: %w", err)
	}
	item.CumulativeGasUsed = decimalUint64(receipt.CumulativeGasUsed)
	item.GasUsed = decimalUint64(receipt.GasUsed)
	if len(receipt.PostState) == 0 {
		switch receipt.Status {
		case 0:
			item.IsError, item.ReceiptStatus = "1", "0"
		case 1:
			item.IsError, item.ReceiptStatus = "0", "1"
		default:
			return accountTransaction{}, errors.New("stored receipt status is neither zero nor one")
		}
	}
	confirmations := new(big.Int).Sub(tipNumber, blockNumber)
	confirmations.Add(confirmations, big.NewInt(1))
	item.Confirmations = confirmations.String()
	if len(item.Input) >= 10 {
		item.MethodID = strings.ToLower(item.Input[:10])
	}
	return item, nil
}

func (b *PostgresBackend) minedBlocks(ctx context.Context, values url.Values) ([]minedBlock, error) {
	blockType := strings.ToLower(strings.TrimSpace(values.Get("blocktype")))
	if blockType == "uncles" {
		return nil, ErrUncleUnavailable
	}
	if blockType != "" && blockType != "blocks" {
		return nil, invalidParameter("blocktype must be blocks or uncles")
	}
	address, _, err := parseAddressParameter(values.Get("address"), "address")
	if err != nil {
		return nil, err
	}
	page, err := parsePagination(values)
	if err != nil {
		return nil, err
	}
	tx, err := b.beginCanonicalSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer dbaccess.Rollback(ctx, tx)
	if _, err := b.requireCanonicalCoreRange(ctx, tx, "0", nil); err != nil {
		return nil, err
	}
	queries := dbgen.New(b.db).WithTx(tx)
	params := dbgen.EtherscanMinedBlocksAscParams{ChainID: b.chain, Miner: strings.ToLower(address.Hex()), Limit: int64(page.limit), Offset: page.offset}
	var rows []dbgen.EtherscanMinedBlocksAscRow
	if page.direction == "DESC" {
		var descending []dbgen.EtherscanMinedBlocksDescRow
		descending, err = queries.EtherscanMinedBlocksDesc(ctx, dbgen.EtherscanMinedBlocksDescParams(params))
		rows = make([]dbgen.EtherscanMinedBlocksAscRow, len(descending))
		for index, row := range descending {
			rows[index] = dbgen.EtherscanMinedBlocksAscRow(row)
		}
	} else {
		rows, err = queries.EtherscanMinedBlocksAsc(ctx, params)
	}

	if err != nil {
		return nil, fmt.Errorf("query mined blocks: %w", err)
	}

	result := make([]minedBlock, 0, page.limit)
	for _, storedRow := range rows {
		numberText, timestampText, hashBytes := storedRow.BlockNumber, storedRow.BlockTimestamp, storedRow.BlockHash
		var minerText pgtype.Text
		if storedRow.Miner != nil {
			minerText = pgtype.Text{String: *storedRow.Miner, Valid: true}
		}

		number, ok := new(big.Int).SetString(numberText, 10)
		if !ok || number.Sign() < 0 {
			return nil, errors.New("stored block number is invalid")
		}
		if _, err := hashFromBytes(hashBytes); err != nil {
			return nil, err
		}
		block, err := decodeStoredBlockContext(timestampText, pgtype.Text{})
		if err != nil {
			return nil, err
		}
		miner, err := decodeStoredBlockMiner(minerText)
		if err != nil {
			return nil, err
		}
		if miner != address {
			return nil, errors.New("stored mined block raw identity does not match indexed row")
		}
		timestamp := decimalUint64(block.Timestamp)
		result = append(result, minedBlock{BlockNumber: number.String(), TimeStamp: timestamp})
	}

	if len(result) == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit mined block snapshot: %w", err)
	}
	return result, nil
}
