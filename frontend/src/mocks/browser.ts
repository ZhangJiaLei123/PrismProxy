// 浏览器预览引导：在纯浏览器（Wails webview 之外）中安装 mock runtime 与假后端，
// 并显示「Mock 预览」角标。wails dev / exe 环境 window.go 与 window.runtime 存在，
// 本文件虽被 main.ts 动态 import，但内部检测会直接跳过、不覆盖任何真实对象。
/* eslint-disable @typescript-eslint/no-explicit-any */
import { installRuntimeMock } from './runtime'
import { installAppMock } from './app'

function isWailsEnv(): boolean {
  return !!(window as any).go?.app?.App || !!(window as any).runtime?.EventsOnMultiple
}

function showBadge() {
  const el = document.createElement('div')
  el.title = '当前为纯浏览器预览：数据为内存 Mock，抓包/CA/ADB 等真实能力不可用'
  el.textContent = 'Mock 预览'
  Object.assign(el.style, {
    position: 'fixed',
    right: '8px',
    bottom: '32px',
    zIndex: 9999,
    padding: '2px 10px',
    borderRadius: '10px',
    font: '12px/20px sans-serif',
    color: '#fff',
    background: 'rgba(202, 138, 4, 0.85)',
    cursor: 'default',
    pointerEvents: 'none',
  } as CSSStyleDeclaration)
  document.body.appendChild(el)
}

export async function installBrowserMocks(): Promise<boolean> {
  if (isWailsEnv()) return false
  installRuntimeMock()
  installAppMock()
  if (document.body) showBadge()
  else window.addEventListener('DOMContentLoaded', showBadge)
  console.info('%c[PrismProxy] 浏览器预览模式：已启用内存 Mock（无真实后端）', 'color:#ca8a04')
  return true
}
