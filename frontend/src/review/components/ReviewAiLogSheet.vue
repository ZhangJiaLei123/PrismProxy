<template>
  <!-- 调用日志抽层（设计 §7）：对话内容（模型原始输出实时流）+ 调用日志（帧级事件），自底部滑入；
       高度可拖拽调整（顶部把手）并经 localStorage 缓存。
       由 ReviewAiPanel 挂载：open 开合由父持有（Esc 逐层收合 / 底部日志按钮共用），
       流式引擎写入的 convTurns/logs 以 props 注入——本组件只负责展示与交互（折叠/拖高/滚随） -->
  <section
    ref="logsheetEl"
    class="ai-logsheet"
    :class="{ open, dragging: logDragging }"
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
      <button class="ai-close" title="收起日志" @click="emit('update:open', false)">
        <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor">
          <path d="M8 6.59L12.95 1.64l1.41 1.41L9.41 8l4.95 4.95-1.41 1.41L8 9.41l-4.95 4.95-1.41-1.41L6.59 8 1.64 3.05l1.41-1.41L8 6.59z" />
        </svg>
      </button>
    </header>
    <div ref="logBodyEl" class="ai-log-body">
      <!-- 对话 tab：提问 → 对话轮次（每条流一对「消息发送→模型返回」气泡；单条轮询逐条成对） -->
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
          <!-- 轮次仅追加不重排（resetRun 整体清空），index 作 key 安全 -->
          <div v-for="(t, ti) in convTurns" :key="ti" class="ai-turn">
            <!-- 消息发送（右）：head 显示字数 + token（done 帧真实 usage 精确，缺失估算带 ≈） -->
            <div class="ai-bubble ai-bubble-send">
              <div class="ai-bubble-head">
                <span class="ai-bubble-role">消息发送</span>
                <span class="ai-conv-count">{{ t.system.length + t.user.length }} 字 · {{ tokText(t.usage?.promptTokens, t.system + t.user) }}</span>
              </div>
              <button v-if="t.system" class="ai-conv-toggle" :class="{ open: t.sysOpen }" @click="t.sysOpen = !t.sysOpen">
                <svg class="chev" viewBox="0 0 16 16" width="10" height="10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M6 4.5l4 4-4 4" />
                </svg>
                <span>System 提示词</span>
                <span class="ai-conv-count">{{ t.system.length }} 字</span>
              </button>
              <div class="ai-conv-fold" :class="{ open: t.sysOpen }">
                <pre class="ai-log-conv">{{ t.system }}</pre>
              </div>
              <button v-if="t.user" class="ai-conv-toggle" :class="{ open: t.usrOpen }" @click="t.usrOpen = !t.usrOpen">
                <svg class="chev" viewBox="0 0 16 16" width="10" height="10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M6 4.5l4 4-4 4" />
                </svg>
                <span>User 提示词</span>
                <span class="ai-conv-count">{{ t.user.length }} 字</span>
              </button>
              <div class="ai-conv-fold" :class="{ open: t.usrOpen }">
                <pre class="ai-log-conv">{{ t.user }}</pre>
              </div>
            </div>
            <!-- 模型返回（左）：思考过程（自动开合）+ 正文输出 -->
            <div class="ai-bubble ai-bubble-recv">
              <div class="ai-bubble-head">
                <span class="ai-bubble-role">模型返回</span>
                <span class="ai-conv-count">{{ t.reason.length + t.text.length }} 字 · {{ tokText(t.usage?.completionTokens, t.reason + t.text) }}</span>
              </div>
              <template v-if="t.reason">
                <button
                  class="ai-conv-toggle"
                  :class="{ open: t.open, live: ti === convTurns.length - 1 && phase === 'streaming' && t.open }"
                  @click="toggleTurn(t)"
                >
                  <svg class="chev" viewBox="0 0 16 16" width="10" height="10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M6 4.5l4 4-4 4" />
                  </svg>
                  <span>思考过程</span>
                  <span class="ai-conv-count">{{ t.reason.length }} 字</span>
                </button>
                <div class="ai-conv-fold" :class="{ open: t.open }">
                  <pre class="ai-log-conv ai-conv-think">{{ t.reason }}</pre>
                </div>
              </template>
              <pre v-if="t.text" class="ai-log-conv">{{ t.text }}</pre>
              <div v-else-if="ti === convTurns.length - 1 && phase === 'streaming'" class="ai-hint">模型输出中…</div>
              <div v-else-if="!t.reason" class="ai-hint">（无输出）</div>
              <div v-if="t.finishReason === 'length'" class="ai-hint ai-conv-trunc">输出被截断（模型上下文/输出预算不足）</div>
            </div>
          </div>
        </template>
      </template>
      <template v-else>
        <div v-if="!logs.length && !prevLogs.length" class="ai-hint ai-log-empty">暂无日志 · 发起分析后逐帧记录调用过程</div>
        <!-- 上一轮归档（G3 审计建议）：默认收起，展开弱化展示最近一轮帧序列 -->
        <button v-if="prevLogs.length" class="ai-log-prev" @click="emit('update:prevOpen', !prevOpen)">
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
</template>

