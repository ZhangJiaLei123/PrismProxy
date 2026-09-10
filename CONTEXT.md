# PrismProxy 项目记忆（CONTEXT）

> 跨任务协同的关键记忆，只记非显而易见的约定、架构决策与踩坑。开发进度不记在这里（见 doc/开发进度.md）。
> 本文件 2026-09-09 合并自 doc/项目记忆.md（该文件现为指针存根）。

## 项目概览
- PrismProxy（棱镜）：Windows 桌面级 HTTP(S) 调试代理（MITM 解密 + 抓包看包），单文件 exe；独立开源工具（`https://github.com/ZhangJiaLei123/PrismProxy.git`），定位/文档/commit/注释**不引入任何具体业务方名称**。
- 技术栈：Go 1.27（代理引擎，标准库为主 + brotli/zstd + modernc.org/sqlite，核心无 CGO）+ Vue 3 + TS + Naive UI + Pinia + Vite 前端；Wails v2 桥接（WebView2）。
- 端口：代理默认 9090，控制 API（ctlapi）9595。
- 前端目录：`frontend/src`（pages/、components/、stores/、composables/、review/）；Wails 绑定在 `frontend/wailsjs/`，前端引用路径为 `../wailsjs/...`（pages 下）或 `../../wailsjs/...`（components 下）。
- 配置全部走**便携模式**：exe 同级 `config/`（settings.json、ca/、projects/<id>/、domains/、ctl-endpoint.json）；旧 %APPDATA%\PrismProxy 配置一次性自动搬迁不删旧文件。wails dev 下 exe 在 build/bin，配置落在 build/bin/config。

## 环境与网络备忘
- PowerShell **5.1**：无 `ForEach-Object -Parallel`（用 Start-Job）；`curl` 是别名必须 `curl.exe`，https 加 `--ssl-no-revoke`（schannel 强查吊销，私有 CA 无 CRL 即使装根也会 TLS 失败；浏览器/.NET/openssl 不硬查）。
- GitHub：`github.com` 可达但间歇超时（出口网络问题，经代理直连模式同构失败可证非代理 bug）；`raw.githubusercontent.com` 被 DNS 污染，用 **jsDelivr** `https://cdn.jsdelivr.net/gh/<user>/<repo>@<branch>/<path>`（另有 fastly./gcore. 备用，缓存延迟数小时）。
- Go 模块：modernc.org/sqlite 依赖树必须 `$env:GOPROXY='https://goproxy.cn,direct'`。
- **PowerShell 编码红线**：绝不用 `Set-Content`/`-replace|Out-File` 重写含中文源码（默认 GBK 毁 UTF-8，gofmt 报 illegal UTF-8）；手工写 JSON 配置用 `[System.IO.File]::WriteAllText` + `UTF8Encoding($false)`——**PS 5.1 `Set-Content -Encoding UTF8` 带 BOM**，settings.json 带 BOM 会 json.Unmarshal 失败静默回退 Default。改源码一律用编辑工具。
- `go mod tidy` 会移除"已声明但未 import"的依赖——先写 import 再 tidy。
- 杀软首次扫描 test exe 报 Access denied，重跑即过。

## 代理引擎关键决策（internal/proxy、internal/capture）
1. **ForceAttemptHTTP2**：为记录上游真实证书链需自定义 TLSClientConfig，Go 一旦自定义就保守禁用 h2，必须显式 `ForceAttemptHTTP2: true`。
2. **流式捕获 tee 语义**：边转发边捕获，Flow 状态机 pending/streaming/done/error；WebSocket/101 升级 http.Transport 无法接管，必须 Hijack。
3. **默认绑 127.0.0.1**：不开放 LAN 匿名代理；局域网绑定是显式开启项。
4. **PID 进程映射**：`GetExtendedTcpTable` 同时查 AF_INET+AF_INET6；accept 即时查不缓存；权限不足取路径失败→降级显示+提权提示。M1 用纯 syscall+LazyDLL 直调 iphlpapi.dll（不用 golang.org/x/sys，零外部依赖）。
5. **解压**：gzip/deflate（标准库）+ br（andybalholm/brotli）+ zstd（klauspost/compress，项目已有包级 Encoder/Decoder，EncodeAll/DecodeAll 并发安全）。透传 Accept-Encoding 后 CDN 会回 zstd。展示层 `capture.DecodeBody` 在 GetFlowBody 时解压，不在捕获路径；Composer 则 DisableCompression 保真透传。
6. **CA**：私钥 ACL 限当前用户；叶子证书 ECDSA P-256、有效期 ≤1 年、LRU + singleflight。
7. **内存**：环形缓冲默认 2000 条/256MB（maxFlows/maxBodyMB 可配），按 body 实际字节统计，body 2MB 截断；事件批量节流 ~50ms + `flow:evict` 淘汰通知。Flow ID 格式 `时间戳毫秒-序号`。
8. **逐跳首部**：proxy `removeHopHeaders`（九项）；旁路记录（compose/未来回放/CLI 造流）必须照做两份清单：① PID 映射填 ProcessInfo；② 逐跳首部剥离（响应侧保留 Content-Length，请求侧含 Content-Length 让 Transport 重算）。
9. store `Add` 按 ID upsert（原位更新发 "update"，否则值拷贝追加发 "new"，覆盖先发 "evict"），订阅锁外回调。

## MITM 协议红线（h2→h1 回写，internal/proxy/mitm.go，2026-09-07 修复）
客户端侧 TLS 恒只协商 http/1.1（NextProtos 只有它，兼容老 Android okhttp/xutils/HttpURLConnection），但上游 ForceAttemptHTTP2 常回 h2 响应。**弃用 `resp.Write`，走 `writeH1Response` 手工组帧**：
1. 状态行恒写 `HTTP/1.1 <code> <reason>`——`resp.Write` 会穿透写出非法 `HTTP/2.0 200 OK`，老 okhttp 抛 ProtocolException 判"无网络"；上游真实协议仍记 `flow.Response.Proto` 供展示（两处分离）。
2. 上游无 Content-Length（h2 常态）一律转 h1 `Transfer-Encoding: chunked`，有 CL 显式写 CL。
3. 状态行+首部写完**立即 flush**；body 32KB 循环读**每块都 flush**（否则 SSE/IM 长连接全积在 4KB bufio 缓冲，客户端连头都收不到；且 goroutine 阻塞使 flow 停 streaming 不落库——"flows 里没有 SSE 流"不代表 App 没发）。
4. HEAD/204/304 剥 Transfer-Encoding/Content-Length 不发体。明文 HTTP 路径走标准 ResponseWriter 无此问题。
- 客户端 TLS 握手失败时 `addBypass(host)`，该 host 后续 CONNECT 直接盲隧道；**全量降级时 UI 无任何报错**（优雅降级）。症状=列表几乎全 CONNECT、明文 HTTP 正常→优先查 CA 指纹是否一致。

