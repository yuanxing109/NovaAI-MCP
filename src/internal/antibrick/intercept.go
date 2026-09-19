// Package antibrick 在「通用载体」落地变更之前做最后一道判定。
//
// 为什么不是子串匹配：早期实现用 strings.Contains 找 "rm -rf /"，
// 只要写成 `rm -fr /`、`rm -rf  /`（双空格）、`rm -rf "/"` 或
// `busybox rm -rf /` 就绕过了；同时 "dd if=" 又会误拦正常的
// `dd if=/dev/zero of=/data/local/tmp/x`。
//
// 现在的做法：把命令切成简单命令，识别动词与真实参数，把路径参数交给
// pathguard 判定。这样 `rm -rf /` 与 `rm -fr "/"` 走同一条路径。
//
// 覆盖范围与残余风险见 docs/security.md「shell 拦截的边界」。
package antibrick

import (
	"path"
	"strings"

	"github.com/novaai/novaai-mcp/internal/pathguard"
)

// InterceptResult 拦截结果
type InterceptResult struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
	Command string `json:"command,omitempty"`
}

// InterceptCommand 检查命令是否危险。cwd 未知时按 "/" 处理，
// 这与 exec.Command 未设置 Dir 时子进程继承的 cwd 一致。
func InterceptCommand(cmd string) InterceptResult {
	return InterceptCommandIn(cmd, "")
}

// InterceptCommandIn 在已知 cwd 的情况下检查命令，
// 使相对路径参数（`rm -rf data`）也能被正确判定。
func InterceptCommandIn(cmd, cwd string) InterceptResult {
	if cwd == "" {
		cwd = "/"
	}
	if reason := analyze(cmd, cwd, 0); reason != "" {
		return InterceptResult{Allowed: false, Reason: reason, Command: cmd}
	}
	return InterceptResult{Allowed: true}
}

// maxUnwrapDepth 限制 sh -c / su -c 的递归展开层数。
const maxUnwrapDepth = 4

func analyze(cmd, cwd string, depth int) string {
	if depth > maxUnwrapDepth {
		return ""
	}
	segs := splitCommands(tokenize(cmd))
	for _, seg := range segs {
		for _, r := range seg.redirects {
			if reason := checkPath(r, cwd, false); reason != "" {
				return reason
			}
		}
		if len(seg.args) == 0 {
			continue
		}

		// 展开 sh -c '...' / su -c '...'：shell 工具本身就是这样包装的，
		// 不展开等于完全没检查。
		if content, ok := shellC(seg.args); ok {
			if reason := analyze(content, cwd, depth+1); reason != "" {
				return reason
			}
			continue
		}

		verb := path.Base(seg.args[0])
		rest := seg.args[1:]

		// 跟踪 cd，让后续简单命令用正确的 cwd
		if verb == "cd" {
			if np := firstNonFlag(rest); np != "" {
				cwd = joinCwd(cwd, np)
			}
			continue
		}

		// busybox / toybox 前缀
		for (verb == "busybox" || verb == "toybox") && len(rest) > 0 {
			verb = path.Base(rest[0])
			rest = rest[1:]
		}

		if reason := analyzeVerb(verb, rest, cwd); reason != "" {
			return reason
		}
	}
	return ""
}

func analyzeVerb(verb string, rest []string, cwd string) string {
	paths := nonFlagArgs(rest)

	switch verb {
	case "rm", "rmdir", "unlink", "shred":
		rec := hasShortFlag(rest, 'r') || hasShortFlag(rest, 'R') ||
			hasLongFlag(rest, "recursive") || hasLongFlag(rest, "dir")
		for _, p := range paths {
			if reason := checkPath(p, cwd, rec); reason != "" {
				return reason
			}
		}

	case "truncate", "mkfifo", "mknod":
		for _, p := range paths {
			if reason := checkPath(p, cwd, false); reason != "" {
				return reason
			}
		}

	case "dd":
		for _, a := range rest {
			if v, ok := strings.CutPrefix(a, "of="); ok {
				if reason := checkPath(v, cwd, false); reason != "" {
					return reason
				}
			}
		}

	case "chmod", "chown", "chgrp":
		rec := hasShortFlag(rest, 'R') || hasLongFlag(rest, "recursive")
		if len(paths) > 0 {
			if reason := checkPath(paths[len(paths)-1], cwd, rec); reason != "" {
				return reason
			}
		}

	case "mv":
		// 源被移走等价于在源位置删除
		if len(paths) >= 1 {
			if reason := checkPath(paths[0], cwd, false); reason != "" {
				return reason
			}
		}
		if len(paths) >= 2 {
			if reason := checkPath(paths[len(paths)-1], cwd, false); reason != "" {
				return reason
			}
		}

	case "cp", "install", "ln":
		if len(paths) >= 2 {
			if reason := checkPath(paths[len(paths)-1], cwd, false); reason != "" {
				return reason
			}
		}

	case "tar":
		if reason := analyzeTar(rest, cwd); reason != "" {
			return reason
		}

	case "unzip", "7z", "7za":
		for i, a := range rest {
			if a == "-C" || a == "-o" {
				if i+1 < len(rest) {
					if reason := checkPath(rest[i+1], cwd, false); reason != "" {
						return reason
					}
				}
			}
		}

	case "find":
		if hasArg(rest, "-delete") || hasArg(rest, "-exec") || hasArg(rest, "-execdir") {
			root := firstNonFlag(rest)
			if root == "" {
				root = "."
			}
			if reason := checkPath(root, cwd, true); reason != "" {
				return reason
			}
		}

	case "mkfs", "mke2fs", "mkswap", "wipefs", "fdisk", "sgdisk", "parted",
		"flash", "fastboot", "make_ext4fs", "e2fsck":
		return "禁止执行分区/格式化类命令：" + verb
	}

	// 兜底：mkfs.ext4 / mke2fs.ext4 这类带后缀的形式
	if strings.HasPrefix(verb, "mkfs") || strings.HasPrefix(verb, "mke2fs") {
		return "禁止执行文件系统格式化命令：" + verb
	}
	return ""
}

