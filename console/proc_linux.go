//go:build linux

package main

import (
	"os/exec"
	"syscall"
)

func hideProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}

func attachProcessLifetime(*exec.Cmd) func() { return nil }
