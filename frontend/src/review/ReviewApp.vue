<template>
  <div class="review-root">
          <!-- 顶栏 -->
          <header class="topbar">
            <div class="brand">
              <svg viewBox="0 0 16 16" width="16" height="16" fill="currentColor" style="color: #b57edc">
                <path d="M2 1.5h8.2L14 5.3V14.5H2V1.5zm1.5 1.5v10h9V6.1L8.9 3H3.5zm2 3h5V7.5h-5V6zm0 2.8h5v1.5h-5V8.8zm0 2.8h3.4v1.5H5.5v-1.5z" />
              </svg>
              <b>数据复盘</b>
              <n-tag v-if="api.mode === 'demo'" size="tiny" type="warning" :bordered="false">演示数据</n-tag>
            </div>
            <div class="top-actions">
              <n-button size="small" quaternary :loading="loading" title="重新加载标签与列表" @click="refreshAll">
                <span style="display: inline-flex; align-items: center; gap: 4px">
                  <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor">
                    <path d="M8 3a5 5 0 1 0 4.55 2.94l-1.36.63A3.5 3.5 0 1 1 11.5 8H9.2l2.8-2.8L14.8 8H12.4A5 5 0 0 0 8 3z" />
                  </svg>
                  刷新
                </span>
              </n-button>
            </div>
          </header>

          <!-- 敏感凭据提示（设计 §6.3 固有暴露面明示；关闭后写 localStorage 不再展示） -->
          <div v-if="showWarn" class="warn-bar">
            <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor" style="flex: none">
              <path d="M8 1.2L15.6 14H.4L8 1.2zm0 3.3L3.7 12.5h8.6L8 4.5zm-.75 2.5h1.5v3h-1.5v-3zm0 3.8h1.5v1.3h-1.5v-1.3z" />
            </svg>
            <span>归档内容可能包含敏感凭据（Token / Cookie / 密码等），请勿在共享屏幕、录屏或不可信浏览器扩展环境打开本页。</span>
            <n-popconfirm
              placement="bottom-end"
              positive-text="不再显示"
              negative-text="直接关闭"
              @positive-click="dismissWarn"
              @negative-click="closeWarnOnce"
            >
              <template #trigger>
                <svg
                  class="warn-x"
                  viewBox="0 0 16 16" width="13" height="13" fill="currentColor"
                  title="关闭提示"
                >
                  <path d="M8 6.59L12.95 1.64l1.41 1.41L9.41 8l4.95 4.95-1.41 1.41L8 9.41l-4.95 4.95-1.41-1.41L6.59 8 1.64 3.05l1.41-1.41L8 6.59z" />
                </svg>
              </template>
              「直接关闭」仅本次隐藏；「不再显示」以后都不再提示。
            </n-popconfirm>
          </div>

          <!-- 致命错误态：未授权 / 应用退出 -->
          <div v-if="fatal" class="fatal">
            <div class="fatal-icon">🔌</div>
            <div class="fatal-title">{{ fatal.title }}</div>
            <div class="fatal-sub">{{ fatal.sub }}</div>
            <n-button size="small" style="margin-top: 12px" @click="refreshAll">重试</n-button>
          </div>

          <div v-else ref="bodyEl" class="body">
            <review-sidebar
              :tags="tags"
              :selected="selectedTag"
              :total-count="totalCount"
              :loading="loading"
              :do-rename="doRename"
              :do-delete="doDelete"
              :style="{ width: sidebarW + 'px' }"
              @select="onSelectTag"
              @refresh="refreshAll"
            />
            <div
              class="splitter"
              :class="{ dragging: dragPane === 'sidebar' }"
              title="拖拽调整宽度"
              @pointerdown="startDrag($event, 'sidebar')"
            ></div>

            <div class="right">
              <div class="list-pane" :style="listW ? { width: listW + 'px' } : undefined">
                <div class="list-toolbar">
                  <n-input
                    v-model:value="keyword"
                    size="small"
                    clearable
                    placeholder="筛选域名 / 路径 / 方法…"
                    style="max-width: 280px"
                  >
                    <template #prefix>
                      <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor" style="opacity: 0.6">
                        <path d="M11.7 10.3l3 3-1.4 1.4-3-3a6 6 0 1 1 1.4-1.4zm-5.2.7a4.5 4.5 0 1 0 0-9 4.5 4.5 0 0 0 0 9z" />
                      </svg>
                    </template>
                  </n-input>

                  <!-- 时间范围筛选：图标手动设置（与时间轴 brush 双向共用 winStart/winEnd），默认空=不限 -->
                  <n-popover trigger="click" placement="bottom-start" :show="timePopShow" @update:show="onTimePop">
                    <template #trigger>
                      <span class="time-btn" :class="{ active: !!winStart }" title="按时间范围筛选">
                        <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor">
                          <path d="M8 1.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13zm0 1.5a5 5 0 1 1 0 10 5 5 0 0 1 0-10zm-.75 1.5h1.5v3.1l2.1 1.2-.75 1.3L7.25 8.6V3z" />
                        </svg>
                      </span>
                    </template>
                    <div class="time-pop">
                      <div class="time-pop-quick">
                        <n-button size="tiny" quaternary @click="onQuick('1m')">近1分钟</n-button>
                        <n-button size="tiny" quaternary @click="onQuick('10m')">近10分钟</n-button>
                        <n-button size="tiny" quaternary @click="onQuick('1h')">近1小时</n-button>
                        <n-button size="tiny" quaternary @click="onQuick('today')">今天</n-button>
                      </div>
                      <n-date-picker
                        v-model:value="timeDraft"
                        type="datetimerange"
                        size="small"
                        clearable
                        start-placeholder="开始时间"
                        end-placeholder="结束时间"
                      />
                      <div class="time-pop-actions">
                        <n-button size="tiny" quaternary @click="onTimeClear">清除筛选</n-button>
                        <n-button size="tiny" type="primary" :disabled="!timeDraft" @click="onTimeApply">应用</n-button>
                      </div>
                    </div>
                  </n-popover>

                  <!-- 数据范围切换（设计 §6.4 需求5）：仅 tagID=all 可用，具体标签下禁用并提示 -->
                  <n-tooltip :disabled="selectedTag === 'all'" placement="bottom">
                    <template #trigger>
                      <span class="scope-wrap" :class="{ 'scope-off': selectedTag !== 'all' }">
                        <n-radio-group
                          v-model:value="scope"
                          size="small"
                          type="buttons"
                          :disabled="selectedTag !== 'all'"
                          @update:value="onScopeChange"
                        >
                          <n-radio-button value="archived">归档数据</n-radio-button>
                          <n-radio-button value="all">全部数据</n-radio-button>
                        </n-radio-group>
                      </span>
                    </template>
                    具体标签下均为归档数据
                  </n-tooltip>

                  <span v-if="winStart" class="win-chip">
                    {{ fmtDateTime(winStart) }} ~ {{ fmtDateTime(winEnd) }}
                    <i class="win-x" title="清除时间筛选" @click="clearWindow">✕</i>
                  </span>
                  <span class="list-count">{{ currentTagName }} · {{ rangeText }} · {{ filteredFlows.length }} / {{ total }} 条</span>
                </div>

                <!-- 密度时间轴（§6.4 需求7）：置于列表 pane 顶部，可折叠 -->
                <review-timeline
                  :histogram="histogram"
                  :selection="winStart ? [winStart, winEnd] : null"
                  :collapsed="timelineCollapsed"
                  @select="onTimelineSelect"
                  @toggle="timelineCollapsed = !timelineCollapsed"
                  @buckets="onBuckets"
                />

                <div v-if="!flows.length && !loading" class="list-empty">
                  <template v-if="winStart">
                    <div class="le-icon">⏱️</div>
                    <div>选定时间范围内暂无流</div>
                    <n-button size="tiny" quaternary @click="clearWindow">清除时间筛选</n-button>
                  </template>
                  <template v-else-if="!tags.length">
                    <div class="le-icon">🏷️</div>
                    <div>还没有任何标签</div>
                    <div class="le-sub">回到主窗口，选中流量后点顶栏「标记」按钮，即可归档到此处复盘</div>
                    <n-button
                      v-if="selectedTag === 'all' && scope === 'archived' && totalFlows > 0"
                      size="small"
                      type="primary"
                      ghost
                      style="margin-top: 6px"
                      @click="scope = 'all'; onScopeChange('all')"
                    >
                      查看全部数据（{{ totalFlows }} 条自动保存流量）
                    </n-button>
                  </template>
                  <template v-else-if="selectedTag === 'all' && scope === 'archived' && totalFlows > 0">
                    <div class="le-icon">📭</div>
                    <div>暂无归档数据</div>
                    <div class="le-sub">库内存在自动保存但未打标的流量，可切换「全部数据」查看</div>
                    <n-button size="small" type="primary" ghost style="margin-top: 6px" @click="scope = 'all'; onScopeChange('all')">
                      切换到全部数据
                    </n-button>
                  </template>
                  <template v-else>
                    <div class="le-icon">📭</div>
                    <div>{{ emptyText }}</div>
                  </template>
                </div>

                <div v-else class="flow-list" @scroll="onScroll">
                  <div
                    v-for="f in filteredFlows"
                    :key="f.ID"
                    class="flow-row"
                    :class="{ active: f.ID === selectedFlow }"
                    @click="onSelectFlow(f.ID)"
                  >
                    <span class="fr-method" :class="'m-' + f.Method">{{ f.Method }}</span>
                    <span class="fr-status" :class="statusCls(f.Status, f.State)">{{ f.Status || '·' }}</span>
                    <span class="fr-host" :title="f.Host">{{ f.Host }}</span>
                    <span class="fr-path" :title="f.Path">{{ f.Path }}</span>
                    <span class="fr-time">{{ fmtTime(f.StartedAt) }}</span>
                    <span class="fr-size">{{ fmtBytes(f.BytesDown) }}</span>
                  </div>
                  <div v-if="hasMore" class="load-more" @click="loadMore">
                    {{ loadingMore ? '加载中…' : `加载更多（已显示 ${filteredFlows.length} / ${total}）` }}
                  </div>
                  <div v-else-if="filteredFlows.length" class="list-end">— 已全部加载 —</div>
                </div>
              </div>

              <div
                class="splitter"
                :class="{ dragging: dragPane === 'list' }"
                title="拖拽调整宽度"
                @pointerdown="startDrag($event, 'list')"
              ></div>

              <div class="detail-pane">
                <review-detail :api="api" :flow-id="selectedFlow" @error="onDetailError" />
              </div>
            </div>
          </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { NButton, NDatePicker, NInput, NPopconfirm, NPopover, NRadioButton, NRadioGroup, NTag, NTooltip, useMessage } from 'naive-ui'
