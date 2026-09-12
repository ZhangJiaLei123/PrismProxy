// AI 会话持久化（阶段三）：模块级单例 store——对话轮次/调用日志/上一轮归档/locate 匹配。
// 以共享 ref 常驻内存（组件卸载/切模式不丢），500ms 防抖写 IndexedDB（跨 WebView 重载恢复）。
// 内存是单一事实源；UI 折叠态（open/txtOpen/sysOpen/usrOpen/touched/txtTouched）不入盘。
import { reactive, ref } from 'vue'
import type { AiMatchItem } from '../lib/types'
import type { AiUsage } from './api'

export interface ConvTurn {
  mode?: string // 开轮时所在 AI 模式（explain/intent/locate/flowmap）：主 md 区按模式取末轮，防跨模式串显/误截断（审计修复）
  ts?: number // 开轮时间戳（历史记录列表展示/导出排序用；旧盘数据缺失按空处理，UI 隐藏时间、导出跳过该行）
  question: string // 本轮提问快照（开轮时快照：locate/flowmap 为自然语言，explain/intent 回显范围说明）
  system: string
  user: string
  reason: string
  text: string
  // 以下六项为 UI 折叠态，不入盘（正文折叠独立 touched：手动收起思考不打扰正文自动展开，反之亦然）
  open: boolean
  txtOpen: boolean
  sysOpen: boolean
  usrOpen: boolean
  touched: boolean
  txtTouched: boolean
  finishReason: string
  usage?: AiUsage
}

export type LogKind = 'start' | 'meta' | 'delta' | 'intent' | 'match' | 'error' | 'done' | 'stop'
export interface LogEntry {
  id: number
  t: number
  kind: LogKind
  msg: string
}

// 每模式末次会话结果（解读/定位/流程的「上次记录」）：关轮定稿时覆盖写入。
// 与 convTurns 末轮反查并存的原因：intent 单条轮询每条流一个轮次，大量标注会把其他
// 模式的轮次挤出 MAX_TURNS 上限（prune 丢弃）；早期落盘轮次也无 mode 字段——两类场景
// 下「按 mode 反查 convTurns 末轮」都会落空，此处独立记录保证各模式上次结果始终可回显
export interface ModeResult {
  question: string // 本轮提问快照（开轮时的 runAsked：locate/flowmap 为自然语言，explain 为范围说明）
  text: string // 模型正文全文（locate 含 ```json 块原文，展示层负责截块）
  ts: number // 关轮定稿时间戳
}

const MAX_TURNS = 200
const MAX_CHARS = 3_000_000
const MAX_LOGS = 500
const DB_NAME = 'prismproxy'
const STORE = 'kv'
const KEY = 'ai-session:v1'
const SAVE_DELAY = 500

// 模块级单例：常驻内存，切模式 tab / 组件卸载不丢
export const convTurns = ref<ConvTurn[]>([])
export const logs = ref<LogEntry[]>([])
export const prevLogs = ref<LogEntry[]>([])
export const matches = ref<AiMatchItem[]>([])
export const lastResults = ref<Record<string, ModeResult>>({})

let logSeq = 0
let saveTimer: number | null = null
let hydrated = false
let epoch = 0 // 递增代数：clearSession 后使在途 hydrate 失效，防止已清数据复活
let dbPromise: Promise<IDBDatabase> | null = null

function getDb(): Promise<IDBDatabase> {
  if (!dbPromise) {
    dbPromise = new Promise((resolve, reject) => {
      const req = indexedDB.open(DB_NAME, 1)
      req.onupgradeneeded = () => {
        if (!req.result.objectStoreNames.contains(STORE)) req.result.createObjectStore(STORE)
      }
      req.onsuccess = () => resolve(req.result)
      req.onerror = () => reject(req.error ?? new Error('IndexedDB open failed'))
    })
    dbPromise.catch(() => {
      dbPromise = null // 打开失败允许下次重试
    })
  }
  return dbPromise
}

function kvGet<T>(key: string): Promise<T | undefined> {
  return getDb().then(
    (db) =>
      new Promise<T | undefined>((resolve, reject) => {
        const req = db.transaction(STORE, 'readonly').objectStore(STORE).get(key)
        req.onsuccess = () => resolve(req.result as T | undefined)
        req.onerror = () => reject(req.error ?? new Error('IndexedDB get failed'))
      })
  )
}

function kvPut(key: string, value: unknown): Promise<void> {
  return getDb().then(
    (db) =>
      new Promise<void>((resolve, reject) => {
        const req = db.transaction(STORE, 'readwrite').objectStore(STORE).put(value, key)
        req.onsuccess = () => resolve()
        req.onerror = () => reject(req.error ?? new Error('IndexedDB put failed'))
      })
  )
}

function kvDel(key: string): Promise<void> {
  return getDb().then(
    (db) =>
      new Promise<void>((resolve, reject) => {
        const req = db.transaction(STORE, 'readwrite').objectStore(STORE).delete(key)
        req.onsuccess = () => resolve()
        req.onerror = () => reject(req.error ?? new Error('IndexedDB delete failed'))
      })
  )
}

type PersistTurn = Pick<
  ConvTurn,
  'mode' | 'ts' | 'question' | 'system' | 'user' | 'reason' | 'text' | 'finishReason'
