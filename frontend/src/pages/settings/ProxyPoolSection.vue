<template>
  <div class="pool">
    <!-- 账号区 -->
    <div class="sub-title">代理账号</div>
    <div class="form-row">
      <span class="label">账号</span>
      <n-select
        v-model:value="selectedId"
        :options="providerOptions"
        placeholder="选择账号"
        style="width: 220px"
        size="small"
      />
      <n-button size="small" quaternary @click="startNew">新建</n-button>
      <n-button size="small" quaternary :disabled="!selectedProvider" @click="startEdit">编辑</n-button>
      <n-popconfirm v-if="selectedProvider" @positive-click="removeProvider">
        <template #trigger>
          <n-button size="small" quaternary type="error">删除</n-button>
        </template>
        删除账号「{{ selectedProvider.name }}」？若为生效账号，模式将回退为直连。
      </n-popconfirm>
      <n-button
        size="small"
        type="primary"
        secondary
        :disabled="!selectedProvider || selectedProvider.active"
        :loading="using"
        title="设为生效账号并切换到代理池模式"
        @click="useProvider"
      >使用</n-button>
    </div>
    <div v-if="!providers.length && !editing" class="hint">
      还没有代理账号，点击「新建」添加 91HTTP 账号；tradeNo / secret 在
      <a class="link" href="https://www.91http.com/" target="_blank" rel="noopener">91HTTP 控制台</a>
      获取。
    </div>

    <!-- 未生效状态（上游非 pool，或 pool 模式但代理未运行）的一键提取入口 -->
    <div v-if="!(poolStatus && poolStatus.running) && activeProvider" class="form-row pool-enable-row">
      <n-button
        size="small"
        type="primary"
        :loading="enabling"
        :title="enableBtnTitle"
        @click="enableAndFetch"
      >{{ poolMode === 'pool' ? '启动代理并提取 IP' : '启用代理池并立即提取 IP' }}</n-button>
      <span class="hint-inline">{{ enableHint }}</span>
    </div>

    <!-- 编辑表单（新建/编辑） -->
    <template v-if="editing">
      <div class="form-row">
        <span class="label">厂商</span>
        <n-select v-model:value="editing.vendor" :options="vendorOptions" size="small" style="width: 130px" :disabled="!editIsNew" />
        <span class="label-s">名称</span>
        <n-input v-model:value="editing.name" size="small" placeholder="留空自动命名" style="width: 170px" />
      </div>
      <div class="form-row">
        <span class="label">tradeNo</span>
        <n-input v-model:value="editing.tradeNo" size="small" placeholder="套餐号" style="width: 180px" />
        <span class="label-s">secret</span>
        <n-input
          v-model:value="editing.secret"
          size="small"
          type="password"
          show-password-on="click"
          :placeholder="secretPlaceholder"
          style="width: 180px"
        />
        <n-button v-if="!editIsNew" size="tiny" quaternary title="保存后清空已保存的密钥" @click="clearSecret">清空</n-button>
      </div>
      <div class="form-row">
        <span class="label">鉴权</span>
        <n-select v-model:value="editing.authMode" :options="authModes" size="small" style="width: 110px" />
        <span class="label-s">提取数</span>
        <n-input-number v-model:value="editing.fetchNum" :min="1" :max="100" :show-button="false" size="small" style="width: 70px" />
        <span class="label-s">过滤已用</span>
        <n-switch v-model:value="editing.filterUsed" size="small" />
      </div>
      <div v-if="editing.authMode === 'userpass'" class="form-row">
        <span class="label">账密</span>
        <n-input v-model:value="editing.username" size="small" placeholder="代理用户名" style="width: 160px" />
        <n-input
          v-model:value="editing.password"
          size="small"
          type="password"
          show-password-on="click"
          :placeholder="pwdPlaceholder"
          style="width: 160px"
        />
        <n-button v-if="!editIsNew && editingProvider?.hasPassword" size="tiny" quaternary title="保存后清空已保存的密码" @click="clearPassword">清空</n-button>
      </div>
      <div class="form-row">
        <span class="label">地区</span>
        <n-input v-model:value="editing.province" size="small" placeholder="省份（可空）" style="width: 110px" />
        <n-input v-model:value="editing.city" size="small" placeholder="城市（可空）" style="width: 110px" />
        <n-select v-model:value="editing.isp" :options="ispOptions" size="small" style="width: 110px" />
      </div>
      <div class="form-row btn-row">
        <n-button size="small" :loading="testing" :disabled="!editing.tradeNo" @click="testFetch">测试提取</n-button>
        <n-button size="small" type="primary" :loading="saving" @click="saveProvider">保存账号</n-button>
        <n-button size="small" quaternary @click="cancelEdit">取消</n-button>
      </div>
      <div v-if="testResult" class="test-result">
        提取成功：{{ testResult.addr }}<template v-if="testResult.region">（{{ testResult.region }}）</template>
        · {{ testResult.withAuth ? '带账密' : '无账密' }} · 共 {{ testResult.count }} 条
        <template v-if="testResult.expireAt">· {{ fmtClock(testResult.expireAt) }} 到期</template>
      </div>
      <div class="hint">提示：经代理出口的明文 HTTP 流量对厂商可见，敏感站点请使用 HTTPS。</div>
      <n-alert v-if="editErr" type="error" size="small" class="pool-alert" closable @close="editErr = ''">{{ editErr }}</n-alert>
    </template>

    <!-- 运行状态 -->
    <template v-if="poolStatus && poolStatus.running">
      <div class="sub-title">运行状态</div>
      <div class="form-row">
        <span class="label">当前出口</span>
        <span class="mono">{{ poolStatus.current?.addr ?? '—' }}</span>
        <span v-if="poolStatus.current?.region" class="hint-inline">{{ poolStatus.current.region }}</span>
        <n-button size="tiny" type="primary" secondary :loading="rotating" :disabled="poolStatus.degraded"
          title="手动提取并立即生效：丢弃当前出口，池内备用优先切换，无货时向厂商新提取（等待≤30s）"
          @click="rotate">手动提取 IP</n-button>
      </div>
      <div class="form-row">
        <span class="label">到期</span>
        <span class="mono" :class="{ danger: remainSec > 0 && remainSec < 60 }">{{ remainText }}</span>
      </div>
      <div class="form-row">
        <span class="label">池大小</span>
        <span class="mono">{{ poolStatus.poolSize }}</span>
        <span class="label-s">套餐余量</span>
        <span class="mono">{{ poolStatus.surplusTotal >= 0 ? poolStatus.surplusTotal : '未知' }}</span>
      </div>
      <n-alert v-if="poolStatus.degraded" type="warning" size="small" class="pool-alert">
        代理池已降级为直连：当前流量使用本机真实 IP（池空策略 direct）。
      </n-alert>
      <n-alert v-if="poolStatus.lastError" type="error" size="small" class="pool-alert" closable>
        最近错误：{{ poolStatus.lastError }}
      </n-alert>
    </template>
    <div v-else-if="poolMode === 'pool'" class="hint">代理池未运行（代理停止或等待提取），启动代理后生效。</div>

    <!-- 池耗尽策略（配置态随时可改；pool 运行中改动热重启生效） -->
    <div v-if="poolMode === 'pool'" class="form-row">
      <span class="label">池空策略</span>
      <n-select
        :value="onEmptyValue"
        :options="onEmptyOptions"
        size="small"
        style="width: 240px"
        :loading="settingOnEmpty"
        @update:value="setOnEmpty"
      />
      <span class="hint-inline">{{ onEmptyHint }}</span>
    </div>

    <!-- 白名单辅助（白名单鉴权账号） -->
    <template v-if="whitelistTarget">
      <div class="sub-title">白名单（{{ whitelistTarget.name }}）</div>
      <div v-for="ip in whitelist" :key="ip" class="form-row wl-row">
        <span class="mono">{{ ip }}</span>
        <n-button size="tiny" quaternary type="error" @click="delWhitelist(ip)">删除</n-button>
      </div>
      <div class="form-row">
        <n-popconfirm @positive-click="addMyIP">
          <template #trigger>
            <n-button size="tiny" secondary :loading="wlBusy">添加本机当前公网 IP</n-button>
          </template>
          将把本机当前公网出口 IP 加入 91HTTP 白名单（该 IP 会暴露给厂商）。继续？
        </n-popconfirm>
        <span class="hint-inline">共 {{ whitelist.length }} 条</span>
      </div>
    </template>

    <!-- 提示区 -->
    <n-alert v-if="needRestart" type="warning" size="small" class="pool-alert" closable @close="needRestart = false">
      配置已保存；代理正在运行，重启代理后生效。
    </n-alert>
    <n-alert v-if="warnings.length" type="warning" size="small" class="pool-alert" closable @close="warnings = []">
      <div v-for="(w, i) in warnings" :key="i">{{ w }}</div>
    </n-alert>
    <div class="hint">AI 分析与 Composer 重发为旁路出站，不经过代理池。</div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { NAlert, NButton, NInput, NInputNumber, NPopconfirm, NSelect, NSwitch, useMessage } from 'naive-ui'
