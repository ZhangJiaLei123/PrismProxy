<template>
  <div class="flow-detail">
    <div v-if="!store.selected" class="empty">在左侧选择一条流查看详情</div>
    <template v-else>
      <div class="title">
        <n-tag size="small" :type="stateTagType">{{ store.selected.State }}</n-tag>
        <span class="url" :title="store.selected.URL">{{ store.selected.Method }} {{ store.selected.URL }}</span>
        <n-button size="tiny" type="primary" secondary class="resend-btn" :disabled="!store.selected.URL || store.selected.Method === 'CONNECT'" @click="store.openComposer(store.selected.ID)">调试重发</n-button>
      </div>
      <n-tabs type="line" size="small" style="flex: 1; min-height: 0" pane-style="height:100%;overflow:auto;padding:8px 12px">
        <n-tab-pane name="overview" tab="概览">
          <n-descriptions :column="1" size="small" label-placement="left" :label-style="{ width: '96px', color: 'rgba(255,255,255,0.55)' }">
            <n-descriptions-item label="状态">{{ store.selected.State }}<span v-if="store.selected.Err" style="color:#e88080">（{{ store.selected.Err }}）</span></n-descriptions-item>
            <n-descriptions-item label="协议">{{ detail?.ReqProto || '-' }} / {{ store.selected.Scheme }}</n-descriptions-item>
            <n-descriptions-item label="状态码">{{ store.selected.Status || '—' }}</n-descriptions-item>
            <n-descriptions-item label="开始时间">{{ fmtDateTime(store.selected.StartedAt) }}</n-descriptions-item>
            <n-descriptions-item label="耗时">{{ fmtDuration(store.selected.DurationMS) }}</n-descriptions-item>
            <n-descriptions-item label="上行">{{ fmtBytes(store.selected.BytesUp) }}</n-descriptions-item>
            <n-descriptions-item label="下行">{{ fmtBytes(store.selected.BytesDown) }}</n-descriptions-item>
            <n-descriptions-item label="客户端">{{ store.selected.ClientAddr }}</n-descriptions-item>
            <n-descriptions-item label="服务端">{{ detail?.ServerAddr || store.selected.Host }}</n-descriptions-item>
            <n-descriptions-item label="进程">
              {{ store.selected.ProcessName }} (pid={{ store.selected.PID }})
              <div v-if="detail?.ProcessPath" class="proc-path">{{ detail.ProcessPath }}</div>
            </n-descriptions-item>
          </n-descriptions>
        </n-tab-pane>

        <n-tab-pane name="request" tab="请求">
          <div class="copy-bar">
            <n-button size="tiny" quaternary @click="copyRaw('req', 'headers')">复制 Headers 原文</n-button>
            <n-button size="tiny" quaternary @click="copyRaw('req', 'body')">复制 Body</n-button>
            <n-button size="tiny" quaternary @click="copyRaw('req', 'all')">复制完整报文</n-button>
          </div>
          <h4 class="sec">Headers</h4>
          <header-table :header="detail?.ReqHeader" />
          <h4 class="sec">Body <span class="size">({{ fmtBytes(detail?.ReqBodySize ?? 0) }})</span></h4>
          <body-viewer ref="reqBody" :flow-id="store.selected.ID" which="req" />
        </n-tab-pane>

        <n-tab-pane name="response" tab="响应" :disabled="!store.selected.Status && store.selected.State === 'pending'">
          <div class="copy-bar">
            <n-button size="tiny" quaternary @click="copyRaw('resp', 'headers')">复制 Headers 原文</n-button>
            <n-button size="tiny" quaternary @click="copyRaw('resp', 'body')">复制 Body</n-button>
            <n-button size="tiny" quaternary @click="copyRaw('resp', 'all')">复制完整报文</n-button>
          </div>
          <h4 class="sec">Headers</h4>
          <header-table :header="detail?.RespHeader" />
          <h4 class="sec">Body <span class="size">({{ fmtBytes(detail?.RespBodySize ?? 0) }})</span></h4>
          <body-viewer ref="respBody" :flow-id="store.selected.ID" which="resp" />
        </n-tab-pane>

        <n-tab-pane name="tls" tab="TLS" :disabled="!detail?.TLS">
          <template v-if="detail?.TLS">
            <n-descriptions :column="1" size="small" label-placement="left" :label-style="{ width: '110px', color: 'rgba(255,255,255,0.55)' }">
              <n-descriptions-item label="客户端 TLS">{{ detail.TLS.ClientVersion }}</n-descriptions-item>
              <n-descriptions-item label="上游 TLS">{{ detail.TLS.ServerVersion }}</n-descriptions-item>
              <n-descriptions-item label="SNI">{{ detail.TLS.ServerName }}</n-descriptions-item>
            </n-descriptions>
            <h4 class="sec">上游真实证书链</h4>
            <div v-for="(c, i) in detail.TLS.PeerCerts" :key="i" class="cert">
              <div><span class="ck">Subject</span> {{ c.Subject }}</div>
              <div><span class="ck">Issuer</span> {{ c.Issuer }}</div>
              <div v-if="c.DNSNames?.length"><span class="ck">SAN</span> {{ c.DNSNames.join(', ') }}</div>
              <div><span class="ck">有效期</span> {{ fmtDate(c.NotBefore) }} ~ {{ fmtDate(c.NotAfter) }}</div>
            </div>
          </template>
        </n-tab-pane>
      </n-tabs>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import { NButton, NDescriptions, NDescriptionsItem, NTabPane, NTabs, NTag, useMessage } from 'naive-ui'
