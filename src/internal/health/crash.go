package health

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"syscall"
	"time"
)

type CrashEntry struct {
	TS        string `json:"ts"`
	Kind      string `json:"kind"`
	Payload   string `json:"payload"`
	Stack     string `json:"stack"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	GoVersion string `json:"goVersion"`
}

// InstallCrashHandlers 安装信号级崩溃处理器。
// 主 goroutine 的 panic 由调用方通过 recover + DumpPanic 处理。
func InstallCrashHandlers(dir, version, commit string) {
	_ = os.MkdirAll(dir, 0700)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGSEGV, syscall.SIGABRT, syscall.SIGBUS)
	go func() {
		sig := <-sigCh
		dumpCrash(dir, "signal", sig.String(), debug.Stack(), version, commit)
		os.Exit(2)
	}()
}

// DumpPanic 由调用方在 recover 后调用，写入 panic 崩溃文件。
func DumpPanic(dir, version, commit string, r any) {
	dumpCrash(dir, "panic", r, debug.Stack(), version, commit)
}

func dumpCrash(dir, kind string, payload any, stack []byte, version, commit string) {
	_ = os.MkdirAll(dir, 0700)
	e := CrashEntry{
		TS:        time.Now().Format(time.RFC3339),
		Kind:      kind,
		Payload:   fmt.Sprint(payload),
		Stack:     string(stack),
		Version:   version,
		Commit:    commit,
		GoVersion: runtime.Version(),
	}
	raw, _ := json.MarshalIndent(e, "", "  ")
	name := fmt.Sprintf("crash-%d-%s.json", time.Now().UnixNano(), kind)
	_ = os.WriteFile(filepath.Join(dir, name), raw, 0600)
	pruneDir(dir, 10)
}

func pruneDir(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var files []os.FileInfo
	for _, e := range entries {
		info, err := e.Info()
		if err == nil {
			files = append(files, info)
		}
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime().After(files[j].ModTime())
	})
	for i := keep; i < len(files); i++ {
		_ = os.Remove(filepath.Join(dir, files[i].Name()))
	}
}
