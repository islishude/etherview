package main

import (
	"archive/zip"
	"bytes"
	"debug/elf"
	"errors"
	"github.com/islishude/etherview/internal/compilerbundle"
	"os"
	"path/filepath"
	"strings"
)

func checkImage(path string) error {
	identity, err := compilerbundle.Validate(path)
	if err != nil {
		return err
	}
	file, err := elf.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	libs, err := file.ImportedLibraries()
	if err != nil || len(libs) != 0 {
		return errors.New("WASM helper has native shared-library dependencies")
	}
	for _, program := range file.Progs {
		if program.Type == elf.PT_INTERP {
			return errors.New("WASM helper has an ELF interpreter")
		}
	}
	binary, err := compilerbundle.ReadFile(identity, "python.wasm")
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(binary, []byte{0, 97, 115, 109, 1, 0, 0, 0}) {
		return errors.New("python payload is not core WASM")
	}
	archive, err := zip.OpenReader(filepath.Join(identity.Root, "python.zip"))
	if err != nil {
		return err
	}
	defer func() { _ = archive.Close() }()
	for _, entry := range archive.File {
		name := strings.ToLower(entry.Name)
		if strings.HasSuffix(name, ".so") || strings.Contains(name, ".so.") || strings.HasSuffix(name, ".dll") || strings.HasSuffix(name, ".dylib") || strings.Contains(name, "pyinstaller") || strings.HasPrefix(name, "site-packages/crypto/") {
			return errors.New("native or replaced Python dependency in runtime archive")
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return errors.New("runtime archive contains a symlink")
		}
	}
	return nil
}
