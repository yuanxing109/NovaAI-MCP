package shutdown

import (
	"context"
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"os"
	"time"

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
	if r.cfg.Listen != "" {
		addr := r.cfg.Listen
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

	if r.cfg.UnixSocket != "" {
		ln, err := startUnixSocket(r.cfg.UnixSocket, handler)
		if err != nil {
			log.Printf("Unix socket 启动失败: %v", err)
		} else {
			r.unixLn = ln
			if err := applySocketContext(r.cfg.UnixSocket); err != nil {
				log.Printf("SELinux context 应用失败（不阻塞）: %v", err)
			}
			log.Printf("Unix socket 监听 %s", r.cfg.UnixSocket)
		}
	}
}

func (r *Runner) GracefulShutdown() {
	time.Sleep(2 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(config.ShutdownGraceSec)*time.Second)
	defer cancel()
	if r.http != nil {
		_ = r.http.Shutdown(ctx)
	}

	r.mgr.CloseAll()

	_ = os.Remove(r.cfg.UnixSocket)

	log.Printf("优雅关闭完成")
}
