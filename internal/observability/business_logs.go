package observability

import (
	"context"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/derivedverify"
	"github.com/islishude/etherview/internal/enrich"
	ensresolver "github.com/islishude/etherview/internal/ens"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/maintenance"
	"github.com/islishude/etherview/internal/metadata"
	"github.com/islishude/etherview/internal/netpolicy"
	"github.com/islishude/etherview/internal/verify"
)

// BusinessObserver keeps the existing bounded metrics and emits one structured
// log only after a worker reports a durable business transition.
type BusinessObserver struct {
	registry *Registry
	logger   *slog.Logger
}

func (observer *BusinessObserver) RecordRPC(observation ethrpc.Observation) {
	if observer != nil && observer.registry != nil {
		for range max(observation.SuccessCount, 0) {
			observer.registry.RecordRPC(string(observation.Purpose), "success")
		}
		for range max(observation.ErrorCount, 0) {
			observer.registry.RecordRPC(string(observation.Purpose), "error")
		}
	}
	logger := slog.Default()
	if observer != nil && observer.logger != nil {
		logger = observer.logger
	}
	logger.Debug("RPC request completed",
		"event", "rpc_request_completed", "component", "rpc-client",
		"rpc", slog.GroupValue(
			slog.String("endpoint", boundedMetadataText(observation.Endpoint, 128)),
			slog.String("purpose", boundedRPCPurpose(string(observation.Purpose))),
			slog.String("method", boundedRPCMethod(observation.Method)),
			slog.Int("batch_size", max(observation.BatchSize, 0)),
			slog.Int("success_count", max(observation.SuccessCount, 0)),
			slog.Int("error_count", max(observation.ErrorCount, 0)),
		),
		"duration_ms", max(observation.Duration.Milliseconds(), 0),
	)
}

func (observer *BusinessObserver) RecordRPCEndpointState(state ethrpc.EndpointState) {
	logger := slog.Default()
	if observer != nil && observer.logger != nil {
		logger = observer.logger
	}
	if state.State == "degraded" {
		if state.ConsecutiveFailures > 6 {
			return
		}
		logger.Warn("RPC endpoint entered cooldown",
			"event", "rpc_endpoint_degraded", "component", "rpc-client",
			"rpc", slog.GroupValue(
				slog.String("endpoint", boundedMetadataText(state.Endpoint, 128)),
				slog.Uint64("consecutive_failures", uint64(state.ConsecutiveFailures)),
				slog.Int64("cooldown_ms", max(state.Cooldown.Milliseconds(), 0)),
			),
		)
		return
	}
	logger.Info("RPC endpoint recovered",
		"event", "rpc_endpoint_recovered", "component", "rpc-client",
		"rpc", slog.GroupValue(slog.String("endpoint", boundedMetadataText(state.Endpoint, 128))),
	)
}

func (observer *BusinessObserver) RecordENS(observation ensresolver.MetricObservation) {
	if observer != nil && observer.registry != nil {
		observer.registry.RecordENS(string(observation.Source), observation.Direction, observation.Outcome)
	}
	logger := slog.Default()
	if observer != nil && observer.logger != nil {
		logger = observer.logger
	}
	logger.Debug("ENS resolution completed",
		"event", "ens_resolution_completed", "component", "ens-resolver",
		"source", boundedENSMetricValue(string(observation.Source), "ens", "custom_ens"),
		"direction", boundedENSMetricValue(observation.Direction, "forward", "primary"),
		"outcome", boundedENSMetricValue(observation.Outcome, "resolved", "not_found", "error", "cache_hit"),
		"duration_ms", max(observation.Duration.Milliseconds(), 0),
	)
}

func boundedENSMetricValue(value string, allowed ...string) string {
	if slices.Contains(allowed, value) {
		return value
	}
	return "other"
}

func boundedRPCMethod(method string) string {
	switch method {
	case "debug_traceBlockByHash", "empty_batch", "eth_blockNumber", "eth_call",
		"eth_chainId", "eth_getBalance", "eth_getBlockByHash", "eth_getBlockByNumber",
		"eth_getBlockReceipts", "eth_getCode", "eth_getStorageAt",
		"eth_getTransactionCount", "eth_getTransactionReceipt",
		"eth_getUncleByBlockHashAndIndex", "mixed_batch", "rpc_modules",
		"trace_transaction", "txpool_content":
		return method
	default:
		return "other"
	}
}

