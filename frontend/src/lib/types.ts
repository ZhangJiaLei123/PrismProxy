// 跨入口共享类型（主窗 wails 绑定形态与复盘页 HTTP 形态通用）。

/** BodyViewer 数据源注入点：复盘页传 fetch 版实现，主窗不传（组件内回落 wails 绑定）。 */
export interface BodyLoader {
  loadBody: (flowId: string, which: 'req' | 'resp') => Promise<unknown>
  loadDetail: (flowId: string) => Promise<unknown>
}

// 复盘页 HTTP DTO（ctl_bridge 响应形态，设计 §4.4/§6.2）：
// TagInfo 带小写 json tag；FlowMeta/FlowDetail/BodyPayload 无 json tag → 大写字段。

export interface ReviewTagInfo {
  id: string
  name: string
  count: number
  createdAt: number
  lastUsedAt: number
}

export interface ReviewFlowMeta {
  ID: string
  State: string
  Scheme: string
  Method: string
  Host: string
  Path: string
  URL: string
  Status: number
  DurationMS: number
  BytesUp: number
  BytesDown: number
  ProcessName: string
  PID: number
  ClientAddr: string
  StartedAt: number
  Err: string
  Pinned: boolean
  Source: string
  Historical: boolean
  Tags: string[]
}

export interface ReviewFlowDetail extends ReviewFlowMeta {
  ReqURL: string
  ReqProto: string
  ReqHeader: Record<string, string[]>
  ReqBodySize: number
  RespProto: string
  RespHeader: Record<string, string[]>
  RespBodySize: number
  ProcessPath: string
  ServerAddr: string
  TLS?: {
    ClientVersion: string
    ServerVersion: string
    ServerName: string
    // 证书有效期：wails/Go time.Time 无 json tag → RFC3339 字符串；demo mock 为 unix 秒数（fmtCertDate 双形态兼容）
    PeerCerts: Array<{ Subject: string; Issuer: string; DNSNames: string[]; NotBefore: string | number; NotAfter: string | number }>
  } | null
}

export interface ReviewBodyPayload {
  Encoding: string
  ContentType: string
  Truncated: boolean
  Raw: string
  Body: string
  DecodeErr: string
}

/** POST /api/v1/compose 请求体（与 Go app.ComposedRequest 同构，小写 json tag）。 */
export interface ReviewComposedRequest {
  method: string
  url: string
  headers: Array<{ key: string; value: string }>
  body: string
  skipVerify: boolean
}

export interface ReviewTagResult {
  tag: ReviewTagInfo
  tagged: number
  archived: number
  skipped: number
}

// M12.1 复盘取数范围：archived=打标流（默认）；all=库内全部 flows（含自动录制未打标）。
export type ReviewScope = 'archived' | 'all'

/** /api/v1/tags 根级响应：标签列表 + total（去重打标流数）+ totalFlows（库内全部流）。 */
export interface ReviewTagsOverview {
  tags: ReviewTagInfo[]
  total: number
  totalFlows: number
}

/** 密度直方图分桶（[t0,t1) 半开，末桶右端闭）。 */
export interface ReviewHistBucket {
  t0: number
  t1: number
  count: number
}

/** GET /tags/{id}/histogram 响应：全域/窗口边界与分桶。 */
export interface ReviewHistogram {
  start: number
  end: number
  buckets: ReviewHistBucket[]
}

/** 列表查询参数（start/end 为 unix 毫秒含头尾，0=不限，支持半开窗口；q 为 method/host/path 子串）。 */
export interface ReviewFlowQuery {
  scope: ReviewScope
  start: number
  end: number
  limit: number
  offset: number
  q: string
  /** M12.2 服务端排序：time|method|status|host|path|size|proc；空=time。 */
  sort?: ReviewSortKey
  /** asc|desc；空走服务端默认（time=desc，其余=asc）。 */
  dir?: ReviewSortDir
  /** true=显示被忽略数据（眼睛开启，不拼排除条件）；默认 false 隐藏。 */
  showIgnored?: boolean
}

/** 可排序列（与后端 flowOrderBy 白名单一致）。 */
export type ReviewSortKey = 'time' | 'method' | 'status' | 'host' | 'path' | 'size' | 'proc'
export type ReviewSortDir = 'asc' | 'desc'

/** M12.2 忽略名单类型：host=域名（含子域/端口口径）；path=路径前缀；proc=进程名；method=HTTP 方法等值；status=状态码等值。 */
export type ReviewIgnoreKind = 'host' | 'path' | 'proc' | 'method' | 'status'

/** review_ignores 表一行（小写 json）。 */
export interface ReviewIgnoreItem {
  kind: ReviewIgnoreKind
  value: string
  createdAt: number
  note: string
}

/** GET /tags/ignores 响应。 */
export interface ReviewIgnoreList {
  ignores: ReviewIgnoreItem[]
}

/** POST /tags/ignores 响应：added=false 表示已存在（幂等，仅刷新 note）。 */
export interface ReviewIgnoreAddResult {
  ignore: ReviewIgnoreItem
  added: boolean
}

// ===== M13 复盘 AI 分析（设计稿 §5.3/§7）=====

/** 意图标注单条结果（intent 事件载荷；useIntents 会话内存缓存 + 摘要条展示）。 */
export interface IntentResult {
  flowId: string
  /** 模型输出序号（对应送审 [#n]，重析覆盖以最新为准） */
  seq: number
  intent: string
  confidence: 'high' | 'medium' | 'low'
  /** 模型判不准（如全是无语义路径），提示用户带正文重析 */
  needsBody: boolean
}

/** 智能定位单条匹配（match 事件载荷，面板可点击卡片跳转选中流）。 */
export interface AiMatchItem {
  flowId: string
  rank: number
  method: string
  url: string
  reason: string
  confidence: 'high' | 'medium' | 'low'
}
