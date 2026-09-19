//go:build !windows

package v02

import (
	"os/exec"
	"syscall"
)

// configureProcAttr 让子进程独立成进程组，便于整组回收。
func configureProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup 杀掉整个进程组，避免超时后残留子进程。
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	return cmd.Process.Kill()
}
