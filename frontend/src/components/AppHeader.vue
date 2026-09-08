<template>
  <n-layout-header bordered style="height: 40px; display: flex; align-items: center; padding: 0 12px; gap: 12px">
    <!-- M9：项目切换器（规则/历史随项目隔离，运行中热切换） -->
    <project-switcher />
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
</template>

<script setup lang="ts">
import { NLayoutHeader, NButton } from 'naive-ui'
import ProjectSwitcher from './ProjectSwitcher.vue'
import { useFlowsStore } from '../stores/flows'

const store = useFlowsStore()

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
</script>
