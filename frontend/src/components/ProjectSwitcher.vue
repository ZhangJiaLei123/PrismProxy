<template>
  <n-select
    class="proj-select"
    size="small"
    :value="currentId"
    :options="options"
    :loading="busy"
    placeholder="项目"
    @update:value="switchTo"
  >
    <template #action>
      <div class="proj-actions">
        <n-button size="tiny" quaternary @click="openCreate">新建…</n-button>
        <n-button size="tiny" quaternary @click="openRename">重命名…</n-button>
        <n-button size="tiny" quaternary type="error" @click="askDelete">删除…</n-button>
      </div>
    </template>
  </n-select>

  <!-- 新建项目弹窗（设计 §7.1：n-modal preset card + n-input，useDialog 不支持内嵌输入框） -->
  <n-modal v-model:show="showCreate" preset="card" title="新建项目" style="width: 420px">
    <div class="form-row">
      <span class="label">名称</span>
      <n-input v-model:value="createName" placeholder="项目名称" :input-props="{ spellcheck: false }" @keyup.enter="confirmCreate" />
    </div>
    <div class="form-row">
      <span class="label">复制规则</span>
      <n-select v-model:value="createFrom" size="small" :options="fromOptions" style="flex: 1" />
    </div>
    <div class="hint">从已有项目复制过滤/解密规则与域名组（不复制流量历史）；重名自动加序号。</div>
    <template #footer>
      <div class="footer-row">
        <n-button size="small" @click="showCreate = false">取消</n-button>
        <n-button size="small" type="primary" :loading="busy" :disabled="!createName.trim()" @click="confirmCreate">创建</n-button>
      </div>
    </template>
  </n-modal>

  <!-- 重命名弹窗 -->
  <n-modal v-model:show="showRename" preset="card" title="重命名项目" style="width: 420px">
    <div class="form-row">
      <span class="label">名称</span>
      <n-input v-model:value="renameName" placeholder="项目名称" :input-props="{ spellcheck: false }" @keyup.enter="confirmRename" />
    </div>
    <template #footer>
      <div class="footer-row">
        <n-button size="small" @click="showRename = false">取消</n-button>
        <n-button size="small" type="primary" :loading="busy" :disabled="!renameName.trim()" @click="confirmRename">保存</n-button>
      </div>
    </template>
  </n-modal>

  <!-- 删除弹窗（当前项目不可删，仅列出其他项目；M9 审计修复：原实现只针对当前项目发起删除必被后端拒绝） -->
  <n-modal v-model:show="showDelete" preset="card" title="删除项目" style="width: 420px">
    <div class="form-row">
      <span class="label">项目</span>
      <n-select v-model:value="deleteId" size="small" :options="deletableOptions" placeholder="选择要删除的项目" />
    </div>
    <div class="hint">当前项目不可删除；选中的项目目录（含流量历史数据库）将一并删除，且不可恢复。</div>
    <template #footer>
      <div class="footer-row">
        <n-button size="small" @click="showDelete = false">取消</n-button>
        <n-button size="small" type="error" :loading="busy" :disabled="!deleteId" @click="confirmDelete">删除</n-button>
      </div>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { NButton, NInput, NModal, NSelect, useDialog, useMessage } from 'naive-ui'
import { EventsOff, EventsOn } from '../../wailsjs/runtime/runtime'
import {
  CreateProject,
  DeleteProject,
  GetCurrentProject,
  ListProjects,
  RenameProject,
  SwitchProject,
} from '../../wailsjs/go/app/App'
import type { settings } from '../../wailsjs/go/models'

const message = useMessage()
const dialog = useDialog()

const projects = ref<settings.ProjectMeta[]>([])
const currentId = ref('')
const busy = ref(false)

const options = computed(() => projects.value.map((p) => ({ label: p.name, value: p.id })))
const current = computed(() => projects.value.find((p) => p.id === currentId.value))

