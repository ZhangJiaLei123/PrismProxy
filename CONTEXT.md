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
- **列表分页与关键字（2026-09-09 改）**：列表是 **n-pagination 页码分页**（非无限滚动），page 1 起、pageSize 默认 100 可选 [50,100,200,500]，`limit/offset` 传服务端（limit 默认 200 上限 1000）；切标签/scope/时间窗/关键字/页大小统一走 `loadFlows(true)`（reset 分支强制 page=1），翻页走 onPageChange（不清屏、保留旧行到新响应）。关键字 `q` **下沉服务端**：`buildFlowQuery(tagID,scope,start,end,q)` 单一谓词构造点，列表与 CountFlows(total) 同口径——`method/host/path` 三列 `LIKE ? ESCAPE '\'` OR（整体括号化），`likePattern` 转义 `\ % _`、TrimSpace 空串不过滤；**直方图显式传 q=""，底图不随关键字过滤**；q 不匹配 Tags。HTTP `GET /tags/{id}/flows` 增 `q` 参数（handler 内 url.Values 局部变量改名 params 避免与关键字 q 混淆，原样 URL 解码透传不裁长度）；ReviewApi.listFlows 七参（…,limit,offset,q），HttpApi 与 DemoApi 必须同口径（Demo 三列小写子串、不含 Tags、limit/offset 钳制、响应回显 q）。前端关键字 watch 防抖 300ms。
- **分页改造代码审查（2026-09-09，2 项 minor 已修）**：①**空态关键字分支**——服务端 q 无匹配返 flows=[]/total=0，旧空态四分支不判 keyword 会谎报「该标签下暂无流/库内为空」；现空态在 winStart 分支后插 `v-else-if="keyword.trim()"` 分支（🔍 没有匹配「kw」的流量 +「清除关键字」按钮，clearKeyword 取消挂起防抖定时器后清空、由 keyword watcher 统一重拉，避免双请求）。②**失败回滚**——`loadFlows(reset, restoreOnError=false)` 第三参：普通失败（非 fatal 遮罩）时仅调用方要求才恢复快照 prevFlows/prevTotal；**只有关键字 watcher 与 onPageSizeChange 传 true**（筛选维度本身未变），切标签/scope/时间窗等默认 false——跨维度恢复会显示与当前选中维度不符的脏数据。onPageChange/onPageSizeChange 失败分别回滚 page/pageSize（均以 `!fatal.value` 为前提，致命遮罩由重试统一处理）。
- ReviewApp 三栏（sidebar 220px / list 46% / detail flex1）支持拖拽调宽：`.splitter` 6px 分隔条 ×2，宽度状态 sidebarW/listW（px，listW 挂载后按 46% 换算，listW=0 时 CSS 46% 兜底；fatal 重试成功后 refreshAll 内 nextTick+initListW 补测）；window 级 pointermove + setPointerCapture，约束 SIDEBAR_MIN 150/侧栏≤45% 总宽/LIST_MIN 320/DETAIL_MIN 360；时间轴 ResizeObserver 随容器宽自动重测桶数，无需手动处理。
- **M12.2 忽略名单与列表排序（2026-09-09）**：`review_ignores` 表（kind∈host/path/proc，PK(kind,value)），**忽略=查询排除隐藏，不删 flows 数据**；单一谓词点仍是 `buildFlowQuery`（reviewQueryOpts{Q,SortKey,SortDir,ShowIgnored}），!ShowIgnored 且名单非空时拼 NOT 排除；Histogram 显式空 opts 不联动。host 四 LIKE（含端口/子域，`%.host` 跨点）、path 精确+下级前缀、proc 走 `json_extract(data,'$.Process.Name')` LOWER 等值；**SQLite BINARY 排序按字节，文本列 ORDER BY 必须包 LOWER()**（flowOrderBy：time 默认 desc、其余 asc，同值 StartedAt desc 兜底）。归一化 NormalizeIgnore*：host 剥端口（仅 :后纯数字）/小写/去尾点去`*.`、path 截 ?# 补前导 /（`/` 拒绝）、proc trim；AddReviewIgnore 先 SELECT 后 INSERT/UPDATE（同毫秒幂等）。读走 reviewReader()（旧归档库可能无表须容错），写走 acquireArchive()+projGen 代际校验。
- **M12.2 HTTP 契约**：`/api/v1/tags/ignores`（ServeMux 精确模式，须显式注册；path 非精确委托 handleTagSub，parts[0]=="ignores" 防御 404）：GET→`{ignores:[{kind,value,createdAt,note}]}`、POST `{kind,value,note}`→`{ignore,added}`（幂等）、DELETE `?kind=&value=`（value 可含 "/" 走 query，不放进 path）→`{deleted}`；`GET /tags/{id}/flows` 增 `sort`（time|method|status|host|path|size|proc）/`dir`（asc|desc）/`showIgnored`（1|true 为真），响应回显。ctlapi Service 接口保持 HTTP 原语参数（不依赖 persist 包），由 ctl_bridge 组装 persist.ReviewListOpts；**接口加方法必须同步 ctlapi_test.go fakeService**（`var _ ctlapi.Service` 编译期断言）与 `frontend/wailsjs/` 三个生成文件（App.d.ts/App.js/models.ts，Go 结构体无 json tag→models.ts 字段大写 Q/SortKey/...），即使 review 页走 HTTP 不调绑定也要手动同步防脏 diff。
- **M12.2 前端**：ReviewApi 新 listIgnores/addIgnore/removeIgnore + listFlows 末参 ListFlowsOpts（HttpApi/DemoApi 必须同口径：DemoApi 内存三层 Map 复刻归一化/命中/LOWER 排序/眼睛过滤）。ReviewApp 7 列（增进程 fr-proc），表头 `.flow-head` sticky（底色 #101014 不透明）点击排序；右键仅 fr-host/fr-path/fr-proc 三列出菜单项（NDropdown trigger=manual + clientX/Y，CONNECT/空路径与空 ProcessName 禁用），addIgnore 成功后同步本地 ignores 数组（角标实时），隐藏态才 loadFlows(true,true) 重算（显示态新忽略项仍可见，不重拉）；**顶栏小眼睛=忽略名单管理面板**（2026-09-09 改，取代直接点击切换）：n-popover raw 自绘暗卡 `.ignore-panel`（320px/max 60vh），列出全部忽略项（k-host/k-path/k-proc 彩色标签，host→path→proc 再按值排序）+ 每项垃圾桶 removeIgnore（removingKey 单条 loading，成功本地剔除并在隐藏态重拉），右上 n-switch「显示被忽略流量」承载原 showIgnored 切换（默认 false，localStorage `prismproxy:review-show-ignored-v1` 持久化，失败回滚 UI+存储）；触发器 `.eye-wrap` 带 `.eye-dot` 规则数角标，有规则/面板打开/显示态时紫色高亮。ignores 项目级、与标签/scope 无关，面板每次打开 loadIgnores 刷新，refreshAll 用 Promise.all 并行首拉；loadIgnores/removeIgnore 失败仅 message 不 fatal。侧栏计数/直方图不随忽略联动，仅列表+CountFlows。

