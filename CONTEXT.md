# PrismProxy 项目记忆（CONTEXT）

> 跨任务协同的关键记忆，只记非显而易见的约定与架构决策。开发进度不记在这里。

## 项目概览
- PrismProxy：Windows 桌面级 HTTP(S) 调试代理（MITM 解密 + 抓包看包），单文件 exe。
- 技术栈：Go 1.27 后端（代理引擎，核心零外部依赖）+ Vue 3 + TS + Naive UI + Pinia + Vite 前端；Wails v2 桥接（WebView2）。
- 端口：代理默认 9090，控制 API（ctlapi）9595。
- 前端目录：`frontend/src`（pages/、components/、stores/、composables/）；Wails 绑定在 `frontend/wailsjs/`，前端引用路径为 `../wailsjs/...`（pages 下）或 `../../wailsjs/...`（components 下）。

## 架构关键约定
- **前后端通信**：Go 方法经 Wails Bindings（`wailsjs/go/app/App`）调用；后端推送经 `EventsOn`（`wailsjs/runtime/runtime`）。关键事件：
  - `proxy:start-error`（启动失败，消息文本）、`proxy:ready`（startup 自动启动/接管完成，触发底栏状态刷新）、`ui:open-settings`（AI CLI `cli ui settings [tab]` 驱动打开设置抽屉，tab 如 `general`/`network`/`decrypt`）。
- **项目隔离（M9/M11）**：规则/历史按项目隔离；`useProjects` composable 统一管理项目状态与 `project:changed` 事件订阅（订阅不依赖组件生命周期）。无打开项目时显示 `WelcomePage`，主界面（含头/底栏）不挂载。
- **流量 store**：`useFlowsStore`（pinia）持有 flows/filter/paused，提供 `init()`、`clear()`、`filtered`；过滤支持关键字/正则（非法正则降级子串）/方法/状态码多选（空串哨兵=全部）。
- **代理/系统代理状态**：仅前端 UI 态，由底栏组件自持（见下）；停止监听前若系统代理已接管需先 `SetSystemProxy(false)`，否则系统流量全断。

## 前端组件结构（2026-09 重构后）
- `App.vue`：纯布局编排（WelcomePage / AppHeader / 内容区 NSplit[FlowList+FlowDetail] / AppFooter / SettingsPanel / Composer）+ 错误条（startError）+ 设置抽屉开关（showSettings/settingsTab）。
- `components/AppHeader.vue`：顶栏 40px。ProjectSwitcher、流计数/暂停刷新/清空列表按钮、GitHub 外链（Wails 用 `runtime.BrowserOpenURL`，浏览器降级 `window.open`）。无 props/emits，直接用 store。
- `components/AppFooter.vue`：底栏 28px。过滤栏（关键字/正则/方法/状态）、代理状态标签、监听开关、系统代理开关、设置按钮。
  - 自持状态：`status`(GetProxyStatus)、`sysState`(GetSystemProxyStatus)、`showSysSwitch`(GetSettings)。
  - **defineExpose 两个刷新方法**：`refreshStatus()` 只查代理/系统代理状态（allSettled 并发两查询，含 StartError 兜底上报），用于热路径（开关动作 finally、`proxy:ready`/`proxy:start-error` 事件后）；`refresh()` = `refreshStatus()` + `refreshPrefs()`（GetSettings 读工具栏开关可见性），仅挂载时与设置保存后调用——避免开关热路径多一次 GetSettings 往返。
  - **StartError 去重**：footer 内 `lastStartError` 记录已上报的后端启动错误原文，同一错误只 emit 一次（用户手动关闭错误条后轮询/事件刷新不会复现旧错误）；错误文本变化（新的一次启动失败）或启动成功后端清空 StartError 后才允许重新上报。
  - emits：`open-settings(tab)`（tab=`network` 点状态标签 / `general` 点设置按钮，App 侧统一走 onUIOpenSettings，含 WindowUnminimise）；`error(message, replace?)`（replace=true 为用户开关动作刚失败→App 侧覆盖显示最新错误；否则为 StartError 轮询兜底→仅错误条为空时恢复）；`clear-error`（StartProxy 启动成功后发出，App 侧清空 startError，等价原版 startError=''）。
- 注意：AppFooter 仅在有打开项目时挂载，欢迎页阶段 `footerRef` 为 null（refreshFooter 已用可选链兜底）。
- 错误条（startError）归属 App.vue：内容区错误条的「打开设置」按钮保持原版行为 `showSettings = true`（不重置当前 settingsTab）。

## 数据复盘页（M12/M12.1）
- 独立 Vite 多页入口 `review.html` + `src/review/`（独立 createApp，不依赖 Wails runtime），经 ctlapi（127.0.0.1:9595，Bearer token，静态 dist 不鉴权、`/api/` 鉴权）由系统浏览器开独立窗口；Wails v2 无多窗口能力。**Vite base 保持默认 `/` 是红线**（改 `/review/` 主窗 404 白屏）。
- 数据层 `review/api.ts`：HttpApi（fetch Bearer）+ DemoApi（无 token/?token=mock）双实现，任何新接口两端都要补；DTO 形态：TagInfo/HistBucket 小写 json、FlowMeta/FlowDetail 大写字段。
- M12.1：列表/直方图查 `scope=archived|all`（默认 archived=打标流；具体标签恒归档，服务端宽容忽略 scope）+ `start/end` unix 毫秒含头尾半开（0=不限）；`/api/v1/tags` 根级 `total`（DISTINCT 去重，前端禁止 count 累加）/`totalFlows`；`GET /tags/{id}/histogram`（buckets 默认 120 上限 500），底图随 tagID+scope 重拉但**恒全域分桶不随窗口变焦**。ReviewTimeline.vue 纯 SVG + pointer 手势状态机（4px 阈值/选区三分区 ±6px/最小 2 桶），拖拽中不刷列表；ReviewApp 用 flowSeq/histSeq 双代际防护。
- ReviewApp 三栏（sidebar 220px / list 46% / detail flex1）支持拖拽调宽：`.splitter` 6px 分隔条 ×2，宽度状态 sidebarW/listW（px，listW 挂载后按 46% 换算，listW=0 时 CSS 46% 兜底；fatal 重试成功后 refreshAll 内 nextTick+initListW 补测）；window 级 pointermove + setPointerCapture，约束 SIDEBAR_MIN 150/侧栏≤45% 总宽/LIST_MIN 320/DETAIL_MIN 360；时间轴 ResizeObserver 随容器宽自动重测桶数，无需手动处理。
- **复盘页全屏高度链路**：NConfigProvider 默认 `abstract=false` 会渲染真实 `<div class="n-config-provider">`（NDialogProvider 是 Fragment、NMessageProvider 仅 teleport 消息到 body，均不占布局），它插在 `#app` 与 `.review-root` 之间；`review.css` 必须给 `.n-config-provider { height:100% }`，否则 `.review-root{height:100%}` 参照 auto 高度，整页只占内容自然高度、下方大片空白。

## 构建与验证
- 前端：`frontend/` 下 `npm run build`（vite build；无独立 type-check 脚本，可用 IDE 诊断）。
- 代码风格：注释为中文，Naive UI 组件按需 import；scoped 样式，深度选择器用 `:deep()`。
