<template>
  <n-alert type="info" :bordered="false" class="bar">
    导入/删除即时生效（自动落盘并热更新规则），无需点击底部「保存」。过滤/解密规则中以 @组ID 引用。
  </n-alert>

  <!-- 导入 -->
  <section class="sec">
    <div class="sec-title">导入域名组</div>
    <div class="row">
      <n-input
        v-model:value="importId"
        size="small"
        placeholder="组 ID（可选，留空取文件名）"
        style="max-width: 210px"
      />
      <n-button size="small" secondary :loading="fileBusy" @click="importFile">本地导入…</n-button>
    </div>
    <div class="row">
      <n-input v-model:value="importURL" size="small" placeholder="https://example.com/domains.txt 或 index.json" style="flex: 1" />
      <n-button size="small" secondary :loading="urlBusy" :disabled="!importURL.trim()" @click="importFromURL">
        URL 导入
      </n-button>
    </div>
    <div class="hint">
      文件格式：每行一个域名，# 开头为注释；组 ID 限小写字母/数字/连字符。与内置组同 ID 时覆盖内置组。
      URL 支持直接域名组 txt 或索引 index.json（索引可勾选下载指定组）。
    </div>
  </section>

  <!-- 索引勾选弹窗 -->
  <n-modal v-model:show="showIndex" preset="card" title="索引文件：勾选要导入的域名组" style="width: 460px">
    <div class="row">
      <n-button size="tiny" tertiary @click="checkAll">全选</n-button>
      <n-button size="tiny" tertiary @click="indexChecked = []">清空</n-button>
      <span class="hint">已选 {{ indexChecked.length }} / {{ indexEntries.length }}</span>
    </div>
    <div class="idx-list">
      <n-checkbox-group v-model:value="indexChecked">
        <div v-for="e in indexEntries" :key="e.id" class="idx-row">
          <n-checkbox :value="e.id" size="small">
            {{ e.name || e.id }}
            <n-tag size="tiny" :bordered="false" type="info">@{{ e.id }}</n-tag>
            <n-tag v-if="existingIDs.has(e.id)" size="tiny" :bordered="false" type="warning">已存在</n-tag>
            <span v-if="e.category" class="hint"> · {{ e.category }}</span>
          </n-checkbox>
        </div>
      </n-checkbox-group>
      <n-empty v-if="!indexEntries.length" description="索引中没有可导入的组" size="small" />
    </div>
    <div v-if="showProgress" class="prog-wrap">
      <n-progress type="line" :percentage="progressPercent" :height="14" :border-radius="7" />
      <div class="hint" style="margin-top: 4px">
        {{ progressCurrent }} / {{ progressTotal }}<span v-if="progressId"> · 正在下载 @{{ progressId }}</span>
      </div>
    </div>
    <template #footer>
      <div class="row" style="justify-content: flex-end; margin-bottom: 0">
        <n-button size="small" :disabled="indexBusy" @click="showIndex = false">取消</n-button>
        <n-button size="small" type="primary" :disabled="!indexChecked.length" :loading="indexBusy" @click="importFromIndex">
          导入所选（{{ indexChecked.length }}）
        </n-button>
      </div>
    </template>
  </n-modal>

  <!-- 编辑自定义域名组弹窗 -->
  <n-modal v-model:show="showEdit" preset="card" :title="`编辑域名组 @${editId}`" style="width: 560px">
    <n-input
      v-model:value="editText"
      type="textarea"
      placeholder="每行一个域名，# 开头为注释"
      class="edit-area"
      :autosize="{ minRows: 16, maxRows: 24 }"
    />
    <div class="hint" style="margin-top: 6px">
      保存后即时落盘并热更新规则（空文件或无有效域名会被拒绝）。
    </div>
    <template #footer>
      <div class="row" style="justify-content: flex-end; margin-bottom: 0">
        <n-button size="small" @click="showEdit = false">取消</n-button>
        <n-button size="small" type="primary" :loading="editBusy" @click="saveEdit">保存</n-button>
      </div>
    </template>
  </n-modal>

  <!-- 组列表 -->
  <section class="sec">
    <div class="sec-title">域名组（{{ list.length }}）</div>
    <div v-for="g in list" :key="g.id" class="group-row">
      <div class="g-main">
        <span class="g-name" :title="g.name">{{ g.name }}</span>
        <n-tag size="tiny" :bordered="false" type="info">@{{ g.id }}</n-tag>
        <n-tag v-if="g.custom" size="tiny" :bordered="false" type="warning">自定义</n-tag>
        <n-tag v-else size="tiny" :bordered="false">内置</n-tag>
        <n-tag v-if="g.category" size="tiny" :bordered="false" type="default">{{ g.category }}</n-tag>
      </div>
      <div class="g-sub">
        <span class="hint">{{ g.count }} 个域名</span>
      </div>
      <span class="spacer" />
      <div class="g-actions">
        <n-button size="tiny" quaternary @click="openEdit(g)">编辑</n-button>
        <n-button size="tiny" quaternary @click="exportGroup(g)">导出</n-button>
        <n-popconfirm @positive-click="removeGroup(g)">
          <template #trigger>
            <n-button size="tiny" quaternary type="error">删除</n-button>
          </template>
          删除域名组「{{ g.name }}」？此操作不可撤销。
        </n-popconfirm>
      </div>
    </div>
    <n-empty v-if="!list.length" description="暂无域名组，从上方本地文件或 URL 导入" size="small" style="margin: 12px 0" />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCheckbox, NCheckboxGroup, NEmpty, NInput, NModal, NPopconfirm, NProgress, NTag, useDialog, useMessage } from 'naive-ui'
