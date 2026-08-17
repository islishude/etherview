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

	"github.com/islishude/etherview/internal/apiops"
)

var requestDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
var proxyDetectionDurationBuckets = []float64{0.1, 0.5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000}

// Registry contains Etherview's bounded-cardinality operational metrics. It is
// safe for concurrent use and exports the Prometheus text format directly.
type Registry struct {
	mu sync.RWMutex

	version string
	role    string

	httpRequests                  map[requestKey]uint64
	httpDuration                  map[requestKey]*histogram
	httpPanics                    map[pair]uint64
	syncLag                       float64
	reorgDepth                    float64
	syncHalted                    map[string]float64
	rpcRequests                   map[pair]uint64
	jobsPending                   map[string]float64
	durableJobs                   map[pair]float64
	verificationCurrent           map[string]float64
	verificationCompilerAvailable map[string]float64
	verificationCompilerSeen      map[string]bool
	repairCurrent                 map[pair]float64
	repairOldestQueued            float64
	billingSettling               map[pair]float64
	durableSnapshotReady          bool
	metricsRefreshFailures        uint64
	metricsLastRefresh            float64
	enrichmentJobs                map[pair]uint64
	traceJobs                     map[string]uint64
	verifyJobs                    map[string]uint64
	metadata                      map[string]uint64
	maintenance                   map[pair]uint64
	analyticsRollups              map[string]uint64
	analyticsDirtyHours           float64
	analyticsOldestDirty          float64
	analyticsBackfill             float64
	rateLimits                    map[string]uint64
	x402Requests                  map[pair]uint64
	proxyDetectionDuration        *histogram
	proxyDetectionRPCCalls        map[string]uint64
	proxyDetectionRPCErrors       map[string]uint64
	proxyDetectionResults         map[proxyDetectionResultKey]uint64
	proxyDetectionAmbiguous       uint64
	proxyDetectionInconsistent    uint64
	safeProxyFingerprintMatches   uint64
	safeProxyCompatibleCandidates uint64
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

type proxyDetectionResultKey struct {
	Detector   string
	Family     string
	Status     string
	Confidence string
}

type histogram struct {
	Buckets []uint64
	Count   uint64
	Sum     float64
}

// NewRegistry constructs a process-local registry for one runtime role.
func NewRegistry(version, role string) *Registry {
	return &Registry{
		version:                       safeLabel(version),
		role:                          safeLabel(role),
		httpRequests:                  make(map[requestKey]uint64),
		httpDuration:                  make(map[requestKey]*histogram),
		httpPanics:                    make(map[pair]uint64),
		syncHalted:                    make(map[string]float64),
		rpcRequests:                   make(map[pair]uint64),
		jobsPending:                   make(map[string]float64),
		durableJobs:                   make(map[pair]float64),
		verificationCurrent:           make(map[string]float64),
		verificationCompilerAvailable: make(map[string]float64),
		verificationCompilerSeen:      make(map[string]bool),
		repairCurrent:                 make(map[pair]float64),
		billingSettling:               make(map[pair]float64),
		enrichmentJobs:                make(map[pair]uint64),
		traceJobs:                     make(map[string]uint64),
		verifyJobs:                    make(map[string]uint64),
		metadata:                      make(map[string]uint64),
		maintenance:                   make(map[pair]uint64),
		analyticsRollups:              make(map[string]uint64),
		rateLimits:                    make(map[string]uint64),
		x402Requests:                  make(map[pair]uint64),
		proxyDetectionDuration:        &histogram{Buckets: make([]uint64, len(proxyDetectionDurationBuckets))},
		proxyDetectionRPCCalls:        make(map[string]uint64),
		proxyDetectionRPCErrors:       make(map[string]uint64),
		proxyDetectionResults:         make(map[proxyDetectionResultKey]uint64),
	}
}

// ObserveProxyDetectionRun records one bounded detector-suite execution. RPC
// counts come from the block-pinned shared context, so memoized reads are not
// double counted across detectors.
func (registry *Registry) ObserveProxyDetectionRun(
	duration time.Duration,
	getCodeCalls, storageCalls, callCalls,
	getCodeErrors, storageErrors, callErrors uint64,
	ambiguous bool,
) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	durationMS := float64(duration) / float64(time.Millisecond)
	registry.proxyDetectionDuration.Count++
	registry.proxyDetectionDuration.Sum += durationMS
	for index, upperBound := range proxyDetectionDurationBuckets {
		if durationMS <= upperBound {
			registry.proxyDetectionDuration.Buckets[index]++
		}
	}
	registry.proxyDetectionRPCCalls["eth_getCode"] += getCodeCalls
	registry.proxyDetectionRPCCalls["eth_getStorageAt"] += storageCalls
	registry.proxyDetectionRPCCalls["eth_call"] += callCalls
	registry.proxyDetectionRPCErrors["eth_getCode"] += getCodeErrors
	registry.proxyDetectionRPCErrors["eth_getStorageAt"] += storageErrors
	registry.proxyDetectionRPCErrors["eth_call"] += callErrors
	if ambiguous {
		registry.proxyDetectionAmbiguous++
	}
}

