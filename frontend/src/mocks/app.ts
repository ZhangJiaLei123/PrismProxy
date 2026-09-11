// 浏览器预览专用：Wails Go 绑定（window.go.app.App）的内存假后端。
// 仅在纯浏览器（window.go 缺失）时由 browser.ts 安装；wails dev/exe 永远不会加载本文件。
// 所有数据驻留内存（刷新即重置），动作真实改状态并 EventsEmit 对应事件，用于 UI 交互调试。
/* eslint-disable @typescript-eslint/no-explicit-any */
import { emit } from './runtime'

// ---- 工具 ----
function b64(s: string): string {
  // 与 Go []byte 的 Wails JSON 传输一致：base64 字符串
  return btoa(unescape(encodeURIComponent(s)))
}
let seq = 0
function nextID() {
  seq += 1
  return 'mock-' + Date.now().toString(36) + '-' + seq
}
const now = () => Date.now()
const delay = (ms: number) => new Promise((r) => setTimeout(r, ms))

// ---- 示例数据 ----
interface FlowRec {
  meta: any
  detail: any
  reqBody: string
  respBody: string
}

const PROCS = ['chrome.exe', 'msedge.exe', 'WeChat.exe', 'python.exe', 'VBoxNetNAT.exe', 'curl.exe', 'node.exe']
const HOSTS = [
  { host: 'api.example.com', scheme: 'https', status: 200, method: 'GET', path: '/v1/users?page=1', ct: 'application/json; charset=utf-8', json: true },
  { host: 'api.example.com', scheme: 'https', status: 200, method: 'POST', path: '/v1/orders', ct: 'application/json; charset=utf-8', json: true },
  { host: 'cdn.static.net', scheme: 'https', status: 200, method: 'GET', path: '/assets/app.js', ct: 'application/javascript', json: false },
  { host: 'log.metrics.io', scheme: 'http', status: 404, method: 'POST', path: '/collect', ct: 'application/json; charset=utf-8', json: true },
  { host: 'auth.example.com', scheme: 'https', status: 302, method: 'GET', path: '/oauth/authorize?client_id=x', ct: 'text/html', json: false },
  { host: 'api.example.com', scheme: 'https', status: 500, method: 'GET', path: '/v1/profile', ct: 'application/json; charset=utf-8', json: true },
  { host: 'www.bing.com', scheme: 'https', status: 200, method: 'GET', path: '/search?q=wails', ct: 'text/html; charset=utf-8', json: false },
  { host: 'upload.files.dev', scheme: 'https', status: 201, method: 'PUT', path: '/files/abc.txt', ct: 'text/plain', json: false },
]

