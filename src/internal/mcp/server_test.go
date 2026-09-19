package mcp

import (
	"strings"
	"testing"
)

// limit <= 0 表示不限制，且必须保留 structuredContent。
func TestSuccessResultNoLimit(t *testing.T) {
	res := successResult(map[string]any{"a": 1}, 0)
	if len(res.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(res.Content))
	}
	if res.StructuredContent == nil {
		t.Error("limit=0 时不应丢弃 structuredContent")
	}
	if strings.Contains(res.Content[0].Text, "结果已截断") {
		t.Error("limit=0 时不应出现截断说明")
	}
}

func TestSuccessResultUnderLimit(t *testing.T) {
	res := successResult(map[string]any{"a": "short"}, 1<<20)
	if res.StructuredContent == nil {
		t.Error("未超限时不应丢弃 structuredContent")
	}
}

// 超限时必须同时：截断 content、追加说明、丢弃 structuredContent。
// 只截 content 而保留完整结构化副本等于没省帧大小，还会让两者不一致。
func TestSuccessResultTruncatesOverLimit(t *testing.T) {
	big := strings.Repeat("x", 500)
	res := successResult(map[string]any{"blob": big}, 100)

	if res.StructuredContent != nil {
		t.Error("截断时必须丢弃 structuredContent")
	}
	if len(res.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(res.Content))
	}

	text := res.Content[0].Text
	if !strings.Contains(text, "结果已截断") {
		t.Fatalf("缺少截断说明: %q", text)
	}

	head := strings.SplitN(text, "\n\n[结果已截断", 2)[0]
	if len(head) > 100 {
		t.Errorf("截断后正文 %d 字节，超过上限 100", len(head))
	}
}

// 截断不能切断 UTF-8 字符。每个汉字 3 字节，任何 limit 都不应产生
// 半个字符（表现为 U+FFFD 或长度超限）。
func TestTruncateBytesKeepsUTF8Boundary(t *testing.T) {
	s := "汉字测试"

	if got := truncateBytes(s, 4); got != "汉" {
		t.Errorf("truncateBytes(%q, 4) = %q, want %q", s, got, "汉")
	}

	for i := 1; i <= len(s)+3; i++ {
		out := truncateBytes(s, int64(i))
		if i < len(s) && len(out) > i {
			t.Fatalf("limit=%d 时输出 %d 字节", i, len(out))
		}
		if strings.ContainsRune(out, '\uFFFD') {
			t.Fatalf("limit=%d 切断了 UTF-8 字符: %q", i, out)
		}
	}
}

func TestTruncateBytesNoLimit(t *testing.T) {
	s := strings.Repeat("x", 10)
	if got := truncateBytes(s, 0); got != s {
		t.Error("limit=0 应原样返回")
	}
	if got := truncateBytes(s, 100); got != s {
		t.Error("未超限应原样返回")
	}
}