// analyzeTar 只拦解压目标，不拦打包源：
// `tar -czf x /data/adb/foo` 是备份（读），`tar -xzf x -C /` 是覆盖（写）。
func analyzeTar(rest []string, cwd string) string {
	extract := false
	for _, a := range rest {
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "x") {
			extract = true
		}
	}
	if !extract {
		return ""
	}
	for i, a := range rest {
		if a == "-C" || a == "--directory" {
			if i+1 < len(rest) {
				return checkPath(rest[i+1], cwd, true)
			}
		}
	}
	// 没有 -C 时解压到 cwd；解压是整棵树的覆盖写，按递归判定
	return checkPath(cwd, cwd, true)
}

func checkPath(p, cwd string, recursive bool) string {
	if p == "" || strings.HasPrefix(p, "-") {
		return ""
	}
	abs := joinCwd(cwd, p)

	if strings.ContainsAny(abs, "*?[") {
		if d := pathguard.CheckGlob(abs); !d.Allowed {
			return d.Reason
		}
		return ""
	}
	if d := pathguard.Check(abs, recursive); !d.Allowed {
		return d.Reason
	}
	return ""
}

func joinCwd(cwd, p string) string {
	if strings.HasPrefix(p, "/") {
		return path.Clean(p)
	}
	if cwd == "" {
		cwd = "/"
	}
	return path.Clean(path.Join(cwd, p))
}

// shellC 识别 `sh -c '...'` / `su -c '...'` / `su 2000 -c '...'`。
func shellC(args []string) (string, bool) {
	switch path.Base(args[0]) {
	case "sh", "ash", "bash", "mksh", "dash", "su":
	default:
		return "", false
	}
	for i := 1; i < len(args)-1; i++ {
		if args[i] == "-c" {
			return args[i+1], true
		}
	}
	return "", false
}

func hasShortFlag(args []string, c byte) bool {
	for _, a := range args {
		if len(a) >= 2 && a[0] == '-' && a[1] != '-' {
			if strings.IndexByte(a, c) > 0 {
				return true
			}
		}
	}
	return false
}

func hasLongFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--"+name {
			return true
		}
	}
	return false
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func nonFlagArgs(args []string) []string {
	var out []string
	skipNext := false
	for _, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if a == "-C" || a == "-o" || a == "--directory" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			continue
		}
		out = append(out, a)
	}
	return out
}

func firstNonFlag(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// ---- 分词 ----

type token struct {
	text string
	op   bool
}

type simpleCmd struct {
	args      []string
	redirects []string
}

func tokenize(s string) []token {
	var out []token
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, token{text: buf.String()})
			buf.Reset()
		}
	}

	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\'':
			i++
			for i < len(s) && s[i] != '\'' {
				buf.WriteByte(s[i])
				i++
			}
			i++ // 跳过收尾引号
		case c == '"':
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				buf.WriteByte(s[i])
				i++
			}
			i++
		case c == '\\' && i+1 < len(s):
			i++
			buf.WriteByte(s[i])
			i++
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
			i++
		case c == ';' || c == '|' || c == '&' || c == '<' || c == '>':
			flush()
			j := i
			for j < len(s) && (s[j] == ';' || s[j] == '|' || s[j] == '&' || s[j] == '<' || s[j] == '>') {
				j++
			}
			out = append(out, token{text: s[i:j], op: true})
			i = j
		default:
			buf.WriteByte(c)
			i++
		}
	}
	flush()
	return out
}

func splitCommands(toks []token) []simpleCmd {
	var out []simpleCmd
	var cur simpleCmd
	pendingRedirect := false

	for _, t := range toks {
		if t.op {
			switch t.text {
			case ">", ">>":
				pendingRedirect = true
			case ";", "&&", "||", "|", "&":
				out = append(out, cur)
				cur = simpleCmd{}
				pendingRedirect = false
			default:
				// <, <<, <>, >& 等：目标不是写入位置
				pendingRedirect = false
			}
			continue
		}
		if pendingRedirect {
			cur.redirects = append(cur.redirects, t.text)
			pendingRedirect = false
			continue
		}
		cur.args = append(cur.args, t.text)
	}
	out = append(out, cur)
	return out
}
