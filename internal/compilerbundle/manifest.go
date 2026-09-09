// Package compilerbundle defines the shared immutable runtime contract. It does
// not import or execute wazero; API/all use it to validate the subprocess bundle.
package compilerbundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const Schema = "etherview-wazero-runtime-v1"
const ExecutorKind = "etherview_wazero_v1"
const ExecutionPolicy = "wasm_subprocess_v1"
const WazeroVersion = "v1.12.0"
const PythonVersion = "3.13.15"
const WASISDKVersion = "24"
const VyperVersion = "0.4.3"
const VyperWheelSHA256 = "3b9671727c888363740dc678e60336759871487d0e4e9fdd973048fa9635c4fd"
const MaxManifestBytes = 2 << 20
const MaxBundleBytes = int64(512 << 20)

var ErrInvalid = errors.New("invalid WASM compiler runtime")

type File struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable"`
}
type Manifest struct {
	Schema         string `json:"schema"`
	Wazero         string `json:"wazero"`
	Python         string `json:"python"`
	WASISDK        string `json:"wasi_sdk"`
	Vyper          string `json:"vyper"`
	CompilerSHA256 string `json:"compiler_sha256"`
	Policy         string `json:"policy"`
	Files          []File `json:"files"`
}
type Identity struct {
	Digest   [32]byte
	Manifest Manifest
	Root     string
}

// Validate checks a coherent ordinary read-only tree and its canonical manifest.
// The root can be relocated, but files cannot escape it or alias another path.
func Validate(executable string) (Identity, error) {
	var zero Identity
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable {
		return zero, ErrInvalid
	}
	root := filepath.Dir(executable)
	path := filepath.Join(root, "runtime-manifest.json")
	raw, err := readOrdinary(path, MaxManifestBytes, false)
	if err != nil {
		return zero, err
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil {
		return zero, ErrInvalid
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return zero, ErrInvalid
	}
	if manifest.Schema != Schema || manifest.Wazero != WazeroVersion || manifest.Python != PythonVersion || manifest.WASISDK != WASISDKVersion || manifest.Vyper != VyperVersion || manifest.CompilerSHA256 != VyperWheelSHA256 || manifest.Policy != ExecutionPolicy || len(manifest.Files) == 0 || len(manifest.Files) > 4096 {
		return zero, ErrInvalid
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || !bytes.Equal(append(canonical, '\n'), raw) {
		return zero, ErrInvalid
	}
	paths := make([]string, 0, len(manifest.Files))
	expected := map[string]bool{"runtime-manifest.json": true}
	total := int64(0)
	for _, file := range manifest.Files {
		if !fs.ValidPath(file.Path) || strings.Contains(file.Path, "\\") || expected[file.Path] || file.Size <= 0 || file.Size > MaxBundleBytes {
			return zero, ErrInvalid
		}
		expected[file.Path] = true
		paths = append(paths, file.Path)
		total += file.Size
		if total > MaxBundleBytes {
			return zero, ErrInvalid
		}
		if file.Executable != (file.Path == filepath.Base(executable)) {
			return zero, ErrInvalid
		}
		data, err := readOrdinary(filepath.Join(root, filepath.FromSlash(file.Path)), file.Size, file.Executable)
		if err != nil || int64(len(data)) != file.Size {
			return zero, ErrInvalid
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != file.SHA256 {
			return zero, ErrInvalid
		}
	}
	if !slices.IsSorted(paths) || !expected[filepath.Base(executable)] || !expected["python.wasm"] || !expected["python.zip"] || !expected["runtime.lock.json"] {
		return zero, ErrInvalid
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return ErrInvalid
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o222 != 0 {
			return ErrInvalid
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || !expected[filepath.ToSlash(rel)] {
			return ErrInvalid
		}
		return nil
	})
	if err != nil {
		return zero, ErrInvalid
	}
	return Identity{Digest: sha256.Sum256(raw), Manifest: manifest, Root: root}, nil
}
func readOrdinary(path string, limit int64, executable bool) (data []byte, err error) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o222 != 0 || st.Size() < 0 || st.Size() > limit || (st.Mode().Perm()&0o111 != 0) != executable {
		return nil, ErrInvalid
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrInvalid
	}
	defer func() {
		if e := f.Close(); e != nil {
			data = nil
			err = ErrInvalid
		}
	}()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(st, opened) {
		return nil, ErrInvalid
	}
	data, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, ErrInvalid
	}
	return data, nil
}

// ReadFile reacquires and authenticates a fixed member before handing its bytes
// to a guest. The returned bytes are detached from later filesystem changes.
func ReadFile(identity Identity, name string) ([]byte, error) {
	for _, file := range identity.Manifest.Files {
		if file.Path == name {
			raw, err := readOrdinary(filepath.Join(identity.Root, filepath.FromSlash(name)), file.Size, file.Executable)
			if err != nil {
				return nil, err
			}
			digest := sha256.Sum256(raw)
			if int64(len(raw)) != file.Size || hex.EncodeToString(digest[:]) != file.SHA256 {
				return nil, ErrInvalid
			}
			return raw, nil
		}
	}
	return nil, ErrInvalid
}
