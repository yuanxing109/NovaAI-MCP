package upstream

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/novaai/novaai-mcp/internal/config"
)

// launchUpstream 按配置把上游拉起来。
//
// 三种方式：
//
//	intent   调用 Android 的 am start。多数上游是"装了个 App 才有 MCP 端口"
//	         的形态，这是唯一能拉起它们的方式。
//	command  直接 spawn（不经 shell，参数逐个传递 —— 不做字符串拼接，
//	         所以配置里的参数不会变成注入点）。
//	manual   不自动启动，只返回提示。
func launchUpstream(ctx context.Context, u config.UpstreamConfig) error {
	switch u.LaunchType() {
	case config.LaunchIntent:
		return launchIntent(ctx, u)
	case config.LaunchCommand:
		return launchCommand(ctx, u)
	default:
		return fmt.Errorf("上游 %s 的启动方式为 manual，需要手动启动", u.Name)
	}
}

// launchIntent 用 am start 拉起一个 Intent。
func launchIntent(ctx context.Context, u config.UpstreamConfig) error {
	l := u.Launch
	am, err := androidTool("am")
	if err != nil {
		return fmt.Errorf("找不到 am 命令（需要 Android 环境）: %w", err)
	}

	args := []string{"start"}
	switch {
	case l.Action != "":
		args = append(args, "-a", l.Action, "-p", l.Package)
	case l.Activity != "":
		component := l.Activity
		if !hasSlash(component) {
			component = l.Package + "/" + component
		}
		args = append(args, "-n", component)
	default:
		// config.Validate 已经保证不会走到这里（action/activity 至少要有一个），
		// 但保留分支，避免未来放宽校验时静默启动一个"空 Intent"。
		return errors.New("launch 配置缺少 action 或 activity")
	}

	cmd := exec.CommandContext(ctx, am, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("am start 失败: %v: %s", err, summarize(out))
	}
	return nil
}

// launchCommand 直接 spawn 一个命令。
func launchCommand(ctx context.Context, u config.UpstreamConfig) error {
	l := u.Launch
	cmd := exec.CommandContext(ctx, l.Command, l.Args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 %s 失败: %v", l.Command, err)
	}
	// 不 Wait：上游是要长期运行的守护进程。这里把它交给 init 收养，
	// 我们不持有它 —— 因为 launch 的语义是"拉起来"，不是"由我托管"。
	go func() { _ = cmd.Wait() }()
	return nil
}

// launchAndWait 拉起上游并等它就绪，然后重探一次。
//
// 返回的 (bool, string) 表示"是否已 running"与失败原因。
func (r *Registry) launchAndWait(ctx context.Context, name string) (bool, string) {
	r.mu.RLock()
	e := r.entries[name]
	if e == nil {
		r.mu.RUnlock()
		return false, "未知上游"
	}
	cfg := e.cfg
	r.mu.RUnlock()

	if err := launchUpstream(ctx, cfg); err != nil {
		r.log("upstream_launch", name, "error", err.Error())
		return false, err.Error()
	}
	r.log("upstream_launch", name, "ok", cfg.LaunchType())

	// 固定等待，不做轮询：Intent 拉起的是 App，冷启动时间不可预测，
	// 轮询上限只能拍一个数字，反而更难解释。等不到就让调用方看到失败。
	select {
	case <-time.After(launchWait):
	case <-ctx.Done():
		return false, ctx.Err().Error()
	}

	r.probe(ctx, name)

	r.mu.RLock()
	defer r.mu.RUnlock()
	e = r.entries[name]
	if e == nil {
		return false, "未知上游"
	}
	if e.status == config.UpstreamRunning {
		return true, ""
	}
	return false, fmt.Sprintf("启动后仍未就绪（%s）: %s", e.status, e.reason)
}

// androidTool 在 Android 的常见 bin 目录里找一个命令。
//
// 不直接依赖 PATH：守护进程是被 `su -c` 拉起来的，PATH 由 root 框架决定，
// 不同 ROM 不一致。/system/bin 是稳定存在的那个。
func androidTool(name string) (string, error) {
	for _, dir := range []string{"/system/bin", "/system/xbin", "/vendor/bin"} {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("在 /system/bin、/system/xbin、/vendor/bin 与 PATH 中均未找到 %s", name)
}

func hasSlash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return true
		}
	}
	return false
}
