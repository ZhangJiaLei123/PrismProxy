// 数据复盘页数据层（设计 §6.2）：
// - 生产：ctlapi（127.0.0.1:9595）HTTP，Authorization: Bearer <token>（token 来自 URL ?token=）
// - 浏览器预览（无 token / fetch 失败）：降级内置 demo 数据，UI 可独立调试（设计 §7）
// 字段形态（已逐行核实 Go 侧）：TagInfo 小写 json（id/name/count/createdAt/lastUsedAt）；
// FlowMeta/FlowDetail/BodyPayload 无 json tag → 大写字段（Tags/Encoding/ContentType/Raw/Body/...）。
import type {
  ReviewBodyPayload,
  ReviewFlowDetail,
  ReviewFlowMeta,
  ReviewTagInfo,
} from '../lib/types'

export interface TagFlowsResp {
  flows: ReviewFlowMeta[]
  total: number
  tag: string
  limit: number
  offset: number
}

export type ApiMode = 'http' | 'demo' | 'unauthorized' | 'offline'

export interface ReviewApi {
  mode: ApiMode
  listTags(): Promise<ReviewTagInfo[]>
  listFlows(tagID: string, limit: number, offset: number): Promise<TagFlowsResp>
  flowDetail(flowID: string): Promise<ReviewFlowDetail>
  flowBody(flowID: string, which: 'req' | 'resp'): Promise<ReviewBodyPayload>
  renameTag(tagID: string, name: string): Promise<void>
  deleteTag(tagID: string, deleteFlows: boolean): Promise<{ id: string; deletedFlows: number }>
}

// ---- HTTP 实现 ----
class HttpApi implements ReviewApi {
  mode: ApiMode = 'http'
  constructor(private token: string) {}

  private headers(): Record<string, string> {
    return { Authorization: 'Bearer ' + this.token, 'X-Prism-Token': this.token }
  }

  private async req<T>(path: string, init?: RequestInit): Promise<T> {
    let resp: Response
    try {
      resp = await fetch(path, { headers: this.headers(), ...init })
    } catch {
      // ctlapi 随 App 退出而关闭（或端口不通）
      throw new ApiError('offline', '无法连接 PrismProxy（应用可能已退出）')
    }
    if (resp.status === 401 || resp.status === 403) {
      throw new ApiError('unauthorized', '未授权或登录态已失效（应用重启后 token 变更）')
    }
    if (!resp.ok) {
      let msg = `请求失败（HTTP ${resp.status}）`
      try {
        const j = await resp.json()
        if (j && typeof j.error === 'string') msg = j.error
      } catch { /* 非 JSON 错误体 */ }
      throw new ApiError('http', msg)
    }
    return (await resp.json()) as T
  }

  async listTags(): Promise<ReviewTagInfo[]> {
    const v = await this.req<{ tags: ReviewTagInfo[] }>('/api/v1/tags')
    return v.tags ?? []
  }

  async listFlows(tagID: string, limit: number, offset: number): Promise<TagFlowsResp> {
    const q = `?limit=${limit}&offset=${offset}`
    return this.req<TagFlowsResp>(`/api/v1/tags/${encodeURIComponent(tagID)}/flows${q}`)
  }

  async flowDetail(flowID: string): Promise<ReviewFlowDetail> {
    return this.req<ReviewFlowDetail>(`/api/v1/tags/flows/${encodeURIComponent(flowID)}`)
  }

  async flowBody(flowID: string, which: 'req' | 'resp'): Promise<ReviewBodyPayload> {
    return this.req<ReviewBodyPayload>(`/api/v1/tags/flows/${encodeURIComponent(flowID)}/body?which=${which}`)
  }

