// 数据层统一出口：页面/组件一律 from '../api'（或 '../../api'）。
// 工厂：探测 token；无 token / mock → demo；有 token → http（首调失败由 UI 转致命态）。
import { DemoApi } from './demo'
import { HttpApi } from './http'
import type { ReviewApi } from './types'

export { ApiError } from './error'
export { DemoApi } from './demo'
export { HttpApi } from './http'
export type {
  AIApiConfigView,
  AiChatEvent,
  AiChatMeta,
  AiChatNotice,
  AiChatMode,
  AiChatOptions,
  AiChatRequest,
  AiUsage,
  ApiMode,
  ListFlowsOpts,
  ReviewApi,
  TagFlowsResp,
} from './types'

export function createApi(): { api: ReviewApi; token: string } {
  const token = new URLSearchParams(location.search).get('token') ?? ''
  if (!token || token === 'mock') {
    return { api: new DemoApi(), token }
  }
  return { api: new HttpApi(token), token }
}
