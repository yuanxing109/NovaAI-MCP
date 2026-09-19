package auth

import (
	"errors"
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

// PeerUID 从 *net.UnixConn 拿对端 UID。
//
// 语义说明：
//
//	peerUid 是"谁在连接"的凭证，不是"以什么权限运行"的身份。
//	如果客户端是 su -c curl --unix-socket ...，返回的是 su 进程的 UID（2000），
//	不是提权后的 0。这是内核行为，无法绕过。
//	因此 peerUid 只用于审计日志记录，不参与鉴权决策。
//	权限决策统一由 sessionBinding.byTokenHash 决定。
func PeerUID(conn net.Conn) (int, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return -1, errors.New("not a unix conn")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1, err
	}
	var uid int
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		uid, sockErr = peerUIDFromFD(fd)
	})
	if err != nil {
		return -1, err
	}
	if sockErr != nil {
		return -1, sockErr
	}
	return uid, nil
}
