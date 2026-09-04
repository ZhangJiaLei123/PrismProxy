<template>
  <!-- 顶部状态条：模式提示（谓词与引擎一致：enabled + 条目非空才算生效） -->
  <n-alert :type="modeAlertType" :bordered="false" class="mode-bar">{{ modeText }}</n-alert>

  <!-- 未知 @引用 本地即时提示（保存时后端还会再校验并透传 warnings） -->
  <n-alert v-if="unknownRefs.length" type="warning" :bordered="false" class="mode-bar">
    未知 @引用（不会命中任何域名）：{{ unknownRefs.join(' ') }}
  </n-alert>

  <!-- 规则组卡片列表 -->
  <filter-group-card
    v-for="(g, gi) in form.filterGroups"
    :key="g.id || gi"
    :g="g"
    :expanded="!!expanded[gi]"
    :group-options="groupOptions"
    :known-groups="knownGroups"
    :process-options="processOptions"
    @toggle="expanded[gi] = !expanded[gi]"
    @remove="form.filterGroups.splice(gi, 1)"
  />

  <n-button dashed block style="margin-top: 10px" @click="addGroup">+ 新建规则组</n-button>

  <div class="hint bottom-hint">
    黑名单 = 命中的不显示；白名单 = 只显示命中的。多个黑名单组取并集屏蔽；存在白名单组时默认全部不显示、白名单命中才显示；黑名单优先于白名单。<br />
    模拟器流量统一归因到 VBoxNetNAT.exe，白名单建议按域名/路径配置。
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton } from 'naive-ui'
import { ListDomainGroups } from '../../../wailsjs/go/main/App'
import type { settings } from '../../../wailsjs/go/models'
import { useFlowsStore } from '../../stores/flows'
import FilterGroupCard from './FilterGroupCard.vue'

const props = defineProps<{ form: settings.Settings }>()

// ---- 域名组下拉（@引用） ----
interface GroupMeta {
  id: string
  name: string
  category: string
}
const knownGroups = ref<string[]>([])
const groupOptions = ref<{ label: string; value: string }[]>([])

onMounted(async () => {
  const res = await ListDomainGroups()
  knownGroups.value = (res?.names as string[]) ?? []
  const meta = (res?.meta as GroupMeta[]) ?? []
  const titles = (res?.titles as Record<string, string>) ?? {}
  // 以 names 为准（含用户导入的自定义组）；显示名：txt 头部标题 > index.json 中文名 > id
  const byId = new Map(meta.map((m) => [m.id, m]))
  groupOptions.value = knownGroups.value.map((id) => {
    const t = titles[id]
    const m = byId.get(id)
    const name = t || m?.name || id
    return {
      label: m?.category ? `${name}（${m.category}）` : name,
      value: '@' + id,
    }
  })
})

// ---- 进程下拉：已捕获流量中出现过的进程名 ----
const flowsStore = useFlowsStore()
const processOptions = computed(() => {
  const set = new Set<string>()
  for (const f of flowsStore.flows) {
    const p = (f.ProcessName || '').trim()
    if (p) set.add(p)
  }
  return [...set].sort().map((p) => ({ label: p, value: p }))
})

// ---- 展开/折叠（默认折叠，新建组自动展开） ----
const expanded = ref<Record<number, boolean>>({})

// ---- 谓词（与引擎 isActiveWhitelist 一致：enabled + 条目非空） ----
const hasEntries = (g: { hosts?: string[]; paths?: string[]; processes?: string[] }) =>
  (g.hosts?.length ?? 0) + (g.paths?.length ?? 0) + (g.processes?.length ?? 0) > 0

const hasWhitelist = computed(
  () => props.form.filterGroups?.some((g) => g.enabled && g.mode === 'whitelist' && hasEntries(g)) ?? false,
)
const hasBlacklist = computed(
  () => props.form.filterGroups?.some((g) => g.enabled && g.mode === 'blacklist' && hasEntries(g)) ?? false,
)

const modeText = computed(() => {
  if (hasWhitelist.value && hasBlacklist.value)
    return '白名单 + 黑名单：先白名单收窄，黑名单再排除（黑名单优先）'
  if (hasWhitelist.value) return '白名单模式：仅显示命中组内条目的流量'
  if (hasBlacklist.value) return '黑名单模式：命中组内条目的流量不显示'
  return '未配置生效的规则组：全部流量均显示'
})
const modeAlertType = computed(() =>
  hasWhitelist.value ? ('warning' as const) : hasBlacklist.value ? ('error' as const) : ('info' as const),
)

// ---- 未知 @引用（本地即时检查） ----
const unknownRefs = computed(() => {
  const out = new Set<string>()
  for (const g of props.form.filterGroups ?? []) {
    for (const h of g.hosts ?? []) {
      if (h.startsWith('@') && !knownGroups.value.includes(h.slice(1))) out.add(h)
    }
  }
  return [...out]
})

// ---- 新建组 ----
function addGroup() {
  props.form.filterGroups ??= []
  const existing = new Set(props.form.filterGroups.map((g) => g.name))
  let n = props.form.filterGroups.length + 1
  while (existing.has(`规则组 ${n}`)) n++
  props.form.filterGroups.push({
    id: '',
    name: `规则组 ${n}`,
    enabled: true,
    mode: 'blacklist',
    hosts: [],
    paths: [],
    processes: [],
  })
  expanded.value[props.form.filterGroups.length - 1] = true
}
</script>

<style scoped>
.mode-bar { margin-bottom: 10px; font-size: 12px; }
.hint { opacity: 0.5; font-size: 11px; }
.bottom-hint { margin-top: 12px; line-height: 1.7; }
</style>