import {
  ListProxyVendors,
  ProxyPoolAddWhitelist,
  ProxyPoolDeleteProvider,
  ProxyPoolDelWhitelist,
  ProxyPoolProviders,
  ProxyPoolRotate,
  ProxyPoolSaveProvider,
  ProxyPoolSetMode,
  ProxyPoolSetOnEmpty,
  ProxyPoolStatus,
  ProxyPoolTestFetch,
  ProxyPoolUseProvider,
  ProxyPoolWhitelist,
  StartProxy,
} from '../../../wailsjs/go/app/App'
import type { app, proxypool, settings } from '../../../wailsjs/go/models'
import { onPoolStatusEvent } from '../../composables/usePoolStatusEvent'

// form 仅用于「使用」后同步外层上游模式下拉（对象 prop 突变，父组件保存时不会把模式改回去）
const props = defineProps<{ form: app.SettingsView }>()
const message = useMessage()

// ---- 数据 ----
const vendors = ref<proxypool.VendorMeta[]>([])
const providers = ref<app.ProviderView[]>([])
const selectedId = ref('')
const poolMode = ref('')
const poolRunning = ref(false)
const poolStatus = ref<proxypool.PoolStatus | null>(null)

// ---- 编辑表单 ----
const editing = ref<settings.ProxyProviderConf | null>(null)
const editIsNew = ref(false)
const testResult = ref<app.ProxyTestResult | null>(null)
const testing = ref(false)
const saving = ref(false)
const using = ref(false)
const editErr = ref('')

