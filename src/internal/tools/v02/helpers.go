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

// shellTimeout 返回 limits.shellTimeoutSeconds 对应的默认超时。
//
// 这是 shell 系执行"未显式指定超时"时的唯一默认值来源。不要在调用点
// 另写 60*time.Second —— 那样配置项就变成了摆设。
func shellTimeout(deps *Deps) time.Duration {
	sec := 60
	if deps != nil && deps.Config != nil && deps.Config.ShellTimeoutSeconds > 0 {
		sec = deps.Config.ShellTimeoutSeconds
	}
	return time.Duration(sec) * time.Second
}

func runCmd(ctx context.Context, deps *Deps, tool, name string, args []string, timeout time.Duration) (stdout, stderr string, code int, err error) {
	if timeout <= 0 {
		timeout = shellTimeout(deps)
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
		timeout = shellTimeout(deps)
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
//
// deps 只用于取配置里的默认超时，不得用它访问 Adapter。
func runShRaw(ctx context.Context, deps *Deps, script, stdin string, timeout time.Duration) (string, string, int, error) {
	if timeout <= 0 {
		timeout = shellTimeout(deps)
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
// 通用文件载体（fs_write / fs_manage / archive / transfer_* / download）
// 必须调用；专用 owner 不调用，因为它就是该位置的合法管理者：
// novaai_config 拥有 config.json，novaai_root_module 拥有 /data/adb/modules。
//
// recursive=true 时额外禁止整体递归删除 /data、/sdcard 这类根。
//
// 定性：这是**防手滑**，不是对抗攻击者。shell 可达时它全部可被绕过。
func guardPath(p string, recursive bool) error {
	return pathguard.Check(p, recursive).Err()
}

// guardRead 判定一次**只读**访问。
//
// 读取刻意比写入宽松：/sdcard/Android/data 下的内容本来就该能读
// （同一棵树上有用户自己的 DCIM、Download）。仍然拒绝读取的只有
// /dev/block 这类块设备。
func guardRead(p string) error {
	return pathguard.CheckRead(p).Err()
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
		return deps.Config.WorkspaceRoot()
	}
	if strings.HasPrefix(p, "/") {
		return p
	}
	return path.Join(deps.Config.WorkspaceRoot(), p)
}

func suPrefix() string {
	for _, p := range []string{"/data/adb/ap/bin/su", "/data/adb/ksu/bin/su", "/data/adb/magisk/magisk"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
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

// itoa 是 strconv.Itoa 的包内实现，避免为一个整数格式化引入 import。
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
