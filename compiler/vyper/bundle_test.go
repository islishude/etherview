package vyper

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wheelFixture(t *testing.T, names []string, mode fs.FileMode) []byte {
	t.Helper()
	var raw bytes.Buffer
	w := zip.NewWriter(&raw)
	for _, name := range names {
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		h.SetMode(mode)
		e, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.Write([]byte("payload")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}
func TestWheelBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		names []string
		mode  fs.FileMode
	}{
		{"parent", []string{"../escape"}, 0o644}, {"absolute", []string{"/escape"}, 0o644},
		{"backslash", []string{"a\\b"}, 0o644}, {"duplicate", []string{"a.py", "a.py"}, 0o644},
		{"symlink", []string{"a.py"}, fs.ModeSymlink | 0o777},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := unpackWheel(t.Context(), wheelFixture(t, tc.names, tc.mode), map[string][]byte{}); err == nil {
				t.Fatal("unsafe wheel accepted")
			}
		})
	}
	files := map[string][]byte{}
	if err := unpackWheel(t.Context(), wheelFixture(t, []string{"a.py", "a.so", "__pycache__/a.pyc", "a.data/entry"}, 0o644), files); err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || string(files["a.py"]) != "payload" {
		t.Fatal("native or installation file retained")
	}
	files["a.py"] = []byte("other")
	if err := unpackWheel(t.Context(), wheelFixture(t, []string{"a.py"}, 0o644), files); err == nil {
		t.Fatal("conflicting module accepted")
	}
}
func TestLockedBuildRejectsTamperingAndCancellation(t *testing.T) {
	r, err := Lookup("0.4.3")
	if err != nil {
		t.Fatal(err)
	}
	read := func(context.Context, Archive) ([]byte, error) { return []byte("tampered"), nil }
	if _, _, err = Build(t.Context(), r, read); err == nil {
		t.Fatal("tampered upstream accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err = Build(ctx, r, read); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost")
	}
	a := r.Archives[0]
	a.URL = "https://evil.example/p.whl"
	if ValidateArchive(a) == nil {
		t.Fatal("unapproved origin accepted")
	}
}

func TestWheelRejectsFalseSizesAndChecksums(t *testing.T) {
	for _, field := range []string{"size", "checksum"} {
		t.Run(field, func(t *testing.T) {
			raw := wheelFixture(t, []string{"a.py"}, 0o644)
			central := bytes.Index(raw, []byte{'P', 'K', 1, 2})
			if field == "size" {
				binary.LittleEndian.PutUint32(raw[central+24:], MaxArchive+1)
			} else {
				raw[central+16] ^= 1
			}
			if err := unpackWheel(t.Context(), raw, map[string][]byte{}); err == nil {
				t.Fatal("invalid archive accepted")
			}
		})
	}
}
func TestEmbeddedLockCoverage(t *testing.T) {
	recipes, err := Recipes()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	embedded := 0
	for _, r := range recipes {
		if seen[r.Version] {
			t.Fatal("duplicate version")
		}
		seen[r.Version] = true
		for _, a := range r.Archives {
			if err := ValidateArchive(a); err != nil {
				t.Fatal(r.Version, err)
			}
			if raw, ok := EmbeddedSource(a); ok {
				embedded++
				if int64(len(raw)) != a.Size {
					t.Fatal("embedded size mismatch")
				}
			}
		}
	}
	if len(seen) != 26 || embedded != 1 {
		t.Fatalf("coverage %d/%d", len(seen), embedded)
	}
}
func TestLockedPackageReproduction(t *testing.T) {
	r, err := Lookup("0.4.3")
	if err != nil {
		t.Fatal(err)
	}
	raw, manifest, err := Build(t.Context(), r, func(_ context.Context, a Archive) ([]byte, error) {
		return os.ReadFile(filepath.Join("../../.local/vyper-wasm/downloads", a.SHA256))
	})
	if err != nil {
		t.Fatal("run make compiler-install: ", err)
	}
	// Compare the compiled-in output identity, not a second call to the builder.
	if !strings.Contains(string(r.Manifest), Digest(raw)) {
		t.Fatal("package differs from approved application lock")
	}
	recipeManifest := bytes.Buffer{}
	if err := json.Compact(&recipeManifest, r.Manifest); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(manifest), recipeManifest.Bytes()) {
		t.Fatal("manifest differs from application lock")
	}
}
