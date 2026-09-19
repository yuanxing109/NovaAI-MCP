package shutdown

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/novaai/novaai-mcp/internal/auth"
	"github.com/novaai/novaai-mcp/internal/config"
	"github.com/novaai/novaai-mcp/internal/mcp"
	"github.com/novaai/novaai-mcp/internal/session"
)

type Runner struct {
	cfg    *config.Config
	server *mcp.Server
	mgr    *session.Manager
	http   *http.Server
	unixLn net.Listener
}

func NewRunner(cfg *config.Config, server *mcp.Server, mgr *session.Manager) *Runner {
	return &Runner{cfg: cfg, server: server, mgr: mgr}
}

func (r *Runner) Start(handler http.Handler) {
	if r.cfg.Network.ListenLoopback || r.cfg.Network.ListenLAN {
		addr := fmt.Sprintf(":%d", r.cfg.Network.Port)
		if !r.cfg.Network.ListenLAN {
			addr = fmt.Sprintf("127.0.0.1:%d", r.cfg.Network.Port)
		}

		srv := &http.Server{
			Addr:         addr,
			Handler:      handler,
			ReadTimeout:  60 * time.Second,
			WriteTimeout: 0,
			IdleTimeout:  120 * time.Second,
			TLSConfig:    &tls.Config{MinVersion: tls.VersionTLS12},
		}
		r.http = srv

		go func() {
			log.Printf("TCP 监听 %s", addr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("TCP server 错误: %v", err)
			}
		}()
	}

	if r.cfg.Security.UnixSocket.Enabled {
		ln, err := auth.StartUnixSocket(
			r.cfg.Security.UnixSocket.Path,
			r.cfg.Security.UnixSocket.Mode,
			handler,
		)
		if err != nil {
			log.Printf("Unix socket 启动失败: %v", err)
		} else {
			r.unixLn = ln
			if r.cfg.Security.UnixSocket.SepolicyInject {
				if err := auth.ApplySocketContext(r.cfg.Security.UnixSocket.Path); err != nil {
					log.Printf("SELinux context 应用失败（不阻塞）: %v", err)
				}
			}
			log.Printf("Unix socket 监听 %s", r.cfg.Security.UnixSocket.Path)
		}
	}
}

func (r *Runner) GracefulShutdown() {
	r.server.BroadcastShutdown()

	time.Sleep(2 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(r.cfg.Limits.ShutdownGraceSec)*time.Second)
	defer cancel()
	if r.http != nil {
		_ = r.http.Shutdown(ctx)
	}

	r.mgr.CloseAll()

	_ = os.Remove(r.cfg.Security.UnixSocket.Path)

	log.Printf("优雅关闭完成")
}
