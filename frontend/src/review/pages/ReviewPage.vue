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
              <!-- M12.2 小眼睛：忽略名单管理面板（移除忽略项 + 切换是否显示被忽略的流量；忽略只是查询过滤，不删除数据） -->
              <n-popover
                trigger="click"
                placement="bottom-end"
                :show="ignorePopShow"
                :show-arrow="false"
                raw
                @update:show="onIgnorePop"
              >
                <template #trigger>
                  <span
                    class="eye-wrap"
                    :class="{ on: ignorePopShow || showIgnored || ignores.length > 0 }"
                    :title="'忽略名单管理' + (ignores.length ? `（${ignores.length} 项）` : '')"
                  >
                    <svg viewBox="0 0 16 16" width="15" height="15" fill="currentColor" class="eye-ic">
                      <path d="M8 3C3.8 3 1.2 8 1.2 8S3.8 13 8 13s6.8-5 6.8-5S12.2 3 8 3zm0 8.2A3.2 3.2 0 1 1 8 4.8a3.2 3.2 0 0 1 0 6.4zm0-5A1.8 1.8 0 1 0 9.8 8 1.8 1.8 0 0 0 8 6.2z" />
                    </svg>
                    <i v-if="ignores.length" class="eye-dot">{{ ignores.length > 99 ? '99+' : ignores.length }}</i>
                  </span>
                </template>
                <div class="ignore-panel">
                  <div class="ip-head">
                    <span class="ip-title">忽略名单</span>
                    <label class="ip-switch">
                      <span class="ip-switch-label" :title="showIgnored ? '列表当前会显示被忽略的流量' : '列表当前隐藏被忽略的流量'">
                        显示被忽略流量
                      </span>
                      <n-switch size="small" :value="showIgnored" @update:value="onShowIgnoredToggle" />
                    </label>
                  </div>
                  <div v-if="ignoresLoading" class="ip-state">加载中…</div>
                  <div v-else-if="!ignores.length" class="ip-state">
                    <div class="ip-empty-ic">🚫</div>
                    <div>忽略名单为空</div>
                    <div class="ip-empty-sub">在列表的域名 / 路径 / 进程 / 方法 / 状态单元格上右键即可忽略，忽略仅隐藏数据、不会删除</div>
                  </div>
                  <div v-else class="ip-list">
                    <div v-for="it in sortedIgnores" :key="it.kind + '|' + it.value" class="ip-item">
                      <span class="ip-kind" :class="'k-' + it.kind">{{ IGNORE_LABEL[it.kind] }}</span>
                      <span class="ip-value" :title="it.value">{{ it.value }}</span>
                      <n-button
                        size="tiny"
                        quaternary
                        class="ip-del"
                        :loading="removingKey === it.kind + '|' + it.value"
                        title="移除此忽略项，数据恢复显示"
                        @click="onRemoveIgnore(it)"
                      >
                        <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor">
                          <path d="M5.5 1.5h5l.5.5V3h3.2v1.5h-1.1l-.5 9.2a1 1 0 0 1-1 .8H4.4a1 1 0 0 1-1-.8l-.5-9.2H2.3V3h5.5v-1h-2.8l.5-.5zM6 5.5v6H4.7l.3-6H6zm2.6 0v6H7.4v-6h1.2zm2.4 0l.3 6H10v-6h1zM6.5 3h3v-.5h-3V3z" />
                        </svg>
                      </n-button>
                    </div>
                  </div>
                </div>
              </n-popover>
              <!-- P4-14 AI 分析入口：打开右侧 AI 面板（默认 intent 模式） -->
              <n-button size="small" quaternary title="AI 分析：意图标注 / 解读 / 定位 / 流程" @click="onMarkIntents">
                <span style="display: inline-flex; align-items: center; gap: 4px">
                  <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor" style="color: #b57edc">
                    <path d="M8 1l1.9 4.6 4.6 1.9-4.6 1.9L8 14 6.1 9.4 1.5 7.5l4.6-1.9L8 1zm4.5 9.5l.8 1.9 1.9.8-1.9.8-.8 1.9-.8-1.9-1.9-.8 1.9-.8.8-1.9z" />
                  </svg>
                  AI 分析
                </span>
              </n-button>
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
                  <!-- P4-10 批量标注意图：有勾选时出现，点击打开 AI 面板 intent 模式 -->
                  <n-button
                    v-if="checkedIds.length"
                    size="tiny"
                    type="primary"
                    ghost
                    @click="onMarkIntents"
                  >
                    ✨ 标注意图（{{ checkedIds.length }}）
                  </n-button>
                  <span class="list-count">{{ currentTagName }} · {{ rangeText }} · 第 {{ page }} 页 · 共 {{ total }} 条</span>
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
                  <!-- 关键字无匹配：须与"本无数据"区分，避免把搜索无结果谎报成标签/库内为空 -->
                  <template v-else-if="keyword.trim()">
                    <div class="le-icon">🔍</div>
                    <div>没有匹配「{{ keyword.trim() }}」的流量</div>
                    <n-button size="tiny" quaternary @click="clearKeyword">清除关键字</n-button>
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

                <!-- M12.3：与主窗共用 FlowTable（虚拟滚动/列布局/双击复制/右键复制 URL、三 shell cURL、
                     调试重发、忽略域名/路径/进程）；排序为服务端排序，翻页后回到顶部 -->
                <flow-table
                  v-else
                  ref="tableRef"
                  :rows="flows"
                  :columns="columns"
                  :sort-key="sortKey"
                  :sort-dir="sortDir"
                  layout-key="prismproxy:review-col-layout-v1"
                  :default-widths="TABLE_DEFAULT_WIDTHS"
                  :col-mins="TABLE_COL_MINS"
                  :selected-id="selectedFlow"
                  :actions="tableActions"
                  selectable
                  v-model:checkedIds="checkedIds"
                  @select="onSelectFlow"
                  @sort="onSort"
                  @action-error="(m) => message.error(m, { duration: 6000, closable: true })"
                  @curl-omitted="onCurlOmitted"
                />

                <!-- 页码分页：上一页/下一页/页码/跳页 + 每页条数切换 -->
                <div v-if="total > 0" class="pager-bar">
                  <n-pagination
                    size="small"
                    :page="page"
                    :page-size="pageSize"
                    :item-count="total"
                    :page-sizes="PAGE_SIZES"
                    show-size-picker
                    show-quick-jumper
                    @update:page="onPageChange"
                    @update:page-size="onPageSizeChange"
                  />
                </div>
              </div>

              <div
                class="splitter"
                :class="{ dragging: dragPane === 'list' }"
                title="拖拽调整宽度"
                @pointerdown="startDrag($event, 'list')"
              ></div>

              <div class="detail-pane">
                <review-detail :api="api" :flow-id="selectedFlow" @error="onDetailError" @explain="onDetailExplain" />
              </div>
            </div>
          </div>

      <!-- P4 AI 分析面板：右侧滑入抽屉（四模式：解读/意图/定位/流程） -->
      <review-ai-panel
        v-model:show="aiPanelShow"
        :api="api"
        :mode="aiPanelMode"
        :flow-id="aiPanelFlowId"
        :flow-label="aiPanelLabel"
        :checked-ids="checkedIds"
        :view-flows="flows"
        :view-total="total"
        :tag-name="currentTagName"
        :scope="scope"
        :keyword="keyword"
        @locate="onAiLocate"
        @error="(m) => message.error(m, { duration: 6000, closable: true })"
      />

      <!-- M12.3：列表右键「调试重发」的弹窗（与详情页内按钮各自独立挂载，互不影响） -->
      <review-composer
        v-model:show="listComposerShow"
        :api="api"
        :flow-id="listComposerFlow"
        @error="(m) => message.error(m, { duration: 6000, closable: true })"
      />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { NButton, NDatePicker, NInput, NPagination, NPopconfirm, NPopover, NRadioButton, NRadioGroup, NSwitch, NTag, NTooltip, useMessage } from 'naive-ui'
