# PrismProxy AI CLI 使用说明

PrismProxy 内置一个**本地控制 CLI**：同一个 `PrismProxy.exe` 既可以启动抓包实例（GUI 或 headless），也可以用 `cli` 子命令控制正在运行的实例——查询状态、读取/清空流量、增改规则、切换系统代理、修改配置，甚至驱动 GUI 界面动作。

它的设计目标读者是 **AI agent / 自动化脚本**：默认输出单行 JSON（stdout），错误信息走 stderr 并以非零码退出，便于程序化解析；加 `--pretty` 可切换为人类可读的缩进格式。

> 设计与实现细节见 [方案.md](./方案.md) §4.12；实时推送（`flows watch` / SSE）设计见 [CLI实时推送设计.md](./CLI实时推送设计.md)；开发/验收记录见 [开发进度.md](./开发进度.md) 的 M8 章节。

---

## 1. 快速开始

```bat
:: 1. 先启动一个实例（两种模式任选其一）
PrismProxy.exe                  :: GUI 模式（正常双击启动等价）
PrismProxy.exe -headless        :: 无界面模式（后台常驻，适合服务器/自动化）

:: 2. 另开终端，用 cli 子命令控制它
PrismProxy.exe cli status --pretty
PrismProxy.exe cli flows list --limit 5
PrismProxy.exe cli rules ignore host api.example.com
```

CLI 与实例之间通过**本地回环控制 API**（默认 `127.0.0.1:9595`，与代理端口 9090 相互独立）通信。实例启动时自动生成随机 token 并把 `{addr, token}` 写入 **`config/ctl-endpoint.json`**（exe 同级 config 目录，与 settings.json 同目录；权限收紧为当前用户可读）。CLI 默认自动发现该文件，**无需手动传地址或 token**。

- 实例正常退出时自动删除 endpoint 文件；若实例被强杀导致文件残留，CLI 会报连接失败（友好提示），下次启动自动覆盖。
- GUI 与 headless 两种模式都开放控制 API，命令行为完全一致；仅 `ui` 界面类命令在 headless 下返回 `"ui": false`。

---

## 2. 命令总览

```
用法: PrismProxy.exe cli <命令> [参数] [--pretty] [--addr 地址] [--token token]

  status                                 代理状态 / 流计数 / 系统代理状态
  flows list [--filter 子串] [--limit N] 流摘要列表（不含 body）
  flows get <id>                         单流详情（Headers 等）
  flows get <id> --body req|resp         单流消息体（JSON：raw/body/base64）
  flows clear                            清空记录列表（保留置顶）
  flows watch [--filter 子串] [--status] [--format ndjson|sse]
                                         实时推送流变更/状态（SSE 长连接，持续运行）
  rules list                             过滤规则组 + 解密规则
  rules ignore host <域名>               快捷忽略域名（自身+全部子域）
  rules ignore process <进程名>          快捷忽略进程
  rules group <id> enable|disable        启用/停用过滤规则组
  rules decrypt mitm|bypass <域名>       添加解密规则（mitm 解密 / bypass 透传）
  sysproxy status                        系统代理状态（off|on|occupied）
  sysproxy on                            接管系统代理
  sysproxy off                           恢复系统代理
  settings get                           读取全部配置（JSON）
  settings set <key> <value>             改单项配置（读-改-写，自动热应用）
  ui clear                               清除记录列表（headless 下仅清数据）
  ui settings [tab]                      打开 GUI 设置面板
                                         （tab: general|network|decrypt|capture|domains）
```

全局参数可放在子命令前或后：

| 参数 | 说明 |
|---|---|
| `--pretty` | 缩进输出，便于人眼阅读（默认单行 JSON） |
| `--addr <host:port>` | 手动指定控制 API 地址（默认自动发现） |
| `--token <token>` | 手动指定 token（默认自动发现） |

退出码：`0` 成功；`1` 运行错误（实例未运行、HTTP 4xx/5xx 等，错误信息在 stderr）；`2` 用法错误（未知命令/缺参数）。`cli help` 无需实例在线即可查看帮助。

---

## 3. 命令详解与输出示例

### 3.1 status — 实例总状态

```bat
PrismProxy.exe cli status --pretty
```

```json
{
  "proxy": {
    "running": true,
    "addr": "127.0.0.1:9090",
    "mode": "mitm",
    "flowCount": 128,
    "startError": ""
  },
  "systemProxy": {
    "state": "on",
    "server": "127.0.0.1:9090",
    "override": "<-loopback>;..."
  },
  "ui": true,
  "headless": false
}
```

