# 数据复盘 × AI 分析设计（M13）

> 版本：v2.3（2026-09-11，首块超时按自托管/云端区分：自托管（未配 APIKey）取 Timeout 全额，云端维持 min(30s, Timeout/4)——实测本地 Ollama 64KB prompt 温机 TTFB≈17s，冷启动叠加模型加载可破 30s；测试连接小 prompt 探不出该差异）
> v2.2（2026-09-11，BaseURL 归一化放宽为「末段 `/v<纯数字>` 保留」——智谱 GLM 官方 OpenAI 兼容端点为 https://open.bigmodel.cn/api/paas/v4 ，仅认 /v1 会误补成 /v4/v1 404）
> v2.1：2026-09-10，计划审计后定稿修订：主窗内嵌 AI 通道=Wails 事件桥、/ai/config 部分更新语义、温度 0 哨兵、取数方法名与截断方向修正、AIConfig 零值兜底。
> v2：2026-09-10，增补「接口意图批量标注 intent」需求。
> v1：2026-09-09 初稿。
> 适用里程碑：M13（下一个版本：数据复盘 × AI 分析，见 README Roadmap）
> 关联文档：[标签与数据复盘设计.md](./标签与数据复盘设计.md)、[项目配置设计.md](./项目配置设计.md)、[AI-CLI使用说明.md](./AI-CLI使用说明.md)、[开发进度.md](./开发进度.md)、[方案.md](./方案.md)

---

## 一、需求

在「数据复盘」中引入 AI 分析能力（README Roadmap「下一个版本」三项）：

1. **接口功能解读（explain）**：对抓到的接口（单条流），由 AI 分析其功能、参数含义与调用时机。入口在复盘详情标题栏，与「调试重发」并列。
2. **接口意图批量标注（intent）**：对一批接口（勾选的流或当前视图已加载的流），由 AI **一次性**为每条流给出一句话「大致功能 / 意图」（如「提交订单」「查询用户余额」「上报设备信息」，建议 ≤20 字），结果回填为流的意图标注，并在**详情顶部摘要条**展示当前选中流的意图；摘要条可重新分析、可跳转完整解读。定位是 explain 的轻量前置版：只看方法/URL/关键头/（可选）少量正文，省 token、快出结果，帮用户在大量记录中快速看懂每条接口是干什么的。
3. **智能定位接口（locate）**：从当前复盘视图的一批抓包记录中，按用户用自然语言描述的目标（如「找下单接口」「哪个接口返回了用户余额」），自动分析出需要的接口，无需逐条翻找；结果可点击跳转到对应流。
4. **业务逻辑整理（flowmap）**：梳理接口间的调用关系，自动产出结构化分析结果——如登录接口时序梳理、下单流程链路；输出 Markdown（有序号步骤、接口列表）。
5. **AI 接口配置**：支持用户自行配置 AI 服务商接口（OpenAI 兼容协议：baseURL / apiKey / model / 温度等），提供预设快捷填充；在**主窗口设置抽屉新增「AI 分析」设置面板**，含「测试连接」。
6. **流式输出**：AI 结果在复盘页边生成边展示（打字机 / 意图逐条回填），支持中途停止。

**非目标（本期不做）**：

- 主窗（实时列表）AI 入口（一期仅复盘页 + 设置面板）；
- 分析结果落库持久化（会话内查看，刷新即重算；意图标注同样只存前端会话内存 Map，不入库；见 §十一待拍板 10）；
- mermaid 图渲染（一期 Markdown 文本，时序图用编号步骤/列表表达，见 §十一 12）；
- 非 OpenAI 兼容协议（Anthropic / Gemini 原生协议）、多服务商多密钥管理、用量计费统计；
- AI CLI 子命令（`cli ai ...`）（一期不做，配置可走已有 `/api/v1/settings`，见 §十一 14）。

---

## 二、现状与关键约束（已逐行核对代码）

| 主题 | 现状 | 对本设计的影响 |
| --- | --- | --- |
| 复盘页通道 | 复盘页是独立 Vite 入口（`src/review/`，不依赖 Wails runtime），全部数据走 ctlapi HTTP（127.0.0.1:9595，Bearer token）；`review/api.ts` 双实现 `HttpApi`/`DemoApi`，新接口两端都要补 | **AI 分析只能走 ctlapi HTTP**；浏览器原生支持 fetch ReadableStream，流式可用 SSE；EventSource 不能带 Authorization 头，故流式用 **fetch + ReadableStream 解析**（非 EventSource），鉴权仍走 Bearer 头 |
| ctlapi | `server.go` ServeMux 前缀注册 + 手动分段（`/api/v1/tags/...`）；Service 接口方法由接线层实现；`handleUI` 已处理 `POST /ui/settings`（`UISettingsTabs` 白名单） | 新增 `/api/v1/ai/chat`（SSE 流式）+ `GET/POST /api/v1/ai/config`（脱敏读/即时保存）；`UISettingsTabs` 增加 `"ai"`（复盘页「去设置」按钮经 `POST /ui/settings{tab:"ai"}` 唤起主窗，headless 返 ui:false） |
| 全局配置 | `settings.GlobalSettings`（`config/settings.json`，exe 同级便携目录，0600 权限先写 tmp 再 rename）含 listen/upstream/persist/adb/projects 等；`GetSettings/SaveSettings` 经 `SettingsView` DTO 合并全局+项目字段，主窗设置抽屉统一保存 | AI 配置是**环境类、跨项目共享**（与 ADB/持久化同类，一个服务商配置服务所有项目），挂到 `GlobalSettings.AI`；**但 apiKey 不进 `SettingsView` 全量保存往返**（避免与规则表单同体误覆盖/落日志），密钥读写走独立 `/ai/config` 接口（§5.3） |
| 设置面板 | `SettingsPanel.vue` 左竖排 tabs：常规/网络/ADB（全局，无项目也可见）+ 解密/过滤/域名组（项目级，`v-if="hasOpenProject"`）；`PROJECT_TABS` 白名单控制无项目回退；tab 组件在 `pages/settings/`，通过 `:form` 共享表单 | 新增 `pages/settings/AiTab.vue`：**全局 tab，无项目时可见**（不加进 PROJECT_TABS）；非密钥字段随 `form` 统一保存，密钥字段独立即时保存（§6） |
| 后端依赖 | go.mod 核心零外部依赖；HTTP 出站已有 `net/http`（compose 重发 `internal/compose`）；modernc.org/sqlite 是唯一重依赖 | AI 客户端用 **Go 标准库实现**（`internal/ai` 新包）：OpenAI Chat Completions 协议 + SSE 解析，不引第三方 SDK |
| 复盘取数 | `ListFlowsByTag`（元数据列表）、`GetTaggedFlow`（详情）、`GetTaggedFlowBody`（惰性正文 zstd/base64）；列表有 scope/start/end/q/sort/showIgnored 过滤与分页（limit 默认 200 上限 1000） | locate/flowmap 的分析数据集 = **复盘当前视图已加载的流 id 集合**（前端把上下文传给后端），后端按 id 从归档库取数裁剪；不为 AI 做全库扫描 |
| 正文敏感面 | 抓包正文可能含 Token/Cookie/密码；复盘页已有敏感凭据警示条 | AI 请求会把数据发往**用户自配的第三方服务商**——必须显著告知 + 默认脱敏开关（§5.4）；apiKey 只存本机、不出现在任何 AI 请求上下文 |
| Wails 绑定同步约定 | 改 ctlapi Service 接口必须同步 `ctlapi_test.go` fakeService（`var _ Service` 编译期断言）；Go 结构体经绑定导出须同步 `frontend/wailsjs/` 三个生成文件，即使复盘走 HTTP | 本期 AI 能力**不经 Wails 绑定**（测试连接在主窗可用绑定调，也可走 ctlapi；见 §5.3 定稿：**主窗测试连接走新增 Wails 绑定**，须同步生成文件） |
| Demo 模式 | 无 token/?token=mock → DemoApi（内存演示数据）；compose 等重发能力 demo 桩抛错 + 按钮禁用（用户既定决策） | AI 分析在 demo 模式**可用但走本地模拟流**（前端不发网络请求，模拟 SSE 逐段吐字），便于纯浏览器演示 UI；「去设置/测试连接」类配置操作在 demo 下禁用 |

