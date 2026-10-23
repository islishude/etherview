//go:build runtimee2e

package runtimee2e

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func assertNativeImageArchitecture(t *testing.T, ctx context.Context, docker, image string) {
	t.Helper()
	inspect := exec.CommandContext(ctx, docker, "image", "inspect", "--format", "{{.Architecture}}", image)
	imageOutput, err := inspect.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect image architecture %q: %v\n%s", image, err, strings.TrimSpace(string(imageOutput)))
	}
	info := exec.CommandContext(ctx, docker, "info", "--format", "{{.Architecture}}")
	hostOutput, err := info.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect Docker host architecture: %v\n%s", err, strings.TrimSpace(string(hostOutput)))
	}
	normalize := func(value string) string {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "amd64", "x86_64", "x86-64":
			return "amd64"
		case "arm64", "aarch64":
			return "arm64"
		default:
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	imageArchitecture := normalize(string(imageOutput))
	hostArchitecture := normalize(string(hostOutput))
	if imageArchitecture == "" || hostArchitecture == "" || imageArchitecture != hostArchitecture {
		t.Fatalf("image %q architecture %q does not match Docker host architecture %q",
			image, imageArchitecture, hostArchitecture)
	}
}
