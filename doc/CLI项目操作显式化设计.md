# CLI 项目操作显式化设计（移除 project switch，--project 必填）

> 状态：**已实施（2026-09-19）** · 评审与实施记录见文末及 memory/进度记忆.md
> 关联：方案.md §4.12（ctlapi）、项目配置设计.md §6.3（CLI 目标项目规则）、AI-CLI使用说明.md

## 1. 背景与动机

当前 CLI 的项目类操作存在"隐式当前项目"语义：

- `project switch <id|名称>` 可在运行中热切换全局当前项目（gcfg.CurrentProject + 引擎当前项目）；
- `rules` / `settings` / `domains` 类命令的 `--project` **可选**，缺省 = 当前项目。

在**多个 AI agent / 多个 CLI 会话同时操作同一实例**的场景下互相踩踏：

1. AI-A `project switch 项目A` 后，AI-B 紧接着的 `rules ignore ...`（未带 --project）会写到项目 A——B 以为在操作自己以为的"当前项目"，实际随 A 的切换漂移；
2. switch 还会清空内存流量列表并载入新项目历史，A 正在读取的流量列表瞬间被换掉；
3. 时序依赖全局态，命令结果不可预测、不可重放。

## 2. 目标与非目标

**目标**

- G1 CLI 移除 `project switch`（以及同为全局态变更的 `project close`）。
- G2 所有涉及**项目配置**的 CLI 操作（rules / settings / domains）必须显式 `--project/-P` 指定目标项目，缺省报用法错误。
- G3 CLI 永远不改变"当前项目"这一全局态：任何 CLI 命令执行前后，`gcfg.CurrentProject` 与引擎当前项目不变。
- G4 服务端（HTTP 层）同步收紧，防止绕过 CLI 直接调 HTTP 时不带 project。
- G5 GUI 功能完全不受影响。

**非目标（本设计不做）**

- 不给 `flows` / `compose` / `watch` 等运行态命令增加按项目读取历史的能力（流量列表绑定引擎当前项目，见 §7 边界）。
- 不增加多实例管理/启动参数 `-project`（列为后续可选项，见 §7.2）。
- 不改变 GUI 的任何交互与 Wails binding 语义。

## 3. 核心原则与不变量

**不变量 I1**：当前项目（gcfg.CurrentProject / a.proj）的唯一变更入口是 GUI（顶栏切换器、欢迎页打开项目）。

**不变量 I2**：CLI / HTTP 的 rules、settings、domains 写操作必须携带显式目标项目；目标 = 当前项目时仍走"内存修改 + 落盘 + 引擎热重建"（立即生效，这不是切换）；目标 ≠ 当前项目时只改该项目文件（现状语义保留）。

**不变量 I3**：实例运行态命令（status / flows / proxy / sysproxy / adb / ca / compose / ui / tags / ai / processes）语义不变，作用于引擎当前项目与全局环境。

## 4. 语义变更总表

| 命令 / 端点 | 旧行为 | 新行为 |
|---|---|---|
| `project switch <id\|名称>` / POST /projects action=switch | 运行中热切换当前项目 | **移除**。CLI exit 2 / HTTP 400，返回引导文案（§6） |
| `project close` / action=close | 关闭当前项目进欢迎页（全局态变更） | **移除**。同上 |
| `project create <名称> [--from]` / action=create | 创建并**自动切换** | 创建但**不切换**；返回新项目 meta。GUI 的 Wails binding `CreateProject` 保持"创建并切换"不变 |
| `project rename <id\|名称> <新名>` | 保留 | 保留（仅改名，不改当前项目归属；改当前项目名会 emitProjectChanged 刷顶栏，允许） |
| `project delete <id\|名称>` | 可删当前项目（自动打开剩余首个） | **拒绝删当前项目**（bridge 层校验，HTTP 400）；无打开项目（欢迎页态）时可删任意项目 |
| `project list` | 保留 | 保留（只读；返回值含 currentProject 供参考） |
| `rules list/ignore/group/decrypt/export/import` | `--project` 可选，空=当前项目 | `--project` **必填**；CLI 缺省 exit 2，HTTP 缺 project query 返回 400 |
| `settings get/set` | 同上 | 同上（M11"无项目态免 project 保存环境字段"分支随服务端必填校验成为死代码，删除） |
| `domains list/get/save/delete/import` | 同上 | 同上 |
| `--project` 指向当前项目 | 内存+落盘+热重建 | **不变**（I2） |
| `--project` 指向非当前项目 | 只改该文件，不热切换 | **不变**（I2） |

