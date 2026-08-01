package analytics

import (
	"math/big"
	"testing"
	"time"
)

func TestAggregateHoursUsesExactFixedPointSemanticsAndOmitsEmptyBuckets(t *testing.T) {
	start := time.Date(2026, 7, 27, 23, 0, 0, 0, time.UTC)
	hours := []hourRow{
		testHour(start, "9", "9", 1, 10, 1),
		testHour(start.Add(2*time.Hour), "10", "10", 1, 20, 2),
	}
	hours[0].GasUsed, hours[0].GasLimit = big.NewInt(1), big.NewInt(3)
	hours[1].GasUsed, hours[1].GasLimit = big.NewInt(2), big.NewInt(3)
	points := aggregateHours(hours, MetricGasUtilization, IntervalHour, start.Add(3*time.Hour))
	if len(points) != 2 {
		t.Fatalf("points=%+v, want two source buckets and no fabricated gap", points)
	}
	if points[0].Value != "33.333333333333333333" || points[1].Value != "66.666666666666666667" {
		t.Fatalf("gas utilization points=%+v", points)
	}
}

func TestAggregateHoursUsesMondayUTCWeeksAndDerivedAverages(t *testing.T) {
	sunday := time.Date(2026, 7, 26, 23, 0, 0, 0, time.UTC)
	monday := sunday.Add(time.Hour)
	hours := []hourRow{
		testHour(sunday, "99", "99", 2, 10, 1),
		testHour(monday, "100", "100", 2, 20, 1),
	}
	points := aggregateHours(hours, MetricAverageTransactionFee, IntervalWeek, monday.Add(time.Hour))
	if len(points) != 2 {
		t.Fatalf("weekly points=%+v", points)
	}
	if got := points[0].BucketStart.Weekday(); got != time.Monday {
		t.Fatalf("week begins %s, want Monday UTC", got)
	}
	if points[0].Value != "5" || points[1].Value != "10" {
		t.Fatalf("derived average fee points=%+v", points)
	}
}

func TestChooseIntervalEnforcesFiveHundredPointLimit(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if interval, err := chooseInterval(from, from.Add(500*time.Hour), IntervalAuto); err != nil || interval != IntervalHour {
		t.Fatalf("500-hour auto interval=%s error=%v", interval, err)
	}
	if _, err := chooseInterval(from, from.Add(501*time.Hour), IntervalHour); err == nil {
		t.Fatal("explicit 501-hour range was accepted")
	}
	if interval, err := chooseInterval(from, from.Add(501*time.Hour), IntervalAuto); err != nil || interval != IntervalDay {
		t.Fatalf("501-hour auto interval=%s error=%v", interval, err)
	}
}

func TestSummaryPreservesHugeExactValues(t *testing.T) {
	points := []Point{
		{Value: "115792089237316195423570985008687907853269984665640564039457584007913129639935"},
		{Value: "1"},
	}
	summary := summarize(points)
	if summary.Highest == nil || *summary.Highest != points[0].Value {
		t.Fatalf("highest=%v", summary.Highest)
	}
	if summary.Total == nil ||
		*summary.Total != "115792089237316195423570985008687907853269984665640564039457584007913129639936" {
		t.Fatalf("total=%v", summary.Total)
	}
}

func TestPercentChangePreservesFractionalAndSignedValues(t *testing.T) {
	t.Parallel()
	if change := percentChange("0.125", "0.1"); change == nil || *change != "25" {
		t.Fatalf("fractional increase=%v", change)
	}
	if change := percentChange("1", "3"); change == nil || *change != "-66.666667" {
		t.Fatalf("signed decrease=%v", change)
	}
	if change := percentChange("1", "0"); change != nil {
		t.Fatalf("zero previous window change=%v", change)
	}
}

func TestOverviewPreviewKeepsOnlySevenLatestCalendarBuckets(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	hours := make([]hourRow, 0, 8)
	for day := range 8 {
		hours = append(hours, testHour(start.AddDate(0, 0, day), "0", "0", 1, 1, 0))
	}
	points := aggregateHours(hours, MetricTransactions, IntervalDay, start.AddDate(0, 0, 8))
	if len(points) != 8 {
		t.Fatalf("fixture points=%d", len(points))
	}
	points = latestPoints(points, 7)
	if len(points) != 7 || !points[0].BucketStart.Equal(bucketStart(start.AddDate(0, 0, 1), IntervalDay)) {
		t.Fatalf("latest preview points=%+v", points)
	}
}

func testHour(
	start time.Time,
	fromBlock string,
	toBlock string,
	transactions int64,
	executionFee int64,
	intervalSamples int64,
) hourRow {
	zero := func() *big.Int { return new(big.Int) }
	return hourRow{
		Start: start, FromBlock: fromBlock, ToBlock: toBlock,
		BlockCount: 1, BlockIntervalSamples: intervalSamples,
		BaseFeeSamples: 1, BlobSamples: 0,
		TransactionCount: big.NewInt(transactions), FailedCount: zero(),
		CreationCount: zero(), GasUsed: zero(), GasLimit: big.NewInt(1),
		BlockInterval: big.NewInt(1), BaseFeeSum: zero(),
		ExecutionFees: big.NewInt(executionFee), PriorityFees: zero(),
		BurnedFees: zero(), BlobGasUsed: zero(), BlobBaseFeeSum: zero(),
		BlobBurnedFees: zero(), ERC20Transfers: zero(), NFTTransfers: zero(),
	}
}
