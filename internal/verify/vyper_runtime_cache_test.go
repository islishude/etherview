package verify

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestVyperArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind byte
		path string
	}{
		{"parent", tar.TypeReg, "../escape"}, {"absolute", tar.TypeReg, "/escape"},
		{"backslash", tar.TypeReg, "a\\b"}, {"symlink", tar.TypeSymlink, "link"},
		{"hardlink", tar.TypeLink, "link"}, {"device", tar.TypeChar, "device"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "input.tar.gz")
			f, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			g := gzip.NewWriter(f)
			w := tar.NewWriter(g)
			if err := w.WriteHeader(&tar.Header{Name: tc.path, Typeflag: tc.kind, Mode: 0444, Linkname: "/etc/passwd"}); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if err := g.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "runtime")
			if err := os.Mkdir(target, 0700); err != nil {
				t.Fatal(err)
			}
			if err := extractVyperArchive(archive, target); err == nil {
				t.Fatal("accepted unsafe archive")
			}
		})
	}
}

func TestVyperRuntimeCacheAuthenticatesAndRepairs(t *testing.T) {
	compiler := newVyperTestCompiler(t)
	root := filepath.Dir(compiler.Path)
	raw, err := os.ReadFile(filepath.Join(root, "runtime-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["schema"], manifest["platform"] = vyperDynamicSchema, runtime.GOOS+"-"+runtime.GOARCH
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "runtime-manifest.json" {
			contents = encoded
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := archive.WriteHeader(&tar.Header{Name: filepath.ToSlash(relative), Typeflag: tar.TypeReg, Mode: int64(info.Mode().Perm()), Size: int64(len(contents))}); err != nil {
			return err
		}
		_, err = archive.Write(contents)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { downloads.Add(1); _, _ = w.Write(buffer.Bytes()) }))
	defer server.Close()
	archiveDigest, manifestDigest := sha256.Sum256(buffer.Bytes()), sha256.Sum256(encoded)
	compilerDigest, err := decodeCatalogDigest(VyperCompilerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	artifact := VyperRuntimeArtifact{Platform: runtime.GOOS + "-" + runtime.GOARCH, URL: server.URL, SHA256: hex.EncodeToString(archiveDigest[:]), ManifestSHA256: hex.EncodeToString(manifestDigest[:]), MaxBytes: int64(buffer.Len()), Protocol: vyperDynamicSchema}
	cacheRoot := t.TempDir()
	parent := &VyperCompiler{Cache: &CompilerCache{Root: cacheRoot, InstallLocker: testCompilerCacheInstallLocker, unsafeHTTPClient: server.Client(), unsafeAllowHTTP: true}}
	entry := CatalogEntry{Version: VyperCompilerVersion, ArtifactSHA256: compilerDigest}
	child, err := parent.ensureRuntime(t.Context(), VyperCompilerVersion, entry, artifact)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeVyperStaging(filepath.Dir(child.Path)) })
	if err := child.ValidateRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := parent.ensureRuntime(t.Context(), VyperCompilerVersion, entry, artifact); err != nil {
		t.Fatal(err)
	}
	if downloads.Load() != 1 {
		t.Fatal("cache hit downloaded again")
	}
	if err := os.Chmod(child.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child.Path, []byte("corrupt executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := child.validateHelper(); err == nil {
		t.Fatal("accepted corrupt runtime")
	}
	repaired, err := parent.ensureRuntime(t.Context(), VyperCompilerVersion, entry, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := repaired.ValidateRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	artifact.SHA256 = strings.Repeat("1", 64)
	artifact.ManifestSHA256 = strings.Repeat("2", 64)
	if _, err := parent.ensureRuntime(t.Context(), VyperCompilerVersion, entry, artifact); err == nil {
		t.Fatal("accepted wrong archive digest")
	}
}
