// Package pathguard 是「受保护路径」判定的唯一 owner。
//
// 为什么需要它：本模块以 root 身份直接操作文件的通用载体不止一个
// （fs_write / fs_manage / archive / transfer_upload / shell / script /
// backup restore）。如果每个工具各自维护一份黑名单，就会产生多个会互相
// 漂移的 owner。因此判定收敛到本包，调用方只负责在变更真正落地之前调用
// Check / CheckRecursiveDelete，被拒时返回 INVALID_PARAM。
//
// 角色划分（重要，不是兼容垫片）：
//   - 通用载体（fs_write、fs_manage、archive、shell…）必须过本包；
//   - 专用 owner 不过本包，因为它就是该位置的合法管理者：
//     novaai_config 拥有 config.json，novaai_root_module / novaai_hook_*
//     拥有 /data/adb/modules。
//
// 判据只有一条：这个操作是否可能让设备无法正常启动，且无法从系统内恢复。
package pathguard

import (
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
)

// Decision 一次判定结果。Rule 只用于审计与错误信息，不参与控制流。
type Decision struct {
	Allowed bool   `json:"allowed"`
	Path    string `json:"path"` // 归一化后的路径
	Rule    string `json:"rule,omitempty"`
	Reason  string `json:"reason,omitempty"`

	// Confirmable 为 true 表示：默认拒绝，但调用方可以在拿到**带外**
	// 用户确认（confirmDangerous）后重试。见 ConfirmCheck。
	//
	// 它存在的理由：/sdcard/Android/data 既不是"永远不能动"（用户卸载
	// 残留、清理某个应用的缓存都是正当需求），也不是"随便动"。
	// 二分法（允许/拒绝）表达不了这一档，于是加了第三个状态。
	Confirmable bool `json:"confirmable,omitempty"`
}

func (d Decision) Err() error {
	if d.Allowed {
		return nil
	}
	return fmt.Errorf("路径受保护（%s）：%s", d.Rule, d.Path)
}

// ErrConfirmable 是 Confirmable 场景下的错误，向调用方说明"加确认可过"。
func (d Decision) ErrConfirmable() error {
	return fmt.Errorf("路径受保护（%s）：%s —— 变更需用户确认（confirmDangerous）",
		d.Rule, d.Path)
}

// rule 一条硬拒绝规则。
//
// allow 不是例外机制，而是角色限定：stateDir 整体必须受保护
// （config.json / token / audit 不能被通用工具改写），但其中若干子树
// 是 agent 的合法工作区。
type rule struct {
	prefix string
	allow  []string
	why    string
}

// fixedDeny 与 stateDir 无关的硬拒绝前缀。
var fixedDeny = []rule{
	// 分区：写入即可能无法启动，且系统内无法恢复
	{prefix: "/system", why: "系统分区"},
	{prefix: "/vendor", why: "厂商分区"},
	{prefix: "/product", why: "产品分区"},
	{prefix: "/system_ext", why: "系统扩展分区"},
	{prefix: "/odm", why: "ODM 分区"},
	{prefix: "/oem", why: "OEM 分区"},
	{prefix: "/boot", why: "引导分区"},
	{prefix: "/recovery", why: "恢复分区"},
	{prefix: "/persist", why: "persist 分区"},
	{prefix: "/metadata", why: "metadata 分区"},
	{prefix: "/efs", why: "EFS 分区"},
	{prefix: "/firmware", why: "firmware 分区"},
	{prefix: "/modem", why: "modem 分区"},
	{prefix: "/radio", why: "radio 分区"},
	{prefix: "/dev/block", why: "块设备节点"},
	{prefix: "/proc/sys", why: "内核参数"},
	{prefix: "/sys", why: "sysfs"},
	// root 框架与模块：破坏后可能丢失 root 或卡开机
	{prefix: "/data/adb/modules", why: "模块目录"},
	{prefix: "/data/adb/modules_update", why: "模块更新目录"},
	{prefix: "/data/adb/magisk", why: "Magisk 自身"},
	{prefix: "/data/adb/ksu", why: "KernelSU 自身"},
	{prefix: "/data/adb/ksud", why: "KernelSU 守护进程"},
	{prefix: "/data/adb/ap", why: "APatch 自身"},
	{prefix: "/data/adb/lspd", why: "LSPosed 自身"},
	{prefix: "/data/adb/post-fs-data.d", why: "开机脚本目录"},
	{prefix: "/data/adb/service.d", why: "开机脚本目录"},
}

