<template>
  <div class="flow-list">
    <div class="list-head row" :style="gridStyle" @contextmenu.prevent="onHeaderMenu($event)">
      <span class="c-state"></span>
      <span
        v-for="col in visColumns"
        :key="col.key"
        :class="[col.cls, 'th', { active: sortKey === col.key }]"
      >
        {{ col.label }}
        <button
          class="sort-btn"
          :class="{ on: sortKey === col.key }"
          :title="sortKey === col.key ? (sortDir === 'asc' ? '升序（点击切换降序）' : '降序（点击切换升序）') : '点击排序'"
          @click.stop="toggleSort(col.key)"
        >{{ sortKey === col.key ? (sortDir === 'asc' ? '▲' : '▼') : '↕' }}</button>
        <i
          class="col-resizer"
          :class="{ active: resizingWi === col.wi }"
          title="拖动调整列宽"
          @pointerdown.prevent.stop="startResize($event, col.wi)"
        ></i>
      </span>
    </div>
    <div v-bind="containerProps" class="list-body">
      <div v-bind="wrapperProps">
        <div
          v-for="{ data: f } in list"
          :key="f.ID"
          class="row item"
          :style="gridStyle"
          :class="{ selected: f.ID === store.selectedId, error: f.State === 'error', pinned: f.Pinned }"
          @click="store.select(f.ID)"
          @contextmenu.prevent="onContextMenu($event, f)"
        >
          <span class="c-state"><i class="dot" :class="f.State"></i></span>
          <span
            v-for="col in visColumns"
            :key="col.key"
            :class="cellCls(col, f)"
            :title="col.key === 'host' ? f.Host : col.key === 'path' ? f.URL : col.key === 'proc' ? f.ProcessName + ' (' + f.PID + ')' : ''"
            @dblclick.stop="copyCell(cellText(col, f))"
          >
            <template v-if="col.key === 'host'">
              <svg v-if="f.Pinned" class="pin-ic" viewBox="0 0 24 24" title="已置顶"><path fill="currentColor" d="M16 9V4h1c.55 0 1-.45 1-1s-.45-1-1-1H7c-.55 0-1 .45-1 1s.45 1 1 1h1v5c0 1.66-1.34 3-3 3v2h5.97v7l1 1 1-1v-7H19v-2c-1.66 0-3-1.34-3-3z"/></svg><span v-if="f.Historical" class="hist-badge" title="从本地数据库加载的历史流量">历史</span>{{ f.Host }}
            </template>
            <template v-else>{{ cellText(col, f) }}</template>
          </span>
        </div>
        <div v-if="!store.filtered.length" class="empty">
          {{ store.flows.length ? '无匹配流量 —— 调整底部过滤条件' : '暂无流量 —— 将系统代理指向 9090 端口或配置应用代理后开始抓包' }}
        </div>
      </div>
    </div>
    <!-- 右键上下文菜单（manual 定位，渲染在虚拟列表外层，避免行回收导致菜单消失） -->
    <n-dropdown
      placement="bottom-start"
      trigger="manual"
      :x="ctxX"
      :y="ctxY"
      :options="ctxOptions"
      :show="ctxShow"
      :on-clickoutside="() => (ctxShow = false)"
      @select="onCtxSelect"
    />
    <!-- 表头右键菜单：列显示勾选 / 左右调整列顺序 / 重置（仅列布局，首列状态点不可隐藏） -->
    <n-dropdown
      placement="bottom-start"
      trigger="manual"
      :x="headCtxX"
      :y="headCtxY"
      :options="headCtxOptions"
      :show="headCtxShow"
      :on-clickoutside="() => (headCtxShow = false)"
      @select="onHeadCtxSelect"
    />
    <transition name="fade">
      <div v-if="copiedText !== null" class="copy-toast">已复制：{{ copiedText }}</div>
    </transition>
  </div>
</template>

<script setup lang="ts">
import { useVirtualList } from '@vueuse/core'
import { computed, ref } from 'vue'
import { NDropdown, useMessage } from 'naive-ui'
import type { DropdownOption } from 'naive-ui'
import { useFlowsStore } from '../stores/flows'
import { fmtBytes, fmtDuration, fmtTime } from '../lib/format'
import { copyText } from '../lib/clip'
import { AddQuickIgnore, BuildCurl, SetFlowPinned } from '../../wailsjs/go/app/App'
import type { app } from '../../wailsjs/go/models'

