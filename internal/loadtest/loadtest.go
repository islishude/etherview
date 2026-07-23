// Package loadtest implements the repository's bounded HTTP load and soak
// driver. It deliberately uses only the public API and records enough context
// for a result to be reviewed without making the driver part of production.
package loadtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxResponseBytes  = 1 << 20
	maxScheduledCalls = 1_000_000
	loadDrainGrace    = time.Second
)

var errLoadAdmissionDropped = errors.New("load request dropped because all workers are saturated")

type Config struct {
	BaseURL                string
	Paths                  []string
	Rate                   int
	Duration               time.Duration
	Concurrency            int
	RequestTimeout         time.Duration
	MaximumP95             time.Duration
	MaximumErrorRate       float64
	MinimumThroughputRatio float64
	MaximumLag             uint64
	APIKey                 string
	Profile                string
	Revision               string
	Dataset                string
	Hardware               string
	RPCBehavior            string
	Client                 *http.Client
	Now                    func() time.Time
}

type Report struct {
	Profile             string         `json:"profile"`
	Revision            string         `json:"revision"`
	Dataset             string         `json:"dataset"`
	Hardware            string         `json:"hardware"`
	RPCBehavior         string         `json:"rpc_behavior"`
	BaseURL             string         `json:"base_url"`
	Paths               []string       `json:"paths"`
	StartedAt           time.Time      `json:"started_at"`
	TargetRate          int            `json:"target_rps"`
	DurationSeconds     float64        `json:"duration_seconds"`
	Requests            int64          `json:"requests"`
	Successes           int64          `json:"successes"`
	Errors              int64          `json:"errors"`
	ErrorRate           float64        `json:"error_rate"`
	ThroughputRPS       float64        `json:"throughput_rps"`
	P50Milliseconds     float64        `json:"p50_ms"`
	P95Milliseconds     float64        `json:"p95_ms"`
	P99Milliseconds     float64        `json:"p99_ms"`
	MaximumMilliseconds float64        `json:"max_ms"`
	CoreLag             uint64         `json:"core_lag_blocks"`
	CoreReady           bool           `json:"core_ready"`
	BackfillComplete    bool           `json:"backfill_complete"`
	StatusCounts        map[string]int `json:"status_counts"`
}

type runtimeStatus struct {
	lag              uint64
	coreReady        bool
	backfillComplete bool
}

type sample struct {
	latency time.Duration
	status  int
	err     error
}

type scheduleOutcome struct {
	dropped int64
	err     error
}

func Run(ctx context.Context, cfg Config) (Report, error) {
	base, err := validate(&cfg)
	if err != nil {
		return Report{}, err
	}
	runCtx, cancel := context.WithTimeout(
		ctx,
		cfg.Duration+2*cfg.RequestTimeout+loadDrainGrace,
	)
	defer cancel()
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	startedAt := now().UTC()
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: cfg.RequestTimeout}
	}
	boundedClient := *client
	boundedClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		// Public API routes are exact. Following redirects would both invalidate
		// their measurements and risk forwarding X-API-Key to another origin.
		return http.ErrUseLastResponse
	}
	client = &boundedClient
	total := int64(math.Ceil(float64(cfg.Rate) * cfg.Duration.Seconds()))
	// One queued admission per worker absorbs scheduler/worker rendezvous jitter
	// without turning the load generator into an unbounded backlog.
	jobs := make(chan int64, cfg.Concurrency)
	results := make(chan sample, cfg.Concurrency)
	var workers sync.WaitGroup
	for range cfg.Concurrency {
		workers.Go(func() {
			for job := range jobs {
				path := cfg.Paths[job%int64(len(cfg.Paths))]
				results <- request(runCtx, client, base, path, cfg.APIKey, cfg.RequestTimeout)
			}
		})
	}
	go func() {
		workers.Wait()
		close(results)
	}()

	started := time.Now()
	scheduleResults := make(chan scheduleOutcome, 1)
	go func() {
		dropped, scheduleErr := schedule(runCtx, started, cfg.Rate, total, jobs)
		scheduleResults <- scheduleOutcome{dropped: dropped, err: scheduleErr}
		close(jobs)
	}()
	samples := make([]sample, 0, int(total))
	for result := range results {
		samples = append(samples, result)
	}
	scheduled := <-scheduleResults
	for range scheduled.dropped {
		samples = append(samples, sample{err: errLoadAdmissionDropped})
	}
	elapsed := time.Since(started)
	status, statusErr := readRuntimeStatus(runCtx, client, base, cfg.APIKey, cfg.RequestTimeout)
	report := summarize(cfg, startedAt, elapsed, samples, status)
	return report, errors.Join(scheduled.err, statusErr, evaluate(cfg, report))
}

