<template>
  <section class="sec">
    <div class="sec-title">基本配置</div>
    <div class="form-row">
      <span class="label">启用分析</span>
      <n-switch v-model:value="form.ai.enabled" size="small" />
      <span class="inline-hint">启用后复盘页出现「AI 分析」入口；未配置 API Key 时会引导回本页</span>
    </div>
    <div class="form-row models-head">
      <span class="label">模型清单</span>
      <span class="inline-hint">
        每条为独立的供应商配置：模型、别名、BaseURL、API Key 各自独立；标记「当前」的条目用于复盘分析。随底部「保存」提交，最多 20 项
      </span>
      <n-button size="tiny" dashed :disabled="entries.length >= 20" @click="addEntry">添加模型</n-button>
    </div>
    <div v-if="!entries.length" class="hint sub">
      暂无配置；点击「添加模型」新增一条，可先选服务商预设快速填充 BaseURL 与推荐模型
    </div>
    <div v-for="(e, i) in entries" :key="entryKey(e)" class="entry" :class="{ current: e.current }">
      <div class="entry-row">
        <n-select
          :value="e.provider"
          :options="providerOptions"
          style="width: 128px"
          @update:value="(id: string) => onProvider(e, id)"
        />
        <n-input v-model:value="e.alias" placeholder="别名（可选）" style="flex: 1" />
      </div>
      <div class="entry-row">
        <span class="mini">URL</span>
        <n-input v-model:value="e.baseUrl" placeholder="https://api.deepseek.com" style="flex: 1" />
      </div>
      <div class="entry-row">
        <span class="mini">Key</span>
        <n-input
          v-model:value="e.apiKey"
          type="password"
          show-password-on="click"
          placeholder="sk-...（本地 Ollama 可留空）"
          style="flex: 1"
        />
      </div>
      <div class="entry-row">
        <span class="mini">模型</span>
        <n-auto-complete
          v-model:value="e.model"
          :options="modelOptions"
          :input-props="{ spellcheck: false }"
          placeholder="模型名，如 deepseek-chat"
          style="flex: 1"
        />
        <n-button
          size="tiny"
          secondary
          :disabled="!e.baseUrl.trim()"
          :loading="busy['fm-' + i]"
          @click="fetchModelsFor(e, i)"
        >
          获取模型
        </n-button>
        <div class="entry-actions">
          <n-button size="tiny" quaternary class="cur-btn" :disabled="e.current" @click="setCurrent(e)">
            {{ e.current ? '当前' : '设为当前' }}
          </n-button>
        </div>
      </div>
      <div class="entry-row">
        <div class="entry-actions">
          <n-button size="tiny" secondary :loading="busy['test-' + i]" @click="runTest(e, i)">测试连接</n-button>
          <n-button size="tiny" quaternary type="error" @click="removeEntry(i)">删除</n-button>
        </div>
      </div>
      <n-alert
        v-if="testResult && testResult.i === i"
        :type="testResult.r.ok ? 'success' : 'error'"
        closable
        style="margin-top: 8px"
        @close="testResult = null"
      >
        {{ testResult.r.ok ? `连接成功：${testResult.r.model}，耗时 ${testResult.r.latencyMs}ms` : `连接失败：${testResult.r.message}` }}
      </n-alert>
    </div>
    <div v-if="entries.length" class="hint sub">
      获取模型：按各条目自身的 BaseURL 与 Key 拉取 OpenAI 兼容 /v1/models，结果仅作模型名下拉建议，仍可自由输入；测试连接：按该条目草稿值测试（Key 留空仅在与当前条目同端点时回填已存密钥），不落盘
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
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { NInput, NAutoComplete, NButton, NSwitch, NSelect, NSlider, NInputNumber, NAlert, NPopconfirm, useMessage } from 'naive-ui'
import { FetchAIModels, TestAIConnection } from '../../../wailsjs/go/app/App'
import type { app, settings } from '../../../wailsjs/go/models'

const props = defineProps<{ form: app.SettingsView }>()
const message = useMessage()
const busy = reactive<Record<string, boolean>>({})

// 服务商预设：条目级选择，选中即填该条目 baseUrl + 推荐 model（均可改）。id 与 settings.Provider
// 取值一致，ollama 预设免 API Key（自托管无鉴权，后端 WarnNoKey 豁免）；zhipu /v4、火山方舟
// Ark /api/v3 均带版本段（归一化按「末段 /v<数字> 保留」处理，不会被误补 /v1）。
const PROVIDERS = [
  { id: 'openai', label: 'OpenAI', baseUrl: 'https://api.openai.com', model: 'gpt-4o-mini' },
  { id: 'deepseek', label: 'DeepSeek', baseUrl: 'https://api.deepseek.com', model: 'deepseek-chat' },
  { id: 'moonshot', label: '月之暗面 Kimi', baseUrl: 'https://api.moonshot.cn', model: 'moonshot-v1-8k' },
  { id: 'zhipu', label: '智谱 GLM', baseUrl: 'https://open.bigmodel.cn/api/paas/v4', model: 'glm-4-flash' },
  { id: 'qwen', label: '通义千问', baseUrl: 'https://dashscope.aliyuncs.com/compatible-mode/v1', model: 'qwen-plus' },
  { id: 'volcengine', label: '火山引擎', baseUrl: 'https://ark.cn-beijing.volces.com/api/v3', model: 'doubao-seed-1.6-250615' },
  { id: 'ollama', label: 'Ollama', baseUrl: 'http://127.0.0.1:11434', model: 'qwen2.5:7b' },
  { id: 'custom', label: '自定义', baseUrl: '', model: '' },
]
const providerOptions = PROVIDERS.map((p) => ({ label: p.label, value: p.id }))

