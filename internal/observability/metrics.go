package observability

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var requestDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Registry contains Etherview's bounded-cardinality operational metrics. It is
// safe for concurrent use and exports the Prometheus text format directly.
type Registry struct {
	mu sync.RWMutex

	version string
	role    string

	httpRequests           map[requestKey]uint64
	httpDuration           map[requestKey]*histogram
	httpPanics             map[pair]uint64
	syncLag                float64
	reorgDepth             float64
	syncHalted             map[string]float64
	rpcRequests            map[pair]uint64
	jobsPending            map[string]float64
	durableJobs            map[pair]float64
	verificationCurrent    map[string]float64
	repairCurrent          map[pair]float64
	repairOldestQueued     float64
	durableSnapshotReady   bool
	metricsRefreshFailures uint64
	metricsLastRefresh     float64
	enrichmentJobs         map[pair]uint64
	traceJobs              map[string]uint64
	verifyJobs             map[string]uint64
	metadata               map[string]uint64
	maintenance            map[pair]uint64
	rateLimits             map[string]uint64
}

type requestKey struct {
	Method string
	Route  string
	Status int
}

type pair struct {
	First  string
	Second string
}

type histogram struct {
	Buckets []uint64
	Count   uint64
	Sum     float64
}

// NewRegistry constructs a process-local registry for one runtime role.
func NewRegistry(version, role string) *Registry {
	return &Registry{
		version:             safeLabel(version),
		role:                safeLabel(role),
		httpRequests:        make(map[requestKey]uint64),
		httpDuration:        make(map[requestKey]*histogram),
		httpPanics:          make(map[pair]uint64),
		syncHalted:          make(map[string]float64),
		rpcRequests:         make(map[pair]uint64),
		jobsPending:         make(map[string]float64),
		durableJobs:         make(map[pair]float64),
		verificationCurrent: make(map[string]float64),
		repairCurrent:       make(map[pair]float64),
		enrichmentJobs:      make(map[pair]uint64),
		traceJobs:           make(map[string]uint64),
		verifyJobs:          make(map[string]uint64),
		metadata:            make(map[string]uint64),
		maintenance:         make(map[pair]uint64),
		rateLimits:          make(map[string]uint64),
	}
}

// RecordHTTPPanic increments a bounded handler-panic signal independently of
// the response status already committed to the client.
func (registry *Registry) RecordHTTPPanic(method, route string) {
	registry.incrementPair(registry.httpPanics, boundedMethod(method), boundedRoute(route))
}

// ObserveHTTPRequest records one completed HTTP request.
func (registry *Registry) ObserveHTTPRequest(method, route string, status int, duration time.Duration) {
	key := requestKey{Method: boundedMethod(method), Route: boundedRoute(route), Status: status}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.httpRequests[key]++
	histogramValue := registry.httpDuration[key]
	if histogramValue == nil {
		histogramValue = &histogram{Buckets: make([]uint64, len(requestDurationBuckets))}
		registry.httpDuration[key] = histogramValue
	}
	seconds := duration.Seconds()
	histogramValue.Count++
	histogramValue.Sum += seconds
	for index, upperBound := range requestDurationBuckets {
		if seconds <= upperBound {
			histogramValue.Buckets[index]++
		}
	}
}

// SetSyncLag updates the difference between the upstream and indexed heads.
func (registry *Registry) SetSyncLag(blocks uint64) {
	registry.mu.Lock()
	registry.syncLag = float64(blocks)
	registry.mu.Unlock()
}

// ObserveReorg records the depth of the most recently observed reorganization.
func (registry *Registry) ObserveReorg(depth uint64) {
	registry.mu.Lock()
	registry.reorgDepth = float64(depth)
	registry.mu.Unlock()
}

// RecordSyncHalt exposes a fatal canonical-safety stop as a persistent gauge.
// The sync service remains halted until operator cancellation/restart, which
// keeps this signal scrapeable instead of exiting before Prometheus can see it.
func (registry *Registry) RecordSyncHalt(reason string) {
	registry.mu.Lock()
	registry.syncHalted[boundedSyncHaltReason(reason)] = 1
	registry.mu.Unlock()
}

