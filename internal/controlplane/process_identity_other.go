//go:build !darwin && !linux

package controlplane

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func processBirthIdentity(pid int) (string, error) {
	output, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}
	identity := strings.Join(strings.Fields(string(output)), " ")
	if identity == "" {
		return "", fmt.Errorf("empty process birth identity for pid %d", pid)
	}
	return "ps:" + identity, nil
}
