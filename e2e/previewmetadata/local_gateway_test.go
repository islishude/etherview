//go:build previewmetadatae2e

package previewmetadatae2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (h *harness) assertLocalGateway(ctx context.Context) {
	h.t.Helper()
	h.t.Log("validate offline Kubo and IPFS tool")
	api, err := h.project.Port(ctx, "ipfs", 5001)
	if err != nil {
		h.t.Fatal(err)
	}
	h.ipfsAPI = "http://" + api
	h.ipfsBinary = filepath.Join(h.t.TempDir(), "ipfs")
	build := exec.CommandContext(ctx, valueOrDefault("GO", "go"), "build", "-o", h.ipfsBinary, "./cmd/ipfs")
	build.Dir = h.root
	if output, err := build.CombinedOutput(); err != nil {
		h.t.Fatalf("build IPFS tool: %v\n%s", err, output)
	}
	config := h.ipfsCommand(ctx, "config", "Gateway.NoFetch")
	if strings.TrimSpace(config) != "true" {
		h.t.Fatalf("offline NoFetch = %q", config)
	}
	container := h.serviceContainer(ctx, "ipfs")
	command := commandOutput(ctx, h.root, dockerCommand(), "inspect", "--format", "{{json .Config.Cmd}}", container)
	if !strings.Contains(command, `"--offline"`) {
		h.t.Fatalf("Kubo is not offline: %s", command)
	}
	h.assertToolRoundtrip(ctx)
	h.assertGatewayTLS(ctx)
	// Initialization and seeding must work again against the same repo. Restart
	// the proxy too so its upstream DNS is refreshed after any container change.
	if _, err := h.project.Run(ctx, "restart", "ipfs", "ipfs-gateway"); err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.project.Run(ctx, "up", "-d", "--no-build", "--wait", "--wait-timeout", "120", "ipfs", "ipfs-gateway"); err != nil {
		h.t.Fatal(err)
	}
	// Docker may assign new ephemeral host ports when restarting a container.
	api, err = h.project.Port(ctx, "ipfs", 5001)
	if err != nil {
		h.t.Fatal(err)
	}
	h.ipfsAPI = "http://" + api
	h.t.Log("validate Kubo persistence after restart")
	h.assertToolRoundtrip(ctx)
	h.assertGatewayTLS(ctx)
	h.captureGatewayIPs(ctx)
}

func (h *harness) assertToolRoundtrip(ctx context.Context) {
	h.t.Helper()
	fixture := filepath.Join(h.root, "e2e", "previewmetadata", "testdata", "metadata.json")
	wantCID := strings.TrimSuffix(strings.TrimPrefix(metadataURI, "ipfs://"), "/metadata.json")
	for range 2 {
		got := h.tool(ctx, "upload", "--wrap", "--api", h.ipfsAPI, fixture)
		if got != wantCID {
			h.t.Fatalf("wrapped fixture CID = %s, want %s", got, wantCID)
		}
	}
	output := filepath.Join(h.t.TempDir(), "metadata.json")
	h.tool(ctx, "download", "--api", h.ipfsAPI, "--output", output, metadataURI)
	data, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(data, expectedMetadata) {
		h.t.Fatalf("IPFS metadata roundtrip mismatch: %v", err)
	}
	for index, content := range [][]byte{nil, {0, 255, 128, 0, 1}, bytes.Repeat([]byte{0, 255, 128, 1}, 1<<18)} {
		dir := h.t.TempDir()
		input := filepath.Join(dir, "input.bin")
		if err := os.WriteFile(input, content, 0o600); err != nil {
			h.t.Fatal(err)
		}
		gotCID := h.tool(ctx, "upload", "--api", h.ipfsAPI, input)
		output := filepath.Join(dir, "output.bin")
		h.tool(ctx, "download", "--api", h.ipfsAPI, "--output", output, gotCID)
		got, err := os.ReadFile(output)
		if err != nil || !bytes.Equal(got, content) {
			h.t.Fatalf("IPFS binary roundtrip %d mismatch: %v", index, err)
		}
	}
	// The image CID exists in the fixture but its data is deliberately not seeded.
	missing := strings.TrimPrefix(resolvedImageURL, metadataGateway+"/ipfs/")
	command := exec.CommandContext(ctx, h.ipfsBinary, "download", "--api", h.ipfsAPI,
		"--timeout", "10s", "--output", filepath.Join(h.t.TempDir(), "missing"), missing)
	outputBytes, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(outputBytes), "RPC returned HTTP") {
		h.t.Fatalf("offline unknown CID must fail at Kubo, not timeout: %v %s", err, outputBytes)
	}
}