// RecordRPC increments an RPC outcome for a bounded architectural purpose.
func (registry *Registry) RecordRPC(purpose, result string) {
	registry.incrementPair(registry.rpcRequests, boundedRPCPurpose(purpose), boundedRPCResult(result))
}

// SetJobsPending updates the durable PostgreSQL backlog for a named queue.
func (registry *Registry) SetJobsPending(queue string, count uint64) {
	registry.mu.Lock()
	registry.jobsPending[boundedJobStage(queue)] = float64(count)
	registry.mu.Unlock()
}

// RecordEnrichmentJob increments a bounded stage/result outcome. Trace stages
// also retain the dedicated compatibility series used by existing alerts.
func (registry *Registry) RecordEnrichmentJob(stage, result string) {
	stage = boundedJobStage(stage)
	result = boundedJobResult(result)
	registry.incrementPair(registry.enrichmentJobs, stage, result)
	if stage == "trace" {
		registry.RecordTraceJob(result)
	}
}

// RecordTraceJob increments a normalized trace-job result.
func (registry *Registry) RecordTraceJob(result string) {
	registry.increment(registry.traceJobs, boundedJobResult(result))
}

// RecordVerificationJob increments a normalized verification-job result.
func (registry *Registry) RecordVerificationJob(result string) {
	registry.increment(registry.verifyJobs, boundedJobResult(result))
}

// RecordMetadataFetch increments a normalized metadata-fetch result.
func (registry *Registry) RecordMetadataFetch(result string) {
	registry.increment(registry.metadata, boundedJobResult(result))
}

// RecordMaintenanceRequest records only controlled operation/result values.
func (registry *Registry) RecordMaintenanceRequest(operation, result string) {
	registry.incrementPair(registry.maintenance, boundedMaintenanceOperation(operation), boundedJobResult(result))
}

// RecordMetricsRefreshFailure exposes PostgreSQL scrape-state loss without
// clearing the last successful snapshot and fabricating a healthy zero.
func (registry *Registry) RecordMetricsRefreshFailure() {
	registry.mu.Lock()
	registry.metricsRefreshFailures++
	registry.mu.Unlock()
}

// RecordRateLimit increments an allow or reject rate-limit decision.
func (registry *Registry) RecordRateLimit(decision string) {
	registry.increment(registry.rateLimits, boundedRateDecision(decision))
}

func (registry *Registry) increment(values map[string]uint64, label string) {
	registry.mu.Lock()
	values[safeLabel(label)]++
	registry.mu.Unlock()
}

func (registry *Registry) incrementPair(values map[pair]uint64, first, second string) {
	registry.mu.Lock()
	values[pair{First: safeLabel(first), Second: safeLabel(second)}]++
	registry.mu.Unlock()
}

// Handler returns a Prometheus text-exposition HTTP handler. Callers should
// mount it at GET /metrics on the operational listener.
func (registry *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			response.Header().Set("Allow", "GET, HEAD")
			http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		response.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		response.Header().Set("Cache-Control", "no-store")
		if request.Method == http.MethodHead {
			response.WriteHeader(http.StatusOK)
			return
		}
		_, _ = io.WriteString(response, registry.Gather())
	})
}

