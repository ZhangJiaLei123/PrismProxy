// 前端版 cURL 生成：复盘归档流不在实时内存、Go BuildCurl 无法直接用，
// 故在前端用已加载的详情/请求体拼装。
//
// cmd 输出已对齐 Chrome 152 DevTools「Copy as cURL (cmd)」格式（2026-09-10 用户指定样例拍板，
// 规则取自 devtools-frontend NetworkLogView.ts _asCurlCommand/escapeStringWin）：
//   ① 裸 curl 开头、首参 --url URL，URL 内 {} [] 加反斜杠防 curl globbing；
//   ② 方法仅在与推断方法（有请求体→POST，无请求体→GET）不一致时才输出 -X；
//   ③ 头按 key 字典序（Go DTO 是 map，JSON 序列化即 ASCII 字典序，与样例一致），
//      cookie（且值含 '='）改用 -b，无 '=' 回退 -H 防 curl 读文件，空白值输出 'k;'；
//   ④ 请求体放 --data-raw 压在最后；参数段 ≥3 才多行，分隔符 ' ^\n  '，否则单行；
//   ⑤ cmd 转义：\→\\、"→\"，再给白名单 [A-Za-z0-9\s_-:=+~/'.,?;*()] 之外的字符加 ^，
//      后跟标识符的 % 改 %^，整体 ^"..."^ 包裹（所以 "→^\^"、{→^{、|→^|）。
//   与 DevTools 唯一刻意差异：非 ASCII（如中文）不替换成空格——原样保留以保证重放成功，
//   现代 Windows Terminal/cmd 按活动代码页仍可粘贴执行。
//
// bash 转义移植 DevTools escapeStringPosix（含控制字符/!/单引号时用 ANSI-C $'...'，
// 否则普通单引号），形态同为多行（\ 续行、两空格缩进）。
// Go 侧 internal/capture/curl.go 已同步为本套规则（2026-09-10，主窗 FlowList 右键 + CLI flows curl；
// 两侧均只支持 cmd/bash，默认 cmd）。
// body 内联规则（文本且 ≤64KB 且未截断才内联）两侧共用，改规则时必须同步本文件与 Go 侧。

export type CurlShell = 'cmd' | 'bash'

export interface CurlInput {
  url: string
  method?: string
  headers?: Record<string, string[]>
  body?: Uint8Array
  bodyTruncated?: boolean
}

export interface CurlResult {
  command: string
  bodyOmitted: boolean
}

// 文本启发式：含 NUL 字节视为二进制，不内联
function isText(data: Uint8Array): boolean {
  for (let i = 0; i < data.length; i++) {
    if (data[i] === 0) return false
  }
  return true
}

// 内联请求体上限：64KB（对齐 Go 侧 MAX_INLINE_BODY）
const MAX_INLINE_BODY = 64 * 1024

// 不输出到 cURL 的首部：代理逐跳/自动维护头 + accept-encoding（对齐 DevTools ignoredHeaders）
// Connection/Proxy-Connection/Keep-Alive/Transfer-Encoding 代理层记录前已剥离（见 server.go），
// 此处保留仅为防御；host 由 curl 按 URL 自动补；content-length 让 curl 按 body 自动补。
const SKIP_HEADERS = new Set([
  'connection',
  'proxy-connection',
  'keep-alive',
  'transfer-encoding',
  'host',
  'content-length',
  'accept-encoding',
])

// cmd 转义（逐字节移植 Chrome DevTools escapeStringWin，顺序不可调换）：
//   1) \→\\、"→\"（先过 MS Crt 解析器）；
//   2) 白名单 [A-Za-z0-9\s_-:=+~/'.,?;*()] 之外的每个字符前加 ^（过 cmd.exe 解析器），
//      故 "→^\^"、{→^{、|→^|、`→^`；白名单含 ()（Chrome 152 实测，用户样例 UA 中括号无 ^）；
//   3) 后跟字母/数字/下划线的 % 改成 %^，避免 MS Crt 当环境变量展开；
//   4) 其余控制字符（含 TAB）→空格，防命令注入；换行→^加两个换行（cmd 续行转义）；
//   5) 整体 ^"..."^ 包裹。
// 与 DevTools 唯一刻意差异：第 4 步不替换非 ASCII（中文等原样保留，保证重放成功）。
function quoteCmd(s: string): string {
  const r = s
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"')
    // 只给「白名单外的 ASCII（含 DEL）」加 ^；\u0080+ 非 ASCII（中文等）刻意原样保留（见文件头）
    .replace(/(?=[\x21-\x7e])[^a-zA-Z0-9\s_\-:=+~'\/.',?;*()]/g, '^$&')
    .replace(/%(?=[a-zA-Z0-9_])/g, '%^')
    .replace(/[\x00-\x09\x0b\x0c\x0e-\x1f\x7f]/g, ' ')
    .replace(/\r?\n|\r/g, '^\n\n')
  return '^"' + r + '^"'
}

