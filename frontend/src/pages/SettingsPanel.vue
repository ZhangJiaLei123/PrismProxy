<template>
  <n-drawer :show="show" :width="640" placement="right" @update:show="close">
    <n-drawer-content title="设置" closable>
      <!-- M11：欢迎页（无打开项目）只提供全局设置；规则/域名组随项目，打开项目后可见 -->
      <n-alert v-if="!hasOpenProject" type="info" :bordered="false" style="margin-bottom: 10px">
        当前未打开项目：以下为全局设置（代理监听、上游、系统代理、ADB、持久化等），对所有项目生效；
        解密规则、过滤规则、域名组按项目隔离，请先新建或打开一个项目。
      </n-alert>
      <n-tabs v-model:value="activeTab" type="line" placement="left" :bar-width="200" class="settings-tabs">
        <!-- ============ 常规：系统代理（实时控制）+ 根证书（全局） ============ -->
        <n-tab-pane name="general" tab="常规">
          <general-tab :form="form" @changed="emit('changed')" />
        </n-tab-pane>

        <!-- ============ 网络：监听 / 上游 / 存储 / 绕过列表（全局） ============ -->
        <n-tab-pane name="network" tab="网络">
          <network-tab :form="form" />
        </n-tab-pane>

        <!-- ============ ADB 代理：模拟器/真机一键设置 http_proxy（全局） ============ -->
        <n-tab-pane name="adb" tab="ADB 代理">
          <adb-tab :form="form" />
        </n-tab-pane>

        <!-- ============ 解密规则（项目级，仅打开项目后可见） ============ -->
        <n-tab-pane v-if="hasOpenProject" name="decrypt" tab="解密规则">
          <decrypt-tab :form="form" />
        </n-tab-pane>

        <!-- ============ 过滤规则：黑白名单规则组（项目级） ============ -->
        <n-tab-pane v-if="hasOpenProject" name="capture" tab="过滤规则">
          <!-- 规则导入即时落盘 + 热更新，导入后重新拉取设置刷新表单 -->
          <capture-tab :form="form" @imported="loadSettings" />
        </n-tab-pane>

        <!-- ============ 域名组：导入/导出/删除（项目级，即时生效，不走表单保存） ============ -->
        <n-tab-pane v-if="hasOpenProject" name="domains" tab="域名组">
          <domains-tab />
        </n-tab-pane>
      </n-tabs>

      <template #footer>
        <!-- warnings 保留在固定 footer（按钮上方）；保存错误用全局悬浮 message（useMessage） -->
        <n-alert v-if="saveWarnings.length" type="warning" closable style="margin-bottom: 10px" title="已保存，但有以下提醒" @close="saveWarnings = []">
          <div v-for="(w, i) in saveWarnings" :key="i">{{ w }}</div>
        </n-alert>
        <div style="display: flex; gap: 8px; justify-content: flex-end">
          <n-button @click="close">取消</n-button>
          <n-button type="primary" :loading="saving" @click="save">保存</n-button>
        </div>
      </template>
    </n-drawer-content>
  </n-drawer>
</template>

<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { NDrawer, NDrawerContent, NTabs, NTabPane, NButton, NAlert, useMessage } from 'naive-ui'
import GeneralTab from './settings/GeneralTab.vue'
import NetworkTab from './settings/NetworkTab.vue'
import AdbTab from './settings/AdbTab.vue'
import DecryptTab from './settings/DecryptTab.vue'
import CaptureTab from './settings/CaptureTab.vue'
import DomainsTab from './settings/DomainsTab.vue'
import { GetSettings, SaveSettings } from '../../wailsjs/go/app/App'
import { EventsOff, EventsOn } from '../../wailsjs/runtime/runtime'
import { useProjects } from '../composables/useProjects'
import type { app } from '../../wailsjs/go/models'

const props = defineProps<{ show: boolean; initialTab?: string }>()

// M11：无打开项目（欢迎页）时只展示全局设置 tab，规则/域名组 tab 隐藏
const { hasOpenProject } = useProjects()
const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
  (e: 'changed'): void // 设置已保存或系统代理状态变化（父组件刷新头部状态）
}>()