import ReviewSidebar from './ReviewSidebar.vue'
import ReviewDetail from './ReviewDetail.vue'
import ReviewTimeline from './ReviewTimeline.vue'
import { ApiError, createApi, type ReviewApi } from './api'
import type { ReviewFlowMeta, ReviewHistogram, ReviewScope, ReviewTagInfo } from '../lib/types'
import { fmtBytes, fmtDateTime, fmtTime } from '../lib/format'

const { api } = createApi() as { api: ReviewApi; token: string }
const message = useMessage()

const tags = ref<ReviewTagInfo[]>([])
const totalTagged = ref(0) // /api/v1/tags 根级 total（CountTaggedFlows 去重口径，禁止 count 累加）
const totalFlows = ref(0) // 根级 totalFlows（CountAllFlows）
const selectedTag = ref('all')
const scope = ref<ReviewScope>('archived')
const flows = ref<ReviewFlowMeta[]>([])
const total = ref(0)
const selectedFlow = ref('')
const keyword = ref('')
const loading = ref(false)
const loadingMore = ref(false)
const fatal = ref<{ title: string; sub: string } | null>(null)

// 敏感凭据黄条：关闭后写 localStorage（'1'），下次打开不再展示（隐私模式读取失败则照常显示）
const WARN_DISMISS_KEY = 'prismproxy:review-warn-dismissed-v1'
function warnDismissed(): boolean {
  try {
    return localStorage.getItem(WARN_DISMISS_KEY) === '1'
  } catch {
    return false
  }
}
const showWarn = ref(!warnDismissed())
// 直接关闭：仅本次隐藏，不写 localStorage（刷新/下次打开仍提示）
function closeWarnOnce() {
  showWarn.value = false
}
function dismissWarn() {
  showWarn.value = false
  try {
    localStorage.setItem(WARN_DISMISS_KEY, '1')
  } catch {
    // 隐私模式等写入失败：仅本次会话隐藏，下次仍提示
  }
}