func NewBusinessObserver(registry *Registry, logger *slog.Logger) *BusinessObserver {
	if logger == nil {
		logger = slog.Default()
	}
	return &BusinessObserver{registry: registry, logger: logger}
}

func (observer *BusinessObserver) RecordEnrichmentJob(transition enrich.JobTransition) {
	stage := boundedJobStage(transition.Job.Stage.String())
	result := boundedJobResult(transition.Result)
	if transition.Event != enrich.JobEventStarted && observer != nil && observer.registry != nil {
		observer.registry.RecordEnrichmentJob(stage, result)
	}
	logger := slog.Default()
	if observer != nil && observer.logger != nil {
		logger = observer.logger
	}
	attributes := enrichmentLogAttributes(transition, stage, result)
	switch transition.Event {
	case enrich.JobEventStarted:
		logger.LogAttrs(context.Background(), slog.LevelDebug, "enrichment job started", attributes...)
	case enrich.JobEventExecutionFailed:
		logger.LogAttrs(context.Background(), slog.LevelError, "enrichment job execution failed", attributes...)
	default:
		logger.LogAttrs(context.Background(), businessLogLevel(result), "enrichment job transitioned", attributes...)
	}
}

func (observer *BusinessObserver) RecordEnrichmentOutbox(transition enrich.OutboxTransition) {
	logger := slog.Default()
	if observer != nil && observer.logger != nil {
		logger = observer.logger
	}
	attributes := []slog.Attr{
		slog.String("event", "enrichment_outbox_transitioned"),
		slog.String("component", boundedMetadataText(transition.Component, 128)),
		groupAttribute("outbox",
			slog.Int64("id", max(transition.ID, 0)),
			slog.String("topic", boundedOutboxTopic(transition.Topic)),
			slog.Int64("attempt", max(transition.Attempt, 0)),
			slog.String("generation", strconv.FormatInt(max(transition.Generation, 0), 10)),
		),
	}
	if transition.BlockHash != (common.Hash{}) {
		attributes = append(attributes, groupAttribute("block",
			slog.String("number", strconv.FormatUint(transition.BlockNumber, 10)),
			slog.String("hash", strings.ToLower(transition.BlockHash.Hex())),
		))
	}
	transitionAttrs := []slog.Attr{slog.String("result", boundedOutboxResult(transition.Result))}
	if transition.Code != "" {
		transitionAttrs = append(transitionAttrs, slog.String("code", boundedOutboxCode(transition.Code)))
	}
	if transition.RetryAfter > 0 {
		transitionAttrs = append(transitionAttrs, slog.Int64("retry_in_ms", transition.RetryAfter.Milliseconds()))
	}
	attributes = append(attributes, groupAttribute("transition", transitionAttrs...))
	if transition.Duration > 0 {
		attributes = append(attributes, slog.Int64("duration_ms", transition.Duration.Milliseconds()))
	}
	if transition.JobsCreated > 0 || transition.JobsExisting > 0 || len(transition.Stages) > 0 {
		stages := make([]string, 0, len(transition.Stages))
		for _, stage := range transition.Stages {
			stages = append(stages, boundedStageVersion(stage))
		}
		attributes = append(attributes, groupAttribute("summary",
			slog.Int("jobs_created", max(transition.JobsCreated, 0)),
			slog.Int("jobs_existing", max(transition.JobsExisting, 0)),
			slog.Any("stages", stages),
		))
	}
	level := slog.LevelInfo
	if transition.Result == "retry" {
		level = slog.LevelWarn
	}
	logger.LogAttrs(context.Background(), level, "enrichment outbox transitioned", attributes...)
}

func boundedOutboxTopic(value string) string {
	switch value {
	case enrich.CoreBlockCanonical, enrich.CoreBlockOrphaned:
		return value
	default:
		return "other"
	}
}

func boundedOutboxResult(value string) string {
	switch value {
	case "enrichment_enqueued", "orphan_acknowledged", "retry",
		"stale_canonical_skipped", "stale_orphan_skipped":
		return value
	default:
		return "other"
	}
}

func boundedOutboxCode(value string) string {
	if value == "dispatch_failed" {
		return value
	}
	return "other"
}

func boundedStageVersion(value string) string {
	parts := strings.Split(value, "@")
	if len(parts) != 2 || boundedJobStage(value) == "other" {
		return "other"
	}
	version, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil || version == 0 {
		return "other"
	}
	return boundedJobStage(value) + "@" + strconv.FormatUint(version, 10)
}

