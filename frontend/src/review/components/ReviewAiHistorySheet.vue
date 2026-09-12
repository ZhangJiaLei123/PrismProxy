<template>
  <!-- 历史记录抽层（解读/定位/流程）：底部滑入，与日志抽层同族（复用 .ai-logsheet 底座样式，
       开合/互斥由父 ReviewAiPanel 持有）。列表 = 三模块已定稿（正文非空、非流式进行中）的对话轮次，最新在上；
       面板主区只展示各模块最新输出，此前输出从这里点开回看（mermaid 流程图展开时水合渲染）。
       勾选多条（可跨模块：切筛选 tab 不清勾选）→「导出所选」合并下载为同一个 md 文件 -->
  <section class="ai-logsheet ai-histsheet" :class="{ open }" aria-label="AI 历史记录">
    <header class="ai-log-head">
      <n-tabs v-model:value="histTab" type="segment" size="small" class="ai-log-tabs">
        <n-tab name="all">全部</n-tab>
        <n-tab name="explain">解读</n-tab>
        <n-tab name="locate">定位</n-tab>
        <n-tab name="flowmap">流程</n-tab>
      </n-tabs>
      <!-- 全选（针对当前筛选结果；跨模块多选流程：切 tab 逐批勾选，勾选集不随筛选重置） -->
      <button class="ai-mini-btn" :disabled="!filtered.length" @click="toggleAll">
        {{ allSelected ? '全不选' : '全选' }}
      </button>
      <button
        class="ai-mini-btn is-primary"
        :disabled="!sel.size"
        title="勾选的记录（可跨模块）合并导出为一个 Markdown 文件"
        @click="exportSelected"
      >
        导出所选{{ sel.size ? ` ${sel.size}` : '' }}
      </button>
      <button class="ai-close" title="收起历史记录" @click="emit('update:open', false)">
        <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor">
          <path d="M8 6.59L12.95 1.64l1.41 1.41L9.41 8l4.95 4.95-1.41 1.41L8 9.41l-4.95 4.95-1.41-1.41L6.59 8 1.64 3.05l1.41-1.41L8 6.59z" />
        </svg>
      </button>
    </header>
    <div ref="bodyEl" class="ai-log-body">
      <div v-if="!filtered.length" class="ai-hint ai-log-empty">
        暂无历史记录 · 解读/定位/流程的分析结果自动留档于此，面板内仅展示各模块最新一次输出
      </div>
      <template v-else>
        <!-- 稳定 id 作 v-for 键：新定稿轮次插顶/列表剪枝时 keyed diff 复用既有 DOM，
             已展开条目的 mermaid 水合产物（SVG）不因 index 错位重建而丢 -->
        <div v-for="t in filtered" :key="t.id" class="ai-hist-item" :class="{ 'is-open': exp.has(t.id) }">
          <div class="ai-hist-row" @click="toggleExpand(t)">
            <label class="ai-hist-check" title="选中用于导出" @click.stop>
              <input type="checkbox" :checked="sel.has(t.id)" @change="toggleSel(t)" />
              <i aria-hidden="true"></i>
            </label>
            <span class="ai-hist-badge" :class="'m-' + t.mode">{{ MODE_LABEL[t.mode ?? ''] ?? t.mode }}</span>
            <span class="ai-hist-q" :title="t.question">{{ t.question }}</span>
            <span v-if="t.ts" class="ai-hist-t">{{ fmtT(t.ts) }}</span>
            <svg
              class="ai-hist-chev"
              viewBox="0 0 16 16"
              width="10"
              height="10"
              fill="none"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linecap="round"
              stroke-linejoin="round"
            >
              <path d="M6 4.5l4 4-4 4" />
            </svg>
          </div>
          <!-- 收起态预览：正文首个非空行（单行省略），展开后让位全文 -->
          <div v-if="!exp.has(t.id)" class="ai-hist-prev">{{ preview(t) }}</div>
          <div class="ai-conv-fold" :class="{ open: exp.has(t.id) }">
            <div class="ai-conv-scroll">
              <div class="ai-md ai-conv-md" v-html="mdHtml(t)"></div>
            </div>
          </div>
        </div>
      </template>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { NTab, NTabs } from 'naive-ui'
import { renderMarkdown } from '../ai-md'
import { hydrateMermaidIn } from '../aiMermaid'
import { AI_EXPORT_MODES, MODE_LABEL, downloadMd } from '../aiExport'
import type { ConvTurn } from '../useAiSession'

const props = defineProps<{
  /** 抽层开合（父持有：底部历史按钮与 Esc 逐层收合共用，与日志抽层互斥） */
  open: boolean
  /** 对话轮次（模块级 store 注入，本组件只读展示 + 本地勾选/展开态） */
  convTurns: ConvTurn[]
  /** 流式进行中轮次（beginTurn→closeTurn 全程持有）：未定稿不入册，防半截输出入册/被勾选导出 */
  liveTurn?: ConvTurn | null
}>()

const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
}>()