## 系统代理（internal/sysproxy）
- 注册表真实路径 `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`（方案里 `HKCU\Internet Settings` 是简写，reg query 必须全路径；PS HKCU: PSDrive 偶发异常，用 .NET `[Microsoft.Win32.Registry]::CurrentUser.OpenSubKey(...)`）；`InternetSetOption` 广播 39+37；备份落 `config/sysproxy-backup.json` + 崩溃自愈 SelfHeal。
- **ProxyOverride 绕过语义**：分号分隔；**裸域名同时匹配自身与全部子域**；`<-loopback>` 绕过回环。绕过发生在客户端侧（WinINET/Chromium 直接拨号，代理根本收不到 CONNECT），比代理内解密黑名单更靠前——是"代理死了注册表残留"断网的系统级免疫。接管时**合并去重**内置绕过列表+用户原值（前缀保留 `<-loopback>`）绝不覆盖，恢复时精确还原备份。
- 已内置开发机绕过列表（trae/字节系 20 个裸域，备份文件 proxyoverride-backup.txt 留原值）；Trae CN.exe 自家网络栈不尊重 ProxyOverride 属预期。
- **退出清理三场景**：①关窗口/Alt+F4→Wails OnShutdown；②**系统关机/注销→Wails v2.15 不处理 WM_QUERYENDSESSION/WM_ENDSESSION（源码核实，OnShutdown 不触发）**，自建隐藏普通顶层窗口接收（session_windows.go：独立 goroutine+LockOSThread+RegisterClassExW/CreateWindowExW/GetMessage；**HWND_MESSAGE message-only 窗口收不到 ENDSESSION 广播**），WM_ENDSESSION(wParam=TRUE) 同步 `App.onSessionEnd()`（还原系统代理+同步清 ADB 设备代理，adbSessionTimeout=8s）；非 Windows session_other.go 空桩。③taskkill /F/崩溃→下次启动 SelfHeal 兜底。还原统一 `restoreSystemProxy()`（StateOn 才 Disable，幂等）。
- 停止监听前若系统代理已接管须先 SetSystemProxy(false)，否则全网断；headless 模式不管系统代理。
- `upstreamMode=system` 在接管后无效（注册表 ProxyServer 是自己→UpstreamFromSystem 防环返回空=直连）；防回环另见 `proxy.GuardUpstreamLoop`。

## Windows CA 与证书信任
- Root CA 位置：`config/ca/prism-ca.pem` + `prism-key.pem`；每次 StartProxy 重新从盘加载 + bypass 挂在新建 proxy.Server 上——**换 CA 后关再开监听即可生效，无需重启程序**。
- **便携模式只搬 settings.json 不搬 CA 目录**：config/ca 为空时 LoadOrCreateCA **静默生成新 CA**，受信根还是旧的→全 CONNECT 盲隧道。排查=对比 `openssl x509 -in config/ca/prism-ca.pem -noout -fingerprint -sha1` 与受信根 `CN=Prism Root CA` 指纹。修复：装当前 CA（certutil）或拷旧 CA 覆盖。
- 历史指纹：旧 CA（%APPDATA%，模拟器用户 CA 由它签发）sha1=`1953B19BC393481C1237CC42200B5472D56E82AE`；2026-09-05 便携新 CA `1C0ECAC5...`（未装根）。
- 安装：`certutil -addstore -user -f Root <cer>` 免管理员但弹安全警告需人工点"是"；全机版需管理员。Edge/Chrome 信任当前用户存储即生效。
- 分诊工具：`openssl s_client -proxy 127.0.0.1:9090 -connect <host>:443 -servername <host> -brief`（Git for Windows 有 openssl 无 curl；anaconda 也带）。
- **wails build -clean 会清 build/bin/config/ca 换全新 CA**（build.bat clean 的"保留 config"保护对直接调 wails -clean 无效）；增量改 Go 用 `go build -trimpath -ldflags "-s -w" -o build\bin\PrismProxy.exe .`（保留 config）。**build.bat clean 已修**：config 先 move 到 build/_config_keep 暂存再迁回（CA/settings/endpoint 全保）。发布包不含 config/CA，终端用户首装仍需装 CA。

## 模拟器（雷电 LDPlayer14 = Android 14/SDK34，VirtualBox 架构）
- guest 网段 172.16.1.0/24；NAT 由独立进程 **VBoxNetNAT.exe** 完成→模拟器流量统一归因 VBoxNetNAT.exe（非 dnplayer/Ld9BoxHeadless），多开共享归因粒度受限。**`172.16.1.2`=宿主回环别名（ping TTL=128）**，代理绑 127.0.0.1:9090 即可被模拟器经 172.16.1.2:9090 访问，无需绑 0.0.0.0；`.1` 是虚拟路由器不可用。
- **仅设 Windows 系统代理对雷电无效**（VBox NAT 不感知），必须 Android 内 `adb shell settings put global http_proxy 172.16.1.2:9090`（清除 `:0`，跨重启持久保留）。雷电自带 adb `D:\leidian\LDPlayer14\adb.exe`，设备 emulator-5554（另有真机勿误操作）。
- **LDPlayer14 用户 CA 合并不生效**（重启后 conscrypt/system cacerts 均无用户证书）。解法=**root 直注系统 CA bind-mount tmpfs（重启失效需重做）**：`ro.debuggable=1` 可 `adb root`；APEX verity 不可 remount，改为拷 /apex/com.android.conscrypt/cacerts 到 /data/local/tmp/cacerts-inject + 加 `<subject_hash_old>.0`（chmod 644/chown root:root/chcon system_security_cacerts_file）后 `mount --bind ... /apex/com.android.conscrypt/cacerts`；新 fork App 立即信任（已运行的 am force-stop 重开）。hash 用 Git 的 openssl `x509 -subject_hash_old`。模拟器内旧用户 CA 文件 `9e85ecda.0`（1953B19B 签发）。
- LDPlayer 的 su 是 toybox 版（`su -c` 内 -o/分号全被拆错）→脚本 push 后 `adb root` + `adb shell sh /data/local/tmp/x.sh`。
- `adb reboot` 在雷电上可能起不来（adbd 不监听）→`ldconsole.exe quit --index 0` 等 6s 再 `launch`。
- 模拟器自带 curl 8.0.1 **不读 CA store 也不读全局代理**（需显式 -x、验证书加 -k；-k 200 只验链路），**验证 CA 信任必须用 AOSP 浏览器 com.android.browser（WebView 信任注入的系统 CA）**。少量主域 CONNECT 盲隧道多为证书固定 pinning。
- App"未连接网络"分诊：`adb logcat -c`→`am force-stop`→冷启动（入口 `cmd package resolve-activity --brief <pkg>`）→等 20-30s→logcat 搜 `ProtocolException|SocketTimeout|...`；CLI flows list 找非 done 流。截图 `screencap -p` 落盘再 pull（PowerShell exec-out `>` 会把 PNG \n 破坏成 \r\n）。

