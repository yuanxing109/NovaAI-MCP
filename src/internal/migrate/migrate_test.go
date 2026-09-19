package migrate

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/novaai/novaai-mcp/internal/config"
)

// 迁移补齐的 profile 必须与全新安装完全一致。
//
// 这条不变式曾被破坏：migrate 里硬编码了一份更宽松的 default profile
// （denyTools 为空、riskCeiling 1），于是"从旧版本迁移上来"的机器比
// "全新安装"的机器多出 novaai_shell 权限，而 shell 是 pathguard 与
// antibrick 的万能绕过。
func TestDefaultProfilesMatchFreshInstall(t *testing.T) {
	got := defaultProfilesAsMap()

	b, err := json.Marshal(config.Default().Profiles)
	if err != nil {
		t.Fatalf("marshal 全新安装的 profile 失败: %v", err)
	}
	var want map[string]any
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatalf("unmarshal 全新安装的 profile 失败: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("迁移补齐的 profile 与全新安装不一致\n got: %#v\nwant: %#v", got, want)
	}
}

// default profile 必须拒绝通用 shell，否则迁移上来的机器等于没有防护。
func TestMigratedDefaultProfileDeniesShell(t *testing.T) {
	profiles := defaultProfilesAsMap()

	raw, ok := profiles["default"]
	if !ok {
		t.Fatal("迁移补齐的 profile 缺少 default")
	}
	m, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("default 不是对象: %#v", raw)
	}

	deny, _ := m["denyTools"].([]any)
	for _, d := range deny {
		if s, _ := d.(string); s == "novaai_shell" {
			return
		}
	}
	t.Fatalf("default.denyTools 未包含 novaai_shell: %#v", deny)
}