- `proxy.running`：代理监听是否在运行；`mode`：`MITM`（解密）或 `tunnel-only`（仅透传）。
- `systemProxy.state`：`off`（未接管）/ `on`（本工具已接管）/ `occupied`（系统代理指向别处，未被本工具占用）。
- `ui` / `headless`：当前是否为 GUI 模式——AI 可据此判断 `ui settings` 类命令是否会生效。

### 3.2 flows — 流量记录

```bat
PrismProxy.exe cli flows list                          :: 全部流摘要（旧→新）
PrismProxy.exe cli flows list --filter baidu --limit 20
PrismProxy.exe cli flows get 1757000000000-12
PrismProxy.exe cli flows get 1757000000000-12 --body resp
PrismProxy.exe cli flows clear
```

- **list**：返回流摘要数组（不含消息体）。`--filter` 按 host / URL / path 子串（不区分大小写）过滤；`--limit N` 返回最近 N 条。每条摘要字段（JSON 键名为 Go 导出名）：

  | 字段 | 含义 |
 |---|---|
  | `ID` | 流 ID（时间戳毫秒-序号），用于 get |
  | `State` | `pending` / `streaming` / `done` / `error` |
  | `Scheme` / `Method` / `Host` / `Path` / `URL` | 请求要素 |
  | `Status` | 响应状态码（0=未响应） |
  | `DurationMS` | 耗时毫秒 |
  | `BytesUp` / `BytesDown` | 上/下行字节数 |
  | `ProcessName` / `PID` | 归因进程（如 `powershell.exe`；模拟器流量归因为 NAT 进程） |
  | `StartedAt` | 开始时间（unix 毫秒） |
  | `Pinned` | 是否置顶（clear 时保留） |
  | `Source` | `capture`（抓包）/ `composer`（调试重发） |
  | `Err` | 错误信息（无则空串） |

- **get `<id>`**：单流详情，在摘要基础上增加 `ReqURL`、`ReqProto`、`ReqHeader`、`RespProto`、`RespHeader`、`ReqBodySize`、`RespBodySize`、TLS/证书/进程路径等全量字段。
- **get `<id> --body req|resp`**：取消息体，返回 `BodyPayload` JSON（字节字段为 base64 字符串）：

  | 字段 | 含义 |
 |---|---|
  | `Encoding` | 原始内容编码（gzip/deflate/br/zstd/空） |
  | `ContentType` | 原始 Content-Type |
  | `Truncated` | 是否超过 2MB 被截断 |
  | `Body` | 处理后的消息体（base64）：已按 gzip/deflate/br/zstd 解压、文本按 charset 转 UTF-8；`DecodeErr` 非空时为原始字节 |
  | `Raw` | 未解压的原始消息体（base64，便于保真处理） |
  | `DecodeErr` | 解压/转码失败原因（空=成功） |
- **clear**：清空记录列表，**置顶流保留**。返回 `{"cleared": N, "pinnedKept": true}`。

### 3.3 rules — 规则管理

```bat
PrismProxy.exe cli rules list
PrismProxy.exe cli rules ignore host api.example.com     :: 之后该域名及其全部子域的流量不再入列表
PrismProxy.exe cli rules ignore process curl.exe         :: 忽略该进程的所有流量
PrismProxy.exe cli rules group grp_abc123 disable        :: 停用某个过滤规则组
PrismProxy.exe cli rules group grp_abc123 enable
PrismProxy.exe cli rules decrypt mitm api.example.com    :: 对该域名强制 MITM 解密
PrismProxy.exe cli rules decrypt bypass pay.example.com  :: 对该域名跳过解密（盲透传）
```

- **list** 返回 `filterGroups`（过滤规则组，含 id/名称/启用状态/黑白名单模式/hosts/paths/processes 条目）、`decryptRules`（解密规则）、`quickIgnore`（两个内置快捷忽略组的 id）。
- **ignore** 写入内置「快捷忽略」组（域名组/进程组，组间 OR 互不干扰），幂等（重复添加返回 `{"added": false}`）；**只影响后续新流量，不删存量记录**；立即生效并落盘。
  - 域名按**裸域名语义**匹配：`example.com` 同时覆盖自身与全部子域。
- **group** 启用/停用过滤规则组（id 取自 `rules list`）；不存在的 id 返回 400。
- **decrypt** 追加解密规则并热重建引擎；`mitm` = 强制解密，`bypass` = 跳过解密（CONNECT 直接隧道）。

### 3.4 sysproxy — 系统代理开关

```bat
PrismProxy.exe cli sysproxy status     :: {"state": "off|on|occupied"}
PrismProxy.exe cli sysproxy on         :: 接管（自动备份原配置、合并绕过列表）
PrismProxy.exe cli sysproxy off        :: 恢复备份
```

