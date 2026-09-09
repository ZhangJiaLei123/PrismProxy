<template>
  <!--
    流详情四 Tab 展示层（主窗 FlowDetail 与复盘页 ReviewDetail 共用）：
    纯展示 + BodyViewer 数据源注入；标题栏由各自外壳渲染（主窗有复制/重发、复盘只读）。
    - meta：列表项级字段（主窗 wails 选中对象 / 复盘 DTO 结构同构）
    - detail：完整详情（headers/TLS/Proto 等）；主窗初始为 null 时按拉取结果兜底展示
    - loader：不传时 BodyViewer 回落 wails 绑定（主窗）；复盘页传 HTTP 版
    - respDisabled：主窗 pending 流禁响应 Tab；复盘恒可用
    - req-copy / resp-copy：复制栏插槽（仅主窗使用，CopyBar 依赖 wails）
  -->
  <n-tabs type="line" size="small" style="flex: 1; min-height: 0" pane-style="height:100%;overflow:auto;padding:8px 12px">
    <n-tab-pane name="overview" tab="概览">
      <n-descriptions :column="1" size="small" label-placement="left" :label-style="{ width: '96px', color: 'rgba(255,255,255,0.55)' }">
        <n-descriptions-item label="状态">{{ meta.State }}<span v-if="meta.Err" style="color:#e88080">（{{ meta.Err }}）</span></n-descriptions-item>
        <n-descriptions-item label="协议">{{ d?.ReqProto || '-' }} / {{ meta.Scheme }}</n-descriptions-item>
        <n-descriptions-item label="状态码">{{ meta.Status || '—' }}</n-descriptions-item>
        <n-descriptions-item label="开始时间">{{ fmtDateTime(meta.StartedAt) }}</n-descriptions-item>
        <n-descriptions-item label="耗时">{{ fmtDuration(meta.DurationMS) }}</n-descriptions-item>
        <n-descriptions-item label="上行">{{ fmtBytes(meta.BytesUp) }}</n-descriptions-item>
        <n-descriptions-item label="下行">{{ fmtBytes(meta.BytesDown) }}</n-descriptions-item>
        <n-descriptions-item v-if="showEndpoints" label="客户端">{{ meta.ClientAddr }}</n-descriptions-item>
        <n-descriptions-item v-if="showEndpoints" label="服务端">{{ d?.ServerAddr || meta.Host }}</n-descriptions-item>
        <n-descriptions-item label="进程">
          {{ meta.ProcessName }} (pid={{ meta.PID }})
          <div v-if="d?.ProcessPath" class="proc-path">{{ d.ProcessPath }}</div>
        </n-descriptions-item>
        <n-descriptions-item label="标签">
          <template v-if="meta.Tags && meta.Tags.length">
            <n-tag v-for="t in meta.Tags" :key="t" size="small" :bordered="false" class="fd-tag">{{ t }}</n-tag>
          </template>
          <span v-else style="color: rgba(255,255,255,0.35)">—</span>
        </n-descriptions-item>
      </n-descriptions>
    </n-tab-pane>

    <n-tab-pane name="request" tab="请求">
      <slot name="req-copy" />
      <h4 class="sec">Headers</h4>
      <header-table :header="d?.ReqHeader" />
      <h4 class="sec">Body <span class="size">({{ fmtBytes(d?.ReqBodySize ?? 0) }})</span></h4>
      <body-viewer ref="reqBody" :flow-id="flowId" which="req" :loader="loader" />
    </n-tab-pane>

    <n-tab-pane name="response" tab="响应" :disabled="respDisabled">
      <slot name="resp-copy" />
      <h4 class="sec">Headers</h4>
      <header-table :header="d?.RespHeader" />
      <h4 class="sec">Body <span class="size">({{ fmtBytes(d?.RespBodySize ?? 0) }})</span></h4>
      <body-viewer ref="respBody" :flow-id="flowId" which="resp" :loader="loader" />
    </n-tab-pane>

    <n-tab-pane name="tls" tab="TLS" :disabled="!d?.TLS">
      <template v-if="d?.TLS">
        <n-descriptions :column="1" size="small" label-placement="left" :label-style="{ width: '110px', color: 'rgba(255,255,255,0.55)' }">
          <n-descriptions-item label="客户端 TLS">{{ d.TLS.ClientVersion }}</n-descriptions-item>
          <n-descriptions-item label="上游 TLS">{{ d.TLS.ServerVersion }}</n-descriptions-item>
          <n-descriptions-item label="SNI">{{ d.TLS.ServerName }}</n-descriptions-item>
        </n-descriptions>
        <h4 class="sec">上游真实证书链</h4>
        <div v-for="(c, i) in d.TLS.PeerCerts" :key="i" class="cert">
          <div><span class="ck">Subject</span> {{ c.Subject }}</div>
          <div><span class="ck">Issuer</span> {{ c.Issuer }}</div>
          <div v-if="c.DNSNames?.length"><span class="ck">SAN</span> {{ c.DNSNames.join(', ') }}</div>
          <div><span class="ck">有效期</span> {{ fmtCertDate(c.NotBefore) }} ~ {{ fmtCertDate(c.NotAfter) }}</div>
        </div>
      </template>
    </n-tab-pane>
  </n-tabs>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { NDescriptions, NDescriptionsItem, NTabPane, NTabs, NTag } from 'naive-ui'
