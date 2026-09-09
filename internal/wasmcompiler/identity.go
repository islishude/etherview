package wasmcompiler

import "runtime/debug"

// LinkedRuntimeMatches rejects accidental module replacements or version drift.
// It runs in the dedicated executable, whose build metadata is retained.
func LinkedRuntimeMatches() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, dep := range info.Deps {
		if dep.Path == "github.com/tetratelabs/wazero" {
			return dep.Version == WazeroVersion && dep.Replace == nil
		}
	}
	return false
}
