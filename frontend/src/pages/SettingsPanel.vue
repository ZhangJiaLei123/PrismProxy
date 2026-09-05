<template>
  <n-drawer :show="show" :width="640" placement="right" @update:show="close">
    <n-drawer-content title="设置" closable>
      <n-tabs v-model:value="activeTab" type="line" placement="left" :bar-width="200" class="settings-tabs">
        <!-- ============ 常规：系统代理（实时控制）+ 根证书 ============ -->
        <n-tab-pane name="general" tab="常规">
          <general-tab :form="form" @changed="emit('changed')" />
        </n-tab-pane>

        <!-- ============ 网络：监听 / 上游 / 存储 / 绕过列表 ============ -->
        <n-tab-pane name="network" tab="网络">
          <network-tab :form="form" />
        </n-tab-pane>

        <!-- ============ 解密规则 ============ -->
        <n-tab-pane name="decrypt" tab="解密规则">
          <decrypt-tab :form="form" />
        </n-tab-pane>

        <!-- ============ 过滤规则：黑白名单规则组 ============ -->
        <n-tab-pane name="capture" tab="过滤规则">
          <!-- 规则导入即时落盘 + 热更新，导入后重新拉取设置刷新表单 -->
          <capture-tab :form="form" @imported="loadSettings" />
        </n-tab-pane>

        <!-- ============ 域名组：导入/导出/删除（即时生效，不走表单保存） ============ -->
        <n-tab-pane name="domains" tab="域名组">
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
import { ref, watch } from 'vue'
import { NDrawer, NDrawerContent, NTabs, NTabPane, NButton, NAlert, useMessage } from 'naive-ui'
import GeneralTab from './settings/GeneralTab.vue'
import NetworkTab from './settings/NetworkTab.vue'
import DecryptTab from './settings/DecryptTab.vue'
import CaptureTab from './settings/CaptureTab.vue'
import DomainsTab from './settings/DomainsTab.vue'
import { GetSettings, SaveSettings } from '../../wailsjs/go/main/App'
import type { settings } from '../../wailsjs/go/models'

const props = defineProps<{ show: boolean; initialTab?: string }>()
const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
  (e: 'changed'): void // 设置已保存或系统代理状态变化（父组件刷新头部状态）
}>()

// ---- 表单状态（各 tab 通过 props 共享修改，保存时统一提交） ----
const emptyForm = (): settings.Settings =>
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
  }) as settings.Settings

const form = ref<settings.Settings>(emptyForm())
const activeTab = ref('general')
const saving = ref(false)
const saveWarnings = ref<string[]>([])
// 保存错误用全局悬浮 message（App.vue 的 n-message-provider 提供）
const message = useMessage()

// ---- 载入（打开面板时；规则导入后由 CaptureTab @imported 触发重载） ----
async function loadSettings() {
  const s = await GetSettings()
  form.value = JSON.parse(JSON.stringify(s)) as settings.Settings
  form.value.bypassList ??= []
  form.value.decryptRules ??= []
  form.value.filterGroups ??= []
  for (const g of form.value.filterGroups) {
    g.hosts ??= []
    g.paths ??= []
    g.processes ??= []
  }
}

// M8：面板打开时加载配置并定位 tab；面板已打开时外部（cli ui settings <tab>）
// 改 initialTab 只切 tab，不重载表单（避免丢失用户正在编辑的内容）。
watch(
  () => [props.show, props.initialTab] as const,
  async ([v], [was]) => {
    if (v && !was) {
      activeTab.value = props.initialTab || 'general'
      saveWarnings.value = []
      await loadSettings()
    } else if (v && props.initialTab) {
      activeTab.value = props.initialTab
    }
  },
)

// ---- 保存 ----
async function save() {
  saving.value = true
  saveWarnings.value = []
  try {
    const nu = JSON.parse(JSON.stringify(form.value)) as settings.Settings
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
