package v02

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 本文件是 codes.go 那张词表的**结构化防线**，盯三个方向：
//
//  1. 代码里出现了没登记中文名的 code（漏了 → 结果里没有 codeName）；
//  2. 词表里有代码没用到的条目（死条目 → 让人以为它还在用）；
//  3. docs/errors.md 第 3 节的表与词表不一致（少一行、多一行、中文名写错）。
//
// 为什么需要它：这张表最典型的腐坏方式是"加了个 code 忘了配名字"，
// 而后果很安静 —— 结果里少一个字段，没人会注意到。
// 这个仓库已经吃过几次"检查器看不出问题"的亏（见 KNOWN_ISSUES 5a/11b），
// 所以这里用变异测试的思路写：它必须能在删掉一行登记时 FAIL。

// codeLiteralRe 匹配代码里的 code 字面量。
//
// 覆盖两种写法：
//   - errFail("PROTECTED_PATH", ...)
//   - map[string]any{"code": "PROTECTED_PATH", ...}    ← 防止有人手写地图
//
// 之所以要覆盖第二种：`execResult` 之前就是三处手写的同形 map，
// 那些 code 天然接不上自动中文名。现在它们收敛到了一个出口，
// 这条正则负责防止手写地图再次出现。
var (
	errFailCodeRe  = regexp.MustCompile(`errFail\(\s*"([A-Z][A-Z_0-9]*)"`)
	rawCodeFieldRe = regexp.MustCompile(`"code"\s*:\s*"([A-Z][A-Z_0-9]*)"`)
)

// exportedCodeConsts 是允许代替字面量使用的常量。CodeOK 必须在这里，
// 否则扫描会把它当成"未登记的 code"。
var exportedCodeConsts = map[string]bool{CodeOK: true}

// scanCodeLiterals 扫本包所有 .go 文件（不含 _test.go），
// 返回 code → 出现位置（文件:行）。
func scanCodeLiterals(t *testing.T) map[string][]string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读包目录失败: %v", err)
	}
	found := map[string][]string{}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("读 %s 失败: %v", name, err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			// 跳过注释行：文档注释里会出现 `"code": "PROTECTED_PATH"` 这样的
			// 示例文本，那不是生产点。
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, re := range []*regexp.Regexp{errFailCodeRe, rawCodeFieldRe} {
				for _, m := range re.FindAllStringSubmatch(line, -1) {
					code := m[1]
					found[code] = append(found[code],
						name+":"+itoa(i+1))
				}
			}
		}
	}
	if len(found) == 0 {
		t.Fatal("一个 code 字面量都没扫到 —— 扫描逻辑坏了（正则或路径不对）")
	}
	return found
}

// 方向一：代码里的每个 code 都必须有中文名。
func TestEveryCodeLiteralIsRegistered(t *testing.T) {
	used := scanCodeLiterals(t)

	var missing []string
	for code, locs := range used {
		if _, ok := codeNames[code]; !ok {
			missing = append(missing, code+" （用于 "+strings.Join(locs, ", ")+"）")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("下列 code 在代码里被使用，但 codeNames 里没有登记 —— "+
			"结果将缺少 codeName：\n  %s\n请在 codes.go 补上中文名。",
			strings.Join(missing, "\n  "))
	}
}

// 方向二：词表里不能有死条目。
//
// 死条目比漏登记更隐蔽：它让后来者以为某个 code 还在用，
// 于是照着它写客户端分支。
func TestNoDeadCodeEntries(t *testing.T) {
	used := scanCodeLiterals(t)

	var dead []string
	for code := range codeNames {
		if exportedCodeConsts[code] {
			continue
		}
		if len(used[code]) == 0 {
			dead = append(dead, code)
		}
	}
	if len(dead) > 0 {
		sort.Strings(dead)
		t.Fatalf("codeNames 里这些条目已无人使用（死条目）：\n  %s\n"+
			"要么删掉，要么说明它为什么保留。", strings.Join(dead, "\n  "))
	}
}

// docsSection3 抽出 docs/errors.md 里第 3 节（工具级错误码）的正文。
func docsSection3(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "docs", "errors.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 docs/errors.md 失败: %v", err)
	}
	text := string(raw)

	start := strings.Index(text, "## 3. 工具级错误码")
	if start < 0 {
		t.Fatal("docs/errors.md 里找不到「## 3. 工具级错误码」")
	}
	rest := text[start:]
	end := strings.Index(rest, "## 4.")
	if end < 0 {
		t.Fatal("docs/errors.md 里找不到第 4 节的开始，无法界定第 3 节")
	}
	return rest[:end]
}

