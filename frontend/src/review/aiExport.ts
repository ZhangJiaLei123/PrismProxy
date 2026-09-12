// AI 分析记录导出（解读/定位/流程）：多条记录拼装为一个 Markdown + 触发浏览器下载。
// 下载用 Blob + a[download]：WebView2 弹出保存对话框，主窗内嵌/独立窗口形态均可用。
// 模块覆盖 = 解读/定位/流程（intent 结果随流持久化在意图列表，不入历史册/导出）。

export interface AiExportRecord {
  mode: string // explain/locate/flowmap
  question: string // 提问快照（开轮时定格）
  text: string // 模型正文全文（locate 含 ```json 块原文，结构化匹配数据随文带走）
  ts: number // 开轮时间戳（旧盘数据缺失传 0，导出时跳过时间行）
}

// 有历史记录/可导出的三模块
export const AI_EXPORT_MODES = ['explain', 'locate', 'flowmap'] as const

export const MODE_LABEL: Record<string, string> = {
  explain: '解读',
  locate: '定位',
  flowmap: '流程',
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}
function fmtTs(t: number): string {
  const d = new Date(t)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}
function fmtFileTs(t: number): string {
  const d = new Date(t)
  return `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}_${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`
}

// 多条记录拼为一个 md：按模块分组（解读→定位→流程），组内按时间升序，方便自上而下阅读
export function buildRecordsMd(records: AiExportRecord[]): string {
  const lines: string[] = [
    '# AI 分析记录',
    '',
    `- 导出时间：${fmtTs(Date.now())}`,
    `- 记录数：${records.length} 条`,
    '',
  ]
  const groups = AI_EXPORT_MODES.map((m) => ({
    m,
    list: records.filter((r) => r.mode === m).sort((a, b) => a.ts - b.ts),
  })).filter((g) => g.list.length)
  for (const g of groups) {
    lines.push(`## ${MODE_LABEL[g.m] ?? g.m}`, '')
    g.list.forEach((r, i) => {
      const q = r.question.replace(/\s+/g, ' ').trim() || '（无提问）'
      lines.push(`### ${i + 1}. ${q}`, '')
      if (r.ts > 0) lines.push(`> ${fmtTs(r.ts)}`, '')
      lines.push(r.text.trim(), '', '---', '')
    })
  }
  return lines.join('\n')
}

// 下载为 md 文件（tag 用于文件名区分场景：模块名 / 导出N条）
export function downloadMd(records: AiExportRecord[], tag: string): void {
  const md = buildRecordsMd(records)
  const blob = new Blob([md], { type: 'text/markdown;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `AI分析_${tag}_${fmtFileTs(Date.now())}.md`
  document.body.appendChild(a)
  a.click()
  a.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 10_000) // 下载发起后延迟回收，兼容慢速保存对话框
}
