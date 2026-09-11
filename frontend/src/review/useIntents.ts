// 意图会话缓存（M13 P4-8）：模块级单例，表格摘要条与 AI 面板跨组件共享。
// 仅会话内存（不入库、刷新重算）；覆盖式写入（同流重析以最新为准）。
import { reactive, ref } from 'vue'
import type { IntentResult } from '../lib/types'
import type { AiChatEvent, ReviewApi } from './api'
import { ApiError } from './api'

const intents = reactive(new Map<string, IntentResult>())
// 跨组件互斥（P4 审计修复）：面板批量标注与摘要条单条重析共用 runIntent 写同一共享 Map，
// 并发时覆盖顺序不确定（后完成者胜），改为「后发起者被拒」——仅互斥，不做进度共享。
const busy = ref(false)

export function useIntents() {
  const intentOf = (flowId: string): IntentResult | undefined => intents.get(flowId)
  const upsert = (r: IntentResult): void => {
    intents.set(r.flowId, r)
  }
  // 批量标注意图：mode=intent 流式收帧。intent 帧的缓存写入由调用方在各自守卫内消费
  // （P1 审计修复：此处不再代写 upsert——面板在 runSeq+phase 双守卫的 onFrame 中回填，
  // 摘要条自吞 intent 帧；避免旧任务停止/重开后迟到帧绕过守卫污染共享缓存）。
  // 异常向上抛由调用方呈现。opts 透传正文开关（面板「包含请求正文」）；摘要条单流重析不传走后端默认。
  async function runIntent(
    api: ReviewApi,
    ids: string[],
    onFrame: (ev: AiChatEvent) => void,
    signal?: AbortSignal,
    opts?: { includeReqBody?: boolean },
  ): Promise<void> {
    if (busy.value) throw new ApiError('http', '已有意图标注任务进行中，请稍候')
    busy.value = true
    try {
      await api.analyze({ mode: 'intent', ids, options: opts }, onFrame, signal)
    } finally {
      busy.value = false
    }
  }
  return { intents, intentOf, upsert, runIntent, busy }
}