## ADB 自动代理（设置「ADB 代理」tab，internal/app/bindings_adb.go）
- 全局 `adb.deviceProxyHost`（默认 172.16.1.2）+ 多条 `ADBDevice{Name,Path,Serial,AutoSet}`；端口不存、运行时取实际监听口。binding：PickAdbPath/AdbTest/AdbSetProxy/AdbClearProxy；`runAdb(path,serial,args...)` 20s 超时（adb server 首启慢），serial 非空前置 `adb -s`。
- **代际收敛（核心）**：`adbGen atomic.Uint64` + `adbCh chan adbOp` 单 worker FIFO 串行队列；startProxy 成功/stopProxy 都在持锁区 `adbGen.Add(1)`，任务入队捕获 gen（goroutine 等解锁后拿快照入队，避免持锁重入死锁），worker 执行前 gen 不符即丢弃——热重启 stop 的 clear 旧代际被弃，只有最新 start 的 set 落盘，杜绝 clear/set 乱序静默覆盖。
- `stopProxy()` 拆 `stopProxyWithOpt(true)` 与 `stopProxyNoHooks()`：Shutdown/headless 走 no-hooks（已先做 onSessionEnd 同步清除，避免冗余 goroutine 被进程退出截断）；startProxy 末尾 `go autoSetAdbProxies`。
- **SaveSettings 收敛 `convergeAdbConfigs`**：被删/取消 AutoSet 的设备补一次 clear（否则设备 http_proxy 永久指向死代理=真断网）；新开 AutoSet 且代理在跑补 set（restarted=true 跳过由启动挂钩统一处理）。diff key=TrimSpace(path)+TrimSpace(serial)。
- AdbTest 三态：device/offline/unauthorized；全 unauthorized 报"请点允许 USB 调试"，多设备提示填序列号。监听地址解析用 net.SplitHostPort（支持 `:9090`）。
- CLI 无 adb 子命令（GUI binding），仅 `cli ui settings adb` 可打开该 tab（UISettingsTabs 白名单含 adb）。前端 AdbTab 行身份用 WeakMap 分配 rid（删中间行后 pick 回写不串行）。

## 规则引擎与域名组
- 三层规则：捕获/解密/进程；有序列表首条命中+默认动作。
- **过滤规则组（M4.6，设计唯一事实源 doc/规则设计.md）**：`FilterGroup{id,name,enabled,mode(blacklist/white),hosts,paths,processes}` 替换旧 captureRules/processRules（启动幂等迁移，迁移后清空旧字段）。语义：组内维度 AND、同维度多条目 OR、组间 OR、deny-override（黑名单恒优先；无白名单组默认全显示）。path glob：`*` 跨 `/`；无通配符=精确或子路径段边界（`/telemetry`≠`/telemetryx`），字符串比较非正则，只匹配 path 不含 query。统一入口 `ShouldDisplay(host,rawURL,procName)`（nil receiver 安全返 true）。**信息缺失维度移除规则**：进程未知（VBoxNetNAT.exe）/盲隧道无 path→该维度约束移除，剩余空则组不匹配（纯 path 白名单放不了隧道流是已知局限）。
- @域名组引用：校验存在性但只 warning 不阻塞（Validate 返 error+warnings；SaveSettings 返 `SaveSettingsResult{warnings}`）；hosts 禁裸 `*`；NewEngine 失败=空引擎全放行、保存失败=拒绝保存；Engine.groups map 仅供解密层 @引用（过滤热路径用编译期预展开）。
- **快捷忽略=双内置黑名单组（必须双组）**：`_quick_ignore_hosts`（hosts 维，裸域名含全部子域）/`_quick_ignore_procs`（processes 维），均 blacklist/enabled 组间 OR。单组混维会因组内 AND 互相收窄导致两个动作彼此失效；旧混合组 settings.Migrate(splitQuickIgnoreGroup) 启动幂等拆分。Binding `AddQuickIgnore(target,value)`。忽略只影响后续流量不删存量。
- **规则导入导出**：JSON version=1（exportedAt/filterGroups/decryptRules/bypassList/groups）；整体替换；bypassList 非 nil 才替换（防旧文件缺字段清空绕过）；内嵌域名组自动 WriteUser 补建；来源=文件对话框/本地路径/http(s) URL；err 阻塞 warns 弹窗；落盘后 SetLimits→rebuildEngine，仅监听/上游变化才重启代理。前端导入前 useDialog 强确认。
- **域名组**：`domains/` 22 个 txt + index.json **源码保留提交 git 但不 embed**（exe 无默认组，首启列表空）；txt 每行一域名（裸域名=自身+全部子域，`*.x.com` 等价），头部标准标题 `# 域名组：<名>`，index.json 不重复域名（txt 唯一事实源，增删必须同步索引）。用户组落 `config/projects/<id>/domains/<id>.txt`（项目化后），仅 LoadUser 加载（Custom=true）。URL 导入 ProbeURLImport 自动识别 index/txt（索引 file 相对 URL 解析）；批量导入 ImportDomainGroupsFromIndex(url,ids,overwrite) 发 `index-import-progress` 事件，冲突 overwrite=false 时 os.Stat 命中 Skipped。显示名优先级：txt 标题 > index name > id。限制：仅 http/https、20s、4MB LimitReader、2xx。gid 一律 ToLower(TrimSpace())（ValidateID `^[a-z0-9][a-z0-9-]{0,63}$`，CLI 从文件名派生也要规范化）。

## SQLite 持久化（internal/persist，M7，默认关闭）
- 选型 **modernc.org/sqlite 纯 Go 无 CGO**（mattn/go-sqlite3 依赖 CGO 破坏单 exe，禁用）；`CGO_ENABLED=0 wails build`。DSN `?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)` + SetMaxOpenConns(1)。每项目独立库 `config/projects/<id>/prism.db`（dbPath 固定，旧 persist.dbPath 配置已废）。
- `Writer{db,queue chan *capture.Flow(2048),...}`：**queue/stop 在 Start() 内创建**（Open 只建表）；Enqueue 仅终态流、先 cloneFlow 深拷贝、`select{case queue<-f:default:dropped++}` 非阻塞丢弃，**绝不阻塞代理热路径**；consumeLoop 100 条/500ms 批量事务。
- 两表：`flows`（id PK + started_at 索引 + 检索字段 + data=完整 Flow JSON，序列化前记 BodyLen 再 Body=nil）、`bodies`（(flow_id,kind) PK，zstd 压缩 blob，≥256B 且压后更短才压）。
- **历史流 body 惰性回查**：LoadRecent 不载入 body，详情经 `persistBody→Writer.LoadBody`（ErrNoRows→(nil,nil) 静默；解压错 log+提示）；Source="history" 防回写（onPersistEvent 跳过 history 流；Pinned 不持久化，加载时强制 false）；DTO `FlowMeta.Historical` PascalCase 键。
- **保留策略**：retention 2s 首跑+每 10 分钟，可取消定时器（Close 后不触发）。必须 `auto_vacuum=INCREMENTAL`（仅建表前生效，旧库 migrate 里 PRAGMA+VACUUM 一次性迁移）；**WAL 收缩铁律顺序**：DELETE→`PRAGMA incremental_vacuum`→`PRAGMA wal_checkpoint(TRUNCATE)`，反了主文件不缩；dbSize=主+wal 合计，enforceSize chunk=64 分批删；retainDays/maxMB=0 不限。体积测试用 crypto/rand 不可压缩体并断言文件字节数。
- **锁重入死锁红线**：pmu 临界区内严禁调用同步触发 store emit 的方法（st.Add）——emit 在解锁后同步回调 onPersistEvent 又取 pmu→同 goroutine 二次加锁永久死锁。**约定：pmu 只保护 writer 指针；订阅与历史补载一律解锁后执行**。锁序铁律：`projMu → a.mu/a.pmu`，禁止反向。
- 热更新：路径不变仅 UpdateRetention（不 Close/Open）；切换项目 persistOwner{w,gen,id} 打标，入队前比对 flow.Gen 与 writer.gen（封代际窗口）；Shutdown/headless 退出前 stopPersist。