---

## 三、总体方案

```
┌──────────── 系统浏览器：复盘页 review.html ────────────┐
│  详情标题栏：[AI 解读本条]   顶栏：[✨ AI 分析]            │
│  详情顶部：意图摘要条（✨ 提交订单 [重析][完整解读]）      │
│  列表：勾选若干流 → 批量「标注意图」                      │
│  ReviewAiPanel（右侧抽屉，560px）：                       │
│    模式 tabs：接口解读 | 意图标注 | 智能定位 | 业务逻辑    │
│    目标输入（locate/flowmap） + 范围说明 + [开始][停止]    │
│    流式 Markdown 渲染区（marked + DOMPurify）             │
│    locate 结果：可点击流卡片 → 跳转列表选中                │
│  fetch POST /api/v1/ai/chat（Bearer，ReadableStream SSE） │
└───────────────────────┬───────────────────────────────┘
                        │ HTTP/SSE（127.0.0.1:9595）
        ┌───────────────▼────────────────┐
        │ ctlapi：/api/v1/ai/chat          │
        │   ai.Service（接线层）：         │
        │   ① 按 ids 从归档库取流+正文      │
        │   ② 裁剪/脱敏 → 组 Prompt        │
        │   ③ 调 internal/ai Client        │
        │   ④ 上游 SSE 逐块转写下游         │
        └───────────────┬────────────────┘
                        │ net/http（OpenAI 兼容 Chat Completions，stream=true）
                        ▼
              用户自配 AI 服务商（https://…）

┌──────── 主窗口（Wails）：设置抽屉新增「AI 分析」tab ────────┐
│  AiTab：服务商预设选择 / baseURL / apiKey（密码框）/ model  │
│  / 温度 / 上下文预算 / 脱敏开关 / [测试连接]                │
│  apiKey 独立即时保存；其余随表单保存                        │
└───────────────────────────────────────────────────────────┘
```

**核心决策**：

- **AI 配置全局唯一**：一份 OpenAI 兼容配置（baseURL/apiKey/model/参数），所有项目共享；设置面板位于主窗（全局 tab，无项目也可配）。
- **协议只认 OpenAI Chat Completions 兼容**（含 stream SSE）：覆盖 OpenAI 官方、DeepSeek、Moonshot、智谱、通义、本地 Ollama（`/v1/chat/completions`）、OneAPI/NewAPI 等绝大多数供给，成本最低。
- **后端做编排，不做透传**：前端只表达「分析意图 + 流 id 集合」；取数、裁剪、脱敏、Prompt 组装、上游调用与流式转写全在 Go 侧（密钥不下发浏览器、Prompt 口径单一、可测试）。
- **结果流式回传**：上游 SSE 由后端逐块转成自己的 SSE 事件（`delta` / `match` / `intent` / `error` / `done`），前端 fetch 读取打字机渲染或逐条回填。
- **分析数据集 = 当前视图**：intent/locate/flowmap 只分析复盘页当前筛选（标签/scope/时间窗/关键字/忽略）下、前端明确传入的 id 集合（intent 默认勾选流或当前已加载页；locate/flowmap 默认当前已加载页，可扩选「全部匹配项」），后端硬性数量/字节双预算封顶。
- **意图标注是轻量批量模式**：intent 与 locate 同走「ids 集合」数据集，但输出不是 Markdown 长文，而是每条流一条短句，经结构化 `intent` 事件逐条回填，前端缓存到会话级 `Map<flowId, intent>`，供详情摘要条/列表角标读取；不逐条调用（一次请求标整批，控成本）。

---

## 四、使用流程

1. 主窗 → 设置 → 「AI 分析」tab：选服务商预设（自动填 baseURL 与模型）→ 填 apiKey → 「测试连接」通过 → 保存。
2. 复盘页：
   - **解读单接口**：选中一条流 → 详情标题栏点「AI 解读」→ AI 面板以 explain 模式打开并自动开始，产出该接口功能/参数含义/调用时机的 Markdown 解读。
   - **批量标注意图**：在列表勾选若干条流（或不勾选则默认当前已加载页全部）→ 点「✨ AI 分析」选「意图标注」→ 开始 → AI 一次分析整批，每条流逐条回填一句话意图（进度可见、可停止）；标注完成后，选中任意一条流，**详情顶部摘要条**即显示其意图（如「提交订单」），摘要条提供「重新分析本条」与「完整解读」（跳 explain）。
   - **智能定位**：点顶栏「✨ AI 分析」→ 选「智能定位」→ 输入「帮我找下单提交的接口」→ 开始 → AI 在当前视图流中筛选，结果以卡片列出（方法/URL/判定理由/置信度），点击卡片列表跳转并选中该流。
   - **业务逻辑**：选「业务逻辑整理」→ 输入「梳理登录流程」→ 开始 → 产出编号步骤链路（每步引用接口），接口引用同样可点击跳转。
3. 生成中可「停止」；失败（未配置 key、网络不通、上游 4xx/超时）面板内联报错并给出对应动作（去设置/重试）。

---

## 五、后端设计

### 5.1 配置结构（settings 包，全局）

`internal/settings/settings.go` 的 `GlobalSettings` 新增：

```go
// AI AI 分析配置（M13，全局环境类，跨项目共享）。
// 零值 = 未配置：Enabled=false，复盘页 AI 入口显示但点击引导去设置。
AI AIConfig `json:"ai"`
```

```go
// AIConfig OpenAI 兼容 Chat Completions 配置。
type AIConfig struct {
    Enabled  bool   `json:"enabled"`  // 是否启用 AI 分析（未配置 key 时前端入口仍可见，仅置灰引导）
    Provider string `json:"provider"` // 服务商预设 id：openai|deepseek|moonshot|zhipu|qwen|ollama|custom（仅 UI 预设用，后端不依赖）
    BaseURL  string `json:"baseUrl"`  // 形如 https://api.deepseek.com（不带版本段，拼接时归一化）；允许填到 /v1（或 /v4 等版本段）前缀，见 NormalizeAIBaseURL
    APIKey   string `json:"apiKey"`   // 密钥；独立读写接口，不进 SettingsView 全量 DTO
    Model    string `json:"model"`    // 模型名，如 deepseek-chat / gpt-4o-mini
    // 调参（旧配置 JSON 缺 AI 字段反序列化得零值，读取侧统一经 withDefaults() 兜底，见下）
    Temperature float64 `json:"temperature"` // 默认 0.3（分析任务偏低温度）；允许 0.1–2（0 视为未设置=哨兵，UI slider min 0.1）
    TimeoutSec  int     `json:"timeoutSec"`  // 整请求超时，默认 120；流式下为首块+整体上限
    MaxFlows    int     `json:"maxFlows"`    // locate/flowmap 单次分析最大流数，默认 50、上限 100
    MaxKB       int     `json:"maxKb"`       // 单次送审正文总预算 KB，默认 64、上限 256
    Redact      bool     `json:"redact"`      // 发送前脱敏（默认 true，§5.4）
}
```

