<template>
  <section class="sec">
    <div class="sec-title">设备侧代理地址</div>
    <div class="form-row">
      <span class="label">宿主机 IP</span>
      <n-input v-model:value="form.adb.deviceProxyHost" placeholder="172.16.1.2" style="width: 220px" />
    </div>
    <div class="hint">
      模拟器内访问宿主机 PrismProxy 的 IP（雷电 NAT 默认 172.16.1.2；其他模拟器/真机请填宿主机可达 IP）。
      端口自动取代理监听端口。设置/清除操作即时执行，配置项保存后生效。
    </div>
  </section>

  <section class="sec">
    <div class="sec-title">ADB 设备配置（可多条）</div>

    <div v-for="(c, i) in form.adb.configs" :key="i" class="adb-card">
      <div class="form-row">
        <span class="label">名称</span>
        <n-input v-model:value="c.name" placeholder="如：雷电模拟器" style="width: 180px" />
        <n-switch v-model:value="c.autoSet" size="small" />
        <span class="autoset-hint">启动代理时自动设置 / 停止时自动清除</span>
      </div>
      <div class="form-row">
        <span class="label">adb 路径</span>
        <n-input v-model:value="c.path" placeholder="如 D:\leidian\LDPlayer14\adb.exe" style="flex: 1" />
        <n-button size="small" :loading="busy['pick' + i]" @click="pick(i)">浏览</n-button>
      </div>
      <div class="form-row btn-row">
        <n-button size="small" :loading="busy['test' + i]" @click="test(i)">测试连接</n-button>
        <n-button size="small" type="primary" :loading="busy['set' + i]" @click="setProxy(i)">设置代理</n-button>
        <n-button size="small" :loading="busy['clear' + i]" @click="clearProxy(i)">清除代理</n-button>
        <n-button size="small" type="error" quaternary @click="remove(i)">删除</n-button>
      </div>
    </div>

    <n-button size="small" block secondary style="margin-top: 4px" @click="add">+ 添加配置</n-button>
    <div class="hint">
      「设置代理」需代理已启动；每条配置对应一个模拟器/设备的 adb（多开可各自一条）。
      「自动设置」勾选后，PrismProxy 启动代理即对该设备写入 http_proxy，停止代理/退出时自动清除。
    </div>
  </section>
</template>

<script setup lang="ts">
import { reactive } from 'vue'
import { NInput, NButton, NSwitch, useMessage } from 'naive-ui'
import { PickAdbPath, AdbTest, AdbSetProxy, AdbClearProxy } from '../../../wailsjs/go/app/App'
import type { settings } from '../../../wailsjs/go/models'

const props = defineProps<{ form: settings.Settings }>()
const message = useMessage()
const busy = reactive<Record<string, boolean>>({})

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

function add() {
  props.form.adb.configs.push({
    name: '模拟器 ' + (props.form.adb.configs.length + 1),
    path: '',
    autoSet: false,
  } as settings.ADBDevice)
}
function remove(i: number) {
  props.form.adb.configs.splice(i, 1)
}

async function pick(i: number) {
  await wrap('pick' + i, async () => {
    const p = await PickAdbPath()
    if (p) props.form.adb.configs[i].path = p
    return ''
  })
}
async function test(i: number) {
  await wrap('test' + i, () => AdbTest(props.form.adb.configs[i].path))
}
async function setProxy(i: number) {
  const c = props.form.adb.configs[i]
  await wrap('set' + i, () => AdbSetProxy(c.path, props.form.adb.deviceProxyHost))
}
async function clearProxy(i: number) {
  await wrap('clear' + i, () => AdbClearProxy(props.form.adb.configs[i].path))
}
</script>

<style scoped>
.sec { font-size: 12px; }
.sec + .sec { margin-top: 20px; }
.sec-title { font-weight: 600; font-size: 13px; margin-bottom: 8px; }
.form-row { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
.label { width: 56px; flex: none; opacity: 0.7; }
.hint { opacity: 0.5; font-size: 11px; margin-top: 4px; line-height: 1.6; }
.adb-card {
  border: 1px solid var(--n-border-color, rgba(128, 128, 128, 0.24));
  border-radius: 6px;
  padding: 10px;
  margin-bottom: 10px;
}
.autoset-hint { font-size: 11px; opacity: 0.6; }
.btn-row { margin-bottom: 0; }
</style>
