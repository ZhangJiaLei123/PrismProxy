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
          <!-- 分析中状态 pill：弹跳点 + 流动渐变文字（静态线索=紫色底与点色，动画非唯一反馈） -->
          <span v-if="phase === 'streaming'" class="ai-live">
            <i></i><i></i><i></i><span>AI 分析中</span>
          </span>
          <!-- 读屏播报（R1 审计修复）：live region 必须先于消息常驻无障碍树才会被播报；sr-only 视觉隐藏不影响布局 -->
          <span class="ai-sr-live" aria-live="polite">{{ phase === 'streaming' ? 'AI 分析中' : '' }}</span>
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
        <div v-else class="ai-md">
          <!-- 首 token 前的等待骨架：微光横条示意"正在思考" -->
          <div v-if="pendingStream" class="ai-skel" aria-hidden="true">
            <i></i><i></i><i></i><i></i><i></i>
          </div>
          <div v-else v-html="renderedHtml"></div>
        </div>

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

    <!-- 底部：外发告知常驻小字（设计 §8-5）+ 右下角日志入口 -->
    <footer class="ai-foot">
      <span v-if="cfgOk" class="ai-foot-text">
        分析数据将发送至 {{ hostLabel }}<template v-if="modelLabel"> · 模型 {{ modelLabel }}</template>
      </span>
      <button class="ai-logbtn" title="AI 调用日志与对话" @click="toggleLog">
        <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M2.5 4l3.5 3.5L2.5 11" />
          <path d="M8.5 11.5H13" />
        </svg>
        <i v-if="phase === 'streaming'" class="ai-logbtn-dot" aria-hidden="true"></i>
      </button>
    </footer>

    <!-- 调用日志抽层：对话内容（模型原始输出实时流）+ 调用日志（帧级事件），自底部滑入；
         高度可拖拽调整（顶部把手）并经 localStorage 缓存 -->
    <section
      ref="logsheetEl"
      class="ai-logsheet"
      :class="{ open: logOpen, dragging: logDragging }"
      :style="logHStyle"
      aria-label="AI 调用详情"
    >
      <!-- 顶部拖拽把手：上下拖动调整高度，双击恢复默认 -->
      <div class="ai-log-grip" title="拖拽调整高度 · 双击恢复默认" @pointerdown="startLogDrag" @dblclick="resetLogH"></div>
      <header class="ai-log-head">
        <n-tabs v-model:value="logTab" type="segment" size="small" class="ai-log-tabs">
          <n-tab name="conv">对话内容</n-tab>
          <n-tab name="log">调用日志</n-tab>
        </n-tabs>
        <button class="ai-close" title="收起日志" @click="logOpen = false">
          <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor">
            <path d="M8 6.59L12.95 1.64l1.41 1.41L9.41 8l4.95 4.95-1.41 1.41L8 9.41l-4.95 4.95-1.41-1.41L6.59 8 1.64 3.05l1.41-1.41L8 6.59z" />
          </svg>
        </button>
      </header>
      <div ref="logBodyEl" class="ai-log-body">
        <!-- 对话 tab：提问 → 提示词（折叠）→ 思考过程（推理流自动展开、答案开始自动收起）→ 模型输出 -->
        <template v-if="logTab === 'conv'">
          <div v-if="convEmpty" class="ai-hint ai-log-empty">
            {{ phase === 'streaming' ? '等待模型输出…' : '暂无对话内容 · 发起一次分析后这里实时展示模型原始输出' }}
          </div>
          <template v-else>
            <section class="ai-conv-sec">
              <div class="ai-conv-label">提问</div>
              <div class="ai-conv-q">{{ qAsked }}</div>
              <div v-if="hasQuestion" class="ai-conv-scope">{{ scopeText }}</div>
            </section>
            <section v-if="promptSystem || promptUser" class="ai-conv-sec">
              <button class="ai-conv-toggle" :class="{ open: promptOpen }" @click="promptOpen = !promptOpen">
                <svg class="chev" viewBox="0 0 16 16" width="10" height="10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M6 4.5l4 4-4 4" />
                </svg>
                <span>提示词（实际送审）</span>
                <span class="ai-conv-count">{{ promptLen }} 字</span>
              </button>
              <div class="ai-conv-fold" :class="{ open: promptOpen }">
                <div class="ai-conv-fold-in">
                  <div class="ai-conv-label-sub">System</div>
                  <pre class="ai-log-conv">{{ promptSystem }}</pre>
                  <div class="ai-conv-label-sub">User</div>
                  <pre class="ai-log-conv">{{ promptUser }}</pre>
                </div>
              </div>
            </section>
            <section v-if="reasonText" class="ai-conv-sec">
              <button
                class="ai-conv-toggle"
                :class="{ open: reasonOpen, live: phase === 'streaming' && reasonOpen }"
                @click="toggleReason"
              >
                <svg class="chev" viewBox="0 0 16 16" width="10" height="10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M6 4.5l4 4-4 4" />
                </svg>
                <span>思考过程</span>
                <span class="ai-conv-count">{{ reasonText.length }} 字</span>
              </button>
              <div class="ai-conv-fold" :class="{ open: reasonOpen }">
                <pre class="ai-log-conv ai-conv-think">{{ reasonText }}</pre>
              </div>
            </section>
            <pre v-if="convText" class="ai-log-conv">{{ convText }}</pre>
          </template>
        </template>
        <template v-else>
          <div v-if="!logs.length && !prevLogs.length" class="ai-hint ai-log-empty">暂无日志 · 发起分析后逐帧记录调用过程</div>
          <!-- 上一轮归档（G3 审计建议）：默认收起，展开弱化展示最近一轮帧序列 -->
          <button v-if="prevLogs.length" class="ai-log-prev" @click="prevOpen = !prevOpen">
            <svg viewBox="0 0 16 16" width="10" height="10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <path v-if="prevOpen" d="M4 6.5l4 4 4-4" />
              <path v-else d="M6 4.5l4 4-4 4" />
            </svg>
            <span>上一轮 · {{ prevLogs.length }} 条</span>
          </button>
          <template v-if="prevOpen">
            <div v-for="l in prevLogs" :key="l.id" class="ai-log-line is-prev">
              <span class="ai-log-t">{{ fmtT(l.t) }}</span>
              <span class="ai-log-kind" :class="'lk-' + l.kind">{{ LOG_KIND_LABEL[l.kind] }}</span>
              <span class="ai-log-msg" :title="l.msg">{{ l.msg }}</span>
            </div>
          </template>
          <div v-for="l in logs" :key="l.id" class="ai-log-line">
            <span class="ai-log-t">{{ fmtT(l.t) }}</span>
            <span class="ai-log-kind" :class="'lk-' + l.kind">{{ LOG_KIND_LABEL[l.kind] }}</span>
            <span class="ai-log-msg" :title="l.msg">{{ l.msg }}</span>
          </div>
        </template>
      </div>
    </section>

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
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { NButton, NCheckbox, NInput, NModal, NPopconfirm, NProgress, NTab, NTabs } from 'naive-ui'
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

