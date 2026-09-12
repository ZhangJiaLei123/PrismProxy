# PrismProxy 项目记忆（CONTEXT）

> **记忆主入口**：本文件只保留项目概览与记忆路由，细节一律进对应子记忆文件阅读。
> 2026-09-12 起按类别拆分为 `memory/` 下六个子记忆文件（原单文件内容已全量迁移，无删减）。
> 本文件 2026-09-09 曾合并自 doc/项目记忆.md（该文件现为指针存根，仍指向本文件）。

## 项目概览
- PrismProxy（棱镜）：Windows 桌面级 HTTP(S) 调试代理（MITM 解密 + 抓包看包），单文件 exe；独立开源工具（`https://github.com/ZhangJiaLei123/PrismProxy.git`），定位/文档/commit/注释**不引入任何具体业务方名称**。
- 技术栈：Go 1.27（代理引擎，标准库为主 + brotli/zstd + modernc.org/sqlite，核心无 CGO）+ Vue 3 + TS + Naive UI + Pinia + Vite 前端；Wails v2 桥接（WebView2）。
- 端口：代理默认 9090，控制 API（ctlapi）9595。
- 前端目录：`frontend/src`（pages/、components/、stores/、composables/、review/）；Wails 绑定在 `frontend/wailsjs/`，前端引用路径为 `../wailsjs/...`（pages 下）或 `../../wailsjs/...`（components 下）。
- 配置全部走**便携模式**：exe 同级 `config/`（settings.json、ca/、projects/<id>/、domains/、ctl-endpoint.json）；旧 %APPDATA%\PrismProxy 配置一次性自动搬迁不删旧文件。wails dev 下 exe 在 build/bin，配置落在 build/bin/config。

## 记忆路由（memory/）

| 记忆文件 | 内容 | 何时读 |
|---|---|---|
| [需求记忆](./memory/需求记忆.md) | 项目定位红线、功能范围拍板、产品决策（FreeNotice/复盘承载/intent/单条轮询/AI 配置归属/cURL 形态）、安全红线（apiKey/v-html） | 开新需求、做设计、需要确认"当初为什么这么定"时 |
| [进度记忆](./memory/进度记忆.md) | 开发时间线：M13 全部波次（P0→P4 及各审计/增强轮次）、里程碑摘要、验证状态与提交号 | 了解做到哪了、下一步是什么、确认某功能是否已验证时 |
| [教训记忆](./教训记忆.md) | 编号踩坑与铁律，六类：编辑与提交/构建与验证/Go 与 net-http/前端 Vue naive-ui/browseruse/AI 与 Ollama | 动手改代码前扫相关分类；遇到诡异现象先来查是否已踩过 |
| [前端代码记忆](./前端代码记忆.md) | 前端架构与通信、主窗骨架、flows store、共享组件、关键样式文件位置（review.css/ai.css/ai-md.ts）、复盘页全套、AI 面板全套、lib/curl.ts、mocks 桩约定 | 改前端任何代码前 |
| [后端代码记忆](./memory/后端代码记忆.md) | Go 包地图、代理引擎 9 决策、MITM 协议红线、sysproxy、CA、ADB 代际收敛、规则引擎、SQLite 持久化、Composer、ctlapi 约定、多项目隔离、Wails 绑定铁律、复盘查询、AI 后端全链路、cURL Go 侧 | 改后端任何代码前 |
| [环境备忘](./memory/环境备忘.md) | PowerShell 5.1 与编码红线、网络与依赖源、雷电模拟器全套、构建与验证、版本发布、curl 冒烟三坑、AI 测试 shim、GUI 自动化（MCP Computer Use） | 跑命令、构建、发布、连模拟器、GUI/浏览器实测前 |

## 按任务导航

- **改前端 UI/样式** → 前端代码记忆（样式链路、多入口样式红线）+ 教训记忆「前端/Vue/naive-ui」类
- **改后端 Go** → 后端代码记忆对应节 + 教训记忆「Go/net-http」类
- **加 Wails 绑定 / ctlapi 接口** → 后端代码记忆「Wails 绑定规则（铁律）」「ctlapi 通用约定」（2 返回值上限、fakeService 同步、wailsjs 三文件手动同步）
- **加 AI 相关能力** → 后端代码记忆「AI 后端」+ 前端代码记忆「AI 面板」+ 需求记忆「AI 配置归属」「安全红线」
- **构建 / 发布 / 冒烟实测** → 环境备忘「构建与验证」「版本发布」「curl 冒烟三坑」
- **模拟器抓包排查** → 环境备忘「模拟器」+ 后端代码记忆「Windows CA 与证书信任」
- **记录新进展 / 新踩坑 / 新决策** → 分别追加到 进度记忆（新波次）/ 教训记忆（新编号）/ 需求记忆（拍板留痕），并保持本路由表准确
