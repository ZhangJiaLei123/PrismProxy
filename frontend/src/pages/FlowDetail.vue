<template>
  <div class="flow-detail">
    <div v-if="!store.selected" class="empty">在左侧选择一条流查看详情</div>
    <template v-else>
      <div class="title">
        <n-tag size="small" :type="stateTagType">{{ store.selected.State }}</n-tag>
        <span class="url" :title="store.selected.URL">{{ store.selected.Method }} {{ store.selected.URL }}</span>
        <n-button size="tiny" type="primary" secondary class="resend-btn" :disabled="!store.selected.URL || store.selected.Method === 'CONNECT'" @click="store.openComposer(store.selected.ID)">调试重发</n-button>
      </div>
      <!-- 四 Tab 展示层与复盘页共用；复制栏走插槽（CopyBar 依赖 wails 绑定），不传 loader 回落 wails 取数 -->
      <flow-detail-tabs
        ref="tabs"
        :meta="store.selected"
        :detail="detail"
        :flow-id="store.selected.ID"
        :resp-disabled="!store.selected.Status && store.selected.State === 'pending'"
      >
        <template #req-copy><copy-bar :flow-id="store.selected.ID" part="req" /></template>
        <template #resp-copy><copy-bar :flow-id="store.selected.ID" part="resp" /></template>
      </flow-detail-tabs>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NButton, NTag } from 'naive-ui'
import FlowDetailTabs from '../components/FlowDetailTabs.vue'
import CopyBar from '../components/CopyBar.vue'
import { GetFlowDetail } from '../../wailsjs/go/app/App'
import type { app } from '../../wailsjs/go/models'
import { useFlowsStore } from '../stores/flows'

const store = useFlowsStore()

const detail = ref<app.FlowDetail | null>(null)
const tabs = ref<InstanceType<typeof FlowDetailTabs> | null>(null)

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
  if (cur && cur.ID !== prev?.ID) tabs.value?.reloadBodies()
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
        tabs.value?.reloadBodies()
      }, 800)
    }
  },
  { immediate: true },
)
</script>

<style scoped>
.flow-detail { height: 100%; display: flex; flex-direction: column; }
.empty { padding: 40px 16px; text-align: center; color: rgba(255, 255, 255, 0.4); }
.title { display: flex; align-items: center; gap: 8px; padding: 8px 12px; border-bottom: 1px solid rgba(255,255,255,0.1); flex: none; }
.url { font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.resend-btn { margin-left: auto; flex: none; }
</style>