// 时间窗口（unix 毫秒，含头尾；0=不限）与密度直方图
const winStart = ref(0)
const winEnd = ref(0)
const histogram = ref<ReviewHistogram>({ start: 0, end: 0, buckets: [] })
const timelineCollapsed = ref(false)

// 工具栏时间图标弹层：draft 为弹层内编辑值（null=空，即全局时间筛选默认空=不限），
// 应用后才写入 winStart/winEnd（与时间轴 brush 共用同一窗口状态）
const timePopShow = ref(false)
const timeDraft = ref<[number, number] | null>(null)

// 三栏宽度（px；listW 挂载后按 46% 初始化为像素值，拖拽期间宽度固定）
const bodyEl = ref<HTMLElement | null>(null)
const sidebarW = ref(220)
const listW = ref(0)
const SIDEBAR_MIN = 150
const SIDEBAR_MAX_RATIO = 0.45
const LIST_MIN = 320
const DETAIL_MIN = 360
const SPLITTER_W = 6

// 拖拽状态（window 级指针监听 + setPointerCapture，保证快速移出分隔条不丢事件）
const dragPane = ref<null | 'sidebar' | 'list'>(null)
let dragStartX = 0
let dragStartW = 0
let dragging = false

function clamp(v: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, v))
}

function startDrag(e: PointerEvent, pane: 'sidebar' | 'list') {
  const body = bodyEl.value
  if (!body) return
  if (listW.value <= 0) {
    listW.value = Math.round((body.clientWidth - sidebarW.value - SPLITTER_W * 2) * 0.46)
  }
  dragPane.value = pane
  dragging = true
  dragStartX = e.clientX
  dragStartW = pane === 'sidebar' ? sidebarW.value : listW.value
  ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
  document.body.classList.add('col-resizing')
  window.addEventListener('pointermove', onDragMove)
  window.addEventListener('pointerup', onDragEnd)
}

