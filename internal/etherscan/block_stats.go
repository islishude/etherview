package etherscan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/islishude/etherview/internal/db/gen"
	"math/big"
	"net/url"
	"strings"
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
	tx, err := b.beginCanonicalSnapshot(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := b.requireCanonicalCoreRange(ctx, tx, "0", nil); err != nil {
		return "", err
	}
	var numberText, timestampText string
	var hashBytes []byte
	query := dbgen.EtherscanBlockNumberByTimeBefore
	if closest == "after" {
		query = dbgen.EtherscanBlockNumberByTimeAfter
	}
	err = tx.QueryRowContext(ctx, query, b.chain, timestamp.String()).Scan(
		&numberText, &hashBytes, &timestampText,
	)
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
	if _, err := hashFromBytes(hashBytes); err != nil {
		return "", err
	}
	indexedTimestamp, ok := new(big.Int).SetString(timestampText, 10)
	if !ok || indexedTimestamp.Sign() < 0 {
		return "", errors.New("stored block-by-time timestamp is invalid")
	}
	if closest == "before" && indexedTimestamp.Cmp(timestamp) > 0 || closest == "after" && indexedTimestamp.Cmp(timestamp) < 0 {
		return "", errors.New("block-by-time query returned a block outside the requested bound")
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit block-by-time snapshot: %w", err)
	}
	return number.String(), nil
}

func (b *PostgresBackend) blockCountdown(ctx context.Context, values url.Values) (blockCountdown, error) {
	target, err := parseDecimal(values.Get("blockno"), "blockno")
	if err != nil {
		return blockCountdown{}, err
	}
	tx, err := b.beginCanonicalSnapshot(ctx)
	if err != nil {
		return blockCountdown{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	var (
		currentText, currentTimestampText, anchorText, anchorTimestampText string
		sampleCountText, configuredStartText, rangeStartText, rangeEndText string
	)
	err = tx.QueryRowContext(ctx, dbgen.EtherscanBlockCountdown, b.chain).Scan(
		&currentText, &currentTimestampText, &anchorText, &anchorTimestampText,
		&sampleCountText, &configuredStartText, &rangeStartText, &rangeEndText,
	)
	if err == sql.ErrNoRows {
		return blockCountdown{}, ErrCoreUnavailable
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
	sampleCount, err := storedUint256(sampleCountText, "countdown sample count")
	if err != nil || sampleCount.Sign() == 0 || sampleCount.Cmp(big.NewInt(128)) > 0 {
		return blockCountdown{}, errors.New("countdown sample count is invalid")
	}
	configuredStart, err := storedUint256(configuredStartText, "countdown configured start")
	if err != nil {
		return blockCountdown{}, err
	}
	rangeStart, err := storedUint256(rangeStartText, "countdown coverage start")
	if err != nil {
		return blockCountdown{}, err
	}
	rangeEnd, err := storedUint256(rangeEndText, "countdown coverage end")
	if err != nil {
		return blockCountdown{}, err
	}
	if rangeStart.Cmp(configuredStart) < 0 || anchor.Cmp(rangeStart) < 0 || rangeEnd.Cmp(current) != 0 {
		return blockCountdown{}, errors.New("countdown coverage interval is inconsistent")
	}
	blockSpan := new(big.Int).Sub(current, anchor)
	expectedSamples := new(big.Int).Add(new(big.Int).Set(blockSpan), big.NewInt(1))
	if expectedSamples.Cmp(sampleCount) != 0 {
		return blockCountdown{}, errors.New("countdown canonical samples are not continuous")
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
	timeSpan := new(big.Int).Sub(currentTimestamp, anchorTimestamp)
	if blockSpan.Sign() == 0 || timeSpan.Sign() == 0 {
		return blockCountdown{}, ErrEstimateUnavailable
	}
	// Ceiling division avoids promising a target earlier than the observed
	// canonical cadence supports.
	numerator := new(big.Int).Mul(remaining, timeSpan)
	numerator.Add(numerator, new(big.Int).Sub(blockSpan, big.NewInt(1)))
	result.EstimateTimeInSec = numerator.Div(numerator, blockSpan).String()
	if err := tx.Commit(); err != nil {
		return blockCountdown{}, fmt.Errorf("commit block countdown snapshot: %w", err)
	}
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
