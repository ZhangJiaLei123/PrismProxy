# CLI 实时推送增强设计（SSE）

> 状态：**已实施并通过 e2e 验收**（2026-09-05）。M8 已交付请求-响应式控制 API（[AI-CLI使用说明.md](./AI-CLI使用说明.md)），本文档定义的实时推送增强（`GET /api/v1/events` SSE 双频道 + `ctlapi.Hub` fan-out + `cli flows watch`）已全部落地，用户文档见 [AI-CLI使用说明.md](./AI-CLI使用说明.md) §3.7；实施期固化的 6 条新陷阱（接口切片序列化、事件流连接隔离、背压只 close(reset)、StartProxy/StopProxy 拆分、query token 变参中间件、ServeMux 子路径须显式注册）见 [项目记忆.md](./项目记忆.md)「M8.5 SSE 实时推送」节。
>
> **2026-09-05 审计修订**（对照现行代码核实）：① 纠正 §5 快照-增量缝隙分析（原"先快照后订阅"会丢流，改为"先订阅后快照"）；② §4.2 补齐 headless 下 `flush()` 早退导致 fan-out 永不执行的实现陷阱；③ §4.1 解决"Hub 不依赖 main 包"与"flows 按 filter 过滤"的包依赖矛盾；④ §3.2 status 频道补 seed 帧与发布点（手动启停/热重启原稿缺失）；⑤ §6.2 CLI 补重连重读 token、指数退避、SSE 无超时 client；⑥ §3/§4 补订阅者上限与 Hub.Close；⑦ §8/§9/§10 同步（取舍补 8-11 条、任务拆解与验收标准对齐修订点）。

## 1. 背景与目标

M8 的控制 API（127.0.0.1:9595）是纯请求-响应模式：AI/脚本获取新流量只能轮询 `cli flows list`。轮询的缺点：延迟受轮询间隔限制、空轮询浪费、多一份去重逻辑（需记最大流 ID 做增量）。

**目标**：增加一条服务端主动推送通道，让消费者"连上即有快照、之后实时收增量"，并补齐代理状态变化（启停/系统代理切换）的推送。

**非目标**：
- 不推送消息体（body）。body 体积大且多数消费者不需要；需要时按 ID 走既有 `flows get <id> --body` 拉取。
- 不做双向通道（无客户端→服务端的实时指令需求；写操作继续用 POST/PUT）。
- 不替代 GUI 的 Wails 事件链路（GUI 继续走 `flow:upsert`/`flow:evict`，推送通道与之并行、同源）。

## 2. 传输选型：SSE（Server-Sent Events）

| 方案 | 结论 |
|---|---|
| **SSE** | **选用**。单向推送、HTTP 原生（`Content-Type: text/event-stream`）、curl/PowerShell/任意 HTTP 客户端均可直接读文本流，AI agent 无需 WebSocket 库；浏览器 `EventSource` 自带重连（`Last-Event-ID`），Go/curl 客户端重连逻辑自实现；Go 侧 `http.Flusher` 即可实现 |
| WebSocket | 双向能力本场景用不到；需帧协议与握手升级，消费门槛高于 SSE |
| 长轮询 | 实现简单但延迟与空转问题仍在，无本质改进 |

SSE 帧格式（W3C 标准）：

```
event: <事件名>\n
data: <一行 JSON>\n
\n
```

- 心跳：每 15 秒发送一行 SSE 注释 `: ping\n\n` 保活（防代理/客户端超时断连）。
- 每帧携带独立的 `id: <单调递增序号>` 行（SSE 标准帧字段，不在 `data` 内）。本期不实现历史回放，`id` 仅用于日志/调试，并为未来的 `Last-Event-ID` 断点续传预留（见 §8 取舍）。

## 3. HTTP 协议设计

### 3.1 端点

```
GET /api/v1/events?channels=flows,status&filter=<子串>
```