- `DefaultGlobal()` 给默认子值：`AI{Enabled:false, Provider:"custom", Temperature:0.3, TimeoutSec:120, MaxFlows:50, MaxKB:64, Redact:true}`。
- **零值兜底（v2.1 定稿）**：`func (c AIConfig) WithDefaults() AIConfig`——Temperature==0→0.3、TimeoutSec<10→120、MaxFlows<1→50、MaxKB<8→64、零值（未初始化）Redact→true；**仅读取侧兜底不回写落盘**。调用点三处：SettingsView 投影（GetSettings/GetAIConfig）、ai.Client 构造、裁剪入口。动机：老用户 settings.json 无 `ai` 字段，反序列化后 `AI` 为零值——若不兜底，Redact=false 等于脱敏默认关闭（隐私倒退）、Timeout/预算 0 导致裁剪与首块超时行为未定义。ValidateEnv 仍按界面显式保存的值校验（Enabled=true 时值已在 UI 约束内）。
- `ValidateEnv()` 增补：`Enabled=true` 时 BaseURL 必须是 http(s) URL、Model 非空；Temperature ∈ [0.1,2]（0 为哨兵不显式保存）；TimeoutSec ∈ [10,600]；MaxFlows ∈ [1,100]；MaxKB ∈ [8,256]。**APIKey 不强制（自托管 Ollama 可无 key）**；key 空仅 warning。**warning 通道归属（v2.1 定稿）**：`ValidateEnv()` 现签名只返 `error`，不改签名——key 空检查放在 `app.SaveSettings` 环境半边校验后追加到既有 `SaveSettingsResult.warnings`（与 M9 规则 warning 同通道），零签名变更零调用方波及。
- BaseURL 归一化（v2.2 修订）：`strings.TrimRight` 去尾 `/`；**末段为 `/v<纯数字>`**（如 `/v1`、智谱 GLM 官方端点 `https://open.bigmodel.cn/api/paas/v4` 末段 `/v4`）则原样保留，否则追加 `/v1`；最终请求 URL = base + `/chat/completions`。判定取 `LastIndex('/')` 后的末段校验 `v`+全数字（域名段如 `api.deepseek.com` 不含 `/` 分隔不会误判）。

### 5.2 AI 客户端（新包 internal/ai，零外部依赖）

```text
internal/ai/
├─ client.go      # Client：OpenAI 兼容 Chat Completions（stream=true），SSE 解析
├─ client_test.go # SSE 帧解析/data: [DONE]/错误体/超时/BaseURL 归一化
├─ prompt.go      # 四种模式的 system/user Prompt 构造 + 流数据裁剪/脱敏（纯函数，可单测）
└─ prompt_test.go
```

**Client 形态**：

```go
type Config struct {
    BaseURL, APIKey, Model string
    Temperature            float64
    Timeout                time.Duration
    ProxyURL               string // 出站代理（接线层算好传入；空=直连），ai 包不反查全局配置（避免 import app 循环依赖）
}

type Delta struct {
    Text     string  // 本块增量文本
    Reason   string  // 可选：推理模型 reasoning 增量（deepseek-reasoner 等），前端可折叠展示，一期可忽略只透传
}

// Stream 发起一次流式对话。onDelta 在收到增量块时回调（同 goroutine 顺序调用）；
// ctx 取消即关闭 HTTP 连接并返回 ctx.Err()。
func (c *Client) Stream(ctx context.Context, messages []Message, onDelta func(Delta)) error
```

- 请求体：标准 OpenAI 形态 `{model, messages:[{role,content}...], temperature, stream:true, stream_options:{include_usage:false}}`。
- 响应：`Content-Type: text/event-stream`，逐行扫 `data: {...JSON...}`，取 `choices[0].delta.content` 拼接回调；遇 `data: [DONE]` 正常结束。
- 非 200：读响应体（截断 2KB）解析 `error.message`，包装为可读错误（如「服务商返回 401：Incorrect API key」）。
- 传输：`http.Client{Timeout}` 不适合流式（整体超时会掐断长回答）——用 `http.NewRequestWithContext(ctx)` + 「首块超时」控制：启动定时器，收到首个 data 帧后取消，整体由 SSE handler 的客户端断连/`TimeoutSec` 外层 ctx 兜底。首块超时阈值（v2.3 修订）按部署形态区分：
    - **云端（已配 APIKey）**：`min(30s, Timeout/4)`——云端 TTFB 通常秒级，快速判死减少无效等待。
    - **自托管（未配 APIKey，Ollama/LM Studio 等）**：`Timeout` 全额——本地模型首 token 前需完成冷加载 + 全量 prompt prefill，实测 64KB prompt 温机 TTFB≈17s（8192 上下文截断后仍需 17s），冷启动叠加模型加载可破 30s；`Timeout<=0` 时兜底 30s。
    - 自托管超时错误文案追加提示：「本地模型冷启动/长文本推理可能较慢，可调大设置中的超时时间后重试」。测试连接用小 prompt 探不出该差异（温机小 prompt TTFB≈0.2s），属预期。
- 出站代理（v2.1 修订）：**ai 包不做代理装配**——`Config.ProxyURL` 由接线层按全局 `UpstreamMode` 算好传入（空=直连）：manual 取配置代理；system 取系统代理并经既有防环逻辑排除自身（接管后防环返回空=直连，语义自动继承）；direct 直连。compose 现状是调用方算好 `Upstream` 字符串传入（compose.go `Sender.Upstream`），ai 包同构——**无可复用的独立 helper，不引入 compose 依赖**。

### 5.3 ctlapi 接口

Service 接口新增（`server.go`）：

```go
// AI 分析（M13）
GetAIConfig() (any, error)                 // 脱敏配置（apiKey 回 ****1234 形态 + hasApiKey 布尔）
SaveAIConfig(raw json.RawMessage) error    // 部分更新（见下路由表：出现的字段才覆盖；key 哨兵语义）
AITestConnection(raw json.RawMessage) (any, error) // 测试连接（可带未落盘的临时配置），返回 {ok, model, latencyMs, message}
AIChat(raw json.RawMessage, w http.ResponseWriter, r *http.Request) error // SSE 流式（特殊：直接写响应流）
```

