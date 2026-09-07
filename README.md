# PrismProxy

```
    ╱╲
   ╱  ╲     P R I S M
  ╱ ◇◇ ╲    棱镜 · 代理抓包工具
 ╱  ◇◇  ╲   "让加密流量，现出原形"
╱_________╲
```

> 棱镜把混杂的白光分解为清晰可见的光谱 —— 正如本工具把加密混杂的网络流量解密为结构清晰、可读可查的请求列表。

PrismProxy 是一款**桌面级 HTTP(S) 调试代理**（Windows 优先），对标 Reqable，聚焦「**抓包 + 看包**」：轻、快、稳。单文件 exe，基于 MITM 中间人原理解密 HTTPS 流量，并支持按进程归因流量来源。

## 功能特性

- **明文 HTTP 代理**：absolute-form 请求直接解析转发
- **HTTPS MITM 解密**：本地 Root CA + 按目标主机动态签发叶子证书（ECDSA P-256，LRU 缓存），握手失败域名自动降级盲透传（bypass）
- **进程归因**：通过 iphlpapi 端口→PID 映射，标记每条流量来自哪个进程（IPv4/IPv6 双栈）
- **流量面板**：实时列表 + 详情（Headers / Body / TLS 信息），增量推送，支持关键字/正则/方法/状态码过滤与置顶
- **过滤规则组**：多规则组黑白名单引擎（域名/进程/方法/状态码），支持导入导出
- **系统代理一键接管**：自动检测第三方代理占用
- **Composer 调试重发**：编辑请求参数后重发
- **效率复制**：右键复制 URL / cURL（cmd / PowerShell / bash 三版单行输出）/ 详情复制，快捷忽略域名/进程
- **证书向导**：`http://127.0.0.1:9090/ca` 证书下载与安装指引页
- **AI CLI**：内置 `cli` 子命令控制运行实例（状态 / 流量 / 规则 / 系统代理 / 配置 / UI），输出单行 JSON，面向 AI agent 与自动化脚本

## 技术栈与架构

| 项 | 内容 |
|----|------|
| 后端 | Go 1.27（代理引擎，核心零外部依赖） |
| 前端 | Vue 3 + TypeScript + Naive UI + Pinia + Vite |
| 桌面框架 | Wails v2（WebView2 渲染，Go Bindings + Events） |
| 平台 | Windows 10/11（依赖 WebView2 运行时，缺失时启动自检引导安装） |
| 默认端口 | 代理 9090（可配置）；控制 API 9595 |

```
┌──────────────────────────────────────────┐
│            PrismProxy.exe（单文件）        │
│  前端 Vue3（流量列表/详情/过滤/设置）        │
│            ▲ Wails Bindings + Events      │
│  Go Core：proxy 代理引擎 / mitm 证书中心    │
│           capture 会话捕获 / store 存储    │
└────────────────┬─────────────────────────┘
                 ▼
  客户端(手机/PC/模拟器) ──▶ :9090 ──▶ Internet
```

## 快速开始

### 运行

```bat
PrismProxy.exe              :: GUI 模式（默认，等价双击启动）
PrismProxy.exe -headless    :: 无界面模式（后台常驻，适合自动化）
```

可选参数：`-addr 127.0.0.1:9090` 指定监听地址，`-no-mitm` 关闭 HTTPS 解密。

### 抓 HTTPS 流量

1. 客户端信任本地 CA：访问 `http://127.0.0.1:9090/ca` 按指引安装证书（Windows 给出 `certutil` 免管理员命令）
2. 将客户端代理指向本机 9090 端口：
   - **手机/模拟器**：WiFi 代理或 `http_proxy` 指向 PC IP（注意：仅设 Windows 系统代理对雷电等 VBox NAT 模拟器无效，需在模拟器内设置）
   - **桌面应用**：设置页开启「系统代理」一键接管，或以 `--proxy-server=127.0.0.1:9090` 启动目标应用

### AI CLI

```bat
PrismProxy.exe cli status --pretty                 :: 实例总状态
PrismProxy.exe cli flows list --limit 5            :: 流摘要列表
PrismProxy.exe cli flows get <id> --body req       :: 请求体
PrismProxy.exe cli rules ignore host api.example.com  :: 快捷忽略域名
PrismProxy.exe cli sysproxy on                     :: 接管系统代理
PrismProxy.exe cli ui settings decrypt             :: 打开 GUI 设置面板
```

完整命令见 [doc/AI-CLI使用说明.md](doc/AI-CLI使用说明.md)。

## 构建与开发

环境要求：Go 1.27、Node.js、Wails CLI v2.15（`go install github.com/wailsapp/wails/v2/cmd/wails@latest`）、WebView2。

```bat
:: 一键构建（release，内嵌前端产物）
build.bat

:: 其他模式
build.bat nsis     :: 构建 + NSIS 安装包
build.bat debug    :: 调试构建
build.bat upx      :: UPX 压缩
build.bat clean    :: 清理产物
```

前端深度依赖 Wails 绑定（`wailsjs/go/main/App`、`wailsjs/runtime`），**纯 `npm run dev` 无法运行，调试必须用 `wails dev`**（前端热重载 + Go 热重载，`wails dev -d` 可挂 Delve 调试器）。

注意：构建前若 `PrismProxy.exe` 正在运行会因文件占用失败，先结束进程。

## 项目结构

```text
PrismProxy/
├─ main.go                  # 入口：cli 子命令分发 / GUI / -headless
├─ wails.json               # Wails 配置
├─ build.bat                # 一键构建脚本（release/nsis/debug/upx/clean）
├─ frontend/                # Vue3 + TS 前端
│  └─ src/
│     ├─ App.vue            # 主布局（顶栏状态 + 流列表 + 详情）
│     ├─ pages/             # 页面
│     ├─ components/        # 流列表/详情/设置抽屉/Composer 等
│     └─ stores/            # Pinia 状态
├─ internal/
│  ├─ app/                  # App 装配、GUI/headless 启动、WebView2 自检
│  ├─ proxy/                # 代理引擎：CONNECT 分流 / MITM / 证书页
│  ├─ mitm/                 # Root CA 生成持久化 + 叶子证书动态签发
│  ├─ capture/              # Flow 捕获（tee 截断 body）
│  ├─ store/                # 流量存储（内存环形缓冲）
│  ├─ rules/                # 过滤规则组引擎 + 端口→PID 进程归因
│  ├─ procs/                # 进程信息
│  ├─ domains/              # 域名规则
│  ├─ settings/             # 配置读写
│  ├─ sysproxy/             # 系统代理接管/恢复
│  ├─ compose/              # Composer 重发
│  └─ ctlapi/               # AI CLI 控制面（127.0.0.1:9595 + token）
└─ doc/                     # 设计文档
```

## 文档

| 文档 | 内容 |
|------|------|
| [doc/方案.md](doc/方案.md) | 完整技术方案（定位 / 原理 / 模块设计） |
| [doc/开发进度.md](doc/开发进度.md) | 里程碑与任务级进度跟踪 |
| [doc/规则设计.md](doc/规则设计.md) | 过滤规则组设计 |
| [doc/AI-CLI使用说明.md](doc/AI-CLI使用说明.md) | CLI 命令详解与输出示例 |
| [doc/CLI实时推送设计.md](doc/CLI实时推送设计.md) | CLI 实时流量推送设计 |
| [doc/项目记忆.md](doc/项目记忆.md) | 关键技术决策与排查记录 |

## 免责声明

本工具仅用于**本地开发调试与授权测试**场景。请勿用于抓取、解密未授权的第三方流量，使用者需对自身行为负责。
