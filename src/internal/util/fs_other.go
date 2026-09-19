//go:build !linux && !android

package util

import "errors"

// ErrStatfsUnsupported 表示当前平台没有 statfs 实现。
// novaaimcpd 的实际运行目标是 Android/Linux，本文件只为
// 在 Windows/macOS 上做交叉编译与 go vet 提供可编译的兜底。
var ErrStatfsUnsupported = errors.New("statfs 在当前平台不可用")

type StatFS struct {
	Bavail uint64
	Bsize  int64
	Bfree  uint64
	Blocks uint64
}

func Statfs(path string) (*StatFS, error) {
	return nil, ErrStatfsUnsupported
}

func FreeBytes(path string) (int64, error) {
	return 0, ErrStatfsUnsupported
}