// Gather produces a deterministic Prometheus text exposition snapshot.
func (registry *Registry) Gather() string {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	var output strings.Builder
	writeHelp(&output, "etherview_build_info", "Static build and runtime role information.", "gauge")
	fmt.Fprintf(&output, "etherview_build_info{role=%s,version=%s} 1\n", quote(registry.role), quote(registry.version))
	writeHelp(&output, "etherview_sync_lag_blocks", "Difference between upstream and indexed canonical head.", "gauge")
	fmt.Fprintf(&output, "etherview_sync_lag_blocks %s\n", formatFloat(registry.syncLag))
	writeHelp(&output, "etherview_reorg_depth_blocks", "Depth of the most recently observed reorganization.", "gauge")
	fmt.Fprintf(&output, "etherview_reorg_depth_blocks %s\n", formatFloat(registry.reorgDepth))
	writeGaugeMap(&output, "etherview_sync_halted", "Whether core sync is halted on a fatal canonical-safety error.", "reason", registry.syncHalted)

	writeHelp(&output, "etherview_http_requests_total", "Completed HTTP requests.", "counter")
	requestKeys := sortedRequestKeys(registry.httpRequests)
	for _, key := range requestKeys {
		fmt.Fprintf(&output, "etherview_http_requests_total{method=%s,route=%s,status=%s} %d\n",
			quote(key.Method), quote(key.Route), quote(strconv.Itoa(key.Status)), registry.httpRequests[key])
	}
	writeHelp(&output, "etherview_http_request_duration_seconds", "HTTP request duration.", "histogram")
	for _, key := range requestKeys {
		value := registry.httpDuration[key]
		labels := fmt.Sprintf("method=%s,route=%s,status=%s", quote(key.Method), quote(key.Route), quote(strconv.Itoa(key.Status)))
		for index, upperBound := range requestDurationBuckets {
			fmt.Fprintf(&output, "etherview_http_request_duration_seconds_bucket{%s,le=%s} %d\n", labels, quote(formatFloat(upperBound)), value.Buckets[index])
		}
		fmt.Fprintf(&output, "etherview_http_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", labels, value.Count)
		fmt.Fprintf(&output, "etherview_http_request_duration_seconds_sum{%s} %s\n", labels, formatFloat(value.Sum))
		fmt.Fprintf(&output, "etherview_http_request_duration_seconds_count{%s} %d\n", labels, value.Count)
	}
	writePairCounters(&output, "etherview_http_panics_total", "Recovered HTTP handler panics grouped by bounded method and route.", "method", "route", registry.httpPanics)

	writePairCounters(&output, "etherview_rpc_requests_total", "RPC calls grouped by purpose and result.", "purpose", "result", registry.rpcRequests)
	writeGaugeMap(&output, "etherview_jobs_pending", "Durable PostgreSQL jobs waiting by queue.", "queue", registry.jobsPending)
	writePairGauges(&output, "etherview_durable_jobs", "Active durable PostgreSQL backlog grouped by stage and status.", "stage", "status", registry.durableJobs)
	writeGaugeMap(&output, "etherview_verification_jobs", "Active verification backlog grouped by status.", "status", registry.verificationCurrent)
	writePairGauges(&output, "etherview_repair_requests", "Active repair and reindex backlog grouped by operation and status.", "operation", "status", registry.repairCurrent)
	writeHelp(&output, "etherview_repair_oldest_queued_seconds", "Age of the oldest queued repair or reindex request.", "gauge")
	if registry.durableSnapshotReady {
		fmt.Fprintf(&output, "etherview_repair_oldest_queued_seconds %s\n", formatFloat(registry.repairOldestQueued))
	}
	writeHelp(&output, "etherview_observability_last_refresh_timestamp_seconds", "Unix timestamp of the last successful PostgreSQL metric refresh.", "gauge")
	fmt.Fprintf(&output, "etherview_observability_last_refresh_timestamp_seconds %s\n", formatFloat(registry.metricsLastRefresh))
	writeHelp(&output, "etherview_observability_refresh_failures_total", "Failed PostgreSQL metric refresh attempts.", "counter")
	fmt.Fprintf(&output, "etherview_observability_refresh_failures_total %d\n", registry.metricsRefreshFailures)
	writePairCounters(&output, "etherview_enrichment_jobs_total", "Enrichment job attempts grouped by stage and result.", "stage", "result", registry.enrichmentJobs)
	writeCounters(&output, "etherview_trace_jobs_total", "Trace jobs grouped by result.", "result", registry.traceJobs)
	writeCounters(&output, "etherview_verification_jobs_total", "Verification jobs grouped by result.", "result", registry.verifyJobs)
	writeCounters(&output, "etherview_metadata_fetches_total", "Metadata fetches grouped by result, including SSRF rejection.", "result", registry.metadata)
	writePairCounters(&output, "etherview_maintenance_requests_total", "Repair and reindex executions grouped by operation and result.", "operation", "result", registry.maintenance)
	writeCounters(&output, "etherview_rate_limit_decisions_total", "Rate limit decisions grouped by outcome.", "decision", registry.rateLimits)
	return output.String()
}

