//go:build previewgatewaye2e

package previewgateway

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestInvalidCertificatesFailStartup(t *testing.T) {
	cert, _, _ := certificate(t)
	_, otherKey, _ := certificate(t)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := os.Getenv("DOCKER")
	if binary == "" {
		binary = "docker"
	}
	image := os.Getenv("PREVIEW_GATEWAY_IMAGE")
	if image == "" {
		image = "caddy:2.11.7-alpine"
	}
	for _, test := range []struct{ name, key string }{{"mismatch", otherKey}, {"invalid", cert}} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "run", "--rm", "--network", "none",
				"-v", root+"/deploy/preview/Caddyfile:/etc/caddy/Caddyfile:ro",
				"-v", cert+":/run/preview-tls/gateway.crt:ro",
				"-v", test.key+":/run/preview-tls/gateway.key:ro", image,
				"caddy", "validate", "--config", "/etc/caddy/Caddyfile")
			output, runErr := command.CombinedOutput()
			if runErr == nil || ctx.Err() != nil {
				t.Fatalf("invalid key must fail promptly: %v %s", runErr, output)
			}
		})
	}
}