function makeFlow(t: number, idx: number, idTag: string): FlowRec {
  const id = 'mock-flow-' + idTag + '-' + (1000 + idx)
  const tpl = HOSTS[idx % HOSTS.length]
  const proc = PROCS[idx % PROCS.length]
  const url = `${tpl.scheme}://${tpl.host}${tpl.path}`
  const err = tpl.status >= 500 ? '模拟后端错误（浏览器 Mock）' : ''
  const reqJSON = JSON.stringify({ q: 'demo', page: idx + 1, ts: t }, null, 2)
  const respJSON = JSON.stringify(
    { code: tpl.status, msg: tpl.status >= 400 ? 'mock error' : 'ok', data: { id: idx + 1, name: '示例数据 ' + (idx + 1) } },
    null,
    2,
  )
  const htmlBody = '<!doctype html><html><head><title>Mock</title></head><body><h1>浏览器 Mock 页面</h1></body></html>'
  const reqBody = ['POST', 'PUT'].includes(tpl.method) ? (tpl.json ? reqJSON : 'name=demo&value=' + (idx + 1)) : ''
  const respBody = tpl.json ? respJSON : tpl.ct.includes('html') ? htmlBody : '// mock static content\nconsole.log("hello")\n'
  const meta = {
    ID: id,
    State: err ? 'error' : 'done',
    Scheme: tpl.scheme,
    Method: tpl.method,
    Host: tpl.host,
    Path: tpl.path,
    URL: url,
    Status: tpl.status,
    DurationMS: 20 + ((idx * 37) % 400),
    BytesUp: reqBody.length,
    BytesDown: respBody.length,
    ProcessName: proc,
    PID: 1000 + idx * 13,
    ClientAddr: '127.0.0.1:' + (50000 + idx),
    StartedAt: t,
    Err: err,
    Pinned: idx % 9 === 4,
    Source: 'mock',
    Historical: false,
    Tags: [] as string[], // M12：会话态标签名列表（Go FlowMeta.Tags 无 json tag，运行时为大写 Tags）
  }
  const detail = {
    ...meta,
    // 展开是浅拷贝：Tags 数组会与 meta 共享引用，打标时两处 push 会重复入列，须独立副本
    Tags: [] as string[],
    ReqURL: url,
    ReqProto: 'HTTP/1.1',
    ReqHeader: {
      Host: [tpl.host],
      'User-Agent': ['PrismProxy-Browser-Mock/1.0'],
      Accept: ['*/*'],
      'Content-Type': reqBody ? ['application/json'] : [],
    },
    ReqBodySize: reqBody.length,
    RespProto: 'HTTP/1.1',
    RespHeader: {
      'Content-Type': [tpl.ct],
      Server: ['mock/1.0'],
      'X-Mock': ['PrismProxy browser preview'],
    },
    RespBodySize: respBody.length,
    ProcessPath: 'C:\\mock\\' + proc,
    ServerAddr: '10.0.0.' + ((idx % 20) + 1) + ':443',
    TLS:
      tpl.scheme === 'https'
        ? {
            ClientVersion: 'TLS 1.3',
            ServerVersion: 'TLS 1.3',
            ServerName: tpl.host,
            PeerCerts: [
              {
                Subject: 'CN=' + tpl.host,
                Issuer: 'CN=PrismProxy Mock CA',
                DNSNames: [tpl.host, '*.' + tpl.host.split('.').slice(-2).join('.')],
                NotBefore: now() / 1000 - 86400,
                NotAfter: now() / 1000 + 86400 * 365,
              },
            ],
          }
        : undefined,
  }
  return { meta, detail, reqBody, respBody }
}

// ---- 内存态 ----
const state = {
  projects: [
    { id: 'mock-proj-demo', name: '示例项目' },
    { id: 'mock-proj-test', name: '测试项目' },
  ] as Array<{ id: string; name: string }>,
  currentProjectId: 'mock-proj-demo',
  proxyRunning: true,
  sysProxyOn: false,
  listenAddr: '127.0.0.1:9090',
  flows: [] as FlowRec[],
  ignoreHosts: new Set<string>(),
  ignoreProcs: new Set<string>(),
  ignorePaths: new Set<string>(),
  settings: null as any,
  aiKey: '' as string, // M13：AI API Key（内存态，刷新即重置）
}

function currentProject() {
  return state.projects.find((p) => p.id === state.currentProjectId) ?? null
}

// ---- M12 标签（内存模拟；标签随项目隔离，切换/关闭/删除项目时重置） ----
interface MockTag {
  id: string
  name: string
  createdAt: number
  lastUsedAt: number
}
const tagStore = new Map<string, MockTag>() // key = 标签 id
const tagLinks = new Map<string, Set<string>>() // 标签 id → 已关联流 id（模拟归档库，清空列表不丢）
function resetTags() {
  tagStore.clear()
  tagLinks.clear()
}
function normTagName(name: string) {
  return (name || '').trim().replace(/\s+/g, ' ')
}
// 与后端 id 形态对齐：「t_」+ 归一化名（去全部空白 + lower）哈希前 12 位 hex（mock 用简化哈希，仅预览）
function mockTagID(name: string) {
  const key = name.toLowerCase().replace(/\s+/g, '')
  let h1 = 0x811c9dc5
  let h2 = 0x1000193
  for (let i = 0; i < key.length; i++) {
    const c = key.charCodeAt(i)
    h1 = (Math.imul(h1 ^ c, 0x01000193)) | 0
    h2 = (Math.imul(h2 + c, 31)) | 0
  }
  const hex = ((h1 >>> 0).toString(16) + (h2 >>> 0).toString(16) + '00000000').slice(0, 12)
  return 't_' + hex
}

