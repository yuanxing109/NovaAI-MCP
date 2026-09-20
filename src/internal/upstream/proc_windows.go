//go:build windows

package upstream

import "os/exec"

// configureProcAttr 在 Windows 上无操作。
//
// 上游聚合的目标平台是 Android，Windows 只是开发/测试宿主；这里不做
// Job Object 那一套，直接 kill 子进程即可（测试用的假上游都是单进程）。
func configureProcAttr(_ *exec.Cmd) {}

// killProcessGroup 在 Windows 上退化为 kill 直接子进程。
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