## Composer 调试重发（internal/compose，M6）
- 独立 http.Client **直连防回环**（不设 tr.Proxy=直连 9090；仅非 direct 上游时代理 + GuardUpstreamLoop）；HTTP/1.1 only（自定义 TLSClientConfig 后**故意不设** ForceAttemptHTTP2）；Dial 15s/Timeout 30s/重定向限 10；DisableCompression:true。
- 本进程发出须显式 `ProcessInfo{PID:os.Getpid(),Name:filepath.Base(exe),Path:exe}`；剥逐跳首部（见代理引擎第 8 条）。
- **重发流经 Store.Add 直入、绕过过滤引擎是设计**（保证重发必入列表；否则可能被自己的快捷忽略吞掉），Source="composer" 仅标记。前端发送成功 store.select(newFlow) 形成迭代链。无 URL/CONNECT 禁用入口；非法 URL 返回 url.Error 且不落库。
- HTML 预览：iframe `srcdoc` + `sandbox="allow-same-origin"`（**绝不开 allow-scripts**）+ referrerpolicy=no-referrer + 注入 `<base href>`。
- 共享展示三件：HeaderTable.vue / CopyBar.vue（硬依赖 wails GetFlowRawText，复盘不挂）/ BodyViewer.vue（七视图）。
- CLI `compose <URL> [--method --header 'K: V'（fs.Var 可重复；单值内兼容分号）--body --insecure]`。

## ctlapi / AI CLI（internal/ctlapi，M8/M8.5/M10）
- 形态：同 exe 子命令 `PrismProxy.exe cli <cmd>`（main.go flag.Parse 前拦截）+ `-headless` 可组合（同 exe 两角色：-headless 起实例、cli 控实例）。API 仅绑 127.0.0.1:9595，Bearer token（crypto/rand 32B，constant-time 比较，支持 Authorization 头与 X-Prism-Token；`/events` 额外接受 query token）。
- endpoint 发现文件 `config/ctl-endpoint.json`{addr,token}：原子写+0600+icacls 限当前用户，实例 Close 删除，强杀残留→cli 友好报错、下次覆盖。
- **ctlService 适配器**（ctl_bridge.go）包装 *App 实现 ctlapi.Service 接口——App 已有同名 Wails binding，**不要直接在 App 上加 ctlapi 方法**（签名冲突）；接口加方法必须同步 `ctlapi_test.go` fakeService（`var _ Service` 编译期断言，缺桩即编译失败）与 wailsjs 三个生成文件。
- flag 处理：Go flag 遇位置参数即停，自写 reorderFlagsGlobal/reorderFlags 把 flag 前置（bool flag 不吞下一 token）；pos[0]=子系统名。**处理器自管 stdout 原文输出（如 rules export）返哨兵 `errOutputHandled`**，RunCLI errors.Is 跳过 printResult——否则尾随 `null` 破坏 `> file` 重定向。
- 命令面（M10 补齐后）：status / flows list|get|clear|watch|pin|curl / rules list|ignore|enable|disable|decrypt|export|import / sysproxy / settings / ui clear|settings [tab] / project list|switch|create|rename|delete|close / proxy start|stop / ca install / adb devices|test|set|clear / domains list|get|save|delete|import / compose / processes。headless 绕行三式：导出→stdout、导入→实例侧 fetchSourceBytes、adb 路径→--adb/--serial。
- **UI 控制**：数据级（ui clear）GUI 经 flow:evict 自动同步 headless 也执行；纯界面（ui settings）走 `EventsEmit("ui:open-settings",tab)`，tab 白名单 general/network/decrypt/capture/domains/adb/ai，非法 400，headless 返 {ui:false}。
- 启动时序：startCtlAPI 在 startup 开头同步执行（数百 ms）会推后 StartProxy，前端只刷一次会看到"已停止"——**startup 末尾（自动启动/接管完成后）必须发 `proxy:ready` 事件**，前端立即拉+事件推双保险。startup 加同步耗时步骤都要考虑。
- **JSON 字段大小写口径**：FlowMeta/FlowDetail/BodyPayload **无 json tag=PascalCase**（字节字段 base64）；status/sysproxy 手构 map 小驼峰；settings.Settings 有 json tag 小驼峰。
- HTTP server：只设 ReadHeaderTimeout，**禁 WriteTimeout/IdleTimeout（杀 SSE）**；Close 先 Hub.Close() 再 Shutdown(2s)。**ServeMux 精确模式不匹配子路径**：`/api/v1/flows` 不匹配 `/flows/clear`（落子树 404），动作子路径必须显式注册。
- **SSE（M8.5）**：`GET /api/v1/events` 双频道 flows/status，Hub fan-out。五决策：①先订阅后快照（snapshot 帧，缝隙流不丢，重叠靠 upsert 幂等+evict 忽略未知 ID）；②headless 下 ctx 恒 nil，hub fan-out 必须置于 flush() ctx 早退**之前**；③ctlapi 不能 import app（循环依赖），过滤走 Filterable 接口（FlowMeta.FilterFields()）；④`[]Filterable` json.Marshal 成 `[{}]`——FlowsUpsert 自定义 MarshalJSON 逐条 marshal 具体类型；⑤watch 事件流长连接与快照请求**连接隔离**（独立 DisableKeepAlives client，否则 SSE 帧被当 HTTP 响应消费）；背压摘除只 close(reset) 不 close(ch)；订阅上限 32 超限 503；watch 退避 0.5→8s、连续 5 次失败退出码 1、Ctrl+C/Break 码 0。
- status 发布点：bindings_proxy 拆公开 StartProxy/StopProxy（发事件）+ 内部 startProxy/stopProxy（不发，供连调）。

