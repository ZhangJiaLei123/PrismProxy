<template>
  <div class="timeline" :class="{ collapsed }">
    <!-- 展开态：标题 + 图例 + 折叠按钮 -->
    <div v-if="!collapsed" class="tl-head">
      <span class="tl-title">密度时间轴</span>
      <span class="tl-legend">
        <i class="lg" style="background: #2e2e3c" />空
        <i class="lg" style="background: #6d28d9" />低
        <i class="lg" style="background: #ea580c" />中
        <i class="lg" style="background: #facc15" />高
      </span>
      <span v-if="effectiveSel" class="tl-win">窗口：{{ fmtDateTime(effectiveSel[0]) }} ~ {{ fmtDateTime(effectiveSel[1]) }}</span>
      <button class="tl-toggle" title="折叠时间轴" @click="emit('toggle')">
        <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor"><path d="M4.2 6l3.8 3.8L11.8 6z" /></svg>
      </button>
    </div>

    <!-- 展开态图表 -->
    <div
      v-if="!collapsed"
      ref="chartRef"
      class="tl-chart"
      :class="cursorCls"
      @pointerdown="onDown"
      @pointermove="onMove"
      @pointerup="onUp"
      @pointercancel="onCancel"
      @mouseleave="tip = null"
    >
      <template v-if="buckets.length">
        <svg :width="width" :height="CH" class="tl-svg">
          <!-- 桶柱（颜色 + 桶高双通道编码，色弱友好 §九-13） -->
          <rect
            v-for="(b, i) in bars"
            :key="i"
            :x="b.x"
            :y="b.y"
            :width="b.w + 0.6"
            :height="b.h"
            :fill="b.color"
          />
          <!-- 选区外压暗 -->
          <template v-if="effectiveSel">
            <rect x="0" y="0" :width="Math.max(0, selX0)" :height="CH" fill="rgba(16,16,24,0.55)" />
            <rect :x="selX1" y="0" :width="Math.max(0, width - selX1)" :height="CH" fill="rgba(16,16,24,0.55)" />
            <rect
              :x="selX0"
              :y="1"
              :width="Math.max(1, selX1 - selX0)"
              :height="CH - 2"
              fill="rgba(250,204,21,0.08)"
              stroke="#facc15"
              stroke-width="1"
            />
            <rect :x="selX0 - 2" y="0" width="4" :height="CH" fill="#facc15" opacity="0.85" />
            <rect :x="selX1 - 2" y="0" width="4" :height="CH" fill="#facc15" opacity="0.85" />
          </template>
        </svg>
        <div v-if="tip" class="tl-tip" :style="{ left: tip.x + 'px' }">
          <div>{{ fmtDateTime(tip.t0) }} ~ {{ fmtDateTime(tip.t1) }}</div>
          <div class="tt-count">{{ tip.count }} 条</div>
        </div>
        <div v-else-if="!dragging" class="tl-foot-hint">拖拽框选 · 拖动中部平移 · 拖边缘缩放 · 单击空白清除</div>
      </template>
      <div v-else class="tl-empty">暂无数据</div>
    </div>

    <!-- 折叠态：小色条概览 + 窗口指示，点击展开 -->
    <div v-else class="tl-strip" title="展开时间轴" @click="emit('toggle')">
      <div class="strip-bars">
        <span
          v-for="(b, i) in stripBars"
          :key="i"
          class="strip-bar"
          :style="{ left: b.x + '%', width: b.w + '%', background: b.color, height: b.h + '%' }"
        />
        <span
          v-if="effectiveSel"
          class="strip-sel"
          :style="{ left: (selX0 / width) * 100 + '%', width: ((selX1 - selX0) / width) * 100 + '%' }"
        />
      </div>
      <span class="strip-label">时间轴{{ effectiveSel ? ' · 已设窗口' : '' }}</span>
      <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor"><path d="M4.2 10L8 6.2 11.8 10z" /></svg>
    </div>
  </div>
</template>

<script setup lang="ts">
// 密度时间轴（设计 §6.4 / §九-13）：全域分桶直方图 + brush 手势状态机。
// 手势：idle →(按下) pressing →(位移>4px) brushing/panning/resizing →(松手) idle；
// 未超阈值松手=单击（空白处清除窗口）。拖拽中只更新本地 draft，松手才 emit select。
// 底图不随窗口重拉（§九-12）；buckets 数按容器宽自适应（每桶≥2px），上限 500。
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { ReviewHistBucket, ReviewHistogram } from '../lib/types'
import { fmtDateTime } from '../lib/format'