func writeHelp(output *strings.Builder, name, help, metricType string) {
	fmt.Fprintf(output, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, metricType)
}

func writeCounters(output *strings.Builder, name, help, labelName string, values map[string]uint64) {
	writeHelp(output, name, help, "counter")
	for _, label := range sortedKeys(values) {
		fmt.Fprintf(output, "%s{%s=%s} %d\n", name, labelName, quote(label), values[label])
	}
}

func writePairCounters(output *strings.Builder, name, help, firstLabel, secondLabel string, values map[pair]uint64) {
	writeHelp(output, name, help, "counter")
	keys := make([]pair, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].First == keys[j].First {
			return keys[i].Second < keys[j].Second
		}
		return keys[i].First < keys[j].First
	})
	for _, key := range keys {
		fmt.Fprintf(output, "%s{%s=%s,%s=%s} %d\n", name, firstLabel, quote(key.First), secondLabel, quote(key.Second), values[key])
	}
}

func writeGaugeMap(output *strings.Builder, name, help, labelName string, values map[string]float64) {
	writeHelp(output, name, help, "gauge")
	for _, label := range sortedKeys(values) {
		fmt.Fprintf(output, "%s{%s=%s} %s\n", name, labelName, quote(label), formatFloat(values[label]))
	}
}

func writePairGauges(output *strings.Builder, name, help, firstLabel, secondLabel string, values map[pair]float64) {
	writeHelp(output, name, help, "gauge")
	keys := make([]pair, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].First == keys[j].First {
			return keys[i].Second < keys[j].Second
		}
		return keys[i].First < keys[j].First
	})
	for _, key := range keys {
		fmt.Fprintf(output, "%s{%s=%s,%s=%s} %s\n", name, firstLabel, quote(key.First), secondLabel, quote(key.Second), formatFloat(values[key]))
	}
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedRequestKeys(values map[requestKey]uint64) []requestKey {
	keys := make([]requestKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Route != keys[j].Route {
			return keys[i].Route < keys[j].Route
		}
		if keys[i].Method != keys[j].Method {
			return keys[i].Method < keys[j].Method
		}
		return keys[i].Status < keys[j].Status
	})
	return keys
}

func boundedMethod(method string) string {
	method = strings.ToUpper(method)
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return method
	default:
		return "OTHER"
	}
}

func boundedRoute(route string) string {
	route = strings.TrimSpace(route)
	if route == "unmatched" || route == "method_not_allowed" {
		return route
	}
	if route == "" || len(route) > 160 || !strings.HasPrefix(route, "/") {
		return "unmatched"
	}
	return route
}

func safeLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	if len(value) > 96 {
		return value[:96]
	}
	return value
}

func boundedJobStage(value string) string {
	switch strings.TrimSpace(value) {
	case "proxy", "proxy@1":
		return "proxy"
	case "abi", "abi@1":
		return "abi"
	case "token", "token@1":
		return "token"
	case "stats", "stats@2":
		return "stats"
	case "trace", "trace@1":
		return "trace"
	case "nft-metadata", "verification":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func boundedJobStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "queued", "leased", "running", "succeeded", "done", "failed", "cancelled":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func boundedJobResult(value string) string {
	switch strings.TrimSpace(value) {
	case "succeeded", "failed", "unavailable", "retry", "error", "timeout", "resource_exhausted", "ssrf_rejected", "stale_target":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func boundedMaintenanceOperation(value string) string {
	switch strings.TrimSpace(value) {
	case "repair", "reindex":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func boundedRPCPurpose(value string) string {
	switch strings.TrimSpace(value) {
	case "head", "history", "state", "trace", "mempool":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func boundedRPCResult(value string) string {
	switch strings.TrimSpace(value) {
	case "success", "error":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func boundedRateDecision(value string) string {
	switch strings.TrimSpace(value) {
	case "allowed", "rejected":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func boundedSyncHaltReason(value string) string {
	switch strings.TrimSpace(value) {
	case "finalized_reorg", "reorg_too_deep", "no_common_ancestor", "source_inconsistent", "sync_cycle_failed":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func quote(value string) string {
	return strconv.Quote(value)
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}