func validate(cfg *Config) (*url.URL, error) {
	if cfg == nil {
		return nil, errors.New("load configuration is nil")
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base == nil || (base.Scheme != "http" && base.Scheme != "https") ||
		base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" ||
		(base.Path != "" && base.Path != "/") {
		return nil, errors.New("load base URL must be an HTTP(S) origin without credentials, query, or fragment")
	}
	if len(cfg.Paths) == 0 || len(cfg.Paths) > 64 {
		return nil, errors.New("load path count must be between 1 and 64")
	}
	for _, path := range cfg.Paths {
		parsed, parseErr := url.ParseRequestURI(path)
		if parseErr != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") ||
			strings.Contains(path, "#") || parsed == nil || parsed.IsAbs() ||
			parsed.Host != "" || parsed.Fragment != "" {
			return nil, errors.New("load paths must be absolute same-origin request paths")
		}
		query, queryErr := url.ParseQuery(parsed.RawQuery)
		if queryErr != nil {
			return nil, errors.New("load paths must contain a valid query")
		}
		for name := range query {
			if strings.EqualFold(name, "apikey") {
				return nil, errors.New("load paths must not contain an apikey query credential")
			}
		}
	}
	if cfg.Rate < 1 || cfg.Rate > 100_000 {
		return nil, errors.New("load rate must be between 1 and 100000 requests per second")
	}
	if cfg.Duration < 100*time.Millisecond || cfg.Duration > 24*time.Hour {
		return nil, errors.New("load duration must be between 100ms and 24h")
	}
	if calls := math.Ceil(float64(cfg.Rate) * cfg.Duration.Seconds()); calls > maxScheduledCalls {
		return nil, fmt.Errorf("load profile exceeds the %d-request memory bound", maxScheduledCalls)
	}
	if cfg.Concurrency < 1 || cfg.Concurrency > 4_096 {
		return nil, errors.New("load concurrency must be between 1 and 4096")
	}
	if cfg.RequestTimeout < 50*time.Millisecond || cfg.RequestTimeout > 2*time.Minute {
		return nil, errors.New("load request timeout must be between 50ms and 2m")
	}
	if cfg.MaximumP95 <= 0 || cfg.MaximumP95 > 2*time.Minute {
		return nil, errors.New("maximum p95 must be between 1ns and 2m")
	}
	if math.IsNaN(cfg.MaximumErrorRate) || math.IsInf(cfg.MaximumErrorRate, 0) ||
		cfg.MaximumErrorRate < 0 || cfg.MaximumErrorRate > 1 {
		return nil, errors.New("maximum error rate must be between 0 and 1")
	}
	if math.IsNaN(cfg.MinimumThroughputRatio) || math.IsInf(cfg.MinimumThroughputRatio, 0) ||
		cfg.MinimumThroughputRatio <= 0 || cfg.MinimumThroughputRatio > 1 {
		return nil, errors.New("minimum throughput ratio must be greater than zero and at most 1")
	}
	if len(cfg.APIKey) > 4_096 {
		return nil, errors.New("load API key exceeds 4096 bytes")
	}
	return base, nil
}

func schedule(ctx context.Context, started time.Time, rate int, total int64, jobs chan<- int64) (int64, error) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	var dropped int64
	for index := int64(0); index < total; index++ {
		target := started.Add(time.Duration(index) * time.Second / time.Duration(rate))
		if wait := time.Until(target); wait > 0 {
			timer.Reset(wait)
			select {
			case <-ctx.Done():
				return dropped, ctx.Err()
			case <-timer.C:
			}
		}
		select {
		case <-ctx.Done():
			return dropped, ctx.Err()
		case jobs <- index:
		default:
			dropped++
		}
	}
	return dropped, nil
}

