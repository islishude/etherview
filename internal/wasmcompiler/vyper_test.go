package wasmcompiler

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/islishude/etherview/internal/compilerbundle"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestVyperWasmFixtures(t *testing.T) {
	path := os.Getenv("VYPER_WASM_RUNTIME_PATH")
	if path == "" {
		path = "../../.local/wasm/runtime/etherview-wasm"
		if _, err := os.Stat(path); err != nil {
			t.Skip("requires source-built Vyper WASM runtime; run make compiler-install")
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := compilerbundle.Validate(path)
	if err != nil {
		t.Fatal(err)
	}
	runner := compilerbundle.Runner{Path: path, Timeout: time.Minute, MaxInputBytes: 5 << 20, MaxOutputBytes: 64 << 20}
	out, err := runner.Execute(context.Background(), identity.Digest, []string{"--self-test", "vyper"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var self struct {
		Schema, Version, Python string
		AccessDenied            bool `json:"access_denied"`
	}
	if json.Unmarshal(out, &self) != nil || self.Schema != "etherview-wazero-vyper-self-test-v1" || self.Version != "0.4.3" || self.Python != "3.13.15" || !self.AccessDenied {
		t.Fatalf("self-test: %s", out)
	}
	files, err := filepath.Glob("../verify/testdata/compiler/vyper/*.json")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, file := range files {
		if strings.Contains(file, ".output.") {
			continue
		}
		reference := strings.TrimSuffix(file, ".json") + ".output.json"
		if _, err := os.Stat(reference); os.IsNotExist(err) {
			continue
		}
		count++
		t.Run(filepath.Base(file), func(t *testing.T) {
			input, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			ref, err := os.ReadFile(reference)
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"--compile", "vyper", filepath.Dir(path), compilerbundle.VyperVersion, compilerbundle.VyperWheelSHA256, strconv.Itoa(runner.MaxInputBytes), strconv.Itoa(runner.MaxOutputBytes), "60000"}
			actual, err := runner.Execute(context.Background(), identity.Digest, args, input)
			if err != nil {
				t.Fatal(err)
			}
			var a, b any
			if json.Unmarshal(actual, &a) != nil || json.Unmarshal(ref, &b) != nil {
				t.Fatal("invalid output JSON")
			}
			if !reflect.DeepEqual(a, b) {
				t.Fatal(jsonDifference("$", a, b))
			}
		})
	}
	if count != 22 {
		t.Fatalf("fixture count = %d, expected 22", count)
	}
}

func TestVyperWasmErrors(t *testing.T) {
	path := os.Getenv("VYPER_WASM_RUNTIME_PATH")
	if path == "" {
		path = "../../.local/wasm/runtime/etherview-wasm"
		if _, err := os.Stat(path); err != nil {
			t.Skip("requires source-built Vyper WASM runtime; run make compiler-install")
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := compilerbundle.Validate(path)
	if err != nil {
		t.Fatal(err)
	}
	runner := compilerbundle.Runner{Path: path, Timeout: time.Minute, MaxInputBytes: 5 << 20, MaxOutputBytes: 64 << 20}
	raw, err := os.ReadFile("../verify/testdata/compiler/vyper/plain.input.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"@external\ndef broken( -> uint256:\n    return 1\n", "import absent\n"} {
		var input map[string]any
		if json.Unmarshal(raw, &input) != nil {
			t.Fatal("fixture")
		}
		input["sources"].(map[string]any)["A.vy"].(map[string]any)["content"] = source
		request, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		command := exec.CommandContext(ctx, "../../.local/vyper/venv/bin/python", "-c", `import json,sys; from vyper.cli.vyper_json import compile_json,exc_handler_to_dict; print(json.dumps(compile_json(json.load(sys.stdin),exc_handler=exc_handler_to_dict),default=str))`)
		command.Stdin = bytes.NewReader(request)
		reference, refErr := command.Output()
		if refErr != nil {
			cancel()
			t.Fatal(refErr)
		}
		args := []string{"--compile", "vyper", filepath.Dir(path), compilerbundle.VyperVersion, compilerbundle.VyperWheelSHA256, "5242880", "67108864", "60000"}
		output, runErr := runner.Execute(ctx, identity.Digest, args, request)
		cancel()
		if runErr != nil {
			t.Fatal(runErr)
		}
		var a, b any
		if json.Unmarshal(output, &a) != nil || json.Unmarshal(reference, &b) != nil {
			t.Fatal("invalid compiler JSON")
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatal(jsonDifference("$", a, b))
		}
	}
	args := []string{"--compile", "vyper", filepath.Dir(path), "0.4.2", compilerbundle.VyperWheelSHA256, "5242880", "67108864", "60000"}
	if out, err := runner.Execute(context.Background(), identity.Digest, args, raw); err == nil || len(out) != 0 {
		t.Fatal("accepted mismatched Vyper version")
	}
	args[3] = "0.4.3"
	args[6] = "16"
	if out, err := runner.Execute(context.Background(), identity.Digest, args, raw); err == nil || len(out) != 0 {
		t.Fatal("accepted oversized Vyper output")
	}
}
