//go:build windows

package compilerbundle

import (
	"os"
	"os/exec"
)

func configureProcess(_ *exec.Cmd) {}

func killGroup(process *os.Process) error {
	return process.Kill()
}

func groupTerminated(process *os.Process) bool {
	return process == nil || process.Pid > 0
}
