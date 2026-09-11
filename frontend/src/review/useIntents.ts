// 意图会话缓存（M13 P4-8）：模块级单例，表格摘要条与 AI 面板跨组件共享。
// 仅会话内存（不入库、刷新重算）；覆盖式写入（同流重析以最新为准）。
import { reactive } from 'vue'
import type { IntentResult } from '../lib/types'
import type { AiChatEvent, ReviewApi } from './api'

const intents = reactive(new Map<string, IntentResult>())

export function useIntents() {
  const intentOf = (flowId: string): IntentResult | undefined => intents.get(flowId)
  const upsert = (r: IntentResult): void => {
    intents.set(r.flowId, r)
  }
  // 批量标注意图：mode=intent 流式收帧，逐条覆盖写入；
  // 帧同时回调 onFrame（面板状态机消费 meta/done/error）；异常向上抛由调用方呈现。
  // opts 透传正文开关（面板「包含请求正文」）；摘要条单流重析不传走后端默认。
  async function runIntent(
    api: ReviewApi,
    ids: string[],
    onFrame: (ev: AiChatEvent) => void,
    signal?: AbortSignal,
    opts?: { includeReqBody?: boolean },
  ): Promise<void> {
    await api.analyze(
      { mode: 'intent', ids, options: opts },
      (ev) => {
        if (ev.event === 'intent') upsert(ev.data)
        onFrame(ev)
      },
      signal,
    )
  }
  return { intents, intentOf, upsert, runIntent }
}