- **复盘页全屏高度链路**：NConfigProvider 默认 `abstract=false` 会渲染真实 `<div class="n-config-provider">`（NDialogProvider 是 Fragment、NMessageProvider 仅 teleport 消息到 body，均不占布局），它插在 `#app` 与 `.review-root` 之间；`review.css` 必须给 `.n-config-provider { height:100% }`，否则 `.review-root{height:100%}` 参照 auto 高度，整页只占内容自然高度、下方大片空白。
- **详情展示层复用 + 复盘页调试重发（2026-09-09，方案 A）**：`components/FlowDetailTabs.vue` 是主窗 `pages/FlowDetail.vue` 与复盘 `review/ReviewDetail.vue` 共用的四 Tab（概览/请求/响应/TLS）**纯展示组件**——props `{meta:ReviewFlowMeta, detail?:ReviewFlowDetail|null, flowId, loader?:BodyLoader, respDisabled?, showEndpoints?}`；wails `app.FlowMeta/FlowDetail` 与复盘 DTO 结构同构（均大写字段）直接互传；`req-copy/resp-copy` 具名插槽留给主窗挂 CopyBar（CopyBar 硬依赖 wails `GetFlowRawText`，复盘不挂）；`defineExpose({reloadBodies})` 供主窗 800ms 轮询重拉正文。两个 Detail 页降为薄数据外壳（取数/标题栏/轮询/代际防护各自保留）。证书有效期 `fmtCertDate` 兼容 RFC3339 字符串（wails/Go time.Time）与 unix 秒数（复盘 DTO 标注形态）。
- **复盘重发数据链路（关键）**：新增 `review/ReviewComposer.vue`（主窗 `pages/Composer.vue` 的 HTTP 版：`v-model:show` 自管开关、不碰 pinia/wails、不做 store.select；预填走归档 `flowDetail/flowBody`，发送走 `api.compose`）。ReviewApi 新增 `compose`（`POST /api/v1/compose`，后端零改动，已注册鉴权）、`liveFlowDetail`（`GET /api/v1/flows/{id}`）、`liveFlowBody`（`GET /api/v1/flows/{id}/body`）。**composer 结果流 Source=composer 因代际比对永不落归档库、只进实时 store**，故响应 BodyViewer 的 loader 必须用 live*（走 `/api/v1/flows/`），归档接口 `/api/v1/tags/flows/{id}` 取不到。`ReviewComposedRequest`（types.ts）= `{method,url,headers:[{key,value}],body,skipVerify}` 小写 json。
- **Demo 模式豁免重发（用户决策，优先于"新接口双实现"默认约定）**：DemoApi 的 compose/liveFlowDetail/liveFlowBody 仅抛错桩（保接口同口径），ReviewDetail 标题栏在 `api.mode==='demo'` 时把「调试重发」按钮置 disabled + NTooltip 提示"仅真实环境可用"。

## 构建与验证
- 前端：`frontend/` 下 `npm run build`（vite build；无独立 type-check 脚本，可用 IDE 诊断）。
- 代码风格：注释为中文，Naive UI 组件按需 import；scoped 样式，深度选择器用 `:deep()`。
