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
    PeerCerts: Array<{ Subject: string; Issuer: string; DNSNames: string[]; NotBefore: number; NotAfter: number }>
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
}