function onDragMove(e: PointerEvent) {
  if (!dragging || !dragPane.value) return
  const body = bodyEl.value
  if (!body) return
  const dx = e.clientX - dragStartX
  if (dragPane.value === 'sidebar') {
    const max = body.clientWidth - SPLITTER_W * 2 - LIST_MIN - DETAIL_MIN
    sidebarW.value = clamp(dragStartW + dx, SIDEBAR_MIN, Math.min(max, body.clientWidth * SIDEBAR_MAX_RATIO))
  } else {
    // 右区（list+detail）剩余宽度
    const rightW = body.clientWidth - sidebarW.value - SPLITTER_W * 2
    listW.value = clamp(dragStartW + dx, LIST_MIN, Math.max(LIST_MIN, rightW - DETAIL_MIN))
  }
}

function onDragEnd(e: PointerEvent) {
  if (!dragging) return
  dragging = false
  dragPane.value = null
  document.body.classList.remove('col-resizing')
  window.removeEventListener('pointermove', onDragMove)
  window.removeEventListener('pointerup', onDragEnd)
  if (e.target instanceof HTMLElement && e.target.hasPointerCapture(e.pointerId)) {
    e.target.releasePointerCapture(e.pointerId)
  }
}

let bucketCount = 120
let bucketsReady = false // ReviewTimeline 首次上报容器自适应桶数前不发 histogram 请求

