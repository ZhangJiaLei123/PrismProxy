// 意图会话缓存（M13 P4-8）：模块级单例，表格摘要条与 AI 面板跨组件共享。
// 来源两路：intent 模式解析完成 upsert（会话内最新，覆盖式写入）；复盘列表/详情
// 加载时 seed 回填归档库持久化结果（仅缓存缺失时写入，不覆盖刚重析的最新值）。
// 后端已持久化 flow_intents 表（M13 §7），刷新/重进复盘页经 seed 恢复展示；
// 项目切换后旧项目条目由复盘页挂载时 clear 清空（§7 审计修复）。
import { reactive, ref } from 'vue'
import type { IntentResult, ReviewFlowMeta } from '../lib/types'
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
  // 批量回填（M13 §7）：把后端随列表/详情带出的持久化意图写入缓存。
  // 仅缓存无该条时写入——刚重析的会话结果（最新语义）不被列表刷新覆盖；
  // confidence 异常值降级 low（与 AI 输出口径一致，needsBody 提示带正文重析）。
  function seed(flows: readonly ReviewFlowMeta[]): void {
    for (const f of flows) {
      if (!f.AIIntent || intents.has(f.ID)) continue
      const c = f.AIConfidence
      intents.set(f.ID, {
        flowId: f.ID,
        seq: 0,
        intent: f.AIIntent,
        confidence: c === 'high' || c === 'medium' ? c : 'low',
        needsBody: !!f.AINeedsBody,
      })
    }
  }
  // 项目切换清理（§7 审计修复）：flow_id 进程内唯一无碰撞风险，但旧项目条目
  // 徒占内存；复盘页挂载时调用，随后 seed 只回填当前项目数据，语义干净。
  function clear(): void {
    intents.clear()
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
  return { intents, intentOf, upsert, seed, clear, runIntent, busy }
}
