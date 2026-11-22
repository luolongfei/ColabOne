//go:build !windows && !linux

package main

import "os/exec"

func hideProcess(*exec.Cmd) {}

func attachProcessLifetime(*exec.Cmd) func() { return nil }