<!-- 非-setup 脚本块：导出父组件流式引擎直接复用的类型（对话轮次 / 帧级日志条目） -->
<script lang="ts">
import type { AiUsage } from '../api'

export interface ConvTurn {
  system: string // meta 帧 system（实际送审 System 提示词）
  user: string // meta 帧 user（实际送审 User 提示词）
  reason: string // 推理增量（delta 帧 reason 字段；普通模型/demo 无此流）
  text: string // 正文增量
  open: boolean // 思考过程折叠开合
  sysOpen: boolean // System 提示词折叠开合
  usrOpen: boolean // User 提示词折叠开合
  touched: boolean // 思考区被用户手动开合过：本轮流式自动开合逻辑退出
  finishReason: string // done 帧 finishReason（length=输出截断）
  usage?: AiUsage // done 帧真实 token 统计（服务商不支持时缺省，前端按字数估算兜底）
}
export type LogKind = 'start' | 'meta' | 'delta' | 'intent' | 'match' | 'error' | 'done' | 'stop'
export interface LogEntry {
  id: number
  t: number
  kind: LogKind
  msg: string
}
</script>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { NTab, NTabs } from 'naive-ui'

const props = defineProps<{
  /** 抽层开合（父持有：底部日志按钮与 Esc 逐层收合共用） */
  open: boolean
  /** 分析状态机（驱动空态文案 / 思考区 live 高亮 / 「模型输出中…」提示） */
  phase: 'idle' | 'streaming' | 'done' | 'stopped' | 'error'
  /** 对话轮次（父流式引擎实时写入，本组件只读展示 + 折叠交互） */
  convTurns: ConvTurn[]
  /** 本轮帧级日志（父侧 >500 自截断） */
  logs: LogEntry[]
  /** 上一轮归档日志 */
  prevLogs: LogEntry[]
  /** 上一轮归档展开态（父持有：新 run 开跑归档时强制收起） */
  prevOpen: boolean
  /** 发起分析时的提问（locate/flowmap 自然语言；explain/intent 为空 → 回显 scopeText） */
  question: string
  /** 范围说明文案 */
  scopeText: string
}>()

const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
  (e: 'update:prevOpen', v: boolean): void
}>()

const logTab = ref<'conv' | 'log'>('log')

// ===== 展示派生 =====
const hasQuestion = computed(() => !!props.question.trim())
const qAsked = computed(() => (hasQuestion.value ? props.question.trim() : props.scopeText))
// 空态判定：尚无任何对话轮次
const convEmpty = computed(() => !props.convTurns.length)
// 全量正文聚合（滚随监听依赖；Markdown 展示视图的 fullText 由父持有，不在此重复）
const fullText = computed(() => props.convTurns.map((t) => t.text).join('\n\n'))

