<template>
  <!-- 动作行（四模式共用小件）：开始（redact 关闭时每次需确认）/停止互斥 + 运行状态。
       由 ReviewAiPanel 的各模式视图挂载：父持运行引擎与全部状态，本组件纯展示转发 start/stop -->
  <div class="ai-actions">
    <n-popconfirm
      v-if="needConfirm"
      placement="top-start"
      positive-text="继续"
      negative-text="取消"
      @positive-click="emit('start')"
    >
      <template #trigger>
        <n-button type="primary" size="small" :disabled="startDisabled">{{ label }}</n-button>
      </template>
      当前未开启脱敏，请求头将原样发送给 AI 服务，确定继续？
    </n-popconfirm>
    <n-button v-else type="primary" size="small" :disabled="startDisabled" @click="emit('start')">{{ label }}</n-button>
    <n-button v-if="phase === 'streaming'" size="small" quaternary @click="emit('stop')">停止</n-button>
    <!-- 分析中状态 pill：弹跳点 + 流动渐变文字（静态线索=紫色底与点色，动画非唯一反馈） -->
    <span v-if="phase === 'streaming'" class="ai-live">
      <i></i><i></i><i></i><span>AI 分析中</span>
    </span>
    <!-- 读屏播报（R1 审计修复）：live region 必须先于消息常驻无障碍树才会被播报；sr-only 视觉隐藏不影响布局 -->
    <span class="ai-sr-live" aria-live="polite">{{ phase === 'streaming' ? 'AI 分析中' : '' }}</span>
    <span v-if="metaText" class="ai-meta">{{ metaText }}</span>
    <span v-else-if="phase === 'done'" class="ai-state">已完成</span>
    <span v-else-if="phase === 'stopped'" class="ai-state">已停止</span>
  </div>
</template>

<script setup lang="ts">
import { NButton, NPopconfirm } from 'naive-ui'

defineProps<{
  /** 面板状态机（设计 §7.3）：idle → streaming → done|stopped|error */
  phase: 'idle' | 'streaming' | 'done' | 'stopped' | 'error'
  /** 开始按钮文案（各模式自定：开始解读/开始标注/开始分析） */
  label: string
  startDisabled: boolean
  /** redact 关闭时开始前需 popconfirm 确认（父按 cfg 计算） */
  needConfirm: boolean
  /** 运行状态摘要（送审条数/预算/单条轮询进度） */
  metaText: string
}>()

const emit = defineEmits<{
  (e: 'start'): void
  (e: 'stop'): void
}>()
</script>
