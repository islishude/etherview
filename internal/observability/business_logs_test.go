package observability

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestBusinessObserverLogsBoundedDurableTransitionsAndPreservesMetrics(t *testing.T) {
	t.Parallel()
	registry := NewRegistry("test", "all")
	var output bytes.Buffer
	observer := NewBusinessObserver(registry, slog.New(slog.NewJSONHandler(&output, nil)))

	observer.RecordEnrichmentJob("state_diff@1", "succeeded")
	observer.RecordVerificationJob("retry")
	observer.RecordMetadataFetch("ssrf_rejected")
	observer.RecordMaintenanceRequest("repair", "failed")
	observer.RecordEnrichmentJob("hostile-secret-stage", "hostile-secret-result")

	logs := output.String()
	for _, expected := range []string{
		`"msg":"enrichment job transitioned"`,
		`"stage":"state_diff"`,
		`"result":"succeeded"`,
		`"msg":"verification job transitioned"`,
		`"result":"retry"`,
		`"msg":"metadata fetch transitioned"`,
		`"result":"ssrf_rejected"`,
		`"msg":"maintenance request transitioned"`,
		`"operation":"repair"`,
		`"level":"INFO"`,
		`"level":"WARN"`,
	} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("business logs missing %q:\n%s", expected, logs)
		}
	}
	if strings.Contains(logs, "hostile-secret") {
		t.Fatalf("business logs leaked unbounded input: %s", logs)
	}
	metrics := registry.Gather()
	for _, expected := range []string{
		`etherview_enrichment_jobs_total{stage="state_diff",result="succeeded"} 1`,
		`etherview_enrichment_jobs_total{stage="other",result="other"} 1`,
		`etherview_verification_jobs_total{result="retry"} 1`,
		`etherview_metadata_fetches_total{result="ssrf_rejected"} 1`,
		`etherview_maintenance_requests_total{operation="repair",result="failed"} 1`,
	} {
		if !strings.Contains(metrics, expected) {
			t.Fatalf("business metrics missing %q:\n%s", expected, metrics)
		}
	}
}

func TestBusinessObserverUsesInfoOnlyForNonFailureOutcomes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		result string
		level  slog.Level
	}{
		{result: "succeeded", level: slog.LevelInfo},
		{result: "unavailable", level: slog.LevelInfo},
		{result: "stale_target", level: slog.LevelInfo},
		{result: "retry", level: slog.LevelWarn},
		{result: "failed", level: slog.LevelWarn},
		{result: "error", level: slog.LevelWarn},
	} {
		t.Run(test.result, func(t *testing.T) {
			if got := businessLogLevel(test.result); got != test.level {
				t.Fatalf("businessLogLevel(%q)=%s want=%s", test.result, got, test.level)
			}
		})
	}
}