async function refresh() {
  try {
    projects.value = (await ListProjects()) ?? []
    const cur = await GetCurrentProject()
    currentId.value = cur?.id ?? ''
  } catch {
    /* 启动早期失败静默，project:changed 事件会再触发 */
  }
}

// 切换项目（后端热切换：清空列表载入新项目历史，经 flow:evict/upsert 事件同步前端）
async function switchTo(id: string) {
  if (!id || id === currentId.value) return
  busy.value = true
  try {
    await SwitchProject(id)
    currentId.value = id
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
    // 失败回退选中态（currentId 未变，强制刷新以还原）
    currentId.value = ''
    await refresh()
  } finally {
    busy.value = false
  }
}

// ---- 新建 ----
const showCreate = ref(false)
const createName = ref('')
const createFrom = ref('')
const fromOptions = computed(() => [
  { label: '空白项目', value: '' },
  ...projects.value.map((p) => ({ label: `从「${p.name}」复制`, value: p.id })),
])

function openCreate() {
  createName.value = ''
  createFrom.value = ''
  showCreate.value = true
}

async function confirmCreate() {
  const name = createName.value.trim()
  if (!name) return
  busy.value = true
  try {
    const meta = await CreateProject(name, createFrom.value)
    showCreate.value = false
    message.success(`已创建并切换到项目「${meta?.name ?? name}」`, { duration: 4000 })
    await refresh()
  } catch (e) {
    // 后端存在"项目已创建但切换失败"路径：清单已含新项目，错误提示后仍需刷新
    message.error(String(e), { closable: true, duration: 6000 })
    await refresh()
  } finally {
    busy.value = false
  }
}

// ---- 重命名（当前项目） ----
const showRename = ref(false)
const renameName = ref('')

function openRename() {
  if (!current.value) return
  renameName.value = current.value.name
  showRename.value = true
}

async function confirmRename() {
  const name = renameName.value.trim()
  if (!name || !current.value) return
  busy.value = true
  try {
    await RenameProject(current.value.id, name)
    showRename.value = false
    await refresh()
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
  } finally {
    busy.value = false
  }
}

// ---- 删除（仅非当前项目；当前项目不可删） ----
const showDelete = ref(false)
const deleteId = ref('')

// 可删除项目 = 除当前项目外的全部；仅剩一个项目时为空（后端约束"至少保留一个"）
const deletableOptions = computed(() =>
  projects.value.filter((p) => p.id !== currentId.value).map((p) => ({ label: p.name, value: p.id })),
)

function askDelete() {
  if (projects.value.length <= 1) {
    message.warning('至少保留一个项目，无法删除', { duration: 4000 })
    return
  }
  deleteId.value = ''
  showDelete.value = true
}

async function confirmDelete() {
  const target = projects.value.find((p) => p.id === deleteId.value)
  if (!target) return
  dialog.warning({
    title: '删除项目',
    content: `删除项目「${target.name}」？其流量历史数据库将一并删除，且不可恢复。`,
    positiveText: '删除',
    negativeText: '取消',
    closable: false,
    maskClosable: false,
    onPositiveClick: async () => {
      try {
        await DeleteProject(target.id)
        message.success(`已删除项目「${target.name}」`, { duration: 4000 })
        showDelete.value = false
        await refresh()
      } catch (e) {
        message.error(String(e), { closable: true, duration: 6000 })
      }
    },
  })
}

// CLI/其他窗口切换项目后同步选中态与清单（改名也经此刷新）
function onProjectChanged() {
  refresh()
}

onMounted(() => {
  EventsOn('project:changed', onProjectChanged)
  refresh()
})
onUnmounted(() => EventsOff('project:changed'))
</script>

<style scoped>
.proj-select { width: 150px; }
.proj-actions { display: flex; gap: 2px; padding: 2px 4px; }
.form-row { display: flex; align-items: center; gap: 8px; margin-bottom: 10px; }
.label { width: 56px; flex: none; opacity: 0.7; font-size: 12px; }
.hint { opacity: 0.5; font-size: 11px; }
.footer-row { display: flex; gap: 8px; justify-content: flex-end; }
</style>
