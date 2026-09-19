//go:build linux && !android

package util

import "syscall"

type StatFS struct {
	Bavail uint64
	Bsize  int64
	Bfree  uint64
	Blocks uint64
}

func Statfs(path string) (*StatFS, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return nil, err
	}
	return &StatFS{
		Bavail: s.Bavail,
		Bsize:  int64(s.Bsize),
		Bfree:  s.Bfree,
		Blocks: s.Blocks,
	}, nil
}

func FreeBytes(path string) (int64, error) {
	s, err := Statfs(path)
	if err != nil {
		return 0, err
	}
	return int64(s.Bavail) * s.Bsize, nil
}
