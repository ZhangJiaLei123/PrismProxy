// mermaid 水合共享出口（ReviewAiLogSheet / ReviewAiHistorySheet 共用）：
// 动态 import mermaid（独立分包，无图不加载），渲染成功替换原代码块；
// 解析失败（图源不完整/语法错）保留代码块降级展示——v-html 重写 DOM 后标记自然失效，
// 下一帧或重触发时自动重试。
import DOMPurify from 'dompurify'

type Mermaid = (typeof import('mermaid'))['default']
let mermaidLoading: Promise<Mermaid> | null = null
let mmdSeq = 0

export function ensureMermaid(): Promise<Mermaid> {
  mermaidLoading ??= import('mermaid')
    .then((m) => {
      m.default.initialize({ startOnLoad: false, theme: 'dark', securityLevel: 'strict' })
      return m.default
    })
    .catch((e) => {
      mermaidLoading = null // 加载失败允许下次重试；失败期间 mermaid 块保持代码块降级
      throw e
    })
  return mermaidLoading
}

// per-root 去重：同一容器水合进行中再次触发转为收尾补跑一次（流式高频触发防重入）
interface HydrateState {
  busy: boolean
  pending: boolean
}
const states = new WeakMap<Element, HydrateState>()

export async function hydrateMermaidIn(root: Element | null): Promise<void> {
  if (!root) return
  const s = states.get(root) ?? { busy: false, pending: false }
  states.set(root, s)
  if (s.busy) {
    s.pending = true
    return
  }
  const pairs = [...root.querySelectorAll('pre > code.language-mermaid')]
    .map((code) => ({ code, pre: code.parentElement }))
    .filter((p): p is { code: Element; pre: HTMLElement } => p.pre !== null)
  if (!pairs.length) return
  s.busy = true
  try {
    // 先挂渲染中角标（含 mermaid 库首次动态加载的等待期），成功随节点替换消失
    for (const { pre } of pairs) pre.classList.add('mmd-loading')
    const mm = await ensureMermaid()
    for (const { code, pre } of pairs) {
      const src = code.textContent ?? ''
      // parse 门禁：语法完整才进入渲染。流式中图源不完整解析失败→静默保留代码块
      // （loading 角标已随上文挂载，此处需摘除），避免每帧闪烁；语法错误终态同为代码块
      const ok = await mm.parse(src, { suppressErrors: true }).catch(() => false)
      if (!ok) {
        pre.classList.remove('mmd-loading')
        continue
      }
      const id = `mmd-${++mmdSeq}`
      try {
        const { svg } = await mm.render(id, src)
        const holder = document.createElement('div')
        holder.className = 'ai-md-mermaid'
        // mermaid 产物过一遍净化（strict 模式已禁交互，此处兜底 SVG 注入面）
        // foreignObject 是 HTML 集成点：缺 HTML_INTEGRATION_POINTS 时即使放行标签，
        // 其内部 HTML 也会被整体清空（图只剩框线无文字，浏览器实测踩坑）
        holder.innerHTML = DOMPurify.sanitize(svg, {
          USE_PROFILES: { svg: true, html: true },
          ADD_TAGS: ['foreignObject'],
          HTML_INTEGRATION_POINTS: { foreignobject: true },
        })
        pre.replaceWith(holder)
      } catch {
        document.getElementById(id)?.remove() // render 失败清理 mermaid 残留元素，保留原代码块
        pre.classList.remove('mmd-loading') // 恢复代码块观感，下轮触发可重试
      }
    }
  } catch {
    // mermaid 库加载失败：摘除全部角标，保持代码块降级（下次触发重试加载）
    for (const { pre } of pairs) pre.classList.remove('mmd-loading')
  } finally {
    s.busy = false
    if (s.pending) {
      s.pending = false
      void hydrateMermaidIn(root)
    }
  }
}
