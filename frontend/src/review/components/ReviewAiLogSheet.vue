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
      <!-- 清空全部记录（阶段三持久化后记录跨页面存活，需提供手动清空出口）；
           流式输出中禁用（审计修复：清空会换掉 convTurns 数组，而父侧 in-flight curTurn/mdBuf
           仍写旧对象——后续 delta 不可见、closeTurn 写回不落盘，UI 卡「等待模型输出…」） -->
      <n-popconfirm placement="top-end" positive-text="清空" negative-text="取消" @positive-click="clearSession">
        <template #trigger>
          <button
            class="ai-close ai-log-clear"
            :title="phase === 'streaming' ? '停止分析后才能清空' : '清空对话与日志'"
            :disabled="phase === 'streaming'"
          >
            <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M3 4.5h10" />
              <path d="M6.5 4.5v-2h3v2" />
              <path d="M4.5 4.5l.8 9h5.4l.8-9" />
            </svg>
          </button>
        </template>
        清空全部对话轮次与调用日志？该操作不可恢复。
      </n-popconfirm>
      <button class="ai-close" title="收起日志" @click="emit('update:open', false)">
        <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor">
          <path d="M8 6.59L12.95 1.64l1.41 1.41L9.41 8l4.95 4.95-1.41 1.41L8 9.41l-4.95 4.95-1.41-1.41L6.59 8 1.64 3.05l1.41-1.41L8 6.59z" />
        </svg>
      </button>
    </header>
    <div ref="logBodyEl" class="ai-log-body">
      <!-- 对话 tab：对话轮次（每条流一对「消息发送→模型返回」气泡；单条轮询逐条成对），
           轮次头展示本轮提问快照（开轮时由父侧存入 t.question） -->
      <template v-if="logTab === 'conv'">
        <div v-if="convEmpty" class="ai-hint ai-log-empty">
          {{ phase === 'streaming' ? '等待模型输出…' : '暂无对话内容 · 发起一次分析后这里实时展示模型原始输出' }}
        </div>
        <template v-else>
          <!-- 轮次仅追加不重排（切模式/新 run/恢复均保留历史），index 作 key 安全 -->
          <div v-for="(t, ti) in convTurns" :key="ti" class="ai-turn">
            <!-- 本轮提问快照：单条轮询逐条同题，仅在与上一轮不同（或首轮）时显示，避免每对气泡重复 -->
            <div v-if="t.question && (ti === 0 || convTurns[ti - 1].question !== t.question)" class="ai-turn-q">
              <span class="ai-turn-q-label">提问</span>
              <span class="ai-turn-q-text" :title="t.question">{{ t.question }}</span>
            </div>
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
                <pre class="ai-log-conv ai-conv-scroll">{{ t.system }}</pre>
              </div>
              <button v-if="t.user" class="ai-conv-toggle" :class="{ open: t.usrOpen }" @click="t.usrOpen = !t.usrOpen">
                <svg class="chev" viewBox="0 0 16 16" width="10" height="10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M6 4.5l4 4-4 4" />
                </svg>
                <span>User 提示词</span>
                <span class="ai-conv-count">{{ t.user.length }} 字</span>
              </button>
              <div class="ai-conv-fold" :class="{ open: t.usrOpen }">
                <pre class="ai-log-conv ai-conv-scroll">{{ t.user }}</pre>
              </div>
            </div>
            <!-- 模型返回（左）：思考过程与正文输出均为折叠块（默认收起，展开限高自滚动） -->
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
                  <pre class="ai-log-conv ai-conv-think ai-conv-scroll">{{ t.reason }}</pre>
                </div>
              </template>
              <!-- 正文输出折叠：默认收起（父侧首个正文增量自动展开直播轮），展开后限高自滚动；
                   Markdown 渲染（表格/代码块/mermaid 图），ai-md.ts 净化出口 -->
              <template v-if="t.text">
                <button
                  class="ai-conv-toggle"
                  :class="{ open: t.txtOpen, live: ti === convTurns.length - 1 && phase === 'streaming' && t.txtOpen }"
                  @click="toggleTxt(t)"
                >
                  <svg class="chev" viewBox="0 0 16 16" width="10" height="10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M6 4.5l4 4-4 4" />
                  </svg>
                  <span>正文输出</span>
                  <span class="ai-conv-count">{{ t.text.length }} 字</span>
                </button>
                <div class="ai-conv-fold" :class="{ open: t.txtOpen }">
                  <div class="ai-md ai-conv-md ai-conv-scroll" v-html="mdHtml(t)"></div>
                </div>
              </template>
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

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { NPopconfirm, NTab, NTabs } from 'naive-ui'
import DOMPurify from 'dompurify'
import { renderMarkdown } from '../ai-md'
import { clearSession } from '../useAiSession'
import type { ConvTurn, LogEntry, LogKind } from '../useAiSession'

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
}>()

