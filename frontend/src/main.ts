import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'

// 纯浏览器预览（localhost:5173 直接打开、无 Wails webview）时，window.go/window.runtime
// 不存在——挂载前动态安装内存 Mock；wails dev/exe 中二者存在，mock 检测后自动跳过。
async function bootstrap() {
  const w = window as unknown as { go?: unknown; runtime?: unknown }
  if (!w.go || !w.runtime) {
    const { installBrowserMocks } = await import('./mocks/browser')
    await installBrowserMocks()
  }
  createApp(App).use(createPinia()).mount('#app')
}

void bootstrap()
