<template>
  <n-modal
    :show="show"
    @update:show="(v: boolean) => (v ? null : close())"
    :mask-closable="false"
    transform-origin="center"
  >
    <div class="composer">
      <div class="cp-header">
        <span class="cp-title">调试重发（Composer）</span>
        <n-checkbox v-model:checked="skipVerify" size="small">跳过 HTTPS 证书校验</n-checkbox>
        <n-button size="small" type="primary" :loading="sending" @click="send">发送</n-button>
        <n-button size="small" quaternary circle class="cp-close" title="关闭" @click="close">✕</n-button>
      </div>

      <div class="cp-row">
        <n-select
          v-model:value="method"
          size="small"
          :options="methodOptions"
          filterable
          tag
          class="cp-method"
          placeholder="方法"
        />
        <n-input v-model:value="url" size="small" placeholder="请求 URL（http/https 绝对地址）" class="cp-url" />
      </div>

      <div class="cp-panes">
        <!-- 请求 -->
        <div class="cp-pane">
          <div class="cp-pane-head">
            <span>请求</span>
            <n-button size="tiny" quaternary @click="headers.push({ key: '', value: '' })">+ 添加首部</n-button>
          </div>
          <div class="cp-headers">
            <div v-for="(h, i) in headers" :key="i" class="cp-hrow">
              <n-input v-model:value="h.key" size="tiny" placeholder="Header" class="cp-hk" />
              <n-input v-model:value="h.value" size="tiny" placeholder="值" class="cp-hv" />
              <n-button size="tiny" quaternary type="error" @click="headers.splice(i, 1)">✕</n-button>
            </div>
            <div v-if="!headers.length" class="cp-empty">无自定义首部</div>
          </div>
          <div class="cp-body-label">Body</div>
          <n-input
            v-model:value="body"
            type="textarea"
            class="cp-body"
            :input-props="{ spellcheck: false }"
            placeholder="请求体（文本）"
          />
        </div>

        <!-- 响应 -->
        <div class="cp-pane">
          <div class="cp-pane-head">
            <span>响应</span>
            <span v-if="resp" class="cp-resp-meta">
              <n-tag size="tiny" :type="statusTagType(resp.State)">{{ resp.State }}</n-tag>
              <span v-if="resp.Status" :class="['cp-status', statusCls(resp.Status)]">{{ resp.Status }}</span>
              <span v-if="resp.Err" class="cp-err" :title="resp.Err">请求失败</span>
            </span>
          </div>
          <template v-if="resp">
            <div class="cp-resp-headers">
              <header-table :header="resp.RespHeader" />
            </div>
            <body-viewer :flow-id="resp.ID" which="resp" class="cp-resp-body" />
          </template>
          <div v-else class="cp-empty cp-resp-empty">尚未发送 —— 编辑请求后点击「发送」，响应将展示在此</div>
        </div>
      </div>
    </div>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  NButton, NCheckbox, NInput, NModal, NSelect, NTag, useMessage,
} from 'naive-ui'
import BodyViewer from '../components/BodyViewer.vue'
import HeaderTable from '../components/HeaderTable.vue'
import { GetFlowBody, GetFlowDetail, SendComposed } from '../../wailsjs/go/app/App'
import type { app } from '../../wailsjs/go/models'
import { useFlowsStore } from '../stores/flows'
import { b64ToBytes, bytesToText } from '../lib/format'

const store = useFlowsStore()
const message = useMessage()

const show = computed(() => store.composerShow)
function close() {
  store.closeComposer()
}

const method = ref('GET')
const url = ref('')
const headers = ref<{ key: string; value: string }[]>([])
const body = ref('')
const skipVerify = ref(false)
const sending = ref(false)
const resp = ref<app.FlowDetail | null>(null)

const methodOptions = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'].map((m) => ({ label: m, value: m }))

// 预填时剥离的逐跳/自动派生首部（Go 侧也会兜底剥离，前端预填先去掉避免误导）
const HOP_HEADERS = new Set([
  'connection', 'proxy-connection', 'proxy-authenticate', 'proxy-authorization',
  'keep-alive', 'te', 'trailer', 'transfer-encoding', 'upgrade', 'content-length',
])

// 打开 Composer：从指定流预填 method/URL/headers/body（文本）
watch(
  () => [store.composerShow, store.composerPrefillId] as const,
  async ([visible, prefillId]) => {
    if (!visible) return
    // 每次打开重置为预填内容（迭代结果通过列表新流继续）
    method.value = 'GET'
    url.value = ''
    headers.value = []
    body.value = ''
    resp.value = null
    if (!prefillId) return
    try {
      const d = await GetFlowDetail(prefillId)
      method.value = d.Method || 'GET'
      url.value = d.ReqURL || d.URL || ''
      const hs: { key: string; value: string }[] = []
      for (const [k, vs] of Object.entries(d.ReqHeader ?? {})) {
        if (HOP_HEADERS.has(k.toLowerCase())) continue
        for (const v of vs ?? []) hs.push({ key: k, value: v })
      }
      headers.value = hs
      // 请求体：取解压后文本（二进制/截断不预填）
      const bp = await GetFlowBody(prefillId, 'req')
      const raw = bp.Body || bp.Raw || ''
      if (raw) {
        const bytes = b64ToBytes(raw as unknown as string)
        if (bytes.length && !bp.Truncated) body.value = bytesToText(bytes)
      }
    } catch (e) {
      message.error('预填请求失败：' + e, { duration: 5000, closable: true })
    }
  },
  { immediate: true },
)

