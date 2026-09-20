//go:build !windows

package upstream

import (
	"os/exec"
	"syscall"
)

// configureProcAttr 让上游子进程独立成进程组，便于整组回收。
//
// 与 tools/v02 的同名函数是同一套做法：stdio 上游常常是个 shell wrapper，
// 它自己再拉起真正的服务进程；只 kill 直接子进程会留下孤儿占着端口。
func configureProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup 杀掉整个进程组。
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	return cmd.Process.Kill()
}
