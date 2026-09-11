// 浏览器预览数据层（无 token / fetch 失败降级）：内置 demo 数据，内存模拟 rename/delete，
// 服务端查询/排序/忽略口径均在此 JS 复刻（设计 §7），UI 可脱离后端独立调试。
import type {
  AiMatchItem,
  IntentResult,
  ReviewBodyPayload,
  ReviewFlowDetail,
  ReviewFlowMeta,
  ReviewHistogram,
  ReviewIgnoreAddResult,
  ReviewIgnoreItem,
  ReviewIgnoreKind,
  ReviewScope,
  ReviewSortDir,
  ReviewSortKey,
  ReviewTagInfo,
  ReviewTagsOverview,
} from '../../lib/types'
import { ApiError } from './error'
import type {
  AIApiConfigView,
  AiChatEvent,
  AiChatMeta,
  AiChatMode,
  AiChatRequest,
  ApiMode,
  ListFlowsOpts,
  ReviewApi,
  TagFlowsResp,
} from './types'

function b64(s: string): string {
  return btoa(unescape(encodeURIComponent(s)))
}

interface DemoDB {
  tags: ReviewTagInfo[]
  flows: ReviewFlowMeta[]
  details: Map<string, ReviewFlowDetail>
  bodies: Map<string, { req: string; resp: string }>
}

interface DemoTpl {
  method: string
  host: string
  path: string
  status: number
  scheme: string
  ct: string
  tag: string // 标签名；'' = 未打标（自动录制落库流，仅 scope=all 可见）
  proc: string
  agoMin: number
}