## 多项目隔离（M9）与无项目态（M11）
- 切分：**规则类按项目隔离**（filterGroups/decryptRules/用户域名组→`config/projects/<id>/project.json`+domains/+独立 prism.db）；**环境类全局**（settings.json：监听/上游/ADB/预算/开关 + Projects 元数据 + CurrentProject）。顶栏热切换不重启代理。
- 热切换：**preflight 前置**（加载配置/域名组/规则预编译全部在 `projGen++` 之前，任一步失败整体拒绝不留半态）；切换段 projGen++→ClearAll→换库→引擎→落盘→emit project:changed 不再失败。在途流终态按 flow.Gen 新旧代际比对丢弃（两项目都不留，拍板）。首次启动旧单配置幂等迁移为默认项目（M11 前）。
- SaveSettings 用 SettingsView 合并视图：入参 currentProject/projects 一律忽略（堵 CLI 整结构回传后门），切换唯一入口 SwitchProject；**rulesProject 令牌**与当前项目不一致拒绝保存（防双端覆盖）。apiKey 等投影字段同理（见 M13）。
- CLI：`--project/-P` 仅对 rules/settings/domains 类生效；非当前项目写=读盘→改→ValidateRules/NewEngine 编译预检→落盘（不改 current、不热切换、非法则文件不变）；当前项目写=内存+落盘+热重建。
- **M11 无项目态（三指针置空）**：proj=nil/groups=nil/eng 持 nil（nil receiver ShouldDisplay/ShouldDecrypt 返 true=代理照常全放行）。`enterNoProjectLocked()`（持 projMu）：projGen++→ClearAll→nil 指针→停 writer（不开新库）→eng.Set(nil)→CurrentProject="" 落盘，进入后不失败。CloseProject 幂等；DeleteProject 放开所有约束（可删当前/删光），删当前先 enterNoProject 释放 db 句柄（否则 Windows 占用删目录失败）→删清单→SaveGlobal（失败回滚插回+切回）→删目录→剩余非空自动切首个。
- nil 守卫：projDir/currentID/currentMeta nil 安全助手；所有写规则/域名组操作持锁后显式报"尚未打开任何项目"；GetSettings 无项目返空规则切片 rulesProject=""，SaveSettings 无项目跳过规则半边但**环境热应用照常**（needRestart 重启/bypass Reapply/convergeAdbConfigs 不跳过；CLI 无项目态空入参须放行到环境保存分支）。
- 前端 useProjects composable 模块级单例，`project:changed` 订阅模块级只注册一次（不依赖组件生命周期）；无项目挂全屏 WelcomePage（主界面含头/底栏不挂载，footerRef 为 null 已可选链兜底）；欢迎页设置抽屉复用，项目级三 tab（解密/过滤/域名组）v-if hasOpenProject，无项目态 effectiveTab() 回退 general。
- 教训：**同一文件多个 SearchReplace 不可并行**（竞态丢改动，已踩三次含 App.vue 漏 import 白屏）；并行编辑按文件隔离。

## 前端架构
- **通信**：Go 方法经 Wails Bindings（wailsjs/go/app/App）；后端推送经 EventsOn（wailsjs/runtime/runtime）。关键事件：`proxy:start-error`、`proxy:ready`（底栏状态刷新）、`ui:open-settings`、`flow:upsert`/`flow:evict`、`project:changed`、`index-import-progress`。
- **App.vue（2026-09 重构后）**：纯布局编排（WelcomePage / AppHeader / NSplit[FlowList+FlowDetail] / AppFooter / SettingsPanel / Composer）+ 错误条 startError + 设置抽屉开关。错误条归属 App.vue，「打开设置」按钮保持 showSettings=true 不重置 tab。
- AppHeader.vue（40px）：ProjectSwitcher、流计数/暂停刷新/清空、GitHub（runtime.BrowserOpenURL，浏览器降级 window.open，内联 octocat SVG 无 @vicons）。无 props/emits 直用 store。
- AppFooter.vue（28px）：过滤栏（关键字/正则/方法/状态多选，空串哨兵=全部，非法正则降级子串）、代理状态标签、监听/系统代理开关、设置按钮。自持 status/sysState/showSysSwitch。
  - **defineExpose**：`refreshStatus()`=并发两状态查询（含 StartError 兜底）用于热路径（开关 finally、proxy:ready/start-error 后）；`refresh()`=refreshStatus+GetSettings 读开关可见性，仅挂载时与设置保存后调（避免热路径多一次往返）。
  - **StartError 去重**：lastStartError 记已上报原文，同一错误只 emit 一次；错误文本变化或后端清空后才允许再报。emits：`open-settings(tab)`（network/general 统一 onUIOpenSettings 含 WindowUnminimise）、`error(message,replace?)`（replace=true 覆盖显示最新，否则仅错误条空时恢复）、`clear-error`。
- **flows store**：flows/filter/paused + init()/clear()/filtered。暂停=纯前端冻结显示（回调丢弃，后端不中断不补发）；clear 保留 Pinned。置顶 Pinned：固定顶部/不参与环形淘汰/Clear 保留/上限 200/会话内不持久化。
- 设置面板 SettingsPanel.vue：左侧 n-tabs（常规/网络/ADB代理/解密规则/过滤规则/域名组；无项目只显全局三个），宽 560-640，内容区独立滚动；watch [show,initialTab]（面板开着外部切 tab 只切 activeTab 不 loadSettings 覆盖编辑）。系统代理实时控制不随表单保存；保存错误用全局 message，warnings 留固定 footer。autoSysProxy 默认 false（startup StartProxy 成功后才接管，端口占用跳过，失败仅 log）。
- pages/ = App.vue 直接挂载的页面级；components/ = BodyViewer/HeaderTable/CopyBar/JsonTree/FreeNotice 共享件；无 vue-router。
- **FreeNotice.vue**：「本工具完全免费，为爱发电 ❤ + 开源地址 GitHub 链接」空态说明组件，点击走 window.runtime.BrowserOpenURL（浏览器预览降级 window.open）；用于 WelcomePage（无项目空态居中；有项目时列表下方 `.main-free`，`.welcome-main` flex column + margin-top:auto 贴右侧底区）与 pages/FlowDetail.vue 未选中流空态，文案/链接改动只改此件。标题 `.fn-title` 为粉→紫→蓝渐变流光文字（background-clip:text + fn-shine 3.5s），爱心 `.fn-heart` 双跳心跳动画（fn-heartbeat 1.4s，含 drop-shadow 脉冲）。**不做 prefers-reduced-motion 降级**：品牌装饰动画用户要求恒播放，且本机 MinAnimate=0（Windows 关闭动画）时降级会致动画全失（2026-09-10 踩坑）。
- **构建红线：vite build 绿 ≠ 前端可用**——vite 无 auto-import 时 Rollup 对未解析标识符静默当全局外部名，退出码 0 但运行时 ReferenceError 白屏；前端改动后必须核对 IDE 诊断零错误或 grep dist 特征文案。模板内 JS 模板字符串以 `}}` 结尾会与 Vue 插值冲突，改用拼接/计算属性。

