<template>
  <!-- 范围说明（四模式共用小件）：默认单行省略，溢出才出现展开箭头（短文案零打扰）；
       收起态整块可点展开（hover 有完整 title）；展开态仅箭头收起——正文可选中复制，滚动区只在文本上 -->
  <div
    class="ai-scope"
    :class="{ 'is-fold': overflow, 'is-open': open }"
    :title="!open && overflow ? text : undefined"
    @click="onBoxClick"
  >
    <div ref="textEl" class="ai-scope-text">{{ text }}</div>
    <button
      v-if="overflow"
      class="ai-scope-chev"
      type="button"
      :aria-expanded="open"
      :title="open ? '收起' : '展开'"
      @click.stop="toggle"
    >
      <svg class="chev" viewBox="0 0 16 16" width="10" height="10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M6 4.5l4 4-4 4" />
      </svg>
    </button>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps<{ text: string }>()

// 溢出检测只在收起态做（nowrap ellipsis 的 scrollWidth 才可比）；展开态保持 true——
// 能展开必先溢出，收起时经 nextTick/ResizeObserver 复测纠正
const overflow = ref(false)
const open = ref(false)
const textEl = ref<HTMLElement | null>(null)
let ro: ResizeObserver | null = null

function measure(): void {
  if (open.value || !textEl.value) return
  overflow.value = textEl.value.scrollWidth > textEl.value.clientWidth + 1
}

function toggle(): void {
  open.value = !open.value
  if (!open.value) void nextTick(measure) // 展开期间面板可能被拖宽，收起后复测
}

// 收起态点整块即展开；展开态不拦（正文可选中复制，收起只认箭头）
function onBoxClick(): void {
  if (!open.value && overflow.value) toggle()
}

onMounted(() => {
  ro = new ResizeObserver(measure) // 面板宽度拖拽/窗口变化时复测（对齐 ReviewTimeline 观察模式）
  if (textEl.value) ro.observe(textEl.value)
})
onBeforeUnmount(() => ro?.disconnect())

// 文案变化（浏览切换目标流/关键词）：收起态下一帧复测
watch(() => props.text, () => void nextTick(measure))
</script>