// 项目级规则（过滤组/解密规则）按项目隔离，支持 CreateProject(fromID) 复制与 SaveSettings 回读
function defaultRules() {
  return {
    filterGroups: [
      {
        id: 'grp-default',
        name: '默认过滤组',
        enabled: true,
        mode: 'include',
        hosts: ['api.example.com', '*.bing.com'],
        paths: ['/v1/'],
        processes: [],
      },
    ],
    decryptRules: [{ action: 'decrypt', host: 'api.example.com' }, { action: 'bypass', host: 'cdn.static.net' }],
  }
}
const projectRules = new Map<string, ReturnType<typeof defaultRules>>()
projectRules.set('mock-proj-demo', defaultRules())
projectRules.set('mock-proj-test', defaultRules())

function rulesFor(projId: string) {
  let r = projectRules.get(projId)
  if (!r) {
    r = defaultRules()
    projectRules.set(projId, r)
  }
  return r
}

function buildSettings(): any {
  const proj = currentProject()
  const rules = proj ? rulesFor(proj.id) : { filterGroups: [], decryptRules: [] }
  const base = {
    listenAddr: state.listenAddr,
    upstreamMode: 'direct',
    upstreamProxy: '',
    maxFlows: 2000,
    maxBodyMB: 256,
    showSysProxySwitch: true,
    autoSysProxy: false,
    bypassList: ['localhost', '127.0.0.1', '*.local'],
    persist: { enabled: false, dbPath: '', retainDays: 7, maxMB: 500 },
    adb: {
      deviceProxyHost: '172.16.1.2',
      configs: [
        { name: '雷电模拟器', path: 'D:\\leidian\\LDPlayer9\\adb.exe', serial: 'emulator-5554', autoSet: true },
      ],
    },
    ai: {
      enabled: false,
      provider: 'custom',
      baseUrl: '',
      model: '',
      temperature: 0.3,
      timeoutSec: 120,
      maxFlows: 50,
      maxKb: 64,
      redact: true,
    },
    filterGroups: rules.filterGroups,
    decryptRules: rules.decryptRules,
    currentProject: proj ?? undefined,
    rulesProject: proj?.id ?? '',
  }
  // 环境字段回读最近一次 SaveSettings（listenAddr/上游/上限/bypass/持久化/ADB）
  const saved = state.settings
  if (!saved) return base
  return {
    ...base,
    upstreamMode: saved.upstreamMode ?? base.upstreamMode,
    upstreamProxy: saved.upstreamProxy ?? base.upstreamProxy,
    maxFlows: saved.maxFlows ?? base.maxFlows,
    maxBodyMB: saved.maxBodyMB ?? base.maxBodyMB,
    showSysProxySwitch: saved.showSysProxySwitch ?? base.showSysProxySwitch,
    autoSysProxy: saved.autoSysProxy ?? base.autoSysProxy,
    bypassList: saved.bypassList ?? base.bypassList,
    persist: saved.persist ?? base.persist,
    adb: saved.adb ?? base.adb,
    ai: saved.ai ?? base.ai,
  }
}

function pushFlow(rec: FlowRec, fireEvent = true) {
  state.flows.unshift(rec)
  if (fireEvent) emit('flow:upsert', [rec.meta])
}

