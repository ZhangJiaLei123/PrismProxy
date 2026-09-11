// 主窗内嵌复盘数据层：直接调 Wails 绑定（不经 ctlapi HTTP，无需 token）。
// 字段形态与 HTTP 完全同源（同一批 Go DTO）：TagInfo/HistBucket/ReviewIgnore 小写 json，
// FlowMeta/FlowDetail/BodyPayload 大写字段。多返回值绑定在 JS 侧 resolve 为数组，按序解构。
import {
  AIChatStart,
  AIChatStop,
  DeleteTagApp,
  GetAIConfigApp,
  GetFlowBody,
  GetFlowDetail,
  GetSettings,
  RenameTagApp,
  ReviewDeleteIgnore,
  ReviewFlowBody,
  ReviewFlowDetail,
  ReviewListIgnores,
  SendComposed,
  WailsAddIgnore,
  WailsFlowList,
  WailsHistogram,
  WailsTagsOverview,
} from '../../../wailsjs/go/app/App'
import { EventsOn } from '../../../wailsjs/runtime/runtime'
import type {
  ReviewBodyPayload,
  ReviewComposedRequest,
  ReviewFlowDetail,
  ReviewIgnoreAddResult,
  ReviewIgnoreItem,
  ReviewIgnoreKind,
  ReviewScope,
  ReviewTagsOverview,
} from '../../lib/types'
import type { AIApiConfigView, AiChatEvent, AiChatRequest, ApiMode, ListFlowsOpts, ReviewApi, TagFlowsResp } from './types'
import { ApiError } from './error'

// Go 侧事件桥载荷：EventsEmit("ai:chat", {handle, event, data})——handle 用于过滤本会话帧
interface AiChatFrame {
  handle: string
  event: AiChatEvent['event']
  data: unknown
}

export class WailsReviewApi implements ReviewApi {
  mode: ApiMode = 'wails'

  async listTags(): Promise<ReviewTagsOverview> {
    // Wails v2 绑定只支持 1~2 个返回值，原 ReviewTagsOverview 是 4 返回值会被 dispatcher
    // 静默置空；改走单结构返回的 WailsTagsOverview 包装（HTTP 复盘页仍用原多返回值方法）。
    const r = await WailsTagsOverview()
    return { tags: r?.tags ?? [], total: r?.total ?? 0, totalFlows: r?.totalFlows ?? 0 }
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
    // persist.ReviewListOpts 无 json tag → PascalCase（Q/SortKey/SortDir/ShowIgnored）
    // 原 ReviewFlowList 是 3 返回值，超 Wails 2 返回上限会静默置空；改走单结构包装。
    const r = await WailsFlowList(tagID, scope, start, end, limit, offset, {
      Q: q,
      SortKey: opts?.sort ?? '',
      SortDir: opts?.dir ?? '',
      ShowIgnored: !!opts?.showIgnored,
    })
    return {
      flows: r?.flows ?? [],
      total: r?.total ?? 0,
      tag: tagID,
      scope,
      start,
      end,
      limit,
      offset,
      q,
      sort: opts?.sort,
      dir: opts?.dir,
      showIgnored: !!opts?.showIgnored,
    }
  }

  async listIgnores(): Promise<ReviewIgnoreItem[]> {
    const r = (await ReviewListIgnores()) as unknown as ReviewIgnoreItem[]
    return r ?? []
  }

  async addIgnore(kind: ReviewIgnoreKind, value: string, note = ''): Promise<ReviewIgnoreAddResult> {
    // 原 ReviewAddIgnore 是 3 返回值，超 Wails 2 返回上限会静默置空；改走单结构包装。
    const r = await WailsAddIgnore(kind, value, note)
    return { ignore: r?.ignore as unknown as ReviewIgnoreItem, added: !!r?.added }
  }

  async removeIgnore(kind: ReviewIgnoreKind, value: string): Promise<boolean> {
    // ReviewDeleteIgnore(...) 多返回值：(deleted, error)
    const r = (await ReviewDeleteIgnore(kind, value)) as unknown as boolean
    return !!r
  }

  async histogram(
    tagID: string,
    scope: ReviewScope,
    start: number,
    end: number,
    buckets: number,
  ): Promise<ReviewHistogram> {
    // 原 ReviewHistogram 是 4 返回值，超 Wails 2 返回上限会静默置空；改走单结构包装。
    const r = await WailsHistogram(tagID, scope, start, end, buckets)
    return { start: r?.start ?? 0, end: r?.end ?? 0, buckets: r?.buckets ?? [] }
  }

  async flowDetail(flowID: string): Promise<ReviewFlowDetail> {
    return (await ReviewFlowDetail(flowID)) as unknown as ReviewFlowDetail
  }

