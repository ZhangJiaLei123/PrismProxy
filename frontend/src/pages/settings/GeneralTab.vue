<template>
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
    <div v-if="sys.state === 'on' && overrideItems.length" class="override-box">
      <div class="override-title">生效中的绕过列表（{{ overrideItems.length }} 项）</div>
      <div class="override-tags">
        <span v-for="item in overrideItems" :key="item" class="override-chip">{{ item }}</span>
      </div>
    </div>
    <n-checkbox v-model:checked="form.autoSysProxy" size="small" style="margin-top: 8px">
      自动开启系统代理
    </n-checkbox>
    <div class="hint">保存后生效；下次启动程序并成功监听后自动接管系统代理。</div>
  </section>

  <section class="sec">
    <div class="sec-title">工具栏</div>
    <n-checkbox v-model:checked="form.showSysProxySwitch" size="small">
      在工具栏显示系统代理开关
    </n-checkbox>
    <div class="hint">保存后生效；隐藏时仍可从本页控制系统代理。</div>
  </section>

  <section class="sec">
    <div class="sec-title">根证书</div>
    <div style="display: flex; align-items: center; gap: 10px">
      <n-button size="small" secondary :loading="caBusy" @click="installCA">安装根证书（当前用户）</n-button>
      <span class="hint">系统弹出安全警告时请点"是"</span>
    </div>
    <div v-if="caMsg" class="hint break">{{ caMsg }}</div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NSwitch, NTag, NButton, NCheckbox } from 'naive-ui'
import { GetSystemProxyStatus, SetSystemProxy, InstallRootCA } from '../../../wailsjs/go/app/App'
import type { app, settings } from '../../../wailsjs/go/models'

defineProps<{ form: settings.Settings }>()
const emit = defineEmits<{ (e: 'changed'): void }>()

// ---- 系统代理状态 ----
const sys = ref<app.SystemProxyStatus>({ state: 'off', server: '', override: '' } as app.SystemProxyStatus)
const sysBusy = ref(false)

// ProxyOverride 注册表值为分号分隔的单行字符串，拆成条目用于标签展示
const overrideItems = computed(() =>
  (sys.value.override || '')
    .split(';')
    .map((s) => s.trim())
    .filter(Boolean),
)

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

onMounted(loadSysStatus)
</script>

<style scoped>
.sec { font-size: 12px; }
.sec + .sec { margin-top: 20px; }
.sec-title { font-weight: 600; font-size: 13px; margin-bottom: 8px; }
.hint { opacity: 0.5; font-size: 11px; margin-top: 4px; }
.hint.break { word-break: break-all; }
.override-box {
  margin-top: 8px;
  padding: 8px 10px;
  border: 1px solid var(--n-border-color, rgba(128, 128, 128, 0.2));
  border-radius: 4px;
  background: rgba(128, 128, 128, 0.06);
}
.override-title { font-size: 11px; opacity: 0.6; margin-bottom: 6px; }
.override-tags { display: flex; flex-wrap: wrap; gap: 6px; max-height: 96px; overflow-y: auto; }
.override-chip {
  padding: 2px 8px;
  border-radius: 3px;
  background: var(--n-color-embedded, rgba(128, 128, 128, 0.14));
  color: var(--n-text-color-3, rgba(128, 128, 128, 0.95));
  font-family: var(--n-font-family-mono, monospace);
  font-size: 11px;
  line-height: 18px;
}
</style>
