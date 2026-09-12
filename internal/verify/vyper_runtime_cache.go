package verify

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	target := filepath.Join(compiler.Cache.Root, "vyper-runtime-"+artifact.ManifestSHA256)
	child := &VyperCompiler{Path: filepath.Join(target, "etherview-vyper"), Version: version, CompilerDigest: entry.ArtifactSHA256, ManifestDigest: digest, Timeout: compiler.Timeout, MaxInputBytes: compiler.MaxInputBytes, MaxOutputBytes: compiler.MaxOutputBytes}
	if _, err := child.validateHelper(); err == nil {
		return child, nil
	}
	archive, err := compiler.Cache.ensureArtifact(ctx, LanguageVyper, version, CompilerArtifact{URL: artifact.URL, SHA256: artifact.SHA256, MaxBytes: artifact.MaxBytes}, "vyper-archive-"+artifact.SHA256+".tar.gz")
	if err != nil {
		return nil, err
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
	if err := extractVyperArchive(archive, temporary); err != nil {
		return nil, err
	}
	staged := &VyperCompiler{Path: filepath.Join(temporary, "etherview-vyper"), Version: version, CompilerDigest: entry.ArtifactSHA256, ManifestDigest: digest}
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

func extractVyperArchive(archive, target string) error {
	invalid := errors.New("invalid Vyper runtime archive")
	file, err := os.Open(archive)
	if err != nil {
		return invalid
	}
	defer file.Close() //nolint:errcheck
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return invalid
	}
	defer compressed.Close() //nolint:errcheck
	reader := tar.NewReader(io.LimitReader(compressed, maxRuntimeTotalBytes+maxRuntimeManifestFiles*1024+1))
	seen := map[string]bool{}
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || header.Typeflag != tar.TypeReg || len(header.PAXRecords) != 0 || !fs.ValidPath(header.Name) || strings.Contains(header.Name, "\\") || seen[header.Name] || len(seen) >= maxRuntimeManifestFiles+1 || header.Size < 0 || header.Size > maxRuntimeFileBytes || header.Mode & ^int64(0o777) != 0 {
			return invalid
		}
		seen[header.Name] = true
		total += header.Size
		if total > maxRuntimeTotalBytes {
			return invalid
		}
		path := filepath.Join(target, filepath.FromSlash(header.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return invalid
		}
		output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return invalid
		}
		written, copyErr := io.CopyN(output, reader, header.Size)
		mode := fs.FileMode(0o444)
		if header.Mode&0o111 != 0 {
			mode = 0o555
		}
		modeErr, syncErr := output.Chmod(mode), output.Sync()
		closeErr := output.Close()
		if written != header.Size || copyErr != nil || modeErr != nil || syncErr != nil || closeErr != nil {
			return invalid
		}
	}
	// Consume the gzip trailer; truncated or corrupt transport must fail even if tar ended.
	if remaining, err := io.Copy(io.Discard, io.LimitReader(compressed, 1025)); err != nil || remaining > 1024 {
		return invalid
	}
	if !seen["runtime-manifest.json"] || !seen["etherview-vyper"] {
		return invalid
	}
	return filepath.WalkDir(target, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return invalid
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o555)
		}
		return nil
	})
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

func (compiler *VyperCompiler) validateHelper() (vyperRuntimeIdentity, error) {
	if compiler.Version == "" {
		return validateVyperHelper(compiler.Path)
	}
	identity, err := validateVyperHelperIdentity(compiler.Path, compiler.Version, compiler.CompilerDigest, compiler.ManifestDigest)
	return identity, err
}

func validateVyperHelperIdentity(path, version string, digest, manifest [sha256.Size]byte) (vyperRuntimeIdentity, error) {
	return validateVyperTree(path, version, hex.EncodeToString(digest[:]), manifest)
}