## 5. 分层设计

### 5.1 CLI 层（internal/ctlapi/cli.go）

1. `RunCLI` 增加 rules/settings/domains 的 `--project/-P` 必填校验。实施修订（2026-09-19，代码审计）：校验**前移到 `newClient` 之前**、只依赖 flag 局部变量——实例离线（无 endpoint 文件）时同一用法错误也稳定返回 exit 2 用法引导，而非 exit 1 连接错误，与 `project switch|close` 前置拦截口径一致：

```go
target := *projectFlag
if target == "" {
    target = *projectShort
}
if (cmd == "rules" || cmd == "settings" || cmd == "domains") && target == "" {
    fmt.Fprintln(os.Stderr, "错误: rules/settings/domains 命令须用 --project/-P 显式指定目标项目（CLI 不再默认作用于当前项目，防止多会话互相干扰；project list 可查看可用项目）")
    return 2
}

c, err := newClient(configDir, *addrFlag, *tokenFlag) // 校验通过后再建连接
if err != nil {
    fmt.Fprintln(os.Stderr, "错误:", err)
    return 1
}
c.project = target
```

2. `cliProject`：删除 `case "switch"` / `case "close"` 分支，新增引导性报错：

```go
case "switch", "close":
    return nil, fmt.Errorf("CLI 已移除 project %s：切换/关闭项目请在 GUI 顶栏操作；CLI 的规则与配置操作请用 --project/-P 指定目标项目", sub)
```

3. `cliProject` 的 `create` 分支调用不变（服务端语义已变为不切换），帮助文本更新。
4. `cliUsageText` 更新：删除 `project switch` / `project close` 行；`project create` 描述改为"新建项目（不切换，当前项目仅 GUI 可变更）"；全局参数 `--project/-P` 说明改为"rules/settings/domains 类命令**必填**"；示例区同步。

### 5.2 HTTP 服务端（internal/ctlapi/server.go）

1. `handleProjects`：删除 `case "switch"` / `case "close"`，default 文案改为 `action 须为 create|rename|delete`；对 `switch|close` 返回 400 + 引导文案（与 CLI 同口径，直接调 HTTP 的外部调用方也能得到明确提示）。
2. rules / settings / domains 系列 handler（handleRules、handleRulesExport、handleRulesImport、handleRulesGroupEnabled、handleSettings、handleDomains）入口统一校验：

```go
project := r.URL.Query().Get("project")
if strings.TrimSpace(project) == "" {
    writeErr(w, http.StatusBadRequest, "缺少 project 参数（目标项目 id|名称；CLI 不再默认当前项目）")
    return
}
```

3. 抽一个小助手 `requireProject(r) (string, bool)`（第二个返回值为是否已写出错误响应），避免六处复制。

### 5.3 桥接层（internal/app/ctl_bridge.go）

1. `ctlService.CreateProject`：改调 app 层新拆出的"只创建不切换"函数（§5.4），不再触发切换。
2. `ctlService.DeleteProject`：**resolve → 拒删当前项目校验 → 删除全程持 projMu**（同一临界区，消除"校验后、删除前 GUI 恰好切到目标项目"的 TOCTOU 窗口；删除实现复用 app 层 `deleteProjectLocked`，勿在持锁状态下调用会自行加锁的 `App.DeleteProject`——Mutex 非重入）：

```go
a.projMu.Lock()
defer a.projMu.Unlock()
id, err := a.resolveProjectIDLocked(idOrName)
if err != nil {
    return err
}
if a.proj != nil && id == a.proj.ID {
    return fmt.Errorf("不能通过 CLI 删除当前项目 %q（删除当前项目会触发自动切换，请在 GUI 中操作；或先在 GUI 切到其他项目）", id)
}
return a.deleteProjectLocked(id)
```

