package analytics

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"
)

const maxPoints = 500

type Reader struct {
	db *sql.DB
}

func NewReader(db *sql.DB) (*Reader, error) {
	if db == nil {
		return nil, errors.New("analytics reader requires a database")
	}
	return &Reader{db: db}, nil
}

type DetailRequest struct {
	ChainID  string
	Metric   Metric
	From     time.Time
	To       time.Time
	Interval Interval
	Now      time.Time
}

func (reader *Reader) Detail(ctx context.Context, request DetailRequest) (Series, error) {
	if reader == nil || reader.db == nil || !canonicalPositiveInteger(request.ChainID) {
		return Series{}, ErrInvalidInput
	}
	if _, ok := ParseMetric(string(request.Metric)); !ok {
		return Series{}, ErrInvalidInput
	}
	if _, ok := ParseInterval(string(request.Interval)); !ok {
		return Series{}, ErrInvalidInput
	}
	request.From, request.To = request.From.UTC(), request.To.UTC()
	if request.Now.IsZero() {
		request.Now = time.Now().UTC()
	} else {
		request.Now = request.Now.UTC()
	}
	if request.From.IsZero() || request.To.IsZero() || !request.From.Before(request.To) || request.To.After(request.Now.Add(time.Hour)) {
		return Series{}, ErrInvalidInput
	}
	interval, err := chooseInterval(request.From, request.To, request.Interval)
	if err != nil {
		return Series{}, err
	}
	tx, err := reader.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Series{}, fmt.Errorf("begin analytics snapshot: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	snapshot, err := readSnapshot(ctx, tx, request.ChainID)
	if err != nil {
		return Series{}, err
	}
	coverage, err := readCoverage(ctx, tx, request.ChainID)
	if err != nil {
		return Series{}, err
	}
	if coverage.AvailableFrom == nil || coverage.AvailableTo == nil ||
		request.From.Before(*coverage.AvailableFrom) && !coverage.Complete {
		return Series{}, PendingError{Coverage: coverage}
	}
	var dirty string
	if err := tx.QueryRowContext(ctx, dirtyRangeSQL, request.ChainID, request.From, request.To).Scan(&dirty); err != nil {
		return Series{}, fmt.Errorf("read analytics dirty range: %w", err)
	}
	if dirty != "0" {
		return Series{}, PendingError{Coverage: coverage}
	}
	if err := tx.QueryRowContext(ctx, missingRollupRangeSQL, request.ChainID, request.From, request.To).Scan(&dirty); err != nil {
		return Series{}, fmt.Errorf("read analytics rollup range: %w", err)
	}
	if dirty != "0" {
		return Series{}, PendingError{Coverage: coverage}
	}
	if err := tx.QueryRowContext(ctx, pendingSourceRangeSQL, request.ChainID, request.From, request.To).Scan(&dirty); err != nil {
		return Series{}, fmt.Errorf("read analytics source range: %w", err)
	}
	if dirty != "0" {
		return Series{}, PendingError{Coverage: coverage}
	}
	rows, err := tx.QueryContext(
		ctx, detailRangeSQL,
		request.ChainID, request.From, request.To, string(interval),
	)
	if err != nil {
		return Series{}, fmt.Errorf("query analytics hours: %w", err)
	}
	hours, err := scanHours(rows)
	if err != nil {
		return Series{}, err
	}
	points, err := aggregateHours(hours, request.Metric, interval, request.Now)
	if err != nil {
		return Series{}, err
	}
	if len(points) > maxPoints {
		return Series{}, ErrCorruptData
	}
	if err := tx.Commit(); err != nil {
		return Series{}, fmt.Errorf("commit analytics snapshot: %w", err)
	}
	return Series{
		Metric: request.Metric, Interval: interval, FromTime: request.From,
		ToTime: request.To, Points: points, Summary: summarize(points),
		Snapshot: snapshot, Coverage: coverage,
	}, nil
}

func (reader *Reader) Overview(ctx context.Context, chainID string, now time.Time) (Overview, error) {
	if reader == nil || reader.db == nil || !canonicalPositiveInteger(chainID) {
		return Overview{}, ErrInvalidInput
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	tx, err := reader.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Overview{}, fmt.Errorf("begin analytics overview snapshot: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	snapshot, err := readSnapshot(ctx, tx, chainID)
	if err != nil {
		return Overview{}, err
	}
	coverage, err := readCoverage(ctx, tx, chainID)
	if err != nil {
		return Overview{}, err
	}
	overview := Overview{
		GeneratedAt: now, Snapshot: snapshot, Coverage: coverage,
		Pending: !coverage.Complete,
	}
	from := now.Add(-7 * 24 * time.Hour)
	if coverage.AvailableFrom == nil || coverage.AvailableTo == nil {
		overview.Pending = true
		if err := tx.Commit(); err != nil {
			return Overview{}, err
		}
		return overview, nil
	}
	if from.Before(*coverage.AvailableFrom) {
		from = *coverage.AvailableFrom
	}
	var dirty string
	if err := tx.QueryRowContext(ctx, missingRollupRangeSQL, chainID, from, now).Scan(&dirty); err != nil {
		return Overview{}, fmt.Errorf("read overview rollup range: %w", err)
	}
	if dirty != "0" {
		overview.Pending = true
		if err := tx.Commit(); err != nil {
			return Overview{}, err
		}
		return overview, nil
	}
	if err := tx.QueryRowContext(ctx, dirtyRangeSQL, chainID, from, now).Scan(&dirty); err != nil {
		return Overview{}, fmt.Errorf("read overview dirty range: %w", err)
	}
	if dirty != "0" {
		overview.Pending = true
		if err := tx.Commit(); err != nil {
			return Overview{}, err
		}
		return overview, nil
	}
	if err := tx.QueryRowContext(ctx, pendingSourceRangeSQL, chainID, from, now).Scan(&dirty); err != nil {
		return Overview{}, fmt.Errorf("read overview source range: %w", err)
	}
	if dirty != "0" {
		overview.Pending = true
		if err := tx.Commit(); err != nil {
			return Overview{}, err
		}
		return overview, nil
	}
	rows, err := tx.QueryContext(ctx, hourlyRangeSQL, chainID, from, now)
	if err != nil {
		return Overview{}, fmt.Errorf("query overview hours: %w", err)
	}
	hours, err := scanHours(rows)
	if err != nil {
		return Overview{}, err
	}
	currentFrom, previousFrom := now.Add(-24*time.Hour), now.Add(-48*time.Hour)
	for _, metric := range metricOrder {
		previewPoints, aggregateErr := aggregateHours(hours, metric, IntervalDay, now)
		if aggregateErr != nil {
			return Overview{}, aggregateErr
		}
		previewPoints = latestPoints(previewPoints, 7)
		current := aggregateWindow(hours, metric, currentFrom, now)
		previous := aggregateWindow(hours, metric, previousFrom, currentFrom)
		var change *string
		if current != nil && previous != nil {
			change = percentChange(*current, *previous, 6)
		}
		overview.Metrics = append(overview.Metrics, Preview{
			Metric: metric, CurrentValue: current, PreviousValue: previous,
			ChangePercent: change, Points: previewPoints,
		})
	}
	if err := tx.Commit(); err != nil {
		return Overview{}, fmt.Errorf("commit analytics overview snapshot: %w", err)
	}
	return overview, nil
}

type hourRow struct {
	Start                                                         time.Time
	FromBlock, ToBlock                                            string
	BlockCount, BlockIntervalSamples, BaseFeeSamples, BlobSamples int64
	TransactionCount, FailedCount, CreationCount                  *big.Int
	GasUsed, GasLimit, BlockInterval                              *big.Int
	BaseFeeSum, ExecutionFees, PriorityFees, BurnedFees           *big.Int
	BlobGasUsed, BlobBaseFeeSum, BlobBurnedFees                   *big.Int
	ERC20Transfers, NFTTransfers                                  *big.Int
}

func scanHours(rows *sql.Rows) ([]hourRow, error) {
	defer rows.Close() //nolint:errcheck
	var result []hourRow
	for rows.Next() {
		var row hourRow
		var values [15]string
		if err := rows.Scan(
			&row.Start, &row.FromBlock, &row.ToBlock, &row.BlockCount,
			&values[0], &values[1], &values[2], &values[3], &values[4],
			&row.BlockIntervalSamples, &values[5], &row.BaseFeeSamples,
			&values[6], &values[7], &values[8], &values[9], &values[10],
			&values[11], &row.BlobSamples, &values[12], &values[13], &values[14],
		); err != nil {
			return nil, fmt.Errorf("scan analytics hour: %w", err)
		}
		destinations := []**big.Int{
			&row.TransactionCount, &row.FailedCount, &row.CreationCount,
			&row.GasUsed, &row.GasLimit, &row.BlockInterval, &row.BaseFeeSum,
			&row.ExecutionFees, &row.PriorityFees, &row.BurnedFees,
			&row.BlobGasUsed, &row.BlobBaseFeeSum, &row.BlobBurnedFees,
			&row.ERC20Transfers, &row.NFTTransfers,
		}
		for index, value := range values {
			parsed, ok := new(big.Int).SetString(value, 10)
			if !ok || parsed.Sign() < 0 {
				return nil, ErrCorruptData
			}
			*destinations[index] = parsed
		}
		if !canonicalPositiveIntegerOrZero(row.FromBlock) || !canonicalPositiveIntegerOrZero(row.ToBlock) ||
			row.BlockCount <= 0 || row.BlockIntervalSamples < 0 || row.BaseFeeSamples < 0 || row.BlobSamples < 0 {
			return nil, ErrCorruptData
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate analytics hours: %w", err)
	}
	return result, nil
}

type accumulator struct {
	from, to                                            string
	count, intervalSamples, baseSamples, blobSamples    int64
	tx, failed, creations, gasUsed, gasLimit, intervals *big.Int
	baseFees, execution, priority, burned               *big.Int
	blobGas, blobBase, blobBurned, erc20, nft           *big.Int
}

func newAccumulator() *accumulator {
	return &accumulator{
		tx: new(big.Int), failed: new(big.Int), creations: new(big.Int),
		gasUsed: new(big.Int), gasLimit: new(big.Int), intervals: new(big.Int),
		baseFees: new(big.Int), execution: new(big.Int), priority: new(big.Int),
		burned: new(big.Int), blobGas: new(big.Int), blobBase: new(big.Int),
		blobBurned: new(big.Int), erc20: new(big.Int), nft: new(big.Int),
	}
}

func (aggregate *accumulator) add(hour hourRow) {
	if aggregate.count == 0 {
		aggregate.from = hour.FromBlock
	}
	aggregate.to = hour.ToBlock
	aggregate.count += hour.BlockCount
	aggregate.intervalSamples += hour.BlockIntervalSamples
	aggregate.baseSamples += hour.BaseFeeSamples
	aggregate.blobSamples += hour.BlobSamples
	for _, pair := range [][2]*big.Int{
		{aggregate.tx, hour.TransactionCount}, {aggregate.failed, hour.FailedCount},
		{aggregate.creations, hour.CreationCount}, {aggregate.gasUsed, hour.GasUsed},
		{aggregate.gasLimit, hour.GasLimit}, {aggregate.intervals, hour.BlockInterval},
		{aggregate.baseFees, hour.BaseFeeSum}, {aggregate.execution, hour.ExecutionFees},
		{aggregate.priority, hour.PriorityFees}, {aggregate.burned, hour.BurnedFees},
		{aggregate.blobGas, hour.BlobGasUsed}, {aggregate.blobBase, hour.BlobBaseFeeSum},
		{aggregate.blobBurned, hour.BlobBurnedFees}, {aggregate.erc20, hour.ERC20Transfers},
		{aggregate.nft, hour.NFTTransfers},
	} {
		pair[0].Add(pair[0], pair[1])
	}
}

func (aggregate *accumulator) value(metric Metric) *string {
	switch metric {
	case MetricTransactions:
		return stringPointer(aggregate.tx.String())
	case MetricFailedTransactions:
		return stringPointer(aggregate.failed.String())
	case MetricAverageTPS:
		return fixedRatio(aggregate.tx, aggregate.intervals, 18)
	case MetricERC20Transfers:
		return stringPointer(aggregate.erc20.String())
	case MetricNFTTransfers:
		return stringPointer(aggregate.nft.String())
	case MetricContractCreations:
		return stringPointer(aggregate.creations.String())
	case MetricBlocks:
		return stringPointer(strconv.FormatInt(aggregate.count, 10))
	case MetricAverageBlockTime:
		return fixedRatio(aggregate.intervals, big.NewInt(aggregate.intervalSamples), 18)
	case MetricGasUsed:
		return stringPointer(aggregate.gasUsed.String())
	case MetricGasUtilization:
		return fixedRatio(new(big.Int).Mul(aggregate.gasUsed, big.NewInt(100)), aggregate.gasLimit, 18)
	case MetricAverageBaseFee:
		return fixedRatio(aggregate.baseFees, big.NewInt(aggregate.baseSamples), 18)
	case MetricExecutionFees:
		return stringPointer(aggregate.execution.String())
	case MetricAverageTransactionFee:
		return fixedRatio(aggregate.execution, aggregate.tx, 18)
	case MetricPriorityFees:
		return stringPointer(aggregate.priority.String())
	case MetricBurnedFees:
		return stringPointer(aggregate.burned.String())
	case MetricBlobGasUsed:
		return stringPointer(aggregate.blobGas.String())
	case MetricAverageBlobBaseFee:
		return fixedRatio(aggregate.blobBase, big.NewInt(aggregate.blobSamples), 18)
	case MetricBlobBurnedFees:
		return stringPointer(aggregate.blobBurned.String())
	default:
		return nil
	}
}

func aggregateHours(hours []hourRow, metric Metric, interval Interval, now time.Time) ([]Point, error) {
	type grouped struct {
		start time.Time
		value *accumulator
	}
	var groups []grouped
	for _, hour := range hours {
		start := bucketStart(hour.Start, interval)
		if len(groups) == 0 || !groups[len(groups)-1].start.Equal(start) {
			groups = append(groups, grouped{start: start, value: newAccumulator()})
		}
		groups[len(groups)-1].value.add(hour)
	}
	points := make([]Point, 0, len(groups))
	for _, group := range groups {
		value := group.value.value(metric)
		if value == nil {
			continue
		}
		end := bucketEnd(group.start, interval)
		points = append(points, Point{
			BucketStart: group.start, BucketEnd: end, Value: *value,
			Partial: now.Before(end), FromBlock: group.value.from, ToBlock: group.value.to,
		})
	}
	return points, nil
}

func aggregateWindow(hours []hourRow, metric Metric, from, to time.Time) *string {
	aggregate := newAccumulator()
	for _, hour := range hours {
		if !hour.Start.Before(from) && hour.Start.Before(to) {
			aggregate.add(hour)
		}
	}
	if aggregate.count == 0 {
		return nil
	}
	return aggregate.value(metric)
}

func latestPoints(points []Point, limit int) []Point {
	if limit <= 0 {
		return nil
	}
	if len(points) <= limit {
		return points
	}
	return points[len(points)-limit:]
}

func summarize(points []Point) Summary {
	if len(points) == 0 {
		return Summary{}
	}
	total := new(big.Rat)
	var highest, lowest *big.Rat
	for _, point := range points {
		value, ok := new(big.Rat).SetString(point.Value)
		if !ok {
			continue
		}
		total.Add(total, value)
		if highest == nil || value.Cmp(highest) > 0 {
			highest = new(big.Rat).Set(value)
		}
		if lowest == nil || value.Cmp(lowest) < 0 {
			lowest = new(big.Rat).Set(value)
		}
	}
	current := points[len(points)-1].Value
	summary := Summary{Current: &current}
	if highest != nil {
		value := ratString(highest)
		summary.Highest = &value
	}
	if lowest != nil {
		value := ratString(lowest)
		summary.Lowest = &value
	}
	totalValue := ratString(total)
	summary.Total = &totalValue
	average := new(big.Rat).Quo(total, big.NewRat(int64(len(points)), 1))
	averageValue := ratString(average)
	summary.Average = &averageValue
	return summary
}

func ratString(value *big.Rat) string {
	result := value.FloatString(18)
	for len(result) > 1 && result[len(result)-1] == '0' {
		result = result[:len(result)-1]
	}
	if result[len(result)-1] == '.' {
		result = result[:len(result)-1]
	}
	return result
}

func chooseInterval(from, to time.Time, requested Interval) (Interval, error) {
	fits := func(interval Interval) bool {
		count := 0
		for cursor := bucketStart(from, interval); cursor.Before(to); cursor = bucketEnd(cursor, interval) {
			count++
			if count > maxPoints {
				return false
			}
		}
		return true
	}
	if requested != IntervalAuto {
		if !fits(requested) {
			return "", ErrInvalidInput
		}
		return requested, nil
	}
	for _, interval := range []Interval{IntervalHour, IntervalDay, IntervalWeek, IntervalMonth} {
		if fits(interval) {
			return interval, nil
		}
	}
	return "", ErrInvalidInput
}

func bucketStart(value time.Time, interval Interval) time.Time {
	value = value.UTC()
	switch interval {
	case IntervalHour:
		return value.Truncate(time.Hour)
	case IntervalDay:
		return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	case IntervalWeek:
		day := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
		offset := (int(day.Weekday()) + 6) % 7
		return day.AddDate(0, 0, -offset)
	case IntervalMonth:
		return time.Date(value.Year(), value.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return value
	}
}

func bucketEnd(value time.Time, interval Interval) time.Time {
	switch interval {
	case IntervalHour:
		return value.Add(time.Hour)
	case IntervalDay:
		return value.AddDate(0, 0, 1)
	case IntervalWeek:
		return value.AddDate(0, 0, 7)
	case IntervalMonth:
		return value.AddDate(0, 1, 0)
	default:
		return value
	}
}

func readSnapshot(ctx context.Context, tx *sql.Tx, chainID string) (Snapshot, error) {
	var result Snapshot
	var hash []byte
	if err := tx.QueryRowContext(ctx, snapshotSQL, chainID).Scan(&result.BlockNumber, &hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Snapshot{}, ErrPending
		}
		return Snapshot{}, fmt.Errorf("read analytics snapshot: %w", err)
	}
	if len(hash) != 32 || !canonicalPositiveIntegerOrZero(result.BlockNumber) {
		return Snapshot{}, ErrCorruptData
	}
	result.ChainID, result.BlockHash = chainID, "0x"+hex.EncodeToString(hash)
	return result, nil
}

func readCoverage(ctx context.Context, tx *sql.Tx, chainID string) (Coverage, error) {
	var coverage Coverage
	var from, to sql.NullTime
	var dirty, progress string
	var complete bool
	err := tx.QueryRowContext(ctx, coverageSQL, chainID).Scan(
		&from, &to, &complete, &dirty, &progress,
	)
	if err != nil {
		return Coverage{}, fmt.Errorf("read analytics coverage: %w", err)
	}
	if from.Valid {
		value := from.Time.UTC()
		coverage.AvailableFrom = &value
	}
	if to.Valid {
		value := to.Time.UTC()
		coverage.AvailableTo = &value
	}
	coverage.Complete, coverage.DirtyHours, coverage.Progress = complete, dirty, progress
	return coverage, nil
}

func canonicalPositiveInteger(value string) bool {
	return value != "0" && canonicalPositiveIntegerOrZero(value)
}

func canonicalPositiveIntegerOrZero(value string) bool {
	if value == "" || len(value) > 78 || len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func stringPointer(value string) *string { return &value }

const snapshotSQL = `
SELECT canonical.number::text, canonical.block_hash
FROM canonical_blocks AS canonical
WHERE canonical.chain_id = $1::numeric
ORDER BY canonical.number DESC
LIMIT 1`

const coverageSQL = `
SELECT backfill.available_from, backfill.available_to, COALESCE(backfill.complete, false),
       (SELECT count(*)::text FROM chart_rollup_dirty_hours WHERE chain_id = $1::numeric),
       CASE
           WHEN COALESCE(backfill.total_blocks, 0) = 0 THEN '0'
           ELSE trim(trailing '.' FROM trim(trailing '0' FROM
               round(backfill.completed_blocks::numeric * 100 / backfill.total_blocks, 18)::text
           ))
       END
FROM (SELECT 1) AS singleton
LEFT JOIN chart_rollup_backfill AS backfill ON backfill.chain_id = $1::numeric`

const dirtyRangeSQL = `
SELECT count(*)::text
FROM chart_rollup_dirty_hours
WHERE chain_id = $1::numeric
  AND bucket_start < $3
  AND bucket_start + interval '1 hour' > $2`

const pendingSourceRangeSQL = `
SELECT count(*)::text
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
LEFT JOIN published_block_stage_results AS stats_result
  ON stats_result.chain_id = canonical.chain_id
 AND stats_result.block_number = canonical.number
 AND stats_result.block_hash = canonical.block_hash
 AND stats_result.stage = 'stats'
 AND stats_result.stage_version = 3
 AND stats_result.state = 'complete'
LEFT JOIN published_block_stage_results AS token_result
  ON token_result.chain_id = canonical.chain_id
 AND token_result.block_number = canonical.number
 AND token_result.block_hash = canonical.block_hash
 AND token_result.stage = 'token'
 AND token_result.stage_version = 1
 AND token_result.state = 'complete'
WHERE canonical.chain_id = $1::numeric
  AND block.timestamp >= extract(epoch FROM $2::timestamptz)::numeric
  AND block.timestamp < extract(epoch FROM $3::timestamptz)::numeric
  AND (stats_result.block_number IS NULL OR token_result.block_number IS NULL)`

const missingRollupRangeSQL = `
SELECT count(*)::text
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
LEFT JOIN chart_hourly_rollups AS rollup
  ON rollup.chain_id = canonical.chain_id
 AND rollup.bucket_start = date_trunc(
     'hour',
     to_timestamp(block.timestamp::double precision),
     'UTC'
 )
WHERE canonical.chain_id = $1::numeric
  AND block.timestamp >= extract(epoch FROM $2::timestamptz)::numeric
  AND block.timestamp < extract(epoch FROM $3::timestamptz)::numeric
  AND rollup.bucket_start IS NULL`

const hourlyRangeSQL = `
SELECT bucket_start, from_block::text, to_block::text, block_count,
       transaction_count::text, failed_transaction_count::text,
       contract_creation_count::text, gas_used::text, gas_limit::text,
       block_interval_samples, block_interval_seconds::text,
       base_fee_samples, base_fee_per_gas_sum::text,
       execution_gas_fee_wei::text, priority_fee_wei::text, burned_wei::text,
       blob_gas_used::text, blob_base_fee_samples, blob_base_fee_per_gas_sum::text,
       blob_burned_wei::text, erc20_transfer_count::text, nft_transfer_count::text
FROM chart_hourly_rollups
WHERE chain_id = $1::numeric
  AND bucket_start < $3
  AND bucket_start + interval '1 hour' > $2
ORDER BY bucket_start`

const detailRangeSQL = `
WITH requested AS (
    SELECT CASE $4::text
               WHEN 'hour' THEN date_trunc('hour', bucket_start, 'UTC')
               WHEN 'day' THEN date_trunc('day', bucket_start, 'UTC')
               WHEN 'week' THEN date_trunc('week', bucket_start, 'UTC')
               WHEN 'month' THEN date_trunc('month', bucket_start, 'UTC')
           END AS bucket_start,
           from_block, to_block, block_count, transaction_count,
           failed_transaction_count, contract_creation_count, gas_used,
           gas_limit, block_interval_samples, block_interval_seconds,
           base_fee_samples, base_fee_per_gas_sum, execution_gas_fee_wei,
           priority_fee_wei, burned_wei, blob_gas_used, blob_base_fee_samples,
           blob_base_fee_per_gas_sum, blob_burned_wei, erc20_transfer_count,
           nft_transfer_count
    FROM chart_hourly_rollups
    WHERE chain_id = $1::numeric
      AND bucket_start < $3
      AND bucket_start + interval '1 hour' > $2
)
SELECT bucket_start, min(from_block)::text, max(to_block)::text,
       sum(block_count)::bigint, sum(transaction_count)::text,
       sum(failed_transaction_count)::text, sum(contract_creation_count)::text,
       sum(gas_used)::text, sum(gas_limit)::text,
       sum(block_interval_samples)::bigint, sum(block_interval_seconds)::text,
       sum(base_fee_samples)::bigint, sum(base_fee_per_gas_sum)::text,
       sum(execution_gas_fee_wei)::text, sum(priority_fee_wei)::text,
       sum(burned_wei)::text, sum(blob_gas_used)::text,
       sum(blob_base_fee_samples)::bigint, sum(blob_base_fee_per_gas_sum)::text,
       sum(blob_burned_wei)::text, sum(erc20_transfer_count)::text,
       sum(nft_transfer_count)::text
FROM requested
GROUP BY bucket_start
ORDER BY bucket_start`