func enrichmentLogAttributes(
	transition enrich.JobTransition,
	stage, result string,
) []slog.Attr {
	job := transition.Job
	attributes := []slog.Attr{
		slog.String("event", "enrichment_job_"+string(transition.Event)),
		slog.String("component", boundedMetadataText(transition.Component, 128)),
		groupAttribute("job",
			slog.String("id", boundedMetadataText(job.ID, 128)),
			slog.String("worker", boundedMetadataText(transition.WorkerID, 128)),
			slog.Uint64("attempt", uint64(job.Attempt)),
			slog.Uint64("max_attempts", uint64(job.MaxAttempts)),
			slog.String("generation", strconv.FormatUint(job.Generation, 10)),
		),
		groupAttribute("stage",
			slog.String("name", stage),
			slog.Uint64("version", uint64(job.Stage.Version)),
		),
		groupAttribute("block",
			slog.String("number", strconv.FormatUint(job.BlockNumber, 10)),
			slog.String("hash", strings.ToLower(job.BlockHash.Hex())),
		),
	}
	transitionAttrs := []slog.Attr{slog.String("result", result)}
	if status := boundedJobStatus(transition.JobStatus); transition.JobStatus != "" {
		transitionAttrs = append(transitionAttrs, slog.String("job_status", status))
	}
	if transition.StageState != "" {
		transitionAttrs = append(transitionAttrs, slog.String("stage_state", boundedStageState(transition.StageState)))
	}
	if code := boundedEnrichmentCode(transition.Code); code != "" {
		transitionAttrs = append(transitionAttrs, slog.String("code", code))
	}
	if transition.RetryAfter > 0 {
		transitionAttrs = append(transitionAttrs, slog.Int64("retry_in_ms", transition.RetryAfter.Milliseconds()))
	}
	attributes = append(attributes, groupAttribute("transition", transitionAttrs...))
	if transition.Duration > 0 {
		attributes = append(attributes, slog.Int64("duration_ms", transition.Duration.Milliseconds()))
	}
	diagnostic := transition.Diagnostic
	if diagnostic.Endpoint != "" {
		attributes = append(attributes, groupAttribute("rpc",
			slog.String("endpoint", boundedMetadataText(diagnostic.Endpoint, 128)),
		))
	}
	if diagnostic.HasTransaction && diagnostic.TransactionHash != (common.Hash{}) {
		attributes = append(attributes, groupAttribute("transaction",
			slog.String("hash", strings.ToLower(diagnostic.TransactionHash.Hex())),
			slog.String("index", strconv.FormatUint(diagnostic.TransactionIndex, 10)),
		))
	}
	if phase := boundedEnrichmentPhase(diagnostic.Phase); phase != "" {
		attributes = append(attributes, groupAttribute("failure", slog.String("phase", phase)))
	}
	if summary := boundedEnrichmentSummary(stage, transition.Details); len(summary) > 0 {
		attributes = append(attributes, groupAttribute("summary", summary...))
	}
	return attributes
}

func (observer *BusinessObserver) RecordVerificationJob(transition verify.JobTransition) {
	result := boundedJobResult(transition.Result)
	if transition.Event != verify.JobEventStarted && observer != nil && observer.registry != nil {
		observer.registry.RecordVerificationJob(result)
	}
	logger := slog.Default()
	if observer != nil && observer.logger != nil {
		logger = observer.logger
	}
	job := transition.Job
	attributes := []slog.Attr{
		slog.String("event", "verification_job_"+string(transition.Event)),
		slog.String("component", boundedMetadataText(transition.Component, 128)),
		groupAttribute("job",
			slog.String("id", boundedMetadataText(job.ID, 128)),
			slog.String("worker", boundedMetadataText(transition.WorkerID, 128)),
			slog.String("kind", boundedVerificationKind(string(job.Kind))),
			slog.Int("attempt", max(job.AttemptCount, 0)),
			slog.Int("max_attempts", max(job.MaxAttempts, 0)),
		),
	}
	transitionAttrs := []slog.Attr{slog.String("result", result)}
	if outcome := boundedVerificationOutcome(transition.Outcome); outcome != "" {
		transitionAttrs = append(transitionAttrs, slog.String("outcome", outcome))
	}
	if code := boundedVerificationCode(string(transition.ErrorCode)); code != "" {
		transitionAttrs = append(transitionAttrs, slog.String("code", code))
	}
	attributes = append(attributes, groupAttribute("transition", transitionAttrs...))
	if transition.Duration > 0 {
		attributes = append(attributes, slog.Int64("duration_ms", transition.Duration.Milliseconds()))
	}
	if request := job.RequestV2; request != nil {
		compiler := []slog.Attr{
			slog.String("language", boundedVerificationLanguage(string(request.Language))),
			slog.String("version", boundedMetadataText(request.CompilerVersion, 128)),
		}
		attributes = append(attributes, groupAttribute("compiler", compiler...))
		if request.Target != nil {
			attributes = append(attributes, groupAttribute("target",
				slog.String("address", boundedFixedHex(request.Target.Address, 20)),
				slog.String("code_hash", boundedFixedHex(request.Target.CodeHash, 32)),
				slog.String("block_hash", boundedFixedHex(request.Target.AtBlockHash, 32)),
			))
		}
	}
	level, message := businessLogLevel(result), "verification job transitioned"
	switch transition.Event {
	case verify.JobEventStarted:
		level, message = slog.LevelDebug, "verification job started"
	case verify.JobEventExecutionFailed:
		level, message = slog.LevelError, "verification job execution failed"
	}
	logger.LogAttrs(context.Background(), level, message, attributes...)
}

