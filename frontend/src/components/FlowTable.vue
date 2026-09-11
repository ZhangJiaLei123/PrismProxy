<template>
  <!--
    运行时无关的共享流量表格：主窗 FlowList（客户端排序/实时 store）与数据复盘列表
    （服务端排序+分页/归档库）共用。本组件只负责：
    - 虚拟滚动（固定 28px 行高）、列宽拖拽 / 列顺序 / 显隐 / 重置（localStorage 按 layoutKey 隔离）
    - 双击复制单元格、复制 toast
    - 行右键菜单与表头右键菜单（菜单项与动作经 actions 注入，组件内不碰 wails/store）
    排序方向状态在父级（@sort），本组件只呈现并上抛。
  -->
  <div class="flow-table">
    <div class="list-head row" :style="gridStyle" @contextmenu.prevent="onHeaderMenu($event)">
      <!-- 勾选列独立于 columns 渲染：不进 colOrder/显隐菜单/布局持久化（M13 P4-9） -->
      <span v-if="selectable" class="c-check" @contextmenu.stop>
        <n-checkbox
          size="small"
          :checked="allChecked"
          :indeterminate="someChecked && !allChecked"
          :disabled="!rows.length"
          @update:checked="toggleAll"
        />
      </span>
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
          @click.stop="emit('sort', col.key)"
        >{{ sortKey === col.key ? (sortDir === 'asc' ? '▲' : '▼') : '↕' }}</button>
        <i
          class="col-resizer"
          :class="{ active: resizingWi === col.wi }"
          title="拖动调整列宽"
          @pointerdown.prevent.stop="startResize($event, col.wi)"
        ></i>
      </span>
    </div>
    <div v-bind="containerProps" class="list-body" @scroll="onScroll">
      <div v-bind="wrapperProps">
        <div
          v-for="{ data: f } in list"
          :key="f.ID"
          class="row item"
          :style="gridStyle"
          :class="rowClass(f)"
          @click="emit('select', f.ID)"
          @contextmenu.prevent="onContextMenu($event, f)"
        >
          <span
            v-if="selectable"
            class="c-check"
            @click.stop
            @dblclick.stop
            @contextmenu.stop
          >
            <n-checkbox
              size="small"
              :checked="checkedSet.has(f.ID)"
              @update:checked="() => toggleRow(f.ID)"
            />
          </span>
          <span class="c-state"><i class="dot" :class="f.State"></i></span>
          <span
            v-for="col in visColumns"
            :key="col.key"
            :class="cellCls(col, f)"
            :title="cellTitle(col, f)"
            @dblclick.stop="copyCell(cellText(col, f))"
          >
            <slot :name="'cell-' + col.key" :flow="f">{{ cellText(col, f) }}</slot>
          </span>
        </div>
      </div>
    </div>
    <!-- 空态放在虚拟列表外层：wrapper 高度随内容收缩，空行时为 0 会导致提示不可见 -->
    <div v-if="!rows.length && emptyText" class="empty">{{ emptyText }}</div>
    <!-- 行右键菜单（manual 定位，渲染在虚拟列表外层，避免行回收导致菜单消失） -->
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
    <!-- 表头右键菜单：列显示勾选 / 左右调整列顺序 / 重置 -->
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
import { computed, ref, watch } from 'vue'
import { NCheckbox, NDropdown } from 'naive-ui'
import type { DropdownOption } from 'naive-ui'
import { fmtBytes, fmtDuration, fmtTime } from '../lib/format'
import { copyText } from '../lib/clip'
import type { ReviewFlowMeta } from '../lib/types'

/** 数据列定义（wi 从 1 起，0 固定为状态点列）；cls 同时是单元格 class 与表头定位依据。 */
export interface FlowColumn {
  key: string
  label: string
  cls: string
  wi: number
  /** 排序时的比较值（仅客户端排序的主窗使用；复盘服务端排序不用） */
  sortVal?: (f: ReviewFlowMeta) => number | string
  /** 单元格展示文本（双击复制也复制它）；默认走内置 time/method/... 分支 */
  text?: (f: ReviewFlowMeta) => string
  /** 单元格 title 提示 */
  title?: (f: ReviewFlowMeta) => string
  /** 附加 class（如 m-GET / s-2xx 着色） */
  cellCls?: (f: ReviewFlowMeta) => string
}