## 数据复盘页（M12/M12.1/M12.2）
- 独立 Vite 多页入口 `review.html` + `src/review/`（独立 createApp，不依赖 Wails runtime），经 ctlapi（127.0.0.1:9595，Bearer token，静态 dist 不鉴权、`/api/` 鉴权）由系统浏览器开独立窗口；Wails v2 无多窗口能力。**Vite base 保持默认 `/` 是红线**（改 `/review/` 主窗 404 白屏）。
- 数据层 `review/api.ts`：HttpApi（fetch Bearer）+ DemoApi（无 token/?token=mock）双实现，任何新接口两端都要补；DTO：TagInfo/HistBucket 小写 json、FlowMeta/FlowDetail 大写字段。
- M12.1：列表/直方图查 `scope=archived|all`（默认 archived=打标流；具体标签恒归档，服务端宽容忽略 scope）+ `start/end` unix 毫秒含头尾半开（0=不限）；`/api/v1/tags` 根级 `total`（DISTINCT 去重，前端禁止 count 累加）/`totalFlows`；`GET /tags/{id}/histogram`（buckets 默认 120 上限 500），底图随 tagID+scope 重拉但**恒全域分桶不随窗口变焦、不随关键字/忽略联动**。ReviewTimeline.vue 纯 SVG + pointer 手势状态机（4px 阈值/选区三分区 ±6px/最小 2 桶），拖拽中不刷列表；ReviewApp 用 flowSeq/histSeq 双代际防护。
- **列表分页与关键字（2026-09-09）**：n-pagination 页码分页（非无限滚动），page 1 起、pageSize 默认 100 可选 [50,100,200,500]，limit/offset 传服务端（limit 默认 200 上限 1000）；切标签/scope/时间窗/关键字/页大小统一 `loadFlows(true)`（reset 强制 page=1），翻页 onPageChange（不清屏保留旧行）。关键字 `q` **下沉服务端**：`buildFlowQuery(tagID,scope,start,end,q)` 单一谓词构造点，列表与 CountFlows(total) 同口径——method/host/path 三列 `LIKE ? ESCAPE '\'` OR（整体括号化），likePattern 转义 `\ % _`、TrimSpace 空串不过滤；直方图显式 q=""；q 不匹配 Tags。HTTP GET /tags/{id}/flows 增 q（handler 内 url.Values 局部变量改名 params 避免与 q 混淆）；ReviewApi.listFlows 七参（…,limit,offset,q），HttpApi/DemoApi 同口径（Demo 三列小写子串、不含 Tags、limit/offset 钳制、回显 q）。前端关键字 watch 防抖 300ms。
- **空态与失败回滚（2026-09-09 审后修）**：空态在 winStart 分支后插 `v-else-if="keyword.trim()"` 分支（🔍 + 清除关键字按钮，clearKeyword 取消挂起防抖定时器后由 watcher 统一重拉，避免双请求），否则 q 无匹配会谎报"暂无流"。`loadFlows(reset, restoreOnError=false)` 普通失败仅调用方要求才恢复快照——**只有关键字 watcher 与 onPageSizeChange 传 true**（筛选维度未变），切标签/scope/时间窗默认 false（跨维度恢复=脏数据）；翻页/改页大小失败分别回滚 page/pageSize（以 !fatal 为前提）。
- ReviewApp 三栏（sidebar 220px / list 46% / detail flex1）拖拽调宽：`.splitter` 6px ×2，SIDEBAR_MIN 150/侧栏≤45%/LIST_MIN 320/DETAIL_MIN 360；listW 挂载后按 46% 换算（=0 时 CSS 兜底，fatal 重试成功 nextTick 补测）；window 级 pointermove+setPointerCapture；时间轴 ResizeObserver 自动重测桶数。
- **M12.2 忽略名单与排序**：`review_ignores` 表（kind∈host/path/proc，PK(kind,value)），**忽略=查询排除隐藏不删 flows**；单一谓词点 `buildFlowQuery`（reviewQueryOpts{Q,SortKey,SortDir,ShowIgnored}），!ShowIgnored 且名单非空拼 NOT 排除；Histogram 显式空 opts 不联动。host 四 LIKE（含端口/子域，`%.host` 跨点）、path 精确+下级前缀、proc 走 `json_extract(data,'$.Process.Name')` LOWER 等值；**SQLite BINARY 按字节排序，文本列 ORDER BY 必须包 LOWER()**（flowOrderBy：time 默认 desc、其余 asc，同值 StartedAt desc 兜底）。归一化 NormalizeIgnore*：host 剥端口（:后纯数字）/小写/去尾点去`*.`、path 截 ?# 补前导 /（`/` 拒绝）、proc trim；AddReviewIgnore 先 SELECT 后 INSERT/UPDATE（同毫秒幂等）。读走 reviewReader()（旧归档库可能无表须容错），写走 acquireArchive()+projGen 代际校验。
- **M12.2 HTTP 契约**：`/api/v1/tags/ignores`（ServeMux 精确模式须显式注册；path 非精确委托 handleTagSub，parts[0]=="ignores" 防御 404）：GET→`{ignores:[{kind,value,createdAt,note}]}`、POST `{kind,value,note}`→`{ignore,added}`（幂等）、DELETE `?kind=&value=`（value 可含 "/" 走 query）→`{deleted}`；GET /tags/{id}/flows 增 sort（time|method|status|host|path|size|proc）/dir（asc|desc）/showIgnored（1|true 为真），响应回显。ctlapi Service 保持 HTTP 原语参数（不依赖 persist 包），ctl_bridge 组装 persist.ReviewListOpts；**接口加方法同步 ctlapi_test.go fakeService** 与 wailsjs 三个生成文件（无 json tag→models.ts 字段大写 Q/SortKey/...），即使复盘走 HTTP 也要手动同步防脏 diff。
- **M12.2 前端**：ReviewApi listIgnores/addIgnore/removeIgnore + listFlows 末参 ListFlowsOpts（DemoApi 内存三层 Map 复刻归一化/命中/LOWER 排序/眼睛过滤）。7 列（增进程 fr-proc），`.flow-head` sticky（#101014 不透明）点击排序；右键仅 fr-host/fr-path/fr-proc 出菜单（NDropdown trigger=manual + clientX/Y，CONNECT/空路径/空 ProcessName 禁用），addIgnore 成功同步本地数组（角标实时），仅隐藏态 loadFlows(true,true) 重算。**顶栏小眼睛=忽略名单管理面板**（取代直接切换）：n-popover raw 暗卡 `.ignore-panel`（320px/max 60vh），host→path→proc 再按值排序 + 每项垃圾桶（removingKey 单条 loading）；右上 n-switch「显示被忽略流量」（默认 false，localStorage `prismproxy:review-show-ignored-v1` 持久化，失败回滚 UI+存储）；`.eye-wrap`+`.eye-dot` 规则数角标，有规则/面板开/显示态紫色高亮。ignores 项目级与标签/scope 无关，面板每次打开 loadIgnores，refreshAll Promise.all 并行首拉；loadIgnores/removeIgnore 失败仅 message 不 fatal。侧栏计数/直方图不联动，仅列表+CountFlows。
- **复盘页全屏高度链路**：NConfigProvider 默认 abstract=false 渲染真实 `<div class="n-config-provider">`（NDialogProvider 是 Fragment、NMessageProvider 仅 teleport），插在 #app 与 .review-root 之间；review.css 必须给 `.n-config-provider { height:100% }`，否则整页只占自然高度、下方大片空白。
- **展示层复用 + 复盘重发（方案 A）**：`components/FlowDetailTabs.vue` 是主窗 pages/FlowDetail.vue 与复盘 review/ReviewDetail.vue 共用的四 Tab（概览/请求/响应/TLS）**纯展示组件**——props `{meta:ReviewFlowMeta, detail?, flowId, loader?:BodyLoader, respDisabled?, showEndpoints?}`；wails app.FlowMeta/FlowDetail 与复盘 DTO 同构（大写字段）直接互传；req-copy/resp-copy 具名插槽留给主窗 CopyBar；defineExpose({reloadBodies}) 供主窗 800ms 轮询。两个 Detail 页是薄数据外壳。`fmtCertDate` 兼容 RFC3339 字符串与 unix 秒数（PeerCerts 类型 `string|number`）。
- **复盘重发链路**：`review/ReviewComposer.vue`（主窗 pages/Composer.vue 的 HTTP 版：v-model:show 自管、不碰 pinia/wails、不做 store.select；预填走归档 flowDetail/flowBody，发送走 api.compose）。ReviewApi：`compose`（POST /api/v1/compose，后端零改动）、`liveFlowDetail`（GET /api/v1/flows/{id}）、`liveFlowBody`（GET /api/v1/flows/{id}/body）。**composer 结果流 Source=composer 因代际比对永不落归档库、只进实时 store**，故响应 BodyViewer loader 必须用 live*（归档接口取不到）。`ReviewComposedRequest` = `{method,url,headers:[{key,value}],body,skipVerify}` 小写 json。
- **Demo 模式豁免重发（用户决策，优先于"新接口双实现"默认约定）**：DemoApi 的 compose/liveFlowDetail/liveFlowBody 仅抛错桩；ReviewDetail 在 demo 模式把「调试重发」置 disabled + NTooltip 提示。
- **方案 A 审计 6 minor 全修沉淀**：①**disabled 按钮的 NTooltip 必须外层 span 接管 trigger**（原生 disabled 不派发鼠标事件，`.resend-wrap{display:inline-flex}` 承载 margin-left:auto）；②证书时间 string|number 双形态；③**compose 载荷上限 2MiB**（=ctlapi readBody 2<<20，超限被 LimitReader 静默截断→400 "unexpected end of JSON input"），ReviewComposer 发送前 `new Blob([JSON.stringify(req)]).size` 预检（pages/Composer 走 wails 无此限制）；④预填 watch 加 prefillSeq 代际防护（快速关开丢弃过期预填）；⑤`lib/composer.ts` 共享纯逻辑（COMPOSER_METHOD_OPTIONS/HOP_HEADERS/expandEditableHeaders/COMPOSE_BODY_LIMIT/composerStatusTagType/composerStatusCls），两份 scoped 样式仍各自维护需两处同步。

