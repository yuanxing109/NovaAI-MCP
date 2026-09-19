package v02

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func registerArchiveTools(reg RegisterFn, deps *Deps) {
	// ---- archive ----
	reg("novaai_archive", "归档", "创建、解压、列出或校验 ZIP/TAR/GZIP/XZ/7z",
		objSchema(map[string]any{
			"action":      enumProp("操作", "create", "extract", "list", "test"),
			"format":      enumProp("格式", "zip", "tar", "tar.gz", "tgz", "gzip", "gz", "xz", "tar.xz", "txz", "7z"),
			"source":      arrProp("源路径"),
			"destination": strProp("目标路径"),
			"overwrite":   boolProp("覆盖已存在"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action      string   `json:"action"`
				Format      string   `json:"format"`
				Source      []string `json:"source"`
				Destination string   `json:"destination"`
				Overwrite   bool     `json:"overwrite"`
			}
			_ = json.Unmarshal(args, &in)

			if in.Format == "" {
				in.Format = "zip"
			}
			for i, s := range in.Source {
				in.Source[i] = resolvePath(deps, s)
			}
			in.Destination = resolvePath(deps, in.Destination)

			if len(in.Source) == 0 {
				return errFail("MISSING_SOURCE", "source 必填"), nil
			}

			bin, err := find7z(deps)
			if err != nil {
				return errFail("NO_7Z", err.Error()), nil
			}

			var cmd string
			switch in.Action {
			case "create":
				if in.Destination == "" {
					return errFail("MISSING_DEST", "destination 必填"), nil
				}
				if err := guardPath(in.Destination, false); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				switch in.Format {
				case "zip":
					cmd = fmt.Sprintf("%s a -tzip %s %s", shQuote(bin), shQuote(in.Destination), quoteAll(in.Source))
				case "7z":
					cmd = fmt.Sprintf("%s a -t7z %s %s", shQuote(bin), shQuote(in.Destination), quoteAll(in.Source))
				case "tar", "tar.gz", "tgz":
					cmd = fmt.Sprintf("tar -czf %s -C %s %s",
						shQuote(in.Destination), shQuote(filepath.Dir(in.Source[0])), shQuote(filepath.Base(in.Source[0])))
				case "xz", "tar.xz", "txz":
					cmd = fmt.Sprintf("tar -cJf %s -C %s %s",
						shQuote(in.Destination), shQuote(filepath.Dir(in.Source[0])), shQuote(filepath.Base(in.Source[0])))
				default:
					cmd = fmt.Sprintf("%s a %s %s", shQuote(bin), shQuote(in.Destination), quoteAll(in.Source))
				}
			case "extract":
				if in.Destination == "" {
					in.Destination = filepath.Dir(in.Source[0])
				}
				if err := guardPath(in.Destination, true); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				_ = os.MkdirAll(in.Destination, 0755)
				cmd = fmt.Sprintf("%s x %s -o%s -y", shQuote(bin), shQuote(in.Source[0]), shQuote(in.Destination))
			case "list":
				cmd = fmt.Sprintf("%s l %s", shQuote(bin), shQuote(in.Source[0]))
			case "test":
				cmd = fmt.Sprintf("%s t %s", shQuote(bin), shQuote(in.Source[0]))
			default:
				return errFail("UNKNOWN_ACTION", in.Action), nil
			}

			out, errOut, code, err := runSh(ctx, deps, "novaai_archive", cmd, 5*time.Minute)
			if err != nil || code != 0 {
				return errFail("ARCHIVE_FAILED", errOut), nil
			}
			return ok(map[string]any{
				"action": in.Action,
				"output": out,
				"path":   in.Destination,
			}), nil
		})

	// ---- download ----
	reg("novaai_download", "下载", "下载文件，支持重试、断点续传与 SHA-256 校验",
		objSchema(map[string]any{
			"action":      enumProp("操作", "start"),
			"url":         strProp("URL"),
			"destination": strProp("目标路径"),
			"headers":     map[string]any{"type": "object"},
			"sha256":      strProp("期望 SHA-256"),
			"retries":     intProp("重试次数"),
			"resume":      boolProp("断点续传"),
			"timeoutMs":   intProp("超时毫秒"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action      string            `json:"action"`
				URL         string            `json:"url"`
				Destination string            `json:"destination"`
				Headers     map[string]string `json:"headers"`
				SHA256      string            `json:"sha256"`
				Retries     int               `json:"retries"`
				Resume      bool              `json:"resume"`
				TimeoutMs   int               `json:"timeoutMs"`
			}
			_ = json.Unmarshal(args, &in)

			if in.Retries <= 0 {
				in.Retries = 3
			}
			if in.TimeoutMs <= 0 {
				in.TimeoutMs = 300000
			}
			in.Destination = resolvePath(deps, in.Destination)

			switch in.Action {
			case "start":
				if in.URL == "" || in.Destination == "" {
					return errFail("MISSING_PARAM", "url 和 destination 必填"), nil
				}
				if err := guardPath(in.Destination, false); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				var headerArgs strings.Builder
				for k, v := range in.Headers {
					headerArgs.WriteString(fmt.Sprintf(" -H %s", shQuote(k+": "+v)))
				}

				resume := ""
				if in.Resume {
					resume = "-C -"
				}

				cmd := fmt.Sprintf(
					"curl -fL --retry %d --max-time %d -o %s%s%s %s",
					in.Retries, in.TimeoutMs/1000, shQuote(in.Destination),
					resume, headerArgs.String(), shQuote(in.URL),
				)

				_, errOut, code, err := runSh(ctx, deps, "novaai_download", cmd,
					time.Duration(in.TimeoutMs)*time.Millisecond)
				if err != nil || code != 0 {
					return errFail("DOWNLOAD_FAILED", errOut), nil
				}

				info, _ := os.Stat(in.Destination)
				size := int64(0)
				if info != nil {
					size = info.Size()
				}

				var actualSHA string
				if in.SHA256 != "" {
					actualSHA, _ = computeHash(in.Destination, "sha256")
					if !strings.EqualFold(actualSHA, in.SHA256) {
						return errFail("SHA256_MISMATCH",
							fmt.Sprintf("期望 %s 实际 %s", in.SHA256, actualSHA)), nil
					}
				}

				return ok(map[string]any{
					"path":   in.Destination,
					"bytes":  size,
					"sha256": actualSHA,
				}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// ---- transfer_upload ----
	reg("novaai_transfer_upload", "上传", "创建、查询、分块完成或取消上传",
		objSchema(map[string]any{
			"action":       enumProp("操作", "create", "chunk", "status", "complete", "cancel"),
			"path":         strProp("目标路径"),
			"content":      strProp("内容（Base64）"),
			"offset":       intProp("偏移"),
			"size":         intProp("字节数"),
			"expectedSize": intProp("期望字节数"),
			"sha256":       strProp("SHA-256"),
			"overwrite":    boolProp("覆盖"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action       string `json:"action"`
				Path         string `json:"path"`
				Content      string `json:"content"`
				Offset       int64  `json:"offset"`
				Size         int64  `json:"size"`
				ExpectedSize int64  `json:"expectedSize"`
				SHA256       string `json:"sha256"`
				Overwrite    bool   `json:"overwrite"`
			}
			_ = json.Unmarshal(args, &in)
			p := resolvePath(deps, in.Path)

			if err := guardPath(p, false); err != nil {
				return errFail("PROTECTED_PATH", err.Error()), nil
			}

			switch in.Action {
			case "create":
				if !in.Overwrite {
					if _, err := os.Stat(p); err == nil {
						return errFail("EXISTS", "文件已存在"), nil
					}
				}
				_ = os.MkdirAll(filepath.Dir(p), 0755)
				f, err := os.Create(p)
				if err != nil {
					return errFail("CREATE_FAILED", err.Error()), nil
				}
				f.Close()
				return ok(map[string]any{"path": p, "ready": true}), nil
			case "chunk":
				data, err := decodeBase64(in.Content)
				if err != nil {
					return errFail("INVALID_BASE64", err.Error()), nil
				}
				f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0644)
				if err != nil {
					return errFail("OPEN_FAILED", err.Error()), nil
				}
				defer f.Close()
				if _, err := f.Seek(in.Offset, 0); err != nil {
					return errFail("SEEK_FAILED", err.Error()), nil
				}
				n, _ := f.Write(data)
				return ok(map[string]any{"written": n, "offset": in.Offset}), nil
			case "complete":
				info, err := os.Stat(p)
				if err != nil {
					return errFail("STAT_FAILED", err.Error()), nil
				}
				if in.ExpectedSize > 0 && info.Size() != in.ExpectedSize {
					return errFail("SIZE_MISMATCH",
						fmt.Sprintf("期望 %d 实际 %d", in.ExpectedSize, info.Size())), nil
				}
				if in.SHA256 != "" {
					actual, _ := computeHash(p, "sha256")
					if !strings.EqualFold(actual, in.SHA256) {
						return errFail("SHA256_MISMATCH", actual), nil
					}
				}
				return ok(map[string]any{"path": p, "bytes": info.Size()}), nil
			case "status":
				info, err := os.Stat(p)
				if err != nil {
					return errFail("STAT_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"path": p, "bytes": info.Size()}), nil
			case "cancel":
				_ = os.Remove(p)
				return ok(map[string]any{"cancelled": p}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// ---- transfer_export ----
	//
	// 只做一件事：把源路径复制到目标路径。没有"任务产物"这个概念 ——
	// 早期版本声明了 task 动作和 taskId/artifactIndex 参数，但服务里
	// 根本没有任务系统，它们永远不可能有值。
	reg("novaai_transfer_export", "导出", "把文件或目录复制到指定路径",
		objSchema(map[string]any{
			"path":        strProp("文件或目录路径"),
			"destination": strProp("目标路径"),
		}),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Path        string `json:"path"`
				Destination string `json:"destination"`
			}
			_ = json.Unmarshal(args, &in)

			src := resolvePath(deps, in.Path)
			dst := in.Destination
			if dst == "" {
				dst = filepath.Join(deps.Config.Paths.WorkspaceRoot, "export",
					fmt.Sprintf("export-%d", time.Now().UnixNano()))
			} else {
				dst = resolvePath(deps, dst)
			}

			if err := guardPath(dst, false); err != nil {
				return errFail("PROTECTED_PATH", err.Error()), nil
			}

			info, err := os.Stat(src)
			if err != nil {
				return errFail("STAT_FAILED", err.Error()), nil
			}

			if info.IsDir() {
				if err := copyPath(src, dst, true, true); err != nil {
					return errFail("EXPORT_FAILED", err.Error()), nil
				}
			} else {
				if err := copyFile(src, dst, true); err != nil {
					return errFail("EXPORT_FAILED", err.Error()), nil
				}
			}

			return ok(map[string]any{
				"source":      src,
				"destination": dst,
			}), nil
		})
}

func find7z(deps *Deps) (string, error) {
	candidates := []string{
		"/data/adb/novaai-mcp/bin/7zz",
		"/data/adb/ksu/bin/7zz",
		"/data/adb/magisk/7zz",
		"/system/bin/7z",
		"/system/xbin/7z",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if p, err := lookPath("7z"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("未找到 7zz 或 7z")
}

func quoteAll(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shQuote(a)
	}
	return strings.Join(parts, " ")
}
