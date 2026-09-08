// useProjects：项目清单/当前项目的共享状态与动作（M11）。
// 模块级单例：ProjectSwitcher（顶栏）与 WelcomePage（欢迎页）共用同一份状态，
// project:changed 事件在模块级统一订阅刷新（不依赖任何组件挂载）。
// 契约：下方动作函数（switchTo/createProject/renameProject/removeProject/closeProject）
// 不内部 catch——调用方必须 try/catch 并向用户提示；finally 中 refresh 自愈状态，
// 故乐观赋值（如 switchTo 预置 currentId）失败后会被后端真值拉回。
import { computed, ref } from 'vue'
import {
  CloseProject,
  CreateProject,
  DeleteProject,
  GetCurrentProject,
  ListProjects,
  RenameProject,
  SwitchProject,
} from '../../wailsjs/go/app/App'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import type { settings } from '../../wailsjs/go/models'

const projects = ref<settings.ProjectMeta[]>([])
const currentId = ref('')
const busy = ref(false)

const current = computed(() => projects.value.find((p) => p.id === currentId.value) ?? null)
// 无打开项目 → 前端展示欢迎页（后端 nil proj：空引擎全放行）
const hasOpenProject = computed(() => !!currentId.value)

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
async function switchTo(id: string): Promise<void> {
  if (!id || id === currentId.value) return
  busy.value = true
  try {
    await SwitchProject(id)
    currentId.value = id
  } finally {
    busy.value = false
    await refresh()
  }
}

// 新建项目并自动切换；fromID 非空=从该项目复制规则与域名组
async function createProject(name: string, fromID = ''): Promise<settings.ProjectMeta | null> {
  const trimmed = name.trim()
  if (!trimmed) return null
  busy.value = true
  try {
    return await CreateProject(trimmed, fromID)
  } finally {
    busy.value = false
    await refresh()
  }
}

async function renameProject(id: string, name: string): Promise<void> {
  const trimmed = name.trim()
  if (!trimmed || !id) return
  busy.value = true
  try {
    await RenameProject(id, trimmed)
  } finally {
    busy.value = false
    await refresh()
  }
}

// 删除项目（可删当前项目/删完全部；删当前后后端自动打开首个剩余项目，全部删完进入欢迎页）
async function removeProject(id: string): Promise<void> {
  if (!id) return
  busy.value = true
  try {
    await DeleteProject(id)
  } finally {
    busy.value = false
    await refresh()
  }
}

// 关闭当前项目（不删除）：回到欢迎页，项目仍在清单可重新打开
async function closeProject(): Promise<void> {
  if (!currentId.value) return
  busy.value = true
  try {
    await CloseProject()
    currentId.value = ''
  } finally {
    busy.value = false
    await refresh()
  }
}

// 模块级事件订阅（只注册一次）：CLI/其他窗口切换、删除、关闭项目后同步清单。
// 不依赖任何组件挂载——欢迎页（无顶栏切换器）显示时事件仍能刷新共享状态。
let subscribed = false
function ensureSubscribed() {
  if (subscribed) return
  subscribed = true
  EventsOn('project:changed', () => {
    void refresh()
  })
  void refresh()
}

export function useProjects() {
  ensureSubscribed()
  return {
    projects,
    currentId,
    current,
    hasOpenProject,
    busy,
    refresh,
    switchTo,
    createProject,
    renameProject,
    removeProject,
    closeProject,
  }
}
