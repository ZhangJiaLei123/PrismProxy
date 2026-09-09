import type { ApiMode } from './types'

/** 数据层错误：kind 区分离线 / 未授权 / 普通 HTTP 错误，UI 据此切换致命态或就地提示。 */
export class ApiError extends Error {
  constructor(public kind: ApiMode | 'http', message: string) {
    super(message)
  }
}
