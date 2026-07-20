package etherscan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"

	"github.com/islishude/etherview/internal/ethrpc"
)

func (b *PostgresBackend) blockNumberByTime(ctx context.Context, values url.Values) (string, error) {
	timestamp, err := parseDecimal(values.Get("timestamp"), "timestamp")
	if err != nil {
		return "", err
	}
	closest := strings.ToLower(strings.TrimSpace(values.Get("closest")))
	if closest != "before" && closest != "after" {
		return "", invalidParameter("closest must be before or after")
	}
	comparison, direction := "<=", "DESC"
	if closest == "after" {
		comparison, direction = ">=", "ASC"
	}
	query := fmt.Sprintf(blockNumberByTimeSQL, comparison, direction, direction, direction)
	var raw []byte
	var numberText, timestampText string
	var hashBytes []byte
	err = b.db.QueryRowContext(ctx, query, b.chain, timestamp.String()).Scan(&raw, &numberText, &hashBytes, &timestampText)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("query block by time: %w", err)
	}
	number, ok := new(big.Int).SetString(numberText, 10)
	if !ok || number.Sign() < 0 {
		return "", errors.New("stored block number is invalid")
	}
	hash, err := hashFromBytes(hashBytes)
	if err != nil {
		return "", err
	}
	var block ethrpc.Block
	if err := decodeRawObject(raw, &block); err != nil {
		return "", fmt.Errorf("decode block-by-time raw JSON: %w", err)
	}
	if block.Number == nil || block.Hash == nil || !block.Hash.Equal(hash) {
		return "", errors.New("stored block-by-time raw identity does not match indexed row")
	}
	wireNumber, err := block.Number.Big()
	if err != nil || wireNumber.Cmp(number) != 0 {
		return "", errors.New("stored block-by-time raw number does not match indexed row")
	}
	wireTimestamp, err := block.Timestamp.Big()
	if err != nil {
		return "", fmt.Errorf("decode block-by-time timestamp: %w", err)
	}
	indexedTimestamp, ok := new(big.Int).SetString(timestampText, 10)
	if !ok || indexedTimestamp.Sign() < 0 || wireTimestamp.Cmp(indexedTimestamp) != 0 {
		return "", errors.New("stored block-by-time raw timestamp does not match indexed row")
	}
	if closest == "before" && wireTimestamp.Cmp(timestamp) > 0 || closest == "after" && wireTimestamp.Cmp(timestamp) < 0 {
		return "", errors.New("block-by-time query returned a block outside the requested bound")
	}
	return number.String(), nil
}

func (b *PostgresBackend) blockCountdown(ctx context.Context, values url.Values) (blockCountdown, error) {
	target, err := parseDecimal(values.Get("blockno"), "blockno")
	if err != nil {
		return blockCountdown{}, err
	}
	var currentText, currentTimestampText, anchorText, anchorTimestampText string
	err = b.db.QueryRowContext(ctx, blockCountdownSQL, b.chain).Scan(
		&currentText, &currentTimestampText, &anchorText, &anchorTimestampText,
	)
	if err == sql.ErrNoRows {
		return blockCountdown{}, ErrNotFound
	}
	if err != nil {
		return blockCountdown{}, fmt.Errorf("query block countdown basis: %w", err)
	}
	current, ok := new(big.Int).SetString(currentText, 10)
	if !ok || current.Sign() < 0 {
		return blockCountdown{}, errors.New("current canonical block is invalid")
	}
	currentTimestamp, ok := new(big.Int).SetString(currentTimestampText, 10)
	if !ok || currentTimestamp.Sign() < 0 {
		return blockCountdown{}, errors.New("current canonical timestamp is invalid")
	}
	anchor, ok := new(big.Int).SetString(anchorText, 10)
	if !ok || anchor.Sign() < 0 || anchor.Cmp(current) > 0 {
		return blockCountdown{}, errors.New("countdown anchor block is invalid")
	}
	anchorTimestamp, ok := new(big.Int).SetString(anchorTimestampText, 10)
	if !ok || anchorTimestamp.Sign() < 0 || anchorTimestamp.Cmp(currentTimestamp) > 0 {
		return blockCountdown{}, errors.New("countdown anchor timestamp is invalid")
	}
	result := blockCountdown{
		CurrentBlock: current.String(), CountdownBlock: target.String(),
		RemainingBlock: "0", EstimateTimeInSec: "0",
	}
	if target.Cmp(current) <= 0 {
		return blockCountdown{}, ErrBlockAlreadyPassed
	}
	remaining := new(big.Int).Sub(target, current)
	result.RemainingBlock = remaining.String()
	blockSpan := new(big.Int).Sub(current, anchor)
	timeSpan := new(big.Int).Sub(currentTimestamp, anchorTimestamp)
	if blockSpan.Sign() == 0 || timeSpan.Sign() == 0 {
		return blockCountdown{}, ErrEstimateUnavailable
	}
	// Ceiling division avoids promising a target earlier than the observed
	// canonical cadence supports.
	numerator := new(big.Int).Mul(remaining, timeSpan)
	numerator.Add(numerator, new(big.Int).Sub(blockSpan, big.NewInt(1)))
	result.EstimateTimeInSec = numerator.Div(numerator, blockSpan).String()
	return result, nil
}

func (b *PostgresBackend) ethSupply(ctx context.Context) (string, error) {
	if b.supply == nil {
		return "", ErrSupplyUnavailable
	}
	value, err := b.supply(ctx, b.chainID)
	if err != nil {
		return "", fmt.Errorf("read native currency supply: %w", err)
	}
	parsed, err := parseCanonicalDecimal(value)
	if err != nil {
		return "", fmt.Errorf("supply provider returned invalid uint256 decimal: %w", err)
	}
	return parsed.String(), nil
}

const blockNumberByTimeSQL = `
SELECT block.raw, block.number::text, block.hash, block.timestamp::text
FROM blocks AS block
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = block.chain_id
 AND canonical.number = block.number
 AND canonical.block_hash = block.hash
WHERE block.chain_id = $1::numeric
  AND block.timestamp %s $2::numeric
ORDER BY block.timestamp %s, block.number %s, block.hash %s
LIMIT 1`

const blockCountdownSQL = `
WITH recent AS (
    SELECT block.number, block.timestamp
    FROM blocks AS block
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = block.chain_id
     AND canonical.number = block.number
     AND canonical.block_hash = block.hash
    WHERE block.chain_id = $1::numeric
    ORDER BY block.number DESC
    LIMIT 128
), tip AS (
    SELECT number, timestamp FROM recent ORDER BY number DESC LIMIT 1
), anchor AS (
    SELECT number, timestamp FROM recent ORDER BY number ASC LIMIT 1
)
SELECT tip.number::text, tip.timestamp::text,
       anchor.number::text, anchor.timestamp::text
FROM tip CROSS JOIN anchor`