import ReviewSidebar from '../components/ReviewSidebar.vue'
import ReviewDetail from '../components/ReviewDetail.vue'
import ReviewTimeline from '../components/ReviewTimeline.vue'
import ReviewComposer from '../components/ReviewComposer.vue'
import ReviewAiPanel from '../components/ReviewAiPanel.vue'
import FlowTable from '../../components/FlowTable.vue'
import type { FlowColumn, FlowTableActions } from '../../components/FlowTable.vue'
import { ApiError, createApi } from '../api'
import type { AiChatMode, ReviewApi } from '../api'
import type { ReviewFlowMeta, ReviewHistogram, ReviewIgnoreItem, ReviewIgnoreKind, ReviewScope, ReviewSortDir, ReviewSortKey, ReviewTagInfo } from '../../lib/types'
import { fmtDateTime } from '../../lib/format'
import { b64ToBytes } from '../../lib/format'
import { buildCurl } from '../../lib/curl'
import type { CurlShell } from '../../lib/curl'

// 独立浏览器入口（review.html）走 createApi（Http/Demo）；主窗内嵌时由 App.vue 注入 WailsReviewApi
const props = defineProps<{ api?: ReviewApi }>()
const { api } = props.api ? { api: props.api } : createApi()
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
// P4 复盘 AI：列表勾选集合（批量标注意图候选）；作废规则见 loadFlows(reset) / onPageChange
const checkedIds = ref<string[]>([])
// AI 面板显隐与初始模式（P4-14 挂载 ReviewAiPanel）：各入口设置后打开
const aiPanelShow = ref(false)
const aiPanelMode = ref<AiChatMode>('intent')
// 标注意图入口：打开面板并预选 intent 模式（候选=勾选集，无勾选=当前已加载页，由面板内说明）
function onMarkIntents(): void {
  aiPanelMode.value = 'intent'
  aiPanelShow.value = true
}
// AI 面板 explain 目标流与展示名（详情页「AI 解读」入口设置）
const aiPanelFlowId = ref('')
const aiPanelLabel = computed(() => {
  const f = flows.value.find((x) => x.ID === aiPanelFlowId.value)
  return f ? f.Method + ' ' + (f.Path || f.URL) : aiPanelFlowId.value
})
function onDetailExplain(): void {
  aiPanelFlowId.value = selectedFlow.value
  aiPanelMode.value = 'explain'
  aiPanelShow.value = true
}
// 面板「查看 →」跳转：目标在当前已加载列表内则选中，否则提示切分页（设计 §7.1）
function onAiLocate(flowId: string): void {
  if (flows.value.some((f) => f.ID === flowId)) {
    selectedFlow.value = flowId
  } else {
    message.info('该流不在当前列表（可能被筛选或翻页），请切换到该流所在分页后重试', { duration: 5000, closable: true })
  }
}
// 页码分页（1 起）；页大小可选 50/100/200/500，默认 100
const page = ref(1)
const pageSize = ref(100)
const PAGE_SIZES = [50, 100, 200, 500]
let kwTimer: ReturnType<typeof setTimeout> | null = null
const fatal = ref<{ title: string; sub: string } | null>(null)