// ---- 表单状态（各 tab 通过 props 共享修改，保存时统一提交） ----
// M9：SettingsView = 全局环境字段 + 当前项目规则字段；rulesProject 为保存时的并发令牌
const emptyForm = (): app.SettingsView =>
  ({
    listenAddr: '127.0.0.1:9090',
    upstreamMode: 'direct',
    upstreamProxy: '',
    maxFlows: 2000,
    maxBodyMB: 256,
    showSysProxySwitch: true,
    bypassList: [],
    filterGroups: [],
    decryptRules: [],
    persist: { enabled: false, retainDays: 7, maxMB: 500 },
    adb: { deviceProxyHost: '172.16.1.2', configs: [] },
    rulesProject: '',
  }) as app.SettingsView

const form = ref<app.SettingsView>(emptyForm())
const activeTab = ref('general')
const saving = ref(false)
const saveWarnings = ref<string[]>([])
// 保存错误用全局悬浮 message（App.vue 的 n-message-provider 提供）
const message = useMessage()

// ---- 载入（打开面板时；规则导入后由 CaptureTab @imported 触发重载） ----
async function loadSettings() {
  const s = await GetSettings()
  form.value = JSON.parse(JSON.stringify(s)) as app.SettingsView
  form.value.bypassList ??= []
  form.value.decryptRules ??= []
  form.value.filterGroups ??= []
  form.value.persist ??= { enabled: false, retainDays: 7, maxMB: 500 }
  form.value.adb ??= { deviceProxyHost: '172.16.1.2', configs: [] }
  form.value.adb.configs ??= []
  if (!form.value.adb.deviceProxyHost) form.value.adb.deviceProxyHost = '172.16.1.2'
  for (const g of form.value.filterGroups) {
    g.hosts ??= []
    g.paths ??= []
    g.processes ??= []
  }
}

// ---- M9：面板打开期间项目被切换（顶栏/CLI）→ 提示丢弃修改并强载新项目规则 ----
// project:changed 是事后通知，切换已发生无法取消，只能提示 + 重载（设计 §7.2）
function onProjectChanged() {
  if (!props.show) return
  saveWarnings.value = []
  message.info('项目已切换，未保存的修改已丢弃', { duration: 4000 })
  loadSettings()
}

// M8：面板打开时加载配置并定位 tab；面板已打开时外部（cli ui settings <tab>）
// 改 initialTab 只切 tab，不重载表单（避免丢失用户正在编辑的内容）。
// M9：打开期间订阅 project:changed（切换时提示+强载），关闭时退订。
watch(
  () => [props.show, props.initialTab] as const,
  async ([v], [was]) => {
    if (v && !was) {
      activeTab.value = effectiveTab(props.initialTab)
      saveWarnings.value = []
      EventsOn('project:changed', onProjectChanged)
      await loadSettings()
    } else if (v && props.initialTab) {
      activeTab.value = effectiveTab(props.initialTab)
    } else if (!v && was) {
      EventsOff('project:changed')
    }
  },
)

// 项目级 tab（解密/过滤/域名组）在无打开项目时不可见，回退到常规
const PROJECT_TABS = ['decrypt', 'capture', 'domains']
function effectiveTab(tab?: string) {
  if (tab && hasOpenProject.value) return tab
  if (tab && !PROJECT_TABS.includes(tab)) return tab
  return 'general'
}
onUnmounted(() => EventsOff('project:changed'))

// ---- 保存 ----
async function save() {
  saving.value = true
  saveWarnings.value = []
  try {
    const nu = JSON.parse(JSON.stringify(form.value)) as app.SettingsView
    const res = await SaveSettings(nu)
    emit('changed')
    // 有 warnings（如未知 @引用）时留在面板展示；否则直接关闭
    if (res?.warnings?.length) {
      saveWarnings.value = res.warnings
    } else {
      emit('update:show', false)
    }
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
  } finally {
    saving.value = false
  }
}

function close() {
  emit('update:show', false)
}
</script>

<style scoped>
/* 左侧竖排 tab：固定标签宽，内容区独立滚动 */
.settings-tabs { height: 100%; }
:deep(.n-tabs-nav) { width: 92px; }
:deep(.n-tabs-tab) { justify-content: flex-start; }
:deep(.n-tabs-pane-wrapper) { padding-left: 16px; overflow-y: auto; }
</style>
