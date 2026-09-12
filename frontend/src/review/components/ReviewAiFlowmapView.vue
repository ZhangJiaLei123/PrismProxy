<template>
  <!-- 流程模块（flowmap）：自然语言目标 → 模型正文 Markdown，无正文开关（后端默认必带正文）。
       由 ReviewAiPanel 挂载：父持运行引擎与全部状态，本组件纯展示 -->
  <ReviewAiScopeText :text="scopeText" />

  <!-- 模式专属输入：自然语言目标 -->
  <n-input
    :value="question"
    type="textarea"
    :rows="2"
    placeholder="例：梳理下单流程的接口调用顺序"
    :disabled="phase === 'streaming'"
    @update:value="emit('update:question', $event)"
  />

  <ReviewAiRunActions
    :phase="phase"
    :running="running"
    label="开始分析"
    :start-disabled="startDisabled"
    :need-confirm="needConfirm"
    :meta-text="metaText"
    @start="emit('start')"
    @stop="emit('stop')"
  />

  <div v-if="phase === 'error'" class="ai-error">{{ errMsg }}</div>

  <ReviewAiMdView :md="md" :pending="pending" :continue-state="continueState" />
</template>

<script setup lang="ts">
import { NInput } from 'naive-ui'
import type { AiChatNotice } from '../api'
import ReviewAiRunActions from './ReviewAiRunActions.vue'
import ReviewAiScopeText from './ReviewAiScopeText.vue'
import ReviewAiMdView from './ReviewAiMdView.vue'

defineProps<{
  scopeText: string
  phase: 'idle' | 'streaming' | 'done' | 'stopped' | 'error'
  /** 全局运行中（面板 phase==='streaming'）：透传 RunActions，跨 tab 驱动停止按钮/运行 pill */
  running: boolean
  startDisabled: boolean
  needConfirm: boolean
  metaText: string
  errMsg: string
  question: string
  /** 模型正文（父级 displayMd：当前模式末轮输出） */
  md: string
  /** 首 token 前骨架占位 */
  pending: boolean
  /** 截断自动续写状态条（父级视图门控：仅发起模式 tab 可见） */
  continueState?: AiChatNotice | null
}>()

const emit = defineEmits<{
  (e: 'start'): void
  (e: 'stop'): void
  (e: 'update:question', v: string): void
}>()
</script>