| 参数 | 说明 |
|---|---|
| `channels` | 订阅频道，逗号分隔；缺省 `flows`。本期实现 `flows`、`status` 两个频道 |
| `filter` | 仅对 `flows` 频道生效，语义同 `flows list --filter`（host/URL/path 子串、不区分大小写）；服务端过滤，省带宽 |

**认证**：沿用 Bearer 机制——`Authorization: Bearer <token>` 或 `X-Prism-Token`。
另支持 query 兜底 `?token=<token>`：浏览器 `EventSource` API 无法自定义请求头，query 是唯一传 token 方式。风险（token 出现在 URL/日志/进程命令行）可接受：仅回环、token 每次启动随机、实例退出即失效；文档中注明优先用 header。**query token 仅 `/events` 端点接受**——其余端点仍只认 header，避免 token 随 URL 扩散到更多访问面。

**并发订阅者上限 32**：超出返回 HTTP 503 `{"error":"订阅者已满"}`。每个订阅者 = 一条 handler goroutine + 128 帧缓冲（upsert 帧可达数十 KB），不设上限会被本机异常进程刷连接耗尽内存（理由见 §8 第 8 条）。

### 3.2 事件帧

连接建立后立即发 `open`，随后按频道推送：

```
event: open
data: {"server":"prismproxy-ctlapi","version":1,"instanceId":"1756990000000","channels":["flows","status"],"time":1757000000000}
```

- `instanceId`：实例启动时间戳（`startCtlAPI` 成功时取 `time.Now().UnixMilli()`），实例重启即变。直连 HTTP 的消费者可据此检测"换了实例"→ 丢弃本地增量状态、重新拉快照（CLI 重连即重打 snapshot，天然覆盖此场景）。

**flows 频道**（复用 App 50ms 合帧结果，与 GUI 同节奏、同批次）：

```
event: flows
data: {"type":"upsert","flows":[ FlowMeta, ... ]}

event: flows
data: {"type":"evict","ids":["1757000000000-12", ...]}
```

- `upsert`：新增或状态更新的流摘要（FlowMeta，PascalCase 键名，同 `flows list`）；同一 50ms 窗口内同 ID 合并为最新快照。
- `evict`：环形淘汰 / 超字节预算淘汰 / `flows clear` 的流 ID 列表。**evict 不受 filter 影响**——淘汰通知必须送达，否则客户端会残留已失效 ID。
- 流状态机语义不变：`pending/streaming/done/error`，update 即最新快照（幂等，客户端按 ID upsert 即可）。

**status 频道**（代理与系统代理状态变化时推送，payload 同 `GET /status` 的内容）：

```
event: status
data: {"proxy":{"running":true,"addr":"127.0.0.1:9090","mode":"MITM","flowCount":128,"startError":""},
       "systemProxy":{"state":"on","server":"127.0.0.1:9090","override":"..."},
       "ui":true,"headless":false}
```

**seed 帧**：连接建立且订阅含 status 时**立即推送一帧当前状态**——新订阅者无需等下一次变化即可建立基线（与 flows 的 snapshot 语义对齐）。

发布点：① startup 自动启动/自动接管完成后（对应既有 `proxy:ready`）；② StartProxy 失败（对应既有 `proxy:start-error`，payload 中 `startError` 非空）；③ `SetSystemProxy` on/off 成功后（含 `cli sysproxy on/off`，二者同链路）；④ **手动启停代理**（GUI 顶栏开关的 `StartProxy` 成功 / `StopProxy` 完成——原稿缺失，恰是最常见操作）；⑤ **settings 热应用导致代理重启**（`listenAddr`/上游变更）后。无变化时不推（非周期心跳式全量）。

已知局限：外部程序改系统代理（`occupied` 状态漂移）无可靠系统级通知，不推送；需要精确对账的消费者自行周期 `GET /status` 兜底。

**reset 事件**（见 §5 背压）：

```
event: reset
data: {"reason":"backpressure"}
```

### 3.3 错误

