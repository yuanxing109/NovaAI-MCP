package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"github.com/novaai/novaai-mcp/internal/adapter"
	"github.com/novaai/novaai-mcp/internal/audit"
	"github.com/novaai/novaai-mcp/internal/config"
	"github.com/novaai/novaai-mcp/internal/health"
	"github.com/novaai/novaai-mcp/internal/mcp"
	"github.com/novaai/novaai-mcp/internal/pathguard"
	"github.com/novaai/novaai-mcp/internal/profile"
	"github.com/novaai/novaai-mcp/internal/ratelimit"
	"github.com/novaai/novaai-mcp/internal/session"
	"github.com/novaai/novaai-mcp/internal/shutdown"
	"github.com/novaai/novaai-mcp/internal/tools"
	"github.com/novaai/novaai-mcp/internal/upstream"
)

var (
	Version = "dev"
	Commit  = "unknown"
)

const (
	defaultStateDir   = "/data/adb/novaai-mcp"
	defaultConfigFile = "config.json"
	pidFileName       = "novaaimcpd.pid"
)

// writePIDFile 把当前进程 PID 写入 path。
//
// 权限 0600：状态目录本身是 0700，但 PID 文件在 shell 侧（root）与
// 可能的其他本地调用方之间共享，收紧权限没有坏处。
func writePIDFile(path string) error {
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0600)
}

