<template>
  <!-- AI 分析面板（设计 §7）：自绘右侧滑入抽屉（非 n-drawer、无遮罩，可边看列表边读）；
       absolute 定位于 .review-root 内——主窗内嵌时只覆盖复盘页区域，独立窗口等效 fixed；
       宽度可拖拽（左缘把手）并经 localStorage 缓存 -->
  <aside
    ref="drawerEl"
    class="ai-drawer"
    :class="{ open: show, 'w-dragging': wDragging }"
    :style="aiWStyle"
  >
    <!-- 左缘宽度拖拽把手：左右拖动调整面板宽度，双击恢复默认 -->
    <div class="ai-grip-x" title="拖拽调整宽度 · 双击恢复默认" @pointerdown="startWDrag" @dblclick="resetW"></div>
    <header class="ai-head">
      <span class="ai-title">
        <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor">
          <path d="M8 1l1.9 4.6 4.6 1.9-4.6 1.9L8 14 6.1 9.4 1.5 7.5l4.6-1.9L8 1zm4.5 9.5l.8 1.9 1.9.8-1.9.8-.8 1.9-.8-1.9-1.9-.8 1.9-.8.8-1.9z" />
        </svg>
        AI 分析
      </span>
      <!-- 用 n-tab（纯导航 tab）而非自闭合 n-tab-pane：naive-ui 2.40.4 的 normalizeSlots
           会把自闭合 pane 的空 children 包装成"渲染注释"的 truthy slot，短路 props.tab，
           导致标签文字永不渲染（tab 被压成 8px 高的"进度条"） -->
      <n-tabs v-model:value="activeTab" type="segment" size="small" class="ai-tabs">
        <n-tab v-for="t in TABS" :key="t.key" :name="t.key">{{ t.label }}</n-tab>
      </n-tabs>
      <button class="ai-close" title="关闭（Esc）" @click="closePanel">
        <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor">
          <path d="M8 6.59L12.95 1.64l1.41 1.41L9.41 8l4.95 4.95-1.41 1.41L8 9.41l-4.95 4.95-1.41-1.41L6.59 8 1.64 3.05l1.41-1.41L8 6.59z" />
        </svg>
      </button>
    </header>

    <div class="ai-body">
      <!-- 空态：未启用 / 未配 Key / 配置读取失败（设计 §九 空态引导） -->
      <div v-if="showEmpty || cfgFailed" class="ai-empty">
        <div class="ai-empty-ic">⚙️</div>
        <div class="ai-empty-title">{{ emptyTitle }}</div>
        <div class="ai-empty-sub">在主窗「设置 → AI 分析」中启用并填写 API Key 后即可使用</div>
        <n-button v-if="api.mode === 'http' && cfg" size="small" type="primary" secondary @click="goSettings">去主窗设置</n-button>
        <div v-if="settingMsg" class="ai-empty-msg">{{ settingMsg }}</div>
      </div>

      <div v-else-if="cfgLoading" class="ai-hint ai-pad">正在读取 AI 配置…</div>

      <template v-else>
        <!-- 四个模式模块（独立组件）：父持运行引擎与全部状态，视图纯展示经 props 注入（同 ReviewAiLogSheet 拆分模式）；
             动作行（RunActions）/Markdown 结论区（MdView）为跨模式共用小件，由各视图内部挂载 -->
        <ReviewAiExplainView
          v-if="activeTab === 'explain'"
          :scope-text="scopeText"
          :phase="viewPhase"
          :running="running"
          :start-disabled="startDisabled"
          :need-confirm="needConfirm"
          :meta-text="metaText"
          :err-msg="errMsg"
          :md="displayMd"
          :pending="pendingStream"
          :continue-state="continueStateView"
          @start="requestStart()"
          @stop="stop"
        />
        <ReviewAiIntentView
          v-else-if="activeTab === 'intent'"
          :scope-text="scopeText"
          :phase="viewPhase"
          :running="running"
          :start-disabled="startDisabled"
          :need-confirm="needConfirm"
          :meta-text="metaText"
          :err-msg="errMsg"
          :continue-state="continueStateView"
          :pct="intentPct"
          :results="intentResults"
          :label-of="flowLabelOf"
          v-model:include-req="includeReq"
          v-model:single-mode="singleMode"
          @start="requestStart()"
          @stop="stop"
          @locate="emit('locate', $event)"
        />
        <ReviewAiLocateView
          v-else-if="activeTab === 'locate'"
          :scope-text="scopeText"
          :phase="viewPhase"
          :running="running"
          :start-disabled="startDisabled"
          :need-confirm="needConfirm"
          :meta-text="metaText"
          :err-msg="errMsg"
          v-model:question="question"
          v-model:include-req="includeReq"
          v-model:include-resp="includeResp"
          :md="displayMd"
          :pending="pendingStream"
          :continue-state="continueStateView"
          :matches="matches"
          :conf-cls="confCls"
          :conf-label="confLabel"
          @start="requestStart()"
          @stop="stop"
          @locate="emit('locate', $event)"
        />
        <ReviewAiFlowmapView
          v-else
          :scope-text="scopeText"
          :phase="viewPhase"
          :running="running"
          :start-disabled="startDisabled"
          :need-confirm="needConfirm"
          :meta-text="metaText"
          :err-msg="errMsg"
          v-model:question="question"
          :md="displayMd"
          :pending="pendingStream"
          :continue-state="continueStateView"
          @start="requestStart()"
          @stop="stop"
        />
      </template>
    </div>

    <!-- 底部：外发告知常驻小字（设计 §8-5）+ 右下角 导出/历史/日志 三入口 -->
    <footer class="ai-foot">
      <span v-if="cfgOk" class="ai-foot-text">
        分析数据将发送至 {{ hostLabel }}<template v-if="modelLabel"> · 模型 {{ modelLabel }}</template>
      </span>
      <button class="ai-logbtn" title="历史记录（解读/定位/流程）" @click="toggleHist">
        <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="8" cy="8" r="5.5" />
          <path d="M8 5.2V8l2 1.4" />
        </svg>
      </button>
      <button
        class="ai-logbtn"
        title="导出当前模块最新结果为 Markdown 文件"
        :disabled="!canExportCur"
        @click="exportCurrent"
      >
        <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M8 2.5v7" />
          <path d="M5 6.5l3 3 3-3" />
          <path d="M3 10.5v1.5A1.5 1.5 0 0 0 4.5 13.5h7a1.5 1.5 0 0 0 1.5-1.5v-1.5" />
        </svg>
      </button>
      <button class="ai-logbtn" title="AI 调用日志与对话" @click="toggleLog">
        <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M2.5 4l3.5 3.5L2.5 11" />
          <path d="M8.5 11.5H13" />
        </svg>
        <i v-if="phase === 'streaming'" class="ai-logbtn-dot" aria-hidden="true"></i>
      </button>
    </footer>

    <!-- 调用日志抽层（独立组件 ReviewAiLogSheet）：对话内容（模型原始输出实时流）+ 调用日志（帧级事件）；
         数据源（convTurns/logs）为模块级 store（useAiSession），开合态由本面板持有，展示/滚随/拖高在子组件内 -->
    <ReviewAiLogSheet
      ref="logSheetRef"
      v-model:open="logOpen"
      v-model:prev-open="prevOpen"
      :phase="phase"
      :live-turn="liveTurn"
      :conv-turns="convTurns"
      :logs="logs"
      :prev-logs="prevLogs"
      :continue-state="continueState"
    />

    <!-- 历史记录抽层（独立组件 ReviewAiHistorySheet）：解读/定位/流程历次输出回看 + 跨模块多选导出；
         与日志抽层同位互斥（开一个关另一个），开合态由本面板持有；live-turn 供剔除流式进行中轮次（未定稿不入册） -->
    <ReviewAiHistorySheet v-model:open="histOpen" :conv-turns="convTurns" :live-turn="liveTurn" />

    <!-- 首次分析告知（设计 §8-1）：localStorage 记忆，确认后才真正开始 -->
    <n-modal
      v-model:show="noticeShow"
      preset="dialog"
      type="warning"
      title="AI 分析数据外发告知"
      positive-text="知道了，开始分析"
      negative-text="取消"
      @positive-click="onNoticeOk"
    >
      <div class="ai-notice">
        <p>分析时将把所选流量的方法、URL、状态码、请求/响应头等数据发送到你配置的 AI 服务（{{ hostLabel }}）。</p>
        <p>如勾选「包含正文」，请求/响应正文也会一并送审。</p>
        <p>当前脱敏设置：<b :class="cfg?.redact ? '' : 'k-ai-low'">{{ cfg?.redact ? '已开启（自动隐藏常见敏感凭据）' : '未开启（请求头原样发送）' }}</b></p>
        <p>分析结果由模型生成，仅供参考，请勿盲信。</p>
      </div>
    </n-modal>
  </aside>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { NButton, NModal, NTab, NTabs } from 'naive-ui'