// 初始流：造 24 条，时间戳递减模拟历史；seedTag 保证不同项目/切换批次 ID 不重复
let seedCounter = 0
function seedFlows() {
  const tag = seedCounter++ + '-' + seq
  const t0 = now()
  for (let i = 0; i < 24; i++) {
    state.flows.push(makeFlow(t0 - i * 3000, i, 's' + tag))
  }
}
seedFlows()

// 实时流过滤判定（与引擎 ShouldDisplay 黑名单语义对齐）：
// host=自身+全部子域；proc=精确；path=精确或段边界前缀（Path 去 query）。
// 注意：mock 只过滤"后续新流"，已在列表中的存量流由前端 store 在添加忽略后统一清理
// （真实后端 AddQuickIgnore 同样只热更新引擎、不回溯清理存量流）。
function isIgnored(rec: FlowRec): boolean {
  const host = rec.meta.Host || ''
  // host：去掉末尾端口再做自身+子域比较（与后端 hostOnly / 前端 removeIgnored 口径一致）
  const stripPort = (h: string) => h.replace(/:\d+$/, '').toLowerCase()
  const h0 = stripPort(host)
  if ([...state.ignoreHosts].some((h) => { const d = stripPort(h); return h0 === d || h0.endsWith('.' + d) })) return true
  // 进程：大小写不敏感精确匹配（与后端 EqualFold 一致）
  if (rec.meta.ProcessName && [...state.ignoreProcs].some((p) => p.toLowerCase() === rec.meta.ProcessName.toLowerCase())) return true
  const p = (rec.meta.Path || '').split('?')[0]
  for (const ip of state.ignorePaths) {
    if (p === ip || p.startsWith(ip + '/')) return true
  }
  return false
}

// 实时流：每 8 秒一条模拟抓包（预览列表动效；间隔留足 UI 交互窗口）
let liveSeq = 100
setInterval(() => {
  if (!state.proxyRunning || !state.currentProjectId) return
  const rec = makeFlow(now(), liveSeq++, 'live-' + Date.now())
  if (isIgnored(rec)) return
  pushFlow(rec)
  if (state.flows.length > 2000) state.flows.length = 2000
}, 8000)

function findFlow(id: string): FlowRec | undefined {
  return state.flows.find((f) => f.meta.ID === id)
}

// 事件异步发出：对齐真实 Wails——绑定 Promise resolve 后前端才收到事件
// （若同步 emit，调用方在 await 之后才注册的监听会错过，如 useProjects.ensureSubscribed）
function later(fn: () => void) {
  setTimeout(fn, 0)
}

