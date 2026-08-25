//go:build darwin

package controlplane

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func processBirthIdentity(pid int) (string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	started := info.Proc.P_starttime
	return fmt.Sprintf("darwin:%d:%d", started.Sec, started.Usec), nil
}
