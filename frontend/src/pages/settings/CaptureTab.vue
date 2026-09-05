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

  <!-- 规则备份：导出 / 导入（即时落盘 + 热更新，替换全部规则组，不经底部「保存」） -->
  <section class="sec">
    <div class="sec-title">规则备份</div>
    <div class="row">
      <n-button size="small" secondary :loading="exportBusy" @click="exportRules">导出规则…</n-button>
      <n-checkbox v-model:checked="embedGroups" size="small">内嵌引用的域名组清单</n-checkbox>
    </div>
    <div class="row">
      <n-button size="small" secondary :loading="fileBusy" @click="importFile">从文件导入…</n-button>
    </div>
    <div class="row">
      <n-input v-model:value="importURL" size="small" placeholder="https://…/rules.json" style="flex: 1" />
      <n-button size="small" secondary :loading="urlBusy" :disabled="!importURL.trim()" @click="importFromURL">
        URL 导入
      </n-button>
    </div>
    <div class="hint">
      导出为 JSON（含规则组、解密规则、绕过列表）。导入为整体替换当前规则，校验通过后即时落盘并热更新，未点「保存」的表单改动会被覆盖；引用的域名组缺失时会给出提示，可到「域名组」页导入。
    </div>
  </section>

  <div class="hint bottom-hint">
    黑名单 = 命中的不显示；白名单 = 只显示命中的。多个黑名单组取并集屏蔽；存在白名单组时默认全部不显示、白名单命中才显示；黑名单优先于白名单。<br />
    模拟器流量统一归因到 VBoxNetNAT.exe，白名单建议按域名/路径配置。
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCheckbox, NInput, useDialog, useMessage } from 'naive-ui'
import { ExportRules, ImportRules, ListDomainGroups, ListSystemProcesses } from '../../../wailsjs/go/app/App'
import type { app, settings } from '../../../wailsjs/go/models'
import { useFlowsStore } from '../../stores/flows'
import FilterGroupCard from './FilterGroupCard.vue'

const props = defineProps<{ form: settings.Settings }>()
// 导入即时落盘 + 热更新后，由父组件（SettingsPanel）重新 GetSettings 刷新表单
const emit = defineEmits<{ (e: 'imported'): void }>()

const message = useMessage()
const dialog = useDialog()

// ---- 域名组下拉（@引用） ----
interface GroupMeta {
  id: string
  name: string
  category: string
}
const knownGroups = ref<string[]>([])
const groupOptions = ref<{ label: string; value: string }[]>([])

onMounted(async () => {
  systemProcs.value = (await ListSystemProcesses()) ?? []
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

// ---- 进程下拉：系统全部运行进程 + 已捕获流量中出现过的进程名（合并去重，小写归一） ----
const flowsStore = useFlowsStore()
const systemProcs = ref<string[]>([])
const processOptions = computed(() => {
  const set = new Set<string>(systemProcs.value)
  for (const f of flowsStore.flows) {
    const p = (f.ProcessName || '').trim().toLowerCase()
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

// ---- 规则导入导出（即时落盘 + 热更新，不经底部「保存」） ----
const embedGroups = ref(true)
const importURL = ref('')
const exportBusy = ref(false)
const fileBusy = ref(false)
const urlBusy = ref(false)

async function exportRules() {
  exportBusy.value = true
  try {
    const dest = await ExportRules(embedGroups.value)
    // 空串 = 用户取消保存对话框
    if (dest) message.success(`已导出到 ${dest}`, { closable: true, duration: 5000 })
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
  } finally {
    exportBusy.value = false
  }
}

// 整体替换前请用户确认（未保存的表单改动会丢失）；取消返回 null
function confirmReplace(): Promise<boolean> {
  return new Promise((resolve) => {
    dialog.warning({
      title: '导入规则将整体替换',
      content: '导入会用文件中的规则组、解密规则与绕过列表替换当前全部规则，当前面板未保存的改动也会丢失。此操作不可撤销，是否继续？',
      positiveText: '替换并导入',
      negativeText: '取消',
      closable: false,
      maskClosable: false,
      onPositiveClick: () => resolve(true),
      onNegativeClick: () => resolve(false),
      onClose: () => resolve(false),
    })
  })
}

async function afterImport(res: app.ImportRulesResult | null) {
  if (!res) return // 用户取消文件对话框
  message.success('规则已导入并即时生效', { closable: true, duration: 3000 })
  // 缺组等校验提醒（不阻塞导入）
  if (res.warnings?.length) {
    const list = res.warnings.map((w) => '· ' + w).join('\n')
    dialog.warning({
      title: `导入完成，有 ${res.warnings.length} 条提醒`,
      content: list,
      positiveText: '知道了',
      style: { whiteSpace: 'pre-wrap' },
    })
  }
  importURL.value = ''
  emit('imported') // 通知 SettingsPanel 重新 GetSettings 刷新表单
}

async function importFile() {
  if (!(await confirmReplace())) return
  fileBusy.value = true
  try {
    await afterImport(await ImportRules(''))
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
  } finally {
    fileBusy.value = false
  }
}

async function importFromURL() {
  const url = importURL.value.trim()
  if (!url) return
  if (!(await confirmReplace())) return
  urlBusy.value = true
  try {
    await afterImport(await ImportRules(url))
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
  } finally {
    urlBusy.value = false
  }
}
</script>

<style scoped>
.mode-bar { margin-bottom: 10px; font-size: 12px; }
.hint { opacity: 0.5; font-size: 11px; }
.bottom-hint { margin-top: 12px; line-height: 1.7; }
.sec { margin-top: 18px; padding-top: 12px; border-top: 1px dashed rgba(128, 128, 128, 0.25); font-size: 12px; }
.sec-title { font-weight: 600; font-size: 13px; margin-bottom: 8px; }
.row { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
</style>