func request(
	ctx context.Context,
	client *http.Client,
	base *url.URL,
	path string,
	apiKey string,
	timeout time.Duration,
) sample {
	target, err := base.Parse(path)
	if err != nil {
		return sample{err: errors.New("resolve load request path")}
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target.String(), nil)
	if err != nil {
		return sample{err: errors.New("construct load request")}
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	started := time.Now()
	response, err := client.Do(req)
	if err != nil {
		return sample{latency: time.Since(started), err: errors.New("HTTP request failed")}
	}
	defer response.Body.Close() //nolint:errcheck
	read, copyErr := io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes+1))
	latency := time.Since(started)
	if copyErr != nil {
		return sample{latency: latency, status: response.StatusCode, err: errors.New("read HTTP response")}
	}
	if read > maxResponseBytes {
		return sample{latency: latency, status: response.StatusCode, err: errors.New("HTTP response exceeded load bound")}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return sample{latency: latency, status: response.StatusCode, err: errors.New("HTTP response was not successful")}
	}
	return sample{latency: latency, status: response.StatusCode}
}

func readRuntimeStatus(
	ctx context.Context,
	client *http.Client,
	base *url.URL,
	apiKey string,
	timeout time.Duration,
) (runtimeStatus, error) {
	target, err := base.Parse("/api/v1/status")
	if err != nil {
		return runtimeStatus{}, errors.New("resolve status path")
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target.String(), nil)
	if err != nil {
		return runtimeStatus{}, errors.New("construct status request")
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	response, err := client.Do(req)
	if err != nil {
		return runtimeStatus{}, errors.New("read status after load")
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return runtimeStatus{}, errors.New("status after load was not successful")
	}
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if readErr != nil {
		return runtimeStatus{}, errors.New("read status after load")
	}
	if len(payload) > maxResponseBytes {
		return runtimeStatus{}, errors.New("status after load exceeded response bound")
	}
	var envelope struct {
		Data *struct {
			Lag              json.RawMessage `json:"lag"`
			CoreReady        *bool           `json:"core_ready"`
			BackfillComplete *bool           `json:"backfill_complete"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return runtimeStatus{}, errors.New("decode status after load")
	}
	if envelope.Data == nil || envelope.Data.CoreReady == nil || envelope.Data.BackfillComplete == nil {
		return runtimeStatus{}, errors.New("status readiness fields are missing")
	}
	var text string
	if err := json.Unmarshal(envelope.Data.Lag, &text); err != nil ||
		text == "" || (len(text) > 1 && text[0] == '0') {
		return runtimeStatus{
			coreReady:        *envelope.Data.CoreReady,
			backfillComplete: *envelope.Data.BackfillComplete,
		}, errors.New("status lag is invalid")
	}
	lag, parseErr := strconv.ParseUint(text, 10, 64)
	if parseErr != nil {
		return runtimeStatus{
			coreReady:        *envelope.Data.CoreReady,
			backfillComplete: *envelope.Data.BackfillComplete,
		}, errors.New("status lag is invalid")
	}
	return runtimeStatus{
		lag:              lag,
		coreReady:        *envelope.Data.CoreReady,
		backfillComplete: *envelope.Data.BackfillComplete,
	}, nil
}

func summarize(cfg Config, startedAt time.Time, elapsed time.Duration, samples []sample, status runtimeStatus) Report {
	latencies := make([]time.Duration, 0, len(samples))
	statusCounts := make(map[string]int)
	var successes int64
	for _, item := range samples {
		latencies = append(latencies, item.latency)
		status := "transport"
		if item.status != 0 {
			status = strconv.Itoa(item.status)
		}
		statusCounts[status]++
		if item.err == nil {
			successes++
		}
	}
	slices.Sort(latencies)
	requests := int64(len(samples))
	failures := requests - successes
	errorRate := 0.0
	if requests != 0 {
		errorRate = float64(failures) / float64(requests)
	}
	throughput := 0.0
	if elapsed > 0 {
		// Admission drops and failed responses are not achieved read
		// throughput, even though they remain requests for the error-rate
		// denominator.
		throughput = float64(successes) / elapsed.Seconds()
	}
	return Report{
		Profile: cfg.Profile, Revision: cfg.Revision, Dataset: cfg.Dataset,
		Hardware: cfg.Hardware, RPCBehavior: cfg.RPCBehavior,
		BaseURL: strings.TrimSuffix(cfg.BaseURL, "/"), Paths: append([]string(nil), cfg.Paths...),
		StartedAt: startedAt, TargetRate: cfg.Rate, DurationSeconds: elapsed.Seconds(),
		Requests: requests, Successes: successes, Errors: failures,
		ErrorRate: errorRate, ThroughputRPS: throughput,
		P50Milliseconds:     durationMilliseconds(percentile(latencies, 0.50)),
		P95Milliseconds:     durationMilliseconds(percentile(latencies, 0.95)),
		P99Milliseconds:     durationMilliseconds(percentile(latencies, 0.99)),
		MaximumMilliseconds: durationMilliseconds(percentile(latencies, 1)),
		CoreLag:             status.lag,
		CoreReady:           status.coreReady,
		BackfillComplete:    status.backfillComplete,
		StatusCounts:        statusCounts,
	}
}

func evaluate(cfg Config, report Report) error {
	var errs []error
	if report.Requests == 0 {
		errs = append(errs, errors.New("load test completed without requests"))
	}
	if report.ErrorRate > cfg.MaximumErrorRate ||
		(cfg.MaximumErrorRate > 0 && cfg.MaximumErrorRate < 1 &&
			report.ErrorRate == cfg.MaximumErrorRate) {
		errs = append(errs, fmt.Errorf(
			"load error rate %.6f meets or exceeds %.6f", report.ErrorRate, cfg.MaximumErrorRate,
		))
	}
	if report.P95Milliseconds >= durationMilliseconds(cfg.MaximumP95) {
		errs = append(errs, fmt.Errorf(
			"load p95 %.3fms meets or exceeds %.3fms",
			report.P95Milliseconds,
			durationMilliseconds(cfg.MaximumP95),
		))
	}
	minimumThroughput := float64(cfg.Rate) * cfg.MinimumThroughputRatio
	if report.ThroughputRPS < minimumThroughput {
		errs = append(errs, fmt.Errorf(
			"load throughput %.3f rps is below %.3f rps", report.ThroughputRPS, minimumThroughput,
		))
	}
	if report.CoreLag > cfg.MaximumLag {
		errs = append(errs, fmt.Errorf(
			"core lag %d exceeds %d blocks", report.CoreLag, cfg.MaximumLag,
		))
	}
	if !report.CoreReady {
		errs = append(errs, errors.New("core is not ready after load"))
	}
	if !report.BackfillComplete {
		errs = append(errs, errors.New("backfill is not complete after load"))
	}
	return errors.Join(errs...)
}

func percentile(sorted []time.Duration, ratio float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(ratio*float64(len(sorted)))) - 1
	index = max(index, 0)
	index = min(index, len(sorted)-1)
	return sorted[index]
}

func durationMilliseconds(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}