const store = useFlowsStore()
const message = useMessage()

type SortKey = 'time' | 'method' | 'status' | 'host' | 'path' | 'dur' | 'size' | 'proc'
type SortDir = 'asc' | 'desc'

const columns: { key: SortKey; label: string; cls: string; wi: number }[] = [
  { key: 'time', label: '时间', cls: 'c-time', wi: 1 },
  { key: 'method', label: '方法', cls: 'c-method', wi: 2 },
  { key: 'status', label: '状态', cls: 'c-status', wi: 3 },
  { key: 'host', label: '域名', cls: 'c-host', wi: 4 },
  { key: 'path', label: '路径', cls: 'c-path', wi: 5 },
  { key: 'dur', label: '耗时', cls: 'c-dur', wi: 6 },
  { key: 'size', label: '大小', cls: 'c-size', wi: 7 },
  { key: 'proc', label: '进程', cls: 'c-proc', wi: 8 },
]

// ---- 列宽拖动 ----
// 列宽（px），与 grid 模板一一对应（首列状态点 22px 固定）；null = 1fr 弹性（路径列默认）
const colWidths = ref<(number | null)[]>([22, 92, 56, 56, 150, null, 66, 64, 100])
const colMins = [22, 52, 44, 40, 60, 60, 48, 48, 60]
const COL_MAX = 400
const resizingWi = ref<number | null>(null)

// 列顺序：元素为 colWidths 的索引（0=固定状态点列，1-8=数据列），仅可见列出现在序中
const DEFAULT_ORDER = [0, 1, 2, 3, 4, 5, 6, 7, 8]
const colOrder = ref<number[]>([...DEFAULT_ORDER])
const visColumns = computed(() => colOrder.value.filter((w) => w >= 1).map((w) => columns[w - 1]))

// ---- 列布局持久化（顺序 + 列宽）到 localStorage，损坏/非法配置静默回退默认 ----
const LAYOUT_KEY = 'prismproxy:flowlist-col-layout-v1'

function saveLayout() {
  try {
    localStorage.setItem(LAYOUT_KEY, JSON.stringify({ order: colOrder.value, widths: colWidths.value }))
  } catch {
    // 存储不可用（隐私模式/配额）静默失败，布局仅本会话生效
  }
}

function loadLayout() {
  try {
    const raw = localStorage.getItem(LAYOUT_KEY)
    if (!raw) return
    const v = JSON.parse(raw) as { order?: unknown; widths?: unknown }
    // 校验 order：数组、首项恒为 0（状态点列）、其余为 1-8 且不重复
    const order = v.order
    if (!Array.isArray(order) || order[0] !== 0 || order.length < 2) return
    const rest = order.slice(1) as unknown[]
    if (!rest.every((n) => Number.isInteger(n) && n >= 1 && n <= 8) || new Set(rest).size !== rest.length) return
    // 校验 widths：长度 9，每项 null 或落在 [colMins, COL_MAX] 的数字
    const widths = v.widths
    if (!Array.isArray(widths) || widths.length !== colMins.length) return
    for (let i = 0; i < widths.length; i++) {
      const w = widths[i]
      if (w !== null && (typeof w !== 'number' || w < colMins[i] || w > COL_MAX)) return
    }
    colOrder.value = order as number[]
    colWidths.value = widths as (number | null)[]
  } catch {
    // JSON 损坏等：保持默认布局
  }
}

loadLayout()

const gridStyle = computed(() => ({
  gridTemplateColumns: colOrder.value.map((w) => (colWidths.value[w] === null ? '1fr' : colWidths.value[w] + 'px')).join(' '),
}))

