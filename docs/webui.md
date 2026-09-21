# KernelSU WebUI

模块根目录的 `webroot/` 提供 WebUI，由 **KernelSU 管理器**加载
（点模块的「打开」进入）。必须存在 `webroot/index.html`，否则入口不出现。

> **只在 KernelSU 上可用。** Magisk 与 APatch 没有等价的模块页面机制。
> 在 KernelSU 之外打开本页面时，页面会显示一条"未检测到 KernelSU 桥"的
> 横幅，仍可浏览，但读写配置 / 探测上游这些操作不可用。

```
webroot/
  index.html         页面骨架（三个标签页）
  style.css          跟随系统深/浅色
  app.js             页面逻辑
  lib/
    kernelsu.js      KernelSU API 封装（多入口探测 + 失败自证）
    mcp-client.js    JSON-RPC over HTTP 客户端
    config.js        config.json 的原子读写
```

---

## 设计约束

三条约束决定了这个页面的形状：

1. **它只是本服务的一个普通 MCP 客户端。** 所有工具调用走
   `http://127.0.0.1:5322/mcp`，与本机任何脚本走的是同一条路。没有私有接口，
   也没有只属于 WebUI 的权限模型 —— 页面能做的事，同网段的 MCP 客户端都能做。
2. **只有"改 config.json"这一件事需要 root**，且只有 `lib/config.js` 做。
3. **上游状态一律来自服务端**（`novaai_upstream_status` /
   `probe_upstreams`）。页面**不自己判活** —— 否则就有了第二份状态判定实现，
   而 `stopped` 与 `error` 的区分、stdio 进程存活这些细节必然漂移。

### 不使用 ES module

页面的载入源随管理器不同（Re:KernelSU 是
`https://appassets.androidplatform.net`，有的版本是 `file://`），静态
`import` 在其中一些环境下会被 CORS 拦掉。所以全部文件都是经典脚本
（挂全局），对 `kernelsu` 的导入是**运行时动态**尝试。
见 `lib/kernelsu.js` 顶部注释。

---

## 页面

### 上游

- **列表**：名称、类型、状态灯、地址/命令、启动方式、已合并工具数；
  `error` 时直接显示原因。
- **状态灯**：绿 = 运行中，灰 = 未启动，黄 = 已禁用，红 = 错误。
- **操作**：
  | 按钮 | 做什么 |
  |---|---|
  | 启动 / 重启 | `novaai_config restart_upstream`（只在配了 `launch` 时出现）。它关掉旧连接、按 `launch` 拉起、再重探 |
  | 探测 | `novaai_config probe_upstreams`（带 `name`，只探这一个） |
  | 禁用 / 启用 | 改 `enabled` → 写盘 → `reload_upstreams` |
  | 删除 | 先备份 `config.json`，再改数组 → 写盘 → `reload_upstreams` |
- **添加上游**：名称、类型（http / stdio）、URL 或命令+参数、启动方式、
  `riskCeiling`、`denyTools`、`enabled` / `autoLaunch` / `exposeWhenStopped`。
  填 URL 时会用 `ss`/`netstat` 检查端口占用 —— **只是提示，不是拦截**
  （如果那个端口上是你要接的服务本身，占用是正常的）。
- **全部探测**（右上角）：`probe_upstreams` 不带 name，探全部。

### 工具目录

- 展示 `tools/list` 的合并结果，按来源分组（本地 / 各上游）。
- 上游工具的**原始工具名**单独列出来（前缀之前的那部分）。
- 搜索框过滤工具名与描述。
- **手动调用测试**：选工具、填 JSON 参数、发一次 `tools/call`，展示结果。
  走的就是公开通路，没有特权旁路。

> **这里不显示风险等级。** 风险等级是服务端**内部**的准入依据，按既定契约
> 不在 `tools/list` 里声明（见 [extensions.md](extensions.md) 第 2.2 节）。
> 页面复刻一份风险表就等于制造第二个 owner —— 那种漂移正是这个仓库反复
> 踩过的坑。真正的准入判定在服务端，页面只负责展示和转发。

### 日志

