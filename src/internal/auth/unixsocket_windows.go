//go:build windows

package auth

import "errors"

// setUmask 在 Windows 上没有 umask，空实现。
// 本文件只为宿主机构建/交叉编译可编译而存在，实际运行目标是 Android。
func setUmask(m int) int { return 0 }

// peerUIDFromFD 在 Windows 上无法取对端 UID。
func peerUIDFromFD(fd uintptr) (int, error) {
	return -1, errors.New("peer uid 在当前平台不可用")
}