function startResize(e: PointerEvent, wi: number) {
  const th = (e.target as HTMLElement).closest('.th') as HTMLElement | null
  if (!th) return
  const startX = e.clientX
  const startW = th.getBoundingClientRect().width
  // 弹性列（路径）被拖时固化为当前像素宽
  if (colWidths.value[wi] === null) colWidths.value[wi] = startW
  const min = colMins[wi]
  resizingWi.value = wi
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'

  const onMove = (ev: PointerEvent) => {
    const w = Math.min(COL_MAX, Math.max(min, Math.round(startW + ev.clientX - startX)))
    colWidths.value[wi] = w
  }
  const onUp = () => {
    resizingWi.value = null
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
    window.removeEventListener('pointermove', onMove)
    window.removeEventListener('pointerup', onUp)
    saveLayout() // 拖动过程不写存储，松手时一次性持久化
  }
  window.addEventListener('pointermove', onMove)
  window.addEventListener('pointerup', onUp)
}

// 默认按时间倒序（最新在顶部），与历史行为一致
const sortKey = ref<SortKey>('time')
const sortDir = ref<SortDir>('desc')

function toggleSort(key: SortKey) {
  if (sortKey.value !== key) {
    sortKey.value = key
    sortDir.value = key === 'time' ? 'desc' : 'asc'
  } else {
    sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
  }
}

const valOf: Record<SortKey, (f: app.FlowMeta) => number | string> = {
  time: (f) => f.StartedAt,
  method: (f) => f.Method,
  status: (f) => f.Status,
  host: (f) => f.Host,
  path: (f) => f.Path || f.URL,
  dur: (f) => f.DurationMS,
  size: (f) => f.BytesDown,
  proc: (f) => f.ProcessName,
}

const sortedFlows = computed(() => {
  const get = valOf[sortKey.value]
  const dir = sortDir.value === 'asc' ? 1 : -1
  return [...store.filtered].sort((a, b) => {
    // 置顶流恒在最前（置顶组内仍按选定排序）
    if (!!a.Pinned !== !!b.Pinned) return a.Pinned ? -1 : 1
    const va = get(a)
    const vb = get(b)
    const c = typeof va === 'string' ? va.localeCompare(vb as string) : (va as number) - (vb as number)
    // 同值按时间倒序兜底，保证顺序稳定可预期
    return c !== 0 ? c * dir : b.StartedAt - a.StartedAt
  })
})

// ---- 双击复制 ----
const copiedText = ref<string | null>(null)
let copyTimer: ReturnType<typeof setTimeout> | undefined

async function copyCell(text: string) {
  if (!text) return
  await copyText(text)
  copiedText.value = text.length > 40 ? text.slice(0, 40) + '…' : text
  clearTimeout(copyTimer)
  copyTimer = setTimeout(() => (copiedText.value = null), 1500)
}

// ---- 右键上下文菜单：复制 URL / cURL、置顶、快捷忽略（M5） ----
const ctxShow = ref(false)
const ctxX = ref(0)
const ctxY = ref(0)
const ctxFlow = ref<app.FlowMeta | null>(null)
// 右键落点列：决定是否给出「忽略此域名/进程」入口
const ctxCol = ref<'host' | 'proc' | null>(null)

function onContextMenu(e: MouseEvent, f: app.FlowMeta) {
  const cell = (e.target as HTMLElement).closest('span')
  ctxCol.value = cell?.classList.contains('c-proc') ? 'proc' : cell?.classList.contains('c-host') ? 'host' : null
  ctxFlow.value = f
  ctxX.value = e.clientX
  ctxY.value = e.clientY
  ctxShow.value = true
}

const ctxOptions = computed<DropdownOption[]>(() => {
  const f = ctxFlow.value
  if (!f) return []
  const opts: DropdownOption[] = [
    { label: '复制 URL', key: 'copy-url' },
    { label: '复制 cURL (cmd)', key: 'curl-cmd' },
    { label: '复制 cURL (PowerShell)', key: 'curl-ps' },
    { label: '复制 cURL (bash / Git Bash)', key: 'curl-bash' },
    { type: 'divider', key: 'd1' },
    { label: f.Pinned ? '取消置顶' : '置顶（固定顶部、不被淘汰）', key: 'pin' },
    { type: 'divider', key: 'd2' },
  ]
  // 仅在域名列/进程列右键时给对应忽略入口；无进程名（隧道/未知）则忽略进程禁用
  if (ctxCol.value === 'host' && f.Host) {
    opts.push({ label: `忽略此域名（${f.Host} 及其子域）`, key: 'ignore-host' })
  }
  if (ctxCol.value === 'proc') {
    opts.push({
      label: f.ProcessName ? `忽略此进程（${f.ProcessName}）` : '忽略此进程（进程未知）',
      key: 'ignore-proc',
      disabled: !f.ProcessName,
    })
  }
  if (ctxCol.value) opts.push({ type: 'divider', key: 'd3' })
  // 仅可编辑的请求（有 URL）支持调试重发；盲透传隧道无请求不可重发
  opts.push({
    label: '调试重发（编辑后重新发送）',
    key: 'composer',
    disabled: !f.URL || f.Method === 'CONNECT',
  })
  return opts
})

