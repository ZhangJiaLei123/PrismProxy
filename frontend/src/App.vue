<template>
  <n-config-provider :theme="darkTheme" style="height: 100%">
    <n-message-provider>
     <n-dialog-provider>
      <n-layout style="height: 100%">
      <n-layout-header bordered style="height: 40px; display: flex; align-items: center; padding: 0 12px; gap: 12px">
        <!-- <span style="font-weight: 600">PrismProxy</span> -->
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
        <span style="flex: 1"></span>
        <span :style="{ opacity: store.paused ? 0.95 : 0.7, fontSize: '12px', color: store.paused ? '#e8c864' : undefined }">
          {{ store.flows.length }} 条流{{ store.paused ? '（已暂停刷新）' : '' }}
        </span>
        <n-button
          size="small"
          secondary
          :type="store.paused ? 'warning' : 'default'"
          :aria-label="store.paused ? '恢复列表刷新' : '暂停列表刷新'"
          :title="store.paused ? '恢复列表刷新（后端抓包未中断）' : '暂停列表刷新（后端继续抓包，仅冻结显示）'"
          @click="store.paused = !store.paused"
        >
          <svg v-if="store.paused" viewBox="0 0 16 16" width="16" height="16" fill="currentColor" aria-hidden="true">
            <path d="M4 2.5l10 5.5-10 5.5v-11z"/>
          </svg>
          <svg v-else viewBox="0 0 16 16" width="16" height="16" fill="currentColor" aria-hidden="true">
            <rect x="3.5" y="2.5" width="3.2" height="11" rx="1"/>
            <rect x="9.3" y="2.5" width="3.2" height="11" rx="1"/>
          </svg>
        </n-button>
        <n-button
          size="small"
          secondary
          aria-label="清空流列表"
          title="清空流列表（置顶流保留）"
          @click="store.clear()"
        >
          <svg viewBox="0 0 16 16" width="16" height="16" fill="currentColor" aria-hidden="true">
            <path d="M11 3h3v1h-2v9.5A1.5 1.5 0 0 1 10.5 15h-5A1.5 1.5 0 0 1 4 13.5V4H2V3h3V1.75C5 1.336 5.336 1 5.75 1h4.5C10.664 1 11 1.336 11 1.75V3zM7 6.75v5.5a.75.75 0 0 1-1.5 0v-5.5a.75.75 0 0 1 1.5 0zm3.5 0v5.5a.75.75 0 0 1-1.5 0v-5.5a.75.75 0 0 1 1.5 0zM6.5 2v1h3V2h-3z"/>
          </svg>
        </n-button>
        <n-button size="small" secondary aria-label="GitHub 仓库" title="在新窗口打开 GitHub 仓库" @click="openGithub">
          <svg viewBox="0 0 16 16" width="16" height="16" fill="currentColor" aria-hidden="true">
            <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0 0 16 8c0-4.42-3.58-8-8-8z"/>
          </svg>
        </n-button>
      </n-layout-header>
      <n-layout-content :style="contentStyle">
        <n-alert
          v-if="startError"
          type="error"
          closable
          style="margin: 6px 8px 0"
          @close="startError = ''"
        >
          <span style="display: flex; align-items: center; gap: 8px">
            <span>{{ startError }}</span>
            <n-button size="tiny" secondary type="error" @click="showSettings = true">打开设置</n-button>
          </span>
        </n-alert>
        <n-split direction="horizontal" :default-size="0.58" :min="0.3" :max="0.8" :style="{ height: splitHeight }">
          <template #1>
            <flow-list />
          </template>
          <template #2>
            <flow-detail />
          </template>
        </n-split>
      </n-layout-content>
      <!-- 底部工具栏：过滤搜索（验收 #8） + 设置入口 -->
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
        <n-select v-model:value="store.filter.method" size="tiny" :options="methodOptions" style="width: 92px" />
        <n-select v-model:value="store.filter.status" size="tiny" :options="statusOptions" style="width: 92px" />
        <span v-if="filterActive" style="opacity: 0.6; white-space: nowrap">
          {{ store.filtered.length }}/{{ store.flows.length }}
        </span>
        <span style="flex: 1"></span>
        <n-button size="tiny" secondary @click="openSettings">设置</n-button>
      </n-layout-footer>
    </n-layout>
      <settings-panel v-model:show="showSettings" :initial-tab="settingsTab" @changed="onSettingsChanged" />
      <composer />
     </n-dialog-provider>
    </n-message-provider>
  </n-config-provider>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NConfigProvider, NLayout, NLayoutHeader, NLayoutContent, NLayoutFooter, NSplit, NTag, NButton, NInput, NSelect, NAlert, NSwitch, NMessageProvider, NDialogProvider, darkTheme } from 'naive-ui'
import FlowList from './pages/FlowList.vue'
import FlowDetail from './pages/FlowDetail.vue'
import SettingsPanel from './pages/SettingsPanel.vue'
import Composer from './pages/Composer.vue'
import { useFlowsStore } from './stores/flows'
import { GetProxyStatus, StartProxy, StopProxy, GetSystemProxyStatus, SetSystemProxy, GetSettings } from '../wailsjs/go/app/App'
import { EventsOn, WindowUnminimise } from '../wailsjs/runtime/runtime'
import type { app } from '../wailsjs/go/models'

