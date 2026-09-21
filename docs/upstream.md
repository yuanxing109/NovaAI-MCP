# 上游 MCP 聚合

NovaAI-MCP 不只是"一个以 root 运行的工具箱"，它同时是一个 **MCP 聚合网关**：

- **对外**：一个 MCP 地址、一份合并后的工具列表；
- **对内**：自身 30 个本地工具 + 用户添加的任意上游 MCP 服务。

上游用 [KernelSU WebUI](webui.md) 增删，也可以直接改 `config.json`
的 `upstreams` 数组（WebUI 做的就是这件事）。

> **本页第一个 JSON 代码块是契约。** 它由
> `src/internal/config/upstream_test.go` 逐键比对 —— 与 `UpstreamConfig`
> 的 json tag 不一致就会失败。改字段时两处一起改。
>
> （这段话本身刻意不写出围栏标记：写了它，取示例的测试就会把这段散文
> 当成第一个代码块。现在提取逻辑会跳过不可解析的块，但没必要去踩。）

---

## 完整示例（可直接复制进 config.json）

```json
{
  "upstreams": [
    {
      "name": "other_app_mcp",
      "type": "http",
      "url": "http://127.0.0.1:9999/mcp",
      "command": "",
      "args": [],
      "enabled": true,
      "riskCeiling": 3,
      "maxConcurrent": 0,
      "denyTools": [],
      "exposeWhenStopped": false,
      "autoLaunch": false,
      "launch": {
        "type": "intent",
        "package": "com.example.app",
        "action": "com.example.app.START_MCP",
        "activity": "",
        "command": "",
        "args": []
      }
    },
    {
      "name": "local_stdio_mcp",
      "type": "stdio",
      "url": "",
      "command": "/data/local/tmp/some_mcp_binary",
      "args": ["--port", "8888"],
      "enabled": true,
      "riskCeiling": 2,
      "maxConcurrent": 0,
      "denyTools": ["dangerous_tool"],
      "exposeWhenStopped": false,
      "autoLaunch": true,
      "launch": {
        "type": "command",
        "package": "",
        "action": "",
        "activity": "",
        "command": "/data/local/tmp/some_mcp_binary",
        "args": ["--port", "8888"]
      }
    }
  ]
}
```

---

## 字段

| 字段 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `name` | ✅ | — | 唯一标识，同时是工具名前缀。只能含字母数字与 `_ - .`，**不能含 `__`** |
| `type` | ✅ | — | `http` 或 `stdio` |
| `url` | http 必填 | — | 必须 `http://` 或 `https://` 开头 |
| `command` + `args` | stdio 必填 | — | 直接 spawn，不经 shell |
| `enabled` | — | `false` | 是否参与聚合。**缺省即不启用**（fail-closed：漏写不会意外暴露上游） |
| `riskCeiling` | — | `3` | 该上游的风险上限。**`0` 或缺省都表示继承默认 3** |
| `denyTools` | — | `[]` | 拒绝列表，可写**裸名**（`echo`）或**带前缀名**（`a__echo`） |
| `exposeWhenStopped` | — | `false` | 未运行时是否仍把工具列给客户端 |
| `autoLaunch` | — | `false` | 调用未运行的上游时是否先按 `launch` 拉起 |
| `maxConcurrent` | — | `4` | 该上游的**并发调用上限**（探测与转发合计）。`0`/缺省 = 4；`-1` = 不限。它是"防把上游打挂"的旋钮 —— 上游（尤其 stdio 单进程）往往顺序处理请求，网关无限制并发转发只会让上游的队列失控 |
| `launch` | — | `manual` | 启动方式，见下 |

> **`riskCeiling` 的粒度是有限的**，别高估它。上游工具的风险由
> `profile.ResolveRisk` 按工具名与 `action` 推断（`internal/profile/risk.go`
> 里那张表），**未知工具名一律算 1**。所以：
>
> - `riskCeiling >= 1`：放行所有未知工具（默认情形，等于没有这条限制）；
> - `riskCeiling = 0`：被当成"继承默认 3"，**不能**表达"只放行 risk 0"。
>
> 真正有效的上游级控制是 `denyTools`。要更细的粒度只有两条路：
> 把工具名改成能命中本地风险表的形状（不现实），或者不接受这个上游。

### launch

| `launch.type` | 需要的字段 | 行为 |
|---|---|---|
| `intent` | `package` + （`action` 或 `activity`） | `am start -a <action> -p <package>`，或 `am start -n <package>/<activity>`。多数上游是"装了 App 才有 MCP 端口"的形态，这是唯一能拉起它们的方式 |
| `command` | `command` +（可选 `args`） | 直接 spawn。**不经 shell**，参数逐个传递，所以配置里的内容不会变成注入点 |
| `manual`（缺省） | — | 不自动启动，只提示 |

