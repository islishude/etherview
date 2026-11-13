//go:build previewmetadatae2e

package previewmetadatae2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPreviewTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	testPreviewToolLayout(t, ctx, "split", false)
}

func TestPreviewToolsFull(t *testing.T) {
	if os.Getenv("PREVIEW_TOOLS_FULL") != "1" {
		t.Skip("opt in with make test-preview-tools-e2e-full")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 55*time.Minute)
	defer cancel()
	for _, phase := range []string{"all", "split"} {
		if !t.Run(phase, func(t *testing.T) { testPreviewToolLayout(t, ctx, phase, true) }) {
			return
		}
	}
}

// Keep focused DeFi acceptance independent of unrelated tool scenarios, while
// preserving the complete catalog gate above unchanged.
func TestPreviewDeFi(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 18*time.Minute)
	defer cancel()
	for _, phase := range []string{"all", "split"} {
		if !t.Run(phase, func(t *testing.T) { testPreviewToolScenarioLayout(t, ctx, phase, true, "defi") }) {
			return
		}
	}
}

func testPreviewToolLayout(t *testing.T, ctx context.Context, phase string, full bool) {
	scenario := "transactions"
	if full {
		scenario = "all"
	}
	testPreviewToolScenarioLayout(t, ctx, phase, full, scenario)
}

