package wasmcompiler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractRejectsInvalid(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte(`eval('x')`), []byte(`var x="data:application/octet-stream;base64,AGFzbQEAAAA=";`)} {
		if _, err := Extract(context.Background(), raw, sha256.Sum256(raw)); err == nil {
			t.Fatal("accepted malformed artifact")
		}
	}
}
func TestExtractCatalog(t *testing.T) {
	root := os.Getenv("SOLC_BASELINE_DIR")
	if root == "" {
		t.Skip("requires authenticated compiler baseline artifacts")
	}
	data, err := os.ReadFile("../../compiler/wasm/solc-baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Builds []struct {
			Path   string
			SHA256 string `json:"sha256"`
		}
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	for _, build := range catalog.Builds {
		t.Run(build.Path, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, build.Path))
			if err != nil {
				t.Fatal(err)
			}
			digest, e := hex.DecodeString(build.SHA256[2:])
			if e != nil || len(digest) != 32 {
				t.Fatal("invalid catalog checksum")
			}
			a, err := Extract(context.Background(), raw, [32]byte(digest))
			if err != nil {
				t.Fatal(err)
			}
			if len(a.Binary) == 0 || len(a.Imports) == 0 || len(a.Exports) == 0 {
				t.Fatal("empty artifact")
			}
		})
	}
}
func TestExtractLocalModern(t *testing.T) {
	for _, name := range []string{"../../compiler/node_modules/solc/soljson.js", "../../e2e/hardhat3/node_modules/solc/soljson.js"} {
		raw, err := os.ReadFile(name)
		if os.IsNotExist(err) {
			t.Skip("local reference not installed")
		}
		if err != nil {
			t.Fatal(err)
		}
		a, err := Extract(context.Background(), raw, sha256.Sum256(raw))
		if err != nil {
			t.Fatal(err)
		}
		if a.Exports["_solidity_compile"] == "" {
			t.Fatal("missing compiler binding")
		}
	}
}

func TestInspectOldABI(t *testing.T) {
	root := os.Getenv("SOLC_BASELINE_DIR")
	if root == "" {
		t.Skip("requires compiler baseline")
	}
	raw, err := os.ReadFile(filepath.Join(root, "solc-emscripten-wasm32-v0.4.0+commit.acd334c9.js"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Extract(context.Background(), raw, sha256.Sum256(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, im := range a.Module.ImportSection {
		t.Logf("%s.%s type=%d binding=%s", im.Module, im.Name, im.Type, a.Imports[im.Name])
	}
}

func TestUnsupportedImportsAreNotStubbed(t *testing.T) {
	for _, name := range []string{"unknown", "___cxa_invented", "___cxa_find_matching_catch_100", "invoke_shell", "jsCall_x", "___syscall999", "10"} {
		if knownHost(name) {
			t.Fatalf("accepted unknown host %s", name)
		}
	}
}
func TestArtifactAuthenticationAndCancellation(t *testing.T) {
	raw, err := os.ReadFile("../../compiler/node_modules/solc/soljson.js")
	if os.IsNotExist(err) {
		t.Skip("reference compiler not installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Extract(context.Background(), raw, [32]byte{}); err == nil {
		t.Fatal("accepted incorrect checksum")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Extract(ctx, raw, sha256.Sum256(raw)); err != context.Canceled {
		t.Fatalf("cancelled extraction: %v", err)
	}
}

func TestCatalogHostCoverage(t *testing.T) {
	root := os.Getenv("SOLC_BASELINE_DIR")
	if root == "" {
		t.Skip("requires authenticated compiler baseline")
	}
	data, err := os.ReadFile("../../compiler/wasm/solc-baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Builds []struct{ Path, SHA256 string }
	}
	if err = json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	unknown := map[string]bool{}
	for _, b := range catalog.Builds {
		raw, err := os.ReadFile(filepath.Join(root, b.Path))
		if err != nil {
			t.Error(err)
			continue
		}
		digest, err := hex.DecodeString(b.SHA256[2:])
		if err != nil || len(digest) != 32 {
			t.Fatal("catalog checksum")
		}
		a, err := Extract(context.Background(), raw, [32]byte(digest))
		if err != nil {
			t.Errorf("%s: %v", b.Path, err)
			continue
		}
		for _, im := range a.Module.ImportSection {
			if im.Type != 0 {
				continue
			}
			name := a.Imports[im.Name]
			if name == "" {
				name = im.Name
			}
			if !knownHost(name) {
				unknown[name] = true
			}
		}
	}
	for name := range unknown {
		t.Errorf("unsupported host %s", name)
	}
}