function demoData(): DemoDB {
  const now = Date.now()
  const mkTag = (id: string, name: string, count: number, agoMin: number): ReviewTagInfo => ({
    id,
    name,
    count,
    createdAt: now - (agoMin + 120) * 60000,
    lastUsedAt: now - agoMin * 60000,
  })
  const tags = [
    mkTag('t_demo_login', '登录流程排查', 3, 5),
    mkTag('t_demo_order', '下单接口', 2, 60 * 26),
    mkTag('t_demo_static', '静态资源', 1, 60 * 72),
  ]
  // 时间分布拉宽到数天（设计 §7）：6 条打标流 + 8 条未打标自动录制流
  const tpl: DemoTpl[] = [
    { method: 'POST', host: 'api.example.com', path: '/v1/auth/login', status: 200, scheme: 'https', ct: 'application/json; charset=utf-8', tag: '登录流程排查', proc: 'WeChat.exe', agoMin: 8 },
    { method: 'GET', host: 'api.example.com', path: '/v1/auth/captcha', status: 200, scheme: 'https', ct: 'application/json; charset=utf-8', tag: '登录流程排查', proc: 'WeChat.exe', agoMin: 40 },
    { method: 'POST', host: 'api.example.com', path: '/v1/auth/login', status: 401, scheme: 'https', ct: 'application/json; charset=utf-8', tag: '登录流程排查', proc: 'WeChat.exe', agoMin: 60 * 3 },
    { method: 'POST', host: 'api.example.com', path: '/v1/orders', status: 201, scheme: 'https', ct: 'application/json; charset=utf-8', tag: '下单接口', proc: 'chrome.exe', agoMin: 60 * 26 },
    { method: 'GET', host: 'api.example.com', path: '/v1/orders/10086', status: 200, scheme: 'https', ct: 'application/json; charset=utf-8', tag: '下单接口', proc: 'chrome.exe', agoMin: 60 * 30 },
    { method: 'GET', host: 'cdn.static.net', path: '/assets/app.js', status: 200, scheme: 'https', ct: 'application/javascript', tag: '静态资源', proc: 'chrome.exe', agoMin: 60 * 72 },
    // 未打标流（自动录制落库）
    { method: 'GET', host: 'api.example.com', path: '/v1/home/banners', status: 200, scheme: 'https', ct: 'application/json', tag: '', proc: 'WeChat.exe', agoMin: 15 },
    { method: 'GET', host: 'api.example.com', path: '/v1/home/feed', status: 200, scheme: 'https', ct: 'application/json', tag: '', proc: 'WeChat.exe', agoMin: 90 },
    { method: 'POST', host: 'api.example.com', path: '/v1/metrics/report', status: 204, scheme: 'https', ct: 'application/json', tag: '', proc: 'WeChat.exe', agoMin: 60 * 6 },
    { method: 'GET', host: 'img.example.com', path: '/avatar/1001.jpg', status: 200, scheme: 'https', ct: 'image/jpeg', tag: '', proc: 'WeChat.exe', agoMin: 60 * 20 },
    { method: 'GET', host: 'api.example.com', path: '/v1/config/bootstrap', status: 200, scheme: 'https', ct: 'application/json', tag: '', proc: 'WeChat.exe', agoMin: 60 * 40 },
    { method: 'GET', host: 'cdn.static.net', path: '/assets/vendor.js', status: 200, scheme: 'https', ct: 'application/javascript', tag: '', proc: 'chrome.exe', agoMin: 60 * 55 },
    { method: 'POST', host: 'api.example.com', path: '/v1/log/collect', status: 500, scheme: 'https', ct: 'application/json', tag: '', proc: 'WeChat.exe', agoMin: 60 * 66 },
    { method: 'GET', host: 'api.example.com', path: '/v1/health', status: 200, scheme: 'http', ct: 'application/json', tag: '', proc: 'healthcheck.exe', agoMin: 60 * 90 },
  ]
  const flows: ReviewFlowMeta[] = []
  const details = new Map<string, ReviewFlowDetail>()
  const bodies = new Map<string, { req: string; resp: string }>()
  tpl.forEach((t, i) => {
    const id = 'demo-flow-' + (i + 1)
    const startedAt = now - t.agoMin * 60000
    const reqBody = t.path.includes('login')
      ? JSON.stringify({ account: '138****0000', password: '******', device: 'demo' }, null, 2)
      : t.method === 'POST'
        ? JSON.stringify({ skuId: 1000 + i, qty: 1 }, null, 2)
        : ''
    const respBody = t.status === 401
      ? JSON.stringify({ code: 401, msg: '账号或密码错误' }, null, 2)
      : JSON.stringify({ code: t.status, msg: 'ok（演示数据）', data: { id: i + 1, ts: startedAt } }, null, 2)
    const meta: ReviewFlowMeta = {
      ID: id,
      State: t.status >= 500 ? 'error' : 'done',
      Scheme: t.scheme,
      Method: t.method,
      Host: t.host,
      Path: t.path,
      URL: `${t.scheme}://${t.host}${t.path}`,
      Status: t.status,
      DurationMS: 35 + i * 17,
      BytesUp: reqBody.length,
      BytesDown: respBody.length,
      ProcessName: t.proc,
      PID: 4000 + i * 11,
      ClientAddr: '127.0.0.1:' + (51000 + i),
      StartedAt: startedAt,
      Err: '',
      Pinned: false,
      Source: 'history',
      Historical: true,
      Tags: t.tag ? [t.tag] : [],
    }
    flows.push(meta)
    const detail: ReviewFlowDetail = {
      ...meta,
      ReqURL: meta.URL,
      ReqProto: 'HTTP/1.1',
      ReqHeader: {
        Host: [t.host],
        'User-Agent': ['PrismProxy-Review-Demo/1.0'],
        Accept: ['*/*'],
        Authorization: ['Bearer eyJ***（演示）'],
        'Content-Type': reqBody ? ['application/json'] : [],
      },
      ReqBodySize: reqBody.length,
      RespProto: 'HTTP/1.1',
      RespHeader: { 'Content-Type': [t.ct], Server: ['demo/1.0'], 'X-Mock': ['PrismProxy review demo'] },
      RespBodySize: respBody.length,
      ProcessPath: 'C:\\demo\\' + t.proc,
      ServerAddr: '10.0.0.' + (i + 1) + ':443',
      TLS: t.scheme === 'https'
        ? {
            ClientVersion: 'TLS 1.3',
            ServerVersion: 'TLS 1.3',
            ServerName: t.host,
            PeerCerts: [
              { Subject: 'CN=' + t.host, Issuer: 'CN=PrismProxy Demo CA', DNSNames: [t.host], NotBefore: now / 1000 - 86400, NotAfter: now / 1000 + 86400 * 365 },
            ],
          }
        : null,
    }
    details.set(id, detail)
    bodies.set(id, { req: reqBody, resp: respBody })
  })
  return { tags, flows, details, bodies }
}

