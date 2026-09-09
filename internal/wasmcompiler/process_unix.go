//go:build !windows

package wasmcompiler

import "golang.org/x/sys/unix"

// ApplyProcessLimits is called only by the subprocess entrypoint, never by API/all.
func ApplyProcessLimits() error {
	if unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0}) != nil {
		return ErrExecution
	}
	var current unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_NOFILE, &current) != nil {
		return ErrExecution
	}
	current.Cur = min(current.Cur, 64)
	current.Max = min(current.Max, 64)
	if unix.Setrlimit(unix.RLIMIT_NOFILE, &current) != nil {
		return ErrExecution
	}
	return nil
}