  async renameTag(tagID: string, name: string): Promise<void> {
    await this.req(`/api/v1/tags/${encodeURIComponent(tagID)}/rename`, {
      method: 'POST',
      headers: { ...this.headers(), 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    })
  }

  async deleteTag(tagID: string, deleteFlows: boolean): Promise<{ id: string; deletedFlows: number }> {
    return this.req(`/api/v1/tags/${encodeURIComponent(tagID)}?flows=${deleteFlows ? 1 : 0}`, {
      method: 'DELETE',
    })
  }
}

export class ApiError extends Error {
  constructor(public kind: ApiMode | 'http', message: string) {
    super(message)
  }
}

// ---- demo 实现（浏览器预览；内存模拟 rename/delete） ----
function b64(s: string): string {
  return btoa(unescape(encodeURIComponent(s)))
}

interface DemoDB {
  tags: ReviewTagInfo[]
  flows: ReviewFlowMeta[]
  details: Map<string, ReviewFlowDetail>
  bodies: Map<string, { req: string; resp: string }>
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
    mkTag('t_demo_order', '下单接口', 2, 46),
    mkTag('t_demo_static', '静态资源', 1, 180),
  ]
  const tpl = [
    { method: 'POST', host: 'api.example.com', path: '/v1/auth/login', status: 200, scheme: 'https', ct: 'application/json; charset=utf-8', tag: '登录流程排查', proc: 'WeChat.exe' },
    { method: 'GET', host: 'api.example.com', path: '/v1/auth/captcha', status: 200, scheme: 'https', ct: 'application/json; charset=utf-8', tag: '登录流程排查', proc: 'WeChat.exe' },
    { method: 'POST', host: 'api.example.com', path: '/v1/auth/login', status: 401, scheme: 'https', ct: 'application/json; charset=utf-8', tag: '登录流程排查', proc: 'WeChat.exe' },
    { method: 'POST', host: 'api.example.com', path: '/v1/orders', status: 201, scheme: 'https', ct: 'application/json; charset=utf-8', tag: '下单接口', proc: 'chrome.exe' },
    { method: 'GET', host: 'api.example.com', path: '/v1/orders/10086', status: 200, scheme: 'https', ct: 'application/json; charset=utf-8', tag: '下单接口', proc: 'chrome.exe' },
    { method: 'GET', host: 'cdn.static.net', path: '/assets/app.js', status: 200, scheme: 'https', ct: 'application/javascript', tag: '静态资源', proc: 'chrome.exe' },
  ]
  const flows: ReviewFlowMeta[] = []
  const details = new Map<string, ReviewFlowDetail>()
  const bodies = new Map<string, { req: string; resp: string }>()
  tpl.forEach((t, i) => {
    const id = 'demo-flow-' + (i + 1)
    const reqBody = t.path.includes('login')
      ? JSON.stringify({ account: '138****0000', password: '******', device: 'demo' }, null, 2)
      : t.method === 'POST'
        ? JSON.stringify({ skuId: 1000 + i, qty: 1 }, null, 2)
        : ''
    const respBody = t.status === 401
      ? JSON.stringify({ code: 401, msg: '账号或密码错误' }, null, 2)
      : JSON.stringify({ code: t.status, msg: 'ok（演示数据）', data: { id: i + 1, ts: now - i * 60000 } }, null, 2)
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
      StartedAt: now - i * 3 * 60000,
      Err: '',
      Pinned: false,
      Source: 'history',
      Historical: true,
      Tags: [t.tag],
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

class DemoApi implements ReviewApi {
  mode: ApiMode = 'demo'
  private db: DemoDB = demoData()

  async listTags(): Promise<ReviewTagInfo[]> {
    return [...this.db.tags].sort((a, b) => b.lastUsedAt - a.lastUsedAt)
  }

  async listFlows(tagID: string, limit: number, offset: number): Promise<TagFlowsResp> {
    const all = tagID === 'all'
      ? this.db.flows
      : this.db.flows.filter((f) => this.db.tags.some((t) => t.id === tagID && f.Tags.includes(t.name)))
    const sorted = [...all].sort((a, b) => b.StartedAt - a.StartedAt)
    return {
      flows: sorted.slice(offset, offset + limit),
      total: sorted.length,
      tag: tagID,
      limit,
      offset,
    }
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
      const ids = new Set(linked.map((f) => f.ID))
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

// ---- 工厂：探测 token；无 token → demo；有 token → http（首调失败由 UI 转态） ----
export function createApi(): { api: ReviewApi; token: string } {
  const token = new URLSearchParams(location.search).get('token') ?? ''
  if (!token || token === 'mock') {
    return { api: new DemoApi(), token }
  }
  return { api: new HttpApi(token), token }
}