/** 行右键可忽略的列维度；主窗实时忽略仅 host/path/proc，复盘额外支持 method/status */
export type FlowIgnoreKind = 'host' | 'path' | 'proc' | 'method' | 'status'

/** 未显式传 ignoreKinds 时的默认维度（主窗实时快捷忽略口径：无 method/status） */
const DEFAULT_IGNORE_KINDS: FlowIgnoreKind[] = ['host', 'path', 'proc']

/** 行右键动作集合；返回 false/抛错由组件统一提示，菜单项是否出现/禁用由 buildMenu 决定。 */
export interface FlowTableActions {
  /** 生成 cmd / bash 两种 shell 的 cURL；bodyOmitted=true 时组件提示手动补充 body */
  buildCurl?: (f: ReviewFlowMeta, shell: 'cmd' | 'bash') => Promise<{ command: string; bodyOmitted?: boolean }>
  pin?: (f: ReviewFlowMeta) => Promise<void> | void
  ignore?: (f: ReviewFlowMeta, kind: FlowIgnoreKind) => Promise<void> | void
  /** 支持右键忽略的列维度；缺省仅 host/path/proc（主窗快捷忽略口径） */
  ignoreKinds?: FlowIgnoreKind[]
  compose?: (f: ReviewFlowMeta) => Promise<void> | void
}

const props = withDefaults(
  defineProps<{
    rows: ReviewFlowMeta[]
    columns: FlowColumn[]
    /** 当前排序键/方向（仅用于表头显示；点击上抛 @sort 由父级决定） */
    sortKey: string
    sortDir: 'asc' | 'desc'
    /** 列布局 localStorage key（主窗/复盘各一份） */
    layoutKey: string
    /** 各列默认像素宽（长度 = columns.length + 1，首项为状态点列）；null=1fr */
    defaultWidths: (number | null)[]
    /** 各列最小宽（长度同 defaultWidths） */
    colMins: number[]
    selectedId?: string
    actions?: FlowTableActions
    emptyText?: string
    /** 行附加 class（如 pinned/error 之外的定制） */
    rowExtraClass?: (f: ReviewFlowMeta) => Record<string, boolean>
    /** M13 P4-9：开启行勾选列（仅复盘页传入；默认关闭=主窗零影响） */
    selectable?: boolean
    /** v-model:checkedIds：当前勾选的流 id 集合（父级持有并负责作废规则） */
    checkedIds?: string[]
  }>(),
  { selectedId: '', emptyText: '', selectable: false, checkedIds: () => [] },
)

const emit = defineEmits<{
  (e: 'select', id: string): void
  (e: 'sort', key: string): void
  /** 右键动作抛错（忽略/置顶/cURL/重发），交由父级 useMessage 提示 */
  (e: 'action-error', message: string): void
  /** cURL 生成成功但 body 因二进制/超限/截断未内联 */
  (e: 'curl-omitted'): void
  /** v-model:checkedIds */
  (e: 'update:checkedIds', ids: string[]): void
}>()

const COL_MAX = 400

// ---- 列宽拖动 ----
const colWidths = ref<(number | null)[]>([...props.defaultWidths])
const resizingWi = ref<number | null>(null)

// 列顺序：元素为列索引（0=固定状态点列，1..N=数据列），仅可见列出现在序中
const colOrder = ref<number[]>([0, ...props.columns.map((c) => c.wi)])
const visColumns = computed(() => colOrder.value.filter((w) => w >= 1).map((w) => props.columns[w - 1]))

