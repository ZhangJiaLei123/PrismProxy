<template>
  <div class="copy-bar">
    <n-button size="tiny" quaternary @click="copy('headers')">复制 Headers 原文</n-button>
    <n-button size="tiny" quaternary @click="copy('body')">复制 Body</n-button>
    <n-button size="tiny" quaternary @click="copy('all')">复制完整报文</n-button>
  </div>
</template>

<script setup lang="ts">
import { NButton, useMessage } from 'naive-ui'
import { GetFlowRawText } from '../../wailsjs/go/main/App'
import { copyText } from '../lib/clip'

// 通用复制栏：请求/响应详情共用，文本由 Go 侧 GetFlowRawText 生成
const props = defineProps<{ flowId: string; part: 'req' | 'resp' }>()
const message = useMessage()

async function copy(kind: 'headers' | 'body' | 'all') {
  if (!props.flowId) return
  try {
    const text = await GetFlowRawText(props.flowId, props.part, kind)
    if (await copyText(text)) {
      const partName = props.part === 'req' ? '请求' : '响应'
      const kindName = kind === 'headers' ? 'Headers 原文' : kind === 'body' ? 'Body' : '完整报文'
      message.success(`已复制${partName}${kindName}`, { duration: 2000, closable: true })
    } else {
      message.error('剪贴板写入失败', { duration: 4000, closable: true })
    }
  } catch (e) {
    message.error(String(e), { duration: 5000, closable: true })
  }
}
</script>

<style scoped>
.copy-bar { display: flex; gap: 4px; margin: 2px 0 6px; }
</style>