> 实现说明：ctlapi 现有 handler 签名都是 `func(http.ResponseWriter,*http.Request)`，Service 保持返回数据的风格。`AIChat` 需要**流式**，定稿为 handler 内调用一个流式 service 方法：
>
> ```go
> // StreamAIChat 解析 req，按 ids 取数组 Prompt，逐块向 emit 推送；emit 负责 SSE 帧序列化。
> StreamAIChat(ctx context.Context, req AIChatRequest, emit func(event AIChatEvent) error) error
> ```
>
> handler 负责 SSE 响应头（`Content-Type: text/event-stream`、`Cache-Control:no-cache`、`X-Accel-Buffering:no`、`Flush()` 立即下发）、把 event 编码成 `event: xxx\ndata: {...}\n\n`。

**路由**（均挂 `s.auth`，前缀注册）：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/ai/config` | 返回脱敏配置：`{...AIConfig, hasApiKey:true, apiKeyMasked:"••••••••1234"}`（apiKey 原值**永不**经此口返回；key 为空时 hasApiKey:false） |
| POST | `/api/v1/ai/config` | **部分更新（v2.1 定稿，M3）**：body 为 AIConfig 子集 JSON，**出现的字段才覆盖、未出现保持原值**（逐字段解析 `map[string]json.RawMessage` / 自定义 Unmarshal）。`apiKey` 特例：传空串=保持原 key、传 `"__clear__"` 哨兵=清空、传值=换 key。即时落盘（不经 SettingsView）。响应返回脱敏投影（同 GET）。理由：主窗 AiTab「保存密钥/清除密钥」只发 `{apiKey}` 单字段，若按全量语义其余字段会被清零；带全量表单又会误存脏表单——两个保存通道从此不打架 |
| POST | `/api/v1/ai/test` | 测试连接。body 同配置（允许用界面上未落盘的值试连；apiKey 空串时读已存 key）。发一次极简请求（`max_tokens:8` 或非流式 1 token 探测），10s 超时；返回 `{ok, model, latencyMs, message}`（v2.1：补 `model`，前端成功提示需要） |
| POST | `/api/v1/ai/chat` | 流式分析。body 见下；响应 SSE |

`POST /api/v1/ai/chat` 请求：

```jsonc
{
  "mode": "explain|intent|locate|flowmap",
  "flowId": "f_xxx",          // explain 必填（单流）
  "ids": ["f_1", "f_2"],      // intent/locate/flowmap 必填：当前视图候选流 id。截断方向（v2.1 定稿）：前端按 StartedAt 降序传（与列表默认排序一致）；后端取数后仍按 StartedAt 降序复查再截前 MaxFlows（保证 AC11「仅发送最近 50 流」不依赖前端传参顺序，前端传参顺序不作契约）
  "question": "帮我找下单提交接口", // locate/flowmap 必填（explain/intent 可空）
  "options": {
    "includeReqBody": true,   // 是否带请求正文片段（explain 默认 true；intent/locate 默认 false 仅头+元数据）
    "includeRespBody": true,  // 同上（explain 默认 true）
    "language": "zh"          // 输出语言，默认 zh
  }
}
```

> intent 默认 `includeReqBody/includeRespBody=false`：意图主要靠方法/路径/query/关键头推断，必要时（如全是 `POST /api/action` 这类无语义路径）Prompt 允许模型在结论里标 `needsBody:true`，由前端提示用户「开启请求正文后重析」而非自动发正文（控成本 + 隐私最小化）。

**SSE 事件协议**（后端自定义，非透传上游）：

```text
event: meta
data: {"mode":"locate","total":12,"sent":10,"budget":{"flows":50,"kb":64},"truncated":false}

event: delta
data: {"text":"根据描述，最可能的"}

event: intent
data: {"flowId":"f_12","seq":3,"intent":"提交订单","confidence":"high","needsBody":false}

event: match
data: {"flowId":"f_12","rank":1,"method":"POST","url":"https://.../orders","reason":"…","confidence":"high"}

event: error
data: {"message":"未配置 API Key，请先到 设置 → AI 分析 配置"}