async function onCtxSelect(key: string) {
  const f = ctxFlow.value
  ctxShow.value = false
  if (!f) return
  try {
    if (key === 'copy-url') {
      await copyText(f.URL)
      copyToast(f.URL)
    } else if (key === 'curl-cmd' || key === 'curl-ps' || key === 'curl-bash') {
      const shell = key === 'curl-cmd' ? 'cmd' : key === 'curl-ps' ? 'powershell' : 'bash'
      const shellName = shell === 'cmd' ? 'cmd' : shell === 'powershell' ? 'PowerShell' : 'bash'
      const r = await BuildCurl(f.ID, shell)
      await copyText(r.command)
      copyToast('cURL 命令（' + shellName + '）')
      if (r.bodyOmitted) message.info('请求体为二进制或超过 64KB，未内联到 cURL（请手动补充）', { duration: 5000, closable: true })
    } else if (key === 'pin') {
      await SetFlowPinned(f.ID, !f.Pinned)
    } else if (key === 'ignore-host' && f.Host) {
      const added = await AddQuickIgnore('host', f.Host)
      if (added) message.success(`已忽略域名 ${f.Host}（含全部子域），后续流量不再显示`, { duration: 4000, closable: true })
      else message.info(`域名 ${f.Host} 已在忽略列表中`, { duration: 3000, closable: true })
    } else if (key === 'ignore-proc' && f.ProcessName) {
      const added = await AddQuickIgnore('process', f.ProcessName)
      if (added) message.success(`已忽略进程 ${f.ProcessName}，该进程后续流量不再显示`, { duration: 4000, closable: true })
      else message.info(`进程 ${f.ProcessName} 已在忽略列表中`, { duration: 3000, closable: true })
    } else if (key === 'composer') {
      store.openComposer(f.ID)
    }
  } catch (e) {
    message.error(String(e), { duration: 6000, closable: true })
  }
}

function copyToast(label: string) {
  copiedText.value = label.length > 40 ? label.slice(0, 40) + '…' : label
  clearTimeout(copyTimer)
  copyTimer = setTimeout(() => (copiedText.value = null), 1500)
}

const { list, containerProps, wrapperProps } = useVirtualList(sortedFlows, {
  itemHeight: 28,
  overscan: 15,
})

function statusClass(f: app.FlowMeta): string {
  if (f.State === 'error') return 's-err'
  const s = f.Status
  if (s >= 500) return 's-5xx'
  if (s >= 400) return 's-4xx'
  if (s >= 300) return 's-3xx'
  if (s >= 200) return 's-2xx'
  return ''
}

// ---- 动态列：单元格展示文本 / 附加 class（隐藏列不渲染；列宽/顺序与表头共用 colOrder） ----
function cellText(col: { key: SortKey }, f: app.FlowMeta): string {
  switch (col.key) {
    case 'time': return fmtTime(f.StartedAt)
    case 'method': return f.Method
    case 'status': return f.Status ? String(f.Status) : '—'
    case 'host': return f.Host
    case 'path': return f.Path || f.URL
    case 'dur': return fmtDuration(f.DurationMS)
    case 'size': return fmtBytes(f.BytesDown)
    case 'proc': return f.ProcessName
  }
}

function cellCls(col: { key: SortKey; cls: string }, f: app.FlowMeta): string {
  const base = col.cls + (col.key === 'host' || col.key === 'path' || col.key === 'proc' ? ' ellipsis' : '')
  if (col.key === 'method') return base + ' m-' + f.Method
  if (col.key === 'status') return base + ' ' + statusClass(f)
  return base
}

