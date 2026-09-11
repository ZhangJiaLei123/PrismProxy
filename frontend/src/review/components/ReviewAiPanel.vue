<template>
  <!-- AI 分析面板（设计 §7）：自绘右侧滑入抽屉（非 n-drawer、无遮罩，可边看列表边读）；
       absolute 定位于 .review-root 内——主窗内嵌时只覆盖复盘页区域，独立窗口等效 fixed -->
  <aside class="ai-drawer" :class="{ open: show }">
    <header class="ai-head">
      <span class="ai-title">
        <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor">
          <path d="M8 1l1.9 4.6 4.6 1.9-4.6 1.9L8 14 6.1 9.4 1.5 7.5l4.6-1.9L8 1zm4.5 9.5l.8 1.9 1.9.8-1.9.8-.8 1.9-.8-1.9-1.9-.8 1.9-.8.8-1.9z" />
        </svg>
        AI 分析
      </span>
      <n-tabs v-model:value="activeTab" type="segment" size="small" class="ai-tabs">
        <n-tab-pane v-for="t in TABS" :key="t.key" :name="t.key" :tab="t.label" />
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
        <!-- 范围说明：模式差异文案（勾选集 / 当前视图 / 单流目标） -->
        <div class="ai-scope">{{ scopeText }}</div>

        <!-- 模式专属输入：locate/flowmap 自然语言目标；explain/intent 无输入 -->
        <n-input
          v-if="activeTab === 'locate' || activeTab === 'flowmap'"
          v-model:value="question"
          type="textarea"
          :rows="2"
          :placeholder="qPlaceholder"
          :disabled="phase === 'streaming'"
        />

        <!-- 正文开关（对齐后端 OR 语义：explain/flowmap 默认必带正文、关不掉，故不给开关） -->
        <div v-if="activeTab === 'intent' || activeTab === 'locate'" class="ai-opts">
          <n-checkbox v-model:checked="includeReq" :disabled="phase === 'streaming'">包含请求正文</n-checkbox>
          <n-checkbox v-if="activeTab === 'locate'" v-model:checked="includeResp" :disabled="phase === 'streaming'">包含响应正文</n-checkbox>
        </div>

        <!-- 动作行：开始（redact 关闭时每次需确认）/停止互斥 + 运行状态 -->
        <div class="ai-actions">
          <n-popconfirm
            v-if="cfg && !cfg.redact"
            placement="top-start"
            positive-text="继续"
            negative-text="取消"
            @positive-click="requestStart()"
          >
            <template #trigger>
              <n-button type="primary" size="small" :disabled="startDisabled">{{ startLabel }}</n-button>
            </template>
            当前未开启脱敏，请求头将原样发送给 AI 服务，确定继续？
          </n-popconfirm>
          <n-button v-else type="primary" size="small" :disabled="startDisabled" @click="requestStart()">{{ startLabel }}</n-button>
          <n-button v-if="phase === 'streaming'" size="small" quaternary @click="stop">停止</n-button>
          <span v-if="metaText" class="ai-meta">{{ metaText }}</span>
          <span v-else-if="phase === 'done'" class="ai-state">已完成</span>
          <span v-else-if="phase === 'stopped'" class="ai-state">已停止</span>
        </div>

        <!-- intent 进度（n/total，tabular-nums 防数字抖动） -->
        <n-progress
          v-if="activeTab === 'intent' && phase !== 'idle'"
          type="line"
          :percentage="intentPct"
          :show-indicator="false"
          class="ai-progress"
        />

        <div v-if="phase === 'error'" class="ai-error">{{ errMsg }}</div>

        <!-- intent 结果列表：设计 §7.2 无 Markdown 区（delta 里的列表/JSON 不展示，仅收 intent 帧） -->
        <div v-if="activeTab === 'intent'" class="ai-intents">
          <div
            v-for="it in intentResults"
            :key="it.flowId"
            class="ai-intent-row"
            :title="intentRowTip(it)"
            @click="emit('locate', it.flowId)"
          >
            <span class="ai-intent-seq">#{{ it.seq }}</span>
            <span class="ai-intent-text" :class="{ 'k-ai-low': it.confidence === 'low', 'k-ai-needs-body': it.needsBody }">{{ it.intent }}</span>
            <span class="ai-intent-flow">{{ flowLabelOf(it.flowId) }}</span>
          </div>
          <div v-if="!intentResults.length && phase !== 'idle'" class="ai-hint">{{ phase === 'streaming' ? '标注中…' : '暂无结果' }}</div>
        </div>

        <!-- Markdown 结论区（explain/locate/flowmap）：50ms 节流渲染，v-html 唯一出口经 renderMarkdown 净化 -->
        <div v-else class="ai-md" v-html="renderedHtml"></div>

        <!-- locate 匹配卡片：rank/method/url/置信度/理由/[查看→] -->
        <div v-if="activeTab === 'locate' && matches.length" class="ai-matches">
          <div v-for="m in matches" :key="m.flowId" class="ai-match">
            <div class="ai-match-head">
              <span class="ai-match-rank">#{{ m.rank }}</span>
              <span class="ai-match-method">{{ m.method }}</span>
              <span class="ai-match-url" :title="m.url">{{ m.url }}</span>
              <span class="ai-match-conf" :class="confCls(m.confidence)">{{ confLabel(m.confidence) }}</span>
            </div>
            <div class="ai-match-reason">
              <span class="ai-match-reason-text" :title="m.reason">{{ m.reason }}</span>
              <n-button size="tiny" quaternary class="ai-match-go" @click="emit('locate', m.flowId)">查看 →</n-button>
            </div>
          </div>
        </div>
      </template>
    </div>

    <!-- 底部外发告知常驻小字（设计 §8-5） -->
    <footer v-if="cfgOk" class="ai-foot">分析数据将发送至 {{ hostLabel }}</footer>

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
import { computed, ref, watch } from 'vue'
import { NButton, NCheckbox, NInput, NModal, NPopconfirm, NProgress, NTabPane, NTabs } from 'naive-ui'
import type { AiChatEvent, AiChatMeta, AiChatMode, AIApiConfigView, ReviewApi } from '../api'
import type { AiMatchItem, IntentResult, ReviewFlowMeta } from '../../lib/types'
import { renderMarkdown } from '../ai-md'
import { useIntents } from '../useIntents'