function saveLayout() {
  try {
    localStorage.setItem(props.layoutKey, JSON.stringify({ order: colOrder.value, widths: colWidths.value }))
  } catch {
    // 存储不可用（隐私模式/配额）静默失败，布局仅本会话生效
  }
}

// 校验并应用布局；数据列 wi 范围 1..maxWi，widths 长度须匹配 colMins
function applyLayout(order: unknown, widths: unknown): boolean {
  const maxWi = props.columns.length
  if (!Array.isArray(order) || order[0] !== 0 || order.length < 2) return false
  const rest = order.slice(1) as unknown[]
  if (!rest.every((n) => Number.isInteger(n) && n >= 1 && n <= maxWi) || new Set(rest).size !== rest.length) return false
  if (!Array.isArray(widths) || widths.length !== props.colMins.length) return false
  for (let i = 0; i < widths.length; i++) {
    const w = widths[i]
    if (w !== null && (typeof w !== 'number' || w < props.colMins[i] || w > COL_MAX)) return false
  }
  colOrder.value = order as number[]
  colWidths.value = widths as (number | null)[]
  return true
}

function loadLayout() {
  try {
    const raw = localStorage.getItem(props.layoutKey)
    if (raw) {
      const v = JSON.parse(raw) as { order?: unknown; widths?: unknown }
      applyLayout(v.order, v.widths)
    }
  } catch {
    // JSON 损坏：保持默认布局
  }
}

loadLayout()

// 列定义随实例固定（主窗/复盘各自静态列），默认宽度变化时（HMR 等）同步未自定义部分
watch(
  () => props.defaultWidths,
  (w) => {
    if (colWidths.value.length !== w.length) colWidths.value = [...w]
  },
)

const gridStyle = computed(() => {
  const cols = colOrder.value.map((w) => (colWidths.value[w] === null ? '1fr' : colWidths.value[w] + 'px'))
  // 勾选列固定 22px 前缀（不参与拖宽/重排/持久化）
  return { gridTemplateColumns: props.selectable ? '22px ' + cols.join(' ') : cols.join(' ') }
})

// ---- 行勾选（M13 P4-9：仅 selectable 开启时；集合由父级持有，组件只上抛变更） ----
const checkedSet = computed(() => new Set(props.checkedIds ?? []))
const allChecked = computed(() => props.rows.length > 0 && props.rows.every((f) => checkedSet.value.has(f.ID)))
const someChecked = computed(() => props.rows.some((f) => checkedSet.value.has(f.ID)))

function toggleRow(id: string) {
  const set = new Set(props.checkedIds ?? [])
  if (set.has(id)) set.delete(id)
  else set.add(id)
  emit('update:checkedIds', [...set])
}

function toggleAll(checked: boolean) {
  // 全选范围=当前视图行（复盘页即当前页数据）；取消仅移除本页勾选
  const set = new Set(props.checkedIds ?? [])
  for (const f of props.rows) {
    if (checked) set.add(f.ID)
    else set.delete(f.ID)
  }
  emit('update:checkedIds', [...set])
}

function startResize(e: PointerEvent, wi: number) {
  const th = (e.target as HTMLElement).closest('.th') as HTMLElement | null
  if (!th) return
  const startX = e.clientX
  const startW = th.getBoundingClientRect().width
  // 弹性列被拖时固化为当前像素宽
  if (colWidths.value[wi] === null) colWidths.value[wi] = startW
  const min = props.colMins[wi]
  resizingWi.value = wi
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'

  const onMove = (ev: PointerEvent) => {
    colWidths.value[wi] = Math.min(COL_MAX, Math.max(min, Math.round(startW + ev.clientX - startX)))
  }
  const onUp = () => {
    resizingWi.value = null
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
    window.removeEventListener('pointermove', onMove)
    window.removeEventListener('pointerup', onUp)
    saveLayout() // 松手时一次性持久化
  }
  window.addEventListener('pointermove', onMove)
  window.addEventListener('pointerup', onUp)
}

