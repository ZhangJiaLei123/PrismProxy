// 调试重发（Composer）共享纯逻辑：主窗 pages/Composer.vue 与复盘 review/ReviewComposer.vue 共用。
// 两个组件运行时完全隔离（wails+pinia vs 纯 HTTP ctlapi），仅共享与 UI/传输无关的常量与纯函数。

/** 方法下拉候选（n-select tag 模式下仍可自由输入其他方法）。 */
export const COMPOSER_METHOD_OPTIONS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'].map((m) => ({ label: m, value: m }))

/** 预填时剥离的逐跳/自动派生首部（Go 侧也会兜底剥离，前端预填先去掉避免误导）。 */
export const HOP_HEADERS = new Set([
  'connection', 'proxy-connection', 'proxy-authenticate', 'proxy-authorization',
  'keep-alive', 'te', 'trailer', 'transfer-encoding', 'upgrade', 'content-length',
])

/** 预填用：把请求头对象展开为可编辑行，剥离逐跳首部。 */
export function expandEditableHeaders(reqHeader: Record<string, string[]> | undefined): Array<{ key: string; value: string }> {
  const hs: Array<{ key: string; value: string }> = []
  for (const [k, vs] of Object.entries(reqHeader ?? {})) {
    if (HOP_HEADERS.has(k.toLowerCase())) continue
    for (const v of vs ?? []) hs.push({ key: k, value: v })
  }
  return hs
}

/** compose 请求载荷上限：与后端 ctlapi readBody(2<<20) 一致，超出被截断导致 JSON 解析失败。 */
export const COMPOSE_BODY_LIMIT = 2 << 20

/** 响应状态 tag 类型。 */
export function composerStatusTagType(state: string): 'success' | 'error' | 'warning' | 'default' {
  return state === 'done' ? 'success' : state === 'error' ? 'error' : state === 'streaming' ? 'warning' : 'default'
}

/** 响应状态码配色 class（配色定义在各自组件的 scoped 样式 .s-2xx 等）。 */
export function composerStatusCls(status: number): string {
  if (status >= 500) return 's-5xx'
  if (status >= 400) return 's-4xx'
  if (status >= 300) return 's-3xx'
  if (status >= 200) return 's-2xx'
  return ''
}
