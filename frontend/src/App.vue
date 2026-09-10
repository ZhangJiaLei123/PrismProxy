<template>
  <n-config-provider :theme="darkTheme" style="height: 100%">
    <n-message-provider>
     <n-dialog-provider>
      <n-layout style="height: 100%">
      <!-- M11：无打开项目时全屏欢迎页（参考 IDEA Welcome），项目/设置操作后自动回到主界面 -->
      <welcome-page v-if="!hasOpenProject" @open-settings="openSettings" />
      <template v-else>
      <app-header />
      <n-layout-content :style="contentStyle">
        <!-- M12.3：数据复盘内嵌视图（走 Wails 绑定，不经 ctlapi HTTP）；复盘态隐藏抓包错误条与底栏 -->
        <review-page v-if="view === 'review'" :api="reviewApi" class="embedded-review" />
        <template v-else>
        <n-alert
          v-if="startError"
          type="error"
          closable
          style="margin: 6px 8px 0"
          @close="startError = ''"
        >
          <span style="display: flex; align-items: center; gap: 8px">
            <span>{{ startError }}</span>
            <n-button size="tiny" secondary type="error" @click="openSettings">打开设置</n-button>
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
        </template>
      </n-layout-content>
      <!-- 底栏：过滤搜索 + 代理状态/快捷开关 + 设置入口（组件内自持状态，refresh 供事件驱动）；复盘态不显示 -->
      <app-footer v-if="view === 'capture'" ref="footerRef" @open-settings="onUIOpenSettings" @error="onFooterError" @clear-error="startError = ''" />
      </template>
    </n-layout>
      <settings-panel v-model:show="showSettings" :initial-tab="settingsTab" @changed="onSettingsChanged" />
      <composer />
     </n-dialog-provider>
    </n-message-provider>
  </n-config-provider>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NConfigProvider, NLayout, NLayoutContent, NSplit, NButton, NAlert, NMessageProvider, NDialogProvider, darkTheme } from 'naive-ui'
import FlowList from './pages/FlowList.vue'
import FlowDetail from './pages/FlowDetail.vue'
import SettingsPanel from './pages/SettingsPanel.vue'
import Composer from './pages/Composer.vue'
import WelcomePage from './pages/WelcomePage.vue'
import ReviewPage from './review/pages/ReviewPage.vue'
import AppHeader from './components/AppHeader.vue'
import AppFooter from './components/AppFooter.vue'
import { useFlowsStore } from './stores/flows'
import { useProjects } from './composables/useProjects'
import { useAppView } from './composables/useAppView'
import { WailsReviewApi } from './review/api/wails'
import { EventsOn, WindowUnminimise } from '../wailsjs/runtime/runtime'

const store = useFlowsStore()
// M11：无打开项目（currentId 为空）时主界面替换为欢迎页
const { hasOpenProject } = useProjects()
// M12.3：抓包/复盘视图切换；内嵌复盘直接用 Wails 绑定适配器
const { view } = useAppView()
const reviewApi = new WailsReviewApi()
const showSettings = ref(false)
// 设置抽屉初始标签：状态标签入口定位到「网络」（代理服务），底部按钮默认「常规」
const settingsTab = ref('general')
// 启动自动抓包失败（如端口占用）的错误条，后端 proxy:start-error 事件驱动
// 底栏快捷开关的动作失败也复用该错误条（消息自带上下文前缀）
const startError = ref('')
// 底栏组件引用：代理/系统代理状态与开关可见性由其自持，事件到达时调 refresh() 同步
const footerRef = ref<InstanceType<typeof AppFooter> | null>(null)

// 全量刷新（含工具栏偏好）：挂载/设置保存后用
function refreshFooter() {
  return footerRef.value?.refresh()
}

// 仅刷代理/系统代理状态：proxy:ready / proxy:start-error 事件后用，省掉一次 GetSettings 往返
function refreshFooterStatus() {
  return footerRef.value?.refreshStatus()
}

// 设置保存后：底栏状态与开关可见性刷新
function onSettingsChanged() {
  refreshFooter()
}

// 底栏上报告警：replace=true 为用户开关动作刚失败（覆盖显示最新错误）；
// 否则为 StartError 轮询兜底（footer 内部已按错误内容去重），仅在错误条为空时恢复
function onFooterError(message: string, replace = false) {
  if (replace || !startError.value) startError.value = message
}

function openSettings() {
  settingsTab.value = 'general'
  showSettings.value = true
}

// M8：AI CLI `cli ui settings [tab]` 经 ctlapi → Wails ui:open-settings 事件驱动；
// 底栏「设置」按钮与代理状态标签（network）也走该入口
function onUIOpenSettings(tab?: string) {
  settingsTab.value = tab || 'general'
  showSettings.value = true
  WindowUnminimise() // 窗口最小化时恢复，用户才能看到抽屉
}

// 内容区：抓包态扣顶栏 40px + 底栏 28px；复盘态无底栏，占满顶栏以下全部高度
const contentStyle = computed(() => ({ height: view.value === 'review' ? 'calc(100% - 40px)' : 'calc(100% - 40px - 28px)' }))
const splitHeight = computed(() => (startError.value ? 'calc(100% - 40px)' : '100%'))

onMounted(async () => {
  EventsOn('proxy:start-error', (msg: string) => {
    startError.value = '代理启动失败：' + msg
    refreshFooter()
  })
  // 后端 startup 完成自动启动/接管后推送；onMounted 首次刷新可能更早（startCtlAPI 等耗时），
  // 事件到达时再刷一次，避免底栏开关恒显"已停止"（M8 时序竞态修复）
  EventsOn('proxy:ready', () => refreshFooter())
  EventsOn('ui:open-settings', (tab?: string) => onUIOpenSettings(tab))
  await store.init()
})
</script>