3. `ctlService.SwitchProject` / `CloseProject`：删除（Service 接口同步删方法），编译期断言 `var _ ctlapi.Service = (*ctlService)(nil)` 保证不漏。
4. `SaveSettings` 桥接中 `project == "" && curID == ""` 的无项目态放行分支：服务端已强制必填，该分支成为死代码 → 删除，简化函数。
5. `resolveProjectIDLocked` 的空串分支保留不动（GUI binding 路径仍依赖"空=当前项目"语义，如 rules 相关 Wails binding）。

### 5.4 app 层（internal/app/bindings_projects.go）

拆分"创建"与"切换"：

```go
// createProjectNoSwitch 新建项目（清单登记 + 目录 + 落盘 + 失败回滚），不切换。
func (a *App) createProjectNoSwitch(name, fromID string) (settings.ProjectMeta, error) { /* 现 CreateProject 前半段原样搬入 */ }

// CreateProject GUI 语义不变：创建并自动切换（Wails binding，前端零改动）。
func (a *App) CreateProject(name, fromID string) (settings.ProjectMeta, error) {
    meta, err := a.createProjectNoSwitch(name, fromID)
    if err != nil {
        return settings.ProjectMeta{}, err
    }
    if serr := a.switchProjectLocked(meta.ID); serr != nil {
        return meta, fmt.Errorf("项目已创建，但切换失败: %w", serr)
    }
    return meta, nil
}
```

- `SwitchProject` / `CloseProject` / `DeleteProject`（GUI binding）签名与语义**全部不动**。
- 实施修订（2026-09-19，审计发现）：从 `App.DeleteProject` 拆出 `deleteProjectLocked(id)`（调用方持 projMu 的删除实现），`App.DeleteProject` 变为持锁薄壳；桥接层据此在同一临界区内完成 §5.3 第 2 条的"校验 + 删除"。
- ctlapi.Service 接口（server.go L47-52）：删除 `SwitchProject` / `CloseProject` 两行。

## 6. 报错文案规范（统一口径）

| 场景 | 退出码/HTTP | 文案要点 |
|---|---|---|
| CLI 缺 --project | exit 2 | 点名三类命令须显式指定 + 提示 `project list` 可查项目 |
| `project switch` / `project close` | exit 2 / 400 | "CLI 已移除…请在 GUI 顶栏操作" + "--project 指定目标项目" |
| 删除当前项目 | 400（CLI 端收到后 exit 1） | "不能通过 CLI 删除当前项目…请在 GUI 中操作" |
| HTTP 缺 project query | 400 | "缺少 project 参数（目标项目 id\|名称）" |

## 7. 已知边界与后续可选项

### 7.1 边界（如实告知 AI / 用户）

1. **流量归属不随 CLI 变**：`flows list/watch`、`compose` 产生/读取的流量属于**引擎当前项目**（由 GUI 决定）。CLI 预配置其他项目规则后，需 GUI 切到该项目再抓包。
2. **headless 模式无法切换项目**：headless 实例的当前项目 = 启动时恢复的 gcfg.CurrentProject，且无 GUI 可切。headless 下 CLI 仍可写任意项目的规则/域名组/设置（文件级），但抓包始终在启动项目。需要"多项目并行抓包"时的正解是**启动多个实例**（见 §7.2）。
3. **rename 当前项目**：允许（只改名不换项目），GUI 顶栏经 project:changed 事件刷新显示。

### 7.2 后续可选项（本设计不实施）

- 启动参数 `-project <id|名称>`：headless 启动时直接打开指定项目，配合多实例（各占一个 listenAddr）实现多项目并行抓包。涉及 endpoint/config 目录隔离问题，需单独设计。
- `flows list --project <id>`：跨项目读历史库（persist 查询），供 AI 复盘非当前项目流量。需只读打开该项目 prism.db，亦单独设计。

## 8. 兼容性影响（Breaking Changes）

面向 AI agent / 脚本的破坏性变更，需在 AI-CLI使用说明.md 顶部显著标注：