// ---- 白名单 ----
const whitelist = ref<string[]>([])
const wlBusy = ref(false)

// ---- 操作反馈 ----
const rotating = ref(false)
const settingOnEmpty = ref(false)
const enabling = ref(false)
const needRestart = ref(false)
const warnings = ref<string[]>([])

// ---- 倒计时（本地 1s 递减，事件/轮询到达时由 expireAt 重新锚定） ----
const nowSec = ref(Math.floor(Date.now() / 1000))
let timer = 0

// ---- 选项常量 ----
const authModes = [
  { label: '自动', value: 'auto' },
  { label: '账密', value: 'userpass' },
  { label: '白名单', value: 'whitelist' },
]
// isp 为字符串契约（'' / '1' / '2' / '3'），见 settings.ProxyProviderConf；
// 值域与 91HTTP 官方一致：1=电信 2=移动 3=联通
const ispOptions = [
  { label: '运营商不限', value: '' },
  { label: '电信', value: '1' },
  { label: '移动', value: '2' },
  { label: '联通', value: '3' },
]

const vendorOptions = computed(() => vendors.value.map((v) => ({ label: v.name, value: v.id })))
const providerOptions = computed(() =>
  providers.value.map((p) => ({ label: p.name + (p.active ? '（生效中）' : ''), value: p.id })),
)
const selectedProvider = computed(() => providers.value.find((p) => p.id === selectedId.value))
// 当前生效账号（可能未被下拉选中；未生效状态一键入口据此渲染）
const activeProvider = computed(() => providers.value.find((p) => p.active) ?? null)
// 一键入口文案：区分「模式没切」与「模式已切但监听没启动」
const enableBtnTitle = computed(() =>
  poolMode.value === 'pool'
    ? '启动代理监听（启动后自动向厂商首次提取 IP，约数秒~30s）'
    : '切换上游为动态代理池并启动代理；代理运行中自动热重启并立即向厂商提取 IP（约数秒~30s）',
)
const enableHint = computed(() =>
  poolMode.value === 'pool'
    ? '代理监听未运行，点击后启动并首次提取'
    : `当前上游不是代理池，点击后生效账号「${activeProvider.value?.name ?? ''}」立即启用`,
)
const editingProvider = computed(() => providers.value.find((p) => p.id === editing.value?.id))
const secretPlaceholder = computed(() =>
  !editIsNew.value && editingProvider.value?.hasSecret ? '已保存，留空保持不变' : 'API 密钥',
)
const pwdPlaceholder = computed(() =>
  !editIsNew.value && editingProvider.value?.hasPassword ? '已保存，留空保持不变' : '代理密码',
)
const whitelistTarget = computed(() =>
  selectedProvider.value?.authMode === 'whitelist' ? selectedProvider.value : null,
)