const { intentOf, runIntent, upsert, busy } = useIntents()

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
  if (activeTab.value === 'intent') return busy.value || !intentIds.value.length
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

// locate 显示缓冲：截掉模型尾部 ```json 块（流式中半截块也不闪现）；
// 大小写不敏感（模型可能输出 ```JSON，与 Go 侧 lastJSONBlock 同语义，L3 审计修复）
const displayMd = computed(() => {
  if (activeTab.value !== 'locate') return rendered.value
  const fences = [...rendered.value.matchAll(/```json/gi)]
  const last = fences[fences.length - 1]
  return last?.index != null ? rendered.value.slice(0, last.index) : rendered.value
})
const renderedHtml = computed(() => renderMarkdown(displayMd.value))
// 首 token 前的骨架占位：流式进行中且尚无任何输出
const pendingStream = computed(() => phase.value === 'streaming' && !displayMd.value)

// ===== 调用日志（右下角抽层）=====
// 每次运行独立记录：帧级事件（meta/intent/match/error…）+ 模型原始输出（对话 tab 实时展示）
type LogKind = 'start' | 'meta' | 'delta' | 'intent' | 'match' | 'error' | 'done' | 'stop'
interface LogEntry {
  id: number
  t: number
  kind: LogKind
  msg: string
}
const LOG_KIND_LABEL: Record<LogKind, string> = {
  start: '开始',
  meta: '参数',
  delta: '输出',
  intent: '意图',
  match: '匹配',
  error: '错误',
  done: '完成',
  stop: '停止',
}
const logs = ref<LogEntry[]>([])
// 上一轮归档（G3 审计建议）：新 run 启动时保留最近一轮帧序列供回看
const prevLogs = ref<LogEntry[]>([])
const prevOpen = ref(false)
const logOpen = ref(false)
const logTab = ref<'conv' | 'log'>('log')
const logBodyEl = ref<HTMLElement | null>(null)
// 非响应式：本 run 起始时间、首 delta 标记与日志自增 id（跨 run 单调；
// 500 上限 shift 后长度恒定，滚底 watch 改以末条 id 驱动——A1 审计修复）
let runStartTs = 0
let sawDelta = false
let logSeq = 0

function log(kind: LogKind, msg: string): void {
  logs.value.push({ id: ++logSeq, t: Date.now(), kind, msg })
  if (logs.value.length > 500) logs.value.shift()
}
// 对话 tab = 模型原始输出（非 locate 截断视图，全量原始流）
const convText = computed(() => rendered.value)

function toggleLog(): void {
  logOpen.value = !logOpen.value
  // 流式中打开优先展示实时对话
  if (logOpen.value && phase.value === 'streaming') logTab.value = 'conv'
}
function fmtT(t: number): string {
  const d = new Date(t)
  const p = (n: number): string => String(n).padStart(2, '0')
  return p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds())
}
// 开合/切 tab：导航意图，无条件滚到底展示最新内容
watch([logOpen, logTab], () => {
  if (!logOpen.value) return
  nextTick(() => {
    const el = logBodyEl.value
    if (el && logOpen.value) el.scrollTop = el.scrollHeight
  })
})
// 流式追加（日志帧/对话更新）：仅当视口已近底部（40px 阈值）才跟随滚动，
// 尊重用户上翻回看——B1 审计修复（原先无条件滚底会每 50ms 把用户拉回底部）
watch([() => logs.value[logs.value.length - 1]?.id ?? 0, convText], () => {
  if (!logOpen.value) return
  nextTick(() => {
    const el = logBodyEl.value
    if (!el || !logOpen.value) return
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 40) el.scrollTop = el.scrollHeight
  })
})

// ===== 抽层高度拖拽 + 缓存 =====
// 顶部把手上下拖动调高，pointerup 落盘 localStorage；双击恢复 CSS 默认。
// 缓存值以内联 height: min(px, calc(100% - 保留)) 生效——窗口变小时 CSS 就近钳制不溢出抽屉
const LOG_H_KEY = 'prismproxy:review-ai-logsheet-h-v1'
const LOG_H_MIN = 200 // 与 .ai-logsheet min-height 一致
const LOG_H_RESERVE_EXTRA = 120 // 正文可视保留；上限 = 抽屉高 - footer 高(--ai-foot-h) - 120（I2 审计修复：footer 高单一事实源是 CSS 变量）
const logsheetEl = ref<HTMLElement | null>(null)
const logH = ref(0) // 0 = 未自定义，走 CSS 默认 min(58%, 430px)
const logDragging = ref(false)
let logDragStartY = 0
let logDragStartH = 0

try {
  const v = Number(localStorage.getItem(LOG_H_KEY))
  if (v >= LOG_H_MIN) logH.value = v
} catch {
  /* 存储不可用仅本会话生效 */
}

const logHStyle = computed<{ height: string } | undefined>(() =>
  logH.value > 0
    ? { height: `min(${logH.value}px, calc(100% - var(--ai-foot-h) - ${LOG_H_RESERVE_EXTRA}px))` }
    : undefined,
)

function logMaxH(): number {
  const drawerH = logsheetEl.value?.parentElement?.clientHeight ?? 0
  const footH = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--ai-foot-h')) || 35
  return Math.max(LOG_H_MIN, drawerH - footH - LOG_H_RESERVE_EXTRA)
}

function startLogDrag(e: PointerEvent): void {
  if (e.button !== 0) return // 仅左键拖拽（I1 审计修复：右键/中键不进入拖拽）
  const el = logsheetEl.value
  if (!el) return
  logDragging.value = true
  logDragStartY = e.clientY
  logDragStartH = el.getBoundingClientRect().height
  window.addEventListener('pointermove', onLogDragMove)
  window.addEventListener('pointerup', onLogDragEnd)
  window.addEventListener('pointercancel', onLogDragEnd)
  document.body.classList.add('row-resizing')
  try {
    ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
  } catch {
    /* 合成事件无活动指针，捕获失败不影响拖拽逻辑 */
  }
  e.preventDefault() // 防触发文本选择
}

function onLogDragMove(e: PointerEvent): void {
  if (!logDragging.value) return
  // 向上拖（clientY 减小）增高
  logH.value = Math.min(Math.max(logDragStartH + (logDragStartY - e.clientY), LOG_H_MIN), logMaxH())
}

function onLogDragEnd(): void {
  if (!logDragging.value) return
  logDragging.value = false
  window.removeEventListener('pointermove', onLogDragMove)
  window.removeEventListener('pointerup', onLogDragEnd)
  window.removeEventListener('pointercancel', onLogDragEnd)
  document.body.classList.remove('row-resizing')
  try {
    localStorage.setItem(LOG_H_KEY, String(logH.value))
  } catch {
    /* 存储不可用仅本会话生效 */
  }
}

// 双击把手恢复默认高度
function resetLogH(): void {
  logH.value = 0
  try {
    localStorage.removeItem(LOG_H_KEY)
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
  phase.value = 'streaming'
  runStartTs = Date.now()
  sawDelta = false
  log('start', TABS.find((t) => t.key === activeTab.value)?.label + ' · ' + scopeText.value)
  const ctl = new AbortController()
  abortCtl = ctl
  const seq = ++runSeq
  // 运行级迟到帧守卫：本 run 被停/被重开后，旧流残余帧不污染新 run 缓冲
  const frameOf = (ev: AiChatEvent): void => {
    if (seq === runSeq) onFrame(ev)
  }
  try {
    if (activeTab.value === 'intent') {
      await runIntent(props.api, intentIds.value, frameOf, ctl.signal, { includeReqBody: includeReq.value })
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
        frameOf,
        ctl.signal,
      )
    }
    // 令牌校验：仅最新 run 可迁移状态（旧 run 续体读到新 run 的 streaming 也不得误标）
    if (seq === runSeq && phase.value === 'streaming') phase.value = 'done'
  } catch (e) {
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
      log('meta', `送审 ${m.sent}/${m.total} 条 · 预算 ${m.budget.flows} 流 / ${m.budget.kb}KB` + (m.truncated ? ' · 已截断' : ''))
      break
    }
    case 'delta':
      if (ev.data.text) {
        if (!sawDelta) {
          sawDelta = true
          log('delta', '模型开始输出')
        }
        pushDelta(ev.data.text)
      }
      break
    case 'intent':
      intentDone.value++
      upsert(ev.data) // P1 审计修复：缓存写入挪进 runSeq+phase 双守卫内（旧任务迟到帧不再污染共享缓存）
      log('intent', `#${ev.data.seq} ${ev.data.intent}` + (ev.data.needsBody ? '（需正文）' : ''))
      break
    case 'match':
      upsertMatch(ev.data)
      log('match', `#${ev.data.rank} ${ev.data.method} ${ev.data.url} · ${confLabel(ev.data.confidence)}`)
      break
    case 'error':
      phase.value = 'error'
      errMsg.value = ev.data.message
      log('error', ev.data.message)
      break
    case 'done':
      flushRender()
      phase.value = 'done'
      log('done', `共 ${mdBuf.length} 字` + (runStartTs ? ` · 耗时 ${((Date.now() - runStartTs) / 1000).toFixed(1)}s` : ''))
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
  flushRender() // P2 审计修复：清残余 50ms 合帧定时器并落盘最后一批 delta（防悬空回调）
  log('stop', '已手动停止')
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
  // 日志按 run 独立：本记录档为上一轮供回看（G3 审计建议），本轮从零开始
  if (logs.value.length) {
    prevLogs.value = logs.value
    prevOpen.value = false
  }
  logs.value = []
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
  // 日志抽层展开时先收抽层，再按一次 Esc 才关面板（Z1 审计修复：交互层级）
  if (logOpen.value) {
    if (logDragging.value) onLogDragEnd() // 拖拽中收抽层先终止拖拽态（摘监听/落盘，I3 审计修复）
    logOpen.value = false
    return
  }
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

// 卸载清理（M2 审计修复）：主窗切走复盘视图（v-if 卸载）时停掉在跑流（隐私外发中止）、
// 摘除 Esc 监听（该监听仅 show→false 时移除，跨挂载会泄漏）
onBeforeUnmount(() => {
  stop()
  flushRender() // P2 审计修复：非 streaming 卸载（如 error 态）时合帧定时器可能仍挂，兜底清掉
  window.removeEventListener('keydown', onEsc)
  // 高度拖拽中卸载：摘除 window 级监听并还原 body 光标
  if (logDragging.value) onLogDragEnd()
})
</script>
