<template>
  <!-- 定位模块（locate）：自然语言目标 → 模型正文（截掉 ```json 块的展示视图由父给）+ 匹配卡片列表。
       由 ReviewAiPanel 挂载：父持运行引擎与全部状态，本组件纯展示 -->
  <ReviewAiScopeText :text="scopeText" />

  <!-- 模式专属输入：自然语言目标 -->
  <n-input
    :value="question"
    type="textarea"
    :rows="2"
    placeholder="例：找出登录接口的响应位置"
    :disabled="phase === 'streaming'"
    @update:value="emit('update:question', $event)"
  />

  <!-- 正文开关（对齐后端 OR 语义） -->
  <div class="ai-opts">
    <n-checkbox
      :checked="includeReq"
      :disabled="phase === 'streaming'"
      @update:checked="emit('update:includeReq', $event)"
    >包含请求正文</n-checkbox>
    <n-checkbox
      :checked="includeResp"
      :disabled="phase === 'streaming'"
      @update:checked="emit('update:includeResp', $event)"
    >包含响应正文</n-checkbox>
  </div>

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

  <!-- 匹配卡片：rank/method/url/置信度/理由/[查看→] -->
  <div v-if="matches.length" class="ai-matches">
    <div v-for="m in matches" :key="m.flowId" class="ai-match">
      <div class="ai-match-head">
        <span class="ai-match-rank">#{{ m.rank }}</span>
        <span class="ai-match-method">{{ m.method }}</span>
        <span class="ai-match-url" :title="m.url">{{ m.url }}</span>
        <span class="ai-match-conf" :class="confCls(m.confidence)">{{ confLabel(m.confidence) }}</span>
      </div>
      <div class="ai-match-reason">
        <span class="ai-match-reason-text" :title="m.reason">{{ m.reason }}</span>
        <n-button size="tiny" quaternary class="ai-match-go" @click="emit('locate', m.flowId)">查看 →</n-button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { NButton, NCheckbox, NInput } from 'naive-ui'
import type { AiMatchItem } from '../../lib/types'
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
  includeReq: boolean
  includeResp: boolean
  /** 模型正文聚合文本（父级 displayMd：已截掉尾部 ```json 块） */
  md: string
  /** 首 token 前骨架占位 */
  pending: boolean
  /** 截断自动续写状态条（父级视图门控：仅发起模式 tab 可见） */
  continueState?: AiChatNotice | null
  /** 匹配卡片（父级 upsertMatch 按 rank 排） */
  matches: AiMatchItem[]
  /** 置信度色卡/文案（父级 helper：onFrame 日志同用） */
  confCls: (c: 'high' | 'medium' | 'low') => string
  confLabel: (c: 'high' | 'medium' | 'low') => string
}>()

const emit = defineEmits<{
  (e: 'start'): void
  (e: 'stop'): void
  (e: 'locate', flowId: string): void
  (e: 'update:question', v: string): void
  (e: 'update:includeReq', v: boolean): void
  (e: 'update:includeResp', v: boolean): void
}>()
</script>