const PAGE = 200
let loadedAll = false
let flowSeq = 0 // 列表请求代际序号（L3：快速切标签时丢弃过期响应）
let histSeq = 0 // 直方图请求代际序号（v3.1：底图随 tagID+scope 重拉，丢弃过期响应）

const totalCount = computed(() => totalTagged.value)
const currentTagName = computed(() =>
  selectedTag.value === 'all'
    ? scope.value === 'all'
      ? '全部数据'
      : '全部已标记'
    : (tags.value.find((t) => t.id === selectedTag.value)?.name ?? '标签'),
)
const rangeText = computed(() => (scope.value === 'all' && selectedTag.value === 'all' ? `全部 ${totalFlows.value}` : `归档 ${totalTagged.value}`))
const emptyText = computed(() =>
  selectedTag.value === 'all'
    ? scope.value === 'all'
      ? '库内暂无任何流量'
      : '暂无归档数据'
    : '该标签下暂无流',
)

const filteredFlows = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return flows.value
  return flows.value.filter(
    (f) =>
      f.Host.toLowerCase().includes(kw) ||
      f.Path.toLowerCase().includes(kw) ||
      f.Method.toLowerCase().includes(kw) ||
      (f.Tags ?? []).some((t) => t.toLowerCase().includes(kw)),
  )
})

const hasMore = computed(() => !loadedAll && flows.value.length < total.value)

function statusCls(status: number, state: string): string {
  if (state === 'error' || status === 0) return 's-err'
  if (status >= 500) return 's-5xx'
  if (status >= 400) return 's-4xx'
  if (status >= 300) return 's-3xx'
  return 's-2xx'
}

async function loadTags() {
  const ov = await api.listTags()
  tags.value = ov.tags
  totalTagged.value = ov.total
  totalFlows.value = ov.totalFlows
}

// 当前查询实际 scope：具体标签下归档语义恒定（§九-11，服务端宽容忽略 scope=all）
function effectiveScope(): ReviewScope {
  return selectedTag.value === 'all' ? scope.value : 'archived'
}

async function loadFlows(reset = true) {
  // 请求代际防护（M12 审计修复 L3）：快速切换标签/刷新时，旧请求若晚于新请求
  // 返回，不得覆盖新标签的列表/总数，过期响应（含其 catch 的 fatal）整体丢弃。
  const my = ++flowSeq
  if (reset) {
    flows.value = []
    total.value = 0
    loadedAll = false
    selectedFlow.value = ''
  }
  loading.value = reset
  loadingMore.value = !reset
  try {
    const offset = reset ? 0 : flows.value.length
    const resp = await api.listFlows(
      selectedTag.value,
      effectiveScope(),
      winStart.value,
      winEnd.value,
      PAGE,
      offset,
    )
    if (my !== flowSeq) return // 已被更新的请求取代
    flows.value = reset ? resp.flows : [...flows.value, ...resp.flows]
    total.value = resp.total
    if (resp.flows.length < PAGE || flows.value.length >= resp.total) loadedAll = true
  } catch (e) {
    if (my !== flowSeq) return // 过期错误不弹 fatal
    handleFatal(e)
  } finally {
    // 仅最新请求负责复位加载态（过期请求不能清掉新请求的 loading）
    if (my === flowSeq) {
      loading.value = false
      loadingMore.value = false
    }
  }
}

