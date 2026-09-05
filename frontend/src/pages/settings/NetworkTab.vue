<template>
  <section class="sec">
    <div class="sec-title">代理服务</div>
    <div class="form-row">
      <span class="label">监听地址</span>
      <n-select v-model:value="bindIP" :options="ipOptions" filterable tag style="width: 180px" />
      <n-input-number v-model:value="bindPort" :min="1" :max="65535" :show-button="false" placeholder="端口" style="width: 100px" />
      <n-button size="small" quaternary :loading="portBusy" title="从当前端口向上探测空闲端口" @click="pickFreePort">
        <svg viewBox="0 0 24 24" width="14" height="14" fill="currentColor">
          <path d="M17.65 6.35A7.958 7.958 0 0 0 12 4c-4.42 0-7.99 3.58-7.99 8s3.57 8 7.99 8c3.73 0 6.84-2.55 7.73-6h-2.08A5.99 5.99 0 0 1 12 18c-3.31 0-6-2.69-6-6s2.69-6 6-6c1.66 0 3.14.69 4.22 1.78L13 11h7V4l-2.35 2.35z" />
        </svg>
      </n-button>
    </div>
    <div class="form-row">
      <span class="label">上游代理</span>
      <n-select v-model:value="form.upstreamMode" :options="upstreamModes" style="width: 150px" />
      <n-input
        v-if="form.upstreamMode === 'manual'"
        v-model:value="form.upstreamProxy"
        placeholder="host:port"
        style="width: 190px"
      />
    </div>
    <div class="form-row">
      <span class="label">存储上限</span>
      <n-input-number v-model:value="form.maxFlows" :min="100" :max="100000" style="width: 140px">
        <template #suffix>条</template>
      </n-input-number>
      <n-input-number v-model:value="form.maxBodyMB" :min="0" :max="4096" style="width: 160px">
        <template #suffix>MB body</template>
      </n-input-number>
    </div>
    <div class="hint">body 预算 0 = 不限；修改监听地址/上游保存后将自动重启代理。</div>
    <div v-if="portErr" class="hint" style="opacity: 0.9; color: #e88080">{{ portErr }}</div>
  </section>

  <section class="sec">
    <div class="sec-title">系统代理绕过列表（ProxyOverride）</div>
    <n-dynamic-tags v-model:value="form.bypassList" />
    <div class="hint">裸域名匹配自身+全部子域；&lt;-loopback&gt; 绕过本地回环。接管状态下需重新开关系统代理生效。</div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { NSelect, NInputNumber, NInput, NButton, NDynamicTags } from 'naive-ui'
import { GetLocalAddrs, FindFreePort } from '../../../wailsjs/go/main/App'
import type { settings } from '../../../wailsjs/go/models'

const props = defineProps<{ form: settings.Settings }>()

const bindIP = ref('127.0.0.1')
const bindPort = ref<number | null>(9090)
const ipOptions = ref<{ label: string; value: string }[]>([])
const portBusy = ref(false)
const portErr = ref('')

// 拆分监听地址为 IP + 端口（仅处理 IPv4 host:port 常规形态）
function splitListenAddr() {
  const i = props.form.listenAddr.lastIndexOf(':')
  if (i > 0) {
    bindIP.value = props.form.listenAddr.slice(0, i)
    bindPort.value = Number(props.form.listenAddr.slice(i + 1)) || 9090
  } else {
    bindIP.value = props.form.listenAddr || '127.0.0.1'
    bindPort.value = 9090
  }
}

// IP/端口变化即时写回 form.listenAddr，父组件保存时无需再拼装
watch([bindIP, bindPort], () => {
  props.form.listenAddr = (bindIP.value || '127.0.0.1') + ':' + (bindPort.value || 9090)
})

onMounted(async () => {
  splitListenAddr()
  const addrs = await GetLocalAddrs()
  const ips = new Set(['127.0.0.1', '0.0.0.0', ...(addrs ?? [])])
  ipOptions.value = [...ips].map((a) => ({ label: a === '0.0.0.0' ? '0.0.0.0（局域网）' : a, value: a }))
})

// ---- 空闲端口探测 ----
async function pickFreePort() {
  portBusy.value = true
  portErr.value = ''
  try {
    const p = await FindFreePort(bindIP.value || '127.0.0.1', bindPort.value || 9090)
    bindPort.value = p
  } catch (e) {
    portErr.value = String(e)
  } finally {
    portBusy.value = false
  }
}

// ---- 选项常量 ----
const upstreamModes = [
  { label: '直连', value: 'direct' },
  { label: '手动代理', value: 'manual' },
  { label: '跟随系统', value: 'system' },
]
</script>

<style scoped>
.sec { font-size: 12px; }
.sec + .sec { margin-top: 20px; }
.sec-title { font-weight: 600; font-size: 13px; margin-bottom: 8px; }
.form-row { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
.label { width: 56px; flex: none; opacity: 0.7; }
.hint { opacity: 0.5; font-size: 11px; margin-top: 4px; }
.hint.break { word-break: break-all; }
</style>
