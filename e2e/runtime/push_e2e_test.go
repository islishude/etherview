//go:build runtimee2e

package runtimee2e

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/islishude/etherview/internal/api/gen"
)

// setupPushReceiver preserves the production sender's public-IP and TLS checks.
// The synthetic address is reachable only inside this test-owned Docker network.
func (h *harness) setupPushReceiver(ctx context.Context) {
	h.t.Helper()
	cert, key, roots := writeRuntimeTLSKeyPair(h.t)
	h.t.Cleanup(func() { h.cleanup() })
	directory := filepath.Dir(cert)
	private, public, e := webpush.GenerateVAPIDKeys()
	if e != nil {
		h.t.Fatal(e)
	}
	browser, e := ecdh.P256().GenerateKey(rand.Reader)
	if e != nil {
		h.t.Fatal(e)
	}
	auth := make([]byte, 16)
	if _, e = rand.Read(auth); e != nil {
		h.t.Fatal(e)
	}
	payload, _ := json.Marshal(map[string]string{"Private": base64.RawURLEncoding.EncodeToString(browser.Bytes()), "Auth": base64.RawURLEncoding.EncodeToString(auth)})
	if e = os.WriteFile(filepath.Join(directory, "browser.json"), payload, 0o444); e != nil {
		h.t.Fatal(e)
	}
	binary := filepath.Join(directory, "receiver")
	command := exec.CommandContext(ctx, "go", "build", "-o", binary, "./e2e/runtime/pushreceiver")
	command.Dir = h.root
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if output, e := command.CombinedOutput(); e != nil {
		h.t.Fatalf("build push receiver: %v %s", e, output)
	}
	sender := map[string]any{"environment": map[string]string{"ETHERVIEW_PUSH_ENABLED": "true", "ETHERVIEW_PUSH_PUBLIC_KEY": public, "ETHERVIEW_PUSH_PRIVATE_KEY": private, "ETHERVIEW_PUSH_CONTACT": "mailto:runtime@example.com", "SSL_CERT_FILE": "/fixture/tls.crt"}, "networks": map[string]any{"default": nil, "push-test": nil}, "volumes": []map[string]any{{"type": "bind", "source": cert, "target": "/fixture/tls.crt", "read_only": true}}}
	receiver := map[string]any{"image": h.project.Env["ETHERVIEW_IMAGE"], "entrypoint": []string{"/fixture/receiver"}, "command": []string{}, "user": "65532:65532", "read_only": true, "restart": "no", "networks": map[string]any{"default": nil, "push-test": map[string]any{"ipv4_address": "11.255.254.2", "aliases": []string{"push.example"}}}, "ports": []map[string]any{{"target": 443, "published": "0", "host_ip": "127.0.0.1", "protocol": "tcp"}}, "volumes": []map[string]any{}}
	mounts := []map[string]any{}
	for _, name := range []string{cert, key, binary, filepath.Join(directory, "browser.json")} {
		mounts = append(mounts, map[string]any{"type": "bind", "source": name, "target": "/fixture/" + filepath.Base(name), "read_only": true})
	}
	receiver["volumes"] = mounts
	services := map[string]any{"runtime-push": receiver, "etherview": sender, "maintenance": sender, "api": map[string]any{"environment": map[string]string{"ETHERVIEW_PUSH_ENABLED": "true", "ETHERVIEW_PUSH_PUBLIC_KEY": public, "ETHERVIEW_PUSH_CONTACT": "mailto:runtime@example.com"}}}
	override := map[string]any{"services": services, "networks": map[string]any{"push-test": map[string]any{"internal": true, "ipam": map[string]any{"config": []map[string]string{{"subnet": "11.255.254.0/24"}}}}}}
	encoded, _ := json.Marshal(override)
	filename := filepath.Join(directory, "push.compose.json")
	if e = os.WriteFile(filename, encoded, 0o600); e != nil {
		h.t.Fatal(e)
	}
	h.project.Files = append(h.project.Files, filename)
	h.secrets = append(h.secrets, private)
	if e = h.project.Up(ctx, "runtime-push"); e != nil {
		h.t.Fatal(e)
	}
	binding, e := h.project.Port(ctx, "runtime-push", 443)
	if e != nil {
		h.t.Fatal(e)
	}
	h.pushURL = "https://" + binding
	h.pushHTTP = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	h.pushInput = gen.PushSubscriptionInput{Endpoint: "https://push.example/push", P256dh: base64.RawURLEncoding.EncodeToString(browser.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(auth), Language: "en"}
}
func (h *harness) pushMessages(ctx context.Context) []struct{ Path, Body, Tag string } {
	h.t.Helper()
	request, _ := http.NewRequestWithContext(ctx, "GET", h.pushURL+"/messages", nil)
	response, e := h.pushHTTP.Do(request)
	if e != nil {
		h.t.Fatal(e)
	}
	defer func() { _ = response.Body.Close() }()
	var messages []struct{ Path, Body, Tag string }
	if e = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&messages); e != nil {
		h.t.Fatal(e)
	}
	return messages
}
func (h *harness) assertRuntimeWatch(ctx context.Context, hash string, canonical bool) {
	h.t.Helper()
	count := func() int {
		total := 0
		for _, m := range h.pushMessages(ctx) {
			if strings.EqualFold(m.Path, "/tx/"+hash) && strings.Contains(m.Tag, h.fixture.orphanHash) {
				total++
			}
		}
		return total
	}
	if canonical {
		waitFor(h.t, ctx, "one-shot encrypted Web Push", func() (bool, string, error) { n := count(); return n == 1, "push delivery", nil })
		return
	}
	if n := count(); n != 1 {
		h.t.Fatalf("reorg/restart resent historical push: %d", n)
	}
}