type HistTab = 'all' | 'explain' | 'locate' | 'flowmap'
const histTab = ref<HistTab>('all')

// ===== 记录派生 =====
// 历史记录 = 三模块已定稿（正文非空）的轮次；无输出的失败轮/开轮即停不入册。
// 流式进行中轮次（liveTurn）排除：首 delta 后 text 即非空，不排除会以半截输出入册被勾选导出
//（续写轮次 closeTurn 前全程持有 liveTurn，同样覆盖）。顺序沿用 convTurns 时间序（旧→新），展示倒序（最新在上）
const records = computed(() =>
  props.convTurns.filter(
    (t) => t !== props.liveTurn && (AI_EXPORT_MODES as readonly string[]).includes(t.mode ?? '') && !!t.text,
  ),
)
const filtered = computed(() =>
  histTab.value === 'all' ? [...records.value].reverse() : records.value.filter((t) => t.mode === histTab.value).reverse(),
)

// ===== 勾选与展开（本地态：Set 以轮次稳定 id 为键——对象引用会在 clearSession/prune 后成孤儿，
// id 键配合下方记录集 watcher 同步剔除，切筛选/翻新列表不丢，跨模块多选保留）=====
const sel = reactive(new Set<number>())
const exp = reactive(new Set<number>())

function toggleSel(t: ConvTurn): void {
  if (sel.has(t.id)) sel.delete(t.id)
  else sel.add(t.id)
}
const allSelected = computed(() => filtered.value.length > 0 && filtered.value.every((t) => sel.has(t.id)))
function toggleAll(): void {
  if (allSelected.value) {
    for (const t of filtered.value) sel.delete(t.id)
  } else {
    for (const t of filtered.value) sel.add(t.id)
  }
}
function exportSelected(): void {
  if (!sel.size) return
  // 按 id 解析回现存记录：清空/剪枝竞态窗口内的孤儿 id 自然跳过，绝不导出已不存在的数据
  const byId = new Map(records.value.map((t) => [t.id, t] as const))
  const list = [...sel].flatMap((id) => {
    const t = byId.get(id)
    return t ? [{ mode: t.mode ?? '', question: t.question, text: t.text, ts: t.ts ?? 0 }] : []
  })
  if (!list.length) return
  downloadMd(list, `导出${list.length}条`)
}

function toggleExpand(t: ConvTurn): void {
  if (exp.has(t.id)) exp.delete(t.id)
  else {
    exp.add(t.id)
    // 展开才挂载 md（v-html），图源此时才存在：下一帧水合 mermaid（流程图回看）
    nextTick(() => void hydrateMermaidIn(bodyEl.value))
  }
}

// ===== 展示辅助 =====
const bodyEl = ref<HTMLElement | null>(null)
function fmtT(t: number): string {
  const d = new Date(t)
  const p = (n: number): string => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function preview(t: ConvTurn): string {
  const line = t.text.split('\n').find((l) => l.trim()) ?? ''
  return line.trim().slice(0, 160)
}

// 正文 Markdown 渲染（ai-md.ts 净化出口）+ 轮次对象 WeakMap 缓存：历史正文不可变，零重解析
const mdCache = new WeakMap<ConvTurn, { text: string; html: string }>()
function mdHtml(t: ConvTurn): string {
  const c = mdCache.get(t)
  if (c && c.text === t.text) return c.html
  const html = renderMarkdown(t.text)
  mdCache.set(t, { text: t.text, html })
  return html
}

// 开层定位顶部（最新在上）并水合已展开条目的 mermaid 块
watch(
  () => props.open,
  (o) => {
    if (!o) return
    nextTick(() => {
      const el = bodyEl.value
      if (el) el.scrollTop = 0
      void hydrateMermaidIn(el)
    })
  },
)

// 记录集变动（新定稿轮次入册 / clearSession 清空 / MAX_TURNS 剪枝 / hydrate 恢复）：
// ① 勾选/展开集剔除孤儿 id——列表里已不存在的键不得残留（清空后「导出所选 N」不亮、不导出已清数据）；
// ② 面板展开时补跑 mermaid 水合——keyed 复用已保留已渲染图，此处兜底新入册/恢复重挂载的展开条目。
// 以 records.length 为触发源：length 不变的正文流式增帧不触发（未定稿轮已不入册），零高频空转
watch(
  () => records.value.length,
  () => {
    if (sel.size || exp.size) {
      const alive = new Set(records.value.map((t) => t.id))
      for (const k of sel) if (!alive.has(k)) sel.delete(k)
      for (const k of exp) if (!alive.has(k)) exp.delete(k)
    }
    if (props.open) nextTick(() => void hydrateMermaidIn(bodyEl.value))
  },
)

// 切筛选 tab：被隐藏条目卸载、重现条目重挂载（v-html 重渲染回代码块），补跑水合恢复已展开的流程图
watch(histTab, () => {
  if (props.open) nextTick(() => void hydrateMermaidIn(bodyEl.value))
})
</script>