import { EventsOff, EventsOn } from '../../../wailsjs/runtime/runtime'
import {
  DeleteDomainGroup,
  ExportDomainGroup,
  GetDomainGroupText,
  ImportDomainGroupFile,
  ImportDomainGroupURL,
  ImportDomainGroupsFromIndex,
  ListDomainGroupDetails,
  ProbeURLImport,
  SaveDomainGroupText,
} from '../../../wailsjs/go/app/App'
import type { app } from '../../../wailsjs/go/models'

const message = useMessage()
const dialog = useDialog()
const list = ref<app.DomainGroupInfo[]>([])

async function refresh() {
  list.value = (await ListDomainGroupDetails()) ?? []
}

// 本地已存在的组 ID（用于索引列表「已存在」标记与冲突检测）
const existingIDs = computed(() => new Set(list.value.map((g) => g.id)))

// ---- 导入 ----
const importId = ref('')
const importURL = ref('')
const fileBusy = ref(false)
const urlBusy = ref(false)

async function afterImport(res: app.DomainGroupImportResult | null) {
  if (!res) return // 用户取消对话框
  message.success(`已导入 @${res.id}（${res.count} 个域名）`, { closable: true, duration: 4000 })
  importId.value = ''
  importURL.value = ''
  await refresh()
}

async function importFile() {
  fileBusy.value = true
  try {
    await afterImport(await ImportDomainGroupFile(importId.value.trim()))
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
  } finally {
    fileBusy.value = false
  }
}

async function importFromURL() {
  urlBusy.value = true
  try {
    const url = importURL.value.trim()
    const probe = await ProbeURLImport(url)
    if (probe.kind === 'index') {
      indexURL.value = url
      indexEntries.value = probe.entries ?? []
      indexChecked.value = indexEntries.value.map((e) => e.id)
      showIndex.value = true
      return
    }
    await afterImport(await ImportDomainGroupURL(url, importId.value.trim()))
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
  } finally {
    urlBusy.value = false
  }
}

// ---- 索引勾选导入 ----
const showIndex = ref(false)
const indexURL = ref('')
const indexEntries = ref<app.DomainIndexEntry[]>([])
const indexChecked = ref<string[]>([])
const indexBusy = ref(false)

// 批量导入进度
const showProgress = ref(false)
const progressCurrent = ref(0)
const progressTotal = ref(0)
const progressId = ref('')
const progressPercent = computed(() => {
  if (!progressTotal.value) return 0
  return Math.round((progressCurrent.value / progressTotal.value) * 100)
})

function checkAll() {
  indexChecked.value = indexEntries.value.map((e) => e.id)
}

// 冲突时让用户一次性选择：覆盖全部 / 跳过已存在；取消则中止导入
function askOverwrite(conflictIDs: string[]): Promise<boolean | null> {
  return new Promise((resolve) => {
    const d = dialog.warning({
      title: '发现本地同名域名组',
      content: `以下 ${conflictIDs.length} 个组本地已存在：${conflictIDs.map((id) => '@' + id).join('、')}。覆盖将替换本地文件，跳过则保留本地不下载。`,
      positiveText: '全部覆盖',
      negativeText: '跳过已存在',
      closable: false,
      maskClosable: false,
      onPositiveClick: () => {
        resolve(true)
      },
      onNegativeClick: () => {
        resolve(false)
      },
      onClose: () => {
        resolve(null)
      },
    })
  })
}