// ---- 表头右键菜单：勾选显示列 / 左右调整顺序 / 重置列布局 ----
const headCtxShow = ref(false)
const headCtxX = ref(0)
const headCtxY = ref(0)
// 右键落点的数据列 wi（colWidths 索引 1-8）；null=点在空白/状态列
const headCtxWi = ref<number | null>(null)

function onHeaderMenu(e: MouseEvent) {
  const th = (e.target as HTMLElement).closest('.th') as HTMLElement | null
  // 点在排序按钮/拖宽手柄上也归属所在列（th 的首个 class 即列 cls，如 c-time）；点状态列/空白为 null
  headCtxWi.value = th ? columns.findIndex((c) => c.cls === th.classList[0]) + 1 : null
  headCtxX.value = e.clientX
  headCtxY.value = e.clientY
  headCtxShow.value = true
}

const headCtxOptions = computed<DropdownOption[]>(() => {
  const wi = headCtxWi.value
  const idx = wi === null ? -1 : colOrder.value.indexOf(wi)
  const opts: DropdownOption[] = [
    { label: '左移此列', key: 'col-left', disabled: wi === null || idx <= 1 },
    { label: '右移此列', key: 'col-right', disabled: wi === null || idx < 1 || idx >= colOrder.value.length - 1 },
    { type: 'divider', key: 'hd1' },
    {
      label: '显示/隐藏列',
      key: 'col-vis',
      children: columns.map((c) => ({
        label: (colOrder.value.includes(c.wi) ? '✓ ' : '　') + c.label,
        key: 'vis-' + c.wi,
        // 至少保留一列，最后一个可见列禁止取消勾选
        disabled: colOrder.value.includes(c.wi) && colOrder.value.length <= 2,
      })),
    },
    { label: '重置列布局', key: 'col-reset' },
  ]
  return opts
})

function onHeadCtxSelect(key: string) {
  headCtxShow.value = false
  if (key === 'col-reset') {
    colOrder.value = [...DEFAULT_ORDER]
    colWidths.value = [22, 92, 56, 56, 150, null, 66, 64, 100]
  } else if (key === 'col-left' || key === 'col-right') {
    const order = colOrder.value
    const wi = headCtxWi.value
    if (wi === null) return
    const i = order.indexOf(wi)
    if (i < 1) return
    const j = key === 'col-left' ? i - 1 : i + 1
    if (j < 1 || j >= order.length) return
    // 与相邻可见列交换位置（colOrder[0] 恒为固定状态点列，不参与交换）
    ;[order[i], order[j]] = [order[j], order[i]]
    colOrder.value = [...order]
  } else if (key.startsWith('vis-')) {
    const order = colOrder.value
    const wi = Number(key.slice(4))
    const i = order.indexOf(wi)
    if (i >= 0) {
      if (order.length <= 2) return // 至少保留状态点 + 一个数据列
      order.splice(i, 1)
    } else {
      // 恢复显示：按默认相对顺序插回（排在默认序中前一个仍可见列之后，找不到则置于数据列首位）
      let at = 1
      for (let k = wi - 1; k >= 1; k--) {
        const p = order.indexOf(k)
        if (p >= 0) { at = p + 1; break }
      }
      order.splice(at, 0, wi)
    }
    colOrder.value = [...order]
  } else {
    return
  }
  saveLayout()
}
</script>

