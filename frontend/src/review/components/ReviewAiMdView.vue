<template>
  <!-- Markdown 结论区（explain/locate/flowmap 共用小件）：50ms 节流渲染的缓冲在父级，
       本组件只做净化渲染——v-html 唯一出口经 renderMarkdown 净化 -->
  <div class="ai-md">
    <!-- 首 token 前的等待骨架：微光横条示意"正在思考" -->
    <div v-if="pending" class="ai-skel" aria-hidden="true">
      <i></i><i></i><i></i><i></i><i></i>
    </div>
    <div v-else v-html="html"></div>
    <!-- 截断自动续写状态条：挂在正文断流处（压缩/续写信使由父级 notice 帧驱动） -->
    <ReviewAiContinueBar :state="continueState" />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { renderMarkdown } from '../ai-md'
import type { AiChatNotice } from '../api'
import ReviewAiContinueBar from './ReviewAiContinueBar.vue'

const props = defineProps<{
  /** 模型正文（父级 displayMd；locate 已在父级截掉尾部 ```json 块） */
  md: string
  /** 首 token 前骨架占位：流式进行中且尚无任何输出 */
  pending: boolean
  /** 截断自动续写状态（compressing/continuing/failed）；null=无续写 */
  continueState?: AiChatNotice | null
}>()

const html = computed(() => renderMarkdown(props.md))
</script>
