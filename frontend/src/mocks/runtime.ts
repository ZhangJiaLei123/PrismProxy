// 浏览器预览专用：Wails runtime 垫片。
// 仅在 window.runtime 缺失（纯浏览器，非 wails dev/exe webview）时由 browser.ts 安装。
// 事件总线语义对齐 Wails：EventsOnMultiple(eventName, cb, maxCallbacks)，max<0 = 无限。
/* eslint-disable @typescript-eslint/no-explicit-any */

type Listener = { cb: (...args: any[]) => void; max: number; n: number }

const listeners = new Map<string, Listener[]>()

export function emit(eventName: string, ...args: any[]) {
  const ls = listeners.get(eventName)
  if (!ls) return
  // 拷贝一份遍历：回调内可能 EventsOff 改变原数组
  for (const l of [...ls]) {
    l.n += 1
    if (l.max >= 0 && l.n > l.max) {
      const i = ls.indexOf(l)
      if (i >= 0) ls.splice(i, 1)
    }
    try {
      l.cb(...args)
    } catch (e) {
      console.error('[mock-runtime] event listener error:', eventName, e)
    }
  }
}

function offEvent(eventName: string) {
  // Wails 的 EventsOff(name)：取消该事件全部监听（前端用法均为整事件退订）
  listeners.delete(eventName)
}

export function installRuntimeMock() {
  const noop = () => {}
  const fakeRuntime: Record<string, any> = {
    EventsOnMultiple: (eventName: string, cb: (...args: any[]) => void, maxCallbacks: number) => {
      if (!listeners.has(eventName)) listeners.set(eventName, [])
      listeners.get(eventName)!.push({ cb, max: maxCallbacks ?? -1, n: 0 })
    },
    EventsOff: (eventName: string) => offEvent(eventName),
    EventsOffAll: () => listeners.clear(),
    EventsEmit: emit,
    // 日志/窗口/系统/通知类 API：浏览器里静默降级
    LogPrint: noop,
    LogTrace: noop,
    LogDebug: noop,
    LogInfo: noop,
    LogWarning: noop,
    LogError: noop,
    LogFatal: noop,
    WindowReload: () => window.location.reload(),
    WindowReloadApp: () => window.location.reload(),
    WindowSetAlwaysOnTop: noop,
    WindowSetSystemDefaultTheme: noop,
    WindowSetLightTheme: noop,
    WindowSetDarkTheme: noop,
    WindowCenter: noop,
    WindowSetTitle: noop,
    WindowFullscreen: noop,
    WindowUnfullscreen: noop,
    WindowIsFullscreen: () => false,
    WindowGetSize: () => ({ w: window.innerWidth, h: window.innerHeight }),
    WindowSetSize: noop,
    WindowSetMaxSize: noop,
    WindowSetMinSize: noop,
    WindowSetPosition: noop,
    WindowGetPosition: () => ({ x: 0, y: 0 }),
    WindowHide: noop,
    WindowShow: noop,
    WindowMaximise: noop,
    WindowToggleMaximise: noop,
    WindowUnmaximise: noop,
    WindowIsMaximised: () => false,
    WindowMinimise: noop,
    WindowUnminimise: noop,
    WindowIsMinimised: () => false,
    WindowIsNormal: () => true,
    WindowSetBackgroundColour: noop,
    ScreenGetAll: () => [],
    BrowserOpenURL: (url: string) => window.open(url, '_blank', 'noopener,noreferrer'),
    Environment: () => ({
      buildType: 'browser-mock',
      platform: navigator.platform,
      arch: '',
      OS: 'browser',
    }),
    Quit: noop,
    Hide: noop,
    Show: noop,
    ClipboardGetText: () => navigator.clipboard?.readText?.() ?? Promise.resolve(''),
    ClipboardSetText: (t: string) => navigator.clipboard?.writeText?.(t),
    OnFileDrop: noop,
    OnFileDropOff: noop,
    CanResolveFilePaths: () => false,
    ResolveFilePaths: () => [],
    InitializeNotifications: noop,
    CleanupNotifications: noop,
    IsNotificationAvailable: () => false,
    RequestNotificationAuthorization: () => Promise.resolve(false),
    CheckNotificationAuthorization: () => Promise.resolve(false),
    SendNotification: noop,
    SendNotificationWithActions: noop,
    RegisterNotificationCategory: noop,
    RemoveNotificationCategory: noop,
    RemoveAllPendingNotifications: noop,
    RemovePendingNotification: noop,
    RemoveAllDeliveredNotifications: noop,
    RemoveDeliveredNotification: noop,
    RemoveNotification: noop,
  }
  ;(window as any).runtime = fakeRuntime
}