<style scoped>
.flow-list { height: 100%; display: flex; flex-direction: column; font-size: 12px; position: relative; }
.row {
  display: grid;
  /* grid-template-columns 由内联 :style="gridStyle" 提供（列宽可拖动） */
  align-items: center;
  padding: 0 8px;
  gap: 6px;
}
.list-head {
  height: 28px; line-height: 28px; flex: none;
  border-bottom: 1px solid rgba(255, 255, 255, 0.12);
  color: rgba(255, 255, 255, 0.55); user-select: none;
}
.list-head .th { position: relative; height: 100%; padding-right: 8px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.list-head .th:hover { color: rgba(255, 255, 255, 0.85); }
.list-head .th.active { color: #70c0e8; }
/* 独立排序按钮：inline 跟随文字，仅悬停表头时显现（visibility 隐藏不占点击），当前排序列常显 */
.sort-btn {
  display: inline-block;
  width: 16px;
  height: 16px;
  margin-left: 2px;
  vertical-align: middle;
  padding: 0;
  border: none;
  border-radius: 3px;
  background: transparent;
  color: rgba(255, 255, 255, 0.45);
  font-size: 9px;
  line-height: 16px;
  cursor: pointer;
  opacity: 0;
  visibility: hidden;
  transition: opacity 0.15s ease, visibility 0.15s, background 0.15s ease, color 0.15s ease;
}
.th:hover .sort-btn { opacity: 1; visibility: visible; }
.sort-btn.on { opacity: 1; visibility: visible; color: #70c0e8; }
.sort-btn:hover { background: rgba(255, 255, 255, 0.14); color: rgba(255, 255, 255, 0.9); }
.sort-btn.on:hover { color: #9fd8f2; }
/* 列宽拖拽手柄：列右缘 6px 热区，悬停/拖动时显示指示线 */
.col-resizer {
  position: absolute;
  right: 0;
  top: 0;
  width: 6px;
  height: 100%;
  cursor: col-resize;
  z-index: 3;
  touch-action: none;
}
.col-resizer::after {
  content: '';
  position: absolute;
  right: 2px;
  top: 22%;
  bottom: 22%;
  width: 2px;
  border-radius: 1px;
  background: transparent;
  transition: background 0.15s ease;
}
.th:hover .col-resizer::after { background: rgba(255, 255, 255, 0.25); }
.col-resizer:hover::after, .col-resizer.active::after { background: #70c0e8; }
.list-body { flex: 1; overflow-y: auto; }
.item { height: 28px; cursor: pointer; border-bottom: 1px solid rgba(255, 255, 255, 0.04); }
.item:hover { background: rgba(255, 255, 255, 0.06); }
.item.selected { background: rgba(32, 128, 240, 0.25); }
.item.error { color: #e88080; }
/* 置顶行：琥珀色左侧条 + 略深背景 */
.item.pinned { background: rgba(229, 192, 123, 0.10); box-shadow: inset 2px 0 0 #e5c07b; }
.item.pinned.selected { background: rgba(32, 128, 240, 0.28); }
.pin-ic { width: 11px; height: 11px; margin-right: 3px; vertical-align: -1px; color: #e5c07b; flex: none; }
.hist-badge { flex: none; margin-right: 4px; padding: 0 4px; border-radius: 3px; font-size: 10px; line-height: 16px; color: #56b6c2; background: rgba(86, 182, 194, 0.14); }
.ellipsis { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.c-time { font-variant-numeric: tabular-nums; color: rgba(255, 255, 255, 0.65); }
.list-head .c-time { color: inherit; }
.dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; background: #555; }
.dot.pending { background: #aaa; }
.dot.streaming { background: #e5c07b; animation: blink 1s infinite alternate; }
.dot.done { background: #63e2b7; }
.dot.error { background: #e88080; }
@keyframes blink { from { opacity: 0.4; } to { opacity: 1; } }
.c-method { font-weight: 600; }
.m-GET { color: #63e2b7; } .m-POST { color: #70c0e8; } .m-PUT, .m-PATCH { color: #e5c07b; }
.m-DELETE { color: #e88080; } .m-CONNECT { color: #b57edc; }
.s-2xx { color: #63e2b7; } .s-3xx { color: #70c0e8; } .s-4xx { color: #e5c07b; } .s-5xx, .s-err { color: #e88080; }
.empty { padding: 40px 16px; text-align: center; color: rgba(255, 255, 255, 0.4); }

/* 复制提示 toast */
.copy-toast {
  position: absolute;
  bottom: 10px;
  left: 0;
  right: 0;
  margin-inline: auto;
  width: fit-content;
  background: #1f2937;
  border: 1px solid #34d399;
  color: #34d399;
  font-size: 11px;
  padding: 4px 12px;
  border-radius: 4px;
  pointer-events: none;
  max-width: 90%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  z-index: 10;
}
</style>
