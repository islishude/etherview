package observability

import (
	"context"
	"log/slog"
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

func (observer *BusinessObserver) RecordVerificationCompiler(available bool) {
	if observer != nil && observer.registry != nil {
		observer.registry.SetVerificationCompilerAvailable(available)
	}
}

func (observer *BusinessObserver) RecordMetadataFetch(result string) {
	result = boundedJobResult(result)
	if observer != nil && observer.registry != nil {
		observer.registry.RecordMetadataFetch(result)
	}
	observer.log("metadata fetch transitioned", slog.Attr{}, result)
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