import BodyViewer from '../components/BodyViewer.vue'
import { GetFlowDetail, GetFlowRawText } from '../../wailsjs/go/main/App'
import type { main } from '../../wailsjs/go/models'
import { useFlowsStore } from '../stores/flows'
import { fmtBytes, fmtDuration, fmtDateTime } from '../lib/format'
import { copyText } from '../lib/clip'

const store = useFlowsStore()
const message = useMessage()

// 复制请求/响应原文（Headers / Body 解压后 / 完整报文），文本由 Go 侧生成
async function copyRaw(part: 'req' | 'resp', kind: 'headers' | 'body' | 'all') {
  const id = store.selectedId
  if (!id) return
  try {
    const text = await GetFlowRawText(id, part, kind)
    if (await copyText(text)) {
      const partName = part === 'req' ? '请求' : '响应'
      const kindName = kind === 'headers' ? 'Headers 原文' : kind === 'body' ? 'Body' : '完整报文'
      message.success(`已复制${partName}${kindName}`, { duration: 2000, closable: true })
    } else {
      message.error('剪贴板写入失败', { duration: 4000, closable: true })
    }
  } catch (e) {
    message.error(String(e), { duration: 5000, closable: true })
  }
}
const detail = ref<main.FlowDetail | null>(null)
const reqBody = ref<InstanceType<typeof BodyViewer> | null>(null)
const respBody = ref<InstanceType<typeof BodyViewer> | null>(null)

const stateTagType = computed(() => {
  switch (store.selected?.State) {
    case 'done': return 'success'
    case 'error': return 'error'
    case 'streaming': return 'warning'
    default: return 'default'
  }
})

async function refresh() {
  const id = store.selectedId
  if (!id) { detail.value = null; return }
  try {
    detail.value = await GetFlowDetail(id)
  } catch {
    detail.value = null
  }
}

// 选中变化或进行中流状态推进（upsert 原位替换触发）→ 刷新详情
watch(() => store.selected, async (cur, prev) => {
  await refresh()
  // 仅在流 ID 切换时重拉 body；同 ID 状态推进由轮询补
  if (cur && cur.ID !== prev?.ID) {
    reqBody.value?.reload()
    respBody.value?.reload()
  }
})

// 进行中流：800ms 轮询直到终态（body 随流增长）
let timer: ReturnType<typeof setInterval> | undefined
watch(
  () => store.selected?.State,
  (st) => {
    if (timer) { clearInterval(timer); timer = undefined }
    if (st === 'pending' || st === 'streaming') {
      timer = setInterval(() => {
        refresh()
        reqBody.value?.reload()
        respBody.value?.reload()
      }, 800)
    }
  },
  { immediate: true },
)

function fmtDate(s: string): string {
  if (!s) return '-'
  return new Date(s).toLocaleDateString('zh-CN')
}

// 内联小组件：Headers 表格
const HeaderTable = (props: { header?: Record<string, string[]> }) => {
  const entries = Object.entries(props.header ?? {}).flatMap(([k, vs]) => vs.map((v) => [k, v] as const))
  if (!entries.length) return h('div', { style: 'color:rgba(255,255,255,0.35);font-size:12px' }, '—')
  return h(
    'table',
    { style: 'border-collapse:collapse;font-size:12px;width:100%' },
    entries.map(([k, v]) =>
      h('tr', [
        h('td', { style: 'padding:2px 8px 2px 0;color:#9cdcfe;white-space:nowrap;vertical-align:top' }, k),
        h('td', { style: 'padding:2px 0;word-break:break-all;color:rgba(255,255,255,0.85)' }, v),
      ]),
    ),
  )
}
</script>

<style scoped>
.flow-detail { height: 100%; display: flex; flex-direction: column; }
.empty { padding: 40px 16px; text-align: center; color: rgba(255, 255, 255, 0.4); }
.title { display: flex; align-items: center; gap: 8px; padding: 8px 12px; border-bottom: 1px solid rgba(255,255,255,0.1); flex: none; }
.url { font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.resend-btn { margin-left: auto; flex: none; }
.sec { margin: 10px 0 4px; font-size: 12px; color: rgba(255, 255, 255, 0.6); }
.sec .size { font-weight: normal; opacity: 0.7; }
.copy-bar { display: flex; gap: 4px; margin: 2px 0 6px; }
.proc-path { font-size: 11px; color: rgba(255, 255, 255, 0.45); word-break: break-all; }
.cert { margin-bottom: 10px; font-size: 12px; line-height: 1.7; }
.ck { display: inline-block; width: 64px; color: rgba(255, 255, 255, 0.5); }
</style>