func (observer *BusinessObserver) RecordDerivedVerification(observation derivedverify.Observation) {
	if observer != nil && observer.registry != nil {
		observer.registry.RecordDerivedVerification(observation.Kind, observation.Result)
	}
}

func boundedVerificationKind(value string) string {
	switch verify.JobKind(value) {
	case verify.JobAddress, verify.JobSolidityMultipart, verify.JobSolidityStandardJSON,
		verify.JobSolidityBatchMultipart, verify.JobSolidityBatchStandardJSON,
		verify.JobSourcify, verify.JobSourcifyFromEtherscan, verify.JobProxy, verify.JobDerived:
		return value
	default:
		return "other"
	}
}

func boundedVerificationLanguage(value string) string {
	switch verify.Language(value) {
	case verify.LanguageSolidity, verify.LanguageYul, verify.LanguageGeas:
		return value
	default:
		return "other"
	}
}

func boundedVerificationOutcome(value string) string {
	switch value {
	case "batch_results", "compilation_failure", "proxy_success", "sourcify_success",
		"verification_failure", "verification_success":
		return value
	case "":
		return ""
	default:
		return "other"
	}
}

func boundedVerificationCode(value string) string {
	switch verify.ErrorCode(value) {
	case verify.ErrorCompileFailed, verify.ErrorCompilerOutput, verify.ErrorCompilerTooLarge,
		verify.ErrorMatchFailed, verify.ErrorSandboxRequired, verify.ErrorCompilerProvenanceMismatch,
		verify.ErrorCompilerUnavailable, verify.ErrorTargetNotCanonical,
		verify.ErrorAttemptsExhausted, verify.ErrorExecutorMigrated:
		return value
	case "":
		return ""
	default:
		return "other"
	}
}

func boundedFixedHex(value string, size int) string {
	value = strings.ToLower(value)
	if len(value) != 2+size*2 || !strings.HasPrefix(value, "0x") {
		return "unknown"
	}
	for _, character := range value[2:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return "unknown"
		}
	}
	return value
}

func (observer *BusinessObserver) RecordVerificationCompiler(family string, available bool) {
	if observer != nil && observer.registry != nil {
		observer.registry.SetVerificationCompilerAvailable(family, available)
	}
}

func (observer *BusinessObserver) RecordMetadataFetch(transition metadata.FetchTransition) {
	result := boundedJobResult(transition.Result)
	if transition.Event != metadata.FetchEventStarted && observer != nil && observer.registry != nil {
		observer.registry.RecordMetadataFetch(result)
	}
	logger := slog.Default()
	if observer != nil && observer.logger != nil {
		logger = observer.logger
	}
	attributes := metadataFetchLogAttributes(transition)
	attributes = append([]slog.Attr{
		slog.String("event", "metadata_fetch_"+boundedMetadataEvent(transition.Event)),
		slog.String("component", boundedMetadataText(transition.Component, 128)),
	}, attributes...)
	attributes = append(attributes, slog.String("result", result))
	if transition.RetryAfter > 0 {
		attributes = append(attributes, slog.Int64("retry_in_ms", transition.RetryAfter.Milliseconds()))
	}
	if transition.Duration > 0 {
		attributes = append(attributes, slog.Int64("duration_ms", transition.Duration.Milliseconds()))
	}
	level, message := businessLogLevel(result), "metadata fetch transitioned"
	if transition.Event == metadata.FetchEventStarted {
		level, message = slog.LevelDebug, "metadata fetch started"
	}
	logger.LogAttrs(
		context.Background(), level, message, attributes...,
	)
}