// 到期展示：expireAt 为 unix 秒（0=未知）；remainSec 由 expireAt 本地推算，无 expireAt 时退化用服务端值
const remainSec = computed(() => {
  const st = poolStatus.value
  if (!st?.running) return 0
  if (st.expireAt && st.expireAt > 0) return Math.max(0, st.expireAt - nowSec.value)
  return st.remainSec ?? 0
})
const remainText = computed(() => {
  const st = poolStatus.value
  if (!st) return '—'
  if (st.expireAt && st.expireAt > 0) return `${fmtClock(st.expireAt)} 到期 · 剩 ${fmtRemain(remainSec.value)}`
  return remainSec.value > 0 ? `剩 ${fmtRemain(remainSec.value)}` : '未知'
})
// 池耗尽策略：配置态取视图顶层 onEmpty（未运行也有值），运行态与 manager 状态一致
const onEmptyOptions = [
  { label: '本地直连（降级走真实 IP）', value: 'direct' },
  { label: '断开服务（拒绝出站，回 502）', value: 'error' },
]
const onEmptyValue = computed(() => onEmptyView.value ?? 'direct')
const onEmptyView = ref('')
const onEmptyHint = computed(() =>
  onEmptyValue.value === 'error'
    ? '池无可用 IP 时断开，不暴露本机真实 IP'
    : '池无可用 IP 时临时直连，会暴露本机真实 IP',
)

async function setOnEmpty(v: string) {
  if (v === onEmptyValue.value) return
  settingOnEmpty.value = true
  try {
    const res = await ProxyPoolSetOnEmpty(v)
    if (res?.status) applyStatus(res.status)
    else onEmptyView.value = v
    warnings.value = res?.warnings ?? []
    message.success(
      v === 'error' ? '已切换为「断开服务」' : '已切换为「本地直连」',
    )
  } catch (e) {
    message.error('策略切换失败：' + errText(e))
  } finally {
    settingOnEmpty.value = false
  }
}

function fmtClock(unixSec: number) {
  return new Date(unixSec * 1000).toLocaleTimeString('zh-CN', { hour12: false })
}
function fmtRemain(sec: number) {
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = sec % 60
  const mm = String(m).padStart(2, '0')
  const ss = String(s).padStart(2, '0')
  return h > 0 ? `${h}:${mm}:${ss}` : `${m}:${ss}`
}
function errText(e: unknown) {
  return e instanceof Error ? e.message : String(e)
}

// ---- 数据装载 ----
function applyStatus(sv: app.PoolStatusView | null) {
  poolMode.value = sv?.mode ?? ''
  poolRunning.value = !!sv?.running
  poolStatus.value = sv?.status ?? null
  onEmptyView.value = sv?.onEmpty ?? ''
}

async function refreshAll() {
  try {
    const [vs, ps, sv] = await Promise.all([ListProxyVendors(), ProxyPoolProviders(), ProxyPoolStatus()])
    vendors.value = vs ?? []
    providers.value = ps ?? []
    applyStatus(sv)
    if (!selectedId.value || !providers.value.some((p) => p.id === selectedId.value)) {
      selectedId.value = sv?.activeProvider || providers.value[0]?.id || ''
    }
  } catch (e) {
    message.error('代理池状态读取失败：' + errText(e))
  }
}

async function refreshProviders() {
  providers.value = (await ProxyPoolProviders()) ?? []
}

async function refreshStatusOnly() {
  applyStatus(await ProxyPoolStatus())
}

