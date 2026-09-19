package v02

import (
	"testing"
	"time"

	"github.com/novaai/novaai-mcp/internal/config"
)

// shellTimeout 是 limits.shellTimeoutSeconds 的唯一读取点。
//
// 这几条断言锁住"配置是 shell 默认超时的唯一来源"这一不变式：早期实现
// 在 runCmd/runSh/runShRaw 三处各写了一次 60*time.Second，配置项从不生效。
func TestShellTimeoutFromConfig(t *testing.T) {
	if got := shellTimeout(nil); got != 60*time.Second {
		t.Errorf("deps=nil: got %v, want 60s", got)
	}

	cfg := &config.Config{}
	if got := shellTimeout(&Deps{Config: cfg}); got != 60*time.Second {
		t.Errorf("未配置(0) 应兜底 60s: got %v", got)
	}

	cfg.Limits.ShellTimeoutSec = 7
	if got := shellTimeout(&Deps{Config: cfg}); got != 7*time.Second {
		t.Errorf("配置 7s: got %v, want 7s", got)
	}
}

// 负数同样视为"未配置"，不能变成立即超时。
func TestShellTimeoutNegativeFallsBack(t *testing.T) {
	cfg := &config.Config{}
	cfg.Limits.ShellTimeoutSec = -5
	if got := shellTimeout(&Deps{Config: cfg}); got != 60*time.Second {
		t.Errorf("负数应兜底 60s: got %v", got)
	}
}
