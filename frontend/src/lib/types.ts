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
