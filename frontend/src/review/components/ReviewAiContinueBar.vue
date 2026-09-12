<template>
  <!-- 截断自动续写状态条（Trae 风格）：输出断流处横贯细线 + 中央胶囊。
       compressing=临时会话压缩历史；continuing=新会话从截断处续写；failed=续写失败保留截断稿。
       纯展示小件，状态由 ReviewAiPanel 持有（notice 帧驱动，done/stop/切轮清空） -->
  <div
    v-if="state"
    class="ai-continue"
    :class="'is-' + state.stage"
    role="status"
    :aria-label="main + (sub ? '，' + sub : '')"
  >
    <span class="ai-continue-line" aria-hidden="true"></span>
    <span class="ai-continue-chip">
      <span v-if="state.stage !== 'failed'" class="ai-continue-spin" aria-hidden="true"></span>
      <svg
        v-else
        class="ai-continue-warn"
        viewBox="0 0 16 16"
        width="12"
        height="12"
        fill="none"
        stroke="currentColor"
        stroke-width="1.5"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path d="M8 2.4l6.2 10.8H1.8L8 2.4z" />
        <path d="M8 6.6v2.6" />
        <path d="M8 11.4h.01" />
      </svg>
      <span class="ai-continue-text">
        <span class="ai-continue-main">{{ main }}</span>
        <span v-if="sub" class="ai-continue-sub">{{ sub }}</span>
      </span>
    </span>
    <span class="ai-continue-line" aria-hidden="true"></span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { AiChatNotice } from '../api'

const props = defineProps<{
  state: AiChatNotice | null
}>()

const main = computed(() => {
  const s = props.state
  if (!s) return ''
  if (s.stage === 'compressing') return s.round > 1 ? `第 ${s.round} 轮 · 正在压缩历史对话…` : '正在压缩历史对话…'
  if (s.stage === 'continuing') return s.round > 1 ? `第 ${s.round} 轮 · 新会话续写中…` : '新会话续写中…'
  return '自动续写失败'
})

const sub = computed(() => {
  const s = props.state
  if (!s) return ''
  if (s.stage === 'compressing') return '输出达到长度上限，临时会话整理上下文后将从截断处继续'
  if (s.stage === 'continuing') return '历史已压缩为会话记忆，正在无缝续写剩余内容'
  return s.message || '已保留当前输出内容，可稍后重试'
})
</script>