func boundedMetadataEvent(event metadata.FetchEvent) string {
	switch event {
	case metadata.FetchEventStarted, metadata.FetchEventTransitioned:
		return string(event)
	case "":
		return string(metadata.FetchEventTransitioned)
	default:
		return "other"
	}
}

func (observer *BusinessObserver) RecordMaintenanceRequest(transition maintenance.RequestTransition) {
	operation := boundedMaintenanceOperation(string(transition.Request.Operation))
	result := boundedJobResult(transition.Result)
	if transition.Event != maintenance.RequestEventStarted && observer != nil && observer.registry != nil {
		observer.registry.RecordMaintenanceRequest(operation, result)
	}
	logger := slog.Default()
	if observer != nil && observer.logger != nil {
		logger = observer.logger
	}
	attributes := []slog.Attr{
		slog.String("event", "maintenance_request_"+string(transition.Event)),
		slog.String("component", boundedMetadataText(transition.Component, 128)),
		groupAttribute("request",
			slog.Int64("id", max(transition.Request.ID, 0)),
			slog.String("worker", boundedMetadataText(transition.WorkerID, 128)),
			slog.String("operation", operation),
			slog.String("stage", boundedJobStage(transition.Request.Stage)),
			slog.String("from_block", strconv.FormatUint(transition.Request.FromBlock, 10)),
			slog.String("to_block", strconv.FormatUint(transition.Request.ToBlock, 10)),
		),
		groupAttribute("transition",
			slog.String("result", result),
			slog.String("code", boundedMaintenanceCode(transition.Code)),
		),
	}
	if transition.Duration > 0 {
		attributes = append(attributes, slog.Int64("duration_ms", transition.Duration.Milliseconds()))
	}
	level, message := businessLogLevel(result), "maintenance request transitioned"
	switch transition.Event {
	case maintenance.RequestEventStarted:
		level, message = slog.LevelDebug, "maintenance request started"
	case maintenance.RequestEventExecutionFailed:
		level, message = slog.LevelError, "maintenance request execution failed"
	}
	logger.LogAttrs(context.Background(), level, message, attributes...)
}

func boundedMaintenanceCode(value string) string {
	switch value {
	case "execution_failed", "finality_guard_failed", "finalized_range", "invalid_request", "transition_failed":
		return value
	case "":
		return ""
	default:
		return "other"
	}
}

func businessLogLevel(result string) slog.Level {
	switch result {
	case "succeeded", "unavailable", "stale_target":
		return slog.LevelInfo
	default:
		return slog.LevelWarn
	}
}

func boundedStageState(state enrich.ResultState) string {
	switch state {
	case enrich.ResultComplete, enrich.ResultUnavailable, enrich.ResultFailed:
		return string(state)
	default:
		return "other"
	}
}

func boundedEnrichmentCode(code string) string {
	switch code {
	case "attempts_exhausted", "capability_unavailable", "execution_failed",
		"permanent_failure", "retryable_failure", "rpc_request_failed",
		"resource_limit", "state_diff_response_invalid", "state_diff_transaction_failed",
		"superseded", "trace_identity_mismatch", "trace_response_invalid",
		"trace_transaction_failed":
		return code
	case "":
		return ""
	default:
		return "other"
	}
}

func boundedEnrichmentPhase(phase string) string {
	switch phase {
	case "account_call_tracer", "call_tracer", "complete_prestate", "diff_prestate",
		"normalize_call_tracer", "normalize_diff", "rpc", "trace_transaction",
		"validate_call_tracer":
		return phase
	case "":
		return ""
	default:
		return "other"
	}
}

