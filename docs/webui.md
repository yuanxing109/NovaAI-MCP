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

页面会被 WebView 以 `file://` 打开，静态 `import` 可能被 CORS 拦掉。
所以全部文件都是经典脚本（挂全局），对 `kernelsu` 的导入是**运行时动态**
尝试。见 `lib/kernelsu.js` 顶部注释。

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
// lib/config.js 的核心
var script =
  'cat > ' + TMP + " << 'NOVA_CFG_EOF'\n" +
  JSON.stringify(cfg) + '\n' +
  'NOVA_CFG_EOF\n' +
  'chmod 0600 ' + TMP + ' && mv ' + TMP + ' ' + CFG + '\n';
```

三个刻意的选择：

1. **原子**：先写 `.tmp` 再 `mv`。半截的 `config.json` 会让 daemon 下次启动
   直接失败 —— 一次"加个上游"变成"服务起不来"。
2. **单引号 heredoc**：`<<'EOF'` 不做任何变量展开与转义处理，内容里的
   `' " $ \ `` ` 都原样落盘。用 `echo '...'` 拼接则会被 shell 解析。
   这点实测验证过：`$HOME` 与 `` `id` `` 原样落盘、未被求值。
3. **拒绝含真实换行的序列化结果**：`JSON.stringify` 不产出真实换行，
   真出现了说明有东西在骗我们，此时 heredoc 的终止条件不再可靠，
   直接拒绝而不是硬写。

改完配置后**必须**调 `novaai_config reload_upstreams` 才会生效 ——
页面把"写盘 + 重载"绑在 `persist()` 里，三个管理动作都走它。

---

## 探测 KernelSU 桥

`lib/kernelsu.js` 依次尝试三条路，任一成功即缓存：

1. `window.ksu`（较新的注入方式）
2. `window.kernelsu`
3. 动态 `import('kernelsu')`

全部失败时 `NovaKsu.exec()` 抛出一条人话错误，`app.js` 在顶部显示横幅。
这是"必须能自证失败"：在普通浏览器里打开本页面，用户应当看到
"请在 KernelSU 管理器里打开"，而不是一串 `TypeError`。

用到的 API：`exec`（读写配置、看进程、`tail` 日志）、`toast`。
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