// androidDataRoots 是「只读根」：读放行，任何变更默认拒绝。
//
// 与 fixedDeny 的区别是判据不同。fixedDeny 问的是"改了会不会开不了机"；
// 这里问的是"改了会不会让别的应用丢数据、且用户无从察觉"。
// Android/data 与 Android/obb 是**其他应用**的私有外部存储：
// 以 root 写进去不会让设备变砖，但会静默破坏那个应用的状态，通常不可恢复。
//
// 必须覆盖全部别名，不能被符号链接绕过：在 /sdcard/Android 下，
// data 与 obb 是指向 /storage/emulated/0/Android/{data,obb} 的符号链接，
// 而 /mnt/sdcard 又是 /storage/emulated/0 的别名。只写一条会被另一条绕过。
//
// 与 fixedDeny 一样按路径分量比较，所以 /sdcard/Android/database
// 不会被 /sdcard/Android/data 误伤。
var androidDataRoots = []rule{
	{prefix: "/sdcard/Android/data", why: "应用私有外部存储"},
	{prefix: "/sdcard/Android/obb", why: "应用私有 OBB 资源"},
	{prefix: "/storage/emulated/0/Android/data", why: "应用私有外部存储"},
	{prefix: "/storage/emulated/0/Android/obb", why: "应用私有 OBB 资源"},
	{prefix: "/data/media/0/Android/data", why: "应用私有外部存储"},
	{prefix: "/data/media/0/Android/obb", why: "应用私有 OBB 资源"},
	{prefix: "/mnt/sdcard/Android/data", why: "应用私有外部存储"},
	{prefix: "/mnt/sdcard/Android/obb", why: "应用私有 OBB 资源"},
}

// readDeny 是**唯一**在读取路径上仍然拒绝的前缀。
//
// 为什么不是直接用 fixedDeny：那两个集合回答的是不同的问题。
// fixedDeny 问"改了会不会开不了机"，所以它包含 /data/adb/modules ——
// 但**读** module.prop 正是 agent 排查模块问题的正常手段，用写入规则去
// 限制读取会砍掉真实能力（novaai_fs_read 目前完全没有守卫，收紧必须是
// 有理由的收紧，而不是顺手扩大一个已有集合的适用范围）。
//
// 这里只留下"读它没有任何正当用途、且会把设备内容拖进工具结果"的位置。
var readDeny = []rule{
	{prefix: "/dev/block", why: "块设备节点"},
	{prefix: "/proc/sys", why: "内核参数"},
}

// stateDirAllow stateDir 内仍然允许通用工具写入的子树。
var stateDirAllow = []string{
	"workspace", "tmp", "downloads", "uploads", "artifacts",
	"backups", "schedules", "skills",
}

// criticalRoots 允许被写入、但不允许被整体递归删除/改权的根。
// 判据：删掉它们等于格机（丢用户数据或丢 root）。
var criticalRoots = []string{
	"/",
	"/data",
	"/data/adb",
	"/data/media",
	"/data/media/0",
	"/sdcard",
	"/storage",
	"/storage/emulated",
	"/storage/emulated/0",
	"/mnt",
	"/data/local/tmp",
}

// shallowRoots 是 criticalRoots 的子集，用于通配符场景。
//
// 语义差别：`rm -rf /data` 与 `rm -rf /data/*` 都等于格机，但
// `rm -rf /data/local/tmp` 危险而 `rm -rf /data/local/tmp/*` 完全正常。
// 所以通配符只按"被清空的目录是否是这个根"判定，用更浅的一组。
var shallowRoots = []string{
	"/",
	"/data",
	"/data/adb",
	"/data/media",
	"/data/media/0",
	"/sdcard",
	"/storage",
	"/storage/emulated",
	"/storage/emulated/0",
	"/mnt",
}

var (
	mu       sync.RWMutex
	stateDir = "/data/adb/novaai-mcp"
)

// SetStateDir 由 main 在加载配置后调用一次，使判定跟随实际 stateDir。
func SetStateDir(dir string) {
	if dir == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	stateDir = path.Clean(dir)
}

// StateDir 返回当前 stateDir。
func StateDir() string {
	mu.RLock()
	defer mu.RUnlock()
	return stateDir
}

// stateDirRule 由当前 stateDir 构造，避免把路径写死在两处。
func stateDirRule() rule {
	base := StateDir()
	allow := make([]string, 0, len(stateDirAllow))
	for _, sub := range stateDirAllow {
		allow = append(allow, path.Join(base, sub))
	}
	return rule{
		prefix: base,
		allow:  allow,
		why:    "MCP 自身状态目录",
	}
}