// 直方图：随 tagID+scope 重拉全域底图（§6.4；恒 start=0/end=0，底图不随窗口变焦 §九-12）
async function loadHistogram() {
  if (!bucketsReady) return
  const my = ++histSeq
  histogram.value = { start: 0, end: 0, buckets: [] }
  try {
    const h = await api.histogram(selectedTag.value, effectiveScope(), 0, 0, bucketCount)
    if (my !== histSeq) return
    histogram.value = h
    reconcileWindow(h)
  } catch (e) {
    if (my !== histSeq) return
    // 直方图为辅助视图，失败不 fatal：提示并保持空底图
    message.error('密度时间轴加载失败：' + String((e as Error)?.message ?? e))
  }
}

// 底图范围变化后已有选区取交集，交集为空则清除窗口（§6.4 联动边界）
function reconcileWindow(h: ReviewHistogram) {
  if (!winStart.value) return
  if (!h.buckets.length || h.end <= h.start) {
    clearWindow()
    return
  }
  const s = Math.max(winStart.value, h.start)
  const e = Math.min(winEnd.value, h.end)
  if (s > e) {
    clearWindow()
    return
  }
  if (s !== winStart.value || e !== winEnd.value) {
    winStart.value = s
    winEnd.value = e
    void loadFlows(true)
  }
}

function onBuckets(n: number) {
  const first = !bucketsReady
  bucketsReady = true
  if (first || n !== bucketCount) {
    bucketCount = n
    void loadHistogram()
  }
}

// 时间轴 brush 提交：松手后一次刷新列表（拖拽中不刷新）
function onTimelineSelect(win: [number, number] | null) {
  if (!win) {
    clearWindow()
    return
  }
  winStart.value = win[0]
  winEnd.value = win[1]
  void loadFlows(true)
}

function clearWindow() {
  if (!winStart.value && !winEnd.value) return
  winStart.value = 0
  winEnd.value = 0
  void loadFlows(true)
}

// 时间图标弹层：打开时把当前窗口（含时间轴选区）同步进 draft；默认空
function onTimePop(show: boolean) {
  timePopShow.value = show
  if (show) {
    timeDraft.value = winStart.value ? [winStart.value, winEnd.value || winStart.value] : null
  }
}

function onTimeApply() {
  if (!timeDraft.value) return
  winStart.value = timeDraft.value[0]
  winEnd.value = timeDraft.value[1]
  timePopShow.value = false
  void loadFlows(true)
}

function onTimeClear() {
  timeDraft.value = null
  timePopShow.value = false
  clearWindow()
}

// 快捷时间：一键应用并关闭弹层（近 X 相对当前时刻；今天=本地 00:00 至今）
function onQuick(kind: '1m' | '10m' | '1h' | 'today') {
  const end = Date.now()
  let start: number
  if (kind === '1m') start = end - 60_000
  else if (kind === '10m') start = end - 10 * 60_000
  else if (kind === '1h') start = end - 3_600_000
  else {
    const d = new Date()
    d.setHours(0, 0, 0, 0)
    start = d.getTime()
  }
  timeDraft.value = [start, end]
  winStart.value = start
  winEnd.value = end
  timePopShow.value = false
  void loadFlows(true)
}

// 范围切换：列表 + 时间轴一并重拉（窗口在同库范围内保留，由选区交集钳制）
function onScopeChange(v: ReviewScope) {
  if (v === scope.value) return
  scope.value = v
  void loadFlows(true)
  void loadHistogram()
}

function loadMore() {
  if (hasMore.value && !loadingMore.value) void loadFlows(false)
}

function onScroll(e: Event) {
  const el = e.target as HTMLElement
  // 距底 120px 内自动加载下一页
  if (el.scrollHeight - el.scrollTop - el.clientHeight < 120) loadMore()
}

async function refreshAll() {
  fatal.value = null
  await nextTick() // fatal 重试成功后 .body 才挂载，需等 DOM 就绪再测宽
  initListW()
  loading.value = true
  try {
    await loadTags()
    await loadFlows(true)
    await loadHistogram()
  } catch (e) {
    handleFatal(e)
  } finally {
    loading.value = false
  }
}

