<template>
  <!-- 解读模块（explain，设计 §7.1）：单流解读，无输入项；范围说明显示 doStart 时快照的目标流。
       由 ReviewAiPanel 挂载：父持运行引擎与全部状态，本组件纯展示 -->
  <ReviewAiScopeText :text="scopeText" />

  <ReviewAiRunActions
    :phase="phase"
    :running="running"
    label="开始解读"
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
}>()
</script>