## M13 复盘 × AI 分析（2026-09-09，仅设计稿 doc/复盘AI分析设计.md，未实施）
- **范围**：复盘页三模式（explain 单接口解读 / locate 自然语言定位接口 / flowmap 业务链路整理）+ 主窗设置抽屉新增全局「AI 分析」tab；一期不做主窗实时入口、结果不落库、不引 mermaid、不做 `cli ai`。
- **AI 配置归属（关键决策）**：全局唯一一份 OpenAI 兼容配置（baseURL/apiKey/model/temperature/timeout/maxFlows/maxKb/redact），挂 `settings.GlobalSettings.AI`（跨项目，与 ADB/持久化同类）；**apiKey 不进 SettingsView 全量 DTO**——独立 `GET/POST /api/v1/ai/config`（GET 只回 hasApiKey+掩码；POST 空串=保持原 key、`__clear__`=清空）；SettingsView.AI 用不含 key 的投影 settings.AISettings，SaveSettings 合并不动已存 key。
- **后端**：新包 `internal/ai`（零外部依赖，net/http + SSE 解析；Stream(ctx,msgs,onDelta)；首块超时 + ctx 整体兜底；出站复用 UpstreamMode 代理装配）；prompt.go 纯函数做三模式 Prompt/双预算裁剪（默认 50 流/64KB）/脱敏（Authorization/Cookie/JWT/密码键/手机号，默认开）。ctlapi 新增 `/api/v1/ai/config`、`/ai/test`（测试连接不落盘）、`/ai/chat`（SSE：自定义帧 meta/delta/match/error/done，**fetch+ReadableStream 带 Bearer**，非 EventSource；全实例并发 1 否则 409；断连 ctx 取消即停上游）；Service 接口加方法同步 fakeService。locate 结构化走单轮 + 文末 ```json 块解析（路线 A，tool-calling 留二期）。
- **前端**：主窗 `pages/settings/AiTab.vue`（全局 tab，不进 PROJECT_TABS；server.go UISettingsTabs 加 "ai"；测试连接走新 Wails 绑定 TestAIConnection，同步 wailsjs 三文件）；复盘新增 `review/ReviewAiPanel.vue`（自绘 560px 右抽屉，非 n-drawer）+ `review/ai-md.ts`（marked + dompurify，**AI HTML 不可信，v-html 仅此一处且必经净化**）；ReviewApi 加 analyze（Http SSE 解析 / Demo 本地定时器模拟流——与 compose 的 demo 禁用相反，AI demo 可玩）；入口：ReviewApp 顶栏「✨AI 分析」+ ReviewDetail 标题栏「AI 解读」；match 卡片点击选中流（一期仅当前已加载页）。新增 npm 依赖 marked、dompurify。
- **安全红线**：apiKey 仅存 config/settings.json（0600），不回传原值、不入 Prompt、不写日志；首次分析告知弹窗（数据外发到 baseURL host）；脱敏关闭每次 popconfirm。

## cURL 复制（BuildCurl binding）
- **单行输出**（Chrome DevTools 形态，杜绝 `^`/`` ` ``/`\` 续行符跨终端问题），三 shell 显式 `curl.exe`（规避 PS 5.1 别名）：cmd/PS 双引号包裹、引号内 `""` 转义（CRT 与 PS 均还原字面引号）；PS 版额外 `` ` ``→`` `` ``、`$`→`` `$ `` 转义；bash 版单引号 `'\''`。body ≤64KB 内联，二进制不内联。CLI `flows curl <id> --shell cmd|powershell|bash`（默认 powershell）。
- 教训：跨 shell 引用规则差异必须逐字实测（`\"` 只在 cmd 有效，PS 中直接闭合字符串）；三版语法互不兼容是本质特性须按终端选菜单；用户贴回的命令可能被聊天框 markdown 渲染篡改，排查先本地逐字节比对。Git for Windows 精简版无 curl。

