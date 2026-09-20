package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/novaai/novaai-mcp/internal/config"
)

type Entry struct {
	TS          string   `json:"ts"`
	Seq         int64    `json:"seq"`
	Session     string   `json:"session,omitempty"`
	Peer        PeerInfo `json:"peer,omitempty"`
	Profile     string   `json:"profile,omitempty"`
	Event       string   `json:"event"`
	Tool        string   `json:"tool,omitempty"`
	Risk        int      `json:"risk,omitempty"`
	ArgsPreview any      `json:"args_preview,omitempty"`
	Result      string   `json:"result,omitempty"`
	DurationMS  int64    `json:"duration_ms,omitempty"`
	ExitCode    int      `json:"exit_code,omitempty"`
	Detail      string   `json:"detail,omitempty"`
}

type PeerInfo struct {
	Type string `json:"type"`
	IP   string `json:"ip,omitempty"`
	Port int    `json:"port,omitempty"`
}

// Logger 是 JSONL 审计写入器。
//
// 轮转规则只有两条：按日切文件（0600，目录 0700），单文件超过
// maxFileBytes 再切一次。保留 retentionDays 天。
type Logger struct {
	mu      sync.Mutex
	cfg     *config.AuditConfig
	dir     string
	curFile *os.File
	curSize int64
	curDate string
	rotated int
	seq     int64
	closed  bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

func NewLogger(cfg *config.Config) (*Logger, error) {
	dir := cfg.AuditDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	l := &Logger{
		cfg:    &cfg.Audit,
		dir:    dir,
		stopCh: make(chan struct{}),
	}
	l.rotateIfNeeded()
	l.wg.Add(1)
	go l.rotateLoop()
	return l, nil
}

func (l *Logger) Log(e Entry) {
	if !l.cfg.Enabled {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}

	e.TS = time.Now().Format(time.RFC3339Nano)
	l.seq++
	e.Seq = l.seq

	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	line = append(line, '\n')

	if l.curFile == nil {
		if err := l.rotateIfNeeded(); err != nil {
			return
		}
	}

	if n, err := l.curFile.Write(line); err == nil {
		l.curSize += int64(n)
	}
	if l.cfg.MaxFileBytes > 0 && l.curSize > l.cfg.MaxFileBytes {
		l.rotate()
	}
}

func (l *Logger) rotateIfNeeded() error {
	today := time.Now().Format("2006-01-02")
	if l.curDate == today && l.curFile != nil {
		return nil
	}
	l.curDate = today
	l.rotated = 0
	l.rotate()
	return nil
}

func (l *Logger) rotate() {
	if l.curFile != nil {
		_ = l.curFile.Close()
	}
	// 按日命名；同一天内的第二次及以后的轮转加序号后缀，
	// 否则会覆盖掉当天的前一个文件。
	name := fmt.Sprintf("audit-%s.jsonl", l.curDate)
	if l.rotated > 0 {
		name = fmt.Sprintf("audit-%s.%d.jsonl", l.curDate, l.rotated)
	}
	l.rotated++

	f, err := os.OpenFile(filepath.Join(l.dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	l.curFile = f
	if info, err := f.Stat(); err == nil {
		l.curSize = info.Size()
	}
	l.pruneOld()
}

// pruneOld 只按保留天数清理，不设文件数上限。
func (l *Logger) pruneOld() {
	if l.cfg.RetentionDays <= 0 {
		return
	}
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -l.cfg.RetentionDays)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(l.dir, info.Name()))
		}
	}
}

func (l *Logger) rotateLoop() {
	defer l.wg.Done()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.mu.Lock()
			_ = l.rotateIfNeeded()
			l.mu.Unlock()
		}
	}
}

func (l *Logger) Close() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	close(l.stopCh)
	if l.curFile != nil {
		_ = l.curFile.Close()
	}
	l.mu.Unlock()
	l.wg.Wait()
}
