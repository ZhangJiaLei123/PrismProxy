<template>
  <n-select
    ref="selectRef"
    class="proj-select"
    size="small"
    :value="currentId"
    :options="options"
    :loading="busy"
    :menu-props="{ style: 'width: 260px' }"
    placeholder="项目"
    @update:value="switchTo"
  >
    <!-- 每个项目行内右侧操作：重命名/删除（hover 显示，与 WelcomePage 一致） -->
    <template #option="{ option }">
      <div class="proj-option">
        <span class="po-name">{{ option.label }}</span>
        <span class="po-ops">
          <n-button size="tiny" quaternary @click.stop="openRename(option.p)">重命名</n-button>
          <n-button size="tiny" quaternary type="error" @click.stop="askDelete(option.p)">删除</n-button>
        </span>
      </div>
    </template>
    <template #action>
      <div class="proj-actions">
        <n-button size="tiny" quaternary @click="openCreate">新建</n-button>
        <n-button size="tiny" quaternary :disabled="!currentId" title="关闭项目回到欢迎页（项目不删除）" @click="confirmClose">关闭</n-button>
      </div>
    </template>
  </n-select>

  <!-- 新建项目弹窗（设计 §7.1：n-modal preset card + n-input，useDialog 不支持内嵌输入框） -->
  <n-modal v-model:show="showCreate" preset="card" title="新建" style="width: 420px">
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

  <!-- 重命名弹窗（当前项目） -->
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

</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NButton, NInput, NModal, NSelect, useDialog, useMessage } from 'naive-ui'
import type { SelectInst } from 'naive-ui'
import { useProjects } from '../composables/useProjects'
import type { settings } from '../../wailsjs/go/models'

const message = useMessage()
const dialog = useDialog()

const {
  projects,
  currentId,
  current,
  busy,
  refresh,
  switchTo,
  createProject,
  renameProject,
  removeProject,
  closeProject,
} = useProjects()

const selectRef = ref<SelectInst | null>(null)

// option 额外携带 p（项目元数据），供行内重命名/删除按钮使用
const options = computed(() => projects.value.map((p) => ({ label: p.name, value: p.id, p })))

// ---- 新建 ----
const showCreate = ref(false)
const createName = ref('')
const createFrom = ref('')
const fromOptions = computed(() => [
  { label: '空白项目', value: '' },
  ...projects.value.map((p) => ({ label: `从「${p.name}」复制`, value: p.id })),
])

function openCreate() {
  selectRef.value?.blur()
  createName.value = ''
  createFrom.value = ''
  showCreate.value = true
}

async function confirmCreate() {
  const name = createName.value.trim()
  if (!name) return
  try {
    const meta = await createProject(name, createFrom.value)
    showCreate.value = false
    if (meta) message.success(`已创建并打开项目「${meta.name ?? name}」`, { duration: 4000 })
  } catch (e) {
    // 后端存在"项目已创建但切换失败"路径：清单已含新项目，错误提示后仍需刷新
    message.error(String(e), { closable: true, duration: 6000 })
  }
}

// ---- 重命名（任意项目，行内触发） ----
const showRename = ref(false)
const renameId = ref('')
const renameName = ref('')

function openRename(p: settings.ProjectMeta) {
  selectRef.value?.blur()
  renameId.value = p.id
  renameName.value = p.name
  showRename.value = true
}

async function confirmRename() {
  const name = renameName.value.trim()
  if (!name || !renameId.value) return
  try {
    await renameProject(renameId.value, name)
    showRename.value = false
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
  }
}

// ---- 删除（任意项目，行内触发；M11 放开"当前项目不可删/至少保留一个"约束） ----
function askDelete(target: settings.ProjectMeta) {
  selectRef.value?.blur()
  const isCurrent = target.id === currentId.value
  dialog.warning({
    title: '删除项目',
    content: isCurrent
      ? `删除当前项目「${target.name}」？其规则、域名组与流量历史数据库将一并删除，且不可恢复。删除后自动打开其余项目；若无剩余项目则回到欢迎页。`
      : `删除项目「${target.name}」？其规则、域名组与流量历史数据库将一并删除，且不可恢复。`,
    positiveText: '删除',
    negativeText: '取消',
    closable: false,
    maskClosable: false,
    onPositiveClick: async () => {
      try {
        await removeProject(target.id)
        message.success(`已删除项目「${target.name}」`, { duration: 4000 })
      } catch (e) {
        message.error(String(e), { closable: true, duration: 6000 })
      }
    },
  })
}

// ---- 关闭当前项目（回到欢迎页，不删除） ----
function confirmClose() {
  if (!current.value) return
  selectRef.value?.blur()
  dialog.info({
    title: '关闭项目',
    content: `关闭项目「${current.value.name}」回到欢迎页？项目不会被删除，可随时重新打开。关闭期间代理无项目规则（全放行）。`,
    positiveText: '关闭项目',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await closeProject()
      } catch (e) {
        message.error(String(e), { closable: true, duration: 6000 })
      }
    },
  })
}

// project:changed 事件订阅与首次刷新由 useProjects 模块统一处理
// （欢迎页不挂载本组件，事件订阅不能依赖组件生命周期）。
onMounted(() => {
  refresh()
})
</script>

<style scoped>
.proj-select { width: 150px; }

/* 下拉选项：项目名 + 行内重命名/删除（hover 行时显示，避开右侧选中对勾） */
.proj-option {
  display: flex;
  align-items: center;
  width: 100%;
  padding-right: 24px;
}
.po-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.po-ops {
  margin-left: auto;
  display: flex;
  gap: 2px;
  opacity: 0;
  transition: opacity 0.12s;
}
.n-base-select-option--pending .po-ops,
.proj-option:hover .po-ops { opacity: 1; }

/* 底部仅「新建/关闭」两个按钮，横向排列不溢出；分割线由 naive-ui 的
   .n-base-select-menu__action 容器自带（border-top: 1px solid actionDividerColor），
   此处不要再画 border-top，否则双分割线。 */
.proj-actions {
  display: flex;
  gap: 4px;
}
.form-row { display: flex; align-items: center; gap: 8px; margin-bottom: 10px; }
.label { width: 56px; flex: none; opacity: 0.7; font-size: 12px; }
.hint { opacity: 0.5; font-size: 11px; }
.footer-row { display: flex; gap: 8px; justify-content: flex-end; }
</style>
