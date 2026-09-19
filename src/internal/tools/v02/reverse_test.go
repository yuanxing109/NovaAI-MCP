package v02

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestApktoolJarIsBundledNotCopied 锁住 G4：apktool.jar 在设备上只有模块内一份。
//
// 旧实现读状态目录 /data/adb/novaai-mcp/tools/apktool.jar —— 那是安装时由
// customize.sh 复制出来的第二份副本，而 wrapper 脚本读的是
// <mod>/bin/tools/apktool.jar。同一批 jar（约 31 MiB）因此存两遍，且两份
// 可能版本漂移。
//
// 现在路径从可执行文件位置推导：<mod>/bin/<abi>/novaaimcpd -> <mod>/bin/tools。
func TestApktoolJarIsBundledNotCopied(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("无法解析可执行文件路径: %v", err)
	}

	want := filepath.Join(filepath.Dir(filepath.Dir(exe)), "tools", "apktool.jar")
	got := apktoolJarPath()
	if got != want {
		t.Fatalf("apktoolJarPath() = %q，期望 %q（随二进制分发，而非状态目录副本）", got, want)
	}

	// 显式拒绝回到状态目录：这是本测试真正要防的回归。
	if strings.Contains(got, "novaai-mcp") {
		t.Fatalf("apktool.jar 又指向了状态目录副本: %q", got)
	}
}
