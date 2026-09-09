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
        <!-- 调试重发：仅真实环境（ctlapi HTTP）可用；demo/无 token 演示模式禁用 -->
        <n-tooltip v-if="composeDisabled" placement="bottom">
          <template #trigger>
            <!-- 原生 disabled 按钮不派发鼠标事件，tooltip trigger 需由外层 span 接管 -->
            <span class="resend-wrap"><n-button size="tiny" type="primary" secondary disabled>调试重发</n-button></span>
          </template>
          演示模式不支持调试重发，仅真实环境可用
        </n-tooltip>
        <n-button v-else size="tiny" type="primary" secondary class="resend-btn" :disabled="!detail.URL || detail.Method === 'CONNECT'" @click="composerShow = true">调试重发</n-button>
      </div>
      <!-- 四 Tab 展示层与主窗共用；loader 走复盘 HTTP API（归档 tags/flows 取数），无复制栏插槽 -->
      <flow-detail-tabs
        :meta="detail"
        :detail="detail"
        :flow-id="flowId"
        :loader="bodyLoader"
      />
      <review-composer
        v-model:show="composerShow"
        :api="api"
        :flow-id="flowId"
        @error="(m) => emit('error', m)"
      />
    </template>
    <div v-else class="empty">
      <n-spin size="small" />
      <div style="margin-top: 8px">加载详情中…</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NButton, NSpin, NTag, NTooltip } from 'naive-ui'
import FlowDetailTabs from '../../components/FlowDetailTabs.vue'
import ReviewComposer from './ReviewComposer.vue'
import type { BodyLoader, ReviewFlowDetail } from '../../lib/types'
import type { ReviewApi } from '../api'

const props = defineProps<{ api: ReviewApi; flowId: string }>()
const emit = defineEmits<{ (e: 'error', msg: string): void }>()

const detail = ref<ReviewFlowDetail | null>(null)
const composerShow = ref(false)
let detailSeq = 0 // 详情请求代际序号（M12 审计修复 L3：快速切流时丢弃过期响应/错误）

const composeDisabled = computed(() => props.api.mode === 'demo')

// BodyViewer 数据源：走复盘页归档 HTTP API（不碰 wailsjs；M12 解耦点）
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
    composerShow.value = false // 切流关闭遗留的重发弹窗（预填按新流重新拉取）
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
.resend-btn { margin-left: auto; flex: none; }
</style>