async function send() {
  if (!url.value.trim()) {
    message.warning('请填写请求 URL', { duration: 3000 })
    return
  }
  sending.value = true
  try {
    const d = await SendComposed({
      method: method.value,
      url: url.value.trim(),
      headers: headers.value.filter((h) => h.key.trim()),
      body: body.value,
      skipVerify: skipVerify.value,
    })
    resp.value = d
    store.select(d.ID) // 重发流入列表并选中，可继续迭代
    if (d.State === 'error') {
      message.error('请求失败（已记入列表）：' + d.Err, { duration: 6000, closable: true })
    }
  } catch (e) {
    // 参数校验失败（无落库流）
    message.error(String(e), { duration: 5000, closable: true })
  } finally {
    sending.value = false
  }
}

function statusTagType(state: string): 'success' | 'error' | 'warning' | 'default' {
  return state === 'done' ? 'success' : state === 'error' ? 'error' : state === 'streaming' ? 'warning' : 'default'
}
function statusCls(status: number): string {
  if (status >= 500) return 's-5xx'
  if (status >= 400) return 's-4xx'
  if (status >= 300) return 's-3xx'
  if (status >= 200) return 's-2xx'
  return ''
}
</script>

<style scoped>
.composer {
  width: 90vw;
  max-width: 1200px;
  height: 84vh;
  display: flex;
  flex-direction: column;
  background: #1e1f26;
  border: 1px solid rgba(255, 255, 255, 0.14);
  border-radius: 8px;
  padding: 12px 14px;
  color: rgba(255, 255, 255, 0.85);
  font-size: 12px;
}
.cp-header { display: flex; align-items: center; gap: 12px; flex: none; padding-bottom: 10px; }
.cp-title { font-weight: 600; font-size: 13px; flex: none; }
.cp-header .n-checkbox { margin-left: auto; }
.cp-close { flex: none; color: rgba(255, 255, 255, 0.65); }
.cp-row { display: flex; gap: 8px; flex: none; margin-bottom: 10px; }
.cp-method { width: 130px; flex: none; }
.cp-url { flex: 1; }
.cp-panes { flex: 1; display: flex; gap: 12px; min-height: 0; }
.cp-pane { flex: 1; min-width: 0; display: flex; flex-direction: column; border: 1px solid rgba(255, 255, 255, 0.1); border-radius: 6px; padding: 8px 10px; }
.cp-pane-head { display: flex; align-items: center; justify-content: space-between; flex: none; margin-bottom: 6px; color: rgba(255, 255, 255, 0.7); font-weight: 600; }
.cp-resp-meta { display: flex; align-items: center; gap: 8px; font-weight: normal; }
.cp-status { font-weight: 700; }
.cp-err { color: #e88080; }
.cp-headers { max-height: 30%; overflow-y: auto; flex: none; }
.cp-hrow { display: flex; gap: 6px; align-items: center; margin-bottom: 4px; }
.cp-hk { width: 34%; flex: none; }
.cp-hv { flex: 1; }
.cp-body-label { margin: 8px 0 4px; color: rgba(255, 255, 255, 0.55); flex: none; }
.cp-body { flex: 1; min-height: 0; display: flex; }
.cp-body :deep(.n-input) { height: 100%; }
.cp-body :deep(.n-input__textarea-el) { font-family: Consolas, 'Courier New', monospace; font-size: 12px; }
.cp-resp-headers { max-height: 32%; overflow-y: auto; flex: none; margin-bottom: 6px; }
.cp-resp-headers :deep(table) { font-family: Consolas, 'Courier New', monospace; font-size: 11px; }
.cp-resp-headers :deep(.ht-k) { padding-top: 1px; padding-bottom: 1px; }
.cp-resp-headers :deep(.ht-v) { padding-top: 1px; padding-bottom: 1px; }
.cp-resp-body { flex: 1; min-height: 0; }
.cp-empty { color: rgba(255, 255, 255, 0.35); padding: 8px 0; }
.cp-resp-empty { display: flex; align-items: center; justify-content: center; height: 100%; }
.s-2xx { color: #63e2b7; } .s-3xx { color: #70c0e8; } .s-4xx { color: #e5c07b; } .s-5xx { color: #e88080; }
</style>
