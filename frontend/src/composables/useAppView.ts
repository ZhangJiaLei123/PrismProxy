import { ref } from 'vue'

// 主窗顶层视图切换：capture=抓包（FlowList+FlowDetail）；review=内嵌数据复盘（ReviewPage）。
// 模块级单例（与 useProjects 同模式），AppHeader 切、App.vue 渲染，无需 props/emits 透传。
export type AppView = 'capture' | 'review'
const view = ref<AppView>('capture')

export function useAppView() {
  return {
    view,
    isReview: () => view.value === 'review',
    setView(v: AppView) {
      view.value = v
    },
  }
}