- 未认证：HTTP 401（与其他端点一致，连接建立前拒绝）。
- 未知频道名：HTTP 400 `{"error":"未知频道: xxx"}`（白名单校验，防拼写错误静默无数据）。
- 订阅者达上限：HTTP 503 `{"error":"订阅者已满"}`（见 §3.1）。

## 4. 服务端架构

### 4.1 ctlapi.Hub（新增，ctlapi 包内）

Hub 管理 SSE 订阅者生命周期与 fan-out，**不依赖 main 包类型**（payload 以 `any` 传入，由 encoding/json 序列化）：

```go
// Frame 一帧待写出的 SSE 事件（event 名 + 已序列化的 data 字节 + 单调序号）
type Frame struct {
    ID    int64
    Event string
    Data  []byte
}

// Filterable 由 main.FlowMeta 实现，供 Hub 按订阅者 filter 过滤 flows upsert。
// 注意：ctlapi 不能 import main（main 已 import ctlapi，反向即循环依赖），
// 因此 filter 所需字段经此小接口暴露，Hub 无需认识具体类型。
type Filterable interface {
    FilterFields() (host, url, path string)
}

// FlowsUpsert / FlowsEvict flows 频道帧结构（接线层构造，Hub 负责过滤与序列化）
type FlowsUpsert struct {
    Type  string       `json:"type"`  // "upsert"
    Flows []Filterable `json:"flows"` // main 侧把 []FlowMeta 包一层传入
}
type FlowsEvict struct {
    Type string   `json:"type"` // "evict"
    IDs  []string `json:"ids"`
}

// Hub 向所有 SSE 订阅者 fan-out 事件（ctlapi 包内，server 持有）
type Hub struct {
    mu   sync.Mutex
    subs map[*subscriber]struct{}
}

// Subscribe 注册订阅者；返回的 unsubscribe 由 handler 在连接关闭时调用。
// channels 为白名单过滤后的频道集合；filter 为 flows 子串过滤（内部 ToLower 一次）。
func (h *Hub) Subscribe(channels []string, filter string) (unsub func(), sink <-chan Frame)

// Publish 由接线层在事件发生时调用（channel="flows"|"status"）。
// flows 传 FlowsUpsert/FlowsEvict，status 传任意可 JSON 序列化快照。
// 非阻塞：订阅者缓冲满则标记该订阅者溢出，下一帧改发 reset（§5）。
func (h *Hub) Publish(channel string, payload any)

// Close 关闭全部订阅者 channel，SSE handler 随之写完剩余帧退出。
// Server.Close 时调用，让客户端立即收到 EOF 而非等进程退出/OS 回收 socket。
func (h *Hub) Close()
```

- 每个订阅者一个 buffered channel（**容量 128 帧**，合帧后频率 ≤20 帧/秒，约 6 秒背压容差）。
- handler（`GET /events`）：鉴权 → 校验频道 → `Hub.Subscribe` → 设置 SSE 响应头（`Content-Type: text/event-stream`、`Cache-Control: no-cache`）→ 订阅含 status 先发 seed 帧 → 循环 `for frame := range sink` 写帧并 `http.Flusher.Flush()`；连接断开（ctx.Done）时 unsubscribe。
- `Publish` 对每订阅者做：flows 频道按其 filter 过滤 upsert（evict 帧不过滤）；channel 不在其订阅集合则跳过；channel 满则置该订阅者 `overflowed` 标记，下一帧改发 `reset` 后由 handler 关闭连接。
- **序列化策略与 filter 的关系**（原稿"marshal 一次多订阅者共享"在 filter 场景不成立，修订）：无 filter 的订阅者共享一份预 marshal 字节；有 filter 的订阅者按 `FilterFields()` 逐条筛选后各自 marshal。筛选在 Publish 内同步完成，成本 O(订阅者数 × 帧内流数)，50ms 合帧节奏下规模可控；匹配口径与 `flows list --filter` 一致（host/URL/path 子串、不区分大小写）。