func testPreviewToolScenarioLayout(t *testing.T, ctx context.Context, phase string, full bool, scenario string) {
	h := newHarness(t)
	t.Cleanup(h.cleanup)
	data, err := os.ReadFile(filepath.Join(h.root, "preview/config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.ReplaceAll(string(data), "  sourcify: true", "  sourcify: false")
	// Binding publication requires coverage through the current canonical tip.
	// Use the runtime E2E poll cadence for this large catalog on three-second slots.
	cfg = strings.ReplaceAll(cfg, "  poll_interval: 2s", "  poll_interval: 250ms")
	cfg = strings.ReplaceAll(cfg, "bytecode_db:\n  enabled: true", "bytecode_db:\n  enabled: false")
	config := filepath.Join(h.artifacts, "tools.config.yaml")
	if err = os.WriteFile(config, []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	h.project.Env["ETHERVIEW_CONFIG_FILE"] = config
	h.prepareNetwork(ctx)
	if _, err = h.project.Run(ctx, "up", "-d", "--no-build", "--wait", "--wait-timeout", "180"); err != nil {
		t.Fatal(err)
	}
	h.initializeArchive(ctx)
	h.connect(ctx)
	waitFor(t, ctx, "tools genesis import", func() (bool, string, error) {
		var state string
		err := h.db.QueryRow(ctx, "SELECT state FROM genesis_state_imports WHERE chain_id=48815").Scan(&state)
		return state == "complete", state, err
	})
	helper := ""
	if full {
		helper = filepath.Join(h.artifacts, "previewblob")
		build := exec.CommandContext(ctx, "go", "build", "-o", helper, "./preview/cmd/previewblob")
		build.Dir = h.root
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("blob helper build: %v\n%s", err, output)
		}
	}
	if phase == "all" {
		h.toolsStrictMetadataGateway(ctx)
		h.contractsAllRoles(ctx)
		// The gateway recreation may change its ephemeral host port. Bind the
		// Go evidence reader to the same final gateway used by the CLI/browser.
		h.connect(ctx)
	}
	binding, err := h.project.Port(ctx, "preview-gateway", 443)
	if err != nil {
		t.Fatal(err)
	}
	_, port, ok := strings.Cut(binding, ":")
	if !ok {
		t.Fatal("missing gateway port")
	}
	dir := filepath.Join(t.TempDir(), "tools-"+phase)
	var runCLI func(args ...string) map[string]any
	runCLI = func(args ...string) map[string]any {
		t.Helper()
		common := []string{"preview/contracts/tools/cli.mjs", "--json", "--dir", dir, "--rpc", "https://execution.etherview.localhost:" + port, "--bundler", "https://bundler.etherview.localhost:" + port, "--app", "https://app.etherview.localhost:" + port, "--project", h.project.Name, "--compose-files", strings.Join(h.project.Files, ","), "--blob-helper", helper, "--timeout", "180"}
		command := exec.CommandContext(ctx, "node", append(common, args...)...)
		command.Dir = h.root
		command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+filepath.Join(h.root, ".local/preview-tls/rootCA.pem"))
		for k, v := range h.project.Env {
			command.Env = append(command.Env, k+"="+v)
		}
		output, runErr := command.CombinedOutput()
		log := filepath.Join(h.artifacts, "tools-"+phase+"-"+args[0]+".log")
		if err := os.WriteFile(log, output, 0600); err != nil {
			t.Fatal(err)
		}
		var report map[string]any
		if err := json.Unmarshal(output, &report); err != nil {
			t.Fatalf("CLI JSON: %v\n%s", err, output)
		}
		if runErr != nil {
			// A moving three-second chain may exhaust the initial binding window.
			// Exercise the documented resume path once, only for that typed outcome.
			if args[0] == "run" && onlyStaleToolBindings(report) {
				t.Log("resume known stale proxy bindings without replacing transactions")
				resumed := runCLI("resume", "--run", report["id"].(string))
				identities := toolTransactionIdentities(resumed)
				for step, hash := range toolTransactionIdentities(report) {
					if identities[step] != hash {
						t.Fatalf("resume replaced transaction %s", step)
					}
				}
				return resumed
			}
			t.Fatalf("Preview tool %s: %v; report %s\n%s", args[0], runErr, log, output)
		}
		return report
	}
	args := []string{"run", scenario}
	if !full {
		args = append(args, "--no-verify")
	}
	report := runCLI(args...)
	id, ok := report["id"].(string)
	if !ok {
		t.Fatal("missing run ID")
	}
	reportPath := filepath.Join(dir, "runs", id, "report.json")
	retainReport := func(label string) {
		t.Helper()
		body, err := os.ReadFile(reportPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.artifacts, "tools-"+phase+"-"+label+".json"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	retainReport("initial")
	if full {
		h.assertToolsBrowser(ctx, binding, reportPath, phase+"-initial")
	}
	initialTransactions := toolTransactionIdentities(report)
	runCLI("resume", "--run", id, "--lifecycle")
	before := runCLI("report", "--run", id)
	if full && scenario == "all" {
		h.assertToolsClearEvidence(ctx, before, phase)
	}
	for step, hash := range initialTransactions {
		if toolTransactionIdentities(before)[step] != hash {
			t.Fatalf("lifecycle replaced transaction %s", step)
		}
	}
	if !full {
		if len(toolTransactionIdentities(before)) <= len(initialTransactions) {
			t.Fatal("lifecycle did not add activity")
		}
		resumed := runCLI("resume", "--run", id, "--lifecycle")
		if !reflect.DeepEqual(toolTransactionIdentities(before), toolTransactionIdentities(resumed)) {
			t.Fatal("completed resume changed transactions")
		}
	}
	if full {
		services := []string{"api", "enrich"}
		if phase == "all" {
			services = []string{"api"}
		}
		if _, err := h.project.Run(ctx, append([]string{"restart"}, services...)...); err != nil {
			t.Fatal(err)
		}
		if _, err := h.project.Run(ctx, append([]string{"up", "-d", "--no-build", "--no-deps", "--wait", "--wait-timeout", "180"}, services...)...); err != nil {
			t.Fatal(err)
		}
	}
	runCLI("check", "--run", id)
	after := runCLI("report", "--run", id)
	// DeFi checks reconcile the newest published hash; identity and transactions
	// remain unchanged even when a newer publication refreshes read-only evidence.
	stableScenarios := func(report map[string]any) string {
		var result []any
		for _, item := range report["scenarios"].([]any) {
			s := item.(map[string]any)
			copy := make(map[string]any, len(s))
			for k, v := range s {
				if s["name"] != "defi" || k != "checks" {
					copy[k] = v
				}
			}
			result = append(result, copy)
		}
		return fmt.Sprint(result)
	}
	if stableScenarios(before) != stableScenarios(after) {
		t.Fatal("read-only check changed public scenario outcomes")
	}
	retainReport("final")
	for _, value := range after["scenarios"].([]any) {
		scenario := value.(map[string]any)
		if scenario["status"] != "api_published" {
			t.Fatalf("unpublished scenario: %v", scenario["name"])
		}
	}
	if full {
		h.assertToolsBrowser(ctx, binding, reportPath, phase+"-final")
		// A second run may add fungible balances to the same wallet. The old run
		// must still reconcile its own NFT IDs and the new exact-block totals.
		var recipient string
		for _, item := range report["scenarios"].([]any) {
			s := item.(map[string]any)
			if s["name"] == "defi" {
				recipient = s["entities"].(map[string]any)["recipient"].(string)
			}
		}
		if recipient == "" {
			t.Fatal("missing DeFi recipient")
		}
		repeated := runCLI("run", "defi", "--to", recipient)
		if repeated["id"] == id {
			t.Fatal("repeated run reused identity")
		}
		runCLI("check", "--run", id)
		checked := runCLI("report", "--run", id)
		if !reflect.DeepEqual(toolTransactionIdentities(after), toolTransactionIdentities(checked)) {
			t.Fatal("read-only DeFi recheck changed transaction identities")
		}
		retainReport("repeated-wallet")
	}
}

func (h *harness) assertToolsClearEvidence(ctx context.Context, report map[string]any, phase string) {
	h.t.Helper()
	for _, item := range report["scenarios"].([]any) {
		s := item.(map[string]any)
		if s["name"] != "eip7702" {
			continue
		}
		authority := strings.ToLower(s["entities"].(map[string]any)["authority"].(string))
		for _, item := range s["transactions"].([]any) {
			tx := item.(map[string]any)
			if !strings.HasSuffix(tx["step"].(string), "-clear") {
				continue
			}
			block := tx["receipt"].(map[string]any)["blockHash"].(string)
			var raw json.RawMessage
			if err := h.rpc.CallContext(ctx, &raw, "debug_traceBlockByHash", block, map[string]any{"tracer": "prestateTracer", "tracerConfig": map[string]any{"diffMode": true}}); err != nil {
				h.t.Fatal(err)
			}
			var traces []struct {
				Hash   string `json:"txHash"`
				Result struct {
					Pre map[string]struct {
						Code *string `json:"code"`
					} `json:"pre"`
					Post map[string]struct {
						Code *string `json:"code"`
					} `json:"post"`
				} `json:"result"`
			}
			if err := json.Unmarshal(raw, &traces); err != nil {
				h.t.Fatal(err)
			}
			for _, trace := range traces {
				if !strings.EqualFold(trace.Hash, tx["hash"].(string)) {
					continue
				}
				pre, post := trace.Result.Pre[authority].Code, trace.Result.Post[authority].Code
				if pre == nil || !strings.HasPrefix(*pre, "0xef0100") || post == nil || *post != "0x" {
					h.t.Fatal("clearing must have explicit transaction-bound pre/post code evidence")
				}
				if err := os.WriteFile(filepath.Join(h.artifacts, "tools-"+phase+"-clear-evidence.json"), raw, 0600); err != nil {
					h.t.Fatal(err)
				}
				return
			}
			h.t.Fatal("clearing transaction missing from exact-block trace")
		}
	}
	h.t.Fatal("missing clear-delegation lifecycle transaction")
}

// ADR-0005 forbids the private-network exception in all-role processes. Like
// the runtime BytecodeDB fixture, use an internal, test-owned Docker subnet
// whose addresses pass the production public-address policy. No Internet
// gateway is contacted, and the ordinary Preview gateway route stays intact.
func (h *harness) toolsStrictMetadataGateway(ctx context.Context) {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join(h.root, "preview/Caddyfile"))
	if err != nil {
		h.t.Fatal(err)
	}
	caddy := filepath.Join(h.artifacts, "tools.Caddyfile")
	config := strings.Replace(string(data), "https://ipfs.etherview.localhost {", "https://ipfs.etherview.localhost, https://tools-ipfs.etherview.localhost {", 1)
	if config == string(data) {
		h.t.Fatal("missing maintained IPFS gateway route")
	}
	if err := os.WriteFile(caddy, []byte(config), 0644); err != nil {
		h.t.Fatal(err)
	}
	mount := func(source, target string) map[string]any {
		return map[string]any{"type": "bind", "source": source, "target": target, "read_only": true}
	}
	client := map[string]any{
		"environment": map[string]string{
			"ETHERVIEW_METADATA_UNSAFE_ALLOW_PRIVATE_NETWORKS":    "false",
			"ETHERVIEW_METADATA_IPFS_GATEWAY":                     "https://tools-ipfs.etherview.localhost",
			"ETHERVIEW_METADATA_IMAGE_NAVIGATION_ALLOWED_ORIGINS": "https://tools-ipfs.etherview.localhost",
			"SSL_CERT_FILE": "/run/ipfs-ca/rootCA.pem",
		},
		"networks": map[string]any{"default": nil, "tools-metadata": nil},
		"volumes":  []map[string]any{mount(filepath.Join(h.root, ".local/preview-tls/rootCA.pem"), "/run/ipfs-ca/rootCA.pem")},
	}
	gateway := map[string]any{
		"networks": map[string]any{"default": nil, "tools-metadata": map[string]any{"ipv4_address": "11.255.251.2", "aliases": []string{"tools-ipfs.etherview.localhost"}}},
		"volumes":  []map[string]any{mount(caddy, "/etc/caddy/Caddyfile")},
	}
	override := map[string]any{
		"services": map[string]any{"api": client, "preview-gateway": gateway},
		"networks": map[string]any{"tools-metadata": map[string]any{"internal": true, "ipam": map[string]any{"config": []map[string]string{{"subnet": "11.255.251.0/24"}}}}},
	}
	encoded, err := json.Marshal(override)
	if err != nil {
		h.t.Fatal(err)
	}
	filename := filepath.Join(h.artifacts, "tools-all-metadata.json")
	if err := os.WriteFile(filename, encoded, 0600); err != nil {
		h.t.Fatal(err)
	}
	h.project.Files = append(h.project.Files, filename)
	if _, err := h.project.Run(ctx, "up", "-d", "--no-build", "--no-deps", "--wait", "--wait-timeout", "90", "preview-gateway"); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) assertToolsBrowser(ctx context.Context, binding, report, phase string) {
	h.t.Helper()
	command := exec.CommandContext(ctx, "node", "web/scripts/preview-tools-browser.mjs", binding, filepath.Join(h.root, ".local/preview-tls/gateway.crt"), report, h.artifacts, phase)
	command.Dir = h.root
	if output, err := command.CombinedOutput(); err != nil {
		h.t.Fatalf("tools browser: %v\n%s", err, output)
	}
}

func onlyStaleToolBindings(report map[string]any) bool {
	failures := 0
	for _, value := range report["scenarios"].([]any) {
		s := value.(map[string]any)
		if s["status"] == "api_published" {
			continue
		}
		failures++
		message, _ := s["error"].(string)
		if !strings.HasPrefix(s["name"].(string), "proxy.") || (!strings.Contains(message, "Proxy publication changed during verification") && !strings.Contains(message, "Timed out: proxy verification admission")) {
			return false
		}
	}
	return failures > 0
}
func toolTransactionIdentities(report map[string]any) map[string]string {
	identities := make(map[string]string)
	for _, value := range report["scenarios"].([]any) {
		s := value.(map[string]any)
		for _, raw := range s["transactions"].([]any) {
			transaction := raw.(map[string]any)
			identities[s["name"].(string)+":"+transaction["step"].(string)] = transaction["hash"].(string)
		}
	}
	return identities
}