// ---- 账号操作 ----
function startNew() {
  editing.value = {
    id: '',
    vendor: vendors.value[0]?.id ?? '91http',
    name: '',
    tradeNo: '',
    secret: '',
    protocol: 'http',
    fetchNum: 1,
    province: '',
    city: '',
    isp: '',
    filterUsed: true,
    authMode: 'auto',
    username: '',
    password: '',
  } as settings.ProxyProviderConf
  editIsNew.value = true
  testResult.value = null
  editErr.value = ''
}

function startEdit() {
  const p = selectedProvider.value
  if (!p) return
  // secret/password 置空 = 保持不变（哨兵语义，密文永不回传）
  editing.value = {
    id: p.id,
    vendor: p.vendor,
    name: p.name,
    tradeNo: p.tradeNo,
    secret: '',
    protocol: p.protocol || 'http',
    fetchNum: p.fetchNum || 1,
    province: p.province || '',
    city: p.city || '',
    isp: p.isp ?? '',
    filterUsed: p.filterUsed ?? true,
    authMode: p.authMode || 'auto',
    username: p.username || '',
    password: '',
  } as settings.ProxyProviderConf
  editIsNew.value = false
  testResult.value = null
  editErr.value = ''
}

function cancelEdit() {
  editing.value = null
  testResult.value = null
  editErr.value = ''
}

function clearSecret() {
  if (!editing.value) return
  editing.value.secret = '__clear__'
  message.info('已标记清空密钥，保存后生效')
}

function clearPassword() {
  if (!editing.value) return
  editing.value.password = '__clear__'
  message.info('已标记清空密码，保存后生效')
}

async function saveProvider() {
  if (!editing.value) return
  saving.value = true
  editErr.value = ''
  try {
    const res = await ProxyPoolSaveProvider(editing.value)
    warnings.value = res.warnings ?? []
    needRestart.value = !res.restarted && poolRunning.value
    selectedId.value = res.provider?.id || selectedId.value
    editing.value = null
    testResult.value = null
    message.success('账号已保存')
    await Promise.all([refreshProviders(), refreshStatusOnly()])
  } catch (e) {
    editErr.value = errText(e)
  } finally {
    saving.value = false
  }
}

async function useProvider() {
  if (!selectedProvider.value) return
  using.value = true
  try {
    const res = await ProxyPoolUseProvider(selectedId.value)
    warnings.value = res.warnings ?? []
    needRestart.value = !res.restarted && poolRunning.value
    // 同步外层上游模式下拉，避免用户随后「保存设置」把模式改回旧值
    props.form.upstreamMode = 'pool'
    message.success(`已启用「${selectedProvider.value.name}」`)
    if (res.status) applyStatus(res.status)
    await refreshProviders()
  } catch (e) {
    message.error('启用失败：' + errText(e))
  } finally {
    using.value = false
  }
}

async function removeProvider() {
  if (!selectedProvider.value) return
  const name = selectedProvider.value.name
  try {
    await ProxyPoolDeleteProvider(selectedId.value)
    message.success(`已删除「${name}」`)
    if (editing.value?.id === selectedId.value) cancelEdit()
    selectedId.value = ''
    await refreshAll()
  } catch (e) {
    message.error('删除失败：' + errText(e))
  }
}

// ---- 测试提取 / 轮换 ----
async function testFetch() {
  if (!editing.value) return
  testing.value = true
  testResult.value = null
  editErr.value = ''
  try {
    testResult.value = await ProxyPoolTestFetch(editing.value)
  } catch (e) {
    editErr.value = errText(e)
  } finally {
    testing.value = false
  }
}

async function rotate() {
  rotating.value = true
  try {
    poolStatus.value = await ProxyPoolRotate()
  } catch (e) {
    message.error('换 IP 失败：' + errText(e))
  } finally {
    rotating.value = false
  }
}

// 组件存活标志：抽屉关闭即卸载，waitForCurrent 的轮询与成功提示必须随之中止（审计 L2）
let alive = true

