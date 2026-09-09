<template>
  <div class="review-detail">
    <div v-if="!flowId" class="empty">
      <div class="empty-icon">📋</div>
      <div>在右侧列表选择一条流查看详情</div>
      <div class="empty-sub">归档正文随标签永久保存，不受录制开关与保留策略影响</div>
    </div>
    <template v-else-if="detail">
      <div class="title">
        <n-tag size="small" :type="stateTagType">{{ detail.State }}</n-tag>
        <span class="url" :title="detail.URL">{{ detail.Method }} {{ detail.URL }}</span>
        <n-button size="tiny" quaternary title="归档正文只读，无需刷新">
          <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor" style="opacity: 0.5">
            <path d="M2 1.5h8.2L14 5.3V14.5H2V1.5zm1.5 1.5v10h9V6.1L8.9 3H3.5zm2 6l2.2 2.2L11 8.4l-1.1-1.1-1.7 1.7-1.1-1.1L5.5 9z" />
          </svg>
        </n-button>
      </div>
      <n-tabs type="line" size="small" style="flex: 1; min-height: 0" pane-style="height:100%;overflow:auto;padding:8px 12px">
        <n-tab-pane name="overview" tab="概览">
          <n-descriptions :column="1" size="small" label-placement="left" :label-style="{ width: '96px', color: 'rgba(255,255,255,0.55)' }">
            <n-descriptions-item label="标签">
              <template v-if="detail.Tags && detail.Tags.length">
                <n-tag v-for="t in detail.Tags" :key="t" size="small" :bordered="false" class="rv-tag">{{ t }}</n-tag>
              </template>
              <span v-else style="color: rgba(255,255,255,0.35)">—</span>
            </n-descriptions-item>
            <n-descriptions-item label="状态">
              {{ detail.State }}<span v-if="detail.Err" style="color:#e88080">（{{ detail.Err }}）</span>
            </n-descriptions-item>
            <n-descriptions-item label="状态码">{{ detail.Status || '—' }}</n-descriptions-item>
            <n-descriptions-item label="开始时间">{{ fmtDateTime(detail.StartedAt) }}</n-descriptions-item>
            <n-descriptions-item label="耗时">{{ fmtDuration(detail.DurationMS) }}</n-descriptions-item>
            <n-descriptions-item label="上行 / 下行">{{ fmtBytes(detail.BytesUp) }} / {{ fmtBytes(detail.BytesDown) }}</n-descriptions-item>
            <n-descriptions-item label="进程">
              {{ detail.ProcessName }} (pid={{ detail.PID }})
              <div v-if="detail.ProcessPath" class="proc-path">{{ detail.ProcessPath }}</div>
            </n-descriptions-item>
          </n-descriptions>
        </n-tab-pane>

        <n-tab-pane name="request" tab="请求">
          <h4 class="sec">Headers</h4>
          <header-table :header="detail.ReqHeader" />
          <h4 class="sec">Body <span class="size">({{ fmtBytes(detail.ReqBodySize) }})</span></h4>
          <body-viewer :flow-id="flowId" which="req" :loader="bodyLoader" />
        </n-tab-pane>

        <n-tab-pane name="response" tab="响应">
          <h4 class="sec">Headers</h4>
          <header-table :header="detail.RespHeader" />
          <h4 class="sec">Body <span class="size">({{ fmtBytes(detail.RespBodySize) }})</span></h4>
          <body-viewer :flow-id="flowId" which="resp" :loader="bodyLoader" />
        </n-tab-pane>

        <n-tab-pane v-if="detail.TLS" name="tls" tab="TLS">
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
          </div>
        </n-tab-pane>
      </n-tabs>
    </template>
    <div v-else class="empty">
      <n-spin size="small" />
      <div style="margin-top: 8px">加载详情中…</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NDescriptions,
  NDescriptionsItem,
  NSpin,
  NTabPane,
  NTabs,
  NTag,
} from 'naive-ui'
import BodyViewer from '../components/BodyViewer.vue'
import HeaderTable from '../components/HeaderTable.vue'
import type { BodyLoader } from '../lib/types'
import type { ReviewApi } from './api'
import type { ReviewFlowDetail } from '../lib/types'
import { fmtBytes, fmtDuration, fmtDateTime } from '../lib/format'

const props = defineProps<{ api: ReviewApi; flowId: string }>()
const emit = defineEmits<{ (e: 'error', msg: string): void }>()

const detail = ref<ReviewFlowDetail | null>(null)
let detailSeq = 0 // 详情请求代际序号（M12 审计修复 L3：快速切流时丢弃过期响应/错误）

// BodyViewer 数据源：走复盘页 HTTP API（不碰 wailsjs；M12 解耦点）
const bodyLoader: BodyLoader = {
  loadBody: (id, which) => props.api.flowBody(id, which),
  loadDetail: (id) => props.api.flowDetail(id),
}

const stateTagType = computed(() => {
  switch (detail.value?.State) {
    case 'done': return 'success'
    case 'error': return 'error'
    case 'streaming': return 'warning'
    default: return 'default'
  }
})

watch(
  () => props.flowId,
  async (id) => {
    const my = ++detailSeq
    detail.value = null
    if (!id) return
    try {
      const d = await props.api.flowDetail(id)
      // A 慢 B 快：过期响应不得覆盖当前流详情
      if (my !== detailSeq) return
      detail.value = d
    } catch (e) {
      if (my !== detailSeq) return // 过期 reject 不误导错误提示
      emit('error', String((e as Error)?.message ?? e))
    }
  },
  { immediate: true },
)
</script>

<style scoped>
.review-detail { height: 100%; display: flex; flex-direction: column; min-width: 0; }
.empty { padding: 48px 16px; text-align: center; color: rgba(255, 255, 255, 0.4); display: flex; flex-direction: column; align-items: center; gap: 6px; }
.empty-icon { font-size: 28px; opacity: 0.6; }
.empty-sub { font-size: 11px; color: rgba(255, 255, 255, 0.3); }
.title { display: flex; align-items: center; gap: 8px; padding: 8px 12px; border-bottom: 1px solid rgba(255,255,255,0.1); flex: none; }
.url { font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sec { margin: 10px 0 4px; font-size: 12px; color: rgba(255, 255, 255, 0.6); }
.sec .size { font-weight: normal; opacity: 0.7; }
.proc-path { font-size: 11px; color: rgba(255, 255, 255, 0.45); word-break: break-all; }
.cert { margin-bottom: 10px; font-size: 12px; line-height: 1.7; }
.ck { display: inline-block; width: 64px; color: rgba(255, 255, 255, 0.5); }
.rv-tag { margin: 0 6px 4px 0; color: #c0a8f0; background: rgba(181, 126, 220, 0.16); }
</style>