// Check 判定一次写入/删除/改权是否允许。
//
// recursive 为 true 时额外应用 criticalRoots：删掉 /data、/sdcard 这类根
// 等同于格机，即使它们本身不在 fixedDeny 里。
//
// 判定顺序有意如此：先 fixedDeny（不可协商），再 androidDataRoots
// （默认拒绝但可确认），最后 stateDir。把可确认的一档放在硬拒绝之后，
// 是为了让"某个路径同时命中两类规则"时取更严的那个。
func Check(p string, recursive bool) Decision {
	return check(p, recursive, false)
}

// ConfirmCheck 与 Check 相同，但把 androidDataRoots 视为已确认放行。
//
// 调用方只有在**已经拿到用户确认**之后才能用这个入口 —— 确认本身
// 必须发生在模型够不着的地方，见各工具对 confirmDangerous 的处理。
func ConfirmCheck(p string, recursive bool) Decision {
	return check(p, recursive, true)
}

func check(p string, recursive, confirmed bool) Decision {
	n := Normalize(p)
	if n == "" {
		return Decision{Allowed: true, Path: n}
	}

	if recursive {
		for _, root := range criticalRoots {
			if n == root {
				return Decision{
					Allowed: false, Path: n,
					Rule:   "critical-root",
					Reason: "不允许整体递归删除/改权：" + root,
				}
			}
		}
	}

	if d := matchRules(n, fixedDeny); !d.Allowed {
		return d
	}

	if !confirmed {
		if d := matchRules(n, androidDataRoots); !d.Allowed {
			d.Confirmable = true
			return d
		}
	}

	return matchRules(n, []rule{stateDirRule()})
}

// CheckRecursiveDelete 是 Check(p, true) 的语义化别名。
func CheckRecursiveDelete(p string) Decision { return Check(p, true) }

// CheckWrite 是 Check(p, false) 的语义化别名。
func CheckWrite(p string) Decision { return Check(p, false) }

// CheckRead 判定一次**只读**访问。
//
// 读取刻意比写入宽松得多，理由有二：
//   - /sdcard/Android/{data,obb} 的语义就是"只能读不能动"，读是承诺的能力；
//   - stateDir 里的 config.json 一直可被 novaai_fs_read 读，且
//     novaai_config export 就是要把配置读出来。
//
// 因此这里**不**复用 fixedDeny，只用 readDeny（块设备与内核参数）。
// 也不应用 criticalRoots —— 那限制的是递归删除，与读无关。
func CheckRead(p string) Decision {
	n := Normalize(p)
	if n == "" {
		return Decision{Allowed: true, Path: n}
	}
	return matchRules(n, readDeny)
}

// CheckSystemPath 只判定 fixedDeny 与 criticalRoots，不含 stateDir 与
// androidDataRoots。
//
// 用于归档恢复：备份本来就是模块自己的数据，恢复回 stateDir 是合法操作，
// 但归档里混入 /system 或 /data/adb/modules 的条目必须拒绝。
func CheckSystemPath(p string, recursive bool) Decision {
	n := Normalize(p)
	if n == "" {
		return Decision{Allowed: true, Path: n}
	}
	if recursive {
		for _, root := range criticalRoots {
			if n == root {
				return Decision{
					Allowed: false, Path: n,
					Rule:   "critical-root",
					Reason: "不允许整体递归删除/改权：" + root,
				}
			}
		}
	}
	return matchRules(n, fixedDeny)
}

func matchRules(n string, rules []rule) Decision {
	for _, r := range rules {
		if !under(n, r.prefix) {
			continue
		}
		exempt := false
		for _, a := range r.allow {
			if under(n, a) {
				exempt = true
				break
			}
		}
		if exempt {
			continue
		}
		return Decision{
			Allowed: false, Path: n,
			Rule:   r.prefix,
			Reason: "受保护位置：" + r.why,
		}
	}
	return Decision{Allowed: true, Path: n}
}

// under 判断 p 是否等于 prefix 或位于 prefix 之下。
//
// 必须按路径分量比较，不能直接 HasPrefix：否则 /system_ext 会被
// /system 的规则误伤，/systemfoo 也会被误判为受保护。
func under(p, prefix string) bool {
	if prefix == "/" {
		return strings.HasPrefix(p, "/")
	}
	if p == prefix {
		return true
	}
	return strings.HasPrefix(p, prefix+"/")
}