import type { AiChatEvent, AiChatMeta, AiChatMode, AiChatNotice, AiUsage, AIApiConfigView, ReviewApi } from '../api'
import type { AiMatchItem, IntentResult, ReviewFlowMeta } from '../../lib/types'
import { useIntents } from '../useIntents'
import ReviewAiLogSheet from './ReviewAiLogSheet.vue'
import ReviewAiHistorySheet from './ReviewAiHistorySheet.vue'
import ReviewAiExplainView from './ReviewAiExplainView.vue'
import ReviewAiIntentView from './ReviewAiIntentView.vue'
import ReviewAiLocateView from './ReviewAiLocateView.vue'
import ReviewAiFlowmapView from './ReviewAiFlowmapView.vue'
import { MODE_LABEL, downloadMd } from '../aiExport'
import {
  addLog,
  archiveLogs,
  convTurns,
  createTurn,
  hydrateAiSession,
  lastResults,
  logs,
  matches,
  prevLogs,
  scheduleSave,
} from '../useAiSession'
import type { ConvTurn } from '../useAiSession'

// 面板状态机（设计 §7.3）：idle → streaming(meta→delta/intent/match*) → done|stopped|error；
// 切 tab 后台续跑（状态跨 tab 记忆，视图级派生见 viewPhase），同面板同时只跑一个任务
type Phase = 'idle' | 'streaming' | 'done' | 'stopped' | 'error'

const props = defineProps<{
  api: ReviewApi
  show: boolean
  mode: AiChatMode
  /** explain 模式目标流（唯一必填输入）；跟随列表选中流，浏览类变化不打断面板 */
  flowId?: string
  /** explain 显式解读信号：详情页「AI 解读」/路径列意图摘要入口递增（列表浏览不递增），
   *  面板据此换目标重置并自动解读；列表点行/翻页/筛选不触发 */
  explainAutoTick?: number
  checkedIds: string[]
  /** 当前视图已加载的流（intent/locate/flowmap 候选池，面板按 StartedAt 降序取 id） */
  viewFlows: ReviewFlowMeta[]
  viewTotal: number
  /** 范围说明文案用：当前标签名 / 关键词 */
  tagName: string
  scope: string
  keyword: string
}>()

const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
  (e: 'locate', flowId: string): void
  (e: 'error', msg: string): void
}>()

const TABS: { key: AiChatMode; label: string }[] = [
  { key: 'explain', label: '解读' },
  { key: 'intent', label: '意图' },
  { key: 'locate', label: '定位' },
  { key: 'flowmap', label: '流程' },
]
const NOTICE_KEY = 'prismproxy:review-ai-notice-v1'

const { intentOf, runIntent, upsert, busy } = useIntents()
// 阶段三持久化：启动恢复 IndexedDB 中的对话轮次/日志/匹配（幂等，模块级单例只跑一次）
void hydrateAiSession()