### 4.2 接线点（main 包）

- `NewServer` 内部创建 Hub，暴露 `server.Hub()` 访问器；`startCtlAPI`（ctl_server.go）成功后把 hub 存入 `App.ctlHub`（实例无控制 API 时为 nil，如端口占用降级）。
- **flows 数据源**：`App.flush()`（app.go）合帧后，在既有 Wails `EventsEmit` 之外增加一次 fan-out——GUI/headless 均执行：

  ```go
  if a.ctlHub != nil {
      if len(ups) > 0 {
          fs := make([]ctlapi.Filterable, len(ups)) // FlowMeta 实现 FilterFields()
          for i, m := range ups { fs[i] = m }
          a.ctlHub.Publish("flows", ctlapi.FlowsUpsert{Type: "upsert", Flows: fs})
      }
      if len(evs) > 0 {
          a.ctlHub.Publish("flows", ctlapi.FlowsEvict{Type: "evict", IDs: evs})
      }
  }
  ```

  复用同一份合帧结果（`[]FlowMeta` 与 `[]string`），**不新增 store 订阅、不新增合帧逻辑**——推送通道只是 flush 的第二个出口。

  > **实现陷阱（已对照现行代码核实）**：`flush()` 现有 `if a.ctx == nil { return }` 早退，而 headless 不走 Wails startup、`a.ctx` 恒为 nil。hub fan-out 必须放在该早退**之前**（或把早退改为只包住两个 `runtime.EventsEmit`），否则 headless 模式下推送通道完全无数据——且 GUI 测试发现不了。
- **status 数据源**：在 §3.2 列出的 5 个发布点调 `a.ctlHub.Publish("status", a.ctlStatusSnapshot())`；状态快照复用 `ctlService.Status()` 的构造逻辑，抽为共享函数 `App.ctlStatusSnapshot()`（`ctlService.Status()` 改为调它，避免双份构造漂移）。
- headless 与 GUI 链路完全一致（flush 在两种模式下都运行；按上述陷阱修正后，headless 只是跳过 Wails emit，hub fan-out 不受影响）。

### 4.3 线程模型

- Publish 从 App 的事件 goroutine（flush timer / 请求处理 goroutine）调用，必须**非阻塞、锁内不做 JSON 编码与 filter 筛选以外的轻活**：marshal/筛选成本见 §4.1，写入各订阅者 channel 用 select-default 判满。
- SSE handler 独立 goroutine 读 channel 写 HTTP 响应；慢消费者只影响自己（被 reset），不影响其他订阅者与代理热路径。
- **HTTP server 超时约束**：控制 server 现状只设 `ReadHeaderTimeout`（[server.go](../internal/ctlapi/server.go)），SSE 长连接可用；**后续维护不得新增 `WriteTimeout`/`IdleTimeout`**——会直接掐断 SSE 连接。
- **关闭路径**：`Server.Close` 现行 `Shutdown(2s)` 不会主动断开活跃连接（Go `Shutdown` 只等其变空闲，SSE 永不空闲，会白等 2s）。需先 `Hub.Close()` 关闭订阅者 channel → handler 写余帧后退出 → 客户端立即收到 EOF；实例强退场景则靠进程退出/OS 回收 socket，CLI 两种路径都能感知。

## 5. 背压与一致性