func (h *harness) assertGatewayTLS(ctx context.Context) {
	h.t.Helper()
	binding, err := h.project.Port(ctx, "ipfs-gateway", 8443)
	if err != nil {
		h.t.Fatal(err)
	}
	transport := h.http.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = "ipfs.preview.test"
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	path := strings.TrimPrefix(resolvedMetadataURL, metadataGateway)
	for _, test := range []struct {
		path   string
		status int
	}{{path, http.StatusOK}, {"/api/v0/version", http.StatusNotFound}} {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+binding+test.path, nil)
		if err != nil {
			h.t.Fatal(err)
		}
		request.Host = "ipfs.preview.test"
		response, err := client.Do(request)
		if err != nil {
			h.t.Fatal(err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != test.status {
			h.t.Fatalf("gateway %s: status=%d error=%v", test.path, response.StatusCode, readErr)
		}
		if test.status == http.StatusOK && (!bytes.Equal(body, expectedMetadata) || response.Header.Get("Content-Type") != "application/json") {
			h.t.Fatalf("gateway content mismatch: %s %q", response.Header.Get("Content-Type"), body)
		}
	}
	untrusted := transport.Clone()
	untrusted.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "ipfs.preview.test", RootCAs: x509.NewCertPool()}
	defer untrusted.CloseIdleConnections()
	untrustedClient := &http.Client{Transport: untrusted, Timeout: 5 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+binding+path, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	response, err := untrustedClient.Do(request)
	if err == nil {
		_ = response.Body.Close()
		h.t.Fatal("gateway accepted an untrusted certificate")
	}
	if _, ok := errors.AsType[x509.UnknownAuthorityError](err); !ok {
		h.t.Fatalf("expected untrusted CA rejection, got %v", err)
	}
}

func (h *harness) captureGatewayIPs(ctx context.Context) {
	h.t.Helper()
	container := h.serviceContainer(ctx, "ipfs-gateway")
	output := commandOutput(ctx, h.root, dockerCommand(), "inspect", "--format", "{{json .NetworkSettings.Networks}}", container)
	var networks map[string]struct {
		IPAddress         string
		GlobalIPv6Address string
	}
	if err := json.Unmarshal([]byte(output), &networks); err != nil {
		h.t.Fatal(err)
	}
	h.gatewayIPs = nil
	for _, network := range networks {
		for _, ip := range []string{network.IPAddress, network.GlobalIPv6Address} {
			if ip != "" {
				h.gatewayIPs = append(h.gatewayIPs, ip)
			}
		}
	}
	if len(h.gatewayIPs) == 0 {
		h.t.Fatal("owned gateway has no addresses")
	}
}

func (h *harness) serviceContainer(ctx context.Context, service string) string {
	h.t.Helper()
	output, err := h.project.Run(ctx, "ps", "-q", service)
	if err != nil || len(strings.Fields(string(output))) != 1 {
		h.t.Fatalf("locate %s: %v %s", service, err, output)
	}
	return strings.TrimSpace(string(output))
}

func (h *harness) serviceImageID(ctx context.Context, service string) string {
	return strings.TrimSpace(commandOutput(ctx, h.root, dockerCommand(), "inspect", "--format", "{{.Image}}", h.serviceContainer(ctx, service)))
}

func (h *harness) ipfsCommand(ctx context.Context, args ...string) string {
	h.t.Helper()
	output, err := h.project.Run(ctx, append([]string{"exec", "-T", "ipfs", "ipfs"}, args...)...)
	if err != nil {
		h.t.Fatalf("Kubo command: %v", err)
	}
	return string(output)
}

func (h *harness) tool(ctx context.Context, args ...string) string {
	h.t.Helper()
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, h.ipfsBinary, args...)
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		h.t.Fatalf("IPFS %s: %v: %s", args[0], err, &stderr)
	}
	return strings.TrimSpace(string(output))
}