event: done
data: {"finishReason":"stop","truncated":false}
```

- `meta` 在组完 Prompt、调用上游前下发（前端先看到「已选取 N 条、发送约 X KB」）。
- `delta` 为正文增量，前端追加到 Markdown 渲染缓冲（**节流渲染**：rAF 或 50ms 合并，避免逐块重排）。
- `match` 仅 locate 模式：定位结论以**结构化卡片**为主、Markdown 为辅。两种实现路线（§十一 7 拍板，**建议路线 A**）：
  - **路线 A（一期，稳）**：单轮调用，Prompt 要求模型先输出 Markdown 分析，再在文末输出严格 JSON 代码块（```json {"matches":[{...}]}```）；后端解析该代码块，逐条发 `match` 事件并从正文中剥离该块。解析失败则仅展示 Markdown（降级，不报错）。
  - 路线 B（二期）：tool-calling/function-calling（各兼容厂商支持参差，DeepSeek 支持、部分小厂/Ollama 模型不支持）。
- `intent` 仅 intent 模式：Prompt 要求模型对每个 `[#n]` 输出一条严格 JSON 数组（```json {"intents":[{"seq":1,"flowId":"f_1","intent":"…","confidence":"high|medium|low","needsBody":false}]}```，与 locate 同套文末 JSON 块解析路线 A）；后端解析后**逐条**发 `intent` 事件（前端边收边回填，不必等整批），前端覆盖式写入会话 Map（重析同流以最新结果为准）。正文 Markdown 可有可无（一期允许模型只输出 JSON 块，后端剥块后若无剩余文本则不产生 delta）。confidence=low 或 needsBody=true 时前端在摘要条给弱样式/「带正文重析」提示。
- 客户端断连（用户点停止/关页）→ `r.Context()` 取消 → Client 关闭上游连接，goroutine 退出，不写库不留任务。
- **并发限制**：同实例同时只允许 1 个进行中的 AI Chat（新请求返回 409「已有分析进行中」）——防止浏览器重复提交叠加费用；停止后释放。内存 `atomic.Bool`/带缓冲 chan 做闸门即可（闸门在 service 实现层，HTTP 409 与 Wails 桥 error 共用同一闸门）。
- **主窗内嵌复盘通道（v2.1 定稿，方案 A：Wails 事件桥）**：M12.3 拍板复盘并入主窗为主要形态，而主窗内嵌 `WailsReviewApi` **不经 ctlapi HTTP、无 token**（wails.localhost 源 fetch `127.0.0.1:9595` 属跨源，服务端刻意无 CORS 头；SSE 也无法走 Wails 请求-响应绑定）。定稿：
  - 新增 Wails 绑定（`bindings_ai.go`，单结构 DTO + error 铁律）：`AIChatStart(req) (AIChatHandle, error)`（内部复用 StreamAIChat 同一编排：取数→裁剪脱敏→ai.Client→逐帧 `EventsEmit("ai:chat", {handle, event, data})` 转发；handle 关联取消函数）与 `AIChatStop(handle string) error`（ctx cancel，语义同 HTTP 断连）。
  - 前端 `review/api/wails.ts` 的 `analyze` 用 `EventsOn("ai:chat", ...)` 订阅 + `signal` 触发 `AIChatStop`；`getAIConfig` 走 `AIChatStart` 同文件新增的 `GetAIConfigApp`/`SaveAIConfigApp` 绑定（或复用 ctl_bridge 暴露——定稿：**Wails 绑定直连 app 层实现**，不经 ctlapi，与主窗 GetSettings 同模式）。
  - 与 `ui:open-settings` 事件推送先例同构；token 不出后端、不扩大 CORS 面；HTTP SSE 通道（独立浏览器复盘页）与事件桥共用 StreamAIChat 编排与并发闸门，双通道互斥由同一 `atomic.Bool` 保证（Wails 侧冲突返回 error 而非 409，前端统一错误 UI）。

### 5.4 取数、裁剪与脱敏（prompt.go）

**取数**（接线层，复用归档只读路径；方法名为 persist.Writer 现有 API，v2.1 修正——原稿 `ListFlowsByTag/GetTaggedFlow/GetTaggedFlowBody` 系笔误，代码中不存在）：

- explain：`LoadFlowByID(id)` 详情 + `LoadBody(id, req/resp)`（惰性 zstd 解压）。
- intent/locate/flowmap：**新增 `LoadFlowsByIDs(ids []string)`**（单条 `IN` 查询，n≤MaxFlows≤100，按 StartedAt 降序返回；缺失 id 即失效）；`includeReqBody/includeRespBody=true` 时对入选流逐个 `LoadBody` 取正文。id 不存在/已删跳过；全部失效 → 直接 error 事件「候选流已不存在，请刷新列表」。
- intent 正文策略：默认不取正文（仅元数据 + 关键头）；`needsBody` 是**模型输出侧标记**而非后端二次取数（一期不做服务端自动二轮调用，避免隐式费用）。

**裁剪（双预算硬封顶，纯函数可测）**：

- 流数：截取前 `MaxFlows` 条，超出 `truncated=true`（meta 事件告知「候选 86 条，仅分析最近 50 条，请缩小时间窗/加关键字」）。
- 字节：每流每侧正文截断 `perSide` 预算（如总预算 MaxKB 平分，explain 单流给 req 8KB/resp 8KB），截断处加 `…（已截断，原始 N KB）`；总超限按流序贪心丢弃低价值侧（intent/locate 先丢响应体再丢请求体，保留 method/url/status/关键 query/header）。
- 默认携带字段：method、完整 URL（含 query）、状态码、耗时、进程名、请求/响应 Content-Type、关键头（`Content-Type`、`Authorization` 仅标注类型不送值、自定义业务头白名单如 `X-Token` 同样脱敏）；**默认不送 Cookie 头原值**。
- CONNECT/无正文/二进制（非文本 Content-Type 或含 NUL 字节）正文跳过，标注 `[binary/octet-stream, N bytes]`。

**脱敏（`Redact=true` 默认开，正则在 Go 侧）**：

- 头：`Authorization`、`Proxy-Authorization`、`Cookie`、`Set-Cookie`、`X-Api-Key`、`X-Token` 等 → 值替换为 `[REDACTED]`（保留 scheme 如 `Bearer [REDACTED]`）。
- 正文：JWT（`eyJ[\w-]+\.[\w-]+\.[\w-]+`）、常见密码/凭据键（`"(password|passwd|pwd|secret|token|access_token|refresh_token|apiKey|api_key)"\s*:\s*"[^"]*"` 大小写不敏感）→ 值替换 `"***"`；手机号/身份证/银行卡可选脱敏（一期做手机号 `1[3-9]\d{9}` → 保留前 3 后 4，置中 ****）。
- 脱敏是「尽力而为」，文案告知用户不能保证覆盖所有敏感字段；关闭开关后 UI 二次确认（n-popconfirm「正文将原样发送至 {host}」）。

**Prompt 口径（要点，完整模板落 prompt.go 注释）**：

- system：固定角色「你是资深接口逆向/抓包分析助手」，输入是 HTTP 抓包记录（可能来自 App/小程序），要求：只基于提供的证据、不确定要说明、中文、Markdown；intent 必须为每个序号输出一句话意图并附文末 JSON 块（意图短句 ≤20 字、动宾结构优先如「查询…/提交…/上报…」、证据不足给「疑似…」并降 confidence、纯无语义路径判不准时 needsBody:true）；locate 必须输出文末 JSON 块（路线 A）；flowmap 用编号步骤串接口、标注依据（先后顺序/状态码/参数传递），证据不足的环节显式标注「推测」。
- 每条流以稳定序号 `[#n]` + flowId 标注，模型输出引用 `[#n]`，后端/前端据此映射跳转（match/intent 事件直接带 flowId；Markdown 中的 `#n` 一期不做内联点击，靠 match 卡片与文末接口清单）。

### 5.5 错误语义

| 场景 | 行为 |
| --- | --- |
| 未启用/无 baseURL/model | chat 返回 SSE `error` 事件（HTTP 200 先发流再报错，前端在面板内展示）或直接 400——定稿：**400 + `{error:"AI 未配置…"}`**，前端点开始时同步 await 首响应即捕获，统一错误 UI |
| key 缺失/401/403/429/5xx | Client 包装上游 message → error 事件（已开始流式后）或 4xx（首请求失败） |
| 取数为空/ids 全失效 | 400「候选流不存在或已被删除」 |
| 超时/断网 | error 事件「连接服务商超时/失败，请检查网络或代理设置」（提示上游代理走应用配置） |
| 重复并发 | 409 |
| ctx 取消（停止） | 正常关闭，不发 error；前端显示「已停止」 |

---

## 六、主窗口设置面板（前端）

新增 [AiTab.vue](../frontend/src/pages/settings/AiTab.vue)，挂到 [SettingsPanel.vue](../frontend/src/pages/SettingsPanel.vue)：

- tab 名 `ai`、标题「AI 分析」，位置：ADB 之后、解密规则之前（**全局区**，无项目也显示；不加入 `PROJECT_TABS`，`effectiveTab` 直接放行）。
- `UISettingsTabs`（server.go）增加 `"ai"`；`cli ui settings ai` 可直达。
- 表单项（n-form 暗色调，沿用 `.sec/.sec-title/.hint` 风格）：
  1. **启用 AI 分析** n-switch（`form.ai.enabled`）。
  2. **服务商预设** n-select：OpenAI / DeepSeek / 月之暗面 Kimi / 智谱 GLM / 通义千问 / Ollama(本地) / 自定义；选中后填充 baseURL + 推荐 model（可改）。预设表前端常量维护（id/baseUrl/model/docURL）。选「自定义」不回填，保留当前 baseURL/model 输入（v2.2 审计补充：custom 通常用于微调现有配置）。
  3. **接口地址 BaseURL** n-input（placeholder `https://api.deepseek.com`；hint：兼容 OpenAI 接口的任意地址，含本地 OneAPI）。
  4. **API Key** n-input type=password，showable；旁边「保存密钥」按钮——**密钥独立即时保存**：进入 tab 时 `GET /ai/config` 取 `hasApiKey/apiKeyMasked`，输入框 placeholder 显示 `已保存：••••1234（留空保存则不变）`；点保存调 `POST /ai/config`（仅 key 字段，空=不变，另有「清除密钥」按钮走 `__clear__`）。理由：不把 key 放进 SettingsView 全量表单深拷贝/保存链路，减少误覆盖与日志面。
  5. **模型** n-input + 预设 tag 快速填（如 deepseek-chat）。
  6. **温度** n-slider 0.1–2 step 0.1（默认 0.3；hint「0 视为未设置，按默认 0.3 生效」——v2.1：0 为哨兵，UI min 0.1）；**单请求超时秒** n-input-number（10–600）。
  7. **分析预算**：最大流数 n-input-number（1–100，默认 50）+ 正文预算 KB（8–256，默认 64）；hint「超过预算自动截断最近更早的记录」。
  8. **发送前脱敏** n-switch（默认开）+ 关闭时的 n-popconfirm 风险确认；hint 列举脱敏范围与「仍会发送 URL、参数名与部分正文到所选服务商」。
  9. **测试连接** n-button：调 Wails 绑定 `AITestConnection(cfg)`（主窗有绑定通道，避免密钥绕浏览器）；结果内联 n-alert（成功显示模型与延迟；失败显示上游错误原文）。测试用界面当前值（未保存也可测），密钥留空时用已存 key。
- 非密钥字段：走现有 `form`（SettingsView.ai）随面板底部「保存」统一提交；**SettingsView 增加 `AI` 字段但不含 apiKey**（Go 侧 DTO 用不含 key 的投影结构 `AISettings`，SaveSettings 合并时不动已存 key）。
- 保存校验错误沿用 SaveSettings warnings/error 通道。

**SettingsView 变化**（dto.go）：

```go
AI settings.AISettings `json:"ai"` // 无 apiKey 的配置投影（apiKey 走 /ai/config）
```

`settings.AISettings` = AIConfig 去掉 APIKey 字段（或 DTO 层匿名结构）；SaveSettings 环境半边：以入参非 key 字段覆盖 g.AI，**保留 g.AI.APIKey 原值**。

**Wails 绑定**（`internal/app/bindings_ai.go` 新文件）：

- `TestAIConnection(cfg settings.AIConfig) (AITestResult, error)`：复用 internal/ai Client；前端 AiTab 调用；同步 `wailsjs/go/app/App.d.ts`、`App.js`、`models.ts`。

---

## 七、复盘页 AI 面板（前端）

### 7.1 入口与布局

- **顶栏**（ReviewApp top-actions，刷新按钮前）：`✨ AI 分析` n-button（small，secondary）；未配置时仍可点（打开面板后显示未配置空态 + 「去主窗设置」按钮）。
- **详情标题栏**（ReviewDetail.vue，「调试重发」旁）：`AI 解读` n-button tiny；demo 模式不禁用（走本地模拟，与 compose 的 demo 禁用策略不同——AI 演示不产生外部副作用）。
- **意图摘要条**（ReviewDetail.vue 顶部、FlowDetailTabs 之上，新增 `review/ReviewIntentBar.vue`）：选中流在意图 Map 中有结果时显示一条细摘要条——`✨ {intent}`（low 置信度/needsBody 用弱化样式 + tooltip 说明），右侧「重新分析」（以单流 ids 发一次 intent 任务，原地转圈覆盖）与「完整解读」（打开 AI 面板 explain 模式）；无结果时不占位（不显示空条），仅在 AI 面板 intent tab 提供主动分析入口。
- **批量勾选**：FlowTable 新增**可选**勾选列（checkbox wi，经 `selectable`/`v-model:checkedIds` 类 props 开启；主窗 FlowList 不传=不渲染，零影响），仅 ReviewPage 开启；勾选集合作废规则（v2.1 补全）：翻页/切标签/切 scope/切关键字/改页大小/**清空（含快捷忽略导致列表内容变化）**后清空勾选。列表工具条在有勾选时显示「✨ 标注意图（N）」按钮，无勾选时 intent 范围默认当前已加载页（在面板内明示范围文案）。
- **容器**：新增 [ReviewAiPanel.vue](../frontend/src/review/ReviewAiPanel.vue)，复盘页内右侧抽屉：
  - 不依赖 naive 的 n-drawer（复盘页已全量引入 naive，可用；但抽屉层级在独立窗口内自绘 `.ai-drawer` 固定定位更可控）——**定稿：自绘 fixed 右侧 560px 滑入面板**（z-index 高于三栏，带 24px 圆角左边、遮罩可选：不设遮罩，允许边看列表边读结论）。
  - `v-model:show`；props：`api: ReviewApi`、初始 `mode`、`flowId`（explain）、候选上下文（勾选/当前视图 ids/总数/标签名/scope/关键字）。
- **意图会话缓存**：ReviewApp（或独立 `review/useIntents.ts` 模块级单例）持有 `Map<flowId, IntentResult>` 与 `analyzing: boolean`；intent 事件到达即写入（响应式），摘要条/面板列表/跳转均读它；**仅会话内存，刷新重算**（与不落库非目标一致）。
- locate 结果点击 → emit `locate(flowId)` → ReviewApp 在列表 pane 中：清除/忽略既有筛选不动，调 `listFlows` 找到该流所在（必要时按 StartedAt 翻页定位一期简化：**要求该流在当前已加载 ids 内即可选中**，点击后若当前页不含则提示「请切换到该流所在分页」——二期再做自动翻页；§十一 8）→ 设 selectedId，ReviewDetail 展示。

### 7.2 面板内容与状态

```text
┌ ✨ AI 分析 ──────────────────────────────── × ┐
│ [接口解读] [意图标注] [智能定位] [业务逻辑]     │
│ ── 意图标注 ──                                 │
│ 将批量分析已勾选 6 条（无勾选=当前视图 12 条）  │
│ ☐ 包含请求正文（判不准时可开，默认关）          │
│ [开始标注]  [停止]（互斥）        进度 3/12     │
│ ── 标注结果（点击跳转） ──                      │
│ ✨ 提交订单        POST …/orders        [#3]   │
│ ✨ 疑似查询用户信息 GET …/user/profile   [#4]   │
│ ── 智能定位 ──                                 │
│ 将分析当前视图 12 条记录（标签：登录流程排查）   │
│ [n-input type=textarea] 描述你要找的接口…       │
│ ☐ 包含请求正文  ☐ 包含响应正文（默认关/关）      │
│ [开始分析]  [停止]（互斥）        状态/预算提示  │
│ ── 结论 ──                                    │
│ 流式 Markdown（marked+DOMPurify，暗色代码块）   │
│ ── 匹配接口 ──（locate 卡片）                   │
│ #1 POST …/orders  【高】 理由…   [查看 →]       │
└───────────────────────────────────────────────┘
```

状态机：`idle → streaming(meta→delta/match*) → done|stopped|error`；切模式/换流重置；同面板一次只允许一个任务（停止后才可再开始）。

### 7.3 SSE 消费（api.ts）

ReviewApi 新增：

```ts
// 流式分析：onEvent 回调；返回 abort 函数（停止）
analyze(
  req: AiChatRequest,
  onEvent: (ev: AiChatEvent) => void,
  signal?: AbortSignal,
): Promise<void>
```

- HttpApi：`fetch('/api/v1/ai/chat', {method:'POST', headers:Bearer+json, body, signal})`；**首响应先判 ok**（400/409 等走 ApiError），200 后 `resp.body.getReader()` + TextDecoder 按 SSE 帧（`\n\n` 分帧，解析 `event:`/`data:` 行）循环回调；reader 异常/AbortError 静默收尾。
- WailsReviewApi（v2.1，主窗内嵌形态）：`analyze` 调 `AIChatStart(req)` 拿 handle → `EventsOn('ai:chat', {handle,event,data})` 订阅转发 onEvent → `signal` 触发 `AIChatStop(handle)`；并发冲突 Wails 侧以 error resolve（统一错误 UI，与 HTTP 409 同提示文案）。三实现（http/demo/wails）共用同一 `AiChatEvent` 类型。
- DemoApi：不发请求，按 mode 用内置中文模板**定时器逐段吐 delta**（explain 讲当前流、intent 为 demoData 每条按 method+path 生成一句话并逐条发 `intent` 事件（约每条 150ms）、locate 从 demoData 里按 question 关键字筛 2–3 条发 match+卡片、flowmap 输出编号步骤），约 800ms 全程；支持 abort 清定时器。
- 不把 analyze 放进 compose 那类「demo 桩抛错」——AI UI 是本版演示重点（既定：demo 模拟流）。

### 7.4 Markdown 渲染与安全

- 新增前端依赖：`marked`（轻量 MD→HTML）+ `dompurify`（XSS 净化，**必须**：AI 输出是不可信 HTML；复盘页虽在本机，服务商返回/链路污染仍可能注入脚本）。
- 统一 `src/review/ai-md.ts`：`renderMarkdown(text): string` = `DOMPurify.sanitize(marked.parse(text,{async:false}), {USE_PROFILES:{html:true}})`，禁外链脚本/事件属性；链接强制 `target="_blank" rel="noopener"`。
- 渲染目标一律 `v-html="rendered"`（仅此一处允许 v-html，输入固定走净化）；增量期间 50ms 节流重渲染，代码块暗色样式入 review.css。
- 不引入 mermaid（§十一 11）。

---

## 八、隐私与安全

1. **apiKey**：仅存 `config/settings.json`（0600，便携目录）；不经 GET 接口回传原值；不进 SettingsView、不进任何发给服务商的 Prompt、不写日志（错误信息中对 URL query 脱敏）。
2. **数据外发告知**：面板底部常驻小字「分析数据将发送至 {BaseURL host}」；首次开始分析弹一次 n-modal 告知（localStorage `prismproxy:review-ai-notice-v1` 记确认）；脱敏关闭时每次开始 popconfirm。
3. **鉴权**：AI 接口全部在 ctlapi Bearer 鉴权后（沿用既有 token 模型，无 CORS，免疫跨站）；SSE 用 fetch 头带 token，不开 query token 口子。
4. **成本闸门**：单实例并发 1；预算默认 50 流/64KB；meta 事件先回显送审规模；无后台自动分析、无预取，全部用户显式触发。
5. **停止即断**：AbortController → HTTP 连接取消 → 上游 ctx cancel，不继续计费接收。

---

## 九、mock / 浏览器预览

- DemoApi.analyze：本地模拟 SSE（§7.3，四模式：intent 逐条回填意图）。
- DemoApi.getAIConfig/saveAIConfig/testConnection：内存配置（默认 hasApiKey=true 让演示流程完整）；配置面板在主窗（不在复盘页），复盘页 demo 下「去设置」按钮隐藏（主窗不存在），改为静态提示。
- `mocks/app.ts`（主窗纯浏览器预览）：补 `TestAIConnection`、GetSettings 默认 ai 字段。

---

## 十、改动文件清单（实施时）

**后端（Go）**

- `internal/settings/settings.go`：`GlobalSettings.AI AIConfig` + `AIConfig/AISettings` 结构 + 默认值 + `ValidateEnv` 增补 + BaseURL 归一化 helper。
- `internal/ai/client.go`、`prompt.go`（新包，零外部依赖）：流式 Client（SSE 解析/首块超时/上游代理装配/错误包装）、四模式 Prompt + 裁剪 + 脱敏（纯函数）。
- `internal/ai/client_test.go`、`prompt_test.go`：SSE 帧/[DONE]/错误体/归一化；脱敏正则；预算截断（流数+字节）；空/二进制正文；locate/intent JSON 块解析（缺 seq/未知 flowId/重复结果覆盖语义）。
- `internal/ctlapi/server.go`：Service 接口 +4 方法；路由 `/api/v1/ai/config`、`/ai/test`、`/ai/chat`；SSE handler（响应头/Flush/帧编码含 `intent`/409 并发闸门/ctx 取消）；`UISettingsTabs` 加 `"ai"`。
- `internal/ctlapi/ai_bridge.go`（或并入现有接线文件）：StreamAIChat 取数（归档 RO 路径：`LoadFlowsByIDs`（新增）/`LoadFlowByID`/`LoadBody`）→ prompt.go 组装 → ai.Client 流式 → emit（intent/match 结构化事件解析转写）；fakeService（ctlapi_test.go）补 4 方法 + 路由测试（401/400/409/SSE 帧序/demo 不涉及）。
- `internal/persist/tags.go`（或新增文件）：`LoadFlowsByIDs(ids []string)`（v2.1：单条 IN 查询，StartedAt 降序）。
- `internal/app/dto.go`：SettingsView 加 `AI settings.AISettings`（无 key 投影）；GetSettings 回填（经 WithDefaults 兜底）、SaveSettings 合并不动 key。
- `internal/app/bindings_ai.go`（新）：`TestAIConnection` + **`AIChatStart`/`AIChatStop`（Wails 事件桥，v2.1）+ `GetAIConfigApp`/`SaveAIConfigApp`（主窗密钥独立存取）** Wails 绑定，全部单结构 DTO + error。
- `internal/app/`（settings 保存接线处）：`SaveAIConfig`（含哨兵语义）、`GetAIConfig`（脱敏）实现 + 全局配置即时落盘（复用 SaveGlobal，注意与运行中缓存一致性：与 SaveSettings 同路径回写 g 并持久化）。

**前端（主窗）**

- `pages/settings/AiTab.vue`（新）；`pages/SettingsPanel.vue` 挂 tab（全局区，PROJECT_TABS 不放）。
- `wailsjs/go/app/App.d.ts`、`App.js`、`models.ts`：TestAIConnection + AIConfig/AITestResult/AISettings 类型手动同步（wails build 可再生成）。
- `mocks/app.ts`：补桩。

**前端（复盘页）**

- `review/ReviewAiPanel.vue`（新）、`review/ReviewIntentBar.vue`（新，详情顶部意图摘要条）、`review/useIntents.ts`（新，会话级意图 Map 单例）、`review/ai-md.ts`（新，marked+DOMPurify 封装）。
- `review/ReviewApp.vue`：顶栏入口 + 面板挂载 + locate 跳转选中 + 勾选集合/意图缓存接线；`review/ReviewDetail.vue`：「AI 解读」按钮 + ReviewIntentBar 挂载；`review/ReviewPage.vue`（主窗内嵌承载处，如适用）同口径。
- `components/FlowTable.vue`：**可选**勾选列（默认关闭，主窗零影响）+ 勾选事件/表头全选。
- `review/api/`（目录，v2.1 修正路径——原稿 `review/api.ts` 系笔误；实际为 index/http/demo/wails/types/error 六文件）：`types.ts`（AiChatMode 含 `intent`、AiChatEvent 联合加 `intent`、IntentResult）、`http.ts`（fetch SSE 解析）、`demo.ts`（本地模拟逐条 intent）、`wails.ts`（**事件桥消费，v2.1**）三实现；业务 DTO（IntentResult 等）入 `src/lib/types.ts`。
- `frontend/package.json`：加 `marked`、`dompurify`（+ `@types/dompurify` 视版本，dompurify 3 自带类型）。

**文档**

- 本文档；[CONTEXT.md](../CONTEXT.md)/doc/项目记忆.md（实施后沉淀红线）；[开发进度.md](./开发进度.md) M13；[README.md](../README.md) 功能特性/文档表；可选 [AI-CLI使用说明.md](./AI-CLI使用说明.md)（若做 §十一 13）。

---

## 十一、待拍板点（建议默认值已给出）

1. **配置归属**：全局唯一一份（建议）。备选：按项目各一份（不同 App 不同 key 场景），但配置复杂度翻倍、复盘跨项目语义混乱，不推荐。
2. **协议范围**：一期仅 OpenAI 兼容 Chat Completions + stream（建议）。Anthropic/Gemini 原生协议不做。
3. **流式形态**：后端转写自定义 SSE（delta/match/intent/error/done），前端 fetch ReadableStream（建议；EventSource 带不了 Bearer 头）。
4. **apiKey 存取**：独立 `/ai/config` GET 脱敏/POST 哨兵语义，不进 SettingsView（建议）。备选：随 SettingsView 明文往返——省一个接口但扩大密钥暴露面，不推荐。
5. **温度默认 0.3、预算默认 50 流/64KB、超时 120s**（建议；可在设置调）。
6. **locate 结构化结果**：路线 A 单轮 + 文末 JSON 块解析（一期，建议）；路线 B tool-calling 二期。
7. **intent 形态（本期新增）**：
   - 批量**单次请求**标整批（非每流一次），结果经 `intent` 事件逐条回填（建议，控成本+进度可见）；备选逐条调用，简单但 N 倍费用与延迟，不推荐。
   - 默认仅元数据+关键头、不带正文（建议）；判不准由模型置 `needsBody:true`，用户手动开正文重析；不做服务端自动二轮。
   - 意图只存前端会话 Map、不入库（与非目标一致）；摘要条只在有结果时出现。
   - 勾选列为 FlowTable 可选能力，仅复盘开启（建议）；一期无勾选时默认当前已加载页，不做「全部匹配项」服务端扩选（locate/flowmap 的扩选承诺不变）。
8. **locate 卡片跨页跳转**：一期仅当前已加载页可选中（候选本就来自当前视图 ids，正常使用都在范围内）；自动翻页定位二期。
9. **入口范围**：一期复盘页入口（顶栏/详情按钮/详情意图摘要条/批量标注按钮）+ 主窗设置；主窗实时列表不做 AI（建议）。
10. **结果持久化**：一期不落库、不重放，会话内查看（建议）。备选：新增 `ai_notes` 表存结论（写扩散/存储与重做成本，二期视反馈）。
11. **Demo 策略**：AI 分析 demo 本地模拟可玩（建议，与 compose 禁用相反，因无外部副作用且是 UI 演示重点）。
12. **图表**：一期 Markdown（编号步骤表达时序），不引 mermaid（建议，减依赖与渲染风险）；二期可加 mermaid + 净化白名单。
13. **匹配 Markdown 内联跳转**：一期点 match 卡片跳转；正文 `[#n]` 内联链接二期。
14. **AI CLI**：一期不加 `cli ai`（建议）；headless 用户直接编辑 settings.json 或经 `/api/v1/settings`；`cli ui settings ai` 随 tab 白名单自然支持（GUI 在线时）。
15. **推理模型 reasoning**：协议预留 `reason` 事件但一期 UI 不分区展示（如选 deepseek-reasoner 思考内容忽略，仅出正文）。
16. **测试连接落库**：测试始终用界面当前值、不触发保存（建议），避免「测试=改配置」误解。

---

## 十二、验收标准（草案）

- **AC1 配置闭环**：主窗设置 → AI 分析 tab（无项目时也可见）：选 DeepSeek 预设自动填 baseURL/model；保存非密钥字段后重启配置仍在；密钥保存后重开面板显示 `••••1234`，留空再保存不覆盖原 key，「清除密钥」生效。
- **AC2 测试连接**：正确 key 测试成功显示延迟；错误 key 返回上游 401 原文；错误 baseURL/超时给出可读错误；未落盘的临时值也能测试。
- **AC3 未配置引导**：清空 key/禁用后，复盘页点「AI 分析」显示空态与「去主窗设置」；该按钮经 `/ui/settings {tab:"ai"}` 唤起主窗并定位 tab（GUI）；headless 下返回 ui:false 提示。
- **AC4 接口解读**：对一条 JSON 接口点「AI 解读」，流式逐字输出 Markdown，内容含功能/参数含义/调用时机；含正文截断标注（大响应体）与二进制体跳过。
- **AC5 意图批量标注**：勾选 6 条（或无勾选取当前已加载页）发起 intent：面板进度逐条推进（n/total），每条回填一句话意图；标注后切换选中流，详情顶部摘要条即时显示对应意图；「重新分析」覆盖旧结果、「完整解读」打开 explain；needsBody/low 置信度有弱样式与提示；默认请求不含正文（抓包核对上游 payload 仅元数据+关键头）。
- **AC6 智能定位**：当前视图 10+ 条流中按自然语言描述，结论给出 ≥1 张匹配卡片（方法/URL/理由/置信度），点「查看」列表选中并展示详情；候选超 50 条时 meta 提示截断。
- **AC7 业务逻辑整理**：登录流程标签下输出编号步骤链路，引用接口与记录顺序/参数传递关系；证据不足处显式标注「推测」。
- **AC8 流式与停止**：输出打字机效果无明显卡顿（节流渲染）；点停止立即中断（后端上游连接取消，无后续 delta/intent）；停止后可重新开始。
- **AC9 并发闸门**：分析进行中再次发起得到 409 友好提示；关闭面板/浏览器后服务端任务随即取消。
- **AC10 脱敏默认开**：默认配置下抓包中的 Authorization/Cookie/JWT/密码键在发往服务商的 Prompt 中为 [REDACTED]/\*\*\*（单测固定报文断言）；关闭脱敏有二次确认且选择被记住。
- **AC11 预算截断**：构造 60 流/超 64KB 场景：仅发送最近 50 流且 meta.truncated=true、正文出现截断标注；调大预算后全量发送。
- **AC12 XSS 安全**：服务商返回含 `<script>`/`onerror=` 的 Markdown，净化后不执行、不报错中断渲染。
- **AC13 错误面**：断网/错误代理/429/500/空候选/ids 全失效均有明确中文提示，面板不崩、应用不崩。
- **AC14 Demo 可用**：无 token 浏览器预览（demo）下四种模式均可完整走一遍（模拟流式 + intent 逐条回填 + locate 卡片点击），不发任何真实网络请求。
- **AC15 项目隔离不受影响**：AI 配置为全局；切项目后设置一致；分析只读当前项目归档库，A 项目流 id 在 B 项目查不到时按失效处理。
- **AC16 构建红线**：`go test ./...`、`go vet`、`npm run build` 全绿；dist grep 到 AI 面板特征字符串；主窗与复盘页资源无 404（FlowTable 主窗无勾选列）；增量 `wails build` 成功；wailsjs 三个生成文件同步无脏 diff。
