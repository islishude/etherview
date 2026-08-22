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

	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
	chainNumeric := analyticsNumeric(request.ChainID)
	var snapshot Snapshot
	var coverage Coverage
	var hours []hourRow
	err = dbaccess.WithTransactionOptions(ctx, reader.db, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, func(queries *dbgen.Queries) error {
		var queryErr error
		snapshot, queryErr = readSnapshot(ctx, queries, chainNumeric, request.ChainID)
		if queryErr != nil {
			return queryErr
		}
		coverage, queryErr = readCoverage(ctx, queries, chainNumeric)
		if queryErr != nil {
			return queryErr
		}
		if coverage.AvailableFrom == nil || coverage.AvailableTo == nil ||
			request.From.Before(*coverage.AvailableFrom) && !coverage.Complete {
			return PendingError{Coverage: coverage}
		}
		if queryErr = requireCleanAnalyticsRange(ctx, queries, chainNumeric, request.From, request.To); queryErr != nil {
			if errors.Is(queryErr, ErrPending) {
				return PendingError{Coverage: coverage}
			}
			return queryErr
		}
		rows, queryErr := queries.ListAnalyticsHours(ctx, dbgen.ListAnalyticsHoursParams{
			BucketInterval: string(interval), ChainID: chainNumeric,
			ToTime: analyticsTime(request.To), FromTime: analyticsTime(request.From),
		})
		if queryErr != nil {
			return fmt.Errorf("query analytics hours: %w", queryErr)
		}
		hours, queryErr = analyticsHoursFromRows(rows)
		return queryErr
	})
	if err != nil {
		return Series{}, err
	}
	points := aggregateHours(hours, request.Metric, interval, request.Now)
	if len(points) > maxPoints {
		return Series{}, ErrCorruptData
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
	chainNumeric := analyticsNumeric(chainID)
	var overview Overview
	var hours []hourRow
	var skipMetrics bool
	err := dbaccess.WithTransactionOptions(ctx, reader.db, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, func(queries *dbgen.Queries) error {
		snapshot, queryErr := readSnapshot(ctx, queries, chainNumeric, chainID)
		if queryErr != nil {
			return queryErr
		}
		coverage, queryErr := readCoverage(ctx, queries, chainNumeric)
		if queryErr != nil {
			return queryErr
		}
		overview = Overview{
			GeneratedAt: now, Snapshot: snapshot, Coverage: coverage,
			Pending: !coverage.Complete,
		}
		from := now.Add(-7 * 24 * time.Hour)
		if coverage.AvailableFrom == nil || coverage.AvailableTo == nil {
			overview.Pending = true
			skipMetrics = true
			return nil
		}
		if from.Before(*coverage.AvailableFrom) {
			from = *coverage.AvailableFrom
		}
		if queryErr = requireCleanAnalyticsRange(ctx, queries, chainNumeric, from, now); queryErr != nil {
			if errors.Is(queryErr, ErrPending) {
				overview.Pending = true
				skipMetrics = true
				return nil
			}
			return queryErr
		}
		rows, queryErr := queries.ListAnalyticsHours(ctx, dbgen.ListAnalyticsHoursParams{
			BucketInterval: string(IntervalHour), ChainID: chainNumeric,
			ToTime: analyticsTime(now), FromTime: analyticsTime(from),
		})
		if queryErr != nil {
			return fmt.Errorf("query overview hours: %w", queryErr)
		}
		hours, queryErr = analyticsHoursFromRows(rows)
		return queryErr
	})
	if err != nil {
		return Overview{}, err
	}
	if skipMetrics {
		return overview, nil
	}
	currentFrom, previousFrom := now.Add(-24*time.Hour), now.Add(-48*time.Hour)
	for _, metric := range metricOrder {
		previewPoints := aggregateHours(hours, metric, IntervalDay, now)
		previewPoints = latestPoints(previewPoints, 7)
		current := aggregateWindow(hours, metric, currentFrom, now)
		previous := aggregateWindow(hours, metric, previousFrom, currentFrom)
		var change *string
		if current != nil && previous != nil {
			change = percentChange(*current, *previous)
		}
		overview.Metrics = append(overview.Metrics, Preview{
			Metric: metric, CurrentValue: current, PreviousValue: previous,
			ChangePercent: change, Points: previewPoints,
		})
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

func analyticsHoursFromRows(rows []dbgen.ListAnalyticsHoursRow) ([]hourRow, error) {
	result := make([]hourRow, 0, len(rows))
	for _, source := range rows {
		if !source.BucketStart.Valid {
			return nil, ErrCorruptData
		}
		var row hourRow
		row.Start, row.FromBlock, row.ToBlock = source.BucketStart.Time.UTC(), source.FromBlock, source.ToBlock
		row.BlockCount = source.BlockCount
		row.BlockIntervalSamples, row.BaseFeeSamples = source.BlockIntervalSamples, source.BaseFeeSamples
		row.BlobSamples = source.BlobBaseFeeSamples
		values := [...]string{
			source.TransactionCount, source.FailedTransactionCount, source.ContractCreationCount,
			source.GasUsed, source.GasLimit, source.BlockIntervalSeconds,
			source.BaseFeePerGasSum, source.ExecutionGasFeeWei, source.PriorityFeeWei,
			source.BurnedWei, source.BlobGasUsed, source.BlobBaseFeePerGasSum,
			source.BlobBurnedWei, source.Erc20TransferCount, source.NftTransferCount,
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
		return new(aggregate.tx.String())
	case MetricFailedTransactions:
		return new(aggregate.failed.String())
	case MetricAverageTPS:
		return fixedRatio(aggregate.tx, aggregate.intervals)
	case MetricERC20Transfers:
		return new(aggregate.erc20.String())
	case MetricNFTTransfers:
		return new(aggregate.nft.String())
	case MetricContractCreations:
		return new(aggregate.creations.String())
	case MetricBlocks:
		return new(strconv.FormatInt(aggregate.count, 10))
	case MetricAverageBlockTime:
		return fixedRatio(aggregate.intervals, big.NewInt(aggregate.intervalSamples))
	case MetricGasUsed:
		return new(aggregate.gasUsed.String())
	case MetricGasUtilization:
		return fixedRatio(new(big.Int).Mul(aggregate.gasUsed, big.NewInt(100)), aggregate.gasLimit)
	case MetricAverageBaseFee:
		return fixedRatio(aggregate.baseFees, big.NewInt(aggregate.baseSamples))
	case MetricExecutionFees:
		return new(aggregate.execution.String())
	case MetricAverageTransactionFee:
		return fixedRatio(aggregate.execution, aggregate.tx)
	case MetricPriorityFees:
		return new(aggregate.priority.String())
	case MetricBurnedFees:
		return new(aggregate.burned.String())
	case MetricBlobGasUsed:
		return new(aggregate.blobGas.String())
	case MetricAverageBlobBaseFee:
		return fixedRatio(aggregate.blobBase, big.NewInt(aggregate.blobSamples))
	case MetricBlobBurnedFees:
		return new(aggregate.blobBurned.String())
	default:
		return nil
	}
}

func aggregateHours(hours []hourRow, metric Metric, interval Interval, now time.Time) []Point {
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
	return points
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

func readSnapshot(
	ctx context.Context,
	queries *dbgen.Queries,
	chainNumeric pgtype.Numeric,
	chainID string,
) (Snapshot, error) {
	row, err := queries.GetAnalyticsSnapshot(ctx, chainNumeric)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Snapshot{}, ErrPending
		}
		return Snapshot{}, fmt.Errorf("read analytics snapshot: %w", err)
	}
	if len(row.BlockHash) != 32 || !canonicalPositiveIntegerOrZero(row.BlockNumber) {
		return Snapshot{}, ErrCorruptData
	}
	return Snapshot{
		ChainID: chainID, BlockNumber: row.BlockNumber,
		BlockHash: "0x" + hex.EncodeToString(row.BlockHash),
	}, nil
}

func readCoverage(ctx context.Context, queries *dbgen.Queries, chainNumeric pgtype.Numeric) (Coverage, error) {
	row, err := queries.GetAnalyticsCoverage(ctx, chainNumeric)
	if err != nil {
		return Coverage{}, fmt.Errorf("read analytics coverage: %w", err)
	}
	coverage := Coverage{Complete: row.Complete, DirtyHours: row.DirtyHours, Progress: row.Progress}
	if row.AvailableFrom.Valid {
		value := row.AvailableFrom.Time.UTC()
		coverage.AvailableFrom = &value
	}
	if row.AvailableTo.Valid {
		value := row.AvailableTo.Time.UTC()
		coverage.AvailableTo = &value
	}
	return coverage, nil
}

func requireCleanAnalyticsRange(
	ctx context.Context,
	queries *dbgen.Queries,
	chainNumeric pgtype.Numeric,
	from, to time.Time,
) error {
	dirty, err := queries.CountDirtyAnalyticsHours(ctx, chainNumeric, analyticsTime(to), analyticsTime(from))
	if err != nil {
		return fmt.Errorf("read analytics dirty range: %w", err)
	}
	if dirty != "0" {
		return ErrPending
	}
	missing, err := queries.CountMissingAnalyticsRollups(ctx, chainNumeric, analyticsTime(from), analyticsTime(to))
	if err != nil {
		return fmt.Errorf("read analytics rollup range: %w", err)
	}
	if missing != "0" {
		return ErrPending
	}
	pending, err := queries.CountPendingAnalyticsSources(ctx, chainNumeric, analyticsTime(from), analyticsTime(to))
	if err != nil {
		return fmt.Errorf("read analytics source range: %w", err)
	}
	if pending != "0" {
		return ErrPending
	}
	return nil
}

func analyticsNumeric(value string) pgtype.Numeric {
	integer, _ := new(big.Int).SetString(value, 10)
	return pgtype.Numeric{Int: integer, Valid: true}
}

func analyticsTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
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
