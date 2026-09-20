package shutdown

import (
	"net"
	"net/http"
	"os"
	"os/exec"
)

// unixSocketMode 是 socket 文件的固定权限：0660，且未 chgrp，仅 root 可连。
const unixSocketMode os.FileMode = 0660

// startUnixSocket 在 path 上监听一个 HTTP server。
//
// 这段逻辑原本在 internal/auth 包里，随该包（token 相关）一起删除后
// 移到这里：socket 的访问控制靠的是文件权限，与鉴权无关。
func startUnixSocket(path string, handler http.Handler) (net.Listener, error) {
	if _, err := os.Stat(path); err == nil {
		_ = os.Remove(path)
	}

	oldMask := setUmask(0077)
	ln, err := net.Listen("unix", path)
	setUmask(oldMask)
	if err != nil {
		return nil, err
	}

	if err := os.Chmod(path, unixSocketMode); err != nil {
		ln.Close()
		return nil, err
	}

	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	return ln, nil
}

// applySocketContext 给 socket 文件打 SELinux 标签。
func applySocketContext(path string) error {
	if _, err := os.Stat("/sys/fs/selinux/class/unix_stream_socket/index"); err == nil {
		if err := runChcon("u:object_r:novaai_socket:s0", path); err == nil {
			return nil
		}
	}
	return runChcon("u:object_r:init_socket:s0", path)
}

func runChcon(ctx, path string) error {
	cmd := exec.Command("chcon", ctx, path)
	return cmd.Run()
}
