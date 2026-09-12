package verify

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVyperArchivePythonProducerRoundTrip(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"etherview-vyper": "fixture helper\n", "runtime-manifest.json": "{}\n"}
	for name, contents := range files {
		mode := os.FileMode(0o444)
		if name == "etherview-vyper" {
			mode = 0o555
		}
		if err := os.WriteFile(filepath.Join(source, name), []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(root, "runtime.tar.gz")
	producer, err := filepath.Abs("../../compiler/vyper/matrix.py")
	if err != nil {
		t.Fatal(err)
	}
	python := os.Getenv("PYTHON")
	if python == "" {
		python = "python3"
	}
	const script = `import importlib.util, pathlib, sys
spec = importlib.util.spec_from_file_location("vyper_matrix", sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
module.pack_runtime(pathlib.Path(sys.argv[2]), pathlib.Path(sys.argv[3]))
`
	output, err := exec.CommandContext(t.Context(), python, "-B", "-c", script, producer, source, archive).CombinedOutput()
	if err != nil {
		t.Fatalf("Python release producer: %v\n%s", err, output)
	}
	target := t.TempDir()
	t.Cleanup(func() {
		if err := removeVyperStaging(target); err != nil {
			t.Error(err)
		}
	})
	if err := extractVyperArchive(archive, target); err != nil {
		t.Fatal(err)
	}
	for name, expected := range files {
		path := filepath.Join(target, name)
		contents, err := os.ReadFile(path)
		if err != nil || string(contents) != expected {
			t.Fatalf("file %s: contents=%q error=%v", name, contents, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o444)
		if name == "etherview-vyper" {
			mode = 0o555
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("file %s: mode=%o", name, info.Mode().Perm())
		}
	}
}

func TestVyperArchivePaddingAndGzipIntegrity(t *testing.T) {
	var raw bytes.Buffer
	writer := tar.NewWriter(&raw)
	for _, name := range []string{"etherview-vyper", "runtime-manifest.json"} {
		if err := writer.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o444, Size: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	nonzero := make([]byte, 512)
	nonzero[128] = 1
	for _, test := range []struct {
		name    string
		padding []byte
		damage  string
		valid   bool
	}{
		{name: "no padding", valid: true},
		{name: "two KiB padding", padding: make([]byte, 2048), valid: true},
		{name: "maximum record padding", padding: make([]byte, 9728), valid: true},
		{name: "excessive padding", padding: make([]byte, 10240)},
		{name: "nonzero trailer", padding: nonzero},
		{name: "partial block", padding: make([]byte, 1)},
		{name: "corrupt CRC", padding: make([]byte, 9728), damage: "crc"},
		{name: "truncated gzip trailer", padding: make([]byte, 2048), damage: "truncate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var encoded bytes.Buffer
			compressed := gzip.NewWriter(&encoded)
			if _, err := io.Copy(compressed, bytes.NewReader(raw.Bytes())); err != nil {
				t.Fatal(err)
			}
			if _, err := compressed.Write(test.padding); err != nil {
				t.Fatal(err)
			}
			if err := compressed.Close(); err != nil {
				t.Fatal(err)
			}
			contents := encoded.Bytes()
			switch test.damage {
			case "crc":
				contents[len(contents)-8] ^= 1
			case "truncate":
				contents = contents[:len(contents)-1]
			}
			archive := filepath.Join(t.TempDir(), "runtime.tar.gz")
			if err := os.WriteFile(archive, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			target := t.TempDir()
			t.Cleanup(func() {
				if err := removeVyperStaging(target); err != nil {
					t.Error(err)
				}
			})
			err := extractVyperArchive(archive, target)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t error=%v", test.valid, err)
			}
		})
	}
}