拉起动作只在两处发生：`autoLaunch: true` 时调用一个未运行的上游；
以及 WebUI 的「启动 / 重启」按钮（它调 `novaai_config restart_upstream`）。
**被拉起的进程不由本服务托管** —— launch 的语义是"拉起来"，不是"由我托管"，
所以它可能被 LMK 杀掉，那时状态会变回 `stopped`。

---

## 工具命名与路由

上游工具以 **`{name}__{tool}`** 出现在 `tools/list` 里（双下划线）。

| 规则 | 说明 |
|---|---|
| 拆分 | 按**已注册的上游名**做最长前缀匹配。`name` 禁止含 `__`，所以分隔符无歧义 |
| 冲突 | 前缀隔离，天然不冲突。上游也能有一个叫 `novaai_status` 的工具，它以 `fake__novaai_status` 出现，**不会**遮蔽本地工具 |
| 合并时机 | 启动探测后、`probe_upstreams` / `reload_upstreams` 后 —— 都是即时重算，不是缓存 |
| 上游工具不注册进本地注册表 | 它们的数量随配置变化，塞进注册表会让"工具总数"这个被断言锁住的数字跟着配置漂移 |

`SplitName` 只在已注册的上游名里匹配。`zzz__x`（没有名为 `zzz` 的上游）
既不是本地工具也不是上游工具，调用会得到 `-32015 工具不存在`。

---

## 状态模型

| 状态 | 含义 | 怎么判出来的 |
|---|---|---|
| `running` | 可达，工具已合并 | HTTP：能 `initialize` + `tools/list`；stdio：进程存活且握手成功 |
| `stopped` | 配置在，探测不通 | 连接拒绝 / 超时 / 进程不在 / 管道关闭 |
| `error` | 探测本身出错 | HTTP 非 2xx、响应不是 JSON-RPC、`tools/list` 结构异常、stdio 起不来 |
| `disabled` | 用户禁用 | 不探测、不暴露 |

> 归类的默认方向是 **`error` 而不是 `stopped`**。因为 `stopped` 会触发
> `autoLaunch` 去拉起进程，把"协议不对"误判成"没在跑"会导致每次调用都
> 白拉一次。宁可显示成 `error` 让用户去看原因。

**状态不落盘。** 每次启动重新探测，WebUI 打开时调 `probe_upstreams` 拉最新。
**不做后台轮询** —— 刷新时机只有三个：

1. NovaAI-MCP 启动时，对所有 `enabled: true` 的上游**并发**探测一次。
   这一轮总时限是 **15 秒**（`upstream.StartupProbeTimeout`），比
   `shellTimeoutSeconds` 短：探测只是"连一下、拉个工具列表"，配了一个
   不可达的上游就让 daemon 多等一分钟启动代价太大。超时不算失败 ——
   没探完的上游停在 `stopped`，之后 `probe_upstreams` 或一次调用会重探。
2. `novaai_config action=probe_upstreams`（可带 `name` 只探一个）；
3. 调用某个**非 running** 的上游工具前，实时探测一次（这一次用
   `shellTimeoutSeconds`，因为要给上游真正干活留余量）。

---

## 连接与会话生命周期

核心原则一句话：**上游连接长驻，session 复用，工具列表缓存。** 只有上游
显式表现为无状态（initialize 时不返回 `Mcp-Session-Id`）时才退化为
每次直接 POST —— 这是少数，不需要配置开关：它由上游的行为自然决定。

### HTTP 上游

```
探测成功（启动 / 热重载 / 调用前实时探测）
  ↓ initialize → 拿上游 Mcp-Session-Id → 存内存（幂等，只做一次）
  ↓ tools/list → 缓存工具列表
  ↓
后续每次转发：复用同一个 http.Client（连接池，Keep-Alive）
  + 带上游 session ID
  ↓
上游返回 404（session 过期/失效）
  → 重置会话 → 重新 initialize → 重试一次原请求
  → 重试仍失败才把错误交给调用方（随后的重探会标 stopped）
```

- 会话 id 在**任何**响应头里出现都会被采纳（有的上游在 tools/list 才发）。
- 上游从不返回会话头 → 内存里的 session 保持为空 → 每次请求不带
  `Mcp-Session-Id`，按无状态上游对待。
- 每个 HTTP 上游一个独立的 `http.Client`：Keep-Alive 连接池、不走代理、
  不跟随重定向（跟随会让上游侧的 Host 校验形同虚设）、响应限读 8 MiB。

### stdio 上游

```
探测成功（或 autoLaunch 触发）
  ↓ spawn 子进程一次，保持 stdin/stdout 管道
  ↓ initialize → tools/list → 缓存
  ↓
后续每次转发：向已有 stdin 写 JSON-RPC（按 id 多路复用收响应）
  ↓
进程退出（EOF / 崩溃 / 调用超时被杀）→ 状态标 stopped
  autoLaunch 则下次调用拉起，否则等手动启动
```

- **进程生命周期 = 上游生命周期**，不是请求生命周期。每次调用都 spawn
  一次既慢又会丢上游的内存状态。
