//go:build linux

package controlplane

import (
	"fmt"
	"os"
	"strings"
)

func processBirthIdentity(pid int) (string, error) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	// The command name is parenthesized and may contain spaces, so fields begin
	// only after its final ')'. Linux proc(5) defines starttime as field 22,
	// which is index 19 in this suffix beginning at field 3.
	closeName := strings.LastIndexByte(string(stat), ')')
	if closeName < 0 {
		return "", fmt.Errorf("malformed process stat for pid %d", pid)
	}
	fields := strings.Fields(string(stat)[closeName+1:])
	if len(fields) <= 19 {
		return "", fmt.Errorf("process stat for pid %d has %d fields", pid, len(fields))
	}
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	return "linux:" + strings.TrimSpace(string(bootID)) + ":" + fields[19], nil
}