import BodyViewer from './BodyViewer.vue'
import HeaderTable from './HeaderTable.vue'
import type { BodyLoader, ReviewFlowDetail, ReviewFlowMeta } from '../lib/types'
import { fmtBytes, fmtDuration, fmtDateTime } from '../lib/format'

const props = withDefaults(
  defineProps<{
    meta: ReviewFlowMeta
    detail?: ReviewFlowDetail | null
    flowId: string
    loader?: BodyLoader
    respDisabled?: boolean
    /** 是否展示客户端/服务端地址行（复盘归档 DTO 亦含，两端统一开启）。 */
    showEndpoints?: boolean
  }>(),
  { detail: null, loader: undefined, respDisabled: false, showEndpoints: true },
)

const reqBody = ref<InstanceType<typeof BodyViewer> | null>(null)
const respBody = ref<InstanceType<typeof BodyViewer> | null>(null)

// detail 未到位时（主窗首帧）用 meta 兜底，保证协议/地址等字段不空
const d = computed<ReviewFlowDetail | null>(() => props.detail ?? (props.meta as ReviewFlowDetail))

/**
 * 证书有效期：wails 绑定的 Go time.Time 为 RFC3339 字符串；
 * 复盘 DTO 类型标注为 unix 秒数字（time.Time 无 json tag 实际亦为字符串），两种形态都兼容。
 */
function fmtCertDate(v: unknown): string {
  if (v === null || v === undefined || v === '') return '-'
  if (typeof v === 'number') {
    // 秒级（< 1e12）与毫秒级时间戳都兜住
    return new Date(v < 1e12 ? v * 1000 : v).toLocaleDateString('zh-CN')
  }
  const t = new Date(String(v)).getTime()
  return Number.isNaN(t) ? '-' : new Date(t).toLocaleDateString('zh-CN')
}

/** 主窗进行中流 800ms 轮询时由外壳调用：重拉请求/响应正文。 */
function reloadBodies() {
  reqBody.value?.reload()
  respBody.value?.reload()
}

defineExpose({ reloadBodies })
</script>

<style scoped>
.sec { margin: 10px 0 4px; font-size: 12px; color: rgba(255, 255, 255, 0.6); }
.sec .size { font-weight: normal; opacity: 0.7; }
.proc-path { font-size: 11px; color: rgba(255, 255, 255, 0.45); word-break: break-all; }
.cert { margin-bottom: 10px; font-size: 12px; line-height: 1.7; }
.ck { display: inline-block; width: 64px; color: rgba(255, 255, 255, 0.5); }
.fd-tag { margin: 0 6px 4px 0; color: #c0a8f0; background: rgba(181, 126, 220, 0.16); }
</style>