function onSelectTag(id: string) {
  if (id === selectedTag.value) return
  selectedTag.value = id
  // 标签切换：列表 + 时间轴底图联动重拉（§6.4 数据源联动）
  void loadFlows(true)
  void loadHistogram()
}

function onSelectFlow(id: string) {
  selectedFlow.value = id
}

function onDetailError(msg: string) {
  message.error(msg, { duration: 5000, closable: true })
}

// 标签管理（侧栏回调；错误抛回侧栏弹窗内展示，成功后刷新标签与列表）
async function doRename(id: string, name: string) {
  await api.renameTag(id, name)
  message.success('标签已重命名' + (tags.value.find((t) => t.name.trim() === name.trim() && t.id !== id) ? '（已合并到同名标签）' : ''))
  await loadTags()
  await loadFlows(true)
  await loadHistogram()
}

async function doDelete(id: string, deleteFlows: boolean) {
  const resp = await api.deleteTag(id, deleteFlows)
  message.success(deleteFlows ? `标签已删除，${resp.deletedFlows} 条流数据已移除` : '标签关联已移除（流数据保留）')
  if (selectedTag.value === id) selectedTag.value = 'all'
  await loadTags()
  await loadFlows(true)
  await loadHistogram()
}

function handleFatal(e: unknown) {
  if (e instanceof ApiError) {
    if (e.kind === 'unauthorized') {
      fatal.value = { title: '未授权或登录态已失效', sub: '请回到 PrismProxy 主窗口重新点「数据复盘」打开本页（token 随应用重启变更）。' }
      return
    }
    if (e.kind === 'offline') {
      fatal.value = { title: 'PrismProxy 已退出', sub: '控制接口（127.0.0.1:9595）不可达。请保持主应用运行后重试。' }
      return
    }
  }
  message.error(String((e as Error)?.message ?? e), { duration: 6000, closable: true })
}

// 列表初始宽度按原 46% 比例换算为像素（CSS width:46% 的初始态在首次拖拽前生效）
function initListW() {
  if (listW.value > 0 || !bodyEl.value) return
  listW.value = Math.round((bodyEl.value.clientWidth - sidebarW.value - SPLITTER_W * 2) * 0.46)
}

onMounted(() => {
  initListW()
  refreshAll()
})

onBeforeUnmount(() => {
  window.removeEventListener('pointermove', onDragMove)
  window.removeEventListener('pointerup', onDragEnd)
})
</script>

