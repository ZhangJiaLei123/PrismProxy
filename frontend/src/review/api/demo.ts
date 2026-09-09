// 浏览器预览数据层（无 token / fetch 失败降级）：内置 demo 数据，内存模拟 rename/delete，
// 服务端查询/排序/忽略口径均在此 JS 复刻（设计 §7），UI 可脱离后端独立调试。
import type {
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
import type { ApiMode, ListFlowsOpts, ReviewApi, TagFlowsResp } from './types'

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
    if (!m) throw new ApiError('http', '非法忽略类型（仅支持 host/path/proc）')
    const v = kind === 'host' ? this.normHost(value) : kind === 'path' ? this.normPath(value) : value.trim()
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
    const v = kind === 'host' ? this.normHost(value) : kind === 'path' ? this.normPath(value) : value.trim()
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
}
