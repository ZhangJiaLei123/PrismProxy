<template>
  <n-drawer :show="show" :width="560" placement="right" @update:show="close">
    <n-drawer-content title="设置" closable>
      <!-- 系统代理：实时控制，不随表单保存（验收 #10） -->
      <section class="sec">
        <div class="sec-title">系统代理</div>
        <div style="display: flex; align-items: center; gap: 10px">
          <n-switch :value="sys.state === 'on'" :loading="sysBusy" @update:value="toggleSysProxy" />
          <n-tag size="small" :type="sysTagType">{{ sysTagText }}</n-tag>
          <n-button size="tiny" quaternary @click="loadSysStatus">刷新</n-button>
        </div>
        <div v-if="sys.state === 'occupied'" class="hint">
          系统代理当前指向 {{ sys.server }}（其他程序占用），开启将接管，关闭恢复。
        </div>
        <div v-if="sys.state === 'on' && sys.override" class="hint break">
          生效中的绕过列表：{{ sys.override }}
        </div>
      </section>

      <n-divider />

      <!-- 基本设置 -->
      <section class="sec">
        <div class="sec-title">基本</div>
        <div class="form-row">
          <span class="label">监听地址</span>
          <n-select v-model:value="bindIP" :options="ipOptions" filterable tag style="width: 180px" />
          <n-input-number v-model:value="bindPort" :min="1" :max="65535" :show-button="false" placeholder="端口" style="width: 100px" />
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
      </section>

      <n-divider />

      <!-- 系统代理绕过列表 -->
      <section class="sec">
        <div class="sec-title">系统代理绕过列表（ProxyOverride）</div>
        <n-dynamic-tags v-model:value="form.bypassList" />
        <div class="hint">裸域名匹配自身+全部子域；&lt;-loopback&gt; 绕过本地回环。接管状态下需重新开关系统代理生效。</div>
      </section>

      <n-divider />

      <!-- 解密规则 -->
      <section class="sec">
        <div class="sec-title">解密规则</div>
        <n-dynamic-input v-model:value="form.decryptRules" :on-create="() => ({ action: 'bypass', host: '' })">
          <template #default="{ value }">
            <div class="rule-row">
              <n-select v-model:value="value.action" :options="decryptActions" style="width: 120px" />
              <n-input v-model:value="value.host" placeholder="域名或 @组名" />
            </div>
          </template>
        </n-dynamic-input>
        <div class="hint">自上而下首条命中生效，未命中默认 MITM 解密；bypass = 盲透传（应对 SSL Pinning）。</div>
      </section>

      <n-divider />

      <!-- 捕获规则 -->
      <section class="sec">
        <div class="sec-title">捕获规则</div>
        <n-dynamic-input
          v-model:value="form.captureRules"
          :on-create="() => ({ action: 'exclude', host: '', urlRe: '', method: '' })"
        >
          <template #default="{ value }">
            <div class="rule-row">
              <n-select v-model:value="value.action" :options="captureActions" style="width: 105px" />
              <n-input v-model:value="value.host" placeholder="域名或 @组名" style="width: 140px" />
              <n-input v-model:value="value.method" placeholder="方法,逗号" style="width: 95px" />
              <n-input v-model:value="value.urlRe" placeholder="URL 正则" />
            </div>
          </template>
        </n-dynamic-input>
        <div class="hint">不记录 = 正常转发但不入列表（屏蔽遥测/心跳）；空维度 = 任意；未命中默认记录。</div>
      </section>

      <n-divider />

      <!-- 进程规则 -->
      <section class="sec">
        <div class="sec-title">进程规则</div>
        <n-dynamic-input v-model:value="form.processRules" :on-create="() => ({ action: 'include', name: '' })">
          <template #default="{ value }">
            <div class="rule-row">
              <n-select v-model:value="value.action" :options="captureActions" style="width: 105px" />
              <n-input v-model:value="value.name" placeholder="进程名，如 dnplayer.exe" />
            </div>
          </template>
        </n-dynamic-input>
        <div class="hint">按进程名精确匹配（不区分大小写）；未命中默认记录。</div>
      </section>

      <n-divider />

      <!-- 域名组与证书 -->
      <section class="sec">
        <div class="sec-title">域名组（规则中 @组名 引用）</div>
        <div class="hint break">{{ groupText || '无可用域名组' }}</div>
      </section>

      <section class="sec">
        <div class="sec-title">根证书</div>
        <div style="display: flex; align-items: center; gap: 10px">
          <n-button size="small" secondary :loading="caBusy" @click="installCA">安装根证书（当前用户）</n-button>
          <span class="hint">系统弹出安全警告时请点"是"</span>
        </div>
        <div v-if="caMsg" class="hint break">{{ caMsg }}</div>
      </section>

      <n-alert v-if="saveErr" type="error" style="margin-top: 12px">{{ saveErr }}</n-alert>

      <template #footer>
        <div style="display: flex; gap: 8px; justify-content: flex-end">
          <n-button @click="close">取消</n-button>
          <n-button type="primary" :loading="saving" @click="save">保存</n-button>
        </div>
      </template>
    </n-drawer-content>
  </n-drawer>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  NDrawer, NDrawerContent, NSwitch, NTag, NButton, NDivider, NSelect, NInput, NInputNumber,
  NDynamicTags, NDynamicInput, NAlert,
} from 'naive-ui'
import {
  GetSettings, SaveSettings, GetSystemProxyStatus, SetSystemProxy,
  GetLocalAddrs, ListDomainGroups, InstallRootCA,
} from '../../wailsjs/go/main/App'
import type { main, settings } from '../../wailsjs/go/models'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
  (e: 'changed'): void // 设置已保存或系统代理状态变化（父组件刷新头部状态）
}>()