## 构建与验证
- 前端：`frontend/` 下 `npm run build`（vite build；无独立 type-check 脚本，用 IDE 诊断；**必须 grep dist 特征或诊断核对，vite 退出码 0 不代表可运行**）。
- Go 增量：`go build -trimpath -ldflags "-s -w" -o build\bin\PrismProxy.exe .`（项目根，保留 config）；**不要用 `wails build -clean`**（清 config/ca 换 CA）；改 Go 后 taskkill /F /IM PrismProxy.exe 重起。
- 一键 `build.bat`（项目根，全英文输出）：无参=release（-trimpath -webview2 embed -ldflags "-s -w"，约 16MB）；debug/nsis/upx/clean/help 可组合。clean 已保护 build/bin/config（move 到 _config_keep 再迁回）。构建前 tasklist 检测运行中 exe 报错退出。
- build.bat 两大 cmd 陷阱（已修）：**bat 必须 CRLF 行尾**（LF 导致解析错乱）；**rem 注释禁用 `&`/`|`**（会被当命令分隔符执行，曾误跑根目录残留旧 exe）——根目录绝不放 exe 产物。PS 管道时 wails stderr 被报 NativeCommandError 属误报，以 exit code 为准。
- **版本发布（v1.2 起，2026-09-10）**：版本号在 build.bat `VERSION=x.y.z`（发布前 bump，它只决定 zip 名不写进二进制）；tag 为 annotated（`git tag -a v1.x -F <UTF-8文件>`，中文信息走文件 `-F` 避 PS 编码）。**仓库从不入库二进制**（.gitignore 拦 *.exe/dist/build/bin），编译产物只作为 **GitHub Release 附件**。本机未装 gh CLI：用 `"protocol=https`nhost=github.com" | git credential fill` 取 GCM 缓存 token（40 位，含 repo 权限），PS5.1 调 `https://api.github.com/repos/<o>/<r>/releases` POST 建 Release，再 PUT/POST `https://uploads.github.com/.../releases/{id}/assets?name=` 传 zip（body 用 `[Text.Encoding]::UTF8.GetBytes` + `charset=utf-8` 保中文；PS5.1 Invoke-RestMethod 无 -SkipCertificateCheck，别加）。自动化跑 build.bat：Trae 禁 `cmd /c`，用 `Start-Process .\build.bat -Wait -PassThru -RedirectStandardInput <预建空文件>` 绕过末尾 pause 挂起并拿 ExitCode。发布前必须 taskkill 所有 PrismProxy.exe（GUI+headless，bat 检测到实例直接退出）。历史 tag：v1、v1.1、v1.2。
- 代码风格：注释中文，Naive UI 组件按需 import；scoped 样式，深度选择器 `:deep()`。
- main 包必须留仓库根（go:embed frontend/dist、wails.json）；业务代码全在 internal/app（bindings_*.go 按职责拆分 + ctl_bridge/headless/session_*/webview2_*），导出 main 仅 NewApp/Startup/Shutdown/EnsureWebView2/RunHeadless；wails 绑定路径 wailsjs/go/app/App（包名从 main 改 app 后 13 处 import+23 处类型引用已同步）。
- WebView2 自检：启动前查注册表 `{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}` pv（HKLM WOW6432Node+原生+HKCU），缺失 MessageBox 引导 + rundll32 evergreen bootstrapper。
- 冒烟：独立 smoke/ 目录放 exe（配置落 smoke/config 不污染 build/bin）；headless `-addr 127.0.0.1:9099`；造流用 powershell.exe Invoke-WebRequest（**curl.exe 在快捷忽略进程名单内，经代理流量被过滤不入列表**；区分进程归因可 curl.exe→curl.exe、IWR→powershell.exe）；GUI 验证前先 taskkill 所有实例（另一实例占 9090+9595 会让新 GUI 两端口都不监听）。

## GUI 自动化（MCP Computer Use 操作 WebView2）
- WebView 内容无 a11y 节点于主窗桥接前；桥接后按钮/输入有**独立 id，click id 直接命中**（比 element_id="1"+坐标可靠）；**set_value 是输入首选**（触发 v-model 同步）。
- **每个 MCP 动作（含 set_value/set_focus/RunCommand 外部扰动后）消耗 UI tree revision**，下一步前必须重新 get_app_state，否则 "No cached UI revision"/"Unknown element_index"；id 全量重排不可跨步骤缓存；`diffMode="no-change"` 时 id 不变可复用；流量列表大撑爆 a11y 树→先清空。
- n-select 浮层失焦自动收起：焦点在别的进程时先 perform_action set_focus；`perform_action expand` 对 n-select 不支持，用 set_focus+click。
- 原生文件对话框是同 pid 独立 window（独立 windowId/id 空间），set_value 可填完整绝对路径；**对话框关闭后再 click 报 "No window found for pid" 是动作已生效信号**，不要重试，直接 Test-Path/读文件核验。n-modal 在同一 webview 文档内同树，id 连续。
- 截图：须显式 windowId 指定主窗口（默认可能截到通知小窗）；像素坐标可直接用于 click 但**窗口移动/缩放后映射漂移**，每次拖拽前以最新截图重量坐标。可点中 opacity:0 悬停才显的按钮。
- **MCP 无法合成双击**（clickCount=2 不产生 WM_LBUTTONDBLCLK）；替代 P/Invoke user32（须 ShowWindowAsync+SetForegroundWindow 并校验），WebView2 仍易丢双击，不可靠请用户手点。
- 复制验收硬证据 `Get-Clipboard`（值=展示文本）；toast 1.5s 生存期 MCP 往返 2-4s 抓不到。
- 长连接 CLI（watch）：`Start-Process -RedirectStandardOutput file -WindowStyle Hidden -PassThru` 后台跑、读文件、Stop-Process 收尾（直接阻塞执行会随工具调用结束被杀）。Ctrl+C 退出码自动化：CreateProcess 带 CREATE_NEW_PROCESS_GROUP + GenerateConsoleCtrlEvent(CTRL_BREAK=1)（CTRL_C 无法定向非零进程组；不能加 CREATE_NO_WINDOW 否则信号无处投递）。
