<template>
  <div class="welcome">
    <!-- 左侧品牌栏（参考 IDEA 欢迎页） -->
    <aside class="welcome-side">
      <div class="brand">
        <div class="brand-logo">W</div>
        <div>
          <div class="brand-name">PrismProxy</div>
          <div class="brand-sub">抓包调试代理</div>
        </div>
      </div>
      <div class="side-actions">
        <button class="side-btn primary" :disabled="busy" @click="openCreate">
          <span class="side-btn-icon">+</span> 新建项目
        </button>
        <button class="side-btn" :disabled="busy || !projects.length" @click="openFirstProject">
          <span class="side-btn-icon">▶</span> 打开项目
        </button>
        <button class="side-btn" @click="emit('open-settings')">
          <span class="side-btn-icon">⚙</span> 设置
        </button>
      </div>
      <div class="side-foot">规则与流量历史按项目隔离</div>
    </aside>

    <!-- 右侧项目列表 -->
    <main class="welcome-main">
      <div class="list-head">
        <span class="list-title">项目</span>
        <span v-if="projects.length" class="list-tip">双击打开 · 右键重命名/删除</span>
      </div>

      <div v-if="!projects.length" class="empty">
        <div class="empty-title">还没有项目</div>
        <div class="empty-sub">新建第一个项目开始抓包；规则、域名组与流量历史都保存在项目内。</div>
        <n-button type="primary" :loading="busy" @click="openCreate">新建第一个项目</n-button>
        <free-notice class="empty-free" />
      </div>

      <ul v-else class="proj-list">
        <li
          v-for="p in projects"
          :key="p.id"
          class="proj-item"
          :class="{ active: p.id === currentId }"
          :title="`打开「${p.name}」`"
          @dblclick="switchTo(p.id)"
          @contextmenu.prevent="onContext($event, p)"
        >
          <span class="proj-name" @click="switchTo(p.id)">{{ p.name }}</span>
          <span class="proj-id">{{ p.id }}</span>
          <span class="proj-ops">
            <n-button size="tiny" quaternary title="打开" @click.stop="switchTo(p.id)">打开</n-button>
            <n-button size="tiny" quaternary title="重命名" @click.stop="openRename(p)">重命名</n-button>
            <n-button size="tiny" quaternary type="error" title="删除" @click.stop="askDelete(p)">删除</n-button>
          </span>
        </li>
      </ul>

      <free-notice v-if="projects.length" class="main-free" />
    </main>

    <!-- 新建项目弹窗 -->
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
          <n-button size="small" type="primary" :loading="busy" :disabled="!createName.trim()" @click="confirmCreate">创建并打开</n-button>
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
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { NButton, NInput, NModal, NSelect, useDialog, useMessage } from 'naive-ui'
import { useProjects } from '../composables/useProjects'
import FreeNotice from '../components/FreeNotice.vue'
import type { settings } from '../../wailsjs/go/models'

const emit = defineEmits<{ (e: 'open-settings'): void }>()

const message = useMessage()
const dialog = useDialog()

const {
  projects,
  currentId,
  busy,
  switchTo,
  createProject,
  renameProject,
  removeProject,
} = useProjects()

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
  try {
    const meta = await createProject(name, createFrom.value)
    showCreate.value = false
    if (meta) message.success(`已创建并打开项目「${meta.name ?? name}」`, { duration: 4000 })
  } catch (e) {
    message.error(String(e), { closable: true, duration: 6000 })
  }
}

// ---- 打开 ----
async function openFirstProject() {
  const first = projects.value.find((p) => p.id === currentId.value) ?? projects.value[0]
  if (first) await switchTo(first.id)
}

// ---- 重命名 ----
const showRename = ref(false)
const renameId = ref('')
const renameName = ref('')

function openRename(p: settings.ProjectMeta) {
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

// ---- 删除（任意项目；删当前/全部后后端自动打开剩余项目或停留在欢迎页） ----
function askDelete(p: settings.ProjectMeta) {
  dialog.warning({
    title: '删除项目',
    content: `删除项目「${p.name}」？其规则、域名组与流量历史数据库将一并删除，且不可恢复。`,
    positiveText: '删除',
    negativeText: '取消',
    closable: false,
    maskClosable: false,
    onPositiveClick: async () => {
      try {
        await removeProject(p.id)
        message.success(`已删除项目「${p.name}」`, { duration: 4000 })
      } catch (e) {
        message.error(String(e), { closable: true, duration: 6000 })
      }
    },
  })
}

</script>

<style scoped>
.welcome {
  display: flex;
  height: 100%;
  background: #1b1d23;
  color: #d8dce3;
  user-select: none;
}

/* 左侧品牌栏 */
.welcome-side {
  width: 260px;
  flex: none;
  display: flex;
  flex-direction: column;
  padding: 28px 20px;
  background: #15171c;
  border-right: 1px solid rgba(255, 255, 255, 0.06);
}
.brand { display: flex; align-items: center; gap: 12px; margin-bottom: 36px; }
.brand-logo {
  width: 40px;
  height: 40px;
  border-radius: 8px;
  background: #fff;
  color: #1b1d23;
  font-weight: 700;
  font-size: 22px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.brand-name { font-size: 18px; font-weight: 600; }
.brand-sub { font-size: 12px; opacity: 0.55; margin-top: 2px; }

.side-actions { display: flex; flex-direction: column; gap: 4px; }
.side-btn {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: #d8dce3;
  font-size: 13px;
  text-align: left;
  cursor: pointer;
  transition: background 0.12s;
}
.side-btn:hover:not(:disabled) { background: rgba(255, 255, 255, 0.07); }
.side-btn:disabled { opacity: 0.4; cursor: not-allowed; }
.side-btn.primary { color: #63e2b7; font-weight: 600; }
.side-btn-icon { width: 16px; display: inline-block; text-align: center; opacity: 0.8; }
.side-foot { margin-top: auto; font-size: 11px; opacity: 0.4; line-height: 1.6; }

/* 右侧列表 */
.welcome-main { flex: 1; padding: 28px 32px; overflow: auto; display: flex; flex-direction: column; }
.list-head { display: flex; align-items: baseline; gap: 12px; margin-bottom: 14px; }
.list-title { font-size: 15px; font-weight: 600; }
.list-tip { font-size: 11px; opacity: 0.4; }

.empty {
  margin-top: 12%;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  text-align: center;
}
.empty-title { font-size: 17px; font-weight: 600; }
.empty-sub { font-size: 12px; opacity: 0.55; margin-bottom: 10px; max-width: 360px; line-height: 1.7; }
.empty-free { margin-top: 36px; }
.main-free { margin-top: auto; padding-top: 48px; }

.proj-list { list-style: none; margin: 0; padding: 0; max-width: 640px; }
.proj-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  border-radius: 6px;
  cursor: pointer;
}
.proj-item:hover { background: rgba(255, 255, 255, 0.05); }
.proj-item.active { background: rgba(99, 226, 183, 0.08); }
.proj-name { font-size: 13px; }
.proj-item.active .proj-name { color: #63e2b7; }
.proj-id { font-size: 11px; opacity: 0.38; font-family: monospace; }
.proj-ops { margin-left: auto; display: flex; gap: 2px; opacity: 0; transition: opacity 0.12s; }
.proj-item:hover .proj-ops { opacity: 1; }

.form-row { display: flex; align-items: center; gap: 8px; margin-bottom: 10px; }
.label { width: 56px; flex: none; opacity: 0.7; font-size: 12px; }
.hint { opacity: 0.5; font-size: 11px; }
.footer-row { display: flex; gap: 8px; justify-content: flex-end; }
</style>
