package geascompiler

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileSupportsRelativeIncludesAndNestedAssemble(t *testing.T) {
	t.Parallel()
	sources := map[string]string{
		"system/main.eas": `#pragma target "prague"
#include "../common/value.eas"
push VALUE
`,
		"system/ctor.eas": `push len(code)
dup1
push @code
push 0
codecopy
push 0
return
#bytes code: assemble("main.eas")
`,
		"common/value.eas": "#define VALUE = 1\n",
		"unused.eas":       "push 2\n",
	}
	runtime, err := Compile(Request{
		Schema: ProtocolSchema, Sources: sources, Entrypoint: "system/main.eas",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.Successful || runtime.Bytecode != "0x6001" {
		t.Fatalf("runtime = %+v", runtime)
	}
	if got, want := strings.Join(runtime.Sources, ","), "common/value.eas,system/main.eas"; got != want {
		t.Fatalf("runtime sources = %q, want %q", got, want)
	}
	creation, err := Compile(Request{
		Schema: ProtocolSchema, Sources: sources, Entrypoint: "system/ctor.eas",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !creation.Successful || creation.Bytecode != "0x60028060095f395ff36001" {
		t.Fatalf("creation = %#v", creation)
	}
	if got, want := strings.Join(creation.Sources, ","), "common/value.eas,system/ctor.eas,system/main.eas"; got != want {
		t.Fatalf("creation sources = %q, want %q", got, want)
	}
}

func TestCompilePinnedSysAsmEIP7002Fixture(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "verify", "testdata", "geas", "sys-asm-eip7002")
	sources := make(map[string]string)
	for _, name := range []string{
		"withdrawals/main.eas",
		"withdrawals/ctor.eas",
		"common/fake_expo.eas",
		"common/mstore.eas",
	} {
		content, err := os.ReadFile(filepath.Join(root, "src", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		sources[name] = string(content)
	}
	for entrypoint, bytecodeName := range map[string]string{
		"withdrawals/main.eas": "main.hex",
		"withdrawals/ctor.eas": "ctor.hex",
	} {
		t.Run(filepath.Base(entrypoint), func(t *testing.T) {
			t.Parallel()
			expected, err := os.ReadFile(filepath.Join(root, "bytecode", "withdrawals", bytecodeName))
			if err != nil {
				t.Fatal(err)
			}
			response, err := Compile(Request{
				Schema: ProtocolSchema, Sources: sources, Entrypoint: entrypoint,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !response.Successful || response.Bytecode != "0x"+strings.TrimSpace(string(expected)) {
				t.Fatalf("compiled %s differs from pinned sys-asm bytecode", entrypoint)
			}
			wantSources := "common/fake_expo.eas,common/mstore.eas,withdrawals/main.eas"
			if entrypoint == "withdrawals/ctor.eas" {
				wantSources = "common/fake_expo.eas,common/mstore.eas,withdrawals/ctor.eas,withdrawals/main.eas"
			}
			if got := strings.Join(response.Sources, ","); got != wantSources {
				t.Fatalf("transitive sources = %q, want %q", got, wantSources)
			}
		})
	}
}

func TestCompileRejectsUnsafeOrExternalSources(t *testing.T) {
	t.Parallel()
	for _, request := range []Request{
		{Schema: ProtocolSchema, Sources: map[string]string{"../main.eas": "push 1"}, Entrypoint: "../main.eas"},
		{Schema: ProtocolSchema, Sources: map[string]string{"/main.eas": "push 1"}, Entrypoint: "/main.eas"},
		{Schema: ProtocolSchema, Sources: map[string]string{"dir\\main.eas": "push 1"}, Entrypoint: "dir\\main.eas"},
		{Schema: ProtocolSchema, Sources: map[string]string{"bad\nmain.eas": "push 1"}, Entrypoint: "bad\nmain.eas"},
		{Schema: ProtocolSchema, Sources: map[string]string{"main.eas": "#include \"missing.eas\""}, Entrypoint: "main.eas"},
	} {
		response, err := Compile(request)
		if err == nil && response.Successful {
			t.Fatalf("unsafe request compiled: %+v", request)
		}
	}
}

func TestRunUsesStrictBoundedProtocol(t *testing.T) {
	t.Parallel()
	request := []byte(`{"schema":"etherview-geas-compiler-v1","sources":{"main.eas":"push 1"},"entrypoint":"main.eas"}`)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--compile"}, bytes.NewReader(request), &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || !response.Successful || response.Bytecode != "0x6001" {
		t.Fatalf("response=%+v error=%v", response, err)
	}

	stdout.Reset()
	stderr.Reset()
	unknown := []byte(`{"schema":"etherview-geas-compiler-v1","sources":{"main.eas":"push 1"},"entrypoint":"main.eas","path":"/tmp/main.eas"}`)
	if code := Run([]string{"--compile"}, bytes.NewReader(unknown), &stdout, &stderr); code == 0 {
		t.Fatal("unknown protocol member was accepted")
	}
	if stdout.Len() != 0 || stderr.String() != "compiler request failed\n" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