注意：`sysproxy on` 要求代理本身在监听（`status.proxy.running=true`）；接管/恢复逻辑与 GUI 顶栏开关完全同链路，程序退出时会自动恢复。

### 3.5 settings — 配置读写

```bat
PrismProxy.exe cli settings get
PrismProxy.exe cli settings set maxFlows 5000
PrismProxy.exe cli settings set listenAddr 127.0.0.1:9090
PrismProxy.exe cli settings set autoSysProxy true
```

- **get**：返回完整 settings.json（监听地址、上游代理、存储上限 maxFlows/maxBodyMB、规则组、解密规则、绕过列表、界面偏好等）。
- **set `<key> <value>`**：读-改-写（先 GET 全量 → 改一个键 → PUT 整体提交），后端做校验并**热应用**（监听地址/上游变更会自动重启代理）。
  - 值自动按 JSON 标量解析：`true`/`false` → 布尔，纯数字 → 数字，其余 → 字符串。
  - 返回 `{"ok": true, "key": ..., "value": ..., "warnings": [...]}`；`warnings` 非空表示保存成功但有告警（如规则引用了不存在的域名组）。
  - 常用 key：`maxFlows`（流上限）、`maxBodyMB`（体上限）、`listenAddr`、`upstreamMode`（`direct`/`system`/`manual`）、`upstreamProxy`、`autoSysProxy`（启动自动接管系统代理）、`showSysProxySwitch`、`bypassList`（需整体替换时建议直接用 get/PUT HTTP API）。

### 3.6 ui — GUI 界面控制

```bat
PrismProxy.exe cli ui clear                 :: 清除记录列表（数据级动作）
PrismProxy.exe cli ui settings              :: 打开设置面板（默认常规 tab）
PrismProxy.exe cli ui settings decrypt      :: 打开并直接定位到「解密规则」tab
```

- **ui clear**：清空流量存储。GUI 模式下列表经事件实时同步；headless 模式同样清数据。返回 `{"cleared": N, "ui": true|false}`。
- **ui settings [tab]**：GUI 模式下弹出设置抽屉并切换到指定 tab，窗口最小化时会自动恢复；tab 白名单：`general`（常规）/ `network`（网络）/ `decrypt`（解密规则）/ `capture`（过滤规则）/ `domains`（域名组），非法值返回 400。**headless 模式无界面可驱动，返回 `{"ui": false, "tab": ...}`，不报错。**
- `ui settings` 不带子命令时等价于 `ui settings general`（`cli ui` 无参数即打开面板）。

### 3.7 flows watch — 实时推送（SSE 长连接）

```bat
PrismProxy.exe cli flows watch                          :: 持续输出全部流变更（ndjson）
PrismProxy.exe cli flows watch --filter baidu --status  :: 只推关键字流，并附带状态变更
PrismProxy.exe cli flows watch --format sse             :: 透传原始 SSE 文本
```

与其他命令不同，`watch` 是**长运行流式命令**：连接实例的 SSE 端点后持续输出，直到 Ctrl+C（退出码 0）或连续重连失败（退出码 1）。适合 agent 实时追踪流量，无需轮询。

| 参数 | 说明 |
|---|---|
| `--filter <子串>` | 只推 host / URL / path 含该子串（不区分大小写）的流；过滤在**服务端**完成，不匹配的流根本不会推送 |
| `--status` | 额外订阅状态频道：代理启停、系统代理接管/恢复、设置热重启等事件立即推送 |
| `--format ndjson` | （默认）每行一个 JSON 对象，每行自带 `event` 字段，最适合程序按行解析 |
| `--format sse` | 原样透传 SSE 协议文本（`event:`/`data:`/`id:` 行 + 空行分隔），可直接对接 SSE 客户端 |

**ndjson 帧类型**（每行 JSON 的 `event` 字段）：

| event | 触发时机 | data 关键字段 |
|---|---|---|
| `snapshot` | 连接建立后**第一行**，当前已有流的快照 | `flows: [...]`（字段同 flows list） |
| `flows` | 有新流/流更新/流被淘汰 | `type: "upsert"` 时 `flows: [...]`；`type: "evict"` 时 `ids: [...]` |
| `status` | 仅 `--status`：代理/系统代理状态变化 | `proxy` / `systemProxy` / `ui` / `headless`（同 status 命令） |

输出示例（ndjson，已截断）：

