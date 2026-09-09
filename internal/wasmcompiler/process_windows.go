//go:build windows

package wasmcompiler

// Production is Linux; Windows retains the WASM and parent-process limits.
func ApplyProcessLimits() error { return nil }
