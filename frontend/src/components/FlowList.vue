<template>
  <div class="flow-list">
    <div class="list-head row" :style="gridStyle">
      <span class="c-state"></span>
      <span
        v-for="col in columns"
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
          :class="{ selected: f.ID === store.selectedId, error: f.State === 'error' }"
          @click="store.select(f.ID)"
        >
          <span class="c-state"><i class="dot" :class="f.State"></i></span>
          <span class="c-time" @dblclick.stop="copyCell(fmtTime(f.StartedAt))">{{ fmtTime(f.StartedAt) }}</span>
          <span class="c-method" :class="'m-' + f.Method" @dblclick.stop="copyCell(f.Method)">{{ f.Method }}</span>
          <span class="c-status" :class="statusClass(f)" @dblclick.stop="copyCell(f.Status ? String(f.Status) : '')">{{ f.Status || '—' }}</span>
          <span class="c-host ellipsis" :title="f.Host" @dblclick.stop="copyCell(f.Host)">{{ f.Host }}</span>
          <span class="c-path ellipsis" :title="f.URL" @dblclick.stop="copyCell(f.Path || f.URL)">{{ f.Path || f.URL }}</span>
          <span class="c-dur" @dblclick.stop="copyCell(fmtDuration(f.DurationMS))">{{ fmtDuration(f.DurationMS) }}</span>
          <span class="c-size" @dblclick.stop="copyCell(fmtBytes(f.BytesDown))">{{ fmtBytes(f.BytesDown) }}</span>
          <span class="c-proc ellipsis" :title="f.ProcessName + ' (' + f.PID + ')'" @dblclick.stop="copyCell(f.ProcessName)">{{ f.ProcessName }}</span>
        </div>
        <div v-if="!store.filtered.length" class="empty">
          {{ store.flows.length ? '无匹配流量 —— 调整底部过滤条件' : '暂无流量 —— 将系统代理指向 9090 端口或配置应用代理后开始抓包' }}
        </div>
      </div>
    </div>
    <transition name="fade">
      <div v-if="copiedText !== null" class="copy-toast">已复制：{{ copiedText }}</div>
    </transition>
  </div>
</template>

<script setup lang="ts">
import { useVirtualList } from '@vueuse/core'
import { computed, ref } from 'vue'
import { useFlowsStore } from '../stores/flows'
import { fmtBytes, fmtDuration, fmtTime } from '../lib/format'
import type { main } from '../../wailsjs/go/models'

const store = useFlowsStore()

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

const gridStyle = computed(() => ({
  gridTemplateColumns: colWidths.value.map((w) => (w === null ? '1fr' : w + 'px')).join(' '),
}))

function startResize(e: PointerEvent, wi: number) {
  const head = (e.target as HTMLElement).closest('.list-head')
  if (!head) return
  const startX = e.clientX
  const colEl = head.children[wi] as HTMLElement
  const startW = colEl.getBoundingClientRect().width
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

const valOf: Record<SortKey, (f: main.FlowMeta) => number | string> = {
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
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    // 剪贴板 API 不可用（非安全上下文）时降级
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    ta.remove()
  }
  copiedText.value = text.length > 40 ? text.slice(0, 40) + '…' : text
  clearTimeout(copyTimer)
  copyTimer = setTimeout(() => (copiedText.value = null), 1500)
}

const { list, containerProps, wrapperProps } = useVirtualList(sortedFlows, {
  itemHeight: 28,
  overscan: 15,
})

function statusClass(f: main.FlowMeta): string {
  if (f.State === 'error') return 's-err'
  const s = f.Status
  if (s >= 500) return 's-5xx'
  if (s >= 400) return 's-4xx'
  if (s >= 300) return 's-3xx'
  if (s >= 200) return 's-2xx'
  return ''
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
