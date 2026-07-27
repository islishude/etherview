package etherscan

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

var topicOperatorPattern = regexp.MustCompile(`^topic([0-3])_([0-3])_opr$`)

func (b *PostgresBackend) logs(ctx context.Context, values url.Values) ([]logEntry, error) {
	page, err := parsePagination(values)
	if err != nil {
		return nil, err
	}
	arguments := []any{b.chain, "0"}
	if raw := strings.TrimSpace(values.Get("fromBlock")); raw != "" {
		value, err := parseDecimal(raw, "fromBlock")
		if err != nil {
			return nil, err
		}
		arguments[1] = value.String()
	}
	clauses := []string{"log.block_number >= $2::numeric"}
	var coverageEnd *string
	if raw := strings.TrimSpace(values.Get("toBlock")); raw != "" {
		value, err := parseDecimal(raw, "toBlock")
		if err != nil {
			return nil, err
		}
		if value.Cmp(mustBig(arguments[1].(string))) < 0 {
			return nil, invalidParameter("toBlock is less than fromBlock")
		}
		text := value.String()
		coverageEnd = &text
		arguments = append(arguments, text)
		clauses = append(clauses, fmt.Sprintf("log.block_number <= $%d::numeric", len(arguments)))
	}
	if raw := strings.TrimSpace(values.Get("address")); raw != "" {
		_, addressBytes, err := parseAddressParameter(raw, "address")
		if err != nil {
			return nil, err
		}
		arguments = append(arguments, addressBytes)
		clauses = append(clauses, fmt.Sprintf("log.address = $%d", len(arguments)))
	}

	topicClause, topicArguments, err := buildTopicFilter(values, len(arguments)+1)
	if err != nil {
		return nil, err
	}
	arguments = append(arguments, topicArguments...)
	if topicClause != "" {
		clauses = append(clauses, topicClause)
	}
	tx, err := b.beginCanonicalSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := b.requireCanonicalCoreRange(ctx, tx, arguments[1].(string), coverageEnd); err != nil {
		return nil, err
	}
	arguments = append(arguments, page.limit, page.offset)
	query := fmt.Sprintf(
		logsSQL, strings.Join(clauses, " AND "), page.direction, page.direction,
		page.direction, len(arguments)-1, len(arguments),
	)
	rows, err := tx.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	result := make([]logEntry, 0, page.limit)
	for rows.Next() {
		item, err := scanLogEntry(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate logs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close logs: %w", err)
	}
	if len(result) == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit log snapshot: %w", err)
	}
	return result, nil
}

func buildTopicFilter(values url.Values, firstPlaceholder int) (string, []any, error) {
	type topicFilter struct {
		index     int
		condition string
		argument  any
	}
	filters := make([]topicFilter, 0, 4)
	for key, items := range values {
		if !strings.HasPrefix(key, "topic") {
			continue
		}
		validFilter := len(key) == len("topic0") && key[len(key)-1] >= '0' && key[len(key)-1] <= '3'
		validOperator := topicOperatorPattern.MatchString(key)
		if !validFilter && !validOperator {
			return "", nil, invalidParameter("unsupported topic parameter %s", key)
		}
		if len(items) != 1 {
			return "", nil, invalidParameter("topic parameter %s must appear exactly once", key)
		}
	}
	for index := range 4 {
		name := fmt.Sprintf("topic%d", index)
		raw := strings.TrimSpace(values.Get(name))
		if raw == "" {
			continue
		}
		hash, bytes, err := parseHashParameter(raw, name)
		if err != nil {
			return "", nil, err
		}
		placeholder := firstPlaceholder + len(filters)
		condition := fmt.Sprintf("lower(log.raw->'topics'->>%d) = $%d", index, placeholder)
		argument := any(strings.ToLower(hash.Hex()))
		if index == 0 {
			condition = fmt.Sprintf("log.topic0 = $%d", placeholder)
			argument = bytes
		}
		filters = append(filters, topicFilter{index: index, condition: condition, argument: argument})
	}
	allowedOperators := make(map[string]struct{}, 3)
	for index := 1; index < len(filters); index++ {
		allowedOperators[fmt.Sprintf("topic%d_%d_opr", filters[index-1].index, filters[index].index)] = struct{}{}
	}
	for key, items := range values {
		if !strings.HasPrefix(key, "topic") || !strings.HasSuffix(key, "_opr") {
			continue
		}
		match := topicOperatorPattern.FindStringSubmatch(key)
		if match == nil || len(items) != 1 {
			return "", nil, invalidParameter("invalid topic operator %s", key)
		}
		left, _ := strconv.Atoi(match[1])
		right, _ := strconv.Atoi(match[2])
		if left >= right || strings.TrimSpace(values.Get(fmt.Sprintf("topic%d", left))) == "" || strings.TrimSpace(values.Get(fmt.Sprintf("topic%d", right))) == "" {
			return "", nil, invalidParameter("topic operator %s references missing or unordered filters", key)
		}
		if _, supported := allowedOperators[key]; !supported {
			return "", nil, invalidParameter("topic operator %s does not connect adjacent supplied filters", key)
		}
		operator := strings.ToLower(strings.TrimSpace(items[0]))
		if operator != "and" && operator != "or" {
			return "", nil, invalidParameter("topic operator %s must be and or or", key)
		}
	}
	if len(filters) == 0 {
		return "", nil, nil
	}
	expression := filters[0].condition
	arguments := []any{filters[0].argument}
	for index := 1; index < len(filters); index++ {
		left, right := filters[index-1].index, filters[index].index
		operator := strings.ToUpper(strings.TrimSpace(values.Get(fmt.Sprintf("topic%d_%d_opr", left, right))))
		if operator == "" {
			operator = "AND"
		}
		if operator != "AND" && operator != "OR" {
			return "", nil, invalidParameter("topic%d_%d_opr must be and or or", left, right)
		}
		expression = fmt.Sprintf("(%s %s %s)", expression, operator, filters[index].condition)
		arguments = append(arguments, filters[index].argument)
	}
	return expression, arguments, nil
}