// ---- App 绑定实现（方法名与 wailsjs/go/app/App.d.ts 对齐） ----
const handlers: Record<string, (...args: any[]) => any> = {
  // --- 项目 ---
  ListProjects: async () => state.projects.map((p) => ({ ...p })),
  GetCurrentProject: async () => {
    const p = currentProject()
    return p ? { ...p } : null
  },
  SwitchProject: async (id: string) => {
    const p = state.projects.find((x) => x.id === id)
    if (!p) throw new Error('项目不存在：' + id)
    const oldIDs = state.flows.map((f) => f.meta.ID)
    state.currentProjectId = id
    state.flows = []
    seedFlows()
    const newMetas = state.flows.map((f) => f.meta)
    // 对齐真实热切换：先 evict 旧项目全部流，再 upsert 新项目历史
    later(() => {
      emit('project:changed')
      emit('flow:evict', oldIDs)
      emit('flow:upsert', newMetas)
    })
  },
  CreateProject: async (name: string, fromID: string) => {
    const oldIDs = state.flows.map((f) => f.meta.ID)
    const proj = { id: nextID(), name }
    state.projects.push(proj)
    // 复制源项目规则（与真实后端一致：复制过滤/解密规则，不复制流量历史）
    projectRules.set(proj.id, fromID ? JSON.parse(JSON.stringify(rulesFor(fromID))) : defaultRules())
    state.currentProjectId = proj.id
    state.flows = []
    later(() => {
      emit('project:changed')
      emit('flow:evict', oldIDs)
    })
    return { ...proj }
  },
  RenameProject: async (id: string, name: string) => {
    const p = state.projects.find((x) => x.id === id)
    if (!p) throw new Error('项目不存在：' + id)
    p.name = name
    later(() => emit('project:changed'))
  },
  DeleteProject: async (id: string) => {
    const i = state.projects.findIndex((x) => x.id === id)
    if (i < 0) throw new Error('项目不存在：' + id)
    state.projects.splice(i, 1)
    projectRules.delete(id)
    let newMetas: any[] = []
    let oldIDs: string[] = []
    if (state.currentProjectId === id) {
      oldIDs = state.flows.map((f) => f.meta.ID)
      state.currentProjectId = state.projects[0]?.id ?? ''
      state.flows = []
      if (state.currentProjectId) {
        seedFlows()
        newMetas = state.flows.map((f) => f.meta)
      }
    }
    later(() => {
      emit('project:changed')
      if (oldIDs.length) {
        emit('flow:evict', oldIDs)
        if (newMetas.length) emit('flow:upsert', newMetas)
      }
    })
  },
  CloseProject: async () => {
    const oldIDs = state.flows.map((f) => f.meta.ID)
    state.currentProjectId = ''
    state.flows = []
    resetTags()
    later(() => {
      emit('project:changed')
      emit('flow:evict', oldIDs)
    })
  },

  // --- 代理 / 系统状态 ---
  GetProxyStatus: async () => ({
    Running: state.proxyRunning,
    Addr: state.listenAddr,
    Mode: 'MITM',
    FlowCount: state.flows.length,
    StartError: '',
  }),
  StartProxy: async (_addr: string) => {
    state.proxyRunning = true
    later(() => emit('proxy:ready'))
  },
  StopProxy: async () => {
    state.proxyRunning = false
  },
  GetSystemProxyStatus: async () => ({
    state: state.sysProxyOn ? 'on' : 'off',
    server: state.sysProxyOn ? state.listenAddr : '',
    override: '',
  }),
  SetSystemProxy: async (on: boolean) => {
    state.sysProxyOn = on
    if (on && !state.proxyRunning) state.proxyRunning = true
  },
  InstallRootCA: async () => {
    await delay(300)
  },

  // --- 设置 ---
  GetSettings: async () => buildSettings(),
  SaveSettings: async (v: any) => {
    if (v.listenAddr) state.listenAddr = v.listenAddr
    // 环境字段整体留存供 GetSettings 回读；规则字段按 rulesProject 写回项目级规则
    state.settings = v
    if (v.rulesProject && projectRules.has(v.rulesProject)) {
      projectRules.set(v.rulesProject, {
        filterGroups: v.filterGroups ?? [],
        decryptRules: v.decryptRules ?? [],
      })
    }
    return { warnings: [] }
  },

  // --- 流 ---
  ListFlows: async () => state.flows.map((f) => f.meta),
  ClearFlows: async () => {
    state.flows = state.flows.filter((f) => f.meta.Pinned)
  },

  // --- M12 标签 / 数据复盘 ---
  // 返回小写字段 json 形态（与 Go TagInfo 的 json tag 一致；wails 绑定不会做 class 实例化）
  ListTags: async () => {
    return [...tagStore.values()]
      .map((t) => ({ id: t.id, name: t.name, count: tagLinks.get(t.id)?.size ?? 0, createdAt: t.createdAt, lastUsedAt: t.lastUsedAt }))
      .sort((a, b) => b.lastUsedAt - a.lastUsedAt)
  },
  TagFlows: async (ids: string[], name: string, autoClear: boolean) => {
    const norm = normTagName(name)
    if (!norm) throw new Error('标签名称不能为空')
    const id = mockTagID(norm)
    let tag = tagStore.get(id)
    if (!tag) {
      tag = { id, name: norm, createdAt: now(), lastUsedAt: now() }
      tagStore.set(id, tag)
      tagLinks.set(id, new Set())
    } else {
      tag.name = norm
      tag.lastUsedAt = now()
    }
    const links = tagLinks.get(id)!
    const uniq = [...new Set(ids)]
    let tagged = 0
    let skipped = 0
    const taggedFlows: FlowRec[] = []
    for (const fid of uniq) {
      const f = findFlow(fid)
      if (!f) {
        // mock 无落盘归档库：内存没有即视为不可得（对齐后端「内存与库中均不可得」skipped）
        skipped++
        continue
      }
      if (!links.has(fid)) {
        links.add(fid)
        tagged++
      }
      if (!f.meta.Tags.includes(tag.name)) {
        f.meta.Tags.push(tag.name)
        if (f.detail && Array.isArray(f.detail.Tags)) f.detail.Tags.push(tag.name)
      }
      taggedFlows.push(f)
    }
    // 对齐后端时序（bindings_tags.go）：先 pendUp 回显打标流（upsert 带最新 Tags），
    // autoClear 时 st.Clear 的 evict 会剔除同 id 待发 upsert——非置顶流消失不复活，置顶流保留
    const pinnedFlows = taggedFlows.filter((f) => f.meta.Pinned)
    later(() => {
      if (autoClear) {
        emit('flow:upsert', pinnedFlows.map((f) => f.meta))
        const evictIDs = taggedFlows.filter((f) => !f.meta.Pinned).map((f) => f.meta.ID)
        emit('flow:evict', evictIDs)
        state.flows = state.flows.filter((f) => f.meta.Pinned)
      } else {
        emit('flow:upsert', taggedFlows.map((f) => f.meta))
      }
    })
    return {
      tag: { id: tag.id, name: tag.name, count: links.size, createdAt: tag.createdAt, lastUsedAt: tag.lastUsedAt },
      tagged,
      archived: tagged, // mock 内存即「库」：关联成功即归档成功
      skipped,
    }
  },
  // 设计 §7：mock 环境复盘页走 Vite dev 同源打开（主窗 window.open）；无 ctlapi/真实 token
  GetReviewURL: async () => '/review.html?token=mock',
  GetFlowDetail: async (id: string) => {
    const f = findFlow(id)
    if (!f) throw new Error('流不存在或已过期')
    return JSON.parse(JSON.stringify(f.detail))
  },
  GetFlowBody: async (id: string, part: string) => {
    const f = findFlow(id)
    if (!f) return { Encoding: '', ContentType: '', Truncated: false, Raw: '', Body: '', DecodeErr: '' }
    const body = part === 'req' ? f.reqBody : f.respBody
    const ct = part === 'req' ? 'application/json' : (f.detail.RespHeader['Content-Type']?.[0] ?? 'application/octet-stream')
    return { Encoding: '', ContentType: ct, Truncated: false, Raw: b64(body), Body: b64(body), DecodeErr: '' }
  },
  GetFlowRawText: async (id: string, part: string, kind: string) => {
    const f = findFlow(id)
    if (!f) throw new Error('流不存在或已过期')
    const body = part === 'req' ? f.reqBody : f.respBody
    const headers = part === 'req' ? f.detail.ReqHeader : f.detail.RespHeader
    const head = Object.entries(headers)
      .filter(([, v]) => (v as string[]).length)
      .map(([k, v]) => `${k}: ${(v as string[]).join(', ')}`)
      .join('\n')
    if (kind === 'headers') return head
    if (kind === 'body') return body
    return head + '\n\n' + body
  },
  SetFlowPinned: async (id: string, pinned: boolean) => {
    const f = findFlow(id)
    if (f) {
      f.meta.Pinned = pinned
      f.detail.Pinned = pinned
      emit('flow:upsert', [f.meta])
    }
  },
  BuildCurl: async (id: string, shell: string) => {
    const f = findFlow(id)
    if (!f) throw new Error('流不存在')
    // 仅浏览器 mock 预览用的简化形态：DevTools 风多行，cmd ^" / bash 单引号
    const q = shell === 'cmd' ? '^"' : "'"
    const sep = shell === 'cmd' ? ' ^\n  ' : ' \\\n  '
    let cmd = `curl --url ${q}${f.meta.URL}${q}`
    if (f.reqBody) cmd += sep + `--data-raw ${q}${f.reqBody}${q}`
    return { command: cmd, bodyOmitted: false }
  },
  AddQuickIgnore: async (kind: string, value: string) => {
    if (kind === 'path') {
      // 与后端 normalizeQuickIgnorePath 对齐：去 query/fragment、补前导 /，拒绝裸 "/"
      let p = (value || '').trim().split(/[?#]/)[0]
      if (p && !p.startsWith('/')) p = '/' + p
      if (!p || p === '/') throw new Error('忽略内容无效：路径不能为空或为 /')
      if (state.ignorePaths.has(p)) return false
      state.ignorePaths.add(p)
      return true
    }
    const set = kind === 'host' ? state.ignoreHosts : state.ignoreProcs
    if (set.has(value)) return false
    set.add(value)
    return true
  },

  // --- Composer ---
  SendComposed: async (req: any) => {
    await delay(400)
    const host = (() => {
      try {
        return new URL(req.url).host
      } catch {
        return 'invalid.url'
      }
    })()
    const ok = !/error/i.test(req.url)
    const rec = makeFlow(now(), liveSeq++, 'compose-' + Date.now())
    rec.meta.ID = nextID()
    rec.meta.Method = (req.method || 'GET').toUpperCase()
    rec.meta.Host = host
    rec.meta.Scheme = req.url.startsWith('https') ? 'https' : 'http'
    rec.meta.URL = req.url
    rec.meta.Path = (() => {
      try {
        return new URL(req.url).pathname
      } catch {
        return '/'
      }
    })()
    rec.meta.Status = ok ? 200 : 502
    rec.meta.State = ok ? 'done' : 'error'
    rec.meta.Err = ok ? '' : '模拟发送失败（URL 含 error）'
    rec.meta.ProcessName = 'composer'
    rec.detail = { ...rec.detail, ...rec.meta, ReqURL: req.url, ReqHeader: { Host: [host] }, RespHeader: { 'Content-Type': ['application/json'] } }
    rec.reqBody = req.body || ''
    rec.respBody = JSON.stringify({ code: 200, msg: 'mock composed response', echo: req.body ?? null }, null, 2)
    pushFlow(rec)
    return JSON.parse(JSON.stringify(rec.detail))
  },

  // --- 域名组 ---
  ListDomainGroupDetails: async () => [
    { id: 'cn-domains', name: '国内常用域名', category: '内置', count: 128, custom: false },
    { id: 'google', name: 'Google 服务', category: '内置', count: 56, custom: false },
    { id: 'my-rules', name: '我的规则', category: '自定义', count: 12, custom: true },
  ],
  ListDomainGroups: async () => ({
    names: ['cn-domains', 'google', 'my-rules'],
    meta: [
      { id: 'cn-domains', title: '国内常用域名' },
      { id: 'google', title: 'Google 服务' },
      { id: 'my-rules', title: '我的规则' },
    ],
    titles: { 'cn-domains': '国内常用域名', google: 'Google 服务', 'my-rules': '我的规则' },
  }),
  GetDomainGroupText: async (id: string) => `# ${id}\nexample.com\napi.example.com\ncdn.example.net\n`,
  SaveDomainGroupText: async (id: string, _text: string) => ({ id, count: 3 }),
  DeleteDomainGroup: async (_id: string) => {},
  ExportDomainGroup: async (id: string) => `C:\\mock-downloads\\${id}.txt`,
  ExportRules: async (_embed: boolean) => 'C:\\mock-downloads\\rules.json',
  ImportRules: async (_path: string) => ({ warnings: [] }),
  ImportDomainGroupFile: async (id: string) => ({ id: id || 'imported-file', count: 8 }),
  ImportDomainGroupURL: async (_url: string, id: string) => ({ id: id || 'imported-url', count: 15 }),
  ImportDomainGroupsFromIndex: async (_url: string, ids: string[], _overwrite: boolean) =>
    ids.map((id) => ({ id, count: 20, skipped: false })),
  ProbeURLImport: async (url: string) => {
    if (/index/i.test(url)) {
      return {
        kind: 'index',
        entries: [
          { id: 'index-group-a', file: 'a.txt', name: '索引组 A', category: '索引' },
          { id: 'index-group-b', file: 'b.txt', name: '索引组 B', category: '索引' },
        ],
      }
    }
    return { kind: 'group' }
  },

  // --- 网络 / ADB / 进程 ---
  GetLocalAddrs: async () => ['127.0.0.1', '192.168.1.100', '172.16.1.1'],
  FindFreePort: async (_ip: string, port: number) => port,
  ListSystemProcesses: async () => [...PROCS, 'explorer.exe', 'svchost.exe', 'Code.exe', 'firefox.exe'],
  PickAdbPath: async () => 'D:\\leidian\\LDPlayer9\\adb.exe',
  AdbTest: async (_path: string) => {
    await delay(300)
    return '模拟器已连接：emulator-5554（Mock）'
  },
  AdbSetProxy: async (_path: string, _serial: string, _addr: string) => '设备代理已设置（Mock）',
  AdbClearProxy: async (_path: string, _serial: string) => '设备代理已清除（Mock）',

  AddDecryptBypass: async (_host: string) => {},

  // --- M13 AI 分析（AiTab 密钥独立存取 + 测试连接，设计稿 §六） ---
  aiView: () => {
    const ai = state.settings?.ai
    return {
      hasApiKey: !!state.aiKey,
      apiKeyMasked: state.aiKey ? (state.aiKey.length <= 8 ? '****' : '****' + state.aiKey.slice(-4)) : '',
      // 对齐 Go WarnNoKey：仅「已启用 + 无 key + 非 ollama」才告警（审计低-2）
      keyMissingWarn:
        ai?.enabled && !state.aiKey && ai.provider !== 'ollama' ? '未配置 API Key：AI 分析不可用（Mock）' : '',
    }
  },
  GetAIConfigApp: async () => handlers.aiView(),
  SaveAIConfigApp: async (key: string) => {
    // 哨兵语义与后端一致：空串=保持、__clear__=清除、传值=换 key
    if (key === '__clear__') state.aiKey = ''
    else if (key.trim()) state.aiKey = key.trim()
    return handlers.aiView()
  },
  TestAIConnection: async (cfg: any) => {
    await delay(400)
    const url = String(cfg?.baseUrl ?? '')
    // 对齐 Go aiProbe 哨兵：BaseURL/模型缺失走 error（reject），非正常业务结果（审计低-3）
    if (!url.trim() || !String(cfg?.model ?? '').trim()) {
      throw new Error('请先配置 AI 接口地址与模型')
    }
    if (url.includes('error')) {
      return { model: cfg.model, latencyMs: 0, ok: false, message: '连接失败：无法访问 BaseURL（Mock）' }
    }
    return { model: cfg.model, latencyMs: 386, ok: true, message: '' }
  },
}

// Proxy 兜底：未实现的方法不崩页面，返回空值并告警
export function installAppMock() {
  const App = new Proxy(
    {},
    {
      get(_t, prop: string) {
        if (prop in handlers) return handlers[prop]
        return async (...args: any[]) => {
          console.warn(`[mock-app] 未实现的绑定 App.${String(prop)}，参数：`, args)
          return null
        }
      },
    },
  )
  ;(window as any).go = { app: { App } }
}
