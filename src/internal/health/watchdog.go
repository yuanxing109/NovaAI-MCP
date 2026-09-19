package health

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/novaai/novaai-mcp/internal/audit"
	"github.com/novaai/novaai-mcp/internal/config"
	"github.com/novaai/novaai-mcp/internal/util"
)

type WatchdogConfig struct {
	Audit    *audit.Logger
	StateDir string
}

type watchdogState struct {
	startTime time.Time
	crashes   int64
}

var defaultWatchdog = &watchdogState{startTime: time.Now()}

const lowDiskThresholdBytes int64 = 100 * 1024 * 1024

func StartWatchdog(cfg *config.Config, wc *WatchdogConfig) {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			checkHealth(cfg, wc)
		}
	}()
}

func checkHealth(cfg *config.Config, wc *WatchdogConfig) {
	if wc.StateDir != "" {
		free, err := util.FreeBytes(wc.StateDir)
		if err == nil && free < lowDiskThresholdBytes {
			wc.Audit.Log(audit.Entry{
				Event:  "health_low_disk",
				Detail: fmt.Sprintf("free=%s", formatBytes(free)),
			})
		}
	}

	crashDir := filepath.Join(wc.StateDir, "crash")
	entries, err := os.ReadDir(crashDir)
	if err == nil {
		atomic.StoreInt64(&defaultWatchdog.crashes, int64(len(entries)))
	}
}

func Snapshot() map[string]any {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return map[string]any{
		"uptimeSeconds": int(time.Since(defaultWatchdog.startTime).Seconds()),
		"goroutines":    runtime.NumGoroutine(),
		"memoryAlloc":   m.Alloc,
		"memorySys":     m.Sys,
		"crashes":       atomic.LoadInt64(&defaultWatchdog.crashes),
		"goVersion":     runtime.Version(),
	}
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
