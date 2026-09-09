<template>
  <div class="sidebar">
    <div class="side-head">
      <span>标签</span>
      <n-button size="tiny" quaternary title="刷新标签列表" :loading="loading" @click="$emit('refresh')">
        <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor">
          <path d="M8 3a5 5 0 1 0 4.55 2.94l-1.36.63A3.5 3.5 0 1 1 11.5 8H9.2l2.8-2.8L14.8 8H12.4A5 5 0 0 0 8 3z" />
        </svg>
      </n-button>
    </div>

    <div
      class="tag-item"
      :class="{ active: selected === 'all' }"
      @click="$emit('select', 'all')"
    >
      <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor" class="ti-icon">
        <path d="M2 2.5h5.6l2 2H14v9H2V2.5zm1.5 1.5v8h11v-6h-3.7l-2-2H3.5z" />
      </svg>
      <span class="ti-name">全部已标记</span>
      <span class="ti-count">{{ totalCount }}</span>
    </div>

    <div
      v-for="t in tags"
      :key="t.id"
      class="tag-item"
      :class="{ active: selected === t.id }"
      @click="$emit('select', t.id)"
      @contextmenu.prevent="openMenu($event, t)"
    >
      <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor" class="ti-icon" style="color: #c0a8f0">
        <path d="M2 1.5h8.2L14 5.3V14.5H2V1.5zm1.5 1.5v10h9V6.1L8.9 3H3.5z" />
      </svg>
      <div class="ti-main">
        <div class="ti-name" :title="t.name">{{ t.name }}</div>
        <div class="ti-time">最近使用 {{ fmtAgo(t.lastUsedAt) }}</div>
      </div>
      <span class="ti-count">{{ t.count }}</span>
      <span class="ti-ops" @click.stop>
        <n-button size="tiny" quaternary title="重命名标签" @click="openRename(t)">
          <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor">
            <path d="M11.3 1.5l3.2 3.2L5.7 13.5l-3.6.9.9-3.6L11.3 1.5zm0 2.2L4.4 10.6l-.4 1.6 1.6-.4 6.9-6.9-1.2-1.2z" />
          </svg>
        </n-button>
        <n-button size="tiny" quaternary title="删除标签" @click="openDelete(t)">
          <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor">
            <path d="M11 3h3v1h-2v9.5A1.5 1.5 0 0 1 10.5 15h-5A1.5 1.5 0 0 1 4 13.5V4H2V3h3V1.75C5 1.336 5.336 1 5.75 1h4.5C10.664 1 11 1.336 11 1.75V3zM7 6.75v5.5a.75.75 0 0 1-1.5 0v-5.5a.75.75 0 0 1 1.5 0zm3.5 0v5.5a.75.75 0 0 1-1.5 0v-5.5a.75.75 0 0 1 1.5 0z" />
          </svg>
        </n-button>
      </span>
    </div>

    <!-- 右键菜单：单一 manual dropdown 定位到鼠标处（n-dropdown 做 v-for 包裹元素时
         trigger 插槽不渲染，故不包裹标签项） -->
    <n-dropdown
      trigger="manual"
      placement="bottom-start"
      :x="menu.x"
      :y="menu.y"
      :options="menuOptions"
      :show="menu.show"
      :on-click="onMenu"
      @clickoutside="menu.show = false"
    />

    <div v-if="!tags.length && !loading" class="side-empty">暂无标签</div>

    <!-- 重命名弹窗（useDialog 不支持输入框，用 n-modal card，设计 §九 M3 同款） -->
    <n-modal
      :show="renameShow"
      preset="card"
      title="重命名标签"
      style="width: 400px"
      :mask-closable="!busy"
      :close-on-esc="!busy"
      @update:show="(v: boolean) => !v && (renameShow = false)"
    >
      <n-input
        ref="renameInput"
        v-model:value="renameName"
        placeholder="输入新的标签名（改成已有标签名则合并）"
        :disabled="busy"
        clearable
        @keyup.enter="confirmRename"
      />
      <div v-if="renameErr" style="color: #e88080; font-size: 12px; margin-top: 8px">{{ renameErr }}</div>
      <template #footer>
        <div style="display: flex; justify-content: flex-end; gap: 8px">
          <n-button size="small" :disabled="busy" @click="renameShow = false">取消</n-button>
          <n-button size="small" type="primary" :loading="busy" :disabled="!renameName.trim()" @click="confirmRename">
            确定
          </n-button>
        </div>
      </template>
    </n-modal>

    <!-- 删除弹窗：仅去关联 / 连流删除 二选一（设计 §6.4、§九 5） -->
    <n-modal
      :show="deleteShow"
      preset="card"
      title="删除标签"
      style="width: 440px"
      :mask-closable="!busy"
      :close-on-esc="!busy"
      @update:show="(v: boolean) => !v && (deleteShow = false)"
    >
      <p style="margin: 0 0 12px; font-size: 12px; line-height: 1.7; color: rgba(255,255,255,0.7)">
        确定删除标签「<b>{{ deleteTarget?.name }}</b>」（{{ deleteTarget?.count ?? 0 }} 条流）吗？
      </p>
      <div v-if="deleteErr" style="color: #e88080; font-size: 12px; margin-bottom: 8px">{{ deleteErr }}</div>
      <div style="display: flex; flex-direction: column; gap: 8px">
        <n-button size="small" :loading="busy" @click="confirmDelete(false)">
          仅移除标签关联（保留流数据）
        </n-button>
        <n-button size="small" type="error" secondary :loading="busy" @click="confirmDelete(true)">
          同时删除该标签下的流数据（不可恢复）
        </n-button>
        <n-button size="small" quaternary :disabled="busy" @click="deleteShow = false">取消</n-button>
      </div>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { nextTick, reactive, ref } from 'vue'
