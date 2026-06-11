//go:build !windows

package embeddedterm

import (
	"errors"
	"os/exec"
	"syscall"
)

func configureProcessGroup(cmd *exec.Cmd) {
	// pty.StartWithSize sets Setsid/Setctty. Setting Setpgid as well fails on
	// Darwin forkpty, while Setsid already makes the child process group killable
	// by pid.
}

func terminateProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil {
		err = syscall.Kill(-pgid, syscall.SIGKILL)
		if err == nil || errors.Is(err, syscall.ESRCH) {
			return nil
		}
	}
	err = cmd.Process.Kill()
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
