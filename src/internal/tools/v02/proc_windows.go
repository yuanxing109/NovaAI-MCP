//go:build windows

package v02

import "os/exec"

// configureProcAttr 在 Windows 上没有进程组语义，空实现。
// 本文件只为宿主机构建/交叉编译可编译而存在，实际运行目标是 Android。
func configureProcAttr(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
