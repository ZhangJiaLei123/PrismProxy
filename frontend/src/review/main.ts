// 数据复盘页独立入口（设计 §6.1）：独立 createApp，不依赖 Wails runtime
// （系统浏览器中 window.go/window.runtime 不存在）；数据全部走 fetch（api.ts）。
// provider 必须挂在 ReviewApp 的祖先——useMessage/useDialog 只能在 provider
// 子组件的 setup 中调用，组件自身模板里包 provider 不算数（naive-ui 约束）。
import { createApp, h } from 'vue'
import { NConfigProvider, NDialogProvider, NMessageProvider, darkTheme, zhCN, dateZhCN } from 'naive-ui'
import ReviewApp from './ReviewApp.vue'
import './review.css'

function Root() {
  return h(
    NConfigProvider,
    { theme: darkTheme, locale: zhCN, dateLocale: dateZhCN },
    { default: () => h(NMessageProvider, null, { default: () => h(NDialogProvider, null, { default: () => h(ReviewApp) }) }) },
  )
}

createApp(Root).mount('#app')