1. `project switch` / `project close` 命令移除 → 旧脚本收到 exit 2 + 引导文案。
2. `rules` / `settings` / `domains` 全部子命令从"缺省当前项目"变为"必填 --project"→ 旧脚本收到 exit 2（HTTP 直调收 400）。
3. `project create` 不再切换 → 依赖"创建后即抓新项目"的脚本需改为 GUI 切换或多实例方案。
4. `project delete` 不能删当前项目 → 依赖"删当前自动打开下一个"的脚本需 GUI 操作。
5. HTTP API 同步收紧（POST /projects 的 switch/close action、rules/settings/domains 的 project query 必填）。

无数据迁移：全部为运行时校验收紧，配置文件格式、目录结构零变化。

## 9. 改动文件清单

| 文件 | 改动 |
|---|---|
| internal/ctlapi/cli.go | RunCLI 必填校验；cliProject 删 switch/close；usage 文本全面更新 |
| internal/ctlapi/server.go | Service 接口删 SwitchProject/CloseProject；handleProjects 删两 action + 引导文案；rules/settings/domains handler 必填校验（requireProject 助手） |
| internal/ctlapi/ctlapi_test.go | fakeService 同步接口；新增/调整用例（§10） |
| internal/app/ctl_bridge.go | 删 SwitchProject/CloseProject 适配；CreateProject 改不切换；DeleteProject 拒当前项目；SaveSettings 删死分支 |
| internal/app/bindings_projects.go | 拆 createProjectNoSwitch；GUI CreateProject 保持自动切换 |
| doc/AI-CLI使用说明.md | 顶部 breaking change 提示；§2 命令总览；§3.3/3.5/3.8/3.11 示例与说明；§4 集成链路 |
| doc/方案.md §4.12 / doc/项目配置设计.md §6.3 | 补记本设计变更（链接本文档） |
| PrismProxy/memory/*（进度/教训/后端代码记忆） | 按记忆规则同步 |

## 10. 测试计划

单元（`go test ./internal/...`）：

1. ctlapi_test.go：handleProjects switch/close → 400 + 文案；create 响应不含切换语义；/rules、/settings、/domains 无 project query → 400。
2. CLI 侧（现有 cli_*_test 模式）：`rules list`（无 --project）→ exit 2；`rules list -P 项目` → 正常；`project switch x` → exit 2 + 引导；`project create` 请求体不含切换；`project delete 当前项目` → 400 透传。
3. 回归：GUI binding CreateProject 自动切换、SwitchProject/DeleteProject 语义不变的既有测试保持全绿。
4. TOCTOU 回归（app_m9_test.go）：TestCtlDeleteProjectRejectsBecameCurrent（CLI 被锁阻塞期间目标被切为当前项目 → 必拒绝）；TestCtlDeleteProjectConcurrentSwitch（当前项目高频翻转 × CLI 循环删除，结果只能是拒绝或"删除成功且代际不推进"）。

手工实测（prism-cli.exe）：

- `project list` 正常；`project switch` 引导报错；`rules ignore host x.com`（缺 --project）exit 2；`-P <当前项目>` 立即生效（GUI 流量被忽略）；`-P <非当前项目>` 只落盘；GUI 顶栏切换不受任何影响。

## 11. 决策点（2026-09-19 用户已拍板，全部采纳推荐答案）

1. `project close` 一并移除？——**是**。它同样改变全局态，与 G3 冲突。
2. `project create` 保留但不切换？——**是**。创建是无害操作，去掉自动切换即可满足 G3。
3. `project delete` 拒删当前项目？——**是**。删当前项目会"自动打开剩余首个"，属隐藏切换。
4. `--project` 指向当前项目时仍立即热生效？——**是**。这是"显式定位"不是切换，且 GUI 并发抓包时预配当前项目是合理需求。

> 状态更新：方案已确认并实施完成（2026-09-19）。实施含一处审计修订：§5.3 DeleteProject 改为全程持锁（TOCTOU 修复），见 §5.3/§5.4 与 §10 第 4 条。二次代码审计修订（2026-09-19）：§5.1 缺 `--project` 校验前移至 `newClient` 之前（实例离线时仍稳定 exit 2）；§5.3 `ctlService.CreateProject` 的 from 解析与创建合并为单临界区。
