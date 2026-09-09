<template>
  <n-layout-header bordered style="height: 40px; display: flex; align-items: center; padding: 0 12px; gap: 12px">
    <!-- M9：项目切换器（规则/历史随项目隔离，运行中热切换） -->
    <project-switcher />
    <!-- M12：数据复盘入口（系统浏览器独立窗口；mock 预览降级 window.open） -->
    <n-button size="small" quaternary aria-label="数据复盘" title="在新窗口打开数据复盘页（已归档/已标记流量）" @click="openReview">
      <span style="display: inline-flex; align-items: center; gap: 4px">
        <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor" aria-hidden="true">
          <path d="M3 1h6l4 4v10H3V1zm6 1.5V5h2.5L9 2.5zM4.5 8h7v1.2h-7V8zm0 2.5h7v1.2h-7v-1.2zm0 2.5h5v1.2h-5V13z"/>
        </svg>
        数据复盘
        <svg viewBox="0 0 16 16" width="11" height="11" fill="currentColor" aria-hidden="true">
          <path d="M6 2v1.5h5.2L1.5 13.2l1 1L12.3 4.5V9.8H14V2H6z"/>
        </svg>
      </span>
    </n-button>
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
    <!-- M12：给当前过滤可见流打标签并归档（清空按钮左侧）；可见流为 0 时禁用 -->
    <n-button
      size="small"
      secondary
      aria-label="归档"
      title="归档(将当前列表中的数据打标签并归档保存)"
      :disabled="!store.filtered.length"
      @click="markShow = true"
    >
      <svg viewBox="0 0 16 16" width="16" height="16" fill="currentColor" aria-hidden="true">
        <path d="M2 1.5h8.2L14 5.3V14.5H2V1.5zm1.5 1.5v10h9V6.1L8.9 3H3.5zm2 3h5V7.5h-5V6zm0 2.8h5v1.5h-5V8.8zm0 2.8h3.4v1.5H5.5v-1.5z"/>
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
    <!-- M12 标记弹窗（名称/勾选态仅本次运行缓存，组件内自持） -->
    <tag-mark-dialog v-model:show="markShow" :count="store.filtered.length" />
  </n-layout-header>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { NLayoutHeader, NButton, useMessage } from 'naive-ui'
import ProjectSwitcher from './ProjectSwitcher.vue'
import TagMarkDialog from './TagMarkDialog.vue'
import { useFlowsStore } from '../stores/flows'
import { GetReviewURL } from '../../wailsjs/go/app/App'

const store = useFlowsStore()
const message = useMessage()

const markShow = ref(false)

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

// M12：数据复盘——后端返回 ctlapi 托管地址（含一次性 token），用系统浏览器开独立窗口
async function openReview() {
  try {
    const url = await GetReviewURL()
    const wailsRuntime = (window as unknown as { runtime?: { BrowserOpenURL?: (u: string) => void } }).runtime
    if (wailsRuntime?.BrowserOpenURL) {
      wailsRuntime.BrowserOpenURL(url)
    } else {
      // mock/浏览器预览：GetReviewURL 返回同源相对地址，直接新标签打开
      window.open(url, '_blank', 'noopener,noreferrer')
    }
  } catch (e) {
    message.error(String(e), { duration: 6000, closable: true })
  }
}
</script>