// 面板状态机（设计 §7.3）：idle → streaming(meta→delta/intent/match*) → done|stopped|error；
// 切模式/换流重置，同面板同时只跑一个任务
type Phase = 'idle' | 'streaming' | 'done' | 'stopped' | 'error'

const props = defineProps<{
  api: ReviewApi
  show: boolean
  mode: AiChatMode
  /** explain 模式目标流（唯一必填输入） */
  flowId?: string
  /** explain 目标展示名（method + path） */
  flowLabel?: string
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

const { intentOf, runIntent } = useIntents()

const activeTab = ref<AiChatMode>(props.mode)
const phase = ref<Phase>('idle')
const errMsg = ref('')
const meta = ref<AiChatMeta | null>(null)
const matches = ref<AiMatchItem[]>([])
const intentDone = ref(0)
const rendered = ref('')
const question = ref('')
const includeReq = ref(false)
const includeResp = ref(false)
const cfg = ref<AIApiConfigView | null>(null)
const cfgLoading = ref(false)
const noticeShow = ref(false)
const settingMsg = ref('')

// 非响应式：delta 原始缓冲与渲染节流句柄（50ms 合帧，避免逐 token 重排）
let mdBuf = ''
let renderTimer: number | null = null
let abortCtl: AbortController | null = null

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
  if (activeTab.value === 'explain') return `解读目标：${props.flowLabel || props.flowId || '—'}`
  if (activeTab.value === 'intent') {
    return props.checkedIds.length
      ? `将批量分析已勾选的 ${props.checkedIds.length} 条流`
      : `未勾选，将分析当前视图 ${byStartedDesc.value.length} 条流（共 ${props.viewTotal} 条）`
  }
  const kw = props.keyword ? `，关键词「${props.keyword}」` : ''
  return `将分析当前视图 ${byStartedDesc.value.length} 条流（${props.tagName}${kw}）`
})
const qPlaceholder = computed(() =>
  activeTab.value === 'locate' ? '例：找出登录接口的响应位置' : '例：梳理下单流程的接口调用顺序',
)
const startLabel = computed(() =>
  activeTab.value === 'intent' ? '开始标注' : activeTab.value === 'explain' ? '开始解读' : '开始分析',
)
const startDisabled = computed(() => {
  if (phase.value === 'streaming' || !cfgOk.value) return true
  if (activeTab.value === 'explain') return !props.flowId
  if (activeTab.value === 'intent') return !intentIds.value.length
  return !question.value.trim()
})
const metaText = computed(() => {
  const m = meta.value
  if (!m) return ''
  let s = `送审 ${m.sent}/${m.total} 条 · 预算 ${m.budget.flows} 流 / ${m.budget.kb}KB`
  if (m.truncated) s += ' · 候选超限已截断'
  return s
})
const intentPct = computed(() => {
  const t = meta.value?.total ?? intentIds.value.length
  return t ? Math.round((intentDone.value / t) * 100) : 0
})

