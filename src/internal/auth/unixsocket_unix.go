//go:build !windows

package auth

import (
	"syscall"
)

// setUmask 设置进程 umask 并返回旧值。
func setUmask(m int) int {
	return syscall.Umask(m)
}
