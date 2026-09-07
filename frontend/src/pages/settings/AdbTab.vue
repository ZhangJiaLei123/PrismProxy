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

    <div v-for="c in form.adb.configs" :key="rid(c)" class="adb-card">
      <div class="form-row">
        <span class="label">名称</span>
        <n-input v-model:value="c.name" placeholder="如：雷电模拟器" style="width: 160px" />
        <n-switch v-model:value="c.autoSet" size="small" />
        <span class="autoset-hint">启动代理时自动设置 / 停止时自动清除</span>
      </div>
      <div class="form-row">
        <span class="label">adb 路径</span>
        <n-input v-model:value="c.path" placeholder="如 D:\leidian\LDPlayer14\adb.exe" style="flex: 1" />
        <n-button size="small" :loading="busy['pick-' + rid(c)]" @click="pick(c)">浏览</n-button>
      </div>
      <div class="form-row">
        <span class="label">序列号</span>
        <n-input v-model:value="c.serial" placeholder="可选；多台设备（多开/真机+模拟器）时填，adb devices 第一列" style="flex: 1" />
      </div>
      <div class="form-row btn-row">
        <n-button size="small" :loading="busy['test-' + rid(c)]" @click="test(c)">测试连接</n-button>
        <n-button size="small" type="primary" :loading="busy['set-' + rid(c)]" @click="setProxy(c)">设置代理</n-button>
        <n-button size="small" :loading="busy['clear-' + rid(c)]" @click="clearProxy(c)">清除代理</n-button>
        <n-button size="small" type="error" quaternary @click="remove(c)">删除</n-button>
      </div>
    </div>

    <n-button size="small" block secondary style="margin-top: 4px" @click="add">+ 添加配置</n-button>
    <div class="hint">
      「设置代理」需代理已启动；每条配置对应一个模拟器/设备的 adb。单台设备可只填路径；
      同一 adb 下有多台设备（模拟器多开/真机+模拟器）时，必须为每条配置填写对应设备「序列号」，否则 adb 会报 more than one device。
      「自动设置」勾选后，PrismProxy 启动代理即对该设备写入 http_proxy，停止代理/退出/关机时自动清除。
    </div>
  </section>
</template>

<script setup lang="ts">
import { reactive } from 'vue'
import { NInput, NButton, NSwitch, useMessage, useDialog } from 'naive-ui'
import { PickAdbPath, AdbTest, AdbSetProxy, AdbClearProxy } from '../../../wailsjs/go/app/App'
import type { settings } from '../../../wailsjs/go/models'

const props = defineProps<{ form: settings.Settings }>()
const message = useMessage()
const dialog = useDialog()
const busy = reactive<Record<string, boolean>>({})

// 行级稳定 id：配置对象不持久 id（settings.ADBDevice 无 id 字段），用 WeakMap 按对象引用
// 分配运行时 id，保证删除中间行后 :key 与 busy 状态不错位、异步回写不落到移位后的别的行。
let seq = 0
const rids = new WeakMap<object, number>()
function rid(c: settings.ADBDevice): number {
  let id = rids.get(c)
  if (id === undefined) {
    seq += 1
    id = seq
    rids.set(c, id)
  }
  return id
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

function add() {
  props.form.adb.configs.push({
    name: '模拟器 ' + (props.form.adb.configs.length + 1),
    path: '',
    serial: '',
    autoSet: false,
  } as settings.ADBDevice)
}
function remove(c: settings.ADBDevice) {
  const label = c.name?.trim() || '该配置'
  dialog.warning({
    title: '删除 ADB 配置',
    content: `确定删除「${label}」？删除后保存生效；若已勾选自动设置且代理正在运行，会同时清除该设备上的代理设置。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: () => {
      const idx = props.form.adb.configs.indexOf(c)
      if (idx >= 0) props.form.adb.configs.splice(idx, 1)
    },
  })
}

async function pick(c: settings.ADBDevice) {
  await wrap('pick-' + rid(c), async () => {
    const p = await PickAdbPath()
    if (p) {
      const cur = props.form.adb.configs.find((x) => rid(x) === rid(c))
      if (cur) cur.path = p // 对话框返回可能在数秒后：按稳定 id 回写，行已删则静默放弃
    }
    return ''
  })
}
async function test(c: settings.ADBDevice) {
  await wrap('test-' + rid(c), () => AdbTest(c.path))
}
async function setProxy(c: settings.ADBDevice) {
  await wrap('set-' + rid(c), () => AdbSetProxy(c.path, c.serial ?? '', props.form.adb.deviceProxyHost))
}
async function clearProxy(c: settings.ADBDevice) {
  await wrap('clear-' + rid(c), () => AdbClearProxy(c.path, c.serial ?? ''))
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