export class DemoApi implements ReviewApi {
  mode: ApiMode = 'demo'
  private db: DemoDB = demoData()
  // M12.2 忽略名单（内存模拟 review_ignores 表；key=kind|value，已归一化）
  private ignores = new Map<ReviewIgnoreKind, Map<string, ReviewIgnoreItem>>([
    ['host', new Map()],
    ['path', new Map()],
    ['proc', new Map()],
    ['method', new Map()],
    ['status', new Map()],
  ])

  // 归一化复刻 persist.NormalizeIgnore*（与服务端同口径）：
  // host 去端口/小写/去尾点/去 *. 前缀；path 截 ?# 并补前导 /（'/' 拒绝）；proc trim。
  private normHost(v: string): string {
    let h = v.trim().toLowerCase()
    const c = h.lastIndexOf(':')
    // 仅在端口形态（:后纯数字）时剥离，IPv6 形态在本演示数据不会出现
    if (c >= 0 && /^\d+$/.test(h.slice(c + 1))) h = h.slice(0, c)
    h = h.replace(/\.+$/, '').replace(/^\*\./, '')
    if (!h) throw new ApiError('http', '域名不能为空')
    return h
  }

  private normPath(v: string): string {
    let p = v.trim()
    const qi = Math.min(...['?', '#'].map((c) => { const i = p.indexOf(c); return i < 0 ? Infinity : i }))
    if (qi !== Infinity) p = p.slice(0, qi)
    p = p.trim()
    if (!p.startsWith('/')) p = '/' + p
    if (p === '/') throw new ApiError('http', '不能忽略整站根路径')
    return p
  }

  private normMethod(v: string): string {
    const m = v.trim().toUpperCase()
    if (!m) throw new ApiError('http', '方法不能为空')
    return m
  }

  private normStatus(v: string): string {
    const s = v.trim()
    if (!/^\d{3}$/.test(s) || Number(s) < 100 || Number(s) > 599) {
      throw new ApiError('http', '状态码无效（须为 100–599 的三位整数）')
    }
    return s
  }

  // 按 kind 归一化（与 addIgnore/removeIgnore 共用，保证删除口径与存储一致）
  private normIgnore(kind: ReviewIgnoreKind, value: string): string {
    if (kind === 'host') return this.normHost(value)
    if (kind === 'path') return this.normPath(value)
    if (kind === 'method') return this.normMethod(value)
    if (kind === 'status') return this.normStatus(value)
    return value.trim()
  }

  // 单条忽略是否命中一行（SQL 排除条件的 JS 复刻）
  private rowIgnored(f: ReviewFlowMeta): boolean {
    const hostHit = (h: string): boolean => {
      const host = f.Host.toLowerCase()
      const colon = host.indexOf(':')
      const bare = colon >= 0 ? host.slice(0, colon) : host
      return bare === h || bare.endsWith('.' + h)
    }
    const pathHit = (p: string): boolean => f.Path === p || f.Path.startsWith(p + '/')
    for (const h of this.ignores.get('host')!.keys()) if (hostHit(h)) return true
    for (const p of this.ignores.get('path')!.keys()) if (pathHit(p)) return true
    const proc = (f.ProcessName || '').toLowerCase()
    for (const pn of this.ignores.get('proc')!.keys()) if (proc && proc === pn.toLowerCase()) return true
    const method = (f.Method || '').toLowerCase()
    for (const mth of this.ignores.get('method')!.keys()) if (method && method === mth.toLowerCase()) return true
    // 状态 0=无响应/错误流，与任何 100–599 名单不等，不会被误伤
    for (const code of this.ignores.get('status')!.keys()) if (f.Status && String(f.Status) === code) return true
    return false
  }