func main() {
	stateDir := flag.String("state", defaultStateDir, "state directory")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetPrefix("[novaaimcpd] ")
	log.Printf("NovaAI-MCP v%s (commit=%s, go=%s, os=%s/%s)",
		Version, Commit, runtime.Version(), runtime.GOOS, runtime.GOARCH)

	crashDir := filepath.Join(*stateDir, "crash")

	// 主 goroutine panic 兜底
	defer func() {
		if r := recover(); r != nil {
			health.DumpPanic(crashDir, Version, Commit, r)
			os.Exit(2)
		}
	}()

	if err := prepareStateDir(*stateDir); err != nil {
		log.Fatalf("准备状态目录失败: %v", err)
	}

	// PID 文件由 daemon 自己写（唯一 owner），shell 侧只读。
	//
	// 旧实现由 shell 写 `$!`，那是 `su` 进程的 PID；而 daemon 是 su 的孙进程，
	// `kill -TERM` 因此可能到不了 daemon，卸载后它仍占着端口与 socket。
	// 见 docs/KNOWN_ISSUES.md。
	//
	// 被 SIGKILL 或 log.Fatalf 时会残留本文件；shell 侧 zcr_is_daemon 会按
	// /proc/<pid>/comm 校验身份，残留文件不会误杀复用了该 PID 的进程。
	pidPath := filepath.Join(*stateDir, pidFileName)
	if err := writePIDFile(pidPath); err != nil {
		log.Printf("写 PID 文件失败（不影响启动）: %v", err)
	}
	defer os.Remove(pidPath)

	// 崩溃信号处理
	health.InstallCrashHandlers(crashDir, Version, Commit)

	configPath := filepath.Join(*stateDir, defaultConfigFile)

	cfg, err := loadConfig(configPath)
	if err != nil {
		log.Fatalf("配置加载失败: %v", err)
	}
	log.Printf("配置加载完成: listen=%s profile=%s", cfg.Listen, cfg.Profile)

	// pathguard 的受保护前缀跟随实际 stateDir，避免把路径写死在两处。
	pathguard.SetStateDir(cfg.StateDir)

	auditLogger, err := audit.NewLogger(cfg)
	if err != nil {
		log.Fatalf("审计初始化失败: %v", err)
	}
	defer auditLogger.Close()

	profileStore := profile.NewStore()
	sessionMgr := session.NewManager(auditLogger)
	defer sessionMgr.Stop()
	defer sessionMgr.CloseAll()

	rateLimiter := ratelimit.NewLimiter(cfg)

	// ---- 上游 MCP 聚合 ----
	//
	// 注册表在启动时构造并**同步探测一次**：tools/list 的合并结果依赖
	// 探测结果，如果异步探测，第一个客户端大概率看到一个还没有上游工具的
	// 列表，然后要等一次 polling 才会变 —— 而本服务不做后台轮询，
	// 那个"之后"永远不会到来。
	//
	// 探测是并发 + 各自带超时的（见 Registry.ProbeAll），并且整体再加一道
	// StartupProbeTimeout 的上限 —— 配了不可达的上游时，宁可先起来，
	// 也不要为了一个探测把启动拖满一个 shellTimeoutSeconds。
	upstreams := upstream.NewRegistry(cfg)
	upstreams.SetAudit(auditLogger)
	if n := upstreams.Count(); n > 0 {
		log.Printf("探测上游 MCP %d 个", n)
		probeCtx, cancelProbe := context.WithTimeout(context.Background(), upstream.StartupProbeTimeout)
		upstreams.Init(probeCtx)
		cancelProbe()
		for _, st := range upstreams.Status() {
			log.Printf("  上游 %s: %s（工具 %d）", st.Name, st.Status, st.Tools)
		}
	}
	defer upstreams.Close()

	cmdAdapter, err := adapter.Detect(nil)
	if err != nil {
		log.Printf("适配层探测失败，使用 AOSP 兜底: %v", err)
		cmdAdapter = adapter.NewAOSPAdapter(nil)
	}
	log.Printf("命令适配层: %s v%s", cmdAdapter.Name(), cmdAdapter.Version())

	// 组装工具依赖
	deps := &tools.Deps{
		Config:    cfg,
		Audit:     auditLogger,
		Sessions:  sessionMgr,
		Profiles:  profileStore,
		Adapter:   cmdAdapter,
		Version:   Version,
		Commit:    Commit,
		StateDir:  *stateDir,
		Upstreams: upstreams,
	}

	registry := tools.NewRegistry()
	tools.RegisterAll(registry, deps)
	log.Printf("已注册本地工具 %d 个，合并上游工具 %d 个",
		registry.Count(), upstreams.ToolCount())

	server := mcp.NewServer(&mcp.ServerConfig{
		Config:    cfg,
		Registry:  registry,
		Audit:     auditLogger,
		RateLimit: rateLimiter,
		Deps:      deps,
		Upstreams: upstreams,
	})

	handler := mcp.BuildMiddlewareChain(server, &mcp.MiddlewareConfig{
		Config:    cfg,
		Audit:     auditLogger,
		Sessions:  sessionMgr,
		RateLimit: rateLimiter,
		Registry:  registry,
	})

	runner := shutdown.NewRunner(cfg, server, sessionMgr)
	runner.Start(handler)

	health.StartWatchdog(cfg, &health.WatchdogConfig{
		Audit:    auditLogger,
		StateDir: *stateDir,
	})

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	log.Printf("收到信号 %v，开始优雅关闭", sig)

	runner.GracefulShutdown()
	log.Printf("已关闭")
}

func prepareStateDir(dir string) error {
	dirs := []string{
		dir,
		filepath.Join(dir, "workspace"),
		filepath.Join(dir, "downloads"),
		filepath.Join(dir, "uploads"),
		filepath.Join(dir, "artifacts"),
		filepath.Join(dir, "tmp"),
		filepath.Join(dir, "audit"),
		filepath.Join(dir, "crash"),
		filepath.Join(dir, "toolchains"),
		filepath.Join(dir, "cache"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0700); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

// loadConfig 读取配置；不存在时生成默认配置。
//
// 配置极简，没有 schema 迁移：字段就那么多，旧配置里的未知键会被
// JSON 反序列化忽略，缺失的键取默认值。
func loadConfig(configPath string) (*config.Config, error) {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Printf("未检测到配置，生成默认配置")
		if err := config.WriteAtomic(configPath, config.Default()); err != nil {
			return nil, err
		}
	}
	return config.Load(configPath)
}
