<template>
  <!-- 底部工具栏：过滤搜索（验收 #8） + 代理状态/快捷开关 + 设置入口 -->
  <n-layout-footer bordered style="height: 28px; display: flex; align-items: center; padding: 0 8px; gap: 6px; font-size: 12px">
    <n-input
      v-model:value="store.filter.keyword"
      size="tiny"
      clearable
      placeholder="过滤：域名 / URL 关键字"
      style="width: 180px"
    />
    <n-button
      size="tiny"
      :type="store.filter.regex ? 'primary' : 'default'"
      :secondary="!store.filter.regex"
      :class="{ 'regex-invalid': regexInvalid }"
      title="正则匹配（不区分大小写；非法正则降级为子串）"
      @click="store.filter.regex = !store.filter.regex"
    >.*</n-button>
    <n-select
      :value="store.filter.methods"
      class="filter-sel"
      size="tiny"
      multiple
      clearable
      max-tag-count="responsive"
      :options="methodOptions"
      placeholder="方法"
      style="width: 120px"
      @update:value="(v: string[]) => pickFilterValues('methods', v)"
    />
    <n-select
      :value="store.filter.statuses"
      class="filter-sel"
      size="tiny"
      multiple
      clearable
      max-tag-count="responsive"
      :options="statusOptions"
      placeholder="状态"
      style="width: 104px"
      @update:value="(v: string[]) => pickFilterValues('statuses', v)"
    />
    <span v-if="filterActive" style="opacity: 0.6; white-space: nowrap">
      {{ store.filtered.length }}/{{ store.flows.length }}
    </span>
    <span style="flex: 1"></span>
    <!-- 代理状态与快捷开关（自顶栏迁入） -->
    <n-tag
      size="small"
      :type="status.Running ? 'success' : 'error'"
      class="status-tag"
      title="点击打开代理设置"
      @click="openProxySettings"
    >
      {{ status.Running ? `代理运行中 ${status.Addr}` : '代理已停止' }}
    </n-tag>
    <n-tag v-if="status.Running" size="small" type="info">{{ status.Mode }}</n-tag>
    <span class="hdr-switch" title="启动/停止代理监听">
      <span class="hdr-label">监听</span>
      <n-switch size="small" :value="status.Running" :loading="listenBusy" @update:value="toggleListen" />
    </span>
    <span v-if="showSysSwitch" class="hdr-switch" title="一键接管/恢复系统代理（接管时若监听未启动会自动拉起）">
      <span class="hdr-label">系统代理</span>
      <n-switch size="small" :value="sysOn" :loading="sysBusy" @update:value="toggleSysProxy" />
    </span>
    <n-button size="tiny" secondary @click="openSettings">设置</n-button>
  </n-layout-footer>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NLayoutFooter, NTag, NButton, NInput, NSelect, NSwitch } from 'naive-ui'
import { useFlowsStore } from '../stores/flows'
import { GetProxyStatus, StartProxy, StopProxy, GetSystemProxyStatus, SetSystemProxy, GetSettings } from '../../wailsjs/go/app/App'
import type { app } from '../../wailsjs/go/models'

// 打开设置抽屉：tab 为设置面板标签（'general' | 'network'）
// error：replace=true 表示用户开关动作刚失败（覆盖显示最新错误）；否则为 StartError 轮询兜底（不覆盖已显示错误）
// clear-error：监听启动成功后通知父级清空错误条
const emit = defineEmits<{
  (e: 'open-settings', tab: string): void
  (e: 'error', message: string, replace?: boolean): void
  (e: 'clear-error'): void
}>()

const store = useFlowsStore()
const status = ref<app.ProxyStatus>({ Running: false, Addr: '', Mode: '', FlowCount: 0 } as app.ProxyStatus)
// 底栏快捷开关：系统代理状态（on/occupied/off）与两个开关的 busy 态
const sysState = ref('off')
const sysOn = computed(() => sysState.value === 'on')
const listenBusy = ref(false)
const sysBusy = ref(false)
// 底栏系统代理开关可见性（设置-常规-工具栏，默认显示）
const showSysSwitch = ref(true)

function errText(e: unknown) {
  return e instanceof Error ? e.message : String(e)
}

// 已上报过的后端启动错误（st.StartError 原文）：同一错误只 emit 一次。
// 用户手动关闭错误条后，轮询/事件刷新不会让旧错误条反复复现；
// 错误文本变化（又一次新的启动失败）或启动成功后端清空后，才允许重新上报
let lastStartError = ''