  // 排序复刻 flowOrderBy：文本列按小写比较；time 默认 desc、其余列默认 asc；
  // 同值按 StartedAt desc + ID 兜底。
  private sortRows(rows: ReviewFlowMeta[], sort?: ReviewSortKey, dir?: ReviewSortDir): ReviewFlowMeta[] {
    const key: ReviewSortKey = sort ?? 'time'
    const wantDir: ReviewSortDir = dir ?? (key === 'time' ? 'desc' : 'asc')
    const cmpText = (a: string, b: string): number => a.toLowerCase().localeCompare(b.toLowerCase())
    const valueOf = (f: ReviewFlowMeta): number | string => {
      switch (key) {
        case 'method': return f.Method
        case 'status': return f.Status
        case 'host': return f.Host
        case 'path': return f.Path
        case 'size': return f.BytesDown
        case 'proc': return f.ProcessName || ''
        default: return f.StartedAt
      }
    }
    return [...rows].sort((a, b) => {
      if (key === 'time') {
        const d = b.StartedAt - a.StartedAt
        return wantDir === 'asc' ? -d : d
      }
      const va = valueOf(a)
      const vb = valueOf(b)
      let c = typeof va === 'number' && typeof vb === 'number' ? va - vb : cmpText(String(va), String(vb))
      if (wantDir === 'desc') c = -c
      if (c !== 0) return c
      return b.StartedAt - a.StartedAt
    })
  }

  // selectRows 复刻服务端谓词：tagID=all 时 archived 仅打标流 / all 全量；
  // 具体标签恒为该标签流（scope 忽略）；start/end 含头尾、0=不限（支持半开）；
  // q 在 method/host/path 三列做小写子串匹配（对应服务端 LIKE 的 ASCII 大小写不敏感）；
  // showIgnored=false 时套用忽略名单（与 buildFlowQuery 同口径）。
  private selectRows(
    tagID: string,
    scope: ReviewScope,
    start: number,
    end: number,
    q = '',
    showIgnored = false,
  ): ReviewFlowMeta[] {
    let rows: ReviewFlowMeta[]
    if (tagID === 'all') {
      rows = scope === 'all' ? [...this.db.flows] : this.db.flows.filter((f) => f.Tags.length > 0)
    } else {
      const t = this.db.tags.find((x) => x.id === tagID)
      rows = t ? this.db.flows.filter((f) => f.Tags.includes(t.name)) : []
    }
    if (start > 0) rows = rows.filter((f) => f.StartedAt >= start)
    if (end > 0) rows = rows.filter((f) => f.StartedAt <= end)
    const kw = q.trim().toLowerCase()
    if (kw) {
      rows = rows.filter(
        (f) =>
          f.Method.toLowerCase().includes(kw) ||
          f.Host.toLowerCase().includes(kw) ||
          f.Path.toLowerCase().includes(kw),
      )
    }
    if (!showIgnored) rows = rows.filter((f) => !this.rowIgnored(f))
    return rows
  }

  async listTags(): Promise<ReviewTagsOverview> {
    const tags = [...this.db.tags].sort((a, b) => b.lastUsedAt - a.lastUsedAt)
    const total = new Set(this.db.flows.filter((f) => f.Tags.length > 0).map((f) => f.ID)).size
    return { tags, total, totalFlows: this.db.flows.length }
  }

