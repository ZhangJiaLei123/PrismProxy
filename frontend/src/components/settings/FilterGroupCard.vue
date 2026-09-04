<template>
  <section class="group-card">
    <div class="group-head">
      <span class="caret" @click="emit('toggle')">{{ expanded ? '▾' : '▸' }}</span>
      <n-input v-model:value="g.name" size="small" placeholder="组名" class="name-input" />
      <n-radio-group v-if="expanded" v-model:value="g.mode" size="small">
        <n-radio-button value="blacklist">黑名单</n-radio-button>
        <n-radio-button value="whitelist">白名单</n-radio-button>
      </n-radio-group>
      <n-tag v-else size="small" :type="g.mode === 'whitelist' ? 'warning' : 'error'" :bordered="false">
        {{ g.mode === 'whitelist' ? '白名单' : '黑名单' }}
      </n-tag>
      <span v-if="!expanded" class="badge">{{ entryBadge }}</span>
      <span class="head-spacer" />
      <n-switch v-model:value="g.enabled" size="small" />
      <n-popconfirm @positive-click="emit('remove')">
        <template #trigger>
          <n-button size="tiny" quaternary type="error">删除</n-button>
        </template>
        删除规则组「{{ g.name || '未命名' }}」？
      </n-popconfirm>
    </div>

    <div v-if="expanded" class="group-body">
      <!-- 已有条目：统一展示，前缀标明维度 -->
      <div v-if="entries.length" class="entry-list">
        <n-tag
          v-for="e in entries"
          :key="e.dim + e.idx"
          size="small"
          closable
          :type="isUnknownRef(e) ? 'error' : 'default'"
          @close="removeEntry(e)"
        >
          <span class="entry-dim">{{ e.label }}</span>{{ e.value }}
        </n-tag>
      </div>
      <div v-else class="hint empty-hint">此规则组暂无条目，不会匹配任何流量</div>

      <!-- 添加条目：先选类型，再填值 -->
      <div class="add-row">
        <n-select v-model:value="newType" size="small" class="type-ctl" :options="typeOptions" />
        <n-select
          v-if="newType === 'processes'"
          size="small"
          filterable
          tag
          class="value-ctl"
          placeholder="选择已见进程或输入进程名"
          :options="processOptions"
          :value="null"
          @update:value="(v: string | null) => addEntry(v ?? undefined)"
        />
        <n-input
          v-else
          v-model:value="newValue"
          size="small"
          class="value-ctl"
          :placeholder="newType === 'hosts' ? 'example.com 或 *.example.com' : '/api/v1/*'"
          @keydown.enter="addEntry()"
        />
        <n-button v-if="newType !== 'processes'" size="small" :disabled="!newValue.trim()" @click="addEntry()">
          添加
        </n-button>
      </div>

      <!-- 域名类型的快捷入口：引用内置域名组 -->
      <div v-if="newType === 'hosts'" class="ref-row">
        <n-select
          size="small"
          filterable
          clearable
          placeholder="引用域名组（追加 @组名）"
          :options="groupOptions"
          :value="null"
          @update:value="(v: string | null) => addEntry(v ?? undefined)"
        />
      </div>
      <div v-if="newType === 'paths'" class="hint dim-hint">
        支持通配符：/api/v1/*；不含通配符按“精确或子路径”匹配；单个 / 匹配所有路径
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  NButton,
  NInput,
  NPopconfirm,
  NRadioButton,
  NRadioGroup,
  NSelect,
  NSwitch,
  NTag,
} from 'naive-ui'
import type { rules } from '../../../wailsjs/go/models'

type Dim = 'hosts' | 'paths' | 'processes'

const props = defineProps<{
  g: rules.FilterGroup
  expanded: boolean
  groupOptions: { label: string; value: string }[]
  knownGroups: string[]
  processOptions: { label: string; value: string }[]
}>()
const emit = defineEmits<{ toggle: []; remove: [] }>()

// ---- 条目统一视图（模型仍是 hosts/paths/processes 三个数组） ----
interface Entry {
  dim: Dim
  label: string
  value: string
  idx: number
}
const dimLabels: Record<Dim, string> = { hosts: '域名', paths: '路径', processes: '进程' }

const entries = computed<Entry[]>(() => {
  const out: Entry[] = []
  ;(['hosts', 'paths', 'processes'] as Dim[]).forEach((dim) => {
    ;(props.g[dim] ?? []).forEach((v, i) => out.push({ dim, label: dimLabels[dim], value: v, idx: i }))
  })
  return out
})

const entryBadge = computed(() => {
  const parts: string[] = []
  if (props.g.hosts?.length) parts.push(`${props.g.hosts.length}域名`)
  if (props.g.paths?.length) parts.push(`${props.g.paths.length}路径`)
  if (props.g.processes?.length) parts.push(`${props.g.processes.length}进程`)
  return parts.length ? parts.join(' · ') : '空'
})

function isUnknownRef(e: Entry) {
  return e.dim === 'hosts' && e.value.startsWith('@') && !props.knownGroups.includes(e.value.slice(1))
}

function removeEntry(e: Entry) {
  props.g[e.dim]?.splice(e.idx, 1)
}

// ---- 添加条目 ----
const newType = ref<Dim>('hosts')
const newValue = ref('')
const typeOptions: { label: string; value: Dim }[] = [
  { label: '域名', value: 'hosts' },
  { label: '路径', value: 'paths' },
  { label: '进程', value: 'processes' },
]

function addEntry(v?: string) {
  const val = (v ?? newValue.value).trim()
  if (!val) return
  const arr = (props.g[newType.value] ??= [])
  if (!arr.includes(val)) arr.push(val)
  newValue.value = ''
}
</script>

<style scoped>
.group-card { border: 1px solid rgba(128, 128, 128, 0.25); border-radius: 6px; padding: 6px 8px; }
.group-head { display: flex; align-items: center; gap: 8px; }
.caret { cursor: pointer; user-select: none; width: 14px; opacity: 0.6; }
.name-input { max-width: 140px; }
.badge { font-size: 11px; opacity: 0.5; white-space: nowrap; }
.head-spacer { flex: 1; }
.group-body { margin-top: 8px; padding-left: 22px; }
.entry-list { display: flex; flex-wrap: wrap; gap: 4px 6px; margin-bottom: 8px; }
.entry-dim { opacity: 0.5; margin-right: 4px; font-size: 11px; }
.empty-hint { margin-bottom: 8px; }
.add-row { display: flex; align-items: center; gap: 8px; }
.type-ctl { width: 84px; flex-shrink: 0; }
.value-ctl { flex: 1; min-width: 0; }
.ref-row { margin-top: 6px; max-width: 260px; }
.hint { opacity: 0.5; font-size: 11px; }
.dim-hint { margin-top: 6px; }
</style>