func scanLogEntry(scanner rowScanner) (logEntry, error) {
	var logJSON, receiptJSON, transactionJSON, blockJSON []byte
	var blockNumberText string
	var blockHashBytes, transactionHashBytes, addressBytes []byte
	var logIndex, transactionIndex int64
	if err := scanner.Scan(
		&logJSON, &receiptJSON, &transactionJSON, &blockJSON, &blockNumberText,
		&blockHashBytes, &logIndex, &transactionIndex, &transactionHashBytes, &addressBytes,
	); err != nil {
		return logEntry{}, fmt.Errorf("scan log: %w", err)
	}
	if logIndex < 0 || transactionIndex < 0 {
		return logEntry{}, errors.New("stored log or transaction index is negative")
	}
	blockNumber, ok := new(big.Int).SetString(blockNumberText, 10)
	if !ok || blockNumber.Sign() < 0 {
		return logEntry{}, errors.New("stored log block number is invalid")
	}
	blockHash, err := hashFromBytes(blockHashBytes)
	if err != nil {
		return logEntry{}, err
	}
	transactionHash, err := hashFromBytes(transactionHashBytes)
	if err != nil {
		return logEntry{}, err
	}
	indexedAddress, err := addressFromBytes(addressBytes)
	if err != nil {
		return logEntry{}, err
	}

	wireLog, err := decodeStoredLog(
		logJSON, transactionHash, blockHash, blockNumber, transactionIndex, logIndex,
	)
	if err != nil {
		return logEntry{}, fmt.Errorf("decode log raw JSON: %w", err)
	}
	if wireLog.Address != indexedAddress {
		return logEntry{}, errors.New("stored log raw identity does not match indexed row")
	}

	transaction, _, err := decodeStoredTransaction(transactionJSON, blockHash, blockNumber, transactionIndex)
	if err != nil {
		return logEntry{}, fmt.Errorf("decode log transaction raw JSON: %w", err)
	}
	if transaction.Hash() != transactionHash {
		return logEntry{}, errors.New("stored log transaction identity does not match indexed row")
	}

	receipt, err := decodeStoredReceiptWithBlockContext(
		receiptJSON,
		blockJSON,
		transaction,
		blockHash,
		blockNumber,
		transactionIndex,
	)
	if err != nil {
		return logEntry{}, fmt.Errorf("decode log receipt raw JSON: %w", err)
	}

	block, err := decodeStoredBlockProjection(blockJSON, blockHash, blockNumber)
	if err != nil {
		return logEntry{}, fmt.Errorf("decode log block raw JSON: %w", err)
	}

	address, err := checksumAddress(wireLog.Address)
	if err != nil {
		return logEntry{}, fmt.Errorf("checksum log address: %w", err)
	}
	result := logEntry{
		Address: address, Topics: make([]string, len(wireLog.Topics)), Data: hexutil.Encode(wireLog.Data),
		BlockNumber: "0x" + blockNumber.Text(16), BlockHash: strings.ToLower(blockHash.Hex()),
		LogIndex:         "0x" + strconv.FormatInt(logIndex, 16),
		TransactionHash:  strings.ToLower(transactionHash.Hex()),
		TransactionIndex: "0x" + strconv.FormatInt(transactionIndex, 16),
	}
	for index, topic := range wireLog.Topics {
		result.Topics[index] = strings.ToLower(topic.Hex())
	}
	result.TimeStamp = hexutil.EncodeUint64(uint64(*block.Timestamp))
	result.GasUsed = hexutil.EncodeUint64(receipt.GasUsed)
	gasPrice, err := effectiveGasPrice(transaction, receipt)
	if err != nil {
		return logEntry{}, err
	}
	result.GasPrice = hexutil.EncodeBig(gasPrice)
	return result, nil
}

const logsSQL = `
SELECT log.raw, receipt.raw, inclusion.raw, block.raw, log.block_number::text,
       log.block_hash, log.log_index, log.tx_index, log.tx_hash, log.address
FROM logs AS log
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = log.chain_id
 AND canonical.number = log.block_number
 AND canonical.block_hash = log.block_hash
JOIN receipts AS receipt
  ON receipt.chain_id = log.chain_id
 AND receipt.block_number = log.block_number
 AND receipt.block_hash = log.block_hash
 AND receipt.tx_index = log.tx_index
JOIN transaction_inclusions AS inclusion
  ON inclusion.chain_id = log.chain_id
 AND inclusion.block_number = log.block_number
 AND inclusion.block_hash = log.block_hash
 AND inclusion.tx_index = log.tx_index
JOIN blocks AS block
  ON block.chain_id = log.chain_id
 AND block.number = log.block_number
 AND block.hash = log.block_hash
WHERE log.chain_id = $1::numeric
  AND %s
ORDER BY log.block_number %s, log.log_index %s, log.block_hash %s
LIMIT $%d OFFSET $%d`