function onProvider(e: settings.AIModelEntry, id: string) {
  const p = PROVIDERS.find((x) => x.id === id)
  if (!p) return
  e.provider = id
  if (id !== 'custom') {
    e.baseUrl = p.baseUrl
    e.model = p.model
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

// ---- 模型清单（多供应商条目）：每条独立 BaseURL/APIKey；随底部「保存」提交，
// 后端落盘前统一归一化（trim/去空/三元组去重/Current 唯一化/≤20），并把当前条目同步到顶层快照。
const entries = computed(() => props.form.ai.entries ?? [])
const currentEntry = computed(() => entries.value.find((e) => e.current))

// 条目稳定 key（v-for）：索引 key 在删除中间条目时会让 NInput 密码可见性等非受控
// 内部状态随 DOM 位置串位（审计问题3）；条目对象身份在增删/编辑期间稳定，用它映射 uid。
const entryKeys = new WeakMap<object, number>()
let entryKeySeq = 0
function entryKey(e: object): number {
  let k = entryKeys.get(e)
  if (k === undefined) {
    k = ++entryKeySeq
    entryKeys.set(e, k)
  }
  return k
}

function addEntry() {
  props.form.ai.entries = [
    ...entries.value,
    { provider: 'custom', model: '', alias: '', baseUrl: '', apiKey: '', current: entries.value.length === 0 },
  ]
}

function removeEntry(i: number) {
  const rest = entries.value.filter((_, idx) => idx !== i)
  // 删的是当前条目：剩余首条提为当前（后端归一化也会兜底，这里即时反馈免保存后跳变）
  if (entries.value[i]?.current && rest.length) rest[0].current = true
  props.form.ai.entries = rest
  // 条目索引前移，钉在卡片内的旧测试结果会错位到别的条目上，直接清掉
  testResult.value = null
}

function setCurrent(e: settings.AIModelEntry) {
  for (const x of entries.value) x.current = x === e
}

// ---- 获取模型（OpenAI 兼容 /v1/models）：每条目卡片自带按钮，用该条目草稿值构造临时配置
// （key 留空且与当前条目同端点时后端才回填已存密钥，异端点不外发），不落盘；结果仅作下拉建议，该条目模型名为空时自动选中首个。
const modelOptions = ref<{ label: string; value: string }[]>([])

async function fetchModelsFor(e: settings.AIModelEntry, i: number) {
  await wrap('fm-' + i, async () => {
    const res = await FetchAIModels({
      ...props.form.ai,
      provider: e.provider,
      baseUrl: e.baseUrl,
      apiKey: e.apiKey,
      model: e.model,
    } as settings.AIConfig)
    const models = res?.models ?? []
    modelOptions.value = models.map((m) => ({ label: m, value: m }))
    if (!e.model.trim() && models.length > 0) e.model = models[0]
    return `已获取 ${models.length} 个模型`
  })
}

// 任一条目 BaseURL 变化：清空过期模型建议（旧列表不再可信，重拉即得）
watch(
  () => entries.value.map((x) => x.baseUrl).join('\n'),
  () => {
    modelOptions.value = []
  },
)

// ---- 当前条目密钥缺失本地提示（对齐后端 WarnNoKey 口径：已启用 + 无 key + 非 ollama）
const keyWarn = computed(() => {
  if (!props.form.ai.enabled) return ''
  const cur = currentEntry.value
  if (!cur) return '尚未添加模型配置：AI 分析不可用，请点击「添加模型」'
  if (cur.provider === 'ollama' || cur.apiKey.trim()) return ''
  return '当前条目未填 API Key：AI 分析将不可用（本地 Ollama 除外）'
})

// ---- 测试连接（per-entry）：每条目卡片自带按钮，用该条目草稿值构造临时配置
// （key 留空且与当前条目同端点时后端才回填已存密钥，异端点不外发），不落盘；结果按条目索引钉在对应卡片内。
const testResult = ref<{ i: number; r: app.AITestResult } | null>(null)

async function runTest(e: settings.AIModelEntry, i: number) {
  await wrap('test-' + i, async () => {
    testResult.value = null
    testResult.value = {
      i,
      r: await TestAIConnection({
        ...props.form.ai,
        provider: e.provider,
        baseUrl: e.baseUrl,
        apiKey: e.apiKey,
        model: e.model,
      } as settings.AIConfig),
    }
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
.models-head { margin-top: 12px; }
.models-head .inline-hint { flex: 1; }
.entry { margin: 0 0 8px 80px; padding: 10px 12px; border: 1px solid rgba(255, 255, 255, 0.12); border-radius: 10px; }
.entry.current { border-color: rgba(99, 226, 183, 0.5); }
.entry-row { display: flex; align-items: center; gap: 8px; }
.entry-row + .entry-row { margin-top: 8px; }
/* 行内按钮组右对齐（margin-left: auto）：行4「设为当前」、行5「测试连接+删除」均不悬浮、不遮输入框 */
.entry-actions { display: flex; align-items: center; gap: 4px; margin-left: auto; }
/* cur-btn 两态文案「当前/设为当前」宽度不同：固定最小宽避免切换当前条目时按钮跳宽 */
.cur-btn { min-width: 68px; }
.mini { flex: none; min-width: 26px; font-size: 11px; opacity: 0.55; text-align: right; white-space: nowrap; }
.hint { opacity: 0.5; font-size: 11px; margin-top: 4px; line-height: 1.6; }
.hint.sub { margin: -4px 0 10px 80px; }
.hint.warn { opacity: 0.85; color: #e2a03f; }
.inline-hint { font-size: 11px; opacity: 0.6; }
.num { width: 32px; text-align: right; opacity: 0.7; font-variant-numeric: tabular-nums; }
</style>
