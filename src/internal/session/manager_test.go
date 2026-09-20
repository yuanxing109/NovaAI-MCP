package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/novaai/novaai-mcp/internal/audit"
	"github.com/novaai/novaai-mcp/internal/config"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()

	lg, err := audit.NewLogger(cfg)
	if err != nil {
		t.Fatalf("构造审计器失败: %v", err)
	}
	t.Cleanup(lg.Close)

	m := NewManager(lg)
	t.Cleanup(m.Stop)
	return m
}

// TestSessionCarriesNoProfile 锁住"会话不携带权限"。
//
// 历史实现把 profile 挂在 session 上（仅作观测），但那个字段极其容易被
// 误读成鉴权依据。现在它不存在了 —— 序列化结果里不允许出现 profile 键。
func TestSessionCarriesNoProfile(t *testing.T) {
	m := newTestManager(t)

	s, err := m.Create()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("会话必须可 JSON 序列化: %v", err)
	}
	if strings.Contains(string(raw), "profile") || strings.Contains(string(raw), "Profile") {
		t.Fatalf("会话对象不应携带权限字段，实际: %s", raw)
	}

	// State 结构体本身也不得有 Profile 字段。
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	for k := range probe {
		if strings.EqualFold(k, "profile") {
			t.Fatalf("会话对象出现了 %q 键", k)
		}
	}
}

func TestCreateAndGet(t *testing.T) {
	m := newTestManager(t)

	s, err := m.Create()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := m.Get(s.ID)
	if err != nil {
		t.Fatalf("Get(%s): %v", s.ID, err)
	}
	if got.ID != s.ID {
		t.Fatalf("Get 返回的会话 id 不一致: %s != %s", got.ID, s.ID)
	}
}

func TestGetUnknownSession(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.Get("no-such-id"); err != ErrSessionNotFound {
		t.Fatalf("未知会话应返回 ErrSessionNotFound，实际: %v", err)
	}
}

func TestCloseMakesSessionUnavailable(t *testing.T) {
	m := newTestManager(t)

	s, _ := m.Create()
	m.Close(s.ID)
	if _, err := m.Get(s.ID); err == nil {
		t.Fatal("已关闭的会话不应再可取到")
	}
	if len(m.List()) != 0 {
		t.Fatalf("Close 后 List() 应为空，实际: %v", m.List())
	}
}