async function doImportFromIndex(overwrite: boolean) {
  showProgress.value = true
  progressCurrent.value = 0
  progressTotal.value = indexChecked.value.length
  progressId.value = ''
  // 监听后端逐项进度事件
  EventsOn('index-import-progress', (data: any) => {
    progressCurrent.value = data.current ?? 0
    progressTotal.value = data.total ?? progressTotal.value
    progressId.value = data.id ?? ''
  })
  try {
    const results = (await ImportDomainGroupsFromIndex(indexURL.value, indexChecked.value, overwrite)) ?? []
    const ok = results.filter((r) => !r.err && !r.skipped)
    const skipped = results.filter((r) => r.skipped)
    const fail = results.filter((r) => r.err)
    if (ok.length) {
      message.success(`已导入 ${ok.length} 个组：${ok.map((r) => '@' + r.id).join('、')}`, { closable: true, duration: 5000 })
    }
    if (skipped.length) {
      message.info(`跳过 ${skipped.length} 个已存在组：${skipped.map((r) => '@' + r.id).join('、')}`, { closable: true, duration: 5000 })
    }
    if (fail.length) {
      message.error(`${fail.length} 个失败：${fail.map((r) => `@${r.id}（${r.err}）`).join('；')}`, { closable: true, duration: 8000 })
    }
    showIndex.value = false
    importId.value = ''
    importURL.value = ''
    await refresh()
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
  } finally {
    EventsOff('index-import-progress')
    indexBusy.value = false
    showProgress.value = false
  }
}

async function importFromIndex() {
  const conflictIDs = indexChecked.value.filter((id) => existingIDs.value.has(id))
  let overwrite = true
  if (conflictIDs.length) {
    const choice = await askOverwrite(conflictIDs)
    if (choice === null) return // 用户中止
    overwrite = choice
  }
  indexBusy.value = true
  await doImportFromIndex(overwrite)
}

// ---- 导出 / 删除 ----
async function exportGroup(g: app.DomainGroupInfo) {
  try {
    const dest = await ExportDomainGroup(g.id)
    if (dest) message.success(`已导出到 ${dest}`, { closable: true, duration: 5000 })
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
  }
}

async function removeGroup(g: app.DomainGroupInfo) {
  try {
    await DeleteDomainGroup(g.id)
    message.success(`已删除 @${g.id}`, { duration: 3000 })
    await refresh()
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
  }
}

// ---- 编辑自定义域名组 ----
const showEdit = ref(false)
const editId = ref('')
const editText = ref('')
const editBusy = ref(false)

async function openEdit(g: app.DomainGroupInfo) {
  try {
    editText.value = await GetDomainGroupText(g.id)
    editId.value = g.id
    showEdit.value = true
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
  }
}

async function saveEdit() {
  editBusy.value = true
  try {
    const res = await SaveDomainGroupText(editId.value, editText.value)
    message.success(`已保存 @${res.id}（${res.count} 个域名）`, { closable: true, duration: 4000 })
    showEdit.value = false
    await refresh()
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
  } finally {
    editBusy.value = false
  }
}

onMounted(refresh)
</script>

<style scoped>
.bar { margin-bottom: 10px; font-size: 12px; }
.sec { font-size: 12px; }
.sec + .sec { margin-top: 16px; }
.sec-title { font-weight: 600; font-size: 13px; margin-bottom: 8px; }
.row { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
.hint { opacity: 0.5; font-size: 11px; }
.group-row { display: flex; align-items: center; gap: 8px; padding: 6px 0; border-bottom: 1px dashed rgba(128, 128, 128, 0.15); }
.g-main { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; min-width: 0; }
.g-sub { flex-shrink: 0; }
.g-actions { display: flex; align-items: center; gap: 2px; flex-shrink: 0; }
.idx-row { padding: 2px 0; }
.idx-list { max-height: 50vh; overflow-y: auto; padding-right: 4px; }
.prog-wrap { margin-top: 10px; }
.edit-area :deep(textarea) { font-family: ui-monospace, Consolas, monospace; font-size: 12px; }
.g-name { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.spacer { flex: 1; }
</style>
