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

          <!-- 敏感凭据提示（设计 §6.3 固有暴露面明示） -->
          <div class="warn-bar">
            <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor" style="flex: none">
              <path d="M8 1.2L15.6 14H.4L8 1.2zm0 3.3L3.7 12.5h8.6L8 4.5zm-.75 2.5h1.5v3h-1.5v-3zm0 3.8h1.5v1.3h-1.5v-1.3z" />
            </svg>
            <span>归档内容可能包含敏感凭据（Token / Cookie / 密码等），请勿在共享屏幕、录屏或不可信浏览器扩展环境打开本页。</span>
          </div>

          <!-- 致命错误态：未授权 / 应用退出 -->
          <div v-if="fatal" class="fatal">
            <div class="fatal-icon">🔌</div>
            <div class="fatal-title">{{ fatal.title }}</div>
            <div class="fatal-sub">{{ fatal.sub }}</div>
            <n-button size="small" style="margin-top: 12px" @click="refreshAll">重试</n-button>
          </div>

          <div v-else class="body">
            <review-sidebar
              :tags="tags"
              :selected="selectedTag"
              :total-count="totalCount"
              :loading="loading"
              :do-rename="doRename"
              :do-delete="doDelete"
              @select="onSelectTag"
              @refresh="refreshAll"
            />

            <div class="right">
              <div class="list-pane">
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
                  <span class="list-count">{{ currentTagName }} · {{ filteredFlows.length }} / {{ total }} 条</span>
                </div>

                <div v-if="!flows.length && !loading" class="list-empty">
                  <template v-if="!tags.length">
                    <div class="le-icon">🏷️</div>
                    <div>还没有任何标签</div>
                    <div class="le-sub">回到主窗口，选中流量后点顶栏「标记」按钮，即可归档到此处复盘</div>
                  </template>
                  <template v-else>
                    <div class="le-icon">📭</div>
                    <div>该标签下暂无流</div>
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

              <div class="detail-pane">
                <review-detail :api="api" :flow-id="selectedFlow" @error="onDetailError" />
              </div>
            </div>
          </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NButton, NInput, NTag, useMessage } from 'naive-ui'
import ReviewSidebar from './ReviewSidebar.vue'
import ReviewDetail from './ReviewDetail.vue'
import { ApiError, createApi, type ReviewApi } from './api'
import type { ReviewFlowMeta, ReviewTagInfo } from '../lib/types'
import { fmtBytes, fmtTime } from '../lib/format'

const { api } = createApi() as { api: ReviewApi; token: string }
const message = useMessage()

const tags = ref<ReviewTagInfo[]>([])
const selectedTag = ref('all')
const flows = ref<ReviewFlowMeta[]>([])
const total = ref(0)
const selectedFlow = ref('')
const keyword = ref('')
const loading = ref(false)
const loadingMore = ref(false)
const fatal = ref<{ title: string; sub: string } | null>(null)

const PAGE = 200
let loadedAll = false
let flowSeq = 0 // 列表请求代际序号（L3：快速切标签时丢弃过期响应）

const totalCount = computed(() => tags.value.reduce((s, t) => s + t.count, 0))
const currentTagName = computed(() =>
  selectedTag.value === 'all' ? '全部已标记' : (tags.value.find((t) => t.id === selectedTag.value)?.name ?? '标签'),
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
  tags.value = await api.listTags()
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
    const resp = await api.listFlows(selectedTag.value, PAGE, offset)
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
  loading.value = true
  try {
    await loadTags()
    await loadFlows(true)
  } catch (e) {
    handleFatal(e)
  } finally {
    loading.value = false
  }
}

function onSelectTag(id: string) {
  if (id === selectedTag.value) return
  selectedTag.value = id
  void loadFlows(true)
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
}

async function doDelete(id: string, deleteFlows: boolean) {
  const resp = await api.deleteTag(id, deleteFlows)
  message.success(deleteFlows ? `标签已删除，${resp.deletedFlows} 条流数据已移除` : '标签关联已移除（流数据保留）')
  if (selectedTag.value === id) selectedTag.value = 'all'
  await loadTags()
  await loadFlows(true)
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

onMounted(refreshAll)
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
.fatal { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 6px; color: rgba(255,255,255,0.6); }
.fatal-icon { font-size: 34px; }
.fatal-title { font-size: 16px; color: rgba(255,255,255,0.85); }
.fatal-sub { font-size: 12px; color: rgba(255,255,255,0.45); max-width: 420px; text-align: center; line-height: 1.7; }
.body { flex: 1; display: flex; min-height: 0; }
.right { flex: 1; display: flex; min-width: 0; }
.list-pane { width: 46%; flex: none; display: flex; flex-direction: column; border-right: 1px solid rgba(255,255,255,0.08); min-height: 0; }
.detail-pane { flex: 1; min-width: 0; min-height: 0; }
.list-toolbar {
  flex: none; display: flex; align-items: center; gap: 10px; padding: 8px 10px;
  border-bottom: 1px solid rgba(255,255,255,0.06);
}
.list-count { font-size: 11px; color: rgba(255,255,255,0.45); white-space: nowrap; }
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