func boundedEnrichmentSummary(stage string, details map[string]string) []slog.Attr {
	if len(details) == 0 {
		return nil
	}
	integerKeys := map[string]bool{
		"abi_requeued": false, "ambiguous": true, "attributed_logs": true,
		"authorizations": true, "beacons": true, "bindings": true,
		"candidates": true, "carried_beacons": true, "carried_negative_evidence": true,
		"carried_proxies": true, "carried_resolutions": true, "carried_uups": true,
		"changes": true, "code_observations": true, "contracts": true,
		"creation_targets": true, "decoded": true, "diamond_cut_events": true,
		"diamond_routed": true, "effective_executions": true, "events": true,
		"execution_resolutions": true, "fallback_logs": true, "frames": true,
		"initialization_events": true, "invalid_signatures": true, "malformed": true,
		"malformed_logs": true, "proxies": true, "proxy_detection_v2_differences": true,
		"proxy_relevant_changes": true, "proxy_requeued": false,
		"recovered_executions": true, "rejected_candidates": true,
		"rejected_events": true, "trace_requeued": false, "transactions": true,
		"unknown": true, "unbound": true, "upgrade_events": true,
		"uups_compatible": true, "uups_probes": true, "uups_rejected": true,
	}
	allowedByStage := map[string]map[string]bool{
		"proxy": integerKeys, "abi": integerKeys, "token": integerKeys,
		"stats": integerKeys, "trace": integerKeys, "state_diff": integerKeys,
	}
	allowed := allowedByStage[stage]
	attributes := make([]slog.Attr, 0, len(details))
	for key, value := range details {
		if numeric, ok := allowed[key]; ok {
			if numeric {
				parsed, err := strconv.ParseUint(value, 10, 64)
				if err == nil {
					attributes = append(attributes, slog.Uint64(key, parsed))
				}
			} else if parsed, err := strconv.ParseBool(value); err == nil {
				attributes = append(attributes, slog.Bool(key, parsed))
			}
			continue
		}
		switch key {
		case "history_coverage", "outcome", "proxy_dependency", "source",
			"state_diff_coverage", "trace_coverage":
			if bounded := boundedMetadataText(value, 64); bounded != "unknown" {
				attributes = append(attributes, slog.String(key, bounded))
			}
		}
	}
	slices.SortFunc(attributes, func(left, right slog.Attr) int {
		return strings.Compare(left.Key, right.Key)
	})
	return attributes
}

func metadataFetchLogAttributes(transition metadata.FetchTransition) []slog.Attr {
	diagnostic := transition.Diagnostic
	attributes := make([]slog.Attr, 0, 8)
	attributes = append(attributes,
		groupAttribute("job",
			slog.Int64("id", max(transition.JobID, 0)),
			slog.String("worker", boundedMetadataText(transition.WorkerID, 128)),
			slog.Uint64("attempt", uint64(transition.Attempt)),
			slog.Uint64("max_attempts", uint64(transition.MaxAttempts)),
		),
		groupAttribute("nft",
			slog.String("contract", strings.ToLower(transition.NFTContract.Hex())),
			slog.String("id", boundedMetadataDecimal(transition.NFTID)),
		),
		groupAttribute("block",
			slog.String("number", strconv.FormatUint(transition.BlockNumber, 10)),
			slog.String("hash", strings.ToLower(transition.BlockHash.Hex())),
		),
	)
	transitionAttributes := []slog.Attr{slog.String("state", boundedMetadataState(transition.State))}
	if code := boundedMetadataCode(transition.Code); code != "" {
		transitionAttributes = append(transitionAttributes, slog.String("code", code))
	}
	if lastCode := boundedMetadataCode(transition.LastCode); lastCode != "" {
		transitionAttributes = append(transitionAttributes, slog.String("last_code", lastCode))
	}
	attributes = append(attributes, groupAttribute("transition", transitionAttributes...))

	if sourceScheme := boundedSourceScheme(diagnostic.SourceScheme); sourceScheme != "" {
		attributes = append(attributes, groupAttribute("source", slog.String("scheme", sourceScheme)))
	}
	if requestAttributes := boundedRequestAttributes(diagnostic); len(requestAttributes) > 0 {
		attributes = append(attributes, groupAttribute("request", requestAttributes...))
	}
	if networkAttributes := boundedNetworkAttributes(diagnostic); len(networkAttributes) > 0 {
		attributes = append(attributes, groupAttribute("network", networkAttributes...))
	}
	failureAttributes := make([]slog.Attr, 0, 2)
	if phase := boundedFetchPhase(diagnostic.Phase); phase != "" && phase != string(metadata.FetchPhaseNone) {
		failureAttributes = append(failureAttributes, slog.String("phase", phase))
	}
	if reason := boundedFetchFailureReason(diagnostic.Reason); reason != "" {
		failureAttributes = append(failureAttributes, slog.String("reason", reason))
	}
	if len(failureAttributes) > 0 {
		attributes = append(attributes, groupAttribute("failure", failureAttributes...))
	}
	return attributes
}