// ---- 虚拟滚动（固定 28px 行高，overscan 15，与原 FlowList 一致） ----
const virtual = useVirtualList(computed(() => props.rows), { itemHeight: 28, overscan: 15 })
const list = virtual.list
const containerProps = virtual.containerProps
const wrapperProps = virtual.wrapperProps
// 复盘页码切换后须回到顶部（useVirtualList 不重置 scrollTop）
function onScroll(e: Event) {
  // 仅作为容器 ref 透传占位；翻页归零由父级通过 expose 的 scrollToTop 调用
  scrollEl.value = e.target as HTMLElement
}
const scrollEl = ref<HTMLElement | null>(null)
function scrollToTop() {
  scrollEl.value?.scrollTo({ top: 0 })
}
defineExpose({ scrollToTop })

// ---- 单元格文本 / class / title ----
function cellText(col: FlowColumn, f: ReviewFlowMeta): string {
  if (col.text) return col.text(f)
  switch (col.key) {
    case 'time': return fmtTime(f.StartedAt)
    case 'method': return f.Method
    case 'status': return f.Status ? String(f.Status) : '—'
    case 'host': return f.Host
    case 'path': return f.Path || f.URL
    case 'dur': return fmtDuration(f.DurationMS)
    case 'size': return fmtBytes(f.BytesDown)
    case 'proc': return f.ProcessName
    case 'tags': return (f.Tags || []).join('、')
    default: return ''
  }
}

function cellTitle(col: FlowColumn, f: ReviewFlowMeta): string {
  if (col.title) return col.title(f)
  return ''
}

function cellCls(col: FlowColumn, f: ReviewFlowMeta): string {
  const ellipsis = ['host', 'path', 'proc', 'tags'].includes(col.key)
  const base = col.cls + (ellipsis ? ' ellipsis' : '')
  if (col.cellCls) return base + ' ' + col.cellCls(f)
  return base
}

function rowClass(f: ReviewFlowMeta): Record<string, boolean> {
  return {
    selected: f.ID === props.selectedId,
    error: f.State === 'error',
    pinned: !!f.Pinned,
    ...(props.rowExtraClass ? props.rowExtraClass(f) : {}),
  }
}

// ---- 双击复制 ----
const copiedText = ref<string | null>(null)
let copyTimer: ReturnType<typeof setTimeout> | undefined

async function copyCell(text: string) {
  if (!text) return
  await copyText(text)
  showToast(text)
}

function showToast(label: string) {
  copiedText.value = label.length > 40 ? label.slice(0, 40) + '…' : label
  clearTimeout(copyTimer)
  copyTimer = setTimeout(() => (copiedText.value = null), 1500)
}

// ---- 行右键菜单 ----
const ctxShow = ref(false)
const ctxX = ref(0)
const ctxY = ref(0)
const ctxFlow = ref<ReviewFlowMeta | null>(null)
// 右键落点列：决定是否给出「忽略此域名/路径/进程/方法/状态」入口
const ctxCol = ref<FlowIgnoreKind | null>(null)

function onContextMenu(e: MouseEvent, f: ReviewFlowMeta) {
  const cell = (e.target as HTMLElement).closest('span')
  const cls = cell?.classList
  ctxCol.value = cls?.contains('c-proc')
    ? 'proc'
    : cls?.contains('c-host')
      ? 'host'
      : cls?.contains('c-path')
        ? 'path'
        : cls?.contains('c-method')
          ? 'method'
          : cls?.contains('c-status')
            ? 'status'
            : null
  ctxFlow.value = f
  ctxX.value = e.clientX
  ctxY.value = e.clientY
  ctxShow.value = true
}

