// 生产数据层：ctlapi（127.0.0.1:9595）HTTP，Authorization: Bearer <token>（token 来自 URL ?token=）。
// 字段形态（已逐行核实 Go 侧）：TagInfo 小写 json（id/name/count/createdAt/lastUsedAt）；
// FlowMeta/FlowDetail/BodyPayload 无 json tag → 大写字段（Tags/Encoding/ContentType/Raw/Body/...）；
// HistBucket 带小写 json tag（t0/t1/count）。
import type {
  ReviewBodyPayload,
  ReviewComposedRequest,
  ReviewFlowDetail,
  ReviewHistogram,
  ReviewIgnoreAddResult,
  ReviewIgnoreItem,
  ReviewIgnoreKind,
  ReviewScope,
  ReviewTagsOverview,
} from '../../lib/types'
import { ApiError } from './error'
import type { ApiMode, ListFlowsOpts, ReviewApi, TagFlowsResp } from './types'

export class HttpApi implements ReviewApi {
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

  async listTags(): Promise<ReviewTagsOverview> {
    const v = await this.req<ReviewTagsOverview>('/api/v1/tags')
    return { tags: v.tags ?? [], total: v.total ?? 0, totalFlows: v.totalFlows ?? 0 }
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
    let query = `?scope=${scope}&start=${start}&end=${end}&limit=${limit}&offset=${offset}&q=${encodeURIComponent(q)}`
    if (opts?.sort) query += `&sort=${encodeURIComponent(opts.sort)}`
    if (opts?.dir) query += `&dir=${encodeURIComponent(opts.dir)}`
    if (opts?.showIgnored) query += '&showIgnored=1'
    return this.req<TagFlowsResp>(`/api/v1/tags/${encodeURIComponent(tagID)}/flows${query}`)
  }

  async listIgnores(): Promise<ReviewIgnoreItem[]> {
    const v = await this.req<{ ignores?: ReviewIgnoreItem[] }>('/api/v1/tags/ignores')
    return v.ignores ?? []
  }

  async addIgnore(kind: ReviewIgnoreKind, value: string, note = ''): Promise<ReviewIgnoreAddResult> {
    return this.req<ReviewIgnoreAddResult>('/api/v1/tags/ignores', {
      method: 'POST',
      headers: { ...this.headers(), 'Content-Type': 'application/json' },
      body: JSON.stringify({ kind, value, note }),
    })
  }

  async removeIgnore(kind: ReviewIgnoreKind, value: string): Promise<boolean> {
    const v = await this.req<{ deleted?: boolean }>(
      `/api/v1/tags/ignores?kind=${encodeURIComponent(kind)}&value=${encodeURIComponent(value)}`,
      { method: 'DELETE' },
    )
    return v.deleted ?? false
  }

  async histogram(
    tagID: string,
    scope: ReviewScope,
    start: number,
    end: number,
    buckets: number,
  ): Promise<ReviewHistogram> {
    const q = `?scope=${scope}&start=${start}&end=${end}&buckets=${buckets}`
    const v = await this.req<ReviewHistogram>(
      `/api/v1/tags/${encodeURIComponent(tagID)}/histogram${q}`,
    )
    return { start: v.start ?? 0, end: v.end ?? 0, buckets: v.buckets ?? [] }
  }

  async flowDetail(flowID: string): Promise<ReviewFlowDetail> {
    return this.req<ReviewFlowDetail>(`/api/v1/tags/flows/${encodeURIComponent(flowID)}`)
  }

  async flowBody(flowID: string, which: 'req' | 'resp'): Promise<ReviewBodyPayload> {
    return this.req<ReviewBodyPayload>(`/api/v1/tags/flows/${encodeURIComponent(flowID)}/body?which=${which}`)
  }

  async compose(req: ReviewComposedRequest): Promise<ReviewFlowDetail> {
    return this.req<ReviewFlowDetail>('/api/v1/compose', {
      method: 'POST',
      headers: { ...this.headers(), 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    })
  }

  async liveFlowDetail(flowID: string): Promise<ReviewFlowDetail> {
    return this.req<ReviewFlowDetail>(`/api/v1/flows/${encodeURIComponent(flowID)}`)
  }

  async liveFlowBody(flowID: string, which: 'req' | 'resp'): Promise<ReviewBodyPayload> {
    return this.req<ReviewBodyPayload>(`/api/v1/flows/${encodeURIComponent(flowID)}/body?which=${which}`)
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