// Normalize 归一化路径：Clean + 解析已存在部分的符号链接。
//
// 解析符号链接是必须的：否则 `ln -s /system /data/local/tmp/x` 之后
// 写 /data/local/tmp/x/build.prop 就能绕过前缀检查。
// 不存在的尾部原样保留（目标是新建文件时就是这种情况）。
//
// 这里用 path 而不是 filepath：受保护路径全是 POSIX 绝对路径，
// 用 filepath 会在 Windows 上把 "/" 变成 "\"，判定与测试都会失真。
func Normalize(p string) string {
	if p == "" {
		return ""
	}
	p = path.Clean(p)
	if !strings.HasPrefix(p, "/") {
		// 相对路径在调用方（resolvePath / joinCwd）已转绝对
		return p
	}
	return resolveSymlinks(p, 0)
}

// maxSymlinkDepth 防止符号链接自引用导致无限递归。
const maxSymlinkDepth = 32

func resolveSymlinks(p string, depth int) string {
	if depth > maxSymlinkDepth {
		return p
	}
	rest := strings.TrimPrefix(p, "/")
	if rest == "" {
		return "/"
	}
	parts := strings.Split(rest, "/")

	cur := "/"
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		next := path.Join(cur, part)
		fi, err := os.Lstat(next)
		if err != nil {
			// 从这里开始不存在，剩下的分量原样拼回去
			return path.Join(append([]string{cur}, parts[i:]...)...)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		target, err := os.Readlink(next)
		if err != nil {
			return path.Join(append([]string{cur}, parts[i:]...)...)
		}
		if !strings.HasPrefix(target, "/") {
			target = path.Join(cur, target)
		}
		joined := path.Join(append([]string{target}, parts[i+1:]...)...)
		return resolveSymlinks(path.Clean(joined), depth+1)
	}
	return cur
}

// CheckGlob 判定一次通配符操作（形如 /data/* 、/sdcard/DCIM/*）。
//
// 通配符展开后命中的是「某个目录里的条目」，所以真正要判定的是那个目录：
//   - 目录本身是 shallowRoots 之一（/data、/sdcard…）→ 拒绝；
//   - 目录落在硬拒绝前缀内（/system、/data/adb/modules…）→ 拒绝；
//   - 其他（/data/local/tmp、/sdcard/DCIM…）→ 放行。
func CheckGlob(pattern string) Decision {
	n := Normalize(pattern)
	if n == "" {
		return Decision{Allowed: true, Path: n}
	}
	dir := globDir(n)
	if dir == "" {
		return Decision{Allowed: true, Path: n}
	}
	for _, root := range shallowRoots {
		if dir == root {
			return Decision{
				Allowed: false, Path: n,
				Rule:   "glob-root",
				Reason: "通配符会清空 " + root,
			}
		}
	}
	return matchRules(dir, fixedDeny)
}

// globDir 返回通配符展开时「被枚举的目录」。
//
//	/data/*           -> /data            （枚举 /data 的条目）
//	/data/local/tmp/* -> /data/local/tmp
//	/data/foo*        -> /data            （部分匹配，条目也在 /data 里）
//	/*                -> /
func globDir(n string) string {
	idx := strings.IndexAny(n, "*?[")
	if idx < 0 {
		return ""
	}
	lit := n[:idx]

	// 字面前缀以 / 结尾：通配符匹配的是该目录下的完整条目
	if strings.HasSuffix(lit, "/") {
		d := strings.TrimRight(lit, "/")
		if d == "" {
			return "/"
		}
		return d
	}

	// 部分匹配：条目位于 lit 的父目录
	if i := strings.LastIndex(lit, "/"); i > 0 {
		return lit[:i]
	}
	return "/"
}

// ProtectedPaths 返回当前生效的硬拒绝前缀，供 status/文档展示。
func ProtectedPaths() []string {
	out := make([]string, 0, len(fixedDeny)+1)
	for _, r := range fixedDeny {
		out = append(out, r.prefix)
	}
	out = append(out, StateDir())
	return out
}

// ReadOnlyPaths 返回"可读、变更需确认"的前缀。
func ReadOnlyPaths() []string {
	out := make([]string, 0, len(androidDataRoots))
	for _, r := range androidDataRoots {
		out = append(out, r.prefix)
	}
	return out
}

// CriticalRoots 返回不允许整体递归删除的根。
func CriticalRoots() []string {
	out := make([]string, len(criticalRoots))
	copy(out, criticalRoots)
	return out
}

// Explain 生成一条用于审计的可读说明。
func Explain(d Decision) string {
	if d.Allowed {
		return "allowed:" + d.Path
	}
	return fmt.Sprintf("denied:%s rule=%s", d.Path, d.Rule)
}
