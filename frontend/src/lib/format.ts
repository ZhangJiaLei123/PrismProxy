export function fmtBytes(n: number): string {
  if (!n) return '-'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(2)} MB`
}

export function fmtDuration(ms: number): string {
  if (!ms) return '-'
  if (ms < 1000) return `${ms} ms`
  return `${(ms / 1000).toFixed(2)} s`
}

export function fmtTime(unixMilli: number): string {
  if (!unixMilli) return '-'
  const d = new Date(unixMilli)
  const p = (x: number, l = 2) => String(x).padStart(l, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${p(d.getMilliseconds(), 3)}`
}

/** 完整时间：YYYY-MM-DD HH:mm:ss.SSS（详情概览用，保留毫秒精度） */
export function fmtDateTime(unixMilli: number): string {
  if (!unixMilli) return '-'
  const d = new Date(unixMilli)
  const p = (x: number, l = 2) => String(x).padStart(l, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${p(d.getMilliseconds(), 3)}`
}

/** base64 → Uint8Array（Wails []byte 经 JSON 编为 base64） */
export function b64ToBytes(b64: string): Uint8Array {
  if (!b64) return new Uint8Array(0)
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

export function bytesToText(b: Uint8Array): string {
  return new TextDecoder('utf-8', { fatal: false }).decode(b)
}