const props = defineProps<{
  histogram: ReviewHistogram
  selection: [number, number] | null
  collapsed: boolean
}>()

const emit = defineEmits<{
  (e: 'select', win: [number, number] | null): void
  (e: 'toggle'): void
  (e: 'buckets', n: number): void
}>()

const CH = 56 // 展开态图表高度
const EDGE = 6 // 选区边缘缩放热区（px）
const CLICK_THRESHOLD = 4 // 单击/拖拽位移阈值（px）
const MIN_BUCKETS_WIN = 2 // 最小窗口=2 桶宽

const chartRef = ref<HTMLElement | null>(null)
const width = ref(300)
const buckets = computed<ReviewHistBucket[]>(() => props.histogram.buckets ?? [])
const span = computed(() => Math.max(0, props.histogram.end - props.histogram.start))
const maxCount = computed(() => buckets.value.reduce((m, b) => Math.max(m, b.count), 0))

// 拖拽中的本地草稿窗口（不回传父级，松手才提交）
const draft = ref<[number, number] | null>(null)
const dragging = ref(false)
type Mode = 'pressing' | 'brushing' | 'panning' | 'resizing-l' | 'resizing-r'
let mode: Mode = 'pressing'
let downX = 0
let downT = 0
let origSel: [number, number] = [0, 0]
let pointerId = -1
const tip = ref<{ x: number; t0: number; t1: number; count: number } | null>(null)

const effectiveSel = computed<[number, number] | null>(() =>
  dragging.value && draft.value ? draft.value : props.selection,
)

// ---- 色温插值：空 #2e2e3c →（低 #6d28d9 → 中 #ea580c → 高 #facc15）----
function hexRGB(hex: string): [number, number, number] {
  return [parseInt(hex.slice(1, 3), 16), parseInt(hex.slice(3, 5), 16), parseInt(hex.slice(5, 7), 16)]
}
const C_LOW = hexRGB('#6d28d9')
const C_MID = hexRGB('#ea580c')
const C_HIGH = hexRGB('#facc15')
function lerp(a: number, b: number, r: number): number {
  return Math.round(a + (b - a) * r)
}
function bucketColor(count: number): string {
  if (count <= 0 || maxCount.value <= 0) return '#2e2e3c'
  const r = count / maxCount.value
  const [a, b, k] = r <= 0.5 ? [C_LOW, C_MID, r / 0.5] : [C_MID, C_HIGH, (r - 0.5) / 0.5]
  return `rgb(${lerp(a[0], b[0], k)},${lerp(a[1], b[1], k)},${lerp(a[2], b[2], k)})`
}

const barW = computed(() => (buckets.value.length ? width.value / buckets.value.length : 0))

const bars = computed(() => {
  const n = buckets.value.length
  if (!n) return []
  const w = width.value / n
  return buckets.value.map((b, i) => {
    const h = b.count === 0 ? 2 : 3 + ((CH - 5) * b.count) / (maxCount.value || 1)
    return { x: i * w, y: CH - h, w, h: Math.ceil(h), color: bucketColor(b.count) }
  })
})

// 折叠态小色条（复用同口径着色，高度百分比）
const stripBars = computed(() => {
  const n = buckets.value.length
  if (!n) return []
  return buckets.value.map((b, i) => ({
    x: (i / n) * 100,
    w: 100 / n + 0.3,
    h: b.count === 0 ? 14 : 30 + 70 * (b.count / (maxCount.value || 1)),
    color: bucketColor(b.count),
  }))
})

// ---- 时间 ↔ 像素 ----
function xToT(x: number): number {
  const W = width.value
  const r = Math.min(1, Math.max(0, x / W))
  return props.histogram.start + r * span.value
}
function tToX(t: number): number {
  if (span.value <= 0) return 0
  return ((t - props.histogram.start) / span.value) * width.value
}
const selX0 = computed(() => (effectiveSel.value ? tToX(effectiveSel.value[0]) : 0))
const selX1 = computed(() => (effectiveSel.value ? tToX(effectiveSel.value[1]) : 0))

const cursorCls = computed(() => {
  if (!buckets.value.length) return ''
  if (dragging.value) {
    if (mode === 'panning') return 'cur-grab'
    if (mode === 'resizing-l' || mode === 'resizing-r') return 'cur-ew'
    return 'cur-cross'
  }
  return ''
})