- **慢消费者**：订阅者 channel 满（128 帧）→ 标记溢出 → 向其发送一帧 `event: reset` → 关闭连接。
- **reset 语义**：客户端收到 reset（或连接断开重连后），必须丢弃本地增量状态，重新拉一次全量快照对账（CLI 自动完成，见 §6.2；直连 HTTP 的消费者自行处理）。
- **不丢命的通知**：evict 帧不经过滤、不与 upsert 合并进同一帧，保证淘汰可达。
- **不做服务端历史回放**：断连期间的事件不补发（无事件存储）。重连后靠"全量快照 + 后续增量"收敛；upsert 幂等（按 ID 覆盖）、evict 对未知 ID 忽略。
- **快照与增量的衔接顺序必须是"先订阅、后快照"**（2026-09-05 审计纠正：原稿"先快照后订阅"的缝隙分析有误——缝隙中创建且之后不再更新的流，其 upsert 在订阅建立前已发完、快照里又没有，客户端会**永久漏掉该流**直到它被淘汰。快速完成的短生命周期流最容易踩中）。正确时序把"缝隙"变成"重叠"，重叠区靠幂等天然安全：

  ```
  客户端：连上 /events（开始缓冲帧） ──重叠──> GET /flows（快照） ──> 输出快照、回放缓冲帧、持续转发
  重叠中"新增且仍在"的流：快照里有，upsert 帧也会到达 → 按 ID 幂等覆盖 ✓
  重叠中"新增又被淘汰"的流：淘汰先于快照 → 快照没有、evict 到达按未知 ID 忽略 ✓；
      快照先于淘汰 → 快照里有、evict 到达正常删除 ✓
  订阅前已存在的流：全在快照里 ✓
  ```

  缓冲帧在 CLI 内部排队即可（快照拉取是秒级窗口，量有界）；直连 HTTP 的消费者同理：先开事件流、再拉快照。

## 6. CLI 子命令设计

### 6.1 `cli flows watch`

```
PrismProxy.exe cli flows watch [--filter 子串] [--status] [--format ndjson|sse]
```

- `--filter`：同 list，传给 SSE 端点。
- `--status`：追加订阅 status 频道（默认只订 flows）。
- `--format ndjson`（默认）：每行一个 JSON 对象（NDJSON），最适合 AI/脚本逐行读取解析；`sse`：原样透传 SSE 文本（便于 `curl -N` 式调试）。
- 持续运行直到 Ctrl+C / 实例退出；退出码：0=用户中断（Ctrl+C），1=连接失败/实例消失。

### 6.2 输出时序（ndjson）

启动时**先输出全量快照，再输出增量**，一条命令完成"全量+实时"：

```jsonl
{"event":"snapshot","flows":[ FlowMeta, ... ]}
{"event":"flows","type":"upsert","flows":[ ... ]}
{"event":"flows","type":"evict","ids":[ ... ]}
{"event":"status","proxy":{...},"systemProxy":{...}}
{"event":"reset","reason":"backpressure"}
{"event":"snapshot","flows":[ ... ]}
```

- CLI 内部流程（**先订阅后快照**，正确性论证见 §5）：连 GET /events 并开始缓冲帧 → GET /flows（**带同一 `--filter`**，保证基线与增量口径一致，不混入永不更新的流）打印 `snapshot` 行 → 回放缓冲帧 → 之后逐帧转发。
- 收到 `reset` 帧或连接中断自动重连时：CLI 自动重跑上述完整流程、打印新 `snapshot` 再继续增量（**`snapshot` 语义 = 丢弃本地表整体重建**，消费者无感，无需自己实现对账）。
- **重连必须重新读 `config/ctl-endpoint.json`**：实例重启会生成新 token（且可能换端口），沿用旧 token 只会无限 401；仅当用户显式传了 `--addr`/`--token` 时才沿用命令行值。
- **重连带指数退避**（0.5s→1s→2s→4s→8s），连续 5 次失败 stderr 报错、退出码 1；实例重启间隙的短暂失败只是退避等待，不算失败退出——与 §10"实例退出报错退出 1；实例重启后重连成功"口径统一。
- **watch 的 HTTP client 不能带总超时**：现有 cli client `http.Client{Timeout: 15s}`（cli.go）会把 SSE 长连接在 15 秒掐断。watch 须另造无 `Timeout` 的 client（仅连接/Dial 级超时），读超时由服务端 15s 心跳兜底。
- `--format sse`：快照同样由 CLI 先合成一帧标准 SSE（`event: snapshot` + `data: {...}`），之后原样透传服务端帧——整段输出都是合法 SSE 文本流（原稿"不包装 snapshot 又合成 snapshot"表述自相矛盾，已统一）。
- `--pretty` 对 watch 无意义（逐行流式输出），忽略不报错。
- Ctrl+C 退出码 0 用 `signal.NotifyContext` 实现；实例消失（EOF/连接重置且重连 5 次全败）退出码 1。