const activeTab = ref<AiChatMode>(props.mode)
const phase = ref<Phase>('idle')
// explain 本次 run 的目标流（doStart 时快照）：头部目标名以此为准——
// flowId 跟随列表选中流后，浏览点行/翻页引起的变化不得让旧结果错挂新标签
const runTarget = ref('')
// run 级快照（doStart 时一次性定格）：切 tab 后台续跑期间，运行逻辑/进度文案/开轮标记
// 读发起时的模式与选项，不随 activeTab/实时 props 漂移（qAsked/scopeText/singleMode 均依赖 activeTab）
const runMode = ref<AiChatMode>('explain')
const runSingle = ref(false)
const runAsked = ref('')
const errMsg = ref('')
const meta = ref<AiChatMeta | null>(null)
// 截断自动续写状态（notice 帧驱动）：compressing=临时会话压缩历史 / continuing=新会话续写 /
// failed=失败保留截断稿；主区渲染 Trae 风格内联状态条（ReviewAiContinueBar），done/新 run/切条清除
const continueState = ref<AiChatNotice | null>(null)
// metaText 用短状态（续写期间数十秒无 delta，RunActions 小字也要可见进度）
const noticeMsg = computed(() => {
  const s = continueState.value
  if (!s) return ''
  if (s.stage === 'compressing') return '续写中：压缩历史对话…'
  if (s.stage === 'continuing') return '续写中：新会话输出…'
  return '续写失败'
})
// 视图门控：续写状态只挂发起模式 tab（外模式 tab 视图为 idle，不展示别模式的续写条）
const continueStateView = computed<AiChatNotice | null>(() =>
  viewPhase.value === 'streaming' ? continueState.value : null,
)
const intentDone = ref(0)
// 对话 tab 聊天模型：每条流一对「消息发送 → 模型返回」气泡。单条轮询每条独立成对
// （每次 meta 开新轮），批量/explain 等单次调用=单轮。curTurn 为 in-flight 轮的
// reactive 代理：50ms 合帧直接写代理属性即触发更新。
// convTurns/logs/prevLogs/matches 为模块级 store（useAiSession）：常驻内存 + IndexedDB
// 持久化，切模式/组件卸载不丢（阶段三）
let curTurn: ConvTurn | null = null
// 进行中轮次引用（curTurn 的响应式镜像，供 LogSheet live 高亮判定）：closeTurn 即熄灭——
// 单条轮询条目间隙期（上条已关轮、下条未开轮）已完成轮不再误亮直播态
const liveTurn = ref<ConvTurn | null>(null)
const question = ref('')
const includeReq = ref(false)
const includeResp = ref(false)
// intent 单条轮询：逐条独立调用模型（勾选后批量红线问题不再出现，代价是总耗时变长）
const singleMode = ref(false)
// 单条轮询整轮候选数（循环开始时快照）：进度分母不随运行中勾选变化漂移
const singleTotal = ref(0)
const cfg = ref<AIApiConfigView | null>(null)
const cfgLoading = ref(false)
const noticeShow = ref(false)
const settingMsg = ref('')

// 非响应式：delta 原始缓冲与渲染节流句柄（50ms 合帧，避免逐 token 重排）
let mdBuf = ''
let reasonBuf = '' // 推理模型思考增量缓冲（delta 帧 reason 字段）
let renderTimer: number | null = null
let abortCtl: AbortController | null = null
// 本 run 前身最近一次 abort 时刻：doStart 起跑前据此补足「后端 aiBusy 释放宽限」——
// 换目标重置/关面板续跑场景下 abort 与新请求同 tick 发出，后端要等旧 handler 返回（ctx 取消
// →上游断开→栈展开→defer Store(false)）才释放闸门，紧随的请求可能撞 409（审计修复）
let lastAbortAt = 0
const AI_RELEASE_GRACE_MS = 350
// 运行令牌（M1 审计修复）：重开新 run 后，旧 run 续体/迟到帧不得触碰新 run 状态
let runSeq = 0

// ===== 配置与空态 =====
const cfgOk = computed(() => !!cfg.value && cfg.value.enabled && cfg.value.hasApiKey)
const showEmpty = computed(() => !!cfg.value && (!cfg.value.enabled || !cfg.value.hasApiKey))
const cfgFailed = computed(() => !cfg.value && !cfgLoading.value)
const emptyTitle = computed(() => {
  if (!cfg.value) return 'AI 配置读取失败'
  return !cfg.value.enabled ? 'AI 分析未启用' : '未配置 API Key'
})
const hostLabel = computed(() => {
  const u = cfg.value?.baseUrl ?? ''
  try {
    return new URL(u).host
  } catch {
    return u || '未配置服务地址'
  }
})
// 当前模型显示名：取「当前」条目的别名，否则模型原名（模型未配置时隐藏该段）
const modelLabel = computed(() => {
  const c = cfg.value
  if (!c) return ''
  const cur = (c.entries ?? []).find((m) => m.current)
  return cur?.alias?.trim() || cur?.model || c.model
})

async function loadCfg(): Promise<void> {
  if (cfgLoading.value) return
  cfgLoading.value = true
  try {
    cfg.value = await props.api.getAIConfig()
  } catch (e) {
    cfg.value = null
    settingMsg.value = ''
  } finally {
    cfgLoading.value = false
  }
  // explain 自动开始（设计 §7.1）：面板打开且 cfg 就绪后触发；redact 关闭时 requestStart(auto) 会放弃
  if (props.show && activeTab.value === 'explain' && props.flowId && cfgOk.value) requestStart(true)
}

// 空态引导（设计 §九）：http 真唤起主窗 / demo、wails 静态提示文案
async function goSettings(): Promise<void> {
  try {
    const r = await props.api.openSettingsAI()
    settingMsg.value = r.msg
  } catch (e) {
    settingMsg.value = String((e as Error)?.message ?? e)
  }
}
// 非 http 形态的空态提示文案预取（demo/wails 无副作用）
watch(showEmpty, (v) => {
  if (v && props.api.mode !== 'http') void goSettings()
})

// ===== 候选 ids =====
const checkedSet = computed(() => new Set(props.checkedIds))
// 候选池按 StartedAt 降序（最新优先；后端仍会降序复查截断）
const byStartedDesc = computed(() => [...props.viewFlows].sort((a, b) => b.StartedAt - a.StartedAt).map((f) => f.ID))
const intentIds = computed(() => (props.checkedIds.length ? byStartedDesc.value.filter((id) => checkedSet.value.has(id)) : byStartedDesc.value))

const flowMap = computed(() => new Map(props.viewFlows.map((f) => [f.ID, f])))
function flowLabelOf(id: string): string {
  const f = flowMap.value.get(id)
  return f ? f.Method + ' ' + (f.Path || f.URL) : id
}