// M12.3 共享表格列定义（cls 必须是 c-host/c-path/c-proc/c-method/c-status，FlowTable 据此识别右键忽略列）；
// 复盘列序与原自绘 7 列一致：方法/状态/域名/路径/时间/大小/进程
function statusCls(f: ReviewFlowMeta): string {
  if (f.State === 'error' || f.Status === 0) return 's-err'
  if (f.Status >= 500) return 's-5xx'
  if (f.Status >= 400) return 's-4xx'
  if (f.Status >= 300) return 's-3xx'
  return 's-2xx'
}

const columns: FlowColumn[] = [
  { key: 'method', label: '方法', cls: 'c-method', wi: 1, cellCls: (f) => 'm-' + f.Method },
  { key: 'status', label: '状态', cls: 'c-status', wi: 2, cellCls: statusCls, text: (f) => (f.Status ? String(f.Status) : '·') },
  { key: 'host', label: '域名', cls: 'c-host', wi: 3, title: (f) => f.Host },
  { key: 'path', label: '路径', cls: 'c-path', wi: 4, title: (f) => f.Path || f.URL },
  { key: 'time', label: '时间', cls: 'c-time', wi: 5 },
  { key: 'size', label: '大小', cls: 'c-size', wi: 6 },
  {
    key: 'proc',
    label: '进程',
    cls: 'c-proc',
    wi: 7,
    text: (f) => f.ProcessName || '·',
    title: (f) => (f.ProcessName ? f.ProcessName + (f.PID ? ' (' + f.PID + ')' : '') : ''),
  },
]
// 状态点列（22px 固定）+ 7 数据列；路径列 1fr 弹性
const TABLE_DEFAULT_WIDTHS: (number | null)[] = [22, 56, 50, null, null, 86, 70, null]
const TABLE_COL_MINS = [22, 44, 40, 80, 100, 60, 48, 70]