### 6.3 与现有命令的关系

`flows watch` 与 `flows list` 互补：一次性脚本用 list，持续监控用 watch。watch 不新增服务端读路径（快照就是 list、增量是 SSE）。

## 7. 消费者使用模式

**curl / 任意 HTTP 客户端（直连 API）**：

```bash
curl -N -H "Authorization: Bearer <token>" \
  "http://127.0.0.1:9595/api/v1/events?channels=flows,status"
```

**AI agent 典型用法**：

```bat
:: 持续监控含 baidu 的流量，逐行读 JSON
PrismProxy.exe cli flows watch --filter baidu
```

- 首行 snapshot 建立基线，之后每行是增量；upsert 按 ID 覆盖本地表、evict 删行即可维护一份与实例一致的实时列表。
- status 频道用于感知"代理被停了/系统代理被切了"，无需轮询 status。
- 需要 body 时再对感兴趣的 ID 调 `flows get <id> --body resp`（推送不带 body，见 §1）。

**浏览器内 EventSource**（如未来做 Web 面板）：用 `?token=` 传参。

## 8. 边界与取舍

1. **推送内容仅限 FlowMeta 摘要 + 状态**：body/headers 不推（体积大、按需拉取）；规则/设置变更不推（低频，且变更方通常就是消费者自己）。
2. **合帧节奏与 GUI 相同（50ms）**：不额外提频；流式中的流每 50ms 最多一帧更新。
3. **无服务端事件缓冲/回放**：断连靠 snapshot 对账（§5），不引入事件存储（M7 SQLite 持久化后若需要"历史回放"再另议，不与本设计耦合）。
4. **SSE 连接不计入实例任何状态**：订阅者是纯观察者，断开/超时不影响代理与 GUI；实例退出时所有 SSE 连接随 HTTP server 关闭，CLI 报错退出。
5. **多订阅者相互隔离**：各自缓冲、各自 reset；一个慢消费者不拖累其他连接。
6. **query token 仅限 EventSource 场景、且仅 `/events` 端点接受**：其余端点只认 header，避免 token 随 URL 扩散到更多访问面；文档提示优先 header；token 本机随机、退出失效。
7. **频道白名单**：`flows`、`status` 之外的频道名 400 拒绝，为未来扩展（如 M7 持久化相关频道）保留命名空间但不静默忽略。
8. **并发订阅者上限 32**：每个订阅者 = 一条 handler goroutine + 128 帧缓冲（upsert 帧可达数十 KB），不设上限会被本机异常进程刷连接耗尽内存；32 对本机工具场景（GUI + 少数 CLI/agent）已富余，超出 503 而非排队，让调用方立刻感知。
9. **CORS 默认不开**：回环 + token 已够本机消费；未来做 Web 面板需要浏览器跨源时再开，且只允许回环 Origin（`http://localhost`/`127.0.0.1`），不与本期耦合。
10. **外部程序改系统代理不推 status**：无可靠系统级通知（§3.2 已知局限），不为它引入轮询；需要精确对账的消费者自行周期 `GET /status`。
11. **`instanceId` 用启动时间戳**而非引入 UUID 依赖：只需"重启即变"的判别能力，时间戳足够且零成本。

## 9. 实施任务拆解（预估）

