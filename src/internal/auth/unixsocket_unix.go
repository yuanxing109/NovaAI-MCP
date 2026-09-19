//go:build !windows

package auth

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// setUmask 设置进程 umask 并返回旧值。
func setUmask(m int) int {
	return syscall.Umask(m)
}

// peerUIDFromFD 通过 SO_PEERCRED 取对端 UID。
func peerUIDFromFD(fd uintptr) (int, error) {
	cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return -1, err
	}
	return int(cred.Uid), nil
}