- 列出 `{stateDir}/audit/` 下最新的 20 个 JSONL 文件，`tail -n 400`。
- 「只看上游事件」过滤 `upstream_*` 事件与 `upstream:` 前缀的工具名。
- 内容按行 JSON 显示（`exec` 只负责取，不解析；解析在页面里做，
  所以坏行不会让整个日志页失败）。

---

## 配置写入怎么做的

```js
// lib/config.js 的核心（printf 单行，原子落盘）
var safeJson = json.replace(/'/g, "'\\''");
var script =
  "printf '%s' '" + safeJson + "' > " + TMP + '; ' +
  'chmod 0600 ' + TMP + ' && mv ' + TMP + ' ' + CFG;
```

三个刻意的选择：

1. **原子**：先写 `.tmp` 再 `mv`。半截的 `config.json` 会让 daemon 下次启动
   直接失败 —— 一次"加个上游"变成"服务起不来"。
2. **单引号 + printf，不用 heredoc**：单引号字符串里只有 `'` 需要转义
   （`'\''`），`$`、反引号、反斜杠都是字面量，JSON.stringify 的产物原样落盘
   （`$HOME` 与 `` `id` `` 实测未被求值）。不用 heredoc 是因为 **KSU 桥的
   shell 对多行 heredoc 会报 "unclosed"**（设备上实测）；顺带让整段脚本
   变成一行，桥是否保留换行都不再影响结果。`echo '...'` 拼接则会被 shell
   解析，从一开始就是错的。
3. **拒绝含真实换行的序列化结果**：`JSON.stringify` 不产出真实换行，
   真出现了说明有东西在骗我们，此时无法安全嵌入脚本，直接拒绝而不是硬写。

改完配置后**必须**调 `novaai_config reload_upstreams` 才会生效 ——
页面把"写盘 + 重载"绑在 `persist()` 里，三个管理动作都走它。

---

## 传输层：为什么是"桥 + curl"，而不是 fetch

页面由 KernelSU 管理器以**虚拟源**载入（本机 Re:KernelSU 是
`https://appassets.androidplatform.net`），与 `http://127.0.0.1:5322` 不同源。
从 HTTPS 源 fetch HTTP 本机地址要过三道闸，每一道都独立致命：

1. **混合内容**：HTTPS 页面请求 HTTP 资源，WebView 默认禁止；
2. **CORS 预检**：`Content-Type: application/json` 触发 OPTIONS 预检，
   daemon 不返回任何 `Access-Control-*` 头 —— 预检必失败；
3. **Origin 拒绝**：就算前两道都放行，跨源 fetch 必带 Origin 头，而
   daemon 的 hostMiddleware 对任何带 Origin 头的请求一律返回 `-32001`。

三道合起来：浏览器看到的永远是 `TypeError: Failed to fetch`，而服务
活得好好的。**这不是配置问题，是这条路径在当前设计下不可能通**
（"删 CORS、有 Origin 就拒"是刻意的设计，见 [security.md](security.md)）。

所以 `lib/mcp-client.js` 的选择顺序是：

1. **KernelSU 桥可用 → 桥 + curl**（root shell 里执行 `/system/bin/curl`，
   没有浏览器、没有 Origin、没有 CORS）；
2. **页面与接口同源 → fetch**（留给将来 daemon 自己托管 webroot 的情形）；
3. **都不是**（普通浏览器直接打开文件）→ 明确报错，不做注定失败的 fetch。

页面顶部横幅会带出 `（传输层：…；桥：…）`，三个值分别对应上面三条路。

---

## 与官方 API 的对照