  async flowBody(flowID: string, which: 'req' | 'resp'): Promise<ReviewBodyPayload> {
    return (await ReviewFlowBody(flowID, which)) as unknown as ReviewBodyPayload
  }

  async compose(req: ReviewComposedRequest): Promise<ReviewFlowDetail> {
    // app.ComposedRequest 小写 json，与 ReviewComposedRequest 同构
    return (await SendComposed(req)) as unknown as ReviewFlowDetail
  }

  async liveFlowDetail(flowID: string): Promise<ReviewFlowDetail> {
    return (await GetFlowDetail(flowID)) as unknown as ReviewFlowDetail
  }

  async liveFlowBody(flowID: string, which: 'req' | 'resp'): Promise<ReviewBodyPayload> {
    return (await GetFlowBody(flowID, which)) as unknown as ReviewBodyPayload
  }

  async renameTag(tagID: string, name: string): Promise<void> {
    await RenameTagApp(tagID, name)
  }

  async deleteTag(tagID: string, deleteFlows: boolean): Promise<{ id: string; deletedFlows: number }> {
    // DeleteTagApp(...) 多返回值：(deletedCount, error)
    const n = (await DeleteTagApp(tagID, deleteFlows)) as unknown as number
    return { id: tagID, deletedFlows: n ?? 0 }
  }

  // ===== AI 分析（M13 P4：AIChatStart 事件桥转发；ctlapi.AIChatRequest 字段全必选需显式补齐）=====

  async analyze(req: AiChatRequest, onEvent: (ev: AiChatEvent) => void, signal?: AbortSignal): Promise<void> {
    const h = await AIChatStart({
      mode: req.mode,
      flowId: req.flowId ?? '',
      ids: req.ids ?? [],
      question: req.question ?? '',
      options: {
        includeReqBody: req.options?.includeReqBody ?? false,
        includeRespBody: req.options?.includeRespBody ?? false,
        language: req.options?.language ?? '',
      },
    })
    const handle = h?.handle ?? ''
    if (!handle) {
      // 无句柄=桥接帧永远无法匹配（防御性兜底：避免 Promise 永久挂起，L1 审计修复）
      throw new ApiError('wails', 'AI 会话启动失败（未获得会话句柄）')
    }
    if (signal?.aborted) {
      void AIChatStop(handle)
      return
    }
    // 事件桥：过滤本 handle 帧，{event,data} 零转换上抛（error→reject，done→resolve，abort→静默收尾）
    await new Promise<void>((resolve, reject) => {
      let settled = false
      const onAbort = (): void => {
        void AIChatStop(handle)
        settle(resolve)
      }
      const settle = (fn: () => void): void => {
        if (settled) return
        settled = true
        off()
        signal?.removeEventListener('abort', onAbort)
        fn()
      }
      const off = EventsOn('ai:chat', (payload: AiChatFrame) => {
        if (!payload || payload.handle !== handle) return
        const ev = { event: payload.event, data: payload.data } as AiChatEvent
        onEvent(ev)
        if (payload.event === 'error') {
          const msg = (payload.data as { message?: string } | null)?.message ?? 'AI 分析失败'
          settle(() => reject(new ApiError('wails', msg)))
        } else if (payload.event === 'done') {
          settle(resolve)
        }
      })
      if (signal?.aborted) onAbort()
      else signal?.addEventListener('abort', onAbort, { once: true })
    })
  }

  async getAIConfig(): Promise<AIApiConfigView> {
    // Go 侧 GetAIConfigApp 仅 3 字段（主窗 AiTab 设计）；baseUrl/redact 等从 SettingsView.ai 补齐合并
    const [s, c] = await Promise.all([GetSettings(), GetAIConfigApp()])
    const ai = s?.ai
    return {
      enabled: ai?.enabled ?? true,
      provider: ai?.provider ?? '',
      baseUrl: ai?.baseUrl ?? '',
      model: ai?.model ?? '',
      temperature: ai?.temperature ?? 0,
      timeoutSec: ai?.timeoutSec ?? 0,
      maxFlows: ai?.maxFlows ?? 0,
      maxKb: ai?.maxKb ?? 0,
      redact: ai?.redact ?? true,
      hasApiKey: !!c?.hasApiKey,
      apiKeyMasked: c?.apiKeyMasked ?? '',
      keyMissingWarn: c?.keyMissingWarn,
    }
  }

  // wails=主窗内嵌，设置就在本窗（无 ctlapi HTTP 通道跨源不可达）：静态提示（设计 §九）
  async openSettingsAI(): Promise<{ opened: boolean; msg: string }> {
    return { opened: false, msg: '请在主窗底部状态栏「设置」→「AI 分析」中配置' }
  }
}
