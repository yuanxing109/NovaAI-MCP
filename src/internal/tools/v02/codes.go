package v02

// 工具级 `code` 的词表 —— **唯一声明点**。
//
// # 为什么需要这张表
//
// `code` 是给**程序**匹配的标识符（`PROTECTED_PATH` 这种），客户端会拿它
// 做分支。它不适合改成中文：一旦改了就是破约，而且下游（探针脚本的正则、
// 审计里的人眼、别的语言写的客户端）全都要跟着动。
//
// 但它对**人**（以及读审计日志的 AI）不友好。所以这里给每个 code 配一个
// 稳定的中文名，由 `schema.go` 的三个构造函数自动附在结果里：
//
//	{"success": false, "code": "PROTECTED_PATH", "codeName": "路径受保护", "message": "..."}
//
// 三条约束：
//   - `code` 保留英文，**不改**（程序匹配用它）；
//   - `codeName` 是稳定的短标签，不是句子 —— 详细原因仍然在 `message` 里
//     （`message` 会带上具体路径/参数，`codeName` 只回答"这是哪一类错"）；
//   - 数字错误码（`-32015` 这类）**不动** —— 它们来自 JSON-RPC 2.0 与 MCP
//     生态的约定，换了客户端就不认了。
//
// # 为什么放在本包
//
// 工具级 code 的生产者只有 `v02/schema.go` 的 `ok` / `okMsg` / `errFail`
// 与 `execResult`（`tools` 包下的 `health_status` / `upstream_status`
// 返回体里没有 `code` 字段，`tools/errors.go` 那个第二实现是零调用的死代码，
// 已删除）。所以这张表放在唯一消费者的旁边，不另开叶子包。
//
// 如果将来 `tools` 包也要产生 code，把这张表提到叶子包再共用 ——
// 不要复制第二份。`codes_test.go` 会机械盯住"有 code 没名字"与
// "有名字没 code"两个方向。

// CodeOK 是成功的 code 值。它**会出现**在成功结果里（不是省略）。
const CodeOK = "OK"

// codeNames 是 code → 稳定中文名。
//
// 分组顺序只为了可读：通用 → 路径 → 文件 → 执行 → 应用 → 归档 → 下载 →
// Root → 系统 → 上游。Go 的 map 无序，所以顺序不影响行为。
var codeNames = map[string]string{
	// 通用
	CodeOK:           "成功",
	"UNKNOWN_ACTION": "未知操作",
	"NOT_FOUND":      "资源不存在",
	"EXISTS":         "已存在",
	"UNSUPPORTED":    "框架不支持",

	// 参数
	"MISSING_PARAM":   "缺少参数",
	"MISSING_PATH":    "缺少路径",
	"MISSING_PID":     "缺少进程号",
	"MISSING_COMMAND": "缺少命令",
	"MISSING_SCRIPT":  "缺少脚本",
	"MISSING_CONFIG":  "缺少配置",
	"MISSING_DEST":    "缺少目标路径",
	"MISSING_SOURCE":  "缺少来源路径",
	"MISSING_TARGET":  "缺少链接目标",
	"MISSING_CONTEXT": "缺少 SELinux 标签",
	"INVALID_PARAM":   "参数非法",
	"INVALID_JSON":    "JSON 非法",
	"INVALID_CONFIG":  "配置非法",
	"INVALID_MODE":    "权限模式非法",
	"INVALID_UID":     "UID 非法",
	"INVALID_BASE64":  "Base64 非法",

	// 路径保护
	"PROTECTED_PATH": "路径受保护",

	// 文件系统
	"STAT_FAILED": "读取元数据失败",
	// LIST_FAILED 同时被 fs_info（列目录）与 app_list（列应用）使用，
	// 所以名字必须停在"列举"这一层 —— 具体列举什么写在 message 里。
	"LIST_FAILED":     "列举失败",
	"OPEN_FAILED":     "打开失败",
	"READ_FAILED":     "读取失败",
	"WRITE_FAILED":    "写入失败",
	"TRUNCATE_FAILED": "截断失败",
	"TOUCH_FAILED":    "更新时间戳失败",
	"SEEK_FAILED":     "定位失败",
	"COPY_FAILED":     "复制失败",
	"MOVE_FAILED":     "移动失败",
	"REMOVE_FAILED":   "删除失败",
	"MKDIR_FAILED":    "建目录失败",
	"CHMOD_FAILED":    "改权限失败",
	"CHOWN_FAILED":    "改属主失败",
	"CHCON_FAILED":    "改 SELinux 标签失败",
	"SYMLINK_FAILED":  "建符号链接失败",
	"HARDLINK_FAILED": "建硬链接失败",
	"HASH_FAILED":     "算哈希失败",

	// 命令执行
	"EXEC_FAILED":   "执行失败",
	"SYNTAX_ERROR":  "脚本语法错误",
	"KILL_FAILED":   "发信号失败",
	"RENICE_FAILED": "调优先级失败",

	// 应用管理
	"INSTALL_FAILED": "安装失败",
	"INFO_FAILED":    "取信息失败",

	// 归档与传输
	"NO_7Z":          "找不到 7z",
	"ARCHIVE_FAILED": "归档失败",
	"CREATE_FAILED":  "创建上传失败",
	"EXPORT_FAILED":  "导出失败",

	// 下载
	"DOWNLOAD_FAILED": "下载失败",
	"SHA256_MISMATCH": "SHA-256 不匹配",
	"SIZE_MISMATCH":   "大小不匹配",

	// Root 与模块
	"MOUNT_FAILED":  "挂载失败",
	"UMOUNT_FAILED": "卸载失败",

	// 屏幕
	"SCREENCAP_FAILED": "截屏失败",
	"INPUT_FAILED":     "输入失败",

	// 上游 MCP 聚合
	"UPSTREAM_UNAVAILABLE":    "上游聚合未启用",
	"UPSTREAM_PROBE_FAILED":   "探测上游失败",
	"UPSTREAM_RESTART_FAILED": "重启上游失败",
}

// CodeName 返回 code 的中文名。未登记时返回空串 —— 不编造，
// 让 codes_test.go 去暴露"有 code 没名字"。
func CodeName(code string) string { return codeNames[code] }

// withCodeName 给结果对象附加 `codeName`。
//
// 只在 code 已登记时附加：宁可少一个字段，也不要写一个猜出来的名字。
func withCodeName(m map[string]any) map[string]any {
	if code, ok := m["code"].(string); ok {
		if name := codeNames[code]; name != "" {
			m["codeName"] = name
		}
	}
	return m
}