// locate 显示缓冲：截掉模型尾部 ```json 块（流式中半截块也不闪现）
const displayMd = computed(() => {
  if (activeTab.value !== 'locate') return rendered.value
  const i = rendered.value.lastIndexOf('```json')
  return i >= 0 ? rendered.value.slice(0, i) : rendered.value
})
const renderedHtml = computed(() => renderMarkdown(displayMd.value))

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
  phase.value = 'streaming'
  abortCtl = new AbortController()
  const signal = abortCtl.signal
  try {
    if (activeTab.value === 'intent') {
      await runIntent(props.api, intentIds.value, onFrame, signal, { includeReqBody: includeReq.value })
    } else {
      await props.api.analyze(
        {
          mode: activeTab.value,
          flowId: activeTab.value === 'explain' ? props.flowId : undefined,
          ids: activeTab.value === 'explain' ? undefined : byStartedDesc.value,
          question: activeTab.value === 'locate' || activeTab.value === 'flowmap' ? question.value.trim() : undefined,
          options: {
            includeReqBody: includeReq.value,
            includeRespBody: activeTab.value === 'locate' ? includeResp.value : undefined,
            language: 'zh',
          },
        },
        onFrame,
        signal,
      )
    }
    if (phase.value === 'streaming') phase.value = 'done'
  } catch (e) {
    // 停止（abort 静默收尾）后到达的 reject 不转 error 态
    if (phase.value === 'streaming') {
      phase.value = 'error'
      errMsg.value = String((e as Error)?.message ?? e)
    }
  } finally {
    abortCtl = null
  }
}

function onFrame(ev: AiChatEvent): void {
  // 迟到帧守卫：stop/done/error 之后到达的帧一律忽略
  if (phase.value !== 'streaming') return
  switch (ev.event) {
    case 'meta':
      meta.value = ev.data
      break
    case 'delta':
      if (ev.data.text) pushDelta(ev.data.text)
      break
    case 'intent':
      intentDone.value++
      break
    case 'match':
      upsertMatch(ev.data)
      break
    case 'error':
      phase.value = 'error'
      errMsg.value = ev.data.message
      break
    case 'done':
      flushRender()
      phase.value = 'done'
      break
  }
}

function pushDelta(text: string): void {
  mdBuf += text
  if (renderTimer !== null) return
  renderTimer = window.setTimeout(() => {
    renderTimer = null
    rendered.value = mdBuf
  }, 50)
}
function flushRender(): void {
  if (renderTimer !== null) {
    window.clearTimeout(renderTimer)
    renderTimer = null
  }
  rendered.value = mdBuf
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
  abortCtl = null
}

// 清运行态（保留 question/开关等输入值）
function resetRun(): void {
  stop()
  phase.value = 'idle'
  errMsg.value = ''
  mdBuf = ''
  rendered.value = ''
  meta.value = null
  matches.value = []
  intentDone.value = 0
}

function closePanel(): void {
  stop()
  emit('update:show', false)
}

// Esc 关闭（streaming 先停止）；输入框聚焦时不拦截
function onEsc(e: KeyboardEvent): void {
  if (e.key !== 'Escape' || noticeShow.value) return
  const t = e.target as HTMLElement | null
  if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return
  closePanel()
}

// ===== 结果辅助 =====
function intentRowTip(it: IntentResult): string {
  const tips: string[] = ['点击在列表中定位该流']
  if (it.confidence === 'low') tips.push('AI 对此判定置信度较低，仅供参考')
  if (it.needsBody) tips.push('需结合请求正文才能准确判定；可勾选「包含请求正文」重新标注')
  return tips.join('；')
}
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
// 切模式重置运行态（同面板单任务）
watch(activeTab, () => resetRun())

watch(
  () => props.show,
  (s) => {
    if (s) {
      activeTab.value = props.mode
      void loadCfg()
      window.addEventListener('keydown', onEsc)
      // explain 自动开始：cfg 已缓存时立即触发，否则由 loadCfg 完成回调触发
      if (props.mode === 'explain' && props.flowId && cfgOk.value) requestStart(true)
    } else {
      stop()
      window.removeEventListener('keydown', onEsc)
    }
  },
)

// explain 目标流变化：重置并自动重新解读（仅 cfg 就绪时；避免与 loadCfg 路径双触发，requestStart 有 streaming 守卫）
watch(
  () => props.flowId,
  () => {
    if (props.show) {
      resetRun()
      if (activeTab.value === 'explain' && props.flowId && cfgOk.value) requestStart(true)
    }
  },
)
</script>
