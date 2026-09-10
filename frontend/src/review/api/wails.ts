// 主窗内嵌复盘数据层：直接调 Wails 绑定（不经 ctlapi HTTP，无需 token）。
// 字段形态与 HTTP 完全同源（同一批 Go DTO）：TagInfo/HistBucket/ReviewIgnore 小写 json，
// FlowMeta/FlowDetail/BodyPayload 大写字段。多返回值绑定在 JS 侧 resolve 为数组，按序解构。
import {
  DeleteTagApp,
  GetFlowBody,
  GetFlowDetail,
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
import type { ApiMode, ListFlowsOpts, ReviewApi, TagFlowsResp } from './types'

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
}