const ctxOptions = computed<DropdownOption[]>(() => {
  const f = ctxFlow.value
  const a = props.actions
  if (!f || !a) return []
  const opts: DropdownOption[] = []
  // 复制 URL：恒提供（组件内直接处理，不经 actions）
  opts.push({ label: '复制 URL', key: 'copy-url' })
  if (a.buildCurl) {
    opts.push(
      { label: '复制 cURL (cmd)', key: 'curl-cmd' },
      { label: '复制 cURL (bash / Git Bash)', key: 'curl-bash' },
    )
  }
  if (a.pin) {
    if (opts.length) opts.push({ type: 'divider', key: 'd1' })
    opts.push({ label: f.Pinned ? '取消置顶' : '置顶（固定顶部、不被淘汰）', key: 'pin' })
  }
  if (a.ignore && ctxCol.value && (a.ignoreKinds ?? DEFAULT_IGNORE_KINDS).includes(ctxCol.value)) {
    if (opts.length) opts.push({ type: 'divider', key: 'd2' })
    if (ctxCol.value === 'host' && f.Host) {
      opts.push({ label: `忽略此域名（${f.Host} 及其子域）`, key: 'ignore-host' })
    }
    if (ctxCol.value === 'path') {
      const p = f.Path || ''
      const short = p.length > 32 ? p.slice(0, 32) + '…' : p
      opts.push({
        label: p ? `忽略此路径（${short} 及其下级路径）` : '忽略此路径（隧道流无路径）',
        key: 'ignore-path',
        disabled: !p || f.Method === 'CONNECT',
      })
    }
    if (ctxCol.value === 'proc') {
      opts.push({
        label: f.ProcessName ? `忽略此进程（${f.ProcessName}）` : '忽略此进程（进程未知）',
        key: 'ignore-proc',
        disabled: !f.ProcessName,
      })
    }
    if (ctxCol.value === 'method') {
      opts.push({
        label: f.Method ? `忽略此方法（所有 ${f.Method} 请求）` : '忽略此方法（方法未知）',
        key: 'ignore-method',
        disabled: !f.Method,
      })
    }
    if (ctxCol.value === 'status') {
      opts.push({
        label: f.Status ? `忽略此状态码（所有 ${f.Status} 响应）` : '忽略此状态码（无响应流不可忽略）',
        key: 'ignore-status',
        disabled: !f.Status,
      })
    }
  }
  if (a.compose) {
    if (opts.length) opts.push({ type: 'divider', key: 'd3' })
    opts.push({
      label: '调试重发（编辑后重新发送）',
      key: 'composer',
      disabled: !f.URL || f.Method === 'CONNECT',
    })
  }
  return opts
})

async function onCtxSelect(key: string) {
  const f = ctxFlow.value
  const a = props.actions
  ctxShow.value = false
  if (!f) return
  try {
    if (key === 'copy-url') {
      await copyText(f.URL)
      showToast(f.URL)
      return
    }
    if (key.startsWith('curl-') && a?.buildCurl) {
      const shell = key === 'curl-cmd' ? 'cmd' : 'bash'
      const r = await a.buildCurl(f, shell)
      await copyText(r.command)
      showToast('cURL 命令（' + shell + '）')
      if (r.bodyOmitted) emit('curl-omitted')
      return
    }
    if (key === 'pin' && a?.pin) {
      await a.pin(f)
      return
    }
    if (key === 'ignore-host' && a?.ignore) {
      await a.ignore(f, 'host')
      return
    }
    if (key === 'ignore-path' && a?.ignore) {
      await a.ignore(f, 'path')
      return
    }
    if (key === 'ignore-proc' && a?.ignore) {
      await a.ignore(f, 'proc')
      return
    }
    if (key === 'ignore-method' && a?.ignore) {
      await a.ignore(f, 'method')
      return
    }
    if (key === 'ignore-status' && a?.ignore) {
      await a.ignore(f, 'status')
      return
    }
    if (key === 'composer' && a?.compose) {
      await a.compose(f)
    }
  } catch (e) {
    emit('action-error', String((e as Error)?.message ?? e))
  }
}

// ---- 表头右键菜单 ----
const headCtxShow = ref(false)
const headCtxX = ref(0)
const headCtxY = ref(0)
// 右键落点的数据列 wi（1..N）；null=点在空白/状态列
const headCtxWi = ref<number | null>(null)

