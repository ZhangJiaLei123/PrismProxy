// 复盘页数据层契约（设计 §6.2/§4.4 M12.1）：
// ReviewApi 为数据访问唯一接口，生产实现 HttpApi（ctlapi fetch），
// 浏览器预览实现 DemoApi（内存假后端），UI 只依赖此接口。
import type {
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

export type ApiMode = 'http' | 'demo' | 'unauthorized' | 'offline'

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
}