官方指南：<https://kernelsu.org/zh_CN/guide/module-webui.html>；
官方 JS 库：npm 包 [`kernelsu`](https://www.npmjs.com/package/kernelsu)（当前 3.0.2）。

`lib/kernelsu.js` 是官方包的**双模式超集**：回调约定、参数形态、window
注册与清理时机都照抄官方 `index.js`（逐字核对过 npm 3.0.2 的发布物），
在此之上多出三样东西 —— Promise 风格桥的兜底、回调超时守卫（官方没有，
回调不来就永远挂起）、桥来源诊断。API 面对照：

| 官方 API | `NovaKsu` 封装 | 页面是否使用 |
|---|---|---|
| `exec(cmd, {cwd,env})` → `Promise<{errno,stdout,stderr}>` | `exec(cmd, options)` 同形 | ✓ 全部根操作 |
| `spawn(cmd, args, {cwd,env})` → ChildProcess 流 | `spawn(cmd, args, options)` 同形 | 未使用；日志页实时 follow 的钥匙 |
| `toast(msg)` | `toast(msg)` | 部分操作反馈 |
| `listPackages(type)` → `string[]`（同步） | `listPackages(type)` → `Promise<string[]>` | intent 启动表单选应用 |
| `getPackagesInfo(pkgs)` → `PackagesInfo[]` | `getPackagesInfo(pkgs)` | 未使用；配合 `ksu://icon/{包名}` 可做带图标的应用选择器 |
| `moduleInfo()` | `moduleInfo()` | 未使用 |
| `fullScreen(bool)` / `enableEdgeToEdge(bool)` / `exit()` | **刻意未封装** | 纯外观/生命周期，需要时三行代码的事 |

**为什么不直接用官方 npm 包**：本页面要在 `file://` 与虚拟源两种环境下以
经典脚本运行（零依赖、无打包器），而官方包是 ES module —— 官方指南自己
也建议配 parcel 之类的打包器。若将来引入打包器，`lib/kernelsu.js` 可整体
替换为官方包，调用面已对齐。

**照抄官方约定同时修掉官方的一个坑**：官方 `exec` 的回调若不来就永远挂起
（没有任何超时）；本封装加了 120 秒守卫，超时报"命令没有被执行"并带上桥类型。

## 探测 KernelSU 桥

### 桥的真实调用约定（在本机验证，不是猜的）

```js
window.ksu.exec(command, optionsJson, callbackName);
// callback(errno, stdout, stderr)
```

两条证据：管理器 APK（`com.resukisu.resukisu`）里的
`com.resukisu.zako.IKsuInterface` 经 `addJavascriptInterface` 注入，
暴露 `exec / spawn / toast / fullScreen / moduleInfo / listPackages`；
同一台设备上已验证可用的模块（proxypin-cert-installer）就是这么调的。

`lib/kernelsu.js` 依次探测 `window.ksu` → `window.kernelsu` →
动态 `import('kernelsu')`，记录来源（出错时报"桥是什么"）。
调用时**先按回调约定**；如果桥其实是 Promise 风格（返回 thenable）就改用
返回值；如果同步抛错才退回 Promise 调用。回调 30 秒没来会明确报
"桥没有回调 —— 命令没有被执行"，而不是挂死。

> **历史教训**：初版按 Promise 风格调 `k.exec(command)`。对三参数的 Java
> 方法少传参数时，WebView 的 JS 桥**不报错**，把缺的参数当 null 传下去 ——
> 命令根本没执行、回调也没来，`Promise.resolve(undefined)` 被规范化成
> `{errno:0, stdout:''}`，上层把"空输出"误读成"**config.json 文件不存在**"。
> 设备上 `config.json` 明明存在、daemon 明明活着，排查方向被完全带偏。
> 参数个数不匹配在这里是**静默失败**。

全部探测失败时 `NovaKsu.exec()` 抛出一条人话错误，`app.js` 在顶部显示横幅。

用到的 API：`exec`（读写配置、curl 转发、看进程、`tail` 日志）、`toast`。
`spawn` / `listPackages` / `moduleInfo` 已在封装里留好路，当前页面未使用。

---

## 已知限制

- **不做后台轮询。** 页面上的状态是快照，需要手动「全部探测」或刷新。
  这与服务端的设计一致（见 [upstream.md](upstream.md) 的探测时机）。
- **日志页只读不follow。** 没有实时流；大文件只取最后 400 行。
- **端口输入框改的是 MCP 地址**（持久化在 `localStorage`），不改
  `config.json` 的 `listen`。改了 `listen` 之后要在这里同步填新端口。
- **添加表单的校验是双份的**：页面做一遍是为了立刻给出人话提示，
  真正把关的是 `config.Validate`（服务端）。两边的规则不完全重合
  （例如"name 不能含 `__`"两边都有，而 `url` 的协议前缀只在服务端查）。
- **`exec` 走的是 KernelSU 的 shell**，页面里的路径是 `exec` 的
  `$PATH` 决定的，与 daemon 自己的环境无关。