function clampSel(t0: number, t1: number): [number, number] {
  const lo = props.histogram.start
  const hi = props.histogram.end
  return [Math.min(hi, Math.max(lo, t0)), Math.min(hi, Math.max(lo, t1))]
}

function localX(e: PointerEvent): number {
  const el = chartRef.value
  if (!el) return 0
  return e.clientX - el.getBoundingClientRect().left
}

// ---- 手势状态机 ----
function onDown(e: PointerEvent) {
  if (!buckets.value.length || e.button !== 0) return
  const x = localX(e)
  try {
    chartRef.value?.setPointerCapture?.(e.pointerId)
  } catch { /* 合成事件等无活动指针场景忽略 */ }
  pointerId = e.pointerId
  downX = x
  downT = xToT(x)
  mode = 'pressing'
  if (props.selection) {
    const x0 = tToX(props.selection[0])
    const x1 = tToX(props.selection[1])
    if (x >= x0 && x <= x1) {
      if (Math.abs(x - x0) <= EDGE) mode = 'resizing-l'
      else if (Math.abs(x - x1) <= EDGE) mode = 'resizing-r'
      else mode = 'panning'
      origSel = [...props.selection] as [number, number]
    } else {
      mode = 'brushing'
    }
  } else {
    mode = 'brushing'
  }
  tip.value = null
}

function onMove(e: PointerEvent) {
  const x = localX(e)
  if (pointerId !== -1) {
    // 按压中或拖拽中
    if (mode === 'pressing') return
    if (!dragging.value) {
      if (Math.abs(x - downX) <= CLICK_THRESHOLD) return
      dragging.value = true
      if (mode === 'panning') draft.value = [...origSel] as [number, number]
      else if (mode.startsWith('resizing')) draft.value = [...origSel] as [number, number]
      else draft.value = [downT, downT]
    }
    const t = xToT(x)
    if (mode === 'brushing') {
      draft.value = [Math.min(downT, t), Math.max(downT, t)]
    } else if (mode === 'panning') {
      const dt = ((x - downX) / width.value) * span.value
      const w = origSel[1] - origSel[0]
      draft.value = clampSel(origSel[0] + dt, origSel[1] + dt)
      // 钳制后保持窗口宽度
      if (draft.value[0] === props.histogram.start) draft.value = [props.histogram.start, props.histogram.start + w]
      if (draft.value[1] === props.histogram.end) draft.value = [props.histogram.end - w, props.histogram.end]
    } else if (mode === 'resizing-l') {
      draft.value = clampSel(Math.min(t, origSel[1]), origSel[1])
    } else if (mode === 'resizing-r') {
      draft.value = clampSel(origSel[0], Math.max(t, origSel[0]))
    }
    return
  }
  // 非拖拽：hover tooltip
  if (!buckets.value.length) return
  const w = width.value / buckets.value.length
  const i = Math.min(buckets.value.length - 1, Math.max(0, Math.floor(x / w)))
  const b = buckets.value[i]
  tip.value = { x: i * w, t0: b.t0, t1: b.t1, count: b.count }
}

function onUp(e: PointerEvent) {
  if (pointerId === -1) return
  const wasDragging = dragging.value
  const x = localX(e)
  pointerId = -1
  if (!wasDragging) {
    // 单击：落点在已有选区外（或无选区）→ 清除窗口；选区内原地单击不动
    mode = 'pressing'
    if (props.selection) {
      const x0 = tToX(props.selection[0])
      const x1 = tToX(props.selection[1])
      if (x < x0 || x > x1) emit('select', null)
    } else {
      emit('select', null)
    }
    return
  }
  // 提交：吸附到桶边界，最小 2 桶宽
  const win = draft.value
  draft.value = null
  dragging.value = false
  mode = 'pressing'
  if (!win || !buckets.value.length) return
  const w = width.value / buckets.value.length
  let i0 = Math.floor(tToX(win[0]) / w)
  let i1 = Math.floor(tToX(win[1]) / w)
  i0 = Math.min(buckets.value.length - 1, Math.max(0, i0))
  i1 = Math.min(buckets.value.length - 1, Math.max(0, i1))
  if (i1 < i0) [i0, i1] = [i1, i0]
  if (i1 - i0 + 1 < MIN_BUCKETS_WIN) return // 窄窗丢弃
  emit('select', [buckets.value[i0].t0, buckets.value[i1].t1])
}

