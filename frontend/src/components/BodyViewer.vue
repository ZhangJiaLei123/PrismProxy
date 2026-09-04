<template>
  <div class="body-viewer">
    <div class="bar">
      <n-radio-group v-model:value="view" size="tiny">
        <n-radio-button value="auto">自动</n-radio-button>
        <n-radio-button value="json">JSON</n-radio-button>
        <n-radio-button value="text">文本</n-radio-button>
        <n-radio-button value="image">图片</n-radio-button>
        <n-radio-button value="form">表单</n-radio-button>
        <n-radio-button value="hex">Hex</n-radio-button>
      </n-radio-group>
      <span class="meta">
        {{ fmtBytes(displayBytes.length) }}
        <template v-if="payload?.Encoding"> · {{ payload.Encoding }}</template>
        <template v-if="payload?.ContentType"> · {{ payload.ContentType }}</template>
        <n-tag v-if="payload?.Truncated" size="tiny" type="warning" style="margin-left: 6px">已截断(2MB)</n-tag>
      </span>
    </div>

    <n-alert v-if="payload?.DecodeErr" type="warning" size="small" style="margin: 6px 0">
      解压失败（{{ payload.DecodeErr }}），显示原始字节
    </n-alert>

    <div class="content">
      <div v-if="!displayBytes.length" class="empty">无消息体</div>

      <json-tree v-else-if="activeView === 'json' && jsonData.ok" :data="jsonData.value" />
      <pre v-else-if="activeView === 'json'" class="text">{{ text }}</pre>

      <pre v-else-if="activeView === 'text'" class="text">{{ text }}</pre>

      <div v-else-if="activeView === 'image'" class="image-wrap">
        <img v-if="imageUrl" :src="imageUrl" alt="response image" />
        <span v-else>无法识别为图片</span>
      </div>

      <n-table v-else-if="activeView === 'form'" size="small" :bordered="false" single-line>
        <tbody>
          <tr v-for="[k, v] in formEntries" :key="k"><td class="fk">{{ k }}</td><td class="fv">{{ v }}</td></tr>
        </tbody>
      </n-table>

      <pre v-else class="text hex">{{ hexDump }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { NAlert, NRadioButton, NRadioGroup, NTable, NTag } from 'naive-ui'
import JsonTree from './JsonTree.vue'
import { GetFlowBody } from '../../wailsjs/go/main/App'
import type { main } from '../../wailsjs/go/models'
import { b64ToBytes, bytesToText, fmtBytes } from '../lib/format'

const props = defineProps<{ flowId: string; which: 'req' | 'resp' }>()

const payload = ref<main.BodyPayload | null>(null)
const view = ref('auto')
const imageUrl = ref('')
const HEX_LIMIT = 64 * 1024

async function load() {
  revokeImage()
  payload.value = null
  if (!props.flowId) return
  try {
    payload.value = await GetFlowBody(props.flowId, props.which)
  } catch {
    payload.value = null
  }
}
watch(() => [props.flowId, props.which], load, { immediate: true })

/** 展示用字节：解压成功用 Body，否则回退 Raw */
const displayBytes = computed<Uint8Array>(() => {
  const p = payload.value
  if (!p) return new Uint8Array(0)
  const src = p.Body || p.Raw || ''
  return b64ToBytes(src as unknown as string)
})

const text = computed(() => bytesToText(displayBytes.value))

const contentType = computed(() => (payload.value?.ContentType || '').toLowerCase())

const activeView = computed(() => {
  if (view.value !== 'auto') return view.value
  const ct = contentType.value
  if (ct.startsWith('image/')) return 'image'
  if (ct.includes('json')) return 'json'
  if (ct.includes('x-www-form-urlencoded')) return 'form'
  if (ct.startsWith('text/') || ct.includes('xml') || ct.includes('javascript') || ct.includes('html')) return 'text'
  // 无 content-type 时试探 JSON
  const t = text.value.trimStart()
  if (t.startsWith('{') || t.startsWith('[')) return 'json'
  return 'hex'
})

const jsonData = computed(() => {
  try {
    return { ok: true, value: JSON.parse(text.value) }
  } catch {
    return { ok: false, value: null }
  }
})

const formEntries = computed<[string, string][]>(() => {
  const out: [string, string][] = []
  for (const [k, v] of new URLSearchParams(text.value)) out.push([k, v])
  return out
})

const hexDump = computed(() => {
  const b = displayBytes.value.slice(0, HEX_LIMIT)
  const lines: string[] = []
  for (let off = 0; off < b.length; off += 16) {
    const chunk = b.slice(off, off + 16)
    const hex = Array.from(chunk, (x) => x.toString(16).padStart(2, '0')).join(' ')
    const ascii = Array.from(chunk, (x) => (x >= 32 && x < 127 ? String.fromCharCode(x) : '.')).join('')
    lines.push(`${off.toString(16).padStart(8, '0')}  ${hex.padEnd(47)}  ${ascii}`)
  }
  if (displayBytes.value.length > HEX_LIMIT) lines.push(`…（仅显示前 ${HEX_LIMIT / 1024}KB）`)
  return lines.join('\n')
})

watch([activeView, displayBytes], () => {
  revokeImage()
  if (activeView.value === 'image' && displayBytes.value.length) {
    const blob = new Blob([displayBytes.value.slice().buffer], { type: contentType.value || 'application/octet-stream' })
    imageUrl.value = URL.createObjectURL(blob)
  }
})

function revokeImage() {
  if (imageUrl.value) {
    URL.revokeObjectURL(imageUrl.value)
    imageUrl.value = ''
  }
}
onBeforeUnmount(revokeImage)

defineExpose({ reload: load })
</script>

<style scoped>
.body-viewer { display: flex; flex-direction: column; height: 100%; }
.bar { display: flex; align-items: center; gap: 8px; padding: 4px 0; flex: none; }
.meta { font-size: 11px; color: rgba(255, 255, 255, 0.5); }
.content { flex: 1; overflow: auto; padding: 4px 0; }
.text {
  margin: 0; font-family: Consolas, 'Courier New', monospace; font-size: 12px;
  white-space: pre-wrap; word-break: break-all; color: rgba(255, 255, 255, 0.85);
}
.hex { white-space: pre; }
.empty { color: rgba(255, 255, 255, 0.35); padding: 12px 0; }
.image-wrap img { max-width: 100%; background: repeating-conic-gradient(#333 0 25%, #222 0 50%) 0 0 / 16px 16px; }
.fk { width: 35%; color: #9cdcfe; word-break: break-all; }
.fv { word-break: break-all; }
</style>
