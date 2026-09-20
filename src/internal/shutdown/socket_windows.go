//go:build windows

package shutdown

// setUmask 在 Windows 上没有 umask，空实现。
// 本文件只为宿主机构建/交叉编译可编译而存在，实际运行目标是 Android。
func setUmask(m int) int { return 0 }
