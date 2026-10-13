package verify

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	vyper "github.com/islishude/etherview/compiler/vyper"
)

func (compiler *VyperCompiler) ensureRuntime(ctx context.Context, version string, entry CatalogEntry, artifact VyperRuntimeArtifact) (result *VyperCompiler, resultErr error) {
	if compiler.Cache == nil || compiler.Cache.InstallLocker == nil {
		return nil, ErrVyperRuntimeUnavailable
	}
	if err := secureCompilerCacheRoot(compiler.Cache.Root); err != nil {
		return nil, err
	}
	digest, err := decodeCatalogDigest(artifact.ManifestSHA256)
	if err != nil {
		return nil, err
	}
	recipe, err := vyper.Lookup(version)
	if err != nil || recipe.Digest() != artifact.RecipeSHA256 {
		return nil, ErrCompilerProvenanceConflict
	}
	var locked bytes.Buffer
	if json.Compact(&locked, recipe.Manifest) != nil || vyper.Digest(append(locked.Bytes(), '\n')) != artifact.ManifestSHA256 || recipe.CompilerSHA256 != hex.EncodeToString(entry.ArtifactSHA256[:]) {
		return nil, ErrCompilerProvenanceConflict
	}
	target := filepath.Join(compiler.Cache.Root, "vyper-runtime-"+artifact.ManifestSHA256)
	child := &VyperCompiler{Path: compiler.Path, SharedPath: compiler.sharedRoot(), PackagePath: target, Version: version, CompilerDigest: entry.ArtifactSHA256, ManifestDigest: digest, Timeout: compiler.Timeout, MaxInputBytes: compiler.MaxInputBytes, MaxOutputBytes: compiler.MaxOutputBytes}
	if _, err := child.validateHelper(); err == nil {
		return child, nil
	}
	assemblyContext, cancel := context.WithTimeout(ctx, compiler.timeout())
	defer cancel()
	payload, manifest, err := vyper.Build(assemblyContext, recipe, func(ctx context.Context, a vyper.Archive) ([]byte, error) {
		path, err := compiler.Cache.ensureArtifact(ctx, LanguageVyper, version, CompilerArtifact{URL: a.URL, SHA256: a.SHA256, MaxBytes: a.Size}, "pypi-"+a.SHA256+".whl")
		if err != nil {
			return nil, err
		}
		return os.ReadFile(path)
	})
	if err != nil {
		// Downloads sanitize transport errors too, so recover cancellation from
		// the assembly context before redacting other failures.
		if contextErr := assemblyContext.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, errors.New("assemble locked Vyper package")
	}
	var expected bytes.Buffer
	if json.Compact(&expected, recipe.Manifest) != nil || !bytes.Equal(bytes.TrimSpace(manifest), expected.Bytes()) {
		return nil, ErrCompilerProvenanceConflict
	}
	temporary, err := os.MkdirTemp(compiler.Cache.Root, ".vyper-runtime-*")
	if err != nil {
		return nil, errors.New("create Vyper runtime staging directory")
	}
	defer func() {
		if err := removeVyperStaging(temporary); err != nil {
			result, resultErr = nil, ErrCompilerCleanup
		}
	}()
	for name, data := range map[string][]byte{"packages.zip": payload, "package-manifest.json": manifest} {
		if err := os.WriteFile(filepath.Join(temporary, name), data, 0o444); err != nil {
			return nil, err
		}
	}
	if err := os.Chmod(temporary, 0o555); err != nil {
		return nil, err
	}
	staged := &VyperCompiler{Path: compiler.Path, SharedPath: compiler.sharedRoot(), PackagePath: temporary, Version: version, CompilerDigest: entry.ArtifactSHA256, ManifestDigest: digest}
	if _, err := staged.validateHelper(); err != nil {
		return nil, err
	}
	err = compiler.Cache.InstallLocker.WithCompilerCacheInstallLock(ctx, digest, func() error {
		if _, err := child.validateHelper(); err == nil {
			return nil
		}
		if err := removeVyperStaging(target); err != nil {
			return err
		}
		if err := os.Rename(temporary, target); err != nil {
			return errors.New("install Vyper runtime")
		}
		_, err := child.validateHelper()
		return err
	})
	if err != nil {
		return nil, err
	}
	return child, nil
}

func removeVyperStaging(path string) error {
	_ = filepath.WalkDir(path, func(current string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			return os.Chmod(current, 0o700)
		}
		return err
	})
	return os.RemoveAll(path)
}
