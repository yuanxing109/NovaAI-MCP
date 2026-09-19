package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestWritePIDFileRecordsOwnPID 锁住 G2：PID 文件必须记录 daemon 自己的 PID。
//
// 旧实现由 shell 侧写 `$!`，那是 `su` 进程的 PID；而 daemon 是 su 的孙进程，
// `kill -TERM` 因此可能到不了 daemon —— 卸载后它仍占着 :5322 与 mcp.sock。
// 现在唯一 owner 是 daemon 自己。
func TestWritePIDFileRecordsOwnPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), pidFileName)

	if err := writePIDFile(path); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 PID 文件: %v", err)
	}

	got := strings.TrimSpace(string(b))
	want := strconv.Itoa(os.Getpid())
	if got != want {
		t.Fatalf("PID 文件内容 = %q，期望本进程 PID %q", got, want)
	}

	// Windows 上 Mode().Perm() 只反映只读位（0666/0444），不是真实 Unix 权限。
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := fi.Mode().Perm(); perm != 0600 {
			t.Errorf("PID 文件权限 = %04o，期望 0600", perm)
		}
	}
}

// TestPIDFileNameMatchesShellExpectation 防止 Go 与 shell 两侧的文件名漂移。
//
// common.sh 的 ZCR_PID_FILE 是 "$ZCR_INTERNAL_DIR/novaaimcpd.pid"；
// 两侧一旦不一致，daemon 写的文件 shell 读不到，停止功能静默失效。
func TestPIDFileNameMatchesShellExpectation(t *testing.T) {
	if pidFileName != "novaaimcpd.pid" {
		t.Fatalf("pidFileName = %q，与 common.sh 的 ZCR_PID_FILE 不一致", pidFileName)
	}

	// 同时确认 common.sh 里确实还是这个名字。
	common, err := os.ReadFile(filepath.Join("..", "..", "..", "common.sh"))
	if err != nil {
		t.Skipf("读取 common.sh 失败: %v", err)
	}
	if !strings.Contains(string(common), "novaaimcpd.pid") {
		t.Error("common.sh 不再引用 novaaimcpd.pid —— Go 与 shell 的 PID 文件名已漂移")
	}
}