- 写 stdin 在锁内串行（不会串包）；响应按自增 id 分发。
- 调用超时会**杀整个进程组**：一个卡死的工具不能留下半死的上游 ——
  让下一次调用重新 spawn，比留个僵进程更可预测。
- 上游写进 stderr 的最后 20 行会被保留，进程退出时作为失败原因展示。

### 并发上限（`maxConcurrent`）

每个上游一个信号量，**探测与转发合计**计数（探测本身就是
initialize + tools/list，也是对上游的真实负载）：

- 到达上限时，后续调用排队等待；等到调用超时（`shellTimeoutSeconds`）
  就报"并发已满"；
- `reload_upstreams` 改了上限会即时生效；
- 这个限制的目的不是防滥用（那是全局 limits 的事），是**防把上游打挂**。

---

## 未运行的上游

**工具列表**：

- `disabled` 的上游**永不**暴露 —— 用户显式禁用它，就不该再看到它的工具；
- `running` 的正常暴露；
- `stopped` / `error` 默认不暴露，除非配了 `exposeWhenStopped`。
  这时暴露的是**上一次成功探测**拿到的列表（`stopped` 的服务不可能回答
  `tools/list`）；从未成功过就无工具可暴露 —— 不能凭空编造 schema。

**调用路由**：

```
tools/call "other_app_mcp__some_tool"
  ↓
① 非 running → 实时探测一次
  ├ running  → 转发
  ├ stopped  → autoLaunch && 有 launch 配置？
  │     ├ 是 → 拉起 → 等 2 秒 → 重探 → 转发或失败
  │     └ 否 → isError: "upstream X 未运行（stopped），请先手动启动"
  ├ error    → isError: "upstream X 异常（error）: <原因>"
  └ disabled → isError: "upstream X 已禁用"
```

等待固定 2 秒而不是"轮询到就绪"：`intent` 拉起的是 App，冷启动时间不可
预测，轮询上限只能拍一个数字，反而更难解释。等不到就给调用方一个明确的
失败，而不是无限等。

**所有上游侧的失败都以 `result.isError = true` 返回**，不用 JSON-RPC error ——
按 MCP 规范，那属于"工具结果错误"。唯一的例外是上游策略拒绝
（`denyTools` / `riskCeiling`），它同样走 `isError`。

---

## 管理接口

### `novaai_upstream_status`（风险 0）

```json
{
  "success": true,
  "tools": 14,
  "upstreams": [
    {"name": "other_app_mcp", "type": "http", "target": "http://127.0.0.1:9999/mcp",
     "status": "running", "enabled": true, "tools": 12, "launch": "intent", "autoLaunch": false},
    {"name": "local_stdio_mcp", "type": "stdio", "target": "/data/local/tmp/x --port 8888",
     "status": "stopped", "enabled": true, "tools": 0, "launch": "command", "autoLaunch": true},
    {"name": "broken_mcp", "type": "http", "target": "http://127.0.0.1:1/mcp",
     "status": "error", "enabled": true, "tools": 0, "launch": "manual",
     "reason": "上游协议异常: HTTP 500: nope"}
  ]
}
```

AI 可以据此提示用户启动未运行的上游。

### `novaai_config` 的上游 action

| action | 风险 | 作用 |
|---|---|---|
| `probe_upstreams` | 0 | 重新探测。带 `name` 只探那一个 |
| `restart_upstream` | 3 | 关掉旧连接；**若配了 launch 就按 launch 拉起**，然后重探。WebUI 的「启动」按钮就是它 |
| `reload_upstreams` | 3 | 从磁盘重读 `config.json` 并重建注册表。WebUI 保存配置后调它 |

`reload_upstreams` 走 `config.Load`（含 `Validate`）。WebUI 写坏配置时它会
**拒绝并保留当前可用**的注册表，而不是把一个非法配置装进去。

> `reload_upstreams` 只重载**上游**。`listen` 这类字段仍然需要重启
> supervisor 才生效 —— 配置里没有热重载机制，这是刻意的。

---

## 安全边界

1. **上游 MCP 的安全性由上游自己负责。** 本服务只做转发，不覆盖上游的鉴权，
   也无法验证上游声称的工具描述是真的。
2. **上游工具继承全局 `default` 档位**（放行全部），上游级控制只有
   `denyTools` 与粒度有限的 `riskCeiling`。
3. **stdio 上游是以 root 身份 spawn 的任意可执行文件。** 与 `novaai_shell`
   同级的能力 —— 能配一个 stdio 上游的人，本来就能拿到 shell。
4. **stdio 上游进程可能被 LMK 杀掉。** 需要 `autoLaunch` 或手动重启。
5. **拉起的进程不由本服务托管**，不会随 `novaai_config` 或 daemon 退出而回收。
6. **上游结果同样受 `resultPreviewBytes` 截断**，且截断时丢弃
   `structuredContent` —— 与本地工具一致。