| # | 任务 | 落点 |
|---|---|---|
| 1 | ctlapi.Hub：Frame/Filterable/FlowsUpsert/FlowsEvict 类型、Subscribe/Publish/Close、订阅者缓冲、溢出 reset、filter 过滤（无 filter 共享序列化、有 filter 各自筛选） | internal/ctlapi/hub.go（新） |
| 2 | SSE handler：`GET /events`、鉴权（含 query token 仅本端点接受）/频道白名单校验/订阅上限 503、SSE 帧写入+Flusher、15s 心跳、status seed 帧、ctx 断开清理 | internal/ctlapi/server.go |
| 3 | 接线：server.Hub() 暴露；App.ctlHub 字段；startCtlAPI 存入（同时记 instanceId）；Server.Close 先 Hub.Close 再 Shutdown | ctl_server.go / app.go / internal/ctlapi/server.go |
| 4 | flush() 双发 flows 事件（**fan-out 置于 `a.ctx == nil` 早退之前**，headless 可用）；status 5 个发布点（ready/start-error/sysproxy/手动启停/settings 热重启）+ 抽 `App.ctlStatusSnapshot()` 共享函数 | app.go / ctl_bridge.go / settings 热应用处 |
| 5 | CLI `flows watch`：**先订阅后快照**（快照带同一 --filter）、ndjson/sse 双格式、**无总超时的 HTTP client**、reset/断线自动重连（**重读 ctl-endpoint.json**、指数退避 0.5s→8s、5 次上限）、Ctrl+C 退出码（signal.NotifyContext） | internal/ctlapi/cli.go |
| 6 | 单测：Hub fan-out/filter/背压 reset/Close；SSE 端到端（httptest，含 seed 帧与 503）；watch 先订阅后快照衔接（重叠区幂等）；**headless 下 flush fan-out 有数据（防 ctx==nil 早退回归）** | internal/ctlapi/*_test.go / app_test.go |
| 7 | 文档：AI-CLI使用说明.md 增 watch 章节（§3.7 已补）；项目记忆记录实施陷阱 | doc/ ✅ |

> **实施结果（2026-09-05）**：任务 1-7 全部完成，go build/vet/test 全绿（10 个包 ok，含 Hub/handler/watch/headless fan-out 等 16 个新增测试函数），e2e 冒烟逐项验证通过（snapshot 首帧、upsert/evict/status seed、--filter 服务端过滤、15s `: ping` 心跳、query token 直连、退避重连退出码 1、无实例→上线自动重连恢复、Ctrl+Break 退出码 0）。额外发现并修复一个 M8 遗留路由 bug：`cli flows clear`（POST /flows/clear）因 Go ServeMux 精确模式不匹配子路径一直 404，已显式注册路由并加回归测试。方案.md §4.12 维持 M8 请求-响应式控制 API 描述不变，本实时推送增强作为其补充，独立成篇不回并。

## 10. 验收标准

- headless 与 GUI 两种模式下，`cli flows watch` 均能：启动即收 snapshot；造流后 1 秒内收到 upsert（**headless 此项为 flush 早退回归的关键防线**，GUI 通过不算数）；`flows clear` 收到 evict；Ctrl+C 退出码 0。
- `--filter` 生效（不匹配 host/URL/path 的 upsert 不推；evict 照常推）；快照同样带 filter（基线与增量口径一致）。
- `--status`：**连接建立即收 seed 帧**（不必等状态变化）；`sysproxy on/off`、手动停启代理、settings 热重启后收到 status 帧。
- 慢消费者（暂停读取 >6 秒）收到 reset 帧；CLI 自动重新 snapshot 并继续，不崩溃、不丢后续事件。
- 实例退出时 watch 命令 stderr 报错、退出码 1；**实例重启（token 已变更）后 watch 重读 `ctl-endpoint.json`、重连成功并自动重打 snapshot**。
- 第 33 个并发订阅收到 HTTP 503；未知频道名收到 HTTP 400；query token 在 `/events` 可用、在其他端点无效（401）。
- Go 单测全绿；`curl -N -H "Authorization: Bearer <token>" .../events` 直连可读。