func boundedFetchFailureReason(reason metadata.FetchFailureReason) string {
	switch reason {
	case metadata.FetchFailureDNSLookupFailed, metadata.FetchFailureDNSTimeout,
		metadata.FetchFailureNetworkPolicyRejected, metadata.FetchFailureConnectTimeout,
		metadata.FetchFailureConnectionRefused, metadata.FetchFailureNetworkUnreachable,
		metadata.FetchFailureTLSHandshakeTimeout, metadata.FetchFailureTLSCertificateInvalid,
		metadata.FetchFailureTLSProtocolError, metadata.FetchFailureRequestTimeout,
		metadata.FetchFailureTransportError:
		return string(reason)
	case "":
		return ""
	default:
		return "other"
	}
}

func boundedRequestAttributes(diagnostic metadata.FetchDiagnostic) []slog.Attr {
	if diagnostic.RequestScheme == "" && diagnostic.RequestHost == "" &&
		diagnostic.RequestPathSHA256 == "" {
		return nil
	}
	attributes := make([]slog.Attr, 0, 11)
	if method := boundedMetadataMethod(diagnostic.RequestMethod); method != "" {
		attributes = append(attributes, slog.String("method", method))
	}
	if scheme := boundedRequestScheme(diagnostic.RequestScheme); scheme != "" {
		attributes = append(attributes, slog.String("scheme", scheme))
	}
	if host := boundedMetadataHost(diagnostic.RequestHost); host != "" {
		attributes = append(attributes, slog.String("host", host))
	}
	if port := boundedMetadataPort(diagnostic.RequestPort); port != "" {
		attributes = append(attributes, slog.String("port", port))
	}
	if path := boundedMetadataPath(diagnostic.RequestPath); path != "" {
		attributes = append(attributes, slog.String("path", path))
	}
	if diagnostic.RequestPathLength >= 0 {
		attributes = append(attributes, slog.Int("path_length", diagnostic.RequestPathLength))
	}
	if digest := boundedSHA256(diagnostic.RequestPathSHA256); digest != "" {
		attributes = append(attributes, slog.String("path_sha256", digest))
	}
	attributes = append(attributes,
		slog.Bool("path_hidden", diagnostic.RequestPathHidden),
		slog.Bool("query_present", diagnostic.QueryPresent),
		slog.Int("redirects", max(diagnostic.RedirectCount, 0)),
	)
	return attributes
}

func boundedNetworkAttributes(diagnostic metadata.FetchDiagnostic) []slog.Attr {
	resolved := boundedMetadataIPs(diagnostic.ResolvedIPs)
	rejected := boundedMetadataIPs(diagnostic.RejectedIPs)
	reasons := boundedMetadataReasons(diagnostic.RejectedReasons)
	prefixes := boundedMetadataPrefixes(diagnostic.RejectedPrefixes)
	connected := boundedMetadataIP(diagnostic.ConnectedIP)
	if len(resolved) == 0 && len(rejected) == 0 && len(reasons) == 0 &&
		len(prefixes) == 0 && connected == "" && diagnostic.ResolvedIPCount == 0 {
		return nil
	}
	attributes := make([]slog.Attr, 0, 10)
	if len(resolved) > 0 {
		attributes = append(attributes, slog.Any("resolved_ips", resolved))
	}
	attributes = append(attributes,
		slog.Int("resolved_count", max(diagnostic.ResolvedIPCount, 0)),
		slog.Bool("resolved_truncated", diagnostic.ResolvedIPsTruncated),
		slog.Bool("policy_bypassed", diagnostic.NetworkPolicyBypassed),
	)
	if connected != "" {
		attributes = append(attributes, slog.String("connected_ip", connected))
	}
	if len(rejected) > 0 {
		attributes = append(attributes, slog.Any("rejected_ips", rejected))
	}
	if len(reasons) > 0 {
		attributes = append(attributes, slog.Any("rejected_reasons", reasons))
	}
	if len(prefixes) > 0 {
		attributes = append(attributes, slog.Any("rejected_prefixes", prefixes))
	}
	attributes = append(attributes,
		slog.Int("rejected_count", max(diagnostic.RejectedIPCount, 0)),
		slog.Bool("rejected_truncated", diagnostic.RejectedIPsTruncated),
	)
	return attributes
}

func groupAttribute(key string, attributes ...slog.Attr) slog.Attr {
	return slog.Attr{Key: key, Value: slog.GroupValue(attributes...)}
}