// 默认时间倒序（最新在顶部），与 FlowList 及服务端 flowOrderBy 默认一致
const sortKey = ref<ReviewSortKey>('time')
const sortDir = ref<ReviewSortDir>('desc')

// 共享表格实例（翻页/重拉后回到顶部）
const tableRef = ref<InstanceType<typeof FlowTable> | null>(null)

// 小眼睛：是否显示被忽略的流量（默认隐藏）；选择写 localStorage 跨会话保留
const SHOW_IGNORED_KEY = 'prismproxy:review-show-ignored-v1'
function loadShowIgnored(): boolean {
  try {
    return localStorage.getItem(SHOW_IGNORED_KEY) === '1'
  } catch {
    return false
  }
}
const showIgnored = ref(loadShowIgnored())
function persistShowIgnored() {
  try {
    localStorage.setItem(SHOW_IGNORED_KEY, showIgnored.value ? '1' : '0')
  } catch {
    // 存储不可用时仅本会话生效
  }
}

// 忽略名单（小眼睛管理面板）：项目级规则，与标签/scope 无关；忽略只是查询过滤，不删数据
const ignores = ref<ReviewIgnoreItem[]>([])
const ignoresLoading = ref(false)
const ignorePopShow = ref(false)
const removingKey = ref('')

// 面板列表排序：域名 → 路径 → 进程 → 方法 → 状态，组内按值排序（ignore createdAt 为归一化入库时刻，展示意义不大）
const sortedIgnores = computed(() => {
  const order: ReviewIgnoreKind[] = ['host', 'path', 'proc', 'method', 'status']
  return [...ignores.value].sort((a, b) => {
    const d = order.indexOf(a.kind) - order.indexOf(b.kind)
    return d !== 0 ? d : a.value.localeCompare(b.value)
  })
})

// 加载忽略名单：失败不致命（辅助管理能力），提示后保留旧列表
async function loadIgnores() {
  ignoresLoading.value = true
  try {
    ignores.value = await api.listIgnores()
  } catch (e) {
    message.error('忽略名单加载失败：' + String((e as Error)?.message ?? e), { duration: 5000, closable: true })
  } finally {
    ignoresLoading.value = false
  }
}

// 面板开合：每次打开刷新名单（外部窗口可能新增过忽略）
function onIgnorePop(show: boolean) {
  ignorePopShow.value = show
  if (show) void loadIgnores()
}

// 面板内开关：切换是否显示被忽略的流量；普通失败回滚开关与 localStorage
async function onShowIgnoredToggle(v: boolean) {
  const prev = showIgnored.value
  showIgnored.value = v
  persistShowIgnored()
  const ok = await loadFlows(true, true)
  if (!ok && !fatal.value) {
    showIgnored.value = prev
    persistShowIgnored()
  }
}

// 移除单条忽略项：删除规则（不删流量），隐藏态下列表需重算恢复显示
async function onRemoveIgnore(it: ReviewIgnoreItem) {
  const key = it.kind + '|' + it.value
  if (removingKey.value) return
  removingKey.value = key
  try {
    await api.removeIgnore(it.kind, it.value)
    ignores.value = ignores.value.filter((x) => !(x.kind === it.kind && x.value === it.value))
    message.success(`已移除忽略${IGNORE_LABEL[it.kind]}，相关流量恢复显示`)
    if (!showIgnored.value) await loadFlows(true, true)
  } catch (e) {
    message.error(String((e as Error)?.message ?? e), { duration: 6000, closable: true })
  } finally {
    removingKey.value = ''
  }
}

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

