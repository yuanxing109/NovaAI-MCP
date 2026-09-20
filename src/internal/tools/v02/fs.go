package v02

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func registerFSTools(reg RegisterFn, deps *Deps) {
	// ---- fs_info ----
	reg("novaai_fs_info", "文件信息", "列目录、读取元数据、磁盘与挂载信息",
		objSchema(map[string]any{
			"action": enumProp("明确操作", "stat", "list", "disk", "mounts"),
			"path":   strProp("文件或目录路径"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action string `json:"action"`
				Path   string `json:"path"`
			}
			_ = json.Unmarshal(args, &in)
			p := resolvePath(deps, in.Path)

			switch in.Action {
			case "stat":
				info, err := os.Stat(p)
				if err != nil {
					return errFail("STAT_FAILED", err.Error()), nil
				}
				return ok(map[string]any{
					"path":    p,
					"size":    info.Size(),
					"mode":    info.Mode().String(),
					"modTime": info.ModTime().Format(time.RFC3339),
					"isDir":   info.IsDir(),
				}), nil
			case "list":
				entries, err := os.ReadDir(p)
				if err != nil {
					return errFail("LIST_FAILED", err.Error()), nil
				}
				out := make([]map[string]any, 0, len(entries))
				for _, e := range entries {
					info, _ := e.Info()
					item := map[string]any{
						"name":  e.Name(),
						"isDir": e.IsDir(),
					}
					if info != nil {
						item["size"] = info.Size()
						item["modTime"] = info.ModTime().Format(time.RFC3339)
					}
					out = append(out, item)
				}
				return ok(map[string]any{"path": p, "entries": out}), nil
			case "disk":
				out, _, _, _ := runSh(ctx, deps, "novaai_fs_info", "df -h", 10*time.Second)
				return ok(map[string]any{"raw": out}), nil
			case "mounts":
				b, err := os.ReadFile("/proc/mounts")
				if err != nil {
					return errFail("READ_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"raw": string(b)}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// ---- fs_read ----
	reg("novaai_fs_read", "读取文件", "流式读取文本、二进制、行、尾部或资源链接",
		objSchema(map[string]any{
			"action":    enumProp("明确操作", "text", "binary", "lines", "tail", "resource"),
			"path":      strProp("文件路径"),
			"encoding":  enumProp("编码", "utf-8", "text", "base64"),
			"offset":    intProp("字节偏移或起始行"),
			"length":    intProp("最大读取字节数"),
			"lineCount": intProp("读取行数"),
			"bytes":     intProp("尾部最大字节"),
		}, "action", "path"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action    string `json:"action"`
				Path      string `json:"path"`
				Encoding  string `json:"encoding"`
				Offset    int    `json:"offset"`
				Length    int    `json:"length"`
				LineCount int    `json:"lineCount"`
				Bytes     int    `json:"bytes"`
			}
			_ = json.Unmarshal(args, &in)
			p := resolvePath(deps, in.Path)

			// 读取守卫刻意很窄：只挡块设备与内核参数。
			// 在此之前 fs_read 完全没有守卫，`cat /dev/block/by-name/boot`
			// 会把整个分区的原始字节塞进工具结果。
			if err := guardRead(p); err != nil {
				return errFail("PROTECTED_PATH", err.Error()), nil
			}

			if in.Length <= 0 {
				in.Length = 262144
			}
			if in.Bytes <= 0 {
				in.Bytes = 65536
			}
			if in.LineCount <= 0 {
				in.LineCount = 500
			}

			switch in.Action {
			case "text", "binary", "resource":
				f, err := os.Open(p)
				if err != nil {
					return errFail("OPEN_FAILED", err.Error()), nil
				}
				defer f.Close()
				if in.Offset > 0 {
					_, _ = f.Seek(int64(in.Offset), io.SeekStart)
				}
				buf := make([]byte, in.Length)
				n, _ := io.ReadFull(f, buf)
				data := buf[:n]
				if in.Encoding == "base64" {
					return ok(map[string]any{
						"encoding": "base64",
						"data":     encodeBase64(data),
						"bytes":    n,
					}), nil
				}
				return ok(map[string]any{
					"encoding":  "utf-8",
					"data":      string(data),
					"bytes":     n,
					"truncated": n == in.Length,
				}), nil
			case "lines":
				out, _, _, err := runSh(ctx, deps, "novaai_fs_read",
					fmt.Sprintf("sed -n '%d,%dp' %s", in.Offset+1, in.Offset+in.LineCount, shQuote(p)),
					15*time.Second)
				if err != nil {
					return errFail("READ_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"data": out}), nil
			case "tail":
				out, _, _, err := runSh(ctx, deps, "novaai_fs_read",
					fmt.Sprintf("tail -c %d %s", in.Bytes, shQuote(p)),
					15*time.Second)
				if err != nil {
					return errFail("READ_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"data": out}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// ---- fs_write ----
	reg("novaai_fs_write", "写入文件", "创建、追加、截断、补丁写入或更新时间戳",
		objSchema(map[string]any{
			"action":        enumProp("明确操作", "create", "append", "truncate", "patch", "touch"),
			"path":          strProp("文件路径"),
			"content":       strProp("内容"),
			"encoding":      enumProp("编码", "utf-8", "text", "base64"),
			"offset":        intProp("写入偏移"),
			"createParents": boolProp("自动创建父目录"),
		}, "action", "path"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action        string `json:"action"`
				Path          string `json:"path"`
				Content       string `json:"content"`
				Encoding      string `json:"encoding"`
				Offset        int    `json:"offset"`
				CreateParents bool   `json:"createParents"`
			}
			_ = json.Unmarshal(args, &in)
			p := resolvePath(deps, in.Path)

			// /sdcard/Android/{data,obb} 与 /system 一样是硬拒绝：
			// confirmDangerous 机制已移除，工具层没有放行通道，如需访问走 shell。
			if err := guardPath(p, false); err != nil {
				return errFail("PROTECTED_PATH", err.Error()), nil
			}

			if in.CreateParents {
				_ = os.MkdirAll(filepath.Dir(p), 0755)
			}

			var data []byte
			if in.Encoding == "base64" {
				var err error
				data, err = decodeBase64(in.Content)
				if err != nil {
					return errFail("INVALID_BASE64", err.Error()), nil
				}
			} else {
				data = []byte(in.Content)
			}

			switch in.Action {
			case "create":
				if err := os.WriteFile(p, data, 0644); err != nil {
					return errFail("WRITE_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"path": p, "bytes": len(data)}), nil
			case "append":
				f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
				if err != nil {
					return errFail("OPEN_FAILED", err.Error()), nil
				}
				defer f.Close()
				n, err := f.Write(data)
				if err != nil {
					return errFail("WRITE_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"path": p, "bytes": n}), nil
			case "truncate":
				if err := os.Truncate(p, int64(in.Offset)); err != nil {
					return errFail("TRUNCATE_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"path": p, "size": in.Offset}), nil
			case "patch":
				f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0644)
				if err != nil {
					return errFail("OPEN_FAILED", err.Error()), nil
				}
				defer f.Close()
				if _, err := f.Seek(int64(in.Offset), io.SeekStart); err != nil {
					return errFail("SEEK_FAILED", err.Error()), nil
				}
				n, _ := f.Write(data)
				return ok(map[string]any{"path": p, "bytes": n, "offset": in.Offset}), nil
			case "touch":
				now := time.Now()
				if err := os.Chtimes(p, now, now); err != nil {
					if _, err2 := os.Create(p); err2 == nil {
						return ok(map[string]any{"path": p}), nil
					}
					return errFail("TOUCH_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"path": p}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// ---- fs_manage ----
	reg("novaai_fs_manage", "文件管理", "目录、复制、移动、删除、权限、所有者与链接管理",
		objSchema(map[string]any{
			"action":      enumProp("操作", "mkdir", "copy", "move", "remove", "chmod", "chown", "symlink", "hardlink", "selinux"),
			"path":        strProp("路径"),
			"source":      strProp("源"),
			"destination": strProp("目标"),
			"recursive":   boolProp("递归"),
			"parents":     boolProp("创建父目录"),
			"overwrite":   boolProp("覆盖"),
			"mode":        strProp("权限，如 0644"),
			"uid":         intProp("UID"),
			"gid":         intProp("GID"),
			"target":      strProp("链接目标"),
			"context":     strProp("SELinux context"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action      string `json:"action"`
				Path        string `json:"path"`
				Source      string `json:"source"`
				Destination string `json:"destination"`
				Recursive   bool   `json:"recursive"`
				Parents     bool   `json:"parents"`
				Overwrite   bool   `json:"overwrite"`
				Mode        string `json:"mode"`
				UID         int    `json:"uid"`
				GID         int    `json:"gid"`
				Target      string `json:"target"`
				Context     string `json:"context"`
			}
			_ = json.Unmarshal(args, &in)

			p := resolvePath(deps, in.Path)
			src := resolvePath(deps, in.Source)
			dst := resolvePath(deps, in.Destination)

			switch in.Action {
			case "mkdir":
				if err := guardPath(p, false); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				var err error
				if in.Parents {
					err = os.MkdirAll(p, 0755)
				} else {
					err = os.Mkdir(p, 0755)
				}
				if err != nil {
					return errFail("MKDIR_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"path": p}), nil
			case "copy":
				if err := guardPath(dst, false); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				if err := copyPath(src, dst, in.Recursive, in.Overwrite); err != nil {
					return errFail("COPY_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"src": src, "dst": dst}), nil
			case "move":
				// 源被移走等价于在源位置删除
				if err := guardPath(src, false); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				if err := guardPath(dst, false); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				if err := os.Rename(src, dst); err != nil {
					return errFail("MOVE_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"src": src, "dst": dst}), nil
			case "remove":
				if err := guardPath(p, in.Recursive); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				var err error
				if in.Recursive {
					err = os.RemoveAll(p)
				} else {
					err = os.Remove(p)
				}
				if err != nil {
					return errFail("REMOVE_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"path": p}), nil
			case "chmod":
				m, err := parseMode(in.Mode)
				if err != nil {
					return errFail("INVALID_MODE", err.Error()), nil
				}
				if err := guardPath(p, in.Recursive); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				if err := os.Chmod(p, m); err != nil {
					return errFail("CHMOD_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"path": p, "mode": in.Mode}), nil
			case "chown":
				if err := guardPath(p, in.Recursive); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				if err := os.Chown(p, in.UID, in.GID); err != nil {
					return errFail("CHOWN_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"path": p, "uid": in.UID, "gid": in.GID}), nil
			case "symlink":
				if in.Target == "" {
					return errFail("MISSING_TARGET", "target 必填"), nil
				}
				if err := guardPath(p, false); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				if err := os.Symlink(in.Target, p); err != nil {
					return errFail("SYMLINK_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"path": p, "target": in.Target}), nil
			case "hardlink":
				if in.Target == "" {
					return errFail("MISSING_TARGET", "target 必填"), nil
				}
				// 硬链接会为受保护文件增加一个可写入口
				if err := guardPath(p, false); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				if err := guardPath(resolvePath(deps, in.Target), false); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				if err := os.Link(in.Target, p); err != nil {
					return errFail("HARDLINK_FAILED", err.Error()), nil
				}
				return ok(map[string]any{"path": p, "target": in.Target}), nil
			case "selinux":
				if in.Context == "" {
					return errFail("MISSING_CONTEXT", "context 必填"), nil
				}
				if err := guardPath(p, in.Recursive); err != nil {
					return errFail("PROTECTED_PATH", err.Error()), nil
				}
				out, stderr, code, err := runSh(ctx, deps, "novaai_fs_manage",
					fmt.Sprintf("chcon %s %s", shQuote(in.Context), shQuote(p)), 15*time.Second)
				if err != nil || code != 0 {
					return errFail("CHCON_FAILED", stderr), nil
				}
				return ok(map[string]any{"output": out}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// ---- fs_search ----
	reg("novaai_fs_search", "文件搜索", "按名称或内容搜索，查找大文件与重复文件",
		objSchema(map[string]any{
			"action":     enumProp("操作", "name", "content", "large", "duplicates"),
			"root":       strProp("搜索根目录"),
			"pattern":    strProp("名称模式"),
			"query":      strProp("内容关键词"),
			"extension":  strProp("扩展名"),
			"maxResults": intProp("最大结果数"),
			"minSize":    intProp("最小字节"),
			"maxSize":    intProp("最大字节"),
		}, "action"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action     string `json:"action"`
				Root       string `json:"root"`
				Pattern    string `json:"pattern"`
				Query      string `json:"query"`
				Extension  string `json:"extension"`
				MaxResults int    `json:"maxResults"`
				MinSize    int64  `json:"minSize"`
				MaxSize    int64  `json:"maxSize"`
			}
			_ = json.Unmarshal(args, &in)
			if in.MaxResults <= 0 {
				in.MaxResults = 200
			}
			root := resolvePath(deps, in.Root)
			if root == "" {
				root = deps.Config.WorkspaceRoot()
			}

			switch in.Action {
			case "name":
				cmd := fmt.Sprintf("find %s -name %s 2>/dev/null | head -n %d", shQuote(root), shQuote(in.Pattern), in.MaxResults)
				out, _, _, _ := runSh(ctx, deps, "novaai_fs_search", cmd, 30*time.Second)
				return ok(map[string]any{"results": splitLines(out)}), nil
			case "content":
				cmd := fmt.Sprintf("grep -r -l %s %s 2>/dev/null | head -n %d", shQuote(in.Query), shQuote(root), in.MaxResults)
				out, _, _, _ := runSh(ctx, deps, "novaai_fs_search", cmd, 30*time.Second)
				return ok(map[string]any{"results": splitLines(out)}), nil
			case "large":
				cmd := fmt.Sprintf("find %s -type f -size +%dk 2>/dev/null | head -n %d",
					shQuote(root), in.MinSize/1024, in.MaxResults)
				out, _, _, _ := runSh(ctx, deps, "novaai_fs_search", cmd, 30*time.Second)
				return ok(map[string]any{"results": splitLines(out)}), nil
			case "duplicates":
				cmd := fmt.Sprintf("find %s -type f -exec md5sum {} + 2>/dev/null | sort | uniq -w32 -D | head -n %d",
					shQuote(root), in.MaxResults)
				out, _, _, _ := runSh(ctx, deps, "novaai_fs_search", cmd, 60*time.Second)
				return ok(map[string]any{"raw": out}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})

	// ---- fs_hash ----
	reg("novaai_fs_hash", "文件哈希", "计算或校验文件哈希",
		objSchema(map[string]any{
			"action":    enumProp("操作", "calculate", "verify"),
			"algorithm": enumProp("算法", "md5", "sha1", "sha256"),
			"path":      strProp("文件路径"),
			"expected":  strProp("期望的哈希值"),
		}, "action", "path"),
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Action    string `json:"action"`
				Algorithm string `json:"algorithm"`
				Path      string `json:"path"`
				Expected  string `json:"expected"`
			}
			_ = json.Unmarshal(args, &in)
			if in.Algorithm == "" {
				in.Algorithm = "sha256"
			}
			p := resolvePath(deps, in.Path)

			h, err := computeHash(p, in.Algorithm)
			if err != nil {
				return errFail("HASH_FAILED", err.Error()), nil
			}

			switch in.Action {
			case "verify":
				match := strings.EqualFold(h, in.Expected)
				return ok(map[string]any{"match": match, "actual": h, "expected": in.Expected}), nil
			case "calculate":
				return ok(map[string]any{"algorithm": in.Algorithm, "hash": h}), nil
			}
			return errFail("UNKNOWN_ACTION", in.Action), nil
		})
}

// ---- 内部工具 ----

func copyPath(src, dst string, recursive, overwrite bool) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if !recursive {
			return fmt.Errorf("src 是目录，需要 recursive: true")
		}
		return filepath.Walk(src, func(p string, i os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			target := filepath.Join(dst, rel)
			if i.IsDir() {
				return os.MkdirAll(target, i.Mode())
			}
			return copyFile(p, target, overwrite)
		})
	}
	return copyFile(src, dst, overwrite)
}

func copyFile(src, dst string, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(dst); err == nil {
			return fmt.Errorf("目标已存在: %s", dst)
		}
	}
	_ = os.MkdirAll(filepath.Dir(dst), 0755)
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func parseMode(s string) (os.FileMode, error) {
	s = strings.TrimPrefix(s, "0o")
	s = strings.TrimPrefix(s, "0")
	if s == "" {
		return 0, fmt.Errorf("空 mode")
	}
	var m uint32
	_, err := fmt.Sscanf(s, "%o", &m)
	return os.FileMode(m), err
}

func computeHash(path, algo string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	switch algo {
	case "md5":
		h := md5.New()
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	case "sha1":
		h := sha1.New()
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	case "sha256":
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	return "", fmt.Errorf("不支持的算法: %s", algo)
}
