package observability

import (
	"context"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/metadata"
	"github.com/islishude/etherview/internal/netpolicy"
)

// BusinessObserver keeps the existing bounded metrics and emits one structured
// log only after a worker reports a durable business transition.
type BusinessObserver struct {
	registry *Registry
	logger   *slog.Logger
}

func NewBusinessObserver(registry *Registry, logger *slog.Logger) *BusinessObserver {
	if logger == nil {
		logger = slog.Default()
	}
	return &BusinessObserver{registry: registry, logger: logger}
}

func (observer *BusinessObserver) RecordEnrichmentJob(stage, result string) {
	stage = boundedJobStage(stage)
	result = boundedJobResult(result)
	if observer != nil && observer.registry != nil {
		observer.registry.RecordEnrichmentJob(stage, result)
	}
	observer.log("enrichment job transitioned", slog.String("stage", stage), result)
}

func (observer *BusinessObserver) RecordVerificationJob(result string) {
	result = boundedJobResult(result)
	if observer != nil && observer.registry != nil {
		observer.registry.RecordVerificationJob(result)
	}
	observer.log("verification job transitioned", slog.Attr{}, result)
}

func (observer *BusinessObserver) RecordVerificationCompiler(family string, available bool) {
	if observer != nil && observer.registry != nil {
		observer.registry.SetVerificationCompilerAvailable(family, available)
	}
}

func (observer *BusinessObserver) RecordMetadataFetch(transition metadata.FetchTransition) {
	result := boundedJobResult(transition.Result)
	if observer != nil && observer.registry != nil {
		observer.registry.RecordMetadataFetch(result)
	}
	logger := slog.Default()
	if observer != nil && observer.logger != nil {
		logger = observer.logger
	}
	attributes := metadataFetchLogAttributes(transition)
	attributes = append(attributes, slog.String("result", result))
	logger.LogAttrs(
		context.Background(), businessLogLevel(result), "metadata fetch transitioned", attributes...,
	)
}

func (observer *BusinessObserver) RecordMaintenanceRequest(operation, result string) {
	operation = boundedMaintenanceOperation(operation)
	result = boundedJobResult(result)
	if observer != nil && observer.registry != nil {
		observer.registry.RecordMaintenanceRequest(operation, result)
	}
	observer.log("maintenance request transitioned", slog.String("operation", operation), result)
}

func (observer *BusinessObserver) log(message string, detail slog.Attr, result string) {
	logger := slog.Default()
	if observer != nil && observer.logger != nil {
		logger = observer.logger
	}
	attrs := make([]slog.Attr, 0, 2)
	if detail.Key != "" {
		attrs = append(attrs, detail)
	}
	attrs = append(attrs, slog.String("result", result))
	logger.LogAttrs(context.Background(), businessLogLevel(result), message, attrs...)
}

func businessLogLevel(result string) slog.Level {
	switch result {
	case "succeeded", "unavailable", "stale_target":
		return slog.LevelInfo
	default:
		return slog.LevelWarn
	}
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
	if phase := boundedFetchPhase(diagnostic.Phase); phase != "" && phase != string(metadata.FetchPhaseNone) {
		attributes = append(attributes, groupAttribute("failure", slog.String("phase", phase)))
	}
	return attributes
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