// RecordProxyDetectionResult records only the detector protocol's closed label
// vocabulary. Unexpected future values collapse to "other".
func (registry *Registry) RecordProxyDetectionResult(detector, family, status, confidence string) {
	detector = boundedProxyDetector(detector)
	family = boundedProxyFamily(family)
	status = boundedProxyDetectionStatus(status)
	confidence = boundedProxyConfidence(confidence)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.proxyDetectionResults[proxyDetectionResultKey{
		Detector: detector, Family: family, Status: status, Confidence: confidence,
	}]++
	if status == "inconsistent" {
		registry.proxyDetectionInconsistent++
	}
	if detector == "safe" && family == "safe" &&
		(status == "confirmed" || status == "inconsistent" || status == "unknown") {
		registry.safeProxyFingerprintMatches++
	}
	if detector == "safe" && family == "safe" && status == "candidate" && confidence == "medium" {
		registry.safeProxyCompatibleCandidates++
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

// SetVerificationCompilerAvailable records one bounded compiler family's
// runtime and catalog availability.
func (registry *Registry) SetVerificationCompilerAvailable(family string, available bool) {
	if family != "solcjs" && family != "geas" {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.verificationCompilerSeen[family] = true
	registry.verificationCompilerAvailable[family] = 0
	if available {
		registry.verificationCompilerAvailable[family] = 1
	}
}

// RecordMetadataFetch increments a normalized metadata-fetch result.
func (registry *Registry) RecordMetadataFetch(result string) {
	registry.increment(registry.metadata, boundedJobResult(result))
}

// RecordMaintenanceRequest records only controlled operation/result values.
func (registry *Registry) RecordMaintenanceRequest(operation, result string) {
	registry.incrementPair(registry.maintenance, boundedMaintenanceOperation(operation), boundedJobResult(result))
}

// RecordAnalyticsRollup records a bounded recompute outcome.
func (registry *Registry) RecordAnalyticsRollup(result string) {
	switch result {
	case "succeeded", "retry", "failed":
	default:
		result = "failed"
	}
	registry.increment(registry.analyticsRollups, result)
}

// SetAnalyticsRollupState updates writer-backed queue and backfill gauges.
func (registry *Registry) SetAnalyticsRollupState(
	dirtyHours int64,
	oldestDirtySeconds float64,
	backfillProgress float64,
) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.analyticsDirtyHours = max(0, float64(dirtyHours))
	registry.analyticsOldestDirty = max(0, oldestDirtySeconds)
	registry.analyticsBackfill = min(100, max(0, backfillProgress))
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

// ObserveX402Request records one terminal process-local billing outcome. Both
// labels are closed over the static eligible-operation catalog and a fixed
// result vocabulary; payer, payment, resource, and remote error values cannot
// become metric labels.
func (registry *Registry) ObserveX402Request(operation, result string) {
	registry.incrementPair(
		registry.x402Requests,
		boundedBillingOperation(operation),
		boundedBillingResult(result),
	)
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
	writeHelp(&output, "etherview_verification_compiler_available", "Whether one API verification compiler family has a validated executor runtime and any required fresh catalog.", "gauge")
	for _, family := range []string{"geas", "solcjs"} {
		if registry.verificationCompilerSeen[family] {
			fmt.Fprintf(&output, "etherview_verification_compiler_available{family=%s} %s\n", quote(family), formatFloat(registry.verificationCompilerAvailable[family]))
		}
	}
	writePairGauges(&output, "etherview_repair_requests", "Active repair and reindex backlog grouped by operation and status.", "operation", "status", registry.repairCurrent)
	writePairGauges(&output, "etherview_x402_stale_settling_payments", "Writer-backed x402 settlements requiring operator reconciliation.", "operation", "reason", registry.billingSettling)
	writeHelp(&output, "etherview_repair_oldest_queued_seconds", "Age of the oldest queued repair or reindex request.", "gauge")
	if registry.durableSnapshotReady {
		fmt.Fprintf(&output, "etherview_repair_oldest_queued_seconds %s\n", formatFloat(registry.repairOldestQueued))
	}
	writeHelp(&output, "etherview_observability_last_refresh_timestamp_seconds", "Unix timestamp of the last successful PostgreSQL metric refresh.", "gauge")
	fmt.Fprintf(&output, "etherview_observability_last_refresh_timestamp_seconds %s\n", formatFloat(registry.metricsLastRefresh))
	writeHelp(&output, "etherview_observability_refresh_failures_total", "Failed PostgreSQL metric refresh attempts.", "counter")
	fmt.Fprintf(&output, "etherview_observability_refresh_failures_total %d\n", registry.metricsRefreshFailures)
	writePairCounters(&output, "etherview_enrichment_jobs_total", "Enrichment job attempts grouped by stage and result.", "stage", "result", registry.enrichmentJobs)
	writeHelp(&output, "etherview_proxy_detection_duration_ms", "Evidence-based proxy detector-suite duration in milliseconds.", "histogram")
	for index, upperBound := range proxyDetectionDurationBuckets {
		fmt.Fprintf(&output, "etherview_proxy_detection_duration_ms_bucket{le=%s} %d\n",
			quote(formatFloat(upperBound)), registry.proxyDetectionDuration.Buckets[index])
	}
	fmt.Fprintf(&output, "etherview_proxy_detection_duration_ms_bucket{le=\"+Inf\"} %d\n", registry.proxyDetectionDuration.Count)
	fmt.Fprintf(&output, "etherview_proxy_detection_duration_ms_sum %s\n", formatFloat(registry.proxyDetectionDuration.Sum))
	fmt.Fprintf(&output, "etherview_proxy_detection_duration_ms_count %d\n", registry.proxyDetectionDuration.Count)
	writeCounters(&output, "etherview_proxy_detection_rpc_calls_total", "Block-pinned RPC calls made by proxy detection V2.", "method", registry.proxyDetectionRPCCalls)
	writeCounters(&output, "etherview_proxy_detection_rpc_errors_total", "Block-pinned RPC transport errors observed by proxy detection V2.", "method", registry.proxyDetectionRPCErrors)
	writeProxyDetectionResults(&output, registry.proxyDetectionResults)
	writeHelp(&output, "etherview_proxy_detection_ambiguous_total", "Proxy resolutions with conflicting detector outcomes.", "counter")
	fmt.Fprintf(&output, "etherview_proxy_detection_ambiguous_total %d\n", registry.proxyDetectionAmbiguous)
	writeHelp(&output, "etherview_proxy_detection_inconsistent_total", "Inconsistent detector outcomes.", "counter")
	fmt.Fprintf(&output, "etherview_proxy_detection_inconsistent_total %d\n", registry.proxyDetectionInconsistent)
	writeHelp(&output, "etherview_safe_proxy_fingerprint_match_total", "Canonical Safe runtime fingerprint matches.", "counter")
	fmt.Fprintf(&output, "etherview_safe_proxy_fingerprint_match_total %d\n", registry.safeProxyFingerprintMatches)
	writeHelp(&output, "etherview_safe_proxy_compatible_candidate_total", "Unknown-runtime Safe-compatible candidates.", "counter")
	fmt.Fprintf(&output, "etherview_safe_proxy_compatible_candidate_total %d\n", registry.safeProxyCompatibleCandidates)
	writeCounters(&output, "etherview_trace_jobs_total", "Trace jobs grouped by result.", "result", registry.traceJobs)
	writeCounters(&output, "etherview_verification_jobs_total", "Verification jobs grouped by result.", "result", registry.verifyJobs)
	writeCounters(&output, "etherview_metadata_fetches_total", "Metadata fetches grouped by result, including SSRF rejection.", "result", registry.metadata)
	writePairCounters(&output, "etherview_maintenance_requests_total", "Repair and reindex executions grouped by operation and result.", "operation", "result", registry.maintenance)
	writeCounters(&output, "etherview_analytics_rollup_recomputes_total", "Historical analytics rollup recomputes grouped by outcome.", "result", registry.analyticsRollups)
	writeHelp(&output, "etherview_analytics_dirty_hours", "UTC hour buckets waiting for canonical analytics recompute.", "gauge")
	fmt.Fprintf(&output, "etherview_analytics_dirty_hours %s\n", formatFloat(registry.analyticsDirtyHours))
	writeHelp(&output, "etherview_analytics_oldest_dirty_seconds", "Age of the oldest UTC analytics bucket waiting for recompute.", "gauge")
	fmt.Fprintf(&output, "etherview_analytics_oldest_dirty_seconds %s\n", formatFloat(registry.analyticsOldestDirty))
	writeHelp(&output, "etherview_analytics_backfill_percent", "Canonical block source publication progress for historical analytics.", "gauge")
	fmt.Fprintf(&output, "etherview_analytics_backfill_percent %s\n", formatFloat(registry.analyticsBackfill))
	writeCounters(&output, "etherview_rate_limit_decisions_total", "Rate limit decisions grouped by outcome.", "decision", registry.rateLimits)
	writePairCounters(&output, "etherview_x402_requests_total", "x402 request attempts grouped by eligible operation and terminal outcome.", "operation", "result", registry.x402Requests)
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

func writeProxyDetectionResults(output *strings.Builder, values map[proxyDetectionResultKey]uint64) {
	writeHelp(output, "etherview_proxy_detection_results_total", "Evidence-based proxy detector outcomes.", "counter")
	keys := make([]proxyDetectionResultKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := keys[i].Detector + "\x00" + keys[i].Family + "\x00" + keys[i].Status + "\x00" + keys[i].Confidence
		right := keys[j].Detector + "\x00" + keys[j].Family + "\x00" + keys[j].Status + "\x00" + keys[j].Confidence
		return left < right
	})
	for _, key := range keys {
		fmt.Fprintf(output, "etherview_proxy_detection_results_total{detector=%s,family=%s,status=%s,confidence=%s} %d\n",
			quote(key.Detector), quote(key.Family), quote(key.Status), quote(key.Confidence), values[key])
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
	case "proxy", "proxy@1", "proxy@2":
		return "proxy"
	case "abi", "abi@1", "abi@2", "abi@3", "abi@4":
		return "abi"
	case "token", "token@1":
		return "token"
	case "stats", "stats@2", "stats@3":
		return "stats"
	case "trace", "trace@1", "trace@2", "trace@3":
		return "trace"
	case "state_diff", "state_diff@1", "state_diff@2", "state_diff@3":
		return "state_diff"
	case "nft-metadata", "verification":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func boundedProxyDetector(value string) string {
	switch strings.TrimSpace(value) {
	case "openzeppelin", "safe":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func boundedProxyFamily(value string) string {
	switch strings.TrimSpace(value) {
	case "erc1167", "erc1967", "safe", "custom":
		return strings.TrimSpace(value)
	case "":
		return "none"
	default:
		return "other"
	}
}

func boundedProxyDetectionStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "confirmed", "candidate", "inconsistent", "not-detected", "unknown":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func boundedProxyConfidence(value string) string {
	switch strings.TrimSpace(value) {
	case "high", "medium", "low":
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
	case "succeeded", "failed", "unavailable", "retry", "error", "timeout", "resource_exhausted", "ssrf_rejected", "stale_target", "superseded":
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

func boundedBillingOperation(value string) string {
	operation, ok := apiops.Lookup(strings.TrimSpace(value))
	if !ok || !operation.BillingEligible {
		return "other"
	}
	return string(operation.ID)
}

func boundedBillingResult(value string) string {
	switch strings.TrimSpace(value) {
	case "required", "invalid", "binding_conflict", "replayed",
		"verify_rejected", "verify_unavailable", "handler_non_success",
		"handler_failed", "ledger_unavailable", "settle_rejected",
		"settlement_unknown", "settled":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func boundedBillingSettlingReason(value string) string {
	switch strings.TrimSpace(value) {
	case "settlement_unknown", "unmarked_after_timeout":
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