// docsCodeRowRe 匹配第 3 节里的表格行：| `CODE` | 中文名 | 说明 |
var docsCodeRowRe = regexp.MustCompile("^\\|\\s*`([A-Z][A-Z_0-9]*)`\\s*\\|\\s*([^|]+?)\\s*\\|")

// 方向三：文档与词表逐条一致（code 集合与中文名都要一致）。
func TestDocsErrorTableMatchesCodeNames(t *testing.T) {
	section := docsSection3(t)

	docNames := map[string]string{}
	for _, line := range strings.Split(section, "\n") {
		m := docsCodeRowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		code, name := m[1], strings.TrimSpace(m[2])
		if prev, ok := docNames[code]; ok && prev != name {
			t.Errorf("docs/errors.md 里 %s 出现了两个不同的中文名：%q 与 %q", code, prev, name)
		}
		docNames[code] = name
	}

	if len(docNames) == 0 {
		t.Fatal("第 3 节里一行都没解析出来 —— 解析逻辑坏了（表格格式变了？）")
	}

	var missingInDoc, extraInDoc, nameMismatch []string
	for code, want := range codeNames {
		got, ok := docNames[code]
		if !ok {
			missingInDoc = append(missingInDoc, code)
			continue
		}
		if got != want {
			nameMismatch = append(nameMismatch,
				code+": 文档写 "+got+"，codes.go 写 "+want)
		}
	}
	for code := range docNames {
		if _, ok := codeNames[code]; !ok {
			extraInDoc = append(extraInDoc, code)
		}
	}

	sort.Strings(missingInDoc)
	sort.Strings(extraInDoc)
	sort.Strings(nameMismatch)

	if len(missingInDoc) > 0 {
		t.Errorf("docs/errors.md 第 3 节缺少这些 code：\n  %s", strings.Join(missingInDoc, "\n  "))
	}
	if len(extraInDoc) > 0 {
		t.Errorf("docs/errors.md 第 3 节多了这些 code（代码里没有）：\n  %s",
			strings.Join(extraInDoc, "\n  "))
	}
	if len(nameMismatch) > 0 {
		t.Errorf("中文名不一致：\n  %s", strings.Join(nameMismatch, "\n  "))
	}
}

// 出口覆盖：四个出口都必须过 withCodeName。
//
// 这条断言是给"新增一个出口忘了装饰"准备的 —— 上面三个方向只看
// code 字面量，看不出某个出口漏了装饰。
func TestResultConstructorsAttachCodeName(t *testing.T) {
	cases := []struct {
		name string
		got  map[string]any
	}{
		{"ok", ok(map[string]any{"x": 1})},
		{"okMsg", okMsg("done")},
		{"errFail", errFail("PROTECTED_PATH", "x")},
		{"execResult", execResult("out", "", 0)},
	}
	for _, c := range cases {
		if c.got["codeName"] == nil || c.got["codeName"] == "" {
			t.Errorf("%s 的结果里没有 codeName：%+v", c.name, c.got)
		}
		if c.got["codeName"] != codeNames[c.got["code"].(string)] {
			t.Errorf("%s 的 codeName 与词表不一致：%+v", c.name, c.got)
		}
	}
}

// 未登记的 code 不该被编造中文名 —— 宁可少一个字段。
func TestUnknownCodeGetsNoName(t *testing.T) {
	got := errFail("NO_SUCH_CODE", "x")
	if _, ok := got["codeName"]; ok {
		t.Fatalf("未登记的 code 不应带 codeName：%+v", got)
	}
	if got["code"] != "NO_SUCH_CODE" {
		t.Fatalf("code 应原样保留：%+v", got)
	}
}

// 数字错误码不参与这套机制：词表里不该出现它们。
func TestCodeNamesHasNoNumericCodes(t *testing.T) {
	for code := range codeNames {
		if strings.HasPrefix(code, "-") || (code[0] >= '0' && code[0] <= '9') {
			t.Errorf("词表里混进了数字码 %q —— 数字码来自 JSON-RPC/MCP 约定，不在本机制内", code)
		}
	}
}