// intent 结果行 = 候选池里有结果的，按模型输出序号排
const intentResults = computed<IntentResult[]>(() =>
  intentIds.value
    .map((id) => intentOf(id))
    .filter((it): it is IntentResult => !!it)
    .sort((a, b) => a.seq - b.seq),
)

// ===== 文案 =====
const scopeText = computed(() => {
  if (activeTab.value === 'explain') {
    // 显示 doStart 时快照的目标流：flowId 跟随列表选中流后，浏览点行/翻页引起的
    // flowId 变化不得让已完成结果错挂新标签（runTarget 为空时兜底当前 flowId）
    const t = runTarget.value || props.flowId || ''
    return `解读目标：${flowLabelOf(t) || '—'}`
  }
  if (activeTab.value === 'intent') {
    const verb = singleMode.value ? '逐条分析' : '批量分析'
    return props.checkedIds.length
      ? `将${verb}已勾选的 ${props.checkedIds.length} 条流`
      : `未勾选，将${verb}当前视图 ${byStartedDesc.value.length} 条流（共 ${props.viewTotal} 条）`
  }
  const kw = props.keyword ? `，关键词「${props.keyword}」` : ''
  return `将分析当前视图 ${byStartedDesc.value.length} 条流（${props.tagName}${kw}）`
})
// 本轮提问快照（开轮时存入对话记录）：locate/flowmap 为自然语言，explain/intent 无输入则回显范围说明
const qAsked = computed(() => question.value.trim() || scopeText.value)
// 未开启脱敏时，开始分析前须经 popconfirm 二次确认（外发隐私风险提示，视图组件内渲染）
const needConfirm = computed(() => !!cfg.value && !cfg.value.redact)
const startDisabled = computed(() => {
  if (phase.value === 'streaming' || !cfgOk.value) return true
  if (activeTab.value === 'explain') return !props.flowId
  if (activeTab.value === 'intent') return busy.value || !intentIds.value.length
  return !question.value.trim()
})
const metaText = computed(() => {
  // 外模式守卫：终态 meta 属于发起模式的历史，不外溢到其他 tab；流式中则保留——
  // 外模式 tab 上「送审 X/Y 条」是唯一的运行进度全局提示（配合停止按钮）
  if (phase.value !== 'streaming' && runMode.value !== activeTab.value) return ''
  // 单条轮询：meta 逐条变化（total 恒为 1），改为展示整轮进度（成功数；分母用循环快照）
  if (runMode.value === 'intent' && runSingle.value && phase.value !== 'idle') {
    let s = `单条轮询 ${intentDone.value}/${singleTotal.value || intentIds.value.length} 条`
    if (phase.value === 'streaming' && noticeMsg.value) s += ' · ' + noticeMsg.value
    return s
  }
  const m = meta.value
  if (!m) return ''
  let s = `送审 ${m.sent}/${m.total} 条 · 预算 ${m.budget.flows} 流 / ${m.budget.kb}KB`
  if (m.truncated) s += ' · 候选超限已截断'
  // length 截断后的自动续写进度（压缩会话/新会话续写可能静默数十秒，必须可见）
  if (phase.value === 'streaming' && noticeMsg.value) s += ' · ' + noticeMsg.value
  return s
})
const intentPct = computed(() => {
  // 单条轮询：每次调用 meta.total=1，分母改用循环开始时的快照数
  const t = runMode.value === 'intent' && runSingle.value ? singleTotal.value || intentIds.value.length : (meta.value?.total ?? intentIds.value.length)
  return t ? Math.round((intentDone.value / t) * 100) : 0
})