```json
{"event":"snapshot","flows":[{"ID":"1757...-1","Host":"www.baidu.com", ...}]}
{"event":"flows","type":"upsert","flows":[{"ID":"1757...-2","State":"streaming","Host":"api.example.com", ...}]}
{"event":"flows","type":"evict","ids":["1757...-9"]}
{"event":"status","proxy":{"running":true,"flowCount":42,...},"systemProxy":{"state":"on",...}}
```

- **快照语义（先订阅后快照）**：watch 先建立事件订阅、再拉取一次当前流列表作为 `snapshot`，然后逐帧推送增量。因此**不会丢失**连接瞬间产生的流——重叠部分靠 upsert 幂等（按 ID 覆盖）、evict 忽略未知 ID 天然安全。消费方把 snapshot 当全量基线，之后 upsert 按 ID 合并、evict 按 ID 删除即可。
- **背压保护**：服务端给每个订阅者 128 帧缓冲；消费过慢导致缓冲满时，服务端会推送一帧 `reset`（ndjson 下 event 为 `reset`）并主动断开，watch 立即自动重连（重连后重新收到 snapshot 全量基线），不会丢数据一致性。
- **断线重连**：连接中断时 watch 自动指数退避重连（0.5s→1s→2s→4s→8s），**每次重连重新读取 `config/ctl-endpoint.json`**（实例重启后 token 会变，用旧 token 会一直 401）；连续 5 次失败放弃并以退出码 1 退出（常见于实例未启动）。
- **空闲心跳**：无事件时服务端每 15s 发一行 SSE 注释 `: ping` 保活（ndjson 模式不输出，sse 模式可见）。
- 退出码：Ctrl+C/Ctrl+Break 优雅停止 = `0`；连续重连失败/用法错误 = `1`/`2`。

**也可以直连 SSE 端点**（不经 CLI）：

```
GET http://127.0.0.1:9595/api/v1/events?channels=flows,status&filter=<子串>&token=<token>
Accept: text/event-stream
Authorization: Bearer <token>     # 或把 token 放在 query（仅 /events 端点支持，方便 curl/EventSource）
```

`channels` 逗号分隔，可选 `flows`、`status`（缺省仅 `flows`）；未知频道返回 400；订阅者上限 32，超出返回 503。首帧为 `event: open`（含 server/version/instanceId/channels），订阅 status 时紧接着一帧 `status` 种子帧。

---

## 4. AI / 脚本集成要点

- **发现实例**：先跑 `cli status`；退出码非 0 且 stderr 提示"未发现运行中的实例"时，可先启动 `PrismProxy.exe -headless`（后台）再重试。
- **解析输出**：stdout 恒为 JSON（成功时是结果对象/数组），stderr 为人类可读错误。建议按退出码判断成败，再解析 stdout。
- **实时监控（推荐）**：`cli flows watch [--filter ...] [--status]` 以 SSE 长连接持续推送流变更与状态，无需轮询、不漏帧（先订阅后快照）。stdout 为 ndjson（每行一帧，带 `event` 字段），可按行读取解析；Ctrl+C 退出码 0。详见 §3.7。
- **轮询监控**（简单场景）：`cli flows list --limit N` 可定期拉取最新流量；`cli status` 可做健康检查。代理刚启动的瞬间即可连通控制 API。
- **典型自动化链路**：
  1. `sysproxy on`（或让目标应用显式走 `127.0.0.1:9090`）；
  2. 操作目标 App 产生流量；
  3. `flows list --filter <关键字>` 定位流，`flows get <id> --body resp` 取响应体分析；
  4. 噪声太多时 `rules ignore process <进程名>` 收敛，或 `rules group <id> disable` 临时关规则组；
  5. 结束后 `sysproxy off` 恢复系统代理。
- **直接调 HTTP API**：CLI 只是薄封装，也可以直接用任意 HTTP 客户端访问 `http://127.0.0.1:9595/api/v1/...`，请求头带 `Authorization: Bearer <token>`（或 `X-Prism-Token: <token>`），token 从 `config/ctl-endpoint.json` 读取。路由与 CLI 命令一一对应（如 `GET /flows?filter=&limit=`、`POST /flows/clear`、`POST /rules`、`PUT /settings`、`POST /ui/settings`）；实时推送走 SSE 端点 `GET /events?channels=flows,status`（唯一支持 query 参数 `token=` 的端点，详见 §3.7）。

## 5. 安全说明

- 控制 API **仅绑定 127.0.0.1**，不对局域网开放；token 为 32 字节随机值，每次启动重新生成，认证使用常量时间比较。
- endpoint 文件权限收紧为当前用户可读写（Windows ACL），正常退出即删除。
- 任何能在本机以你的用户身份执行程序/读取该文件的进程，都能控制本实例——请勿在多用户共用机器上放宽 config 目录权限。
