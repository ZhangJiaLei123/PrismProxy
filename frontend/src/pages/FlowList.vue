<template>
  <!--
    主窗抓包列表：FlowTable 的薄封装（实时 store + 客户端排序 + 置顶/快捷忽略/主窗 Composer）。
    表格本体（虚拟滚动/列布局/双击复制/右键菜单）在 components/FlowTable.vue，与复盘列表共用。
  -->
  <flow-table
    :rows="sortedFlows"
    :columns="columns"
    :sort-key="sortKey"
    :sort-dir="sortDir"
    layout-key="prismproxy:flowlist-col-layout-v2"
    :default-widths="DEFAULT_WIDTHS"
    :col-mins="COL_MINS"
    :selected-id="store.selectedId ?? ''"
    :actions="actions"
    :empty-text="emptyText"
    @select="(id) => store.select(id)"
    @sort="toggleSort"
    @action-error="(m) => message.error(m, { duration: 6000, closable: true })"
    @curl-omitted="onCurlOmitted"
  >
    <!-- 域名列：置顶图标 + 历史徽标（主窗特有） -->
    <template #cell-host="{ flow: f }">
      <svg v-if="f.Pinned" class="pin-ic" viewBox="0 0 24 24" title="已置顶"><path fill="currentColor" d="M16 9V4h1c.55 0 1-.45 1-1s-.45-1-1-1H7c-.55 0-1 .45-1 1s.45 1 1 1h1v5c0 1.66-1.34 3-3 3v2h5.97v7l1 1 1-1v-7H19v-2c-1.66 0-3-1.34-3-3z"/></svg><span v-if="f.Historical" class="hist-badge" title="从本地数据库加载的历史流量">历史</span>{{ f.Host }}
    </template>
    <!-- 标签列：n-tag 小徽章（最多 2 个 + +N） -->
    <template #cell-tags="{ flow: f }">
      <template v-if="f.Tags && f.Tags.length">
        <n-tag v-for="t in f.Tags.slice(0, 2)" :key="t" size="tiny" :bordered="false" class="tag-badge">{{ t }}</n-tag>
        <span v-if="f.Tags.length > 2" class="tag-more">+{{ f.Tags.length - 2 }}</span>
      </template>
    </template>
  </flow-table>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMessage, NTag } from 'naive-ui'
import FlowTable from '../components/FlowTable.vue'
import type { FlowColumn } from '../components/FlowTable.vue'
import { useFlowsStore } from '../stores/flows'
import { AddQuickIgnore, BuildCurl, SetFlowPinned } from '../../wailsjs/go/app/App'
import type { app } from '../../wailsjs/go/models'

const store = useFlowsStore()
const message = useMessage()

type SortKey = 'time' | 'method' | 'status' | 'host' | 'path' | 'dur' | 'size' | 'proc' | 'tags'
type SortDir = 'asc' | 'desc'

// 状态着色（方法/状态）以 cellCls 形式注入，class 规则在共享组件内
function statusClass(f: app.FlowMeta): string {
  if (f.State === 'error') return 's-err'
  if (f.Status >= 500) return 's-5xx'
  if (f.Status >= 400) return 's-4xx'
  if (f.Status >= 300) return 's-3xx'
  if (f.Status >= 200) return 's-2xx'
  return ''
}

const columns: FlowColumn[] = [
  { key: 'time', label: '时间', cls: 'c-time', wi: 1 },
  { key: 'method', label: '方法', cls: 'c-method', wi: 2, cellCls: (f) => 'm-' + f.Method },
  { key: 'status', label: '状态', cls: 'c-status', wi: 3, cellCls: statusClass },
  { key: 'host', label: '域名', cls: 'c-host', wi: 4, title: (f) => f.Host },
  { key: 'path', label: '路径', cls: 'c-path', wi: 5, title: (f) => f.URL },
  { key: 'dur', label: '耗时', cls: 'c-dur', wi: 6 },
  { key: 'size', label: '大小', cls: 'c-size', wi: 7 },
  { key: 'proc', label: '进程', cls: 'c-proc', wi: 8, title: (f) => f.ProcessName + ' (' + f.PID + ')' },
  // M12：标签列（wi=9），默认可见、排在最后；老用户布局 v1→v2 迁移追加
  { key: 'tags', label: '标签', cls: 'c-tags', wi: 9, title: (f) => (f.Tags || []).join('、') },
]

// 列宽（px），首列状态点 22px 固定；null = 1fr 弹性（路径列默认）。共 9 数据列 + 状态列 = 10 元素
const DEFAULT_WIDTHS: (number | null)[] = [22, 92, 56, 56, 150, null, 66, 64, 100, 110]
const COL_MINS = [22, 52, 44, 40, 60, 60, 48, 48, 60, 70]