// bash 转义（移植 DevTools escapeStringPosix）：含控制字符/DEL-0x9f/!/单引号时用
// ANSI-C 引用 $'...'（!→\u0021 防历史展开、控制字符→\uXXXX），否则普通单引号包裹。
function quoteBash(s: string): string {
  if (/[\0-\x1f\x7f-\x9f!]|'/.test(s)) {
    const toHex = (ch: string): string => '\\u' + ch.charCodeAt(0).toString(16).padStart(4, '0')
    return (
      "$'" +
      s
        .replace(/\\/g, '\\\\')
        .replace(/'/g, "\\'")
        .replace(/\n/g, '\\n')
        .replace(/\r/g, '\\r')
        .replace(/[\0-\x1f\x7f-\x9f!]/g, toHex) +
      "'"
    )
  }
  return "'" + s + "'"
}

// URL 额外转义（DevTools 在 escapeString 之后无分平台统一处理）：{} [] 前加 \ 防 curl globbing。
function escapeUrl(quoted: string): string {
  return quoted.replace(/[[{}\]]/g, '\\$&')
}

/**
 * 生成 cURL 命令（Chrome DevTools 形态）：首段 curl --url URL，其后 -X/-H/-b/--data-raw
 * 各占一段；参数段 ≥3 时多行（行尾 cmd `^` / bash `\` 续行，次行两空格缩进），否则单行。
 * 入参 url 为空时抛错，与 Go BuildCurl 的错误口径一致。
 */
export function buildCurl(input: CurlInput, shell: CurlShell): CurlResult {
  if (!input.url) {
    throw new Error('url is empty')
  }

  const quote = shell === 'cmd' ? quoteCmd : quoteBash

  // 按逻辑行收集：首行=可执行文件+--url，其后每段参数独占一行
  const lines: string[] = []

  // 可执行文件本身（curl）不含特殊字符，无需引号包裹
  lines.push('curl --url ' + escapeUrl(quote(input.url)))

  // 先判定 body 能否内联：DevTools 推断方法依据「是否有 data 段」——有内联 body→POST，否则 GET；
  // body 被省略（二进制/超限/截断）时必须显式 -X POST，否则 curl 会默认发 GET。
  let bodyOmitted = false
  let inlineBody: string | null = null
  if (input.body && input.body.length > 0) {
    if (isText(input.body) && input.body.length <= MAX_INLINE_BODY && !input.bodyTruncated) {
      inlineBody = new TextDecoder('utf-8', { fatal: false }).decode(input.body)
    } else {
      // 二进制/超限/已截断：不内联请求体，交由 UI 提示用户手工补
      bodyOmitted = true
    }
  }

  // 与推断方法不一致才输出 -X（对齐 DevTools）
  const method = (input.method || '').toUpperCase()
  const inferred = inlineBody !== null ? 'POST' : 'GET'
  if (method && method !== inferred) {
    lines.push('-X ' + quote(method))
  }

  // 首部按 key 字典序排序（对齐 DevTools，Go map JSON 序列化本身也是该顺序）
  const keys = Object.keys(input.headers || {}).filter((k) => !SKIP_HEADERS.has(k.toLowerCase())).sort()
  for (const k of keys) {
    for (const v of input.headers![k]) {
      // 值为空或纯空白的头输出 k;（curl 语法：分号表示空头）
      if (!v.trim()) {
        lines.push('-H ' + quote(k + ';'))
        continue
      }
      // cookie 仅在含 '=' 时用 -b；无 '=' 时 curl 会把值当 cookie 文件名读取（安全风险），回退 -H
      if (k.toLowerCase() === 'cookie' && v.includes('=')) {
        lines.push('-b ' + quote(v))
        continue
      }
      lines.push('-H ' + quote(k + ': ' + v))
    }
  }

  if (inlineBody !== null) {
    // DevTools 两平台均用 --data-raw（原样保留反斜杠等，不做 @ 文件展开）
    lines.push('--data-raw ' + quote(inlineBody))
  }

  // DevTools 规则：参数段 ≥3 才多行（分隔符 cmd ' ^\n  ' / bash ' \\\n  '，次行两空格缩进），
  // 否则单行空格连接；lines[0] 已含裸 curl 前缀。
  const sep = lines.length >= 3 ? (shell === 'cmd' ? ' ^\n  ' : ' \\\n  ') : ' '
  return { command: lines.join(sep), bodyOmitted }
}
