<template>
  <section class="sec">
    <div class="sec-title">基本配置</div>
    <div class="form-row">
      <span class="label">启用分析</span>
      <n-switch v-model:value="form.ai.enabled" size="small" />
      <span class="inline-hint">启用后复盘页出现「AI 分析」入口；未配置 API Key 时会引导回本页</span>
    </div>
    <div class="form-row">
      <span class="label">服务商预设</span>
      <n-select
        :value="form.ai.provider"
        :options="providerOptions"
        style="width: 200px"
        @update:value="onProvider"
      />
    </div>
    <div class="form-row">
      <span class="label">BaseURL</span>
      <n-input v-model:value="form.ai.baseUrl" placeholder="https://api.deepseek.com" style="flex: 1" />
    </div>
    <div class="form-row">
      <span class="label">API Key</span>
      <n-input
        v-model:value="keyInput"
        type="password"
        show-password-on="click"
        :placeholder="keyPlaceholder"
        style="flex: 1"
      />
      <n-button size="small" type="primary" :disabled="!keyInput.trim()" :loading="busy['saveKey']" @click="saveKey">
        保存密钥
      </n-button>
      <n-button size="small" quaternary type="error" :disabled="!hasKey" :loading="busy['clearKey']" @click="clearKey">
        清除密钥
      </n-button>
    </div>
    <div class="hint sub">
      密钥独立保存：点「保存密钥」立即生效，不随底部「保存」提交；已保存值不回显原文，留空再保存则不变。
    </div>
    <div class="form-row">
      <span class="label">模型</span>
      <n-input v-model:value="form.ai.model" placeholder="如 deepseek-chat / gpt-4o-mini" style="flex: 1" />
    </div>
    <div v-if="keyWarn" class="hint warn">{{ keyWarn }}</div>
  </section>

  <section class="sec">
    <div class="sec-title">参数与预算</div>
    <div class="form-row">
      <span class="label">温度</span>
      <n-slider v-model:value="form.ai.temperature" :min="0.1" :max="2" :step="0.1" style="flex: 1" />
      <span class="num">{{ form.ai.temperature.toFixed(1) }}</span>
    </div>
    <div class="hint sub">旧配置 0 视为未设置按 0.3 生效（slider 从 0.1 起）；分析任务建议 0.1–0.7。</div>
    <div class="form-row">
      <span class="label">超时（秒）</span>
      <n-input-number v-model:value="form.ai.timeoutSec" :min="10" :max="600" style="width: 140px" />
      <span class="inline-hint">整请求上限；流式下同时约束首块超时</span>
    </div>
    <div class="form-row">
      <span class="label">最大流数</span>
      <n-input-number v-model:value="form.ai.maxFlows" :min="1" :max="100" style="width: 140px" />
      <span class="inline-hint">单次分析最多送审的接口条数（按时间取最近）</span>
    </div>
    <div class="form-row">
      <span class="label">正文预算</span>
      <n-input-number v-model:value="form.ai.maxKb" :min="8" :max="256" style="width: 140px">
        <template #suffix>KB</template>
      </n-input-number>
      <span class="inline-hint">单次送审正文总预算，超出自动截断</span>
    </div>
  </section>

  <section class="sec">
    <div class="sec-title">隐私脱敏</div>
    <div class="form-row">
      <span class="label">发送前脱敏</span>
      <n-popconfirm
        :show="showRedactConfirm"
        :show-icon="false"
        @update:show="(v: boolean) => { if (!v) showRedactConfirm = false }"
        @positive-click="confirmRedactOff"
        @negative-click="showRedactConfirm = false"
      >
        <template #trigger>
          <n-switch :value="form.ai.redact" size="small" @update:value="onRedactIntent" />
        </template>
        关闭脱敏后，请求头与正文将原样发送到所选服务商（含 Cookie、Token 等）。确定关闭？
      </n-popconfirm>
      <span class="inline-hint">默认开启；关闭需确认</span>
    </div>
    <div class="hint sub">
      开启时请求头仅发送白名单名称与 Cookie 名（值打码），正文中的凭据/JWT/手机号打码；
      即使开启，URL、参数名与部分正文仍会发送到所选服务商。
    </div>
  </section>

  <section class="sec">
    <div class="sec-title">测试连接</div>
    <div class="form-row">
      <n-button size="small" type="primary" secondary :loading="busy['test']" @click="runTest">测试连接</n-button>
      <span class="inline-hint">用当前界面值测试（未保存的密钥输入也生效；留空则用已存值），不落盘</span>
    </div>
    <n-alert
      v-if="testResult"
      :type="testResult.ok ? 'success' : 'error'"
      closable
      style="margin-top: 8px"
      @close="testResult = null"
    >
      {{ testResult.ok ? `连接成功：${testResult.model}，耗时 ${testResult.latencyMs}ms` : `连接失败：${testResult.message}` }}
    </n-alert>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { NInput, NButton, NSwitch, NSelect, NSlider, NInputNumber, NAlert, NPopconfirm, useMessage } from 'naive-ui'