// ---- 列布局持久化 v1→v2 迁移（v1=8 数据列；迁移成功后由 FlowTable 落 v2 key）----
// 必须在 FlowTable setup 读 localStorage 之前跑完（父 setup 先于子 setup）
;(function migrateLayoutV1() {
  const V2 = 'prismproxy:flowlist-col-layout-v2'
  const V1 = 'prismproxy:flowlist-col-layout-v1'
  try {
    if (localStorage.getItem(V2)) return
    const old = localStorage.getItem(V1)
    if (!old) return
    const v = JSON.parse(old) as { order?: unknown; widths?: unknown }
    if (!Array.isArray(v.order) || !Array.isArray(v.widths)) return
    const rest = v.order.slice(1) as unknown[]
    const okOrder = v.order[0] === 0 && rest.every((n) => Number.isInteger(n) && n >= 1 && n <= 8) && new Set(rest).size === rest.length
    const okWidths = v.widths.length === 9 && (v.widths as unknown[]).every((w) => w === null || (typeof w === 'number' && w >= 40 && w <= 400))
    if (!okOrder || !okWidths) return
    localStorage.setItem(
      V2,
      JSON.stringify({ order: [...(v.order as number[]), 9], widths: [...(v.widths as (number | null)[]), DEFAULT_WIDTHS[9]] }),
    )
  } catch {
    // JSON 损坏等：保持默认布局
  }
})()

// ---- 客户端排序（置顶恒前；默认时间倒序；同值 StartedAt 倒序兜底） ----
const sortKey = ref<SortKey>('time')
const sortDir = ref<SortDir>('desc')

function toggleSort(key: string) {
  const k = key as SortKey
  if (sortKey.value !== k) {
    sortKey.value = k
    sortDir.value = k === 'time' ? 'desc' : 'asc'
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
  // 标签列按标签名字典序（与单元格 tags 分支口径一致）
  tags: (f) => (f.Tags || []).join('、'),
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

const emptyText = computed(() =>
  store.flows.length ? '无匹配流量 —— 调整底部过滤条件' : '暂无流量 —— 将系统代理指向 9090 端口或配置应用代理后开始抓包',
)

function onCurlOmitted() {
  message.info('请求体为二进制或超过 64KB，未内联到 cURL（请手动补充）', { duration: 5000, closable: true })
}

// ---- 右键动作（M5 快捷忽略 + 置顶 + 主窗 Composer） ----
const actions = {
  buildCurl: (f: app.FlowMeta, shell: 'cmd' | 'bash') => BuildCurl(f.ID, shell),
  pin: (f: app.FlowMeta) => SetFlowPinned(f.ID, !f.Pinned),
  // 主窗实时快捷忽略仅域名/路径/进程（FlowTable 默认 ignoreKinds 即此三类，不会上抛 method/status）
  ignore: async (f: app.FlowMeta, kind: 'host' | 'path' | 'proc') => {
    if (kind === 'host' && f.Host) {
      const added = await AddQuickIgnore('host', f.Host)
      if (added) {
        const n = store.removeIgnored('host', f.Host)
        message.success(`已忽略域名 ${f.Host}（含全部子域），已从列表清理 ${n} 条相关流量，后续不再显示`, { duration: 4000, closable: true })
      } else message.info(`域名 ${f.Host} 已在忽略列表中`, { duration: 3000, closable: true })
    } else if (kind === 'proc' && f.ProcessName) {
      const added = await AddQuickIgnore('process', f.ProcessName)
      if (added) {
        const n = store.removeIgnored('process', f.ProcessName)
        message.success(`已忽略进程 ${f.ProcessName}，已从列表清理 ${n} 条相关流量，后续不再显示`, { duration: 4000, closable: true })
      } else message.info(`进程 ${f.ProcessName} 已在忽略列表中`, { duration: 3000, closable: true })
    } else if (kind === 'path' && f.Path) {
      const added = await AddQuickIgnore('path', f.Path)
      if (added) {
        const n = store.removeIgnored('path', f.Path)
        message.success(`已忽略该路径（含下级路径，通配符 *? 可用），已从列表清理 ${n} 条相关流量，后续不再显示`, { duration: 4000, closable: true })
      } else message.info('该路径已在忽略列表中', { duration: 3000, closable: true })
    }
  },
  compose: (f: app.FlowMeta) => store.openComposer(f.ID),
}
</script>

<style scoped>
/* 具名插槽内容在父组件渲染，FlowTable 的 scoped 样式命中不了，这里仅放插槽内元素样式 */
.pin-ic { width: 11px; height: 11px; margin-right: 3px; vertical-align: -1px; color: #e5c07b; flex: none; }
.hist-badge { flex: none; margin-right: 4px; padding: 0 4px; border-radius: 3px; font-size: 10px; line-height: 16px; color: #56b6c2; background: rgba(86, 182, 194, 0.14); }
.tag-badge { max-width: 72px; }
.tag-more { flex: none; font-size: 10px; color: rgba(255, 255, 255, 0.5); }
</style>