function onHeaderMenu(e: MouseEvent) {
  const th = (e.target as HTMLElement).closest('.th') as HTMLElement | null
  // th 的首个 class 即列 cls（如 c-time），据此反查 wi；点状态列/空白为 null
  headCtxWi.value = th ? props.columns.findIndex((c) => c.cls === th.classList[0]) + 1 : null
  headCtxX.value = e.clientX
  headCtxY.value = e.clientY
  headCtxShow.value = true
}

const headCtxOptions = computed<DropdownOption[]>(() => {
  const wi = headCtxWi.value
  const idx = wi === null ? -1 : colOrder.value.indexOf(wi)
  return [
    { label: '左移此列', key: 'col-left', disabled: wi === null || idx <= 1 },
    { label: '右移此列', key: 'col-right', disabled: wi === null || idx < 1 || idx >= colOrder.value.length - 1 },
    { type: 'divider', key: 'hd1' },
    {
      label: '显示/隐藏列',
      key: 'col-vis',
      children: props.columns.map((c) => ({
        label: (colOrder.value.includes(c.wi) ? '✓ ' : '　') + c.label,
        key: 'vis-' + c.wi,
        // 至少保留状态点 + 一个数据列
        disabled: colOrder.value.includes(c.wi) && colOrder.value.length <= 2,
      })),
    },
    { label: '重置列布局', key: 'col-reset' },
  ]
})

function onHeadCtxSelect(key: string) {
  headCtxShow.value = false
  if (key === 'col-reset') {
    colOrder.value = [0, ...props.columns.map((c) => c.wi)]
    colWidths.value = [...props.defaultWidths]
  } else if (key === 'col-left' || key === 'col-right') {
    const order = colOrder.value
    const wi = headCtxWi.value
    if (wi === null) return
    const i = order.indexOf(wi)
    if (i < 1) return
    const j = key === 'col-left' ? i - 1 : i + 1
    if (j < 1 || j >= order.length) return
    // 与相邻可见列交换（colOrder[0] 恒为状态点列，不参与交换）
    ;[order[i], order[j]] = [order[j], order[i]]
    colOrder.value = [...order]
  } else if (key.startsWith('vis-')) {
    const order = colOrder.value
    const wi = Number(key.slice(4))
    const i = order.indexOf(wi)
    if (i >= 0) {
      if (order.length <= 2) return // 至少保留一个数据列
      order.splice(i, 1)
    } else {
      // 恢复显示：按默认相对顺序插回（前一个默认序仍可见列之后，找不到置数据列首位）
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
.flow-table { height: 100%; display: flex; flex-direction: column; font-size: 12px; position: relative; }
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
.item.pinned { background: rgba(229, 192, 123, 0.10); box-shadow: inset 2px 0 0 #e5c07b; }
.item.pinned.selected { background: rgba(32, 128, 240, 0.28); }
.pin-ic { width: 11px; height: 11px; margin-right: 3px; vertical-align: -1px; color: #e5c07b; flex: none; }
.hist-badge { flex: none; margin-right: 4px; padding: 0 4px; border-radius: 3px; font-size: 10px; line-height: 16px; color: #56b6c2; background: rgba(86, 182, 194, 0.14); }
.c-tags { display: flex; align-items: center; gap: 3px; overflow: hidden; }
.c-tags :deep(.tag-badge) { flex: none; max-width: 72px; height: 18px; font-size: 10px; line-height: 18px; padding: 0 5px; color: #c0a8f0; background: rgba(181, 126, 220, 0.16); }
.c-tags .tag-more { flex: none; font-size: 10px; color: rgba(255, 255, 255, 0.5); }
.ellipsis { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.c-check { display: flex; align-items: center; justify-content: center; }
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
.empty {
  position: absolute;
  top: 28px;
  left: 0;
  right: 0;
  bottom: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
  text-align: center;
  color: rgba(255, 255, 255, 0.4);
  pointer-events: none;
}
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
.fade-enter-active, .fade-leave-active { transition: opacity 0.2s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }
</style>