// ---- 表单状态 ----
const emptyForm = (): settings.Settings =>
  ({
    listenAddr: '127.0.0.1:9090',
    upstreamMode: 'direct',
    upstreamProxy: '',
    maxFlows: 2000,
    maxBodyMB: 256,
    bypassList: [],
    captureRules: [],
    decryptRules: [],
    processRules: [],
  }) as settings.Settings

const form = ref<settings.Settings>(emptyForm())
const bindIP = ref('127.0.0.1')
const bindPort = ref<number | null>(9090)
const ipOptions = ref<{ label: string; value: string }[]>([])
const groupText = ref('')
const saving = ref(false)
const saveErr = ref('')

// ---- 系统代理状态 ----
const sys = ref<main.SystemProxyStatus>({ state: 'off', server: '', override: '' } as main.SystemProxyStatus)
const sysBusy = ref(false)

const sysTagType = computed(() => (sys.value.state === 'on' ? 'success' : sys.value.state === 'occupied' ? 'warning' : 'default'))
const sysTagText = computed(() =>
  sys.value.state === 'on' ? '已接管 ' + sys.value.server : sys.value.state === 'occupied' ? '被占用 ' + sys.value.server : '未启用',
)

async function loadSysStatus() {
  sys.value = await GetSystemProxyStatus()
}

async function toggleSysProxy(v: boolean) {
  sysBusy.value = true
  try {
    await SetSystemProxy(v)
    await loadSysStatus()
    emit('changed') // 接管会自动启动代理，父组件刷新运行状态
  } finally {
    sysBusy.value = false
  }
}

// ---- 载入 ----
watch(
  () => props.show,
  async (v) => {
    if (!v) return
    saveErr.value = ''
    caMsg.value = ''
    const s = await GetSettings()
    form.value = JSON.parse(JSON.stringify(s)) as settings.Settings
    form.value.bypassList ??= []
    form.value.captureRules ??= []
    form.value.decryptRules ??= []
    form.value.processRules ??= []
    // 拆分监听地址为 IP + 端口（仅处理 IPv4 host:port 常规形态）
    const i = form.value.listenAddr.lastIndexOf(':')
    if (i > 0) {
      bindIP.value = form.value.listenAddr.slice(0, i)
      bindPort.value = Number(form.value.listenAddr.slice(i + 1)) || 9090
    } else {
      bindIP.value = form.value.listenAddr || '127.0.0.1'
      bindPort.value = 9090
    }
    const addrs = await GetLocalAddrs()
    const ips = new Set(['127.0.0.1', '0.0.0.0', ...(addrs ?? [])])
    ipOptions.value = [...ips].map((a) => ({ label: a === '0.0.0.0' ? '0.0.0.0（局域网）' : a, value: a }))
    const groups = await ListDomainGroups()
    groupText.value = ((groups?.names as string[]) ?? []).map((n) => '@' + n).join(' ')
    await loadSysStatus()
  },
)

// ---- 保存 ----
async function save() {
  saving.value = true
  saveErr.value = ''
  try {
    const nu = JSON.parse(JSON.stringify(form.value)) as settings.Settings
    nu.listenAddr = (bindIP.value || '127.0.0.1') + ':' + (bindPort.value || 9090)
    await SaveSettings(nu)
    emit('changed')
    emit('update:show', false)
  } catch (e) {
    saveErr.value = String(e)
  } finally {
    saving.value = false
  }
}

function close() {
  emit('update:show', false)
}

// ---- 根证书 ----
const caBusy = ref(false)
const caMsg = ref('')

async function installCA() {
  caBusy.value = true
  caMsg.value = ''
  try {
    await InstallRootCA()
    caMsg.value = '已安装到当前用户受信根存储。'
  } catch (e) {
    caMsg.value = String(e)
  } finally {
    caBusy.value = false
  }
}

// ---- 选项常量 ----
const upstreamModes = [
  { label: '直连', value: 'direct' },
  { label: '手动代理', value: 'manual' },
  { label: '跟随系统', value: 'system' },
]
const decryptActions = [
  { label: 'MITM 解密', value: 'mitm' },
  { label: 'bypass 透传', value: 'bypass' },
]
const captureActions = [
  { label: '记录', value: 'include' },
  { label: '不记录', value: 'exclude' },
]
</script>

<style scoped>
.sec { font-size: 12px; }
.sec-title { font-weight: 600; font-size: 13px; margin-bottom: 8px; }
.form-row { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
.label { width: 56px; flex: none; opacity: 0.7; }
.hint { opacity: 0.5; font-size: 11px; margin-top: 4px; }
.hint.break { word-break: break-all; }
.rule-row { display: flex; gap: 6px; width: 100%; }
:deep(.n-divider) { margin: 14px 0; }
</style>
