package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const vyperSharedSchema = "etherview-python-wasm-v1"
const vyperPackageSchema = "etherview-vyper-wasm-package-v1"
const vyperSharedManifestSHA256 = "b87574439230ec442792386f2ccd82fa020f735769d55770492b9da727502d24"

type vyperWASMFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type vyperWASMManifest struct {
	Schema         string `json:"schema"`
	Version        string `json:"version,omitempty"`
	CompilerSHA256 string `json:"compiler_sha256,omitempty"`
	Pyodide        string `json:"pyodide"`
	Python         string `json:"python"`
	Pycryptodome   string `json:"pycryptodome,omitempty"`
	Dependencies   []struct {
		Name     string `json:"name"`
		Version  string `json:"version"`
		SHA256   string `json:"sha256"`
		Filename string `json:"filename"`
	} `json:"dependencies,omitempty"`
	Files []vyperWASMFile `json:"files"`
}

func validateVyperWASMTree(root, name string, expected [sha256.Size]byte) (vyperWASMManifest, [sha256.Size]byte, error) {
	var manifest vyperWASMManifest
	var zero [sha256.Size]byte
	invalid := errors.New("invalid Vyper WASM manifest")
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return manifest, zero, invalid
	}
	manifestPath := filepath.Join(root, name)
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o222 != 0 || info.Size() > maxRuntimeManifestBytes {
		return manifest, zero, invalid
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil || strictVyperJSON(raw, &manifest) != nil {
		return manifest, zero, invalid
	}
	digest := sha256.Sum256(raw)
	if expected != zero && digest != expected {
		return manifest, zero, invalid
	}
	if manifest.Pyodide != "0.29.3" || manifest.Python != "3.13.2" || len(manifest.Files) == 0 || len(manifest.Files) > maxRuntimeManifestFiles {
		return manifest, zero, invalid
	}
	listed := map[string]bool{manifestPath: true}
	var total int64
	for _, file := range manifest.Files {
		if strings.Contains(file.Path, "/") || !fs.ValidPath(file.Path) || strings.Contains(file.Path, "\\") || file.Path == name {
			return manifest, zero, invalid
		}
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		if listed[path] {
			return manifest, zero, invalid
		}
		listed[path] = true
		want, e := decodeCatalogDigest(file.SHA256)
		if e != nil {
			return manifest, zero, invalid
		}
		info, e := os.Lstat(path)
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o333 != 0 {
			return manifest, zero, invalid
		}
		actual, e := fileSHA256(path, maxRuntimeFileBytes)
		if e != nil || actual != want {
			return manifest, zero, invalid
		}
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return invalid
		}
		i, e := entry.Info()
		if e != nil || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0o222 != 0 {
			return invalid
		}
		if entry.IsDir() {
			return nil
		}
		if !i.Mode().IsRegular() || !listed[path] {
			return invalid
		}
		total += i.Size()
		if total > maxRuntimeTotalBytes {
			return invalid
		}
		return nil
	})
	if err != nil {
		return manifest, zero, invalid
	}
	return manifest, digest, nil
}

func (compiler *VyperCompiler) sharedPath() string {
	if compiler.SharedPath != "" {
		return compiler.SharedPath
	}
	return filepath.Join(filepath.Dir(filepath.Dir(compiler.Path)), "python-wasm")
}
func (compiler *VyperCompiler) hostIdentity() ([sha256.Size]byte, [sha256.Size]byte, error) {
	host, err := validateSolcJSRuntimeManifest(compiler.Path)
	if err != nil {
		return host, [sha256.Size]byte{}, err
	}
	want, _ := decodeCatalogDigest(vyperSharedManifestSHA256)
	m, shared, err := validateVyperWASMTree(compiler.sharedPath(), "shared-manifest.json", want)
	if err != nil || m.Schema != vyperSharedSchema || m.Pycryptodome != "3.21.0" {
		return host, shared, ErrVyperRuntimeUnavailable
	}
	return host, shared, nil
}
func vyperExecutorDigest(host, shared, pkg [sha256.Size]byte) [sha256.Size]byte {
	hash := sha256.New()
	hash.Write([]byte("etherview/node_vyper_wasm_v1\x00"))
	hash.Write(host[:])
	hash.Write(shared[:])
	hash.Write(pkg[:])
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}
func (compiler *VyperCompiler) validateHelper() (vyperRuntimeIdentity, error) {
	host, shared, err := compiler.hostIdentity()
	if err != nil {
		return vyperRuntimeIdentity{}, err
	}
	if compiler.PackagePath == "" {
		return vyperRuntimeIdentity{executor: vyperExecutorDigest(host, shared, [sha256.Size]byte{})}, nil
	}
	m, digest, err := validateVyperWASMTree(compiler.PackagePath, "package-manifest.json", compiler.ManifestDigest)
	if err != nil || m.Schema != vyperPackageSchema || m.Version != compiler.version() || m.CompilerSHA256 != hex.EncodeToString(compiler.CompilerDigest[:]) || len(m.Files) != 1 || m.Files[0].Path != "packages.zip" {
		return vyperRuntimeIdentity{}, ErrVyperRuntimeUnavailable
	}
	return vyperRuntimeIdentity{compiler: compiler.CompilerDigest, executor: vyperExecutorDigest(host, shared, digest)}, nil
}

// Revalidation never adopts a different host after startup, even if a newly
// fetched catalog happens to authorize that host's digest.
func (compiler *VyperCompiler) checkedHostIdentity() ([sha256.Size]byte, [sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	expected, ready := compiler.runtimeIdentity()
	if !ready {
		return zero, zero, ErrVyperRuntimeUnavailable
	}
	host, shared, err := compiler.hostIdentity()
	if err != nil || expected.compiler != zero || expected.executor != vyperExecutorDigest(host, shared, zero) {
		compiler.markUnavailable()
		return zero, zero, ErrVyperRuntimeUnavailable
	}
	return host, shared, nil
}
