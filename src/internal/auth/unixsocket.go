package auth

import (
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
)

func StartUnixSocket(path, mode string, handler http.Handler) (net.Listener, error) {
	if _, err := os.Stat(path); err == nil {
		_ = os.Remove(path)
	}

	oldMask := setUmask(0077)
	ln, err := net.Listen("unix", path)
	setUmask(oldMask)
	if err != nil {
		return nil, err
	}

	var perm os.FileMode = 0660
	if mode != "" {
		if v, err := strconv.ParseUint(mode, 8, 32); err == nil {
			perm = os.FileMode(v)
		}
	}
	if err := os.Chmod(path, perm); err != nil {
		ln.Close()
		return nil, err
	}

	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	return ln, nil
}

func ApplySocketContext(path string) error {
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