const store = useFlowsStore()
const status = ref<app.ProxyStatus>({ Running: false, Addr: '', Mode: '', FlowCount: 0 } as app.ProxyStatus)
const showSettings = ref(false)
// 设置抽屉初始标签：状态标签入口定位到「网络」（代理服务），底部按钮默认「常规」
const settingsTab = ref('general')
// 启动自动抓包失败（如端口占用）的错误条，后端 proxy:start-error 事件驱动
// 顶栏快捷开关的动作失败也复用该错误条（消息自带上下文前缀）
const startError = ref('')
// 顶栏快捷开关：系统代理状态（on/occupied/off）与两个开关的 busy 态
const sysState = ref('off')
const sysOn = computed(() => sysState.value === 'on')
const listenBusy = ref(false)
const sysBusy = ref(false)
// 顶栏系统代理开关可见性（设置-常规-工具栏，默认显示）
const showSysSwitch = ref(true)

async function loadPrefs() {
  const s = await GetSettings()
  showSysSwitch.value = s.showSysProxySwitch !== false
}

// 设置保存或系统代理状态变化后：刷新头部状态 + 重新读取开关可见性
function onSettingsChanged() {
  refreshStatus()
  loadPrefs()
}

function errText(e: unknown) {
  return e instanceof Error ? e.message : String(e)
}

// 监听开关：停止时若系统代理已接管则先恢复注册表，避免代理停了系统流量全断
async function toggleListen(v: boolean) {
  listenBusy.value = true
  try {
    if (v) {
      await StartProxy('')
      startError.value = ''
    } else {
      if (sysOn.value) await SetSystemProxy(false)
      await StopProxy()
    }
  } catch (e) {
    startError.value = (v ? '代理启动失败：' : '代理停止失败：') + errText(e)
  } finally {
    listenBusy.value = false
    await refreshStatus()
  }
}

// 系统代理开关：后端 SetSystemProxy(true) 会在监听未启动时自动拉起代理
async function toggleSysProxy(v: boolean) {
  sysBusy.value = true
  try {
    await SetSystemProxy(v)
  } catch (e) {
    startError.value = '系统代理切换失败：' + errText(e)
  } finally {
    sysBusy.value = false
    await refreshStatus()
  }
}

function openProxySettings() {
  settingsTab.value = 'network'
  showSettings.value = true
}

function openSettings() {
  settingsTab.value = 'general'
  showSettings.value = true
}

// M8：AI CLI `cli ui settings [tab]` 经 ctlapi → Wails ui:open-settings 事件驱动
function onUIOpenSettings(tab?: string) {
  settingsTab.value = tab || 'general'
  showSettings.value = true
  WindowUnminimise() // 窗口最小化时恢复，用户才能看到抽屉
}

const GITHUB_URL = 'https://github.com/ZhangJiaLei123/PrismProxy'
function openGithub() {
  // Wails 桌面端：调系统默认浏览器（应用外新窗口）；纯浏览器预览环境降级为新标签页打开
  const wailsRuntime = (window as unknown as { runtime?: { BrowserOpenURL?: (url: string) => void } }).runtime
  if (wailsRuntime?.BrowserOpenURL) {
    wailsRuntime.BrowserOpenURL(GITHUB_URL)
  } else {
    window.open(GITHUB_URL, '_blank', 'noopener,noreferrer')
  }
}

// 内容区固定扣掉顶栏 40px + 底栏 28px；错误条占 40px 时列表高度同步收缩
const contentStyle = { height: 'calc(100% - 40px - 28px)' }
const splitHeight = computed(() => (startError.value ? 'calc(100% - 40px)' : '100%'))

async function refreshStatus() {
  const [st, sys] = await Promise.all([GetProxyStatus(), GetSystemProxyStatus()])
  status.value = st
  sysState.value = sys.state
  // 启动失败兜底：事件先于挂载到达时从状态里恢复错误条
  if (st.StartError && !startError.value) {
    startError.value = '代理启动失败：' + st.StartError
  }
}

// 过滤选项（空值 = 全部）
const methodOptions = [
  { label: '全部方法', value: '' },
  ...['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS', 'CONNECT'].map((m) => ({ label: m, value: m })),
]
const statusOptions = [
  { label: '全部状态', value: '' },
  { label: '2xx', value: '2xx' },
  { label: '3xx', value: '3xx' },
  { label: '4xx', value: '4xx' },
  { label: '5xx', value: '5xx' },
  { label: '错误', value: 'error' },
]

const filterActive = computed(
  () => !!(store.filter.keyword.trim() || store.filter.method || store.filter.status),
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

onMounted(async () => {
  EventsOn('proxy:start-error', (msg: string) => {
    startError.value = '代理启动失败：' + msg
    refreshStatus()
  })
  // 后端 startup 完成自动启动/接管后推送；onMounted 首次刷新可能更早（startCtlAPI 等耗时），
  // 事件到达时再刷一次，避免顶栏开关恒显"已停止"（M8 时序竞态修复）
  EventsOn('proxy:ready', () => refreshStatus())
  EventsOn('ui:open-settings', (tab?: string) => onUIOpenSettings(tab))
  await store.init()
  await refreshStatus()
  await loadPrefs()
})
</script>

<style scoped>
.regex-invalid { color: #e88080 !important; }
.status-tag { cursor: pointer; }
.hdr-switch { display: flex; align-items: center; gap: 4px; }
.hdr-label { font-size: 12px; opacity: 0.8; }
</style>