// 刷新代理状态 + 系统代理状态（热路径：开关动作 finally、proxy:ready / proxy:start-error 事件后调用）。
// allSettled 隔离：单个查询失败不影响另一个状态更新
async function refreshStatus() {
  const [st, sys] = await Promise.allSettled([GetProxyStatus(), GetSystemProxyStatus()])
  if (st.status === 'fulfilled') {
    status.value = st.value
    if (st.value.StartError) {
      // 启动失败兜底：事件先于挂载到达时从状态里恢复错误条；同一错误不重复上报
      if (st.value.StartError !== lastStartError) {
        lastStartError = st.value.StartError
        emit('error', '代理启动失败：' + st.value.StartError)
      }
    } else {
      lastStartError = ''
    }
  }
  if (sys.status === 'fulfilled') sysState.value = sys.value.state
}

// 刷新工具栏偏好（系统代理开关可见性）；仅挂载时与设置保存后需要，不进开关热路径
async function refreshPrefs() {
  try {
    const s = await GetSettings()
    showSysSwitch.value = s.showSysProxySwitch !== false
  } catch {
    // 偏好读取失败不影响代理状态展示
  }
}

// 全量刷新：挂载时与设置保存后由父组件调用
function refresh() {
  return Promise.all([refreshStatus(), refreshPrefs()])
}

// 监听开关：停止时若系统代理已接管则先恢复注册表，避免代理停了系统流量全断
async function toggleListen(v: boolean) {
  listenBusy.value = true
  try {
    if (v) {
      await StartProxy('')
      // 启动成功：清掉可能残留的启动失败错误条（与原版 startError='' 等价）
      emit('clear-error')
    } else {
      if (sysOn.value) await SetSystemProxy(false)
      await StopProxy()
    }
  } catch (e) {
    // 用户动作失败：覆盖显示最新错误，避免旧错误条占位导致新失败无反馈
    emit('error', (v ? '代理启动失败：' : '代理停止失败：') + errText(e), true)
  } finally {
    listenBusy.value = false
    await refresh()
  }
}

// 系统代理开关：后端 SetSystemProxy(true) 会在监听未启动时自动拉起代理
async function toggleSysProxy(v: boolean) {
  sysBusy.value = true
  try {
    await SetSystemProxy(v)
  } catch (e) {
    emit('error', '系统代理切换失败：' + errText(e))
  } finally {
    sysBusy.value = false
    await refresh()
  }
}

function openProxySettings() {
  emit('open-settings', 'network')
}

function openSettings() {
  emit('open-settings', 'general')
}

// 多选过滤选项：首项「全部」是空值哨兵，选中=清空具体选择；多选交互见 pickFilterValues
const ALL_SENTINEL = ''
const methodOptions = [
  { label: '全部方法', value: ALL_SENTINEL },
  ...['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS', 'CONNECT'].map((m) => ({ label: m, value: m })),
]
const statusOptions = [
  { label: '全部状态', value: ALL_SENTINEL },
  ...['2xx', '3xx', '4xx', '5xx'].map((s) => ({ label: s, value: s })),
  { label: '错误', value: 'error' },
]

// 多选值处理：点「全部」→ 清空为全部；点具体项 → 去掉哨兵只留具体值；全清空 → 全部
function pickFilterValues(kind: 'methods' | 'statuses', v: string[]) {
  let next = v
  if (v.includes(ALL_SENTINEL)) {
    // 上一态已含哨兵（点的是具体项）→ 去哨兵留具体值；否则（点的是「全部」）→ 全部
    next = store.filter[kind].includes(ALL_SENTINEL) ? v.filter((x) => x !== ALL_SENTINEL) : []
  }
  store.filter[kind] = next
}

const filterActive = computed(
  () =>
    !!(
      store.filter.keyword.trim() ||
      store.filter.methods.filter((m) => m !== ALL_SENTINEL).length ||
      store.filter.statuses.filter((s) => s !== ALL_SENTINEL).length
    ),
)
// 非法正则红色提示（匹配逻辑自动降级为子串）
const regexInvalid = computed(() => {
  if (!store.filter.regex || !store.filter.keyword.trim()) return false
  try {
    new RegExp(store.filter.keyword.trim())
    return false
  } catch {
    return true
  }
})

// refresh：挂载/设置保存后全量刷新（含工具栏偏好）；refreshStatus：事件与开关动作后只刷代理状态
defineExpose({ refresh, refreshStatus })

onMounted(() => {
  void refresh()
})
</script>

<style scoped>
.regex-invalid { color: #e88080 !important; }
.status-tag { cursor: pointer; }
.hdr-switch { display: flex; align-items: center; gap: 4px; }
.hdr-label { font-size: 12px; opacity: 0.8; }
/* 底栏 28px 固定高度：多选 tag 单行排列、溢出裁剪，禁止换行把 footer 撑高 */
.filter-sel :deep(.n-base-selection-tags) {
  flex-wrap: nowrap;
  overflow: hidden;
}
</style>