  async listFlows(
    tagID: string,
    scope: ReviewScope,
    start: number,
    end: number,
    limit: number,
    offset: number,
    q: string,
    opts?: ListFlowsOpts,
  ): Promise<TagFlowsResp> {
    const rows = this.selectRows(tagID, scope, start, end, q, opts?.showIgnored ?? false)
    const sorted = this.sortRows(rows, opts?.sort, opts?.dir)
    const lim = limit <= 0 ? 200 : Math.min(limit, 1000)
    const off = Math.max(0, offset)
    return {
      flows: sorted.slice(off, off + lim),
      total: sorted.length,
      tag: tagID,
      scope,
      start,
      end,
      limit: lim,
      offset: off,
      q,
      sort: opts?.sort,
      dir: opts?.dir,
      showIgnored: opts?.showIgnored ?? false,
    }
  }

  async listIgnores(): Promise<ReviewIgnoreItem[]> {
    const out: ReviewIgnoreItem[] = []
    for (const m of this.ignores.values()) for (const it of m.values()) out.push(it)
    return out.sort((a, b) => b.createdAt - a.createdAt)
  }

  async addIgnore(kind: ReviewIgnoreKind, value: string, note = ''): Promise<ReviewIgnoreAddResult> {
    const m = this.ignores.get(kind)
    if (!m) throw new ApiError('http', '非法忽略类型（仅支持 host/path/proc/method/status）')
    const v = this.normIgnore(kind, value)
    if (!v) throw new ApiError('http', '忽略值不能为空')
    const existing = m.get(v)
    if (existing) {
      existing.note = note
      return { ignore: { ...existing }, added: false }
    }
    const item: ReviewIgnoreItem = { kind, value: v, createdAt: Date.now(), note }
    m.set(v, item)
    return { ignore: { ...item }, added: true }
  }

  async removeIgnore(kind: ReviewIgnoreKind, value: string): Promise<boolean> {
    const v = this.normIgnore(kind, value)
    return this.ignores.get(kind)?.delete(v) ?? false
  }

  async histogram(
    tagID: string,
    scope: ReviewScope,
    winStart: number,
    winEnd: number,
    buckets: number,
  ): Promise<ReviewHistogram> {
    let n = buckets <= 0 ? 120 : Math.min(buckets, 500)
    const rows = this.selectRows(tagID, scope, winStart, winEnd)
    if (rows.length === 0) {
      // 与服务端一致：无窗口空库返空数组；带窗口返窗口形状全零桶
      if (winStart > 0 && winEnd > 0) {
        return { start: winStart, end: winEnd, buckets: this.emptyBuckets(winStart, winEnd, n) }
      }
      return { start: 0, end: 0, buckets: [] }
    }
    let t0: number
    let t1: number
    if (winStart > 0 && winEnd > 0) {
      t0 = winStart
      t1 = winEnd
    } else {
      t0 = Math.min(...rows.map((f) => f.StartedAt))
      t1 = Math.max(...rows.map((f) => f.StartedAt))
      if (winStart > 0) t0 = winStart
      if (winEnd > 0) t1 = winEnd
    }
    if (t1 === t0) {
      return { start: t0, end: t1, buckets: [{ t0, t1, count: rows.length }] }
    }
    const span = t1 - t0
    let width = Math.floor(span / n)
    if (span % n !== 0) width++
    n = Math.min(Math.floor(span / width) + 1, n)
    const out = this.emptyBuckets(t0, t1, n, width)
    for (const f of rows) {
      let b = Math.floor((f.StartedAt - t0) / width)
      if (b < 0) b = 0
      if (b >= n) b = n - 1
      out[b].count++
    }
    out[n - 1].t1 = t1
    return { start: t0, end: t1, buckets: out }
  }

  private emptyBuckets(t0: number, t1: number, n: number, width?: number): { t0: number; t1: number; count: number }[] {
    const span = t1 - t0
    const w = width ?? (span > 0 ? Math.ceil(span / n) : 1)
    const out: { t0: number; t1: number; count: number }[] = []
    for (let i = 0; i < n; i++) out.push({ t0: t0 + i * w, t1: t0 + (i + 1) * w, count: 0 })
    if (out.length) out[n - 1].t1 = t1
    return out
  }

