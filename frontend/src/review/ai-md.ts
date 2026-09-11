// AI 输出 Markdown 渲染唯一出口（M13 P4-2，AC12 锚点）：
// marked 解析 + DOMPurify 净化（AI 输出是不可信 HTML，必须净化后再 v-html）。
// 全项目 v-html 仅允许经此模块的返回值渲染。
import { marked } from 'marked'
import DOMPurify from 'dompurify'

marked.setOptions({ async: false, gfm: true, breaks: false })

// 链接强制新窗口 + noopener（防 reverse tabnabbing）；hook 全局只挂一次
let hookInstalled = false
function installLinkHook() {
  if (hookInstalled) return
  hookInstalled = true
  DOMPurify.addHook('afterSanitizeAttributes', (node) => {
    if (!(node instanceof Element)) return
    node.querySelectorAll('a[href]').forEach((a) => {
      a.setAttribute('target', '_blank')
      a.setAttribute('rel', 'noopener noreferrer')
    })
  })
}

/** Markdown 文本 → 净化后的 HTML（v-html 唯一入口，调用方一律经此函数）。 */
export function renderMarkdown(text: string): string {
  installLinkHook()
  const html = marked.parse(text ?? '') as string
  return DOMPurify.sanitize(html, { USE_PROFILES: { html: true } })
}
