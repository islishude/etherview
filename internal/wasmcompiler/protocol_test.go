package wasmcompiler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProtocolRejectsUnboundedOrGenericInvocations(t *testing.T) {
	for _, args := range [][]string{nil, {"--eval", "print(1)"}, {"--compile"}, {"--self-test", "extra"}, {"--compile", "python", "/tmp/a", "v", "00", "1", "1", "1"}, {"--compile", "solc", "relative", "v", "00", "1", "1", "1"}} {
		var out bytes.Buffer
		if err := Run(context.Background(), args, bytes.NewReader(nil), &out); !errors.Is(err, ErrInvocation) || out.Len() != 0 {
			t.Fatalf("args %v: %v %q", args, err, out.String())
		}
	}
	for _, raw := range []string{"0", "-1", "+1", "01", "1.0", "9223372036854775808"} {
		if _, err := protocolLimit(raw, 100); err == nil {
			t.Fatalf("accepted limit %q", raw)
		}
	}
}
func TestProtocolSelfTest(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--self-test"}, bytes.NewReader(nil), &out); err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Schema, Wazero string
		MemoryPages    int `json:"memory_pages"`
	}
	if json.Unmarshal(out.Bytes(), &reply) != nil || reply.Schema != SelfTestSchema || reply.Wazero != WazeroVersion || reply.MemoryPages != 8192 {
		t.Fatalf("self test: %s", out.Bytes())
	}
}
func TestProtocolInputLimitBeforeArtifactAccess(t *testing.T) {
	var out bytes.Buffer
	args := []string{"--compile", "solc", "/missing/artifact", "v", "0000000000000000000000000000000000000000000000000000000000000000", "1", "1", "1000"}
	if err := Run(context.Background(), args, bytes.NewReader([]byte("{}")), &out); !errors.Is(err, ErrInputLimit) || out.Len() != 0 {
		t.Fatalf("input limit: %v", err)
	}
}
func TestArtifactRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "link")
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readArtifact(link); !errors.Is(err, ErrArtifact) {
		t.Fatalf("symlink: %v", err)
	}
}
