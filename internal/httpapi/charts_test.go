package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/analytics"
	"github.com/islishude/etherview/internal/config"
)

type fakeAnalyticsReader struct {
	overview analytics.Overview
	series   analytics.Series
	err      error
	request  analytics.DetailRequest
}

func (fake *fakeAnalyticsReader) Overview(context.Context, string, time.Time) (analytics.Overview, error) {
	return fake.overview, fake.err
}

func (fake *fakeAnalyticsReader) Detail(
	_ context.Context,
	request analytics.DetailRequest,
) (analytics.Series, error) {
	fake.request = request
	return fake.series, fake.err
}

func testChartsHandler(t *testing.T, reader AnalyticsReader) http.Handler {
	t.Helper()
	cfg := config.Default()
	cfg.Chain.ID = 1
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{}, Catalog: &fakeCatalog{}, Analytics: reader,
		Now:       func() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) },
		RequestID: func() string { return "charts-request" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestChartMetricNormalizesParametersAndUsesGeneratedEnvelope(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	value := "115792089237316195423570985008687907853269984665640564039457584007913129639935"
	fake := &fakeAnalyticsReader{series: analytics.Series{
		Metric: analytics.MetricExecutionFees, Interval: analytics.IntervalHour,
		FromTime: from, ToTime: to,
		Points: []analytics.Point{{
			BucketStart: from, BucketEnd: from.Add(time.Hour), Value: value,
			FromBlock: "99", ToBlock: "100",
		}},
		Summary:  analytics.Summary{Current: &value, Highest: &value, Lowest: &value, Total: &value, Average: &value},
		Snapshot: analytics.Snapshot{ChainID: "1", BlockNumber: "100", BlockHash: "0x" + string(make([]byte, 64))},
		Coverage: analytics.Coverage{Complete: true, DirtyHours: "0", Progress: "100"},
	}}
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/stats/charts/execution-fees?from_time=2026-07-27T00:00:00Z&to_time=2026-07-28T00:00:00Z&interval=hour",
		nil,
	)
	response := httptest.NewRecorder()
	testChartsHandler(t, fake).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.request.Metric != analytics.MetricExecutionFees ||
		fake.request.Interval != analytics.IntervalHour ||
		!fake.request.From.Equal(from) || !fake.request.To.Equal(to) {
		t.Fatalf("detail request=%+v", fake.request)
	}
	var envelope struct {
		Data struct {
			Points []struct {
				Value string `json:"value"`
			} `json:"points"`
			Coverage struct {
				Progress string `json:"backfill_progress"`
			} `json:"coverage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Points) != 1 || envelope.Data.Points[0].Value != value ||
		envelope.Data.Coverage.Progress != "100" {
		t.Fatalf("response=%+v", envelope)
	}
}

func TestChartMetricRejectsUnknownMetricAndInvalidRange(t *testing.T) {
	t.Parallel()
	handler := testChartsHandler(t, &fakeAnalyticsReader{})
	for _, path := range []string{
		"/api/v1/stats/charts/not-a-metric?from_time=2026-07-27T00:00:00Z&to_time=2026-07-28T00:00:00Z",
		"/api/v1/stats/charts/transactions?from_time=nope&to_time=2026-07-28T00:00:00Z",
		"/api/v1/stats/charts/transactions?from_time=2026-07-27T00:00:00Z&to_time=2026-07-28T00:00:00Z&interval=minute",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestChartMetricReturnsExplicitPendingCoverage(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	fake := &fakeAnalyticsReader{err: analytics.PendingError{Coverage: analytics.Coverage{
		AvailableFrom: &from, AvailableTo: &from, DirtyHours: "3",
		Progress: "37.5",
	}}}
	response := httptest.NewRecorder()
	testChartsHandler(t, fake).ServeHTTP(response, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/stats/charts/transactions?from_time=2026-07-27T00:00:00Z&to_time=2026-07-28T00:00:00Z",
		nil,
	))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Coverage struct {
					Progress string `json:"backfill_progress"`
				} `json:"coverage"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "analytics_pending" || body.Error.Details.Coverage.Progress != "37.5" {
		t.Fatalf("body=%s", response.Body.String())
	}
	if !errors.Is(fake.err, analytics.ErrPending) {
		t.Fatal("pending error lost its stable classification")
	}
}
