import { onBeforeUnmount } from 'vue'
import { EventsOff, EventsOn } from '../../wailsjs/runtime/runtime'

// proxypool:status 的进程级单订阅（审计 M4）。
// 背景：Wails v2 的 EventsOff(eventName) 会按事件名注销「全部」监听者，JS 侧无句柄机制。
// AppFooter 常驻、ProxyPoolSection 随设置抽屉挂载/卸载，若各自 EventsOn/EventsOff，
// 抽屉关闭即把底栏的监听一并移除，此后底栏收不到轮换/降级推送。
// 方案：模块级只向 Wails 注册一次，组件作为监听者加入/移出集合（引用计数）。

type PoolStatusHandler = (payload: any) => void

const EVENT = 'proxypool:status'
const listeners = new Set<PoolStatusHandler>()
let registered = false

function dispatch(payload: any) {
  // 复制一份遍历：处理器内不做增删，仍防御未来改动在回调中释放
  for (const fn of [...listeners]) fn(payload)
}

/** 订阅代理池状态事件；组件卸载时自动释放，不影响其他组件的订阅。 */
export function onPoolStatusEvent(handler: PoolStatusHandler): void {
  if (!registered) {
    EventsOn(EVENT, dispatch)
    registered = true
  }
  listeners.add(handler)
  onBeforeUnmount(() => {
    listeners.delete(handler)
    if (listeners.size === 0 && registered) {
      EventsOff(EVENT)
      registered = false
    }
  })
}