<style scoped>
.review-root { height: 100%; display: flex; flex-direction: column; }
.topbar {
  height: 42px; flex: none; display: flex; align-items: center; gap: 10px;
  padding: 0 14px; border-bottom: 1px solid rgba(255,255,255,0.08);
}
.brand { display: flex; align-items: center; gap: 7px; font-size: 14px; }
.top-actions { margin-left: auto; }
.warn-bar {
  flex: none; display: flex; align-items: center; gap: 8px;
  padding: 6px 14px; font-size: 12px; color: #e8c864;
  background: rgba(232, 200, 100, 0.08); border-bottom: 1px solid rgba(232, 200, 100, 0.18);
}
.warn-x {
  flex: none; margin-left: auto; cursor: pointer; opacity: 0.55;
  transition: opacity 0.12s;
}
.warn-x:hover { opacity: 1; }
.fatal { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 6px; color: rgba(255,255,255,0.6); }
.fatal-icon { font-size: 34px; }
.fatal-title { font-size: 16px; color: rgba(255,255,255,0.85); }
.fatal-sub { font-size: 12px; color: rgba(255,255,255,0.45); max-width: 420px; text-align: center; line-height: 1.7; }
.body { flex: 1; display: flex; min-height: 0; }
.right { flex: 1; display: flex; min-width: 0; }
.splitter {
  flex: none; width: 6px; cursor: col-resize; position: relative; z-index: 2;
}
.splitter::after {
  content: ''; position: absolute; top: 0; bottom: 0; left: 2px; width: 2px;
  background: transparent; transition: background 0.12s;
}
.splitter:hover::after, .splitter.dragging::after { background: rgba(181, 126, 220, 0.65); }
:global(body.col-resizing), :global(body.col-resizing *) { cursor: col-resize !important; user-select: none; }
.list-pane { width: 46%; flex: none; display: flex; flex-direction: column; border-right: 1px solid rgba(255,255,255,0.08); min-height: 0; }
.detail-pane { flex: 1; min-width: 0; min-height: 0; }
.list-toolbar {
  flex: none; display: flex; align-items: center; gap: 10px; padding: 8px 10px;
  border-bottom: 1px solid rgba(255,255,255,0.06);
}
.list-count { font-size: 11px; color: rgba(255,255,255,0.45); white-space: nowrap; }
.time-btn {
  display: inline-flex; align-items: center; justify-content: center;
  width: 28px; height: 28px; flex: none; border-radius: 3px; cursor: pointer;
  color: rgba(255,255,255,0.55); transition: color 0.12s, background 0.12s;
}
.time-btn:hover { color: #63e2b7; background: rgba(255,255,255,0.08); }
.time-btn.active { color: #facc15; background: rgba(250,204,21,0.1); }
.time-pop { display: flex; flex-direction: column; gap: 8px; }
.time-pop-quick { display: flex; gap: 2px; flex-wrap: wrap; }
.time-pop-actions { display: flex; justify-content: flex-end; gap: 8px; }
.scope-wrap.scope-off { opacity: 0.45; cursor: not-allowed; display: inline-flex; }
.win-chip {
  display: inline-flex; align-items: center; gap: 5px;
  font-size: 10px; color: #facc15; background: rgba(250,204,21,0.1);
  border: 1px solid rgba(250,204,21,0.35); border-radius: 10px;
  padding: 1px 8px; white-space: nowrap; max-width: 300px; overflow: hidden; text-overflow: ellipsis;
}
.win-x { font-style: normal; cursor: pointer; opacity: 0.7; padding: 0 1px; }
.win-x:hover { opacity: 1; }
.flow-list { flex: 1; overflow-y: auto; }
.flow-row {
  display: grid; grid-template-columns: 52px 42px minmax(110px, 1.4fr) minmax(120px, 2fr) 78px 62px;
  gap: 8px; align-items: center; padding: 5px 10px; cursor: pointer; font-size: 12px;
  border-bottom: 1px solid rgba(255,255,255,0.04);
}
.flow-row:hover { background: rgba(255,255,255,0.04); }
.flow-row.active { background: rgba(181,126,220,0.14); }
.fr-method { font-weight: 600; font-size: 11px; }
.m-GET { color: #7ec699; }
.m-POST { color: #e8c864; }
.m-PUT { color: #82aaff; }
.m-DELETE { color: #e88080; }
.m-PATCH { color: #c0a8f0; }
.fr-status { font-size: 11px; text-align: center; border-radius: 3px; padding: 1px 0; }
.s-2xx { color: #7ec699; }
.s-3xx { color: #82aaff; }
.s-4xx { color: #e8c864; }
.s-5xx, .s-err { color: #e88080; }
.fr-host { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: rgba(255,255,255,0.82); }
.fr-path { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: rgba(255,255,255,0.55); font-family: Consolas, monospace; font-size: 11px; }
.fr-time, .fr-size { font-size: 11px; color: rgba(255,255,255,0.45); text-align: right; white-space: nowrap; }
.load-more, .list-end { padding: 10px; text-align: center; font-size: 12px; color: rgba(255,255,255,0.5); cursor: pointer; }
.load-more:hover { color: #c0a8f0; }
.list-empty { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 6px; color: rgba(255,255,255,0.4); text-align: center; padding: 20px; }
.le-icon { font-size: 30px; opacity: 0.7; }
.le-sub { font-size: 11px; color: rgba(255,255,255,0.32); max-width: 300px; line-height: 1.7; }
</style>