> & { usage?: AiUsage }
interface PersistState {
  v: 1
  logSeq: number
  turns: PersistTurn[]
  logs: LogEntry[]
  prevLogs: LogEntry[]
  matches: AiMatchItem[]
  // 可选字段：v 不升级以兼容旧盘数据（缺失时按空恢复）
  lastResults?: Record<string, ModeResult>
}

function toPersistTurn(t: ConvTurn): PersistTurn {
  const p: PersistTurn = {
    mode: t.mode,
    ts: t.ts,
    question: t.question,
    system: t.system,
    user: t.user,
    reason: t.reason,
    text: t.text,
    finishReason: t.finishReason,
  }
  if (t.usage) p.usage = t.usage
  return p
}

function turnChars(t: ConvTurn): number {
  return t.question.length + t.system.length + t.user.length + t.reason.length + t.text.length
}

function prune(): void {
  let chars = 0
  for (const t of convTurns.value) chars += turnChars(t)
  while (convTurns.value.length > MAX_TURNS || (chars > MAX_CHARS && convTurns.value.length > 1)) {
    const dropped = convTurns.value.shift()
    if (dropped) chars -= turnChars(dropped)
  }
  if (logs.value.length > MAX_LOGS) logs.value = logs.value.slice(-MAX_LOGS)
  if (prevLogs.value.length > MAX_LOGS) prevLogs.value = prevLogs.value.slice(-MAX_LOGS)
}

function persist(): void {
  saveTimer = null
  prune()
  const state: PersistState = {
    v: 1,
    logSeq,
    turns: convTurns.value.map(toPersistTurn),
    logs: logs.value,
    prevLogs: prevLogs.value,
    matches: matches.value,
    lastResults: lastResults.value,
  }
  void kvPut(KEY, state).catch(() => {})
}

export function scheduleSave(): void {
  if (saveTimer !== null) return
  saveTimer = setTimeout(persist, SAVE_DELAY)
}

// 启动恢复：读 IndexedDB 补齐内存（幂等）；epoch 守卫防止 clearSession 后在途恢复复活数据
export async function hydrateAiSession(): Promise<void> {
  if (hydrated) return
  hydrated = true
  const myEpoch = epoch
  try {
    const raw = await kvGet<PersistState>(KEY)
    if (!raw || raw.v !== 1 || myEpoch !== epoch) return
    const restored = raw.turns.map(
      (t): ConvTurn =>
        reactive<ConvTurn>({
          mode: t.mode,
          ts: t.ts,
          question: t.question ?? '',
          system: t.system ?? '',
          user: t.user ?? '',
          reason: t.reason ?? '',
          text: t.text ?? '',
          open: false,
          txtOpen: false,
          sysOpen: false,
          usrOpen: false,
          touched: false,
          txtTouched: false,
          finishReason: t.finishReason ?? '',
          usage: t.usage,
        })
    )
    convTurns.value = restored.concat(convTurns.value)
    if (!logs.value.length && raw.logs?.length) logs.value = raw.logs
    if (!prevLogs.value.length && raw.prevLogs?.length) prevLogs.value = raw.prevLogs
    if (!matches.value.length && raw.matches?.length) matches.value = raw.matches
    // 键级合并（内存优先）：kvGet 在途期间 closeTurn 可能已写入新结果，不得被旧盘覆盖——
    // 与 logs/matches 的内存优先恢复语义对齐（...undefined 安全兼容旧盘缺字段）
    lastResults.value = { ...raw.lastResults, ...lastResults.value }
    logSeq = Math.max(logSeq, raw.logSeq ?? 0)
    scheduleSave()
  } catch {
    // 恢复失败静默：内存态照常工作
  }
}

export function createTurn(question: string, system: string, user: string, mode?: string): ConvTurn {
  const t = reactive<ConvTurn>({
    mode,
    ts: Date.now(),
    question,
    system,
    user,
    reason: '',
    text: '',
    open: false,
    txtOpen: false,
    sysOpen: false,
    usrOpen: false,
    touched: false,
    txtTouched: false,
    finishReason: '',
  })
  convTurns.value.push(t)
  scheduleSave()
  return t
}

export function addLog(kind: LogKind, msg: string): void {
  logSeq += 1
  logs.value.push({ id: logSeq, t: Date.now(), kind, msg })
  if (logs.value.length > MAX_LOGS) logs.value.shift()
  scheduleSave()
}

// 本轮日志归档为“上一轮”（新 run 全量清场时调用）；无日志返回 false
export function archiveLogs(): boolean {
  if (!logs.value.length) return false
  prevLogs.value = logs.value
  logs.value = []
  scheduleSave()
  return true
}

export function clearSession(): void {
  convTurns.value = []
  logs.value = []
  prevLogs.value = []
  matches.value = []
  lastResults.value = {}
  logSeq = 0
  if (saveTimer !== null) {
    clearTimeout(saveTimer)
    saveTimer = null
  }
  epoch += 1 // 使在途 hydrate 失效
  void kvDel(KEY).catch(() => {})
}

// 页面隐藏时兜底落盘（防抖未到期即离开）
if (typeof window !== 'undefined') {
  window.addEventListener('pagehide', () => {
    if (saveTimer !== null) {
      clearTimeout(saveTimer)
      persist()
    }
  })
}
