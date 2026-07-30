//go:build !windows

package verify

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureCompilerProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killCompilerProcessGroup(process *os.Process) error {
	err := syscall.Kill(-process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

func compilerProcessGroupTerminated(process *os.Process) bool {
	if process == nil {
		return true
	}
	err := syscall.Kill(-process.Pid, 0)
	return errors.Is(err, syscall.ESRCH)
}
