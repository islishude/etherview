// Package analytics owns the bounded historical execution-statistics
// projection. PostgreSQL remains the authority for every source and rollup.
package analytics

import (
	"errors"
	"math/big"
	"slices"
	"strings"
	"time"
)

var (
	ErrInvalidInput = errors.New("analytics input is invalid")
	ErrPending      = errors.New("analytics range is pending")
	ErrCorruptData  = errors.New("analytics data is inconsistent")
)

type Metric string

const (
	MetricTransactions          Metric = "transactions"
	MetricFailedTransactions    Metric = "failed-transactions"
	MetricAverageTPS            Metric = "average-tps"
	MetricERC20Transfers        Metric = "erc20-transfers"
	MetricNFTTransfers          Metric = "nft-transfers"
	MetricContractCreations     Metric = "contract-creations"
	MetricBlocks                Metric = "blocks"
	MetricAverageBlockTime      Metric = "average-block-time"
	MetricGasUsed               Metric = "gas-used"
	MetricGasUtilization        Metric = "gas-utilization"
	MetricAverageBaseFee        Metric = "average-base-fee"
	MetricExecutionFees         Metric = "execution-fees"
	MetricAverageTransactionFee Metric = "average-transaction-fee"
	MetricPriorityFees          Metric = "priority-fees"
	MetricBurnedFees            Metric = "burned-fees"
	MetricBlobGasUsed           Metric = "blob-gas-used"
	MetricAverageBlobBaseFee    Metric = "average-blob-base-fee"
	MetricBlobBurnedFees        Metric = "blob-burned-fees"
)

var metricOrder = []Metric{
	MetricTransactions, MetricFailedTransactions, MetricAverageTPS,
	MetricERC20Transfers, MetricNFTTransfers, MetricContractCreations,
	MetricBlocks, MetricAverageBlockTime, MetricGasUsed, MetricGasUtilization,
	MetricAverageBaseFee, MetricExecutionFees, MetricAverageTransactionFee,
	MetricPriorityFees, MetricBurnedFees, MetricBlobGasUsed,
	MetricAverageBlobBaseFee, MetricBlobBurnedFees,
}

func Metrics() []Metric { return append([]Metric(nil), metricOrder...) }

func ParseMetric(value string) (Metric, bool) {
	metric := Metric(value)
	if slices.Contains(metricOrder, metric) {
		return metric, true
	}
	return "", false
}

func (metric Metric) IsAverage() bool {
	switch metric {
	case MetricAverageTPS, MetricAverageBlockTime, MetricGasUtilization,
		MetricAverageBaseFee, MetricAverageTransactionFee, MetricAverageBlobBaseFee:
		return true
	default:
		return false
	}
}

type Interval string

const (
	IntervalAuto  Interval = "auto"
	IntervalHour  Interval = "hour"
	IntervalDay   Interval = "day"
	IntervalWeek  Interval = "week"
	IntervalMonth Interval = "month"
)

func ParseInterval(value string) (Interval, bool) {
	interval := Interval(value)
	switch interval {
	case IntervalAuto, IntervalHour, IntervalDay, IntervalWeek, IntervalMonth:
		return interval, true
	default:
		return "", false
	}
}

type Snapshot struct {
	ChainID     string
	BlockNumber string
	BlockHash   string
}

type Coverage struct {
	AvailableFrom *time.Time
	AvailableTo   *time.Time
	Complete      bool
	DirtyHours    string
	Progress      string
}

type Point struct {
	BucketStart time.Time
	BucketEnd   time.Time
	Value       string
	Partial     bool
	FromBlock   string
	ToBlock     string
}

type Summary struct {
	Current *string
	Highest *string
	Lowest  *string
	Total   *string
	Average *string
}

type Series struct {
	Metric   Metric
	Interval Interval
	FromTime time.Time
	ToTime   time.Time
	Points   []Point
	Summary  Summary
	Snapshot Snapshot
	Coverage Coverage
}

type Preview struct {
	Metric        Metric
	CurrentValue  *string
	PreviousValue *string
	ChangePercent *string
	Points        []Point
}

type Overview struct {
	GeneratedAt time.Time
	Snapshot    Snapshot
	Coverage    Coverage
	Metrics     []Preview
	Pending     bool
}

type PendingError struct {
	Coverage Coverage
}

func (err PendingError) Error() string { return ErrPending.Error() }
func (err PendingError) Unwrap() error { return ErrPending }

func fixedRatio(numerator, denominator *big.Int, scale int) *string {
	if numerator == nil || denominator == nil || denominator.Sign() <= 0 {
		return nil
	}
	value := new(big.Rat).SetFrac(numerator, denominator).FloatString(scale)
	value = strings.TrimRight(strings.TrimRight(value, "0"), ".")
	if value == "" {
		value = "0"
	}
	return &value
}

func percentChange(current, previous string, scale int) *string {
	currentValue, currentOK := new(big.Rat).SetString(current)
	previousValue, previousOK := new(big.Rat).SetString(previous)
	if !currentOK || !previousOK || previousValue.Sign() == 0 {
		return nil
	}
	change := new(big.Rat).Sub(currentValue, previousValue)
	change.Mul(change, big.NewRat(100, 1))
	change.Quo(change, previousValue)
	value := ratString(change)
	if scale >= 0 {
		value = change.FloatString(scale)
		value = strings.TrimRight(strings.TrimRight(value, "0"), ".")
		if value == "" || value == "-0" {
			value = "0"
		}
	}
	return &value
}