  async flowDetail(flowID: string): Promise<ReviewFlowDetail> {
    const d = this.db.details.get(flowID)
    if (!d) throw new ApiError('http', '演示流不存在或已被删除')
    return d
  }

  async flowBody(flowID: string, which: 'req' | 'resp'): Promise<ReviewBodyPayload> {
    const d = this.db.details.get(flowID)
    if (!d) throw new ApiError('http', '演示流不存在')
    const raw = this.db.bodies.get(flowID)?.[which] ?? ''
    return {
      Encoding: '',
      ContentType: which === 'req' ? (d.ReqBodySize ? 'application/json' : '') : (d.RespHeader['Content-Type']?.[0] ?? 'application/octet-stream'),
      Truncated: false,
      Raw: b64(raw),
      Body: b64(raw),
      DecodeErr: '',
    }
  }

  // 调试重发仅真实环境可用（用户决策：DemoApi 不实现，按钮禁用；桩保接口同口径）
  async compose(): Promise<ReviewFlowDetail> {
    throw new ApiError('http', '演示模式不支持调试重发，仅真实环境可用')
  }

  async liveFlowDetail(): Promise<ReviewFlowDetail> {
    throw new ApiError('http', '演示模式无实时流数据')
  }

  async liveFlowBody(): Promise<ReviewBodyPayload> {
    throw new ApiError('http', '演示模式无实时流数据')
  }

  async renameTag(tagID: string, name: string): Promise<void> {
    const t = this.db.tags.find((x) => x.id === tagID)
    if (!t) throw new ApiError('http', '演示标签不存在')
    const target = this.db.tags.find((x) => x.name.trim() === name.trim() && x.id !== tagID)
    if (target) {
      // 合并：流的标签名改到目标，计数合并
      for (const f of this.db.flows) {
        const i = f.Tags.indexOf(t.name)
        if (i >= 0) {
          f.Tags.splice(i, 1)
          if (!f.Tags.includes(target.name)) f.Tags.push(target.name)
        }
      }
      target.count += t.count
      target.lastUsedAt = Date.now()
      this.db.tags = this.db.tags.filter((x) => x.id !== tagID)
    } else {
      for (const f of this.db.flows) {
        const i = f.Tags.indexOf(t.name)
        if (i >= 0) f.Tags[i] = name.trim()
      }
      t.name = name.trim()
      t.lastUsedAt = Date.now()
    }
  }

  async deleteTag(tagID: string, deleteFlows: boolean): Promise<{ id: string; deletedFlows: number }> {
    const t = this.db.tags.find((x) => x.id === tagID)
    if (!t) throw new ApiError('http', '演示标签不存在')
    const linked = this.db.flows.filter((f) => f.Tags.includes(t.name))
    let deletedFlows = 0
    if (deleteFlows) {
      // 与服务端口径一致：仅删「不再被任何标签引用」的流（共享流保留）
      const ids = new Set(
        linked
          .filter((f) => f.Tags.length === 1 && f.Tags[0] === t.name)
          .map((f) => f.ID),
      )
      this.db.flows = this.db.flows.filter((f) => !ids.has(f.ID))
      for (const id of ids) {
        this.db.details.delete(id)
        this.db.bodies.delete(id)
      }
      deletedFlows = ids.size
    } else {
      for (const f of linked) f.Tags = f.Tags.filter((x) => x !== t.name)
    }
    this.db.tags = this.db.tags.filter((x) => x.id !== tagID)
    return { id: tagID, deletedFlows }
  }

  // ===== AI 分析（M13 P4 demo 桩：不发网络，定时器模拟 SSE 事件序列）=====