function toggleTurn(t: ConvTurn): void {
  t.open = !t.open
  t.touched = true
}

// token 展示：done 帧真实 usage（include_usage 末帧）精确展示；缺失（服务商不支持/demo）
// 时按字数估算并带 ≈ 前缀（CJK ≈0.7 tok/字、其他 ≈0.25 tok/字符，向上取整）
function estTokens(s: string): number {
  let cjk = 0
  for (const ch of s) if ((ch.codePointAt(0) ?? 0) >= 0x2e80) cjk++
  return Math.ceil(cjk * 0.7 + (s.length - cjk) * 0.25)
}
function tokText(tok: number | undefined, s: string): string {
  const real = tok != null && tok > 0
  return (real ? '' : '≈') + (real ? tok : estTokens(s)).toLocaleString('en-US') + ' tok'
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
function fmtT(t: number): string {
  const d = new Date(t)
  const p = (n: number): string => String(n).padStart(2, '0')
  return p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds())
}

// ===== 滚动跟随 =====
// 开合/切 tab：导航意图，无条件滚到底展示最新内容
watch([() => props.open, logTab], () => {
  if (!props.open) return
  nextTick(() => {
    const el = logBodyEl.value
    if (el && props.open) el.scrollTop = el.scrollHeight
  })
})
// 流式追加（日志帧/对话更新）：仅当视口已近底部（40px 阈值）才跟随滚动，
// 尊重用户上翻回看——B1 审计修复（原先无条件滚底会每 50ms 把用户拉回底部）
watch(
  [
    () => props.logs[props.logs.length - 1]?.id ?? 0,
    fullText,
    () => props.convTurns.length,
    () => props.convTurns[props.convTurns.length - 1]?.reason.length ?? 0,
  ],
  () => {
    if (!props.open) return
    nextTick(() => {
      const el = logBodyEl.value
      if (!el || !props.open) return
      if (el.scrollHeight - el.scrollTop - el.clientHeight < 40) el.scrollTop = el.scrollHeight
    })
  },
)

// ===== 抽层高度拖拽 + 缓存 =====
// 顶部把手上下拖动调高，pointerup 落盘 localStorage；双击恢复 CSS 默认。
// 缓存值以内联 height: min(px, calc(100% - 保留)) 生效——窗口变小时 CSS 就近钳制不溢出抽屉
const LOG_H_KEY = 'prismproxy:review-ai-logsheet-h-v1'
const LOG_H_MIN = 200 // 与 .ai-logsheet min-height 一致
const LOG_H_RESERVE_EXTRA = 120 // 正文可视保留；上限 = 抽屉高 - footer 高(--ai-foot-h) - 120（I2 审计修复：footer 高单一事实源是 CSS 变量）
const logsheetEl = ref<HTMLElement | null>(null)
const logBodyEl = ref<HTMLElement | null>(null)
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
  logDragStartH = el.clientHeight // clientWidth/Height 不含 1px border，= style 值零漂移（G-1 审计修复，rect 起点会逐会话 +1px）
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
    if (logH.value >= LOG_H_MIN) localStorage.setItem(LOG_H_KEY, String(logH.value)) // 未达下限（0 哨兵）不落盘，防纯点击残留 "0"（G-3 审计修复）
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

// ===== 供父组件调用的命令（Esc 逐层收合 / 流式中优先展示对话）=====
// 流式中打开抽层：父组件强制切到对话 tab
function openConv(): void {
  logTab.value = 'conv'
}
// 终止高度拖拽（收合前调用）：未拖拽时 no-op（I3 审计修复语义）
function endDrag(): void {
  if (logDragging.value) onLogDragEnd()
}
defineExpose({ openConv, endDrag })

// 高度拖拽中卸载：摘除 window 级监听并还原 body 光标
onBeforeUnmount(() => {
  if (logDragging.value) onLogDragEnd()
})
</script>
