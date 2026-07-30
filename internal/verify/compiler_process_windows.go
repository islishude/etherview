//go:build windows

package verify

import (
	"os"
	"os/exec"
)

func configureCompilerProcess(_ *exec.Cmd) {}

func killCompilerProcessGroup(process *os.Process) error {
	return process.Kill()
}

func compilerProcessGroupTerminated(process *os.Process) bool {
	return process == nil || process.Pid > 0
}