function onCancel() {
  pointerId = -1
  dragging.value = false
  draft.value = null
  mode = 'pressing'
}

// ---- 容器宽自适应 → 请求桶数（每桶≥2px，上限 500） ----
let ro: ResizeObserver | null = null
let lastN = -1
function measure() {
  const el = chartRef.value
  if (!el) return
  width.value = el.clientWidth || 300
  const n = Math.min(500, Math.max(24, Math.floor(width.value / 2)))
  if (n !== lastN) {
    lastN = n
    emit('buckets', n)
  }
}

onMounted(() => {
  if (typeof ResizeObserver !== 'undefined' && chartRef.value) {
    ro = new ResizeObserver(measure)
    ro.observe(chartRef.value)
  }
  measure()
})
onBeforeUnmount(() => ro?.disconnect())

// 折叠态 chartRef 被 v-if 移除：展开重建后需重新 observe（ResizeObserver 不自动跟踪）
watch(
  () => props.collapsed,
  async (c) => {
    if (!c) {
      await nextTick()
      ro?.disconnect()
      if (typeof ResizeObserver !== 'undefined' && chartRef.value) {
        ro = new ResizeObserver(measure)
        ro.observe(chartRef.value)
      }
      measure()
    }
  },
)

// 底图切换后草稿无意义（父级已做选区交集中的清除）
watch(
  () => props.histogram,
  () => {
    onCancel()
    tip.value = null
  },
)
</script>

<style scoped>
.timeline {
  flex: none;
  border-bottom: 1px solid rgba(255,255,255,0.08);
  background: rgba(255,255,255,0.02);
  user-select: none;
}
.tl-head {
  display: flex; align-items: center; gap: 10px;
  padding: 4px 10px 2px;
}
.tl-title { font-size: 11px; color: rgba(255,255,255,0.55); }
.tl-legend { display: inline-flex; align-items: center; gap: 3px; font-size: 10px; color: rgba(255,255,255,0.4); }
.tl-legend .lg { display: inline-block; width: 9px; height: 9px; border-radius: 2px; margin-left: 5px; }
.tl-win { margin-left: auto; font-size: 10px; color: #facc15; opacity: 0.85; }
.tl-toggle {
  margin-left: auto; background: none; border: none; color: rgba(255,255,255,0.45);
  cursor: pointer; padding: 2px; display: inline-flex; border-radius: 3px;
}
.tl-win + .tl-toggle { margin-left: 6px; }
.tl-toggle:hover { color: rgba(255,255,255,0.85); background: rgba(255,255,255,0.06); }
.tl-chart { position: relative; height: 56px; margin: 0 8px 2px; touch-action: none; }
.tl-chart.cur-cross { cursor: crosshair; }
.tl-chart.cur-grab { cursor: grabbing; }
.tl-chart.cur-ew { cursor: ew-resize; }
.tl-svg { display: block; }
.tl-empty {
  position: absolute; inset: 0; display: flex; align-items: center; justify-content: center;
  font-size: 11px; color: rgba(255,255,255,0.3);
}
.tl-tip {
  position: absolute; top: 2px; transform: translateX(4px);
  background: rgba(30,30,42,0.95); border: 1px solid rgba(255,255,255,0.15);
  border-radius: 4px; padding: 4px 7px; font-size: 10px; color: rgba(255,255,255,0.8);
  white-space: nowrap; pointer-events: none; line-height: 1.5;
}
.tt-count { color: #facc15; }
.tl-foot-hint {
  position: absolute; right: 4px; bottom: 1px; font-size: 9px;
  color: rgba(255,255,255,0.22); pointer-events: none;
}
/* 折叠态 */
.tl-strip {
  display: flex; align-items: center; gap: 8px; padding: 3px 10px; cursor: pointer;
}
.tl-strip:hover { background: rgba(255,255,255,0.04); }
.strip-bars { position: relative; flex: 1; height: 14px; }
.strip-bar { position: absolute; bottom: 0; border-radius: 1px; }
.strip-sel {
  position: absolute; top: -1px; bottom: -1px;
  border: 1px solid #facc15; background: rgba(250,204,21,0.12); pointer-events: none;
}
.strip-label { font-size: 10px; color: rgba(255,255,255,0.4); white-space: nowrap; }
.tl-strip svg { color: rgba(255,255,255,0.4); flex: none; }
</style>
