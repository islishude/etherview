package verify

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/islishude/etherview/internal/compilerbundle"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestWasmVyperCompilerPinnedIdentity(t *testing.T) {
	path := os.Getenv("WASM_EXECUTOR_TEST_PATH")
	if path == "" {
		path = "../../.local/wasm/runtime/etherview-wasm"
		if _, err := os.Stat(path); err != nil {
			t.Skip("requires built WASM runtime; run make compiler-install")
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	compiler := &WasmCompiler{Path: path, Vyper: true, Timeout: time.Minute, MaxInputBytes: 5 << 20, MaxOutputBytes: 64 << 20}
	ctx := context.Background()
	if err = compiler.ValidateRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := compiler.Resolve(ctx, LanguageVyper, VyperCompilerVersion)
	if err != nil {
		t.Fatal(err)
	}
	if !p.valid() || p.Kind != CompilerVyper || p.ExecutorKind != WasmExecutorKind || p.ExecutionPolicy != WasmSubprocessPolicy || p.CatalogGeneration != 0 {
		t.Fatalf("invalid provenance: %#v", p)
	}
	input, err := os.ReadFile("testdata/compiler/vyper/plain.input.json")
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.CompilePinned(ctx, LanguageVyper, VyperCompilerVersion, p, input)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := os.ReadFile("testdata/compiler/vyper/plain.input.output.json")
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if json.Unmarshal(result, &got) != nil || json.Unmarshal(reference, &want) != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("compiler result differs from reference")
	}
	old := p
	old.ExecutorKind = VyperExecutorKind
	old.ExecutionPolicy = TrustedSubprocessPolicy
	if _, err = compiler.CompilePinned(ctx, LanguageVyper, VyperCompilerVersion, old, input); !errors.Is(err, ErrCompilerProvenanceConflict) {
		t.Fatalf("accepted old executor identity: %v", err)
	}
	compiler.mu.Lock()
	compiler.digest[0] ^= 1
	compiler.mu.Unlock()
	changed, err := compiler.Resolve(ctx, LanguageVyper, VyperCompilerVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = compiler.CompilePinned(ctx, LanguageVyper, VyperCompilerVersion, changed, input); !errors.Is(err, compilerbundle.ErrChanged) || compiler.Ready() {
		t.Fatalf("runtime change not fenced: %v", err)
	}
}