const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
  (e: 'update:prevOpen', v: boolean): void
}>()

const logTab = ref<'conv' | 'log'>('log')

// ===== 展示派生 =====
// 空态判定：尚无任何对话轮次
const convEmpty = computed(() => !props.convTurns.length)

function toggleTurn(t: ConvTurn): void {
  t.open = !t.open
  t.touched = true
}

// 正文折叠开关：与思考过程同款（手动置 touched 退出父侧自动开合）
function toggleTxt(t: ConvTurn): void {
  t.txtOpen = !t.txtOpen
  t.txtTouched = true
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

// ===== 模型正文 Markdown 渲染 + mermaid 水合 =====
// 正文经 ai-md.ts 净化出口渲染（表格/代码块/mermaid 图）；结果按轮次对象 WeakMap 缓存——
// 流式仅末轮 text 变化，历史轮次命中缓存零重解析，resetRun 后旧对象随 GC 自动清除。
// mermaid 代码块动态 import（独立分包，对话中无图不加载）：渲染成功替换原代码块，
// 解析失败（流式中途图源不完整/语法错）保留代码块降级展示——v-html 重写 DOM 后标记
// 自然失效，下一帧或 phase 离开 streaming 时自动重试。
const mdCache = new WeakMap<ConvTurn, { text: string; html: string }>()
function mdHtml(t: ConvTurn): string {
  const c = mdCache.get(t)
  if (c && c.text === t.text) return c.html
  const html = renderMarkdown(t.text)
  mdCache.set(t, { text: t.text, html })
  return html
}

type Mermaid = (typeof import('mermaid'))['default']
let mermaidLoading: Promise<Mermaid> | null = null
let mmdSeq = 0
function ensureMermaid(): Promise<Mermaid> {
  mermaidLoading ??= import('mermaid')
    .then((m) => {
      m.default.initialize({ startOnLoad: false, theme: 'dark', securityLevel: 'strict' })
      return m.default
    })
    .catch((e) => {
      mermaidLoading = null // 加载失败允许下次重试；失败期间 mermaid 块保持代码块降级
      throw e
    })
  return mermaidLoading
}

let hydrating = false
let mmdPending = false // hydrating 期间又有新触发：收尾后补跑一次（审计修复：原实现静默丢弃，done 后末轮完整图源停留代码块态）
async function hydrateMermaid(): Promise<void> {
  const root = logBodyEl.value
  if (root && hydrating) {
    mmdPending = true
    return
  }
  if (!root) return
  const pairs = [...root.querySelectorAll('pre > code.language-mermaid')]
    .map((code) => ({ code, pre: code.parentElement }))
    .filter((p): p is { code: Element; pre: HTMLElement } => p.pre !== null)
  if (!pairs.length) return
  hydrating = true
  try {
    // 先挂渲染中角标（含 mermaid 库首次动态加载的等待期），成功随节点替换消失
    for (const { pre } of pairs) pre.classList.add('mmd-loading')
    const mm = await ensureMermaid()
    for (const { code, pre } of pairs) {
      const src = code.textContent ?? ''
      // parse 门禁：语法完整才进入渲染。流式中图源不完整解析失败→静默保留代码块
      // （loading 角标已随上文挂载，此处需摘除），避免每帧闪烁；语法错误终态同为代码块
      const ok = await mm.parse(src, { suppressErrors: true }).catch(() => false)
      if (!ok) {
        pre.classList.remove('mmd-loading')
        continue
      }
      const id = `mmd-${++mmdSeq}`
      try {
        const { svg } = await mm.render(id, src)
        const holder = document.createElement('div')
        holder.className = 'ai-md-mermaid'
        // mermaid 产物过一遍净化（strict 模式已禁交互，此处兜底 SVG 注入面）
        // foreignObject 是 HTML 集成点：缺 HTML_INTEGRATION_POINTS 时即使放行标签，
        // 其内部 HTML 也会被整体清空（图只剩框线无文字，浏览器实测踩坑）
        holder.innerHTML = DOMPurify.sanitize(svg, {
          USE_PROFILES: { svg: true, html: true },
          ADD_TAGS: ['foreignObject'],
          HTML_INTEGRATION_POINTS: { foreignobject: true },
        })
        pre.replaceWith(holder)
      } catch {
        document.getElementById(id)?.remove() // render 失败清理 mermaid 残留元素，保留原代码块
        pre.classList.remove('mmd-loading') // 恢复代码块观感，下轮触发可重试
      }
    }
  } catch {
    // mermaid 库加载失败：摘除全部角标，保持代码块降级（下次触发重试加载）
    for (const { pre } of pairs) pre.classList.remove('mmd-loading')
  } finally {
    hydrating = false
    if (mmdPending) {
      mmdPending = false
      void hydrateMermaid()
    }
  }
}

// ===== 滚动跟随 =====
// 直播轮展开体跟随：展开体限高自滚动后，流式追加发生在块内而非外层——末轮各滚动体
// 近底部（40px 阈值，未上翻回看）才钉住底缘；force 用于开层/切 tab 的无条件跟随（对齐外层滚底语义）
function pinLiveFolds(force: boolean): void {
  if (props.phase !== 'streaming') return
  const root = logBodyEl.value
  if (!root) return
  const turns = root.querySelectorAll<HTMLElement>('.ai-turn')
  const live = turns[turns.length - 1]
  if (!live) return
  for (const sc of live.querySelectorAll<HTMLElement>('.ai-conv-scroll')) {
    if (force || sc.scrollHeight - sc.scrollTop - sc.clientHeight < 40) sc.scrollTop = sc.scrollHeight
  }
}
// 开合/切 tab：导航意图，无条件滚到底展示最新内容；顺带水合 mermaid 块
watch([() => props.open, logTab], () => {
  if (!props.open) return
  nextTick(() => {
    void hydrateMermaid()
    const el = logBodyEl.value
    if (el && props.open) el.scrollTop = el.scrollHeight
    pinLiveFolds(true)
  })
})
// 流式追加（日志帧/对话更新）：仅当视口已近底部（40px 阈值）才跟随滚动，
// 尊重用户上翻回看——B1 审计修复（原先无条件滚底会每 50ms 把用户拉回底部）。
// 依赖取末轮 text/reason 长度（引擎仅写末轮）而非全量聚合——审计修复：原 fullText
// 每 50ms 合帧全量 join（上限 3M 字符）重算，MB 级字符串分配徒增 GC 压力
watch(
  [
    () => props.logs[props.logs.length - 1]?.id ?? 0,
    () => props.convTurns[props.convTurns.length - 1]?.text.length ?? 0,
    () => props.convTurns.length,
    () => props.convTurns[props.convTurns.length - 1]?.reason.length ?? 0,
  ],
  () => {
    if (!props.open) return
    nextTick(() => {
      void hydrateMermaid()
      const el = logBodyEl.value
      if (!el || !props.open) return
      if (el.scrollHeight - el.scrollTop - el.clientHeight < 40) el.scrollTop = el.scrollHeight
      pinLiveFolds(false)
    })
  },
)
// phase 收尾（streaming→done/stopped/error）：末帧图源此时才完整，补一次水合
watch(
  () => props.phase,
  () => {
    nextTick(() => {
      void hydrateMermaid()
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
