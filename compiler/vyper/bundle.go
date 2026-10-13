// Package vyper assembles application-locked upstream bytes without executing
// package installers, build hooks or dependency resolution.
package vyper

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
)

const MaxArchive = 200 << 20

//go:embed application-lock.json
var lockJSON []byte

//go:embed adapter.py
var adapter []byte

type Archive struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
	URL      string `json:"url"`
	Size     int64  `json:"size"`
}

type Recipe struct {
	Version        string          `json:"version"`
	CompilerSHA256 string          `json:"compiler_sha256"`
	Pyodide        string          `json:"pyodide"`
	Python         string          `json:"python"`
	Archives       []Archive       `json:"archives"`
	Manifest       json.RawMessage `json:"manifest"`
}

type Lock struct {
	Schema  string   `json:"schema"`
	Recipes []Recipe `json:"recipes"`
}

func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (r Recipe) Digest() string {
	raw, _ := json.Marshal(r)
	return Digest(raw)
}

func Recipes() ([]Recipe, error) {
	var lock Lock
	if json.Unmarshal(lockJSON, &lock) != nil || lock.Schema != "etherview-vyper-lock-v1" || len(lock.Recipes) == 0 {
		return nil, errors.New("invalid embedded Vyper lock")
	}
	seen := make(map[string]bool)
	for _, recipe := range lock.Recipes {
		if recipe.Version == "" || seen[recipe.Version] || recipe.Version == "0.2.0" {
			return nil, errors.New("invalid embedded Vyper version")
		}
		seen[recipe.Version] = true
	}
	return lock.Recipes, nil
}

func Lookup(version string) (Recipe, error) {
	recipes, err := Recipes()
	if err != nil {
		return Recipe{}, err
	}
	for _, recipe := range recipes {
		if recipe.Version == version {
			return recipe, nil
		}
	}
	return Recipe{}, errors.New("unknown locked Vyper version")
}

func ValidateArchive(a Archive) error {
	digest, err := hex.DecodeString(a.SHA256)
	if err != nil || len(digest) != sha256.Size || a.Size <= 0 || a.Size > MaxArchive {
		return errors.New("invalid locked Vyper archive")
	}
	u, err := url.Parse(a.URL)
	if err != nil || u.Scheme != "https" || u.Host != "files.pythonhosted.org" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid locked PyPI origin")
	}
	return nil
}

type ReadArchive func(context.Context, Archive) ([]byte, error)

// Build authenticates every input and produces a deterministic package. The
// caller additionally compares the manifest with the application lock before
// installing it. The same implementation is used by release checks and workers.
func Build(ctx context.Context, r Recipe, read ReadArchive) ([]byte, []byte, error) {
	files := make(map[string][]byte)
	dependencies := make([]map[string]string, 0, len(r.Archives))
	var compilerFound bool
	for _, a := range r.Archives {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if err := ValidateArchive(a); err != nil {
			return nil, nil, err
		}
		raw, err := read(ctx, a)
		if err != nil {
			return nil, nil, err
		}
		if int64(len(raw)) != a.Size || Digest(raw) != a.SHA256 {
			return nil, nil, errors.New("vyper archive identity mismatch")
		}
		if a.Name == "vyper" {
			if compilerFound || a.Version != r.Version || a.SHA256 != r.CompilerSHA256 {
				return nil, nil, errors.New("vyper compiler identity mismatch")
			}
			compilerFound = true
		} else {
			dependencies = append(dependencies, map[string]string{"name": a.Name, "version": a.Version, "filename": a.Filename, "sha256": a.SHA256})
		}
		err = unpackWheel(ctx, raw, files)
		if err != nil {
			return nil, nil, fmt.Errorf("unpack %s: %w", a.Name, err)
		}
	}
	if !compilerFound {
		return nil, nil, errors.New("missing Vyper compiler")
	}
	if _, exists := files["adapter.py"]; exists {
		return nil, nil, errors.New("upstream adapter collision")
	}
	files["adapter.py"] = adapter
	zip, err := makeZip(ctx, files)
	if err != nil {
		return nil, nil, err
	}
	manifest, err := json.Marshal(map[string]any{
		"schema": "etherview-vyper-wasm-package-v1", "version": r.Version, "compiler_sha256": r.CompilerSHA256,
		"pyodide": r.Pyodide, "python": r.Python, "dependencies": dependencies,
		"files": []map[string]string{{"path": "packages.zip", "sha256": Digest(zip)}},
	})
	return zip, append(manifest, '\n'), err
}