func boundedMetadataState(state metadata.State) string {
	switch state {
	case metadata.StatePending, metadata.StateAvailable, metadata.StateUnavailable,
		metadata.StateUnsafe, metadata.StateError:
		return string(state)
	default:
		return "other"
	}
}

func boundedMetadataCode(code string) string {
	switch code {
	case "attempts_exhausted", "fetch_error", "invalid_content", "invalid_document",
		"invalid_fetch_result", "response_too_large", "source_block_noncanonical",
		"source_unavailable", "superseded", "temporary_fetch_error", "unsafe_content",
		"unsafe_url":
		return code
	case "":
		return ""
	default:
		return "other"
	}
}

func boundedFetchPhase(phase metadata.FetchPhase) string {
	switch phase {
	case metadata.FetchPhaseNone, metadata.FetchPhaseURL, metadata.FetchPhaseRedirect,
		metadata.FetchPhaseDNS, metadata.FetchPhaseNetworkPolicy, metadata.FetchPhaseTransport,
		metadata.FetchPhaseHTTP, metadata.FetchPhaseContent, metadata.FetchPhaseDocument,
		metadata.FetchPhaseCanonicality:
		return string(phase)
	default:
		return "other"
	}
}

func boundedSourceScheme(value string) string {
	switch value {
	case "ipfs", "https", "http", "invalid", "other":
		return value
	default:
		return ""
	}
}

func boundedRequestScheme(value string) string {
	switch value {
	case "https", "http", "invalid", "other":
		return value
	default:
		return ""
	}
}

func boundedMetadataMethod(value string) string {
	if strings.ToUpper(strings.TrimSpace(value)) == "GET" {
		return "GET"
	}
	return ""
}

func boundedMetadataDecimal(value string) string {
	if value == "" || len(value) > 78 {
		return "unknown"
	}
	for index, character := range value {
		if character < '0' || character > '9' || index == 0 && len(value) > 1 && character == '0' {
			return "unknown"
		}
	}
	return value
}

func boundedMetadataText(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maximum {
		return "unknown"
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return "unknown"
		}
	}
	return value
}

func boundedMetadataHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 253 {
		return ""
	}
	for _, character := range value {
		if character <= 0x20 || character == 0x7f || character == '/' || character == '@' {
			return ""
		}
	}
	return value
}

func boundedMetadataPort(value string) string {
	if value == "" || len(value) > 5 {
		return ""
	}
	port, err := strconv.ParseUint(value, 10, 16)
	if err != nil || port == 0 {
		return ""
	}
	return strconv.FormatUint(port, 10)
}

func boundedMetadataPath(value string) string {
	if value == "" || len(value) > 1024 || !strings.HasPrefix(value, "/") {
		return ""
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f || character == '?' || character == '#' {
			return ""
		}
	}
	return value
}

func boundedSHA256(value string) string {
	value = strings.ToLower(value)
	if len(value) != 64 {
		return ""
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return ""
		}
	}
	return value
}

func boundedMetadataIP(value string) string {
	address, err := netip.ParseAddr(value)
	if err != nil {
		return ""
	}
	return address.Unmap().String()
}

func boundedMetadataIPs(values []string) []string {
	result := make([]string, 0, min(len(values), 8))
	for _, value := range values {
		address := boundedMetadataIP(value)
		if address == "" || slices.Contains(result, address) {
			continue
		}
		result = append(result, address)
		if len(result) == 8 {
			break
		}
	}
	return result
}

func boundedMetadataReasons(values []string) []string {
	result := make([]string, 0, min(len(values), 8))
	for _, value := range values {
		switch netpolicy.IPClassification(value) {
		case netpolicy.IPClassificationInvalid, netpolicy.IPClassificationPrivate,
			netpolicy.IPClassificationLoopback, netpolicy.IPClassificationLinkLocal,
			netpolicy.IPClassificationUnspecified, netpolicy.IPClassificationNonGlobal,
			netpolicy.IPClassificationSpecialPurpose:
		default:
			continue
		}
		if !slices.Contains(result, value) {
			result = append(result, value)
		}
		if len(result) == 8 {
			break
		}
	}
	return result
}

func boundedMetadataPrefixes(values []string) []string {
	result := make([]string, 0, min(len(values), 8))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.String() != value || slices.Contains(result, value) {
			continue
		}
		result = append(result, value)
		if len(result) == 8 {
			break
		}
	}
	return result
}
