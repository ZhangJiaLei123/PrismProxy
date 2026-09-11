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
import type { AiChatEvent, AiChatRequest, ApiMode, AIApiConfigView, ListFlowsOpts, ReviewApi, TagFlowsResp } from './types'

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

  async getAIConfig(): Promise<AIApiConfigView> {
    return this.req<AIApiConfigView>('/api/v1/ai/config')
  }

  // 空态引导：POST /ui/settings{tab:'ai'} 唤起主窗并切到 AI 设置页；
  // headless（无 GUI）返回 ui:false，如实提示。
  async openSettingsAI(): Promise<{ opened: boolean; msg: string }> {
    const v = await this.req<{ ui?: boolean }>('/api/v1/ui/settings', {
      method: 'POST',
      headers: { ...this.headers(), 'Content-Type': 'application/json' },
      body: JSON.stringify({ tab: 'ai' }),
    })
    return v.ui
      ? { opened: true, msg: '已在主窗打开「AI 分析」设置' }
      : { opened: false, msg: '当前无 GUI 主窗（headless 模式），请直接编辑配置文件中 [ai] 段' }
  }

  // SSE 流式不能走 req()（其 await resp.json()）：首响应先判 ok（400/409 同步错误段），
  // 200 后 getReader 按 \n\n 分帧解析 event:/data: 行逐帧回调。signal 取消时 AbortError 静默收尾。
  async analyze(req: AiChatRequest, onEvent: (ev: AiChatEvent) => void, signal?: AbortSignal): Promise<void> {
    let resp: Response
    try {
      resp = await fetch('/api/v1/ai/chat', {
        method: 'POST',
        headers: { ...this.headers(), 'Content-Type': 'application/json' },
        body: JSON.stringify(req),
        signal,
      })
    } catch (e) {
      if (signal?.aborted) return // 停止：静默收尾
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
      // 409 并发闸门复用 http 错误面（面板统一内联提示）
      throw new ApiError('http', msg)
    }
    const reader = resp.body?.getReader()
    if (!reader) throw new ApiError('http', '响应流不可用')
    const decoder = new TextDecoder()
    let buf = ''
    try {
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        // SSE 以空行分帧；末帧可能未完整，留在 buf
        let idx: number
        while ((idx = buf.indexOf('\n\n')) >= 0) {
          const frame = buf.slice(0, idx)
          buf = buf.slice(idx + 2)
          const ev = parseSseFrame(frame)
          if (ev) onEvent(ev)
        }
      }
      buf += decoder.decode()
      if (buf.trim()) {
        const ev = parseSseFrame(buf)
        if (ev) onEvent(ev)
      }
    } catch (e) {
      if (signal?.aborted) return // 停止/关面板：静默收尾（后端 ctx 取消，无后续帧）
      throw e
    }
  }
}

/** 单帧解析：event: xxx 行 + data: {...} 行（后端每帧恰一个 event 一个 data）。 */
function parseSseFrame(frame: string): AiChatEvent | null {
  let event = ''
  let data = ''
  for (const line of frame.split('\n')) {
    if (line.startsWith('event:')) event = line.slice(6).trim()
    else if (line.startsWith('data:')) data += line.slice(5).trim()
  }
  if (!event) return null
  try {
    return { event, data: data ? JSON.parse(data) : null } as AiChatEvent
  } catch {
    return null // 坏 JSON 帧丢弃，不中断流
  }
}
