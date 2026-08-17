package observability

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/metadata"
)

func TestBusinessObserverLogsBoundedDurableTransitionsAndPreservesMetrics(t *testing.T) {
	t.Parallel()
	registry := NewRegistry("test", "all")
	var output bytes.Buffer
	observer := NewBusinessObserver(registry, slog.New(slog.NewJSONHandler(&output, nil)))

	observer.RecordEnrichmentJob("state_diff@1", "succeeded")
	observer.RecordVerificationJob("retry")
	observer.RecordMetadataFetch(testMetadataTransition())
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
		`"job":{"id":296,"worker":"metadata-worker-01","attempt":1,"max_attempts":5}`,
		`"nft":{"contract":"0x5fbdb2315678afecb367f032d93f642f64180aa3","id":"0"}`,
		`"transition":{"state":"unsafe","code":"unsafe_url"}`,
		`"source":{"scheme":"ipfs"}`,
		`"request":{"method":"GET","scheme":"https","host":"ipfs.io","port":"443","path":"/ipfs/Qma3sC19HbnWHqeLgcsQnR7Kvgus4oPQirXNH7QYBeACaq/0"`,
		`"network":{"resolved_ips":["198.18.17.210"]`,
		`"policy_bypassed":true`,
		`"rejected_prefixes":["198.18.0.0/15"]`,
		`"failure":{"phase":"network_policy"}`,
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

func testMetadataTransition() metadata.FetchTransition {
	return metadata.FetchTransition{
		JobID:       296,
		WorkerID:    "metadata-worker-01",
		NFTContract: common.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3"),
		NFTID:       "0",
		BlockNumber: 48,
		BlockHash:   common.HexToHash("0xdbe5e5d7c61b447253523cb300f575de8d48a0a0503542cfb4072e7b95ca0798"),
		Attempt:     1,
		MaxAttempts: 5,
		State:       metadata.StateUnsafe,
		Code:        "unsafe_url",
		Result:      "ssrf_rejected",
		Diagnostic: metadata.FetchDiagnostic{
			SourceScheme: "ipfs", RequestMethod: "GET", RequestScheme: "https",
			RequestHost: "ipfs.io", RequestPort: "443",
			RequestPath:       "/ipfs/Qma3sC19HbnWHqeLgcsQnR7Kvgus4oPQirXNH7QYBeACaq/0",
			RequestPathLength: 58,
			RequestPathSHA256: strings.Repeat("a", 64),
			ResolvedIPs:       []string{"198.18.17.210"}, ResolvedIPCount: 1,
			RejectedIPs: []string{"198.18.17.210"}, RejectedIPCount: 1,
			RejectedReasons: []string{"special_use"}, RejectedPrefixes: []string{"198.18.0.0/15"},
			NetworkPolicyBypassed: true,
			Phase:                 metadata.FetchPhaseNetworkPolicy,
		},
	}
}

func TestMetadataTransitionLogRejectsUnboundedOrSecretBearingFields(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	observer := NewBusinessObserver(nil, slog.New(slog.NewJSONHandler(&output, nil)))
	transition := testMetadataTransition()
	transition.WorkerID = "worker\nworker-secret"
	transition.NFTID = "nft-secret"
	transition.Code = "code-secret"
	transition.Result = "result-secret"
	transition.Diagnostic = metadata.FetchDiagnostic{
		SourceScheme: "scheme-secret", RequestMethod: "POST", RequestScheme: "ftp",
		RequestHost: "user:host-secret@example.com", RequestPort: "credential-secret",
		RequestPath: "/path-secret?query-secret=value", RequestPathLength: 123,
		RequestPathSHA256: strings.Repeat("f", 64), QueryPresent: true,
		ResolvedIPs: []string{"ip-secret", "198.18.17.210"}, ResolvedIPCount: 2,
		RejectedIPs: []string{"rejected-secret", "198.18.17.210"}, RejectedIPCount: 2,
		RejectedReasons:  []string{"reason-secret", "special_use"},
		RejectedPrefixes: []string{"prefix-secret", "198.18.0.0/15"},
		Phase:            "phase-secret",
	}
	observer.RecordMetadataFetch(transition)
	logs := output.String()
	for _, forbidden := range []string{
		"worker-secret", "nft-secret", "code-secret", "result-secret", "scheme-secret",
		"host-secret", "credential-secret", "path-secret", "query-secret", "ip-secret",
		"rejected-secret", "reason-secret", "prefix-secret", "phase-secret",
	} {
		if strings.Contains(logs, forbidden) {
			t.Fatalf("metadata log leaked %q: %s", forbidden, logs)
		}
	}
	for _, expected := range []string{
		`"worker":"unknown"`, `"id":"unknown"`, `"code":"other"`, `"result":"other"`,
		`"resolved_ips":["198.18.17.210"]`, `"rejected_reasons":["special_use"]`,
		`"policy_bypassed":false`,
		`"failure":{"phase":"other"}`,
	} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("metadata log missing %q: %s", expected, logs)
		}
	}
}

func TestMetadataTransitionLogPreservesSafeGroupsThroughProductionRedactor(t *testing.T) {
	t.Parallel()
	for _, format := range []LogFormat{LogFormatJSON, LogFormatText} {
		t.Run(string(format), func(t *testing.T) {
			var output bytes.Buffer
			logger := NewLogger(LoggerOptions{Writer: &output, Format: format})
			observer := NewBusinessObserver(nil, logger)
			transition := testMetadataTransition()
			transition.Diagnostic.QueryPresent = true
			observer.RecordMetadataFetch(transition)
			logs := output.String()
			for _, expected := range []string{
				"metadata fetch transitioned", "5fbdb2315678afecb367f032d93f642f64180aa3",
				"Qma3sC19HbnWHqeLgcsQnR7Kvgus4oPQirXNH7QYBeACaq", "198.18.17.210",
				"198.18.0.0/15", "network_policy",
			} {
				if !strings.Contains(logs, expected) {
					t.Fatalf("%s log missing %q: %s", format, expected, logs)
				}
			}
			if strings.Contains(logs, "[REDACTED]") {
				t.Fatalf("safe metadata identity was redacted from %s log: %s", format, logs)
			}
		})
	}
}

func TestBusinessObserverExposesCompilerAvailabilityWithoutBusinessLogSpam(t *testing.T) {
	t.Parallel()
	registry := NewRegistry("test", "api")
	var output bytes.Buffer
	observer := NewBusinessObserver(registry, slog.New(slog.NewJSONHandler(&output, nil)))
	observer.RecordVerificationCompiler("solcjs", false)
	observer.RecordVerificationCompiler("geas", true)
	if metrics := registry.Gather(); !strings.Contains(
		metrics, `etherview_verification_compiler_available{family="solcjs"} 0`,
	) {
		t.Fatalf("unavailable compiler metric is absent:\n%s", metrics)
	}
	observer.RecordVerificationCompiler("solcjs", true)
	if metrics := registry.Gather(); !strings.Contains(
		metrics, `etherview_verification_compiler_available{family="solcjs"} 1`,
	) {
		t.Fatalf("recovered compiler metric is absent:\n%s", metrics)
	}
	if metrics := registry.Gather(); !strings.Contains(
		metrics, `etherview_verification_compiler_available{family="geas"} 1`,
	) {
		t.Fatalf("Geas compiler metric is absent:\n%s", metrics)
	}
	if output.Len() != 0 {
		t.Fatalf("compiler polling emitted business-transition logs: %s", output.String())
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
