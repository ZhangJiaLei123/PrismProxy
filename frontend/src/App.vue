<template>
  <n-config-provider :theme="darkTheme" style="height: 100%">
    <n-layout style="height: 100%">
      <n-layout-header bordered style="height: 40px; display: flex; align-items: center; padding: 0 12px; gap: 12px">
        <span style="font-weight: 600">PrismProxy</span>
        <n-tag size="small" :type="status.Running ? 'success' : 'error'">
          {{ status.Running ? `代理运行中 ${status.Addr}` : '代理已停止' }}
        </n-tag>
        <n-tag v-if="status.Running" size="small" type="info">{{ status.Mode }}</n-tag>
        <span style="flex: 1"></span>
        <span style="opacity: 0.7; font-size: 12px">{{ store.flows.length }} 条流</span>
        <n-button size="small" secondary @click="store.clear()">清空</n-button>
      </n-layout-header>
      <n-layout-content style="height: calc(100% - 40px - 28px)">
        <n-split direction="horizontal" :default-size="0.58" :min="0.3" :max="0.8" style="height: 100%">
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
        <n-button size="tiny" secondary @click="showSettings = true">设置</n-button>
      </n-layout-footer>
    </n-layout>
    <settings-panel v-model:show="showSettings" @changed="refreshStatus" />
  </n-config-provider>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NConfigProvider, NLayout, NLayoutHeader, NLayoutContent, NLayoutFooter, NSplit, NTag, NButton, NInput, NSelect, darkTheme } from 'naive-ui'
import FlowList from './components/FlowList.vue'
import FlowDetail from './components/FlowDetail.vue'
import SettingsPanel from './components/SettingsPanel.vue'
import { useFlowsStore } from './stores/flows'
import { GetProxyStatus } from '../wailsjs/go/main/App'
import type { main } from '../wailsjs/go/models'

const store = useFlowsStore()
const status = ref<main.ProxyStatus>({ Running: false, Addr: '', Mode: '', FlowCount: 0 } as main.ProxyStatus)
const showSettings = ref(false)

async function refreshStatus() {
  status.value = await GetProxyStatus()
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
  await store.init()
  await refreshStatus()
})
</script>

<style scoped>
.regex-invalid { color: #e88080 !important; }
</style>
