<template>
  <!-- 意图模块（intent，设计 §7.2）：批量/单条轮询标注，无 Markdown 区（delta 里的列表/JSON 不展示，仅收 intent 帧）。
       由 ReviewAiPanel 挂载：父持运行引擎与全部状态，本组件纯展示 -->
  <ReviewAiScopeText :text="scopeText" />

  <!-- 正文开关（对齐后端 OR 语义）+ 单条轮询：逐条独立调用模型，规避整批 prompt 过大导致的截断/首响应超时（批量标注实测红线） -->
  <div class="ai-opts">
    <n-checkbox
      :checked="includeReq"
      :disabled="phase === 'streaming'"
      @update:checked="emit('update:includeReq', $event)"
    >包含请求正文</n-checkbox>
    <n-checkbox
      :checked="singleMode"
      :disabled="phase === 'streaming'"
      title="每条流单独调用一次模型，慢但稳，可规避批量 prompt 过大导致的输出截断或超时"
      @update:checked="emit('update:singleMode', $event)"
    >单条轮询处理</n-checkbox>
  </div>

  <ReviewAiRunActions
    :phase="phase"
    :running="running"
    label="开始标注"
    :start-disabled="startDisabled"
    :need-confirm="needConfirm"
    :meta-text="metaText"
    @start="emit('start')"
    @stop="emit('stop')"
  />

  <!-- intent 进度（n/total，tabular-nums 防数字抖动） -->
  <n-progress
    v-if="phase !== 'idle'"
    type="line"
    :percentage="pct"
    :show-indicator="false"
    class="ai-progress"
  />

  <div v-if="phase === 'error'" class="ai-error">{{ errMsg }}</div>

  <!-- 结果列表：候选池里有结果的，按模型输出序号排（排序/过滤在父级 intentResults） -->
  <div class="ai-intents">
    <div
      v-for="it in results"
      :key="it.flowId"
      class="ai-intent-row"
      :title="rowTip(it)"
      @click="emit('locate', it.flowId)"
    >
      <span class="ai-intent-seq">#{{ it.seq }}</span>
      <span class="ai-intent-text" :class="{ 'k-ai-low': it.confidence === 'low', 'k-ai-needs-body': it.needsBody }">{{ it.intent }}</span>
      <span class="ai-intent-flow">{{ labelOf(it.flowId) }}</span>
    </div>
    <div v-if="!results.length && phase !== 'idle'" class="ai-hint">{{ phase === 'streaming' ? '标注中…' : '暂无结果' }}</div>
  </div>
</template>

<script setup lang="ts">
import { NCheckbox, NProgress } from 'naive-ui'
import type { IntentResult } from '../../lib/types'
import ReviewAiRunActions from './ReviewAiRunActions.vue'
import ReviewAiScopeText from './ReviewAiScopeText.vue'

defineProps<{
  scopeText: string
  phase: 'idle' | 'streaming' | 'done' | 'stopped' | 'error'
  /** 全局运行中（面板 phase==='streaming'）：透传 RunActions，跨 tab 驱动停止按钮/运行 pill */
  running: boolean
  startDisabled: boolean
  needConfirm: boolean
  metaText: string
  errMsg: string
  /** 进度百分比（父级按单条轮询快照/批量 total 计算） */
  pct: number
  /** 结果行（父级 intentResults：候选池有结果者按 seq 排） */
  results: IntentResult[]
  /** flowId → 「METHOD 路径」标签（父级 flowLabelOf，候选池实时映射） */
  labelOf: (id: string) => string
  includeReq: boolean
  singleMode: boolean
}>()

const emit = defineEmits<{
  (e: 'start'): void
  (e: 'stop'): void
  (e: 'locate', flowId: string): void
  (e: 'update:includeReq', v: boolean): void
  (e: 'update:singleMode', v: boolean): void
}>()

function rowTip(it: IntentResult): string {
  const tips: string[] = ['点击在列表中定位该流']
  if (it.confidence === 'low') tips.push('AI 对此判定置信度较低，仅供参考')
  if (it.needsBody) tips.push('需结合请求正文才能准确判定；可勾选「包含请求正文」重新标注')
  return tips.join('；')
}
</script>
