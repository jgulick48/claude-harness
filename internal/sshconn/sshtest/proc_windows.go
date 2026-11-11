//go:build windows

package sshtest

import "os/exec"

func setProcessGroup(*exec.Cmd) {}

func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