// locate 显示缓冲：截掉当前模式末轮的尾部 ```json 块（流式中半截块也不闪现）；
// 大小写不敏感（模型可能输出 ```JSON，与 Go 侧 lastJSONBlock 同语义，L3 审计修复）
const displayMd = computed(() => {
  const text = lastText.value
  if (activeTab.value !== 'locate') return text
  const fences = [...text.matchAll(/```json/gi)]
  const last = fences[fences.length - 1]
  return last?.index != null ? text.slice(0, last.index) : text
})
// 首 token 前的骨架占位：流式进行中且尚无任何输出（仅发起模式 tab 显示，外模式不闪骨架）
const pendingStream = computed(() => phase.value === 'streaming' && runMode.value === activeTab.value && !displayMd.value)
// 视图级 phase：终态/流式只挂发起模式 tab——外模式 tab 视为 idle（无错误条/进度条/骨架，
// 输入可编辑），运行中的全局提示由 running 驱动（RunActions 停止按钮与「AI 分析中」pill）
const viewPhase = computed<Phase>(() => (runMode.value === activeTab.value ? phase.value : 'idle'))
const running = computed(() => phase.value === 'streaming')

// ===== 调用日志与历史记录（右下角抽层，UI 见子组件 ReviewAiLogSheet / ReviewAiHistorySheet）=====
// 数据源（logs/prevLogs/convTurns/matches）在模块级 store useAiSession，本组件仅持有开合态
const prevOpen = ref(false)
const logOpen = ref(false)
const histOpen = ref(false)
const logSheetRef = ref<InstanceType<typeof ReviewAiLogSheet> | null>(null)
// ===== 对话轮次生命周期 =====
// meta 帧=开轮；done/error/stop=关轮（幂等，覆盖 done/error/abort/卸载全路径）。
// 关轮先把缓冲落盘本轮，再清 in-flight 缓冲；同时清残余 50ms 合帧定时器（防悬空回调）
function beginTurn(system: string, user: string): void {
  // 开轮标记用 doStart 快照（runAsked/runMode）：切 tab 后台续跑时不再读实时 activeTab，
  // 避免运行中途的 meta 帧把本轮错标为切换后的模式（lastText 按模式反查会串显）
  curTurn = createTurn(runAsked.value, system, user, runMode.value)
  liveTurn.value = curTurn
}
function closeTurn(fr?: string, usage?: AiUsage): void {
  if (renderTimer !== null) {
    window.clearTimeout(renderTimer)
    renderTimer = null
  }
  if (curTurn) {
    curTurn.reason = reasonBuf
    curTurn.text = mdBuf
    curTurn.finishReason = fr ?? ''
    curTurn.usage = usage
    // 末次结果记录（解读/定位/流程的「上次记录」数据源）：正文非空才覆盖——
    // 开轮即停/无输出的失败轮不抹掉上次可用结果；intent 轮次也写入但视图不消费
    const m = curTurn.mode
    if (m && curTurn.text) {
      lastResults.value[m] = { question: curTurn.question, text: curTurn.text, ts: Date.now() }
    }
  }
  mdBuf = ''
  reasonBuf = ''
  curTurn = null
  liveTurn.value = null
  scheduleSave() // 关轮=内容定稿点，落盘防抖
}
// 非响应式：本 run 起始时间与首 delta 标记
let runStartTs = 0
let sawDelta = false // run 级首 delta 标记：仅驱动「模型开始输出」日志
// 当前模式的末轮模型输出：ConvTurn.mode 记录开轮时所在模式，反向找属于本模式的末轮——
// 切模式/持久化恢复多轮历史后不再跨模式串显，locate 截 json 块也不误伤其他模式的历史轮
// （审计修复：原全局末轮实现会在 explain 跑完切 locate 时串显并误截断）
const lastText = computed(() => {
  for (let i = convTurns.value.length - 1; i >= 0; i--) {
    const t = convTurns.value[i]
    if (t.mode !== activeTab.value) continue
    if (t.text) return t.text
    break // 命中末轮但为空（error 无输出/开轮即停的无输出轮）：不短路，落入下方分流
  }
  // 新 run 流式中的 meta 空轮维持「开轮清屏」骨架；其余情况（含终态空轮）回退持久化的
  // 末次结果——失败/无输出不遮蔽上次记录（intent 单条轮询挤占 MAX_TURNS、旧盘轮次无
  // mode、清空会话后反查落空，均靠此兜底）
  if (phase.value === 'streaming' && runMode.value === activeTab.value) return ''
  return lastResults.value[activeTab.value]?.text ?? ''
})

function toggleLog(): void {
  logOpen.value = !logOpen.value
  if (logOpen.value) {
    histOpen.value = false // 两抽层同位互斥
    if (phase.value === 'streaming') logSheetRef.value?.openConv() // 流式中打开优先展示实时对话
  }
}

function toggleHist(): void {
  histOpen.value = !histOpen.value
  if (histOpen.value) logOpen.value = false
}

// ===== 导出当前模块最新结果 =====
// 数据源与主区显示同源兜底：lastResults（关轮定稿）优先，缺失时反查 convTurns 本模式末轮；
// locate 导出含 ```json 块原文（结构化匹配数据随文带走，与历史记录册口径一致）
const HIST_MODES: readonly AiChatMode[] = ['explain', 'locate', 'flowmap']
const curModeResult = computed<{ mode: string; question: string; text: string; ts: number } | null>(() => {
  const m = activeTab.value
  if (!HIST_MODES.includes(m)) return null // intent 结果在意图列表自管理，无 md 输出可导
  const lr = lastResults.value[m]
  if (lr?.text) return { mode: m, question: lr.question, text: lr.text, ts: lr.ts }
  for (let i = convTurns.value.length - 1; i >= 0; i--) {
    const t = convTurns.value[i]
    if (t.mode === m && t.text) return { mode: m, question: t.question, text: t.text, ts: t.ts ?? 0 }
  }
  return null
})
const canExportCur = computed(() => viewPhase.value !== 'streaming' && !!curModeResult.value)
function exportCurrent(): void {
  const r = curModeResult.value
  if (r) downloadMd([r], MODE_LABEL[r.mode] ?? r.mode)
}

// ===== 面板宽度拖拽 + 缓存 =====
// 左缘把手左右拖动调宽，pointerup 落盘 localStorage；双击恢复 CSS 默认 560px。
// 缓存值以内联 width: min(px, calc(100% - 保留)) 生效——窗口变小时 CSS 就近钳制不溢出容器，
// 无需 JS 响应 resize（与日志抽层高度拖拽同策略）
const AI_W_KEY = 'prismproxy:review-ai-panel-w-v1'
const AI_W_MIN = 420 // 拖拽下限：四模式 tabs + 正文区可读的最小宽度（窄窗口下实际显示宽由下方 CSS min() 就近钳制，可低于此值，G-4 已知设计）
const AI_W_RESERVE = 400 // 底层列表区可视保留；上限 = 容器宽 - 400
const drawerEl = ref<HTMLElement | null>(null)
const aiW = ref(0) // 0 = 未自定义，走 CSS 默认 560px
const wDragging = ref(false)
let wDragStartX = 0
let wDragStartW = 0

try {
  const v = Number(localStorage.getItem(AI_W_KEY))
  if (v >= AI_W_MIN) aiW.value = v
} catch {
  /* 存储不可用仅本会话生效 */
}

const aiWStyle = computed<{ width: string } | undefined>(() =>
  aiW.value > 0 ? { width: `min(${aiW.value}px, calc(100% - ${AI_W_RESERVE}px))` } : undefined,
)

function aiMaxW(): number {
  const cw = drawerEl.value?.parentElement?.clientWidth ?? 0
  return Math.max(AI_W_MIN, cw - AI_W_RESERVE)
}

function startWDrag(e: PointerEvent): void {
  if (e.button !== 0) return // 仅左键拖拽（对齐 I1 审计修复）
  const el = drawerEl.value
  if (!el) return
  wDragging.value = true
  wDragStartX = e.clientX
  wDragStartW = el.clientWidth // 不含 1px border-left，= style 值零漂移（G-1 审计修复，与日志抽层同步）
  window.addEventListener('pointermove', onWDragMove)
  window.addEventListener('pointerup', onWDragEnd)
  window.addEventListener('pointercancel', onWDragEnd)
  document.body.classList.add('ew-resizing')
  try {
    ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
  } catch {
    /* 合成事件无活动指针，捕获失败不影响拖拽逻辑 */
  }
  e.preventDefault() // 防触发文本选择
}

function onWDragMove(e: PointerEvent): void {
  if (!wDragging.value) return
  // 向左拖（clientX 减小）增宽
  aiW.value = Math.min(Math.max(wDragStartW + (wDragStartX - e.clientX), AI_W_MIN), aiMaxW())
}

function onWDragEnd(): void {
  if (!wDragging.value) return
  wDragging.value = false
  window.removeEventListener('pointermove', onWDragMove)
  window.removeEventListener('pointerup', onWDragEnd)
  window.removeEventListener('pointercancel', onWDragEnd)
  document.body.classList.remove('ew-resizing')
  try {
    if (aiW.value >= AI_W_MIN) localStorage.setItem(AI_W_KEY, String(aiW.value)) // 未达下限（0 哨兵）不落盘，防纯点击残留 "0"（G-3 审计修复，与日志抽层同步）
  } catch {
    /* 存储不可用仅本会话生效 */
  }
}

// 双击把手恢复默认宽度
function resetW(): void {
  aiW.value = 0
  try {
    localStorage.removeItem(AI_W_KEY)
  } catch {
    /* 忽略 */
  }
}

// ===== 运行控制 =====
function requestStart(auto = false): void {
  if (phase.value === 'streaming') return
  // 自动开始（explain 进入面板）遇 redact 关闭：放弃，留待用户手点（popconfirm 无法代答）
  if (auto && cfg.value && !cfg.value.redact) return
  if (!noticeSeen()) {
    noticeShow.value = true
    return
  }
  void doStart()
}

function noticeSeen(): boolean {
  try {
    return localStorage.getItem(NOTICE_KEY) === '1'
  } catch {
    return false
  }
}

function onNoticeOk(): void {
  try {
    localStorage.setItem(NOTICE_KEY, '1')
  } catch {
    /* 忽略存储失败 */
  }
  void doStart()
}

async function doStart(): Promise<void> {
  resetRun()
  // run 级快照（在 resetRun 之后）：切 tab 后台续跑期间读发起时的模式/选项，不随 tab 漂移；
  // 单条轮询循环逐条读实时值会被外模式 tab 上的改动影响，includeReq 一并定格
  runMode.value = activeTab.value
  runSingle.value = singleMode.value
  runAsked.value = qAsked.value
  const includeReqAt = includeReq.value
  // explain 目标快照（在 resetRun 之后：stopRun 已清 runTarget）
  if (runMode.value === 'explain') runTarget.value = props.flowId || ''
  phase.value = 'streaming'
  runStartTs = Date.now()
  sawDelta = false
  addLog('start', TABS.find((t) => t.key === runMode.value)?.label + ' · ' + scopeText.value)
  const ctl = new AbortController()
  abortCtl = ctl
  const seq = ++runSeq
  // 运行级迟到帧守卫：本 run 被停/被重开后，旧流残余帧不污染新 run 缓冲
  const frameOf = (ev: AiChatEvent): void => {
    if (seq === runSeq) onFrame(ev)
  }
  // 释放宽限（审计修复）：本 run 前身刚被 abort 时，后端 aiBusy 闸门要等旧 handler 返回才
  // 释放（异步毫秒级），与新请求存在竞态 → 409「已有 AI 分析任务在进行」红条。补足宽限再
  // 发请求；冷启动（lastAbortAt 久远）零等待直过。等待期间 UI 已入 streaming 骨架
  const sinceAbort = Date.now() - lastAbortAt
  if (sinceAbort < AI_RELEASE_GRACE_MS) {
    await new Promise((r) => setTimeout(r, AI_RELEASE_GRACE_MS - sinceAbort))
  }
  try {
    if (runMode.value === 'intent' && runSingle.value) {
      // 单条轮询：逐条独立调用模型（每条一次完整往返），规避整批 prompt 过大导致的
      // 输出截断/首响应超时。单条失败不中断后续；停止或 run 被重开立即退出循环。
      // 候选列表开始时快照：AI 抽屉无遮罩，运行中翻页会清空 checkedIds（intentIds
      // 静默膨胀为全视图）、筛选重置会产生 undefined id——实时读 computed 会错标/伪失败。
      let failed = 0
      const ids = [...intentIds.value]
      const total = ids.length
      singleTotal.value = total // 进度分母同步快照，防运行中分母漂移
      for (let i = 0; i < total; i++) {
        if (seq !== runSeq || ctl.signal.aborted) break
        const id = ids[i]
        // 单条轮询帧守卫：done/error 帧只代表单条结束——done 记录后继续下一条；
        // error 不置 error 态，由 analyze 的 reject 交循环 catch 统一计数（http/wails 实现均帧后抛出）
        const itemFrame = (ev: AiChatEvent): void => {
          if (seq !== runSeq) return
          if (ev.event === 'done') {
            const len = mdBuf.length // closeTurn 会清缓冲，字数先取
            const cont = ev.data?.continued ?? 0
            closeTurn(ev.data?.finishReason, ev.data?.usage)
            noticeMsg.value = '' // 本条续写状态不带到下一条
            const fr = ev.data?.finishReason
            let tail = ''
            if (fr === 'length') tail = ' · 输出被截断（模型上下文不足）'
            else if (cont > 0) tail = ` · 经 ${cont} 次自动续写完成`
            addLog('done', `第 ${i + 1}/${total} 条完成 · ${len} 字` + tail)
            return
          }
          if (ev.event === 'error') return
          onFrame(ev)
        }
        try {
          await runIntent(props.api, [id], itemFrame, ctl.signal, { includeReqBody: includeReqAt })
        } catch (e) {
          if (ctl.signal.aborted || seq !== runSeq) break
          closeTurn() // 本条中途失败（error 帧被 itemFrame 拦截或网络异常）：关闭进行中轮次再继续下一条
          failed++
          addLog('error', `第 ${i + 1}/${total} 条失败：${flowLabelOf(id)} · ${String((e as Error)?.message ?? e)}`)
        }
      }
      if (seq === runSeq && phase.value === 'streaming' && total > 0) {
        if (failed >= total) {
          // 全部失败（系统性故障典型场景）：对齐批量失败语义，置 error 态让主界面红条可见
          phase.value = 'error'
          errMsg.value = `单条轮询全部失败（0/${total}），详见日志抽层`
        } else if (failed > 0) {
          addLog('error', `轮询结束 · 成功 ${total - failed}/${total} · 失败 ${failed} 条`)
        }
      }
    } else if (runMode.value === 'intent') {
      await runIntent(props.api, intentIds.value, frameOf, ctl.signal, { includeReqBody: includeReqAt })
    } else {
      await props.api.analyze(
        {
          mode: runMode.value,
          flowId: runMode.value === 'explain' ? props.flowId : undefined,
          ids: runMode.value === 'explain' ? undefined : byStartedDesc.value,
          question: runMode.value === 'locate' || runMode.value === 'flowmap' ? question.value.trim() : undefined,
          options: {
            includeReqBody: includeReqAt,
            includeRespBody: runMode.value === 'locate' ? includeResp.value : undefined,
            language: 'zh',
          },
        },
        frameOf,
        ctl.signal,
      )
    }
    // 令牌校验：仅最新 run 可迁移状态（旧 run 续体读到新 run 的 streaming 也不得误标）
    if (seq === runSeq && phase.value === 'streaming') phase.value = 'done'
  } catch (e) {
    closeTurn() // 无 error 帧直接 reject（网络中断等）：关闭进行中轮次（幂等）
    // 停止（abort 静默收尾）后到达的 reject 不转 error 态；旧 run 的 reject 不覆盖新 run
    if (seq === runSeq && phase.value === 'streaming') {
      phase.value = 'error'
      errMsg.value = String((e as Error)?.message ?? e)
    }
  } finally {
    // 只回收自己的句柄：竞态下 abortCtl 可能已被新 run 换掉（M1 审计修复）
    if (abortCtl === ctl) abortCtl = null
  }
}

function onFrame(ev: AiChatEvent): void {
  // 迟到帧守卫：stop/done/error 之后到达的帧一律忽略
  if (phase.value !== 'streaming') return
  switch (ev.event) {
    case 'meta': {
      meta.value = ev.data
      const m = ev.data
      beginTurn(m.system ?? '', m.user ?? '') // 每次送审开新轮：单条轮询每条流独立成对气泡
      addLog('meta', `送审 ${m.sent}/${m.total} 条 · 预算 ${m.budget.flows} 流 / ${m.budget.kb}KB` + (m.truncated ? ' · 已截断' : ''))
      break
    }
    case 'delta':
      if (ev.data.reason) pushReason(ev.data.reason)
      if (ev.data.text) pushDelta(ev.data.text)
      break
    case 'intent':
      intentDone.value++
      upsert(ev.data) // P1 审计修复：缓存写入挪进 runSeq+phase 双守卫内（旧任务迟到帧不再污染共享缓存）
      addLog('intent', `#${ev.data.seq} ${ev.data.intent}` + (ev.data.needsBody ? '（需正文）' : ''))
      break
    case 'match':
      upsertMatch(ev.data)
      addLog('match', `#${ev.data.rank} ${ev.data.method} ${ev.data.url} · ${confLabel(ev.data.confidence)}`)
      break
    case 'notice': {
      // length 截断 → 后端自动开「临时压缩会话 + 新会话续写」：日志留痕 + metaText 短进度
      const round = ev.data.round ?? 1
      if (ev.data.stage === 'compressing') {
        noticeMsg.value = '续写中：压缩历史对话…'
        addLog('notice', `检测到输出截断 · 第 ${round} 轮自动续写：临时会话正在压缩历史对话…`)
      } else if (ev.data.stage === 'continuing') {
        noticeMsg.value = '续写中：新会话输出…'
        addLog('notice', `第 ${round} 轮历史压缩完成 · 已开启新会话续写`)
      } else {
        noticeMsg.value = '续写失败'
        addLog('error', ev.data.message || '自动续写失败，已保留截断稿')
      }
      break
    }
    case 'error':
      closeTurn() // 本轮就此终止，缓冲落盘后不再累积
      phase.value = 'error'
      errMsg.value = ev.data.message
      addLog('error', ev.data.message)
      break
    case 'done': {
      const len = mdBuf.length // closeTurn 会清缓冲，字数先取
      const cont = ev.data?.continued ?? 0
      closeTurn(ev.data?.finishReason, ev.data?.usage)
      phase.value = 'done'
      noticeMsg.value = ''
      // finishReason 如实来自上游（length=输出预算耗尽被截断）；continued>0=经自动续写后收尾
      const fr = ev.data?.finishReason
      let tail = ''
      if (fr === 'length') {
        tail = ' · 输出被截断（模型上下文/输出预算不足），建议减小批量流数或精简正文'
      } else if (cont > 0) {
        tail = ` · 经 ${cont} 次自动续写完成`
      }
      addLog('done', `共 ${len} 字` + (runStartTs ? ` · 耗时 ${((Date.now() - runStartTs) / 1000).toFixed(1)}s` : '') + tail)
      break
    }
  }
}

// 50ms 合帧统一出口：正文与思考增量共用同一 timer，同时写入当前 in-flight 轮
function scheduleRender(): void {
  if (renderTimer !== null) return
  renderTimer = window.setTimeout(() => {
    renderTimer = null
    if (curTurn) {
      curTurn.text = mdBuf
      curTurn.reason = reasonBuf
    }
  }, 50)
}
function pushDelta(text: string): void {
  // 孤儿 delta 守卫：无进行中轮次（关轮后残余帧/服务端违反 meta 先行的违约顺序）直接丢弃——
  // 否则污染下一轮缓冲，且抢先置 sawDelta 抑制下一轮「模型开始输出」日志
  if (!curTurn) return
  if (!sawDelta) {
    sawDelta = true
    addLog('delta', '模型开始输出')
  }
  // 本轮首个正文增量：思考阶段结束，自动收起本轮思考区（用户手动开合过则不打扰）
  if (curTurn && !mdBuf && !curTurn.touched && curTurn.open) curTurn.open = false
  // 正文折叠默认收起：首个正文增量自动展开本轮直播输出，保持流式可见（用户手动收起过则不打扰）
  if (curTurn && !mdBuf && !curTurn.txtTouched && !curTurn.txtOpen) curTurn.txtOpen = true
  mdBuf += text
  scheduleRender()
}
function pushReason(reason: string): void {
  // 思考增量先于正文到达：自动展开本轮思考区（正文开始后不再打扰；touched 已由子组件 toggleTurn 置位）
  if (curTurn && !reasonBuf && !curTurn.touched && !curTurn.open) curTurn.open = true
  reasonBuf += reason
  scheduleRender()
}

function upsertMatch(m: AiMatchItem): void {
  const i = matches.value.findIndex((x) => x.flowId === m.flowId)
  if (i >= 0) matches.value.splice(i, 1, m)
  else matches.value.push(m)
  matches.value.sort((a, b) => a.rank - b.rank)
}

function stop(): void {
  if (phase.value !== 'streaming') return
  phase.value = 'stopped'
  abortCtl?.abort()
  lastAbortAt = Date.now()
  abortCtl = null
  closeTurn() // P2 审计修复：清残余 50ms 合帧定时器并把最后一批 delta 落盘本轮（防悬空回调）
  addLog('stop', '已手动停止')
}

// 停流清 in-flight（仅经 resetRun 在新 run 发起前调用）：保留对话轮次、日志与匹配（阶段三持久化语义）
function stopRun(): void {
  stop()
  phase.value = 'idle'
  errMsg.value = ''
  mdBuf = ''
  reasonBuf = ''
  curTurn = null
  meta.value = null
  runTarget.value = ''
  noticeMsg.value = ''
}
// 仅 doStart 前的全量清场：新 run 独占界面态，日志归档为上一轮供回看（G3 审计建议）
function resetRun(): void {
  stopRun()
  matches.value = []
  intentDone.value = 0
  singleTotal.value = 0
  if (archiveLogs()) prevOpen.value = false
}

// 关面板不停流（后台续跑，与切 tab 同构）：重开面板经 viewPhase 派生自动恢复显示进行中
// 任务（进度/骨架/停止按钮跨 tab 可见），跑完重开直接回显结果。停止唯一入口=停止按钮；
// 外发知情已在发起时确认（redact 提示），续跑是该次同意的延续
function closePanel(): void {
  emit('update:show', false)
}

// Esc 关闭面板（streaming 后台续跑不停流）；输入框聚焦时不拦截
function onEsc(e: KeyboardEvent): void {
  if (e.key !== 'Escape' || noticeShow.value) return
  const t = e.target as HTMLElement | null
  if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return
  // 抽层展开时先收抽层，再按一次 Esc 才关面板（Z1 审计修复：交互层级）；
  // 两抽层同位互斥不会同时展开，历史记录无拖拽态，直接收合
  if (histOpen.value) {
    histOpen.value = false
    return
  }
  if (logOpen.value) {
    logSheetRef.value?.endDrag() // 拖拽中收抽层先终止拖拽态（摘监听/落盘，I3 审计修复；拖拽态已迁入子组件，经 expose 命令式收尾）
    if (wDragging.value) onWDragEnd() // 宽度拖拽把手上部在抽层外仍可达：收抽层同时终止（与上行 I3 同构：一次 Esc = 终止拖拽 + 逐层收合，G-2 审计修复）
    logOpen.value = false
    return
  }
  // 宽度拖拽中先终止拖拽态（摘监听/落盘，对齐 I3），Esc 语义继续走层级收合
  if (wDragging.value) onWDragEnd()
  closePanel()
}

// ===== 结果辅助 =====
function confCls(c: 'high' | 'medium' | 'low'): string {
  return c === 'high' ? 'k-ai-conf-high' : c === 'medium' ? 'k-ai-conf-med' : 'k-ai-low'
}
function confLabel(c: 'high' | 'medium' | 'low'): string {
  return c === 'high' ? '高' : c === 'medium' ? '中' : '低'
}

// ===== 联动 =====
watch(
  () => props.mode,
  (m) => {
    activeTab.value = m
  },
)
// 切 tab 不停流（状态记忆）：后台续跑，任务不因浏览其他模式而中断——
// 运行态经 running 跨 tab 可见，结果/终态归属发起模式（viewPhase/lastText 按 mode 反查）

watch(
  () => props.show,
  (s) => {
    if (s) {
      // 打开不重置 activeTab（状态记忆：恢复上次打开的 tab）；显式模式入口经 props.mode
      // watch 切换（aiPanelMode 变化先于 show=true 触发，watch 顺序见上方联动节）
      // 停止按钮留下的 stopped 终态在重开时归位：上次结果以干净态回显（done/error 保留）
      if (phase.value === 'stopped') phase.value = 'idle'
      void loadCfg()
      window.addEventListener('keydown', onEsc)
      // explain 自动开始统一由 explainAutoTick watch 驱动；cfg 未就绪时由 loadCfg 完成回调兜底
    } else {
      // 关面板不停流（后台续跑）：重开面板自动恢复显示进行中任务/已完成结果；
      // 停止唯一入口=停止按钮（或离开复盘视图触发卸载 stop）
      window.removeEventListener('keydown', onEsc)
    }
  },
)

// explain 显式入口信号（详情页「AI 解读」/路径列意图摘要递增）：换目标重置并自动解读。
// 列表浏览（点行/翻页/筛选）不递增 → 面板不受影响：在跑继续、结果保留（runTarget 自洽）
watch(
  () => props.explainAutoTick,
  () => {
    if (!props.show || !cfgOk.value) return
    // tick 自带「强制进 explain」语义（审计修复）：mode 同值赋值不触发 props.mode watch，
    // 面板可能停在其他 tab（上次手动切走）——显式点击必须切回解读再自动开始，否则点击意图丢失
    activeTab.value = 'explain'
    resetRun()
    requestStart(true)
  },
)

// 卸载清理（M2 审计修复）：主窗切走复盘视图（v-if 卸载）时停掉在跑流（隐私外发中止）、
// 摘除 Esc 监听（该监听仅 show→false 时移除，跨挂载会泄漏）
onBeforeUnmount(() => {
  stop()
  closeTurn() // P2 审计修复：非 streaming 卸载（如 error 态）时合帧定时器可能仍挂，兜底清掉
  window.removeEventListener('keydown', onEsc)
  // 宽度拖拽中卸载：同上兜底（摘监听/还原光标/落盘）
  if (wDragging.value) onWDragEnd()
})
</script>
