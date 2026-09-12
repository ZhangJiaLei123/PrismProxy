import { ref } from 'vue'
import { CheckUpdate } from '../../wailsjs/go/app/App'
import type { app } from '../../wailsjs/go/models'

// 更新检查的模块级单例（与 useAppView/useProjects 同模式）：
// App.vue 启动时按设置静默检查一次，设置面板「常规」tab 读取同一状态展示，
// 无需 props 透传，也避免开关面板重复请求 GitHub API。
// sessionStorage 缓存 1 小时：跨组件与面板重开共享；同一次程序运行内不重复打 API。
const CACHE_KEY = 'prismproxy:update-check'
const CACHE_TTL = 60 * 60 * 1000

const info = ref<app.UpdateInfo | null>(null)
const busy = ref(false)
const checking = ref(false) // 启动/自动检查是否已发起（含进行中）
// 仅手动点按钮完成检查后才显示「已是最新版本」反馈（静默检查无更新不打扰）
const manualChecked = ref(false)
const error = ref('')

function loadCache(): boolean {
  try {
    const raw = sessionStorage.getItem(CACHE_KEY)
    if (!raw) return false
    const cached = JSON.parse(raw) as { ts: number; info: app.UpdateInfo }
    if (Date.now() - cached.ts < CACHE_TTL) {
      info.value = cached.info
      return true
    }
  } catch {
    // 缓存损坏视为未缓存
  }
  return false
}

// silent=true：失败不显红字、无更新不置 manualChecked（启动自动检查 / 进面板兜底）
async function checkUpdate(silent = false) {
  busy.value = true
  if (!silent) error.value = ''
  try {
    const res = await CheckUpdate()
    info.value = res
    if (!silent) manualChecked.value = true
    sessionStorage.setItem(CACHE_KEY, JSON.stringify({ ts: Date.now(), info: res }))
  } catch (e) {
    info.value = null
    if (!silent) error.value = '检查更新失败：' + String(e)
  } finally {
    busy.value = false
  }
}

// 启动自动检查：命中 1 小时缓存直接展示；否则静默请求。
// autoEnabled=false（用户在设置里关掉「启动自动检查更新」）时完全不请求。
function startAutoCheck(autoEnabled: boolean) {
  if (checking.value || !autoEnabled) return
  checking.value = true
  if (loadCache()) return
  checkUpdate(true)
}

// 手动「检查更新」：强制刷新，忽略缓存
function manualCheck() {
  return checkUpdate(false)
}

// 打开外链：Wails 浏览器打开，浏览器预览降级 window.open（与 AppHeader.openGithub 同模式）
function openRelease(url: string) {
  const wailsRuntime = (window as unknown as { runtime?: { BrowserOpenURL?: (u: string) => void } }).runtime
  if (wailsRuntime?.BrowserOpenURL) {
    wailsRuntime.BrowserOpenURL(url)
  } else {
    window.open(url, '_blank')
  }
}

export function useUpdate() {
  return {
    info,
    busy,
    manualChecked,
    error,
    startAutoCheck,
    manualCheck,
    openRelease,
  }
}
