<template>
  <!-- M12 标记弹窗：标签名（历史自动补全 + 清空图标）+ 自动清空复选框；名称/勾选态仅本次运行缓存 -->
  <n-modal
    :show="show"
    preset="card"
    title="标记当前列表"
    style="width: 460px"
    :mask-closable="!tagMark.submitting"
    :close-on-esc="!tagMark.submitting"
    @update:show="(v: boolean) => !v && close()"
  >
    <p class="tip">将为当前筛选条件下的 <b>{{ count }}</b> 条流打标签并归档到项目库（不受自动录制开关与保留策略影响）。</p>
    <div class="field">
      <label>标签名称</label>
      <n-auto-complete
        v-model:value="name"
        :options="tagOptions"
        :disabled="tagMark.submitting"
        clearable
        placeholder="输入标签名，或从历史标签选择"
        @update:value="onNameInput"
      />
    </div>
    <n-checkbox v-model:checked="autoClear" :disabled="tagMark.submitting" style="margin-top: 12px">
      标记后自动清空当前列表
      <span class="hint">（置顶流仍保留；归档数据不受影响）</span>
    </n-checkbox>
    <template #footer>
      <div style="display: flex; justify-content: flex-end; gap: 8px">
        <n-button size="small" :disabled="tagMark.submitting" @click="close()">取消</n-button>
        <n-button
          size="small"
          type="primary"
          :loading="tagMark.submitting"
          :disabled="!canSubmit"
          @click="submit"
        >
          标记 {{ count }} 条
        </n-button>
      </div>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NModal, NButton, NAutoComplete, NCheckbox, useMessage } from 'naive-ui'
import { useFlowsStore } from '../stores/flows'
import { useTagMarkStore } from '../stores/tagMark'

// show 由父组件（AppHeader）v-model 控制；count 为当前过滤可见流数
const props = defineProps<{ show: boolean; count: number }>()
const emit = defineEmits<{ (e: 'update:show', v: boolean): void }>()

const store = useFlowsStore()
const tagMark = useTagMarkStore()
const message = useMessage()

// 弹窗内编辑态：打开时从本次运行缓存回填，确认/取消后写回缓存（关闭即保留，便于连续打标）
const name = ref('')
const autoClear = ref(false)

watch(
  () => props.show,
  (open) => {
    if (open) {
      name.value = tagMark.name
      autoClear.value = tagMark.autoClear
      // 打开即重拉历史标签（复盘页改名/删除后主窗同步，设计 §4.4 跨窗口闭环）
      void tagMark.refreshHistory()
    }
  },
)

// 历史标签自动补全：按最近使用倒序（后端 ListTags 已按 last_used_at 倒序）
const tagOptions = computed(() =>
  tagMark.history.map((t) => ({ label: `${t.Name}（${t.Count} 条）`, value: t.Name })),
)

const canSubmit = computed(() => name.value.trim().length > 0 && props.count > 0 && !tagMark.submitting)

function onNameInput(v: string) {
  name.value = v
}

function close() {
  if (tagMark.submitting) return
  // 缓存本次编辑（即使取消也保留名称/勾选态，下次打开回填；重启复位）
  tagMark.name = name.value.trim()
  tagMark.autoClear = autoClear.value
  emit('update:show', false)
}

async function submit() {
  const tagName = name.value.trim()
  if (!tagName || tagMark.submitting) return
  // 作用域 = 当前过滤后可见流（设计 §九 2）；后端仍会去重
  const ids = store.filtered.map((f) => f.ID)
  try {
    const res = await tagMark.mark(ids, tagName, autoClear.value)
    const tName = res?.Tag?.Name ?? tagName
    message.success(`已标记 ${res.Tagged} 条流到标签「${tName}」（归档 ${res.Archived} 条）`, { duration: 4000, closable: true })
    if (res.Skipped > 0) {
      message.warning(`${res.Skipped} 条流已过期（不在内存且未落盘），未能归档`, { duration: 5000, closable: true })
    }
    // paused 下 upsert/evict 事件被丢弃且不补发：手动重拉列表刷新徽章/清空显示
    if (store.paused) await store.relist()
    close()
  } catch (e) {
    message.error(String(e), { duration: 6000, closable: true })
  }
}
</script>

<style scoped>
.tip { margin: 0 0 14px; font-size: 12px; color: rgba(255, 255, 255, 0.65); line-height: 1.6; }
.field { display: flex; flex-direction: column; gap: 6px; }
.field label { font-size: 12px; color: rgba(255, 255, 255, 0.75); }
.hint { font-size: 11px; color: rgba(255, 255, 255, 0.45); }
</style>