  async analyze(req: AiChatRequest, onEvent: (ev: AiChatEvent) => void, signal?: AbortSignal): Promise<void> {
    const mode: AiChatMode = req.mode
    const wanted = mode === 'explain' ? (req.flowId ? [req.flowId] : []) : (req.ids ?? [])
    const flows = wanted
      .map((id) => this.db.flows.find((f) => f.ID === id))
      .filter((f): f is ReviewFlowMeta => !!f)
    if (mode === 'explain' && flows.length === 0) throw new ApiError('http', '演示流不存在或已被删除')
    if (mode !== 'explain' && flows.length === 0) throw new ApiError('http', '当前视图没有可分析的候选流')
    if ((mode === 'locate' || mode === 'flowmap') && !req.question?.trim()) {
      throw new ApiError('http', '请先输入分析目标')
    }
    if (signal?.aborted) return

    // 定时器模拟事件流：abort 清定时器静默收尾（与 HttpApi 中止口径一致）
    const timers: ReturnType<typeof setTimeout>[] = []
    const at = (ms: number, fn: () => void): void => {
      timers.push(setTimeout(fn, ms))
    }
    const clearAll = (): void => {
      for (const t of timers) clearTimeout(t)
      timers.length = 0
    }
    signal?.addEventListener('abort', clearAll, { once: true })

    // meta：送审规模先回显（demo 全量送审，无截断）
    const meta: AiChatMeta = {
      mode,
      total: flows.length,
      sent: flows.length,
      budget: { flows: 20, kb: 512 },
      truncated: false,
    }
    onEvent({ event: 'meta', data: meta })
    const finish = (): void => onEvent({ event: 'done', data: { finishReason: 'stop', truncated: false } })

    if (mode === 'intent') {
      // 每条候选 ~150ms 逐条吐 intent（seq 对应送审序号 [#n]）
      flows.forEach((f, i) => {
        at(120 + i * 150, () => onEvent({ event: 'intent', data: this.demoIntent(f, i + 1) }))
      })
      at(140 + flows.length * 150, finish)
      return
    }

    if (mode === 'locate') {
      const hits = this.demoLocate(flows, req.question!.trim())
      hits.forEach((f, i) => {
        at(150 + i * 200, () =>
          onEvent({
            event: 'match',
            data: {
              flowId: f.ID,
              rank: i + 1,
              method: f.Method,
              url: f.URL,
              reason: this.demoReason(f),
              confidence: f.Tags.length > 0 ? 'high' : 'medium',
            },
          }),
        )
      })
      at(200 + hits.length * 200, finish)
      return
    }

    // explain / flowmap：markdown 分段 delta（每段 ~180ms）
    const segs = mode === 'explain' ? this.demoExplain(flows[0]!) : this.demoFlowmap([...flows].reverse())
    segs.forEach((text, i) => {
      at(150 + i * 180, () => onEvent({ event: 'delta', data: { text } }))
    })
    at(200 + segs.length * 180, finish)
  }

  async getAIConfig(): Promise<AIApiConfigView> {
    // 内存桩：默认已配置可用（面板据此跳过空态提示）
    return {
      enabled: true,
      provider: 'demo',
      baseUrl: 'https://api.demo-llm.example/v1',
      model: 'demo-model',
      temperature: 0.3,
      timeoutSec: 120,
      maxFlows: 20,
      maxKb: 512,
      redact: true,
      hasApiKey: true,
      apiKeyMasked: 'sk-demo****',
    }
  }

  // demo 无外部设置面板：静态提示（设计 §九，空态按钮隐藏改文案）
  async openSettingsAI(): Promise<{ opened: boolean; msg: string }> {
    return { opened: false, msg: '演示模式无需配置，AI 分析走内置模拟数据' }
  }

