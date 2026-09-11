// 复盘页数据层契约（设计 §6.2/§4.4 M12.1）：
// ReviewApi 为数据访问唯一接口，生产实现 HttpApi（ctlapi fetch），
// 浏览器预览实现 DemoApi（内存假后端），UI 只依赖此接口。
import type {
  AiMatchItem,
  IntentResult,
  ReviewBodyPayload,
  ReviewComposedRequest,
  ReviewFlowDetail,
  ReviewFlowMeta,
  ReviewHistogram,
  ReviewIgnoreAddResult,
  ReviewIgnoreItem,
  ReviewIgnoreKind,
  ReviewScope,
  ReviewSortDir,
  ReviewSortKey,
  ReviewTagsOverview,
} from '../../lib/types'

export interface TagFlowsResp {
  flows: ReviewFlowMeta[]
  total: number
  tag: string
  scope?: string
  start?: number
  end?: number
  limit: number
  offset: number
  q?: string
  sort?: string
  dir?: string
  showIgnored?: boolean
}

/** M12.2 列表附加参数：服务端排序 + 是否显示被忽略数据（眼睛）。 */
export interface ListFlowsOpts {
  sort?: ReviewSortKey
  dir?: ReviewSortDir
  showIgnored?: boolean
}

// wails=主窗内嵌复盘（直接调 Wails 绑定，不经 ctlapi HTTP）
export type ApiMode = 'http' | 'demo' | 'unauthorized' | 'offline' | 'wails'

// ===== AI 分析（M13 P4，设计稿 §5.3/§7.3）=====

export type AiChatMode = 'explain' | 'intent' | 'locate' | 'flowmap'

/** POST /api/v1/ai/chat 请求体（与 Go ctlapi.AIChatRequest 小写 json 同构）。 */
export interface AiChatRequest {
  mode: AiChatMode
  /** explain 必填（单流） */
  flowId?: string
  /** intent/locate/flowmap 必填：当前视图候选流 id（前端按 StartedAt 降序传；后端仍降序复查截断） */
  ids?: string[]
  /** locate/flowmap 必填：自然语言目标 */
  question?: string
  options?: AiChatOptions
}

export interface AiChatOptions {
  includeReqBody?: boolean
  includeRespBody?: boolean
  language?: string
}

/** meta 事件：组完 Prompt、调上游前下发（送审规模先回显）。 */
export interface AiChatMeta {
  mode: string
  total: number
  sent: number
  budget: { flows: number; kb: number }
  truncated: boolean
}

/** SSE 事件判别联合（{event, data} 帧形态；三实现零转换上抛，面板统一消费）。 */
export type AiChatEvent =
  | { event: 'meta'; data: AiChatMeta }
  | { event: 'delta'; data: { text?: string; reason?: string } }
  | { event: 'intent'; data: IntentResult }
  | { event: 'match'; data: AiMatchItem }
  | { event: 'error'; data: { message: string } }
  | { event: 'done'; data: { finishReason: string; truncated: boolean } }

/** AI 配置掩码视图（GET /ai/config 返回；wails 形态由 SettingsView.ai + GetAIConfigApp 合并）。 */
export interface AIApiConfigView {
  enabled: boolean
  provider: string
  baseUrl: string
  model: string
  temperature: number
  timeoutSec: number
  maxFlows: number
  maxKb: number
  redact: boolean
  hasApiKey: boolean
  apiKeyMasked: string
  keyMissingWarn?: string
}

export interface ReviewApi {
  mode: ApiMode
  listTags(): Promise<ReviewTagsOverview>
  listFlows(
    tagID: string,
    scope: ReviewScope,
    start: number,
    end: number,
    limit: number,
    offset: number,
    q: string,
    opts?: ListFlowsOpts,
  ): Promise<TagFlowsResp>
  listIgnores(): Promise<ReviewIgnoreItem[]>
  addIgnore(kind: ReviewIgnoreKind, value: string, note?: string): Promise<ReviewIgnoreAddResult>
  removeIgnore(kind: ReviewIgnoreKind, value: string): Promise<boolean>
  histogram(
    tagID: string,
    scope: ReviewScope,
    start: number,
    end: number,
    buckets: number,
  ): Promise<ReviewHistogram>
  flowDetail(flowID: string): Promise<ReviewFlowDetail>
  flowBody(flowID: string, which: 'req' | 'resp'): Promise<ReviewBodyPayload>
  /** 调试重发：POST /api/v1/compose（独立直连目标，同步返回结果流详情；composer 流只进实时 store，不落归档库）。 */
  compose(req: ReviewComposedRequest): Promise<ReviewFlowDetail>
  /** 实时流详情：composer 结果流不在归档库，须走 /api/v1/flows/{id}。 */
  liveFlowDetail(flowID: string): Promise<ReviewFlowDetail>
  /** 实时流正文：GET /api/v1/flows/{id}/body?which=。 */
  liveFlowBody(flowID: string, which: 'req' | 'resp'): Promise<ReviewBodyPayload>
  renameTag(tagID: string, name: string): Promise<void>
  deleteTag(tagID: string, deleteFlows: boolean): Promise<{ id: string; deletedFlows: number }>
  /** AI 流式分析（M13）：SSE 事件逐帧回调；signal 可选中止（停止静默收尾，不 reject）。
   * 同步错误（未配置/候选失效/409 并发冲突）reject ApiError。 */
  analyze(req: AiChatRequest, onEvent: (ev: AiChatEvent) => void, signal?: AbortSignal): Promise<void>
  /** AI 配置掩码视图：未配置空态判断（enabled/hasApiKey）、外发 host 告知（baseUrl）、redact 当前值。 */
  getAIConfig(): Promise<AIApiConfigView>
  /** 空态引导「去主窗设置」：http 形态 POST /ui/settings{tab:'ai'} 唤起主窗；
   * demo/wails 无 HTTP 通道，返回静态提示（opened:false）。 */
  openSettingsAI(): Promise<{ opened: boolean; msg: string }>
}