// 按当前页码/页大小/关键字单页拉取；reset=true 表示筛选条件变化（回到第 1 页），
// 此时清空旧列表与选中项。restoreOnError 仅用于「关键字/页大小」这类筛选维度本身未变、
// 仅刷新结果的 reset：普通失败时恢复旧列表；切标签/scope/时间窗等不恢复（否则会显示
// 与当前选中维度不符的跨筛选旧数据）。请求代际防护（M12 审计修复 L3）：丢弃过期响应。
// 返回本次请求是否成功（过期请求恒视为不成功，供调用方决定是否回滚 UI 状态）。
async function loadFlows(reset = false, restoreOnError = false): Promise<boolean> {
  const my = ++flowSeq
  // restoreOnError 失败时回滚用的快照
  const prevFlows = flows.value
  const prevTotal = total.value
  if (reset) {
    // 筛选条件变化（标签/scope/时间窗/关键字/页大小/排序）一律回到第 1 页
    page.value = 1
    flows.value = []
    total.value = 0
    selectedFlow.value = ''
    checkedIds.value = [] // P4-10 作废规则：筛选/列表重置后勾选集合失效
  }
  loading.value = true
  try {
    const limit = pageSize.value
    const offset = (page.value - 1) * pageSize.value
    const resp = await api.listFlows(
      selectedTag.value,
      effectiveScope(),
      winStart.value,
      winEnd.value,
      limit,
      offset,
      keyword.value.trim(),
      { sort: sortKey.value, dir: sortDir.value, showIgnored: showIgnored.value },
    )
    if (my !== flowSeq) return false // 已被更新的请求取代
    flows.value = resp.flows
    total.value = resp.total
    return true
  } catch (e) {
    if (my !== flowSeq) return false // 过期错误不弹 fatal、不回滚
    handleFatal(e)
    // 普通失败（非未授权/离线致命遮罩）且调用方要求时：恢复清空前的列表与总数，
    // 避免关键字/页大小刷新失败导致空态或分页栏消失
    if (restoreOnError && !fatal.value) {
      flows.value = prevFlows
      total.value = prevTotal
    }
    return false
  } finally {
    // 仅最新请求负责复位加载态（过期请求不能清掉新请求的 loading）
    if (my === flowSeq) loading.value = false
  }
}

// 翻页：保持筛选条件，仅替换列表（不清屏，旧行保留到新响应返回）；失败回滚到原页码
async function onPageChange(p: number) {
  const prev = page.value
  page.value = p
  selectedFlow.value = ''
  checkedIds.value = [] // P4-10 作废规则：翻页后勾选集合失效
  const ok = await loadFlows()
  if (!ok && !fatal.value) {
    page.value = prev
  } else {
    tableRef.value?.scrollToTop()
  }
}

// 每页条数变化：回到第 1 页；普通失败恢复原页大小（loadFlows 已恢复旧列表/总数）
async function onPageSizeChange(size: number) {
  const prev = pageSize.value
  pageSize.value = size
  const ok = await loadFlows(true, true)
  if (!ok && !fatal.value) pageSize.value = prev
}

// 关键字输入防抖约 300ms 下沉服务端搜索（method/host/path 子串），并回到第 1 页
watch(keyword, () => {
  if (kwTimer) clearTimeout(kwTimer)
  kwTimer = setTimeout(() => void loadFlows(true, true), 300)
})