import { NButton, NDropdown, NInput, NModal } from 'naive-ui'
import type { ReviewTagInfo } from '../lib/types'

// 重命名/删除为异步动作（经回调 props 上抛父组件执行，便于 await 与统一错误提示）
const props = defineProps<{
  tags: ReviewTagInfo[]
  selected: string
  totalCount: number
  loading: boolean
  doRename: (id: string, name: string) => Promise<void>
  doDelete: (id: string, deleteFlows: boolean) => Promise<void>
}>()
defineEmits<{
  (e: 'select', id: string): void
  (e: 'refresh'): void
}>()

const busy = ref(false)

// --- 右键菜单（重命名 / 删除） ---
const menu = reactive({ show: false, x: 0, y: 0 })
const menuTarget = ref<ReviewTagInfo | null>(null)
const menuOptions = [
  { label: '重命名…', key: 'rename' },
  { label: '删除…', key: 'delete', props: { style: 'color:#e88080' } },
]
function openMenu(e: MouseEvent, t: ReviewTagInfo) {
  menu.show = false
  menu.x = e.clientX
  menu.y = e.clientY
  menuTarget.value = t
  nextTick(() => { menu.show = true })
}
function onMenu(key: string) {
  menu.show = false
  const t = menuTarget.value
  if (!t) return
  if (key === 'rename') openRename(t)
  else if (key === 'delete') openDelete(t)
}

// --- 重命名 ---
const renameShow = ref(false)
const renameName = ref('')
const renameErr = ref('')
const renameTarget = ref<ReviewTagInfo | null>(null)
const renameInput = ref<InstanceType<typeof NInput> | null>(null)

function openRename(t: ReviewTagInfo) {
  renameTarget.value = t
  renameName.value = t.name
  renameErr.value = ''
  renameShow.value = true
  void nextTick(() => renameInput.value?.focus())
}

async function confirmRename() {
  const name = renameName.value.trim()
  if (!name || !renameTarget.value || busy.value) return
  busy.value = true
  renameErr.value = ''
  try {
    await props.doRename(renameTarget.value.id, name)
    renameShow.value = false
  } catch (e) {
    renameErr.value = String((e as Error)?.message ?? e)
  } finally {
    busy.value = false
  }
}

// --- 删除 ---
const deleteShow = ref(false)
const deleteErr = ref('')
const deleteTarget = ref<ReviewTagInfo | null>(null)

function openDelete(t: ReviewTagInfo) {
  deleteTarget.value = t
  deleteErr.value = ''
  deleteShow.value = true
}

async function confirmDelete(deleteFlows: boolean) {
  if (!deleteTarget.value || busy.value) return
  busy.value = true
  deleteErr.value = ''
  try {
    await props.doDelete(deleteTarget.value.id, deleteFlows)
    deleteShow.value = false
  } catch (e) {
    deleteErr.value = String((e as Error)?.message ?? e)
  } finally {
    busy.value = false
  }
}

// 「最近使用」相对时间（分钟/小时/天）
function fmtAgo(ms: number): string {
  if (!ms) return '—'
  const diff = Date.now() - ms
  const min = Math.floor(diff / 60000)
  if (min < 1) return '刚刚'
  if (min < 60) return `${min} 分钟前`
  const h = Math.floor(min / 60)
  if (h < 24) return `${h} 小时前`
  return `${Math.floor(h / 24)} 天前`
}
</script>

<style scoped>
.sidebar { width: 220px; flex: none; border-right: 1px solid rgba(255,255,255,0.08); display: flex; flex-direction: column; overflow-y: auto; padding: 8px 6px; }
.side-head { display: flex; align-items: center; justify-content: space-between; padding: 4px 8px 8px; font-size: 12px; color: rgba(255,255,255,0.5); }
.tag-item { display: flex; align-items: center; gap: 6px; padding: 6px 8px; border-radius: 5px; cursor: pointer; font-size: 12px; position: relative; }
.tag-item:hover { background: rgba(255,255,255,0.06); }
.tag-item.active { background: rgba(181,126,220,0.18); }
.ti-icon { flex: none; opacity: 0.85; }
.ti-main { flex: 1; min-width: 0; }
.ti-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ti-time { font-size: 10px; color: rgba(255,255,255,0.4); margin-top: 1px; }
.ti-count { flex: none; font-size: 11px; color: rgba(255,255,255,0.5); background: rgba(255,255,255,0.08); border-radius: 8px; padding: 0 7px; line-height: 16px; }
.ti-ops { display: none; flex: none; align-items: center; gap: 2px; }
.tag-item:hover .ti-count { display: none; }
.tag-item:hover .ti-ops { display: inline-flex; }
.side-empty { padding: 16px 8px; text-align: center; font-size: 11px; color: rgba(255,255,255,0.3); }
</style>