// 一键提取入口：非 pool 模式先切模式（运行中热重启），仍未运行则拉起监听 → 轮询等待当前出口
async function enableAndFetch() {
  if (!activeProvider.value) return
  enabling.value = true
  try {
    if (poolMode.value !== 'pool') {
      const res = await ProxyPoolSetMode('pool')
      warnings.value = res.warnings ?? []
      props.form.upstreamMode = 'pool'
      if (res.status) applyStatus(res.status)
    }
    // SetMode 只在代理已运行时热重启；代理停着则显式启动（StartProxy 内部按当前模式拉起池）
    if (!poolRunning.value) {
      await StartProxy('')
      applyStatus(await ProxyPoolStatus())
    }
    const ok = await waitForCurrent(30_000)
    if (!alive) return // 抽屉已关闭：不再写状态/弹消息
    if (ok) {
      message.success(`代理池已生效，当前出口 ${poolStatus.value?.current?.addr ?? '已提取'}`)
    } else if (poolRunning.value) {
      message.warning('代理池已启用，首次提取仍在进行，稍后查看运行状态')
    } else {
      message.warning('已切换代理池模式；代理启动失败，请查看主界面错误提示')
    }
    await refreshProviders()
  } catch (e) {
    if (alive) message.error('启用代理池失败：' + errText(e))
  } finally {
    enabling.value = false
  }
}

// 轮询池状态直到出现当前出口（首提取受厂商接口耗时影响）；超时/代理未运行/组件卸载返回 false
async function waitForCurrent(timeoutMs: number): Promise<boolean> {
  const deadline = Date.now() + timeoutMs
  while (alive && Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 1000))
    if (!alive) return false
    try {
      const sv = await ProxyPoolStatus()
      applyStatus(sv)
      if (sv.running && sv.status?.current?.addr) return true
      if (!sv.running) return false
    } catch {
      // 瞬态查询失败不中断轮询
    }
  }
  return false
}

// ---- 白名单 ----
async function refreshWhitelist() {
  const t = whitelistTarget.value
  if (!t) {
    whitelist.value = []
    return
  }
  try {
    whitelist.value = (await ProxyPoolWhitelist(t.id)) ?? []
  } catch {
    whitelist.value = []
  }
}

async function addMyIP() {
  const t = whitelistTarget.value
  if (!t) return
  wlBusy.value = true
  try {
    whitelist.value = (await ProxyPoolAddWhitelist(t.id, '')) ?? []
    message.success('已添加本机公网 IP')
  } catch (e) {
    message.error('添加失败：' + errText(e))
  } finally {
    wlBusy.value = false
  }
}

async function delWhitelist(ip: string) {
  const t = whitelistTarget.value
  if (!t) return
  try {
    whitelist.value = (await ProxyPoolDelWhitelist(t.id, ip)) ?? []
  } catch (e) {
    message.error('删除失败：' + errText(e))
  }
}

watch(whitelistTarget, () => void refreshWhitelist(), { immediate: true })

// ---- 生命周期：初始装载 + proxypool:status 订阅 + 倒计时心跳 ----
onMounted(() => {
  timer = window.setInterval(() => {
    nowSec.value = Math.floor(Date.now() / 1000)
  }, 1000)
  void refreshAll()
})

// 走进程级单订阅：抽屉关闭释放本组件监听时不连带注销常驻底栏的监听（审计 M4）
onPoolStatusEvent((st: any) => {
  poolStatus.value = st as proxypool.PoolStatus
})

onUnmounted(() => {
  alive = false
  window.clearInterval(timer)
})
</script>

<style scoped>
.pool { font-size: 12px; }
.sub-title { font-weight: 600; opacity: 0.85; margin: 10px 0 6px; }
.sub-title:first-child { margin-top: 0; }
.form-row { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; flex-wrap: wrap; }
.label { width: 56px; flex: none; opacity: 0.7; }
.label-s { flex: none; opacity: 0.7; }
.hint { opacity: 0.5; font-size: 11px; margin-top: 4px; }
.hint-inline { opacity: 0.5; font-size: 11px; }
.mono { font-variant-numeric: tabular-nums; }
.danger { color: #e88080; }
.link { color: #63e2b7; text-decoration: none; }
.link:hover { text-decoration: underline; }
.btn-row { margin-top: 2px; }
.test-result { font-size: 11px; opacity: 0.8; margin: 2px 0 6px; word-break: break-all; }
.pool-alert { margin: 4px 0 8px; }
/* 非 pool 模式一键启用入口：与账号区拉开间距 */
.pool-enable-row { margin-top: 6px; gap: 8px; }
.wl-row { margin-bottom: 4px; }
</style>
