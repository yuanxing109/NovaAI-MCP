package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/novaai/novaai-mcp/internal/adapter"
	"github.com/novaai/novaai-mcp/internal/audit"
	"github.com/novaai/novaai-mcp/internal/auth"
	"github.com/novaai/novaai-mcp/internal/config"
	"github.com/novaai/novaai-mcp/internal/health"
	"github.com/novaai/novaai-mcp/internal/mcp"
	"github.com/novaai/novaai-mcp/internal/migrate"
	"github.com/novaai/novaai-mcp/internal/pathguard"
	"github.com/novaai/novaai-mcp/internal/profile"
	"github.com/novaai/novaai-mcp/internal/ratelimit"
	"github.com/novaai/novaai-mcp/internal/session"
	"github.com/novaai/novaai-mcp/internal/shutdown"
	"github.com/novaai/novaai-mcp/internal/tools"
)

var (
	Version = "dev"
	Commit  = "unknown"
)

const (
	defaultStateDir   = "/data/adb/novaai-mcp"
	defaultConfigFile = "config.json"
	defaultTokenFile  = "token"
)

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

	// 崩溃信号处理
	health.InstallCrashHandlers(crashDir, Version, Commit)

	configPath := filepath.Join(*stateDir, defaultConfigFile)
	tokenPath := filepath.Join(*stateDir, defaultTokenFile)

	cfg, err := loadOrMigrateConfig(*stateDir, configPath, tokenPath)
	if err != nil {
		log.Fatalf("配置加载失败: %v", err)
	}
	log.Printf("配置加载完成: schema=%d token.enabled=%v lan.enabled=%v",
		cfg.SchemaVersion, cfg.Security.Token.Enabled, cfg.Security.LAN.Enabled)

	// pathguard 的受保护前缀跟随实际 stateDir，避免把路径写死在两处。
	pathguard.SetStateDir(cfg.Paths.StateDir)

	auditLogger, err := audit.NewLogger(cfg)
	if err != nil {
		log.Fatalf("审计初始化失败: %v", err)
	}
	defer auditLogger.Close()

	profileStore := profile.NewStore(cfg)
	sessionMgr := session.NewManager(cfg, auditLogger)
	defer sessionMgr.Stop()
	defer sessionMgr.CloseAll()

	rateLimiter := ratelimit.NewLimiter(cfg)

	cmdAdapter, err := adapter.Detect(nil)
	if err != nil {
		log.Printf("适配层探测失败，使用 AOSP 兜底: %v", err)
		cmdAdapter = adapter.NewAOSPAdapter(nil)
	}
	log.Printf("命令适配层: %s v%s", cmdAdapter.Name(), cmdAdapter.Version())

	// 组装工具依赖
	deps := &tools.Deps{
		Config:   cfg,
		Audit:    auditLogger,
		Sessions: sessionMgr,
		Profiles: profileStore,
		Adapter:  cmdAdapter,
		Version:  Version,
		Commit:   Commit,
		StateDir: *stateDir,
	}

	registry := tools.NewRegistry()
	tools.RegisterAll(registry, deps)
	log.Printf("已注册工具 %d 个", registry.Count())

	server := mcp.NewServer(&mcp.ServerConfig{
		Config:    cfg,
		Registry:  registry,
		Audit:     auditLogger,
		Sessions:  sessionMgr,
		RateLimit: rateLimiter,
		Deps:      deps,
	})

	handler := mcp.BuildMiddlewareChain(server, &mcp.MiddlewareConfig{
		Config:    cfg,
		Audit:     auditLogger,
		Sessions:  sessionMgr,
		RateLimit: rateLimiter,
		Registry:  registry,
		Profiles:  profileStore,
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

func loadOrMigrateConfig(stateDir, configPath, tokenPath string) (*config.Config, error) {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Printf("未检测到配置，生成默认配置")
		if err := config.WriteAtomic(configPath, config.Default()); err != nil {
			return nil, err
		}
	} else if err := migrate.RunIfNeeded(stateDir, configPath, tokenPath); err != nil {
		log.Printf("迁移失败: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	if err := syncToken(cfg, tokenPath); err != nil {
		return nil, err
	}
	return cfg, nil
}

// syncToken 让 token 文件成为唯一事实来源。
//
// 历史问题：默认配置里随机生成一个 token，而 token 文件里是另一个随机 token，
// action.sh 展示的是文件里的那个，客户端照着配必然 401。这里统一以文件为准。
func syncToken(cfg *config.Config, tokenPath string) error {
	if cfg.Security.Token.RotateOnStart {
		tok, err := auth.GenerateToken()
		if err != nil {
			return err
		}
		cfg.Security.Token.Value = tok
		if err := auth.WriteToken(tokenPath, tok); err != nil {
			return err
		}
		if err := config.WriteAtomic(filepath.Join(filepath.Dir(tokenPath), defaultConfigFile), cfg); err != nil {
			return err
		}
		log.Printf("已轮换 token（rotateOnStart=true）")
		return nil
	}

	tok, err := auth.LoadToken(tokenPath)
	if err != nil || tok == "" {
		if cfg.Security.Token.Value == "" {
			t, genErr := auth.GenerateToken()
			if genErr != nil {
				return genErr
			}
			cfg.Security.Token.Value = t
		}
		return auth.WriteToken(tokenPath, cfg.Security.Token.Value)
	}

	cfg.Security.Token.Value = tok
	return auth.EnsurePerms(tokenPath)
}