// 空态「清除关键字」：清空后由 keyword watcher 统一防抖重拉（回第 1 页），
// 此处取消挂起定时器避免与即将触发的 watcher 重复请求
function clearKeyword() {
  if (kwTimer) clearTimeout(kwTimer)
  keyword.value = ''
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

async function refreshAll() {
  fatal.value = null
  await nextTick() // fatal 重试成功后 .body 才挂载，需等 DOM 就绪再测宽
  initListW()
  loading.value = true
  try {
    // 忽略名单为辅助能力，加载失败不阻断主流程（loadIgnores 内部自行提示）
    await Promise.all([loadTags(), loadIgnores()])
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

// M12.2 表头排序（FlowTable 上抛列 key）：首次点某列，时间默认降序、其余升序；再点同列切换方向
function onSort(key: string) {
  const k = key as ReviewSortKey
  if (sortKey.value !== k) {
    sortKey.value = k
    sortDir.value = k === 'time' ? 'desc' : 'asc'
  } else {
    sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
  }
  void loadFlows(true, true)
}

const IGNORE_LABEL: Record<ReviewIgnoreKind, string> = {
  host: '域名',
  path: '路径',
  proc: '进程',
  method: '方法',
  status: '状态',
}

function onCurlOmitted() {
  message.info('请求体为二进制或超过 64KB，未内联到 cURL（请手动补充）', { duration: 5000, closable: true })
}

// 列表右键「调试重发」：选中流并打开页面级 ReviewComposer（预填走归档 detail/body）
const listComposerShow = ref(false)
const listComposerFlow = ref('')
function openListComposer(f: ReviewFlowMeta) {
  onSelectFlow(f.ID)
  listComposerFlow.value = f.ID
  listComposerShow.value = true
}

// 注入共享表格的右键动作（无 pin：复盘列表不置顶）
const tableActions: FlowTableActions = {
  // 归档流不在实时内存，前端用归档详情 + 请求体重拼 cURL（lib/curl.ts 与 Go curl.go 转义逐字对齐）
  buildCurl: async (f, shell: CurlShell) => {
    const d = await api.flowDetail(f.ID)
    const bp = await api.flowBody(f.ID, 'req')
    // bp.Body 为已解压 base64 字符串（无 Body 时回落 Raw 原文）；为空给零字节
    const b64 = bp.Body || bp.Raw || ''
    const body = b64 ? b64ToBytes(b64) : new Uint8Array(0)
    return buildCurl(
      {
        method: d.Method,
        url: d.ReqURL || d.URL,
        headers: d.ReqHeader || {},
        body,
        bodyTruncated: !!bp.Truncated,
      },
      shell,
    )
  },
  ignore: async (f, kind) => {
    const value =
      kind === 'host' ? f.Host
      : kind === 'path' ? f.Path
      : kind === 'proc' ? f.ProcessName
      : kind === 'method' ? f.Method
      : f.Status ? String(f.Status) : ''
    if (!value) return
    const r = await api.addIgnore(kind, value)
    if (r.added) {
      // 同步本地名单（面板即使未打开，角标计数也保持最新）
      if (!ignores.value.some((x) => x.kind === r.ignore.kind && x.value === r.ignore.value)) {
        ignores.value = [...ignores.value, r.ignore]
      }
      message.success(`已忽略${IGNORE_LABEL[kind]} ${r.ignore.value}（仅隐藏数据，不删除；点顶栏小眼睛可管理或移除）`, { duration: 4000, closable: true })
    } else {
      message.info(`该${IGNORE_LABEL[kind]}已在忽略名单中`, { duration: 3000, closable: true })
    }
    // 当前处于「显示忽略」时无需重拉（新忽略项仍可见）；隐藏态下列表与总数需重算
    if (!showIgnored.value) await loadFlows(true, true)
  },
  compose: openListComposer,
  // 复盘忽略名单支持全部五个维度（方法/状态为复盘专属，实时引擎快捷忽略无此两类）
  ignoreKinds: ['host', 'path', 'proc', 'method', 'status'] as FlowIgnoreKind[],
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
  if (kwTimer) clearTimeout(kwTimer)
  window.removeEventListener('pointermove', onDragMove)
  window.removeEventListener('pointerup', onDragEnd)
})
</script>

<style scoped>
/* position:relative：AI 抽屉（.ai-drawer absolute）的定位锚点——主窗内嵌时抽屉只覆盖复盘页区域 */
.review-root { position: relative; height: 100%; display: flex; flex-direction: column; }
.topbar {
  height: 42px; flex: none; display: flex; align-items: center; gap: 10px;
  padding: 0 14px; border-bottom: 1px solid rgba(255,255,255,0.08);
}
.brand { display: flex; align-items: center; gap: 7px; font-size: 14px; }
.top-actions { margin-left: auto; display: flex; align-items: center; gap: 4px; }
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
/* M12.3：共享 FlowTable 填充 list-pane 剩余高度（其内部虚拟列表 flex:1 滚动） */
.flow-table { flex: 1; min-height: 0; }
/* 小眼睛：忽略名单入口（有规则/面板打开/显示忽略态时高亮；角标显示规则数） */
.eye-wrap {
  position: relative; display: inline-flex; align-items: center; justify-content: center;
  width: 26px; height: 26px; border-radius: 4px; cursor: pointer;
  color: rgba(255,255,255,0.55); transition: color 0.12s, background 0.12s;
}
.eye-wrap:hover { background: rgba(255,255,255,0.08); color: rgba(255,255,255,0.9); }
.eye-wrap.on { color: #c99bf0; }
.eye-ic { display: block; }
.eye-dot {
  position: absolute; top: -3px; right: -5px; min-width: 14px; height: 14px;
  padding: 0 3px; border-radius: 7px; box-sizing: border-box;
  background: #b57edc; color: #1a1220; font-size: 9px; font-style: normal;
  font-weight: 700; line-height: 14px; text-align: center; white-space: nowrap;
}
/* 忽略名单管理面板（n-popover raw，自绘暗色卡片） */
.ignore-panel {
  width: 320px; max-height: 60vh; display: flex; flex-direction: column;
  background: #1b1b20; border: 1px solid rgba(255,255,255,0.12); border-radius: 8px;
  box-shadow: 0 8px 28px rgba(0,0,0,0.5); overflow: hidden;
}
.ip-head {
  flex: none; display: flex; align-items: center; justify-content: space-between;
  gap: 10px; padding: 10px 12px; border-bottom: 1px solid rgba(255,255,255,0.08);
}
.ip-title { font-size: 13px; font-weight: 600; color: rgba(255,255,255,0.88); }
.ip-switch { display: inline-flex; align-items: center; gap: 8px; cursor: pointer; }
.ip-switch-label { font-size: 11px; color: rgba(255,255,255,0.55); white-space: nowrap; }
.ip-list { flex: 1; overflow-y: auto; padding: 4px 0; }
.ip-item {
  display: flex; align-items: center; gap: 8px; padding: 5px 12px 5px 10px;
}
.ip-item:hover { background: rgba(255,255,255,0.05); }
.ip-kind {
  flex: none; width: 34px; text-align: center; font-size: 10px; line-height: 18px;
  border-radius: 3px; color: rgba(255,255,255,0.85);
}
.ip-kind.k-host { background: rgba(126,198,153,0.16); color: #7ec699; }
.ip-kind.k-path { background: rgba(130,170,255,0.16); color: #82aaff; }
.ip-kind.k-proc { background: rgba(192,168,240,0.18); color: #c0a8f0; }
.ip-kind.k-method { background: rgba(112,192,232,0.16); color: #70c0e8; }
.ip-kind.k-status { background: rgba(229,192,123,0.16); color: #e5c07b; }
.ip-value {
  flex: 1; min-width: 0; font-size: 11px; font-family: Consolas, monospace;
  color: rgba(255,255,255,0.75); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.ip-del { flex: none; color: rgba(255,255,255,0.45); }
.ip-del:hover { color: #e88080; }
.ip-state {
  flex: 1; overflow-y: auto; padding: 22px 18px; text-align: center;
  font-size: 12px; color: rgba(255,255,255,0.5); display: flex; flex-direction: column;
  align-items: center; gap: 6px;
}
.ip-empty-ic { font-size: 24px; opacity: 0.8; }
.ip-empty-sub { font-size: 11px; color: rgba(255,255,255,0.35); line-height: 1.7; max-width: 240px; }
.pager-bar {
  flex: none; display: flex; justify-content: center; align-items: center;
  padding: 6px 8px; border-top: 1px solid rgba(255,255,255,0.06);
}
.list-empty { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 6px; color: rgba(255,255,255,0.4); text-align: center; padding: 20px; }
.le-icon { font-size: 30px; opacity: 0.7; }
.le-sub { font-size: 11px; color: rgba(255,255,255,0.32); max-width: 300px; line-height: 1.7; }
</style>
