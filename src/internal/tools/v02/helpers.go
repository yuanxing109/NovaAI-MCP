package v02

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/novaai/novaai-mcp/internal/antibrick"
	"github.com/novaai/novaai-mcp/internal/pathguard"
	"github.com/novaai/novaai-mcp/internal/util"
)

func runCmd(ctx context.Context, deps *Deps, tool, name string, args []string, timeout time.Duration) (stdout, stderr string, code int, err error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	finalArgs := deps.Adapter.Preprocess(tool, name, args)

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, name, finalArgs...)
	configureProcAttr(cmd)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()

	processed, _ := deps.Adapter.Postprocess(tool, name, []byte(outBuf.String()))
	stdout = string(processed)
	stderr = errBuf.String()

	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			return stdout, stderr, ee.ExitCode(), nil
		}
		if cctx.Err() == context.DeadlineExceeded {
			return stdout, stderr, -1, fmt.Errorf("命令超时")
		}
		return stdout, stderr, -1, runErr
	}
	return stdout, stderr, 0, nil
}

func runSh(ctx context.Context, deps *Deps, tool, script string, timeout time.Duration) (string, string, int, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	// 命令拦截检查
	result := antibrick.InterceptCommand(script)
	if !result.Allowed {
		return "", result.Reason, -1, fmt.Errorf("命令被拦截: %s", result.Reason)
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "/system/bin/sh", "-c", script)
	configureProcAttr(cmd)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()

	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			return outBuf.String(), errBuf.String(), ee.ExitCode(), nil
		}
		if cctx.Err() == context.DeadlineExceeded {
			return outBuf.String(), errBuf.String(), -1, errors.New("命令超时")
		}
		return outBuf.String(), errBuf.String(), -1, runErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
}

// runShRaw 直接执行 /system/bin/sh -c，不经过 Adapter。
// 硬性规定：shell/script 工具使用本函数，Adapter.Preprocess 不得介入。
// 所有裸 shell 入口都必须过 antibrick 拦截，这是唯一的收口点。
func runShRaw(ctx context.Context, script, stdin string, timeout time.Duration) (string, string, int, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	// 命令拦截检查
	result := antibrick.InterceptCommand(script)
	if !result.Allowed {
		return "", result.Reason, -1, fmt.Errorf("命令被拦截: %s", result.Reason)
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "/system/bin/sh", "-c", script)
	configureProcAttr(cmd)
	// 超时/取消时杀整个进程组，避免残留子进程
	cmd.Cancel = func() error { return killProcessGroup(cmd) }

	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	runErr := cmd.Run()

	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			return outBuf.String(), errBuf.String(), ee.ExitCode(), nil
		}
		if cctx.Err() == context.DeadlineExceeded {
			return outBuf.String(), errBuf.String(), -1, fmt.Errorf("命令超时")
		}
		return outBuf.String(), errBuf.String(), -1, runErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
}

// shQuote 是 util.ShQuote 的包内别名，实现只有一份（internal/util/quote.go）。
func shQuote(s string) string { return util.ShQuote(s) }

// guardPath 在文件变更真正落地之前判定受保护路径。
//
// 通用文件载体（fs_write / fs_manage / archive / transfer_* / download /
// backup restore）必须调用；专用 owner 不调用，因为它就是该位置的合法
// 管理者：novaai_config 拥有 config.json，novaai_root_module 与
// novaai_hook_* 拥有 /data/adb/modules。
//
// recursive=true 时额外禁止整体递归删除 /data、/sdcard 这类根。
func guardPath(p string, recursive bool) error {
	return pathguard.Check(p, recursive).Err()
}

// idRe 校验所有会被拼进文件路径的标识符（模块 ID、技能 ID、scheduleId…）。
//
// 必须以字母或数字开头，因此 ".."、"../x"、"/abs" 一律不通过；
// 这是路径穿越的唯一防线，不要再在别处复制第二份。
var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// resolvePath 把工具参数里的路径转成绝对路径。
//
// 这里用 POSIX 语义（"以 / 开头"）而不是 filepath.IsAbs：目标平台是
// Android，两者结论一致；但判定"是否绝对"必须与 pathguard 用同一套规则，
// 否则同一个字符串在两处得到不同解释，防护就会出现缝隙。
func resolvePath(deps *Deps, p string) string {
	if p == "" {
		return deps.Config.Paths.WorkspaceRoot
	}
	if strings.HasPrefix(p, "/") {
		return p
	}
	return path.Join(deps.Config.Paths.WorkspaceRoot, p)
}

func suPrefix() string {
	for _, p := range []string{"/data/adb/ap/bin/su", "/data/adb/ksu/bin/su", "/data/adb/magisk/magisk"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func asInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return 0
}

func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func splitLines(s string) []string {
	out := []string{}
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