  // demo：按路径形态推断接口意图（真实实现由模型输出）
  private demoIntent(f: ReviewFlowMeta, seq: number): IntentResult {
    const p = f.Path.toLowerCase()
    let intent = `访问 ${f.Method} ${p}`
    let ok = true
    if (p.includes('login')) intent = '提交登录认证'
    else if (p.includes('captcha')) intent = '获取图形验证码'
    else if (p.includes('orders/')) intent = '查询订单详情'
    else if (p.endsWith('/orders')) intent = f.Method === 'POST' ? '创建订单' : '查询订单列表'
    else if (p.includes('banners') || p.includes('feed')) intent = '拉取首页内容'
    else if (p.includes('metrics') || p.includes('log')) intent = '上报埋点/日志'
    else if (p.includes('config')) intent = '拉取启动配置'
    else if (p.includes('health')) intent = '健康检查探活'
    else if (p.includes('/assets/') || /\.(js|css|png|jpe?g|gif|svg|ico|woff2?)$/.test(p)) intent = '加载静态资源'
    else {
      // 无语义路径：降置信度并提示带正文重析
      ok = false
      intent = `访问 ${f.Method} ${p}（路径无语义，建议带正文重析）`
    }
    return { flowId: f.ID, seq, intent, confidence: ok ? 'high' : 'low', needsBody: !ok }
  }

  // demo：按 question 关键字对候选流打分取前 3 条；零命中兜底最近 2 条
  private demoLocate(flows: ReviewFlowMeta[], question: string): ReviewFlowMeta[] {
    const kws = question
      .toLowerCase()
      .split(/[\s,，。;；、?？!！]+/)
      .filter(Boolean)
    const ranked = flows
      .map((f) => {
        const hay = `${f.Method} ${f.Host} ${f.Path} ${f.Status} ${f.Tags.join(' ')}`.toLowerCase()
        let s = 0
        for (const k of kws) if (hay.includes(k)) s++
        return { f, s }
      })
      .filter((x) => x.s > 0)
      .sort((a, b) => b.s - a.s || b.f.StartedAt - a.f.StartedAt)
      .map((x) => x.f)
    if (ranked.length === 0) return flows.slice(0, 2)
    return ranked.slice(0, 3)
  }

  private demoReason(f: ReviewFlowMeta): string {
    if (f.Status >= 400) return `状态 ${f.Status} 异常，与排查目标相关性最高`
    if (f.Tags.length > 0) return `属于「${f.Tags[0]}」标签，路径直接命中关键字`
    return '路径/方法与目标关键字匹配'
  }

  private demoExplain(f: ReviewFlowMeta): string[] {
    const when = new Date(f.StartedAt).toLocaleString()
    const out = [
      `### 请求概览\n\n- **方法**：${f.Method} ${f.Scheme}://${f.Host}${f.Path}\n- **状态**：${f.Status}，耗时 ${f.DurationMS}ms\n- **进程**：${f.ProcessName}（PID ${f.PID}）\n- **时间**：${when}\n`,
    ]
    if (f.Status >= 400) {
      out.push(
        `### 状态分析\n\n该请求返回 **${f.Status}**，属于${f.Status >= 500 ? '服务端错误' : '客户端错误'}。建议重点检查请求参数与鉴权凭证。\n`,
      )
    } else {
      out.push(`### 状态分析\n\n请求正常返回（${f.Status}），耗时 ${f.DurationMS}ms，响应体 ${f.BytesDown} 字节。\n`)
    }
    if (f.Tags.length > 0) out.push(`### 归属\n\n该流已被打标「${f.Tags.join('、')}」。\n`)
    out.push('> 演示模式说明：以上为内置桩数据解读，接入真实 AI 后由模型输出分析。\n')
    return out
  }

  private demoFlowmap(flows: ReviewFlowMeta[]): string[] {
    // flows 已按时间正序（旧→新）；每 4 步一段 delta 模拟流式输出
    const head = `### 调用流程（按时间正序，共 ${flows.length} 步）\n\n`
    const steps = flows.map((f, i) => {
      const st = f.Status >= 400 ? ` ⚠️ ${f.Status}` : ''
      return `${i + 1}. \`${f.Method} ${f.Path}\` → ${f.Status}${st}`
    })
    const out: string[] = []
    for (let i = 0; i < steps.length; i += 4) {
      out.push((i === 0 ? head : '') + steps.slice(i, i + 4).join('\n') + '\n')
    }
    if (out.length === 0) out.push('当前候选为空，无法生成流程图。\n')
    return out
  }
}
