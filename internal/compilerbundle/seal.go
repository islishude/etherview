package compilerbundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Seal finalizes an already assembled build tree. It never replaces a manifest.
func Seal(root, executable string) error {
	if filepath.Base(executable) != executable {
		return ErrInvalid
	}
	if _, err := os.Lstat(filepath.Join(root, "runtime-manifest.json")); !os.IsNotExist(err) {
		return ErrInvalid
	}
	manifest := Manifest{Schema: Schema, Wazero: WazeroVersion, Python: PythonVersion, WASISDK: WASISDKVersion, Vyper: VyperVersion, CompilerSHA256: VyperWheelSHA256, Policy: ExecutionPolicy}
	directories := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrInvalid
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		st, err := entry.Info()
		if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > MaxBundleBytes {
			return ErrInvalid
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		mode := os.FileMode(0o444)
		isExecutable := rel == executable
		if isExecutable {
			mode = 0o555
		}
		if err = os.Chmod(path, mode); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		manifest.Files = append(manifest.Files, File{Path: rel, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data)), Executable: isExecutable})
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(root, "runtime-manifest.json"), append(raw, '\n'), 0o444); err != nil {
		return err
	}
	for _, directory := range directories {
		if err = os.Chmod(directory, 0o555); err != nil {
			return err
		}
	}
	_, err = Validate(filepath.Join(root, executable))
	return err
}