import { GetAIConfigApp, SaveAIConfigApp, TestAIConnection } from '../../../wailsjs/go/app/App'
import type { app, settings } from '../../../wailsjs/go/models'

const props = defineProps<{ form: app.SettingsView }>()
const message = useMessage()
const busy = reactive<Record<string, boolean>>({})

// 服务商预设：选中即填 baseUrl + 推荐 model（均可改）。id 与 settings.Provider 取值一致，
// ollama 预设免 API Key（自托管无鉴权，后端 WarnNoKey 豁免）；zhipu 官方端点带 /v4 版本段
// （归一化按「末段 /v<数字> 保留」处理，不会被误补 /v1）。
const PROVIDERS = [
  { id: 'openai', label: 'OpenAI', baseUrl: 'https://api.openai.com', model: 'gpt-4o-mini' },
  { id: 'deepseek', label: 'DeepSeek', baseUrl: 'https://api.deepseek.com', model: 'deepseek-chat' },
  { id: 'moonshot', label: '月之暗面 Kimi', baseUrl: 'https://api.moonshot.cn', model: 'moonshot-v1-8k' },
  { id: 'zhipu', label: '智谱 GLM', baseUrl: 'https://open.bigmodel.cn/api/paas/v4', model: 'glm-4-flash' },
  { id: 'qwen', label: '通义千问', baseUrl: 'https://dashscope.aliyuncs.com/compatible-mode/v1', model: 'qwen-plus' },
  { id: 'ollama', label: 'Ollama（本地）', baseUrl: 'http://127.0.0.1:11434', model: 'qwen2.5:7b' },
  { id: 'custom', label: '自定义', baseUrl: '', model: '' },
]
const providerOptions = PROVIDERS.map((p) => ({ label: p.label, value: p.id }))

function onProvider(id: string) {
  const p = PROVIDERS.find((x) => x.id === id)
  if (!p) return
  props.form.ai.provider = id
  if (id !== 'custom') {
    props.form.ai.baseUrl = p.baseUrl
    props.form.ai.model = p.model
  }
}

// ---- API Key 独立存取（keyInput 不进 form，不随底部「保存」提交） ----
const keyInput = ref('')
const hasKey = ref(false)
const savedMask = ref('')
const keyWarn = ref('')
const keyPlaceholder = computed(() =>
  hasKey.value ? `已保存：${savedMask.value || '••••'}（留空保存则不变）` : 'sk-...',
)

onMounted(refreshKeyState)
async function refreshKeyState() {
  try {
    const v = await GetAIConfigApp()
    hasKey.value = !!v?.hasApiKey
    savedMask.value = v?.apiKeyMasked ?? ''
    keyWarn.value = v?.keyMissingWarn ?? ''
  } catch {
    // 掩码仅展示用，拉取失败不阻塞表单
  }
}

async function wrap(key: string, fn: () => Promise<string>) {
  if (busy[key]) return
  busy[key] = true
  try {
    const msg = await fn()
    if (msg) message.success(msg)
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
  } finally {
    busy[key] = false
  }
}

async function saveKey() {
  const k = keyInput.value.trim()
  if (!k) return
  await wrap('saveKey', async () => {
    await SaveAIConfigApp(k)
    keyInput.value = ''
    await refreshKeyState()
    return 'API Key 已保存'
  })
}

async function clearKey() {
  await wrap('clearKey', async () => {
    await SaveAIConfigApp('__clear__')
    keyInput.value = ''
    await refreshKeyState()
    return '已清除保存的 API Key'
  })
}

// ---- 测试连接：界面当前值（含未落盘 key 输入）构造临时配置，key 留空时后端回填已存值 ----
const testResult = ref<app.AITestResult | null>(null)

async function runTest() {
  await wrap('test', async () => {
    testResult.value = null
    const cfg = { ...props.form.ai, apiKey: keyInput.value.trim() } as settings.AIConfig
    testResult.value = await TestAIConnection(cfg)
    return ''
  })
}

// ---- 脱敏开关：开→直接生效；关→受控 popconfirm 风险确认 ----
const showRedactConfirm = ref(false)

function onRedactIntent(v: boolean) {
  if (v) {
    props.form.ai.redact = true
    return
  }
  showRedactConfirm.value = true
}
function confirmRedactOff() {
  props.form.ai.redact = false
  showRedactConfirm.value = false
}
</script>

<style scoped>
.sec { font-size: 12px; }
.sec + .sec { margin-top: 20px; }
.sec-title { font-weight: 600; font-size: 13px; margin-bottom: 8px; }
.form-row { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
.label { width: 72px; flex: none; opacity: 0.7; }
.hint { opacity: 0.5; font-size: 11px; margin-top: 4px; line-height: 1.6; }
.hint.sub { margin: -4px 0 10px 80px; }
.hint.warn { opacity: 0.85; color: #e2a03f; }
.inline-hint { font-size: 11px; opacity: 0.6; }
.num { width: 32px; text-align: right; opacity: 0.7; font-variant-numeric: tabular-nums; }
</style>
