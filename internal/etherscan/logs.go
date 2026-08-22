package etherscan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/islishude/etherview/internal/db/gen"
)

var topicOperatorPattern = regexp.MustCompile(`^topic([0-3])_([0-3])_opr$`)

func (b *PostgresBackend) logs(ctx context.Context, values url.Values) ([]logEntry, error) {
	page, err := parsePagination(values)
	if err != nil {
		return nil, err
	}
	fromBlock := "0"
	if raw := strings.TrimSpace(values.Get("fromBlock")); raw != "" {
		value, err := parseDecimal(raw, "fromBlock")
		if err != nil {
			return nil, err
		}
		fromBlock = value.String()
	}
	var coverageEnd *string
	var toBlock any
	if raw := strings.TrimSpace(values.Get("toBlock")); raw != "" {
		value, err := parseDecimal(raw, "toBlock")
		if err != nil {
			return nil, err
		}
		if value.Cmp(mustBig(fromBlock)) < 0 {
			return nil, invalidParameter("toBlock is less than fromBlock")
		}
		text := value.String()
		coverageEnd = &text
		toBlock = text
	}
	var address any
	if raw := strings.TrimSpace(values.Get("address")); raw != "" {
		_, addressBytes, err := parseAddressParameter(raw, "address")
		if err != nil {
			return nil, err
		}
		address = addressBytes
	}

	topicFilters, err := buildTopicFilter(values)
	if err != nil {
		return nil, err
	}
	encodedTopics, err := json.Marshal(topicFilters)
	if err != nil {
		return nil, fmt.Errorf("encode log topic filters: %w", err)
	}
	tx, err := b.beginCanonicalSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := b.requireCanonicalCoreRange(ctx, tx, fromBlock, coverageEnd); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, dbgen.EtherscanLogs,
		b.chain, fromBlock, toBlock, address, encodedTopics,
		page.limit, page.offset, page.direction,
	)
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

type sqlTopicFilter struct {
	Index    int    `json:"index"`
	Value    string `json:"value"`
	Operator string `json:"operator"`
}

func buildTopicFilter(values url.Values) ([]sqlTopicFilter, error) {
	type topicFilter struct {
		index int
		value string
	}
	filters := make([]topicFilter, 0, 4)
	for key, items := range values {
		if !strings.HasPrefix(key, "topic") {
			continue
		}
		validFilter := len(key) == len("topic0") && key[len(key)-1] >= '0' && key[len(key)-1] <= '3'
		validOperator := topicOperatorPattern.MatchString(key)
		if !validFilter && !validOperator {
			return nil, invalidParameter("unsupported topic parameter %s", key)
		}
		if len(items) != 1 {
			return nil, invalidParameter("topic parameter %s must appear exactly once", key)
		}
	}
	for index := range 4 {
		name := fmt.Sprintf("topic%d", index)
		raw := strings.TrimSpace(values.Get(name))
		if raw == "" {
			continue
		}
		hash, _, err := parseHashParameter(raw, name)
		if err != nil {
			return nil, err
		}
		filters = append(filters, topicFilter{index: index, value: strings.ToLower(hash.Hex())})
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
			return nil, invalidParameter("invalid topic operator %s", key)
		}
		left, _ := strconv.Atoi(match[1])
		right, _ := strconv.Atoi(match[2])
		if left >= right || strings.TrimSpace(values.Get(fmt.Sprintf("topic%d", left))) == "" || strings.TrimSpace(values.Get(fmt.Sprintf("topic%d", right))) == "" {
			return nil, invalidParameter("topic operator %s references missing or unordered filters", key)
		}
		if _, supported := allowedOperators[key]; !supported {
			return nil, invalidParameter("topic operator %s does not connect adjacent supplied filters", key)
		}
		operator := strings.ToLower(strings.TrimSpace(items[0]))
		if operator != "and" && operator != "or" {
			return nil, invalidParameter("topic operator %s must be and or or", key)
		}
	}
	result := make([]sqlTopicFilter, 0, len(filters))
	for index, filter := range filters {
		operator := "AND"
		if index > 0 {
			left, right := filters[index-1].index, filter.index
			operator = strings.ToUpper(strings.TrimSpace(values.Get(fmt.Sprintf("topic%d_%d_opr", left, right))))
			if operator == "" {
				operator = "AND"
			}
		}
		result = append(result, sqlTopicFilter{Index: filter.index, Value: filter.value, Operator: operator})
	}
	return result, nil
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
