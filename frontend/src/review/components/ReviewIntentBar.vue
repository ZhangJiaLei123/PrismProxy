<template>
  <!-- 意图摘要条（设计 §7.1）：仅当该流已有意图结果时占位，无结果不显示空条 -->
  <div v-if="intent" class="intent-bar">
    <span class="ib-ic">✨</span>
    <span
      class="ib-text"
      :class="{ 'k-ai-low': intent.confidence === 'low', 'k-ai-needs-body': intent.needsBody }"
      :title="intentTip"
    >{{ intent.intent }}</span>
    <span class="ib-actions">
      <n-button size="tiny" quaternary :loading="reanalyzing" title="仅对本条重新发起意图标注，原地覆盖旧结果" @click="onReanalyze">
        重新分析
      </n-button>
      <n-button size="tiny" quaternary title="打开 AI 面板，完整解读本条接口" @click="emit('explain')">完整解读</n-button>
    </span>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { NButton } from 'naive-ui'
import type { ReviewApi } from '../api'
import type { IntentResult } from '../../lib/types'
import { useIntents } from '../useIntents'

const props = defineProps<{ api: ReviewApi; flowId: string }>()
const emit = defineEmits<{ (e: 'explain'): void; (e: 'error', msg: string): void }>()

// 意图缓存为 useIntents 模块级单例（reactive Map），标注完成即在此响应式出现
const { intentOf, runIntent } = useIntents()
const intent = computed<IntentResult | undefined>(() => intentOf(props.flowId))
const reanalyzing = ref(false)

// 弱样式说明：low 置信度 / needsBody（需正文才能判准）逐一拼接进 tooltip
const intentTip = computed(() => {
  const it = intent.value
  if (!it) return ''
  const tips: string[] = []
  if (it.confidence === 'low') tips.push('AI 对此判定置信度较低，仅供参考')
  if (it.needsBody) tips.push('AI 判断需结合请求正文才能准确判定；可在 AI 面板勾选「包含请求正文」后重新分析')
  return tips.join('；')
})

// 单流重析：以单元素 ids 发一次 intent 任务，intent 帧到达即覆盖缓存（Map 响应式刷新本条）
async function onReanalyze(): Promise<void> {
  reanalyzing.value = true
  try {
    await runIntent(props.api, [props.flowId], () => {})
  } catch (e) {
    emit('error', String((e as Error)?.message ?? e))
  } finally {
    reanalyzing.value = false
  }
}
</script>

<style scoped>
/* k-ai-low / k-ai-needs-body 弱样式色卡定义在 ai.css（禁止组件内联色值） */
.intent-bar {
  display: flex; align-items: center; gap: 8px;
  flex: none; padding: 4px 12px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  font-size: 12px;
  /* 文字色在此声明供 .ib-text 继承：k-ai-* 弱样式（ai.css 全局类）可直接覆盖继承色 */
  color: rgba(255, 255, 255, 0.78);
}
.ib-ic { flex: none; }
.ib-text {
  flex: 1; min-width: 0;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  transition: color 0.15s;
}
.ib-actions { flex: none; display: flex; gap: 2px; }
</style>
