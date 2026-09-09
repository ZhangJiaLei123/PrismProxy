import { defineStore } from 'pinia'
import { TagFlows, ListTags } from '../../wailsjs/go/app/App'
import type { app } from '../../wailsjs/go/models'

// M12 标记弹窗的「本次运行」缓存 + 打标动作封装（设计 §5.2）。
// 严格要求：标签名/自动清空勾选态仅存 Pinia 内存（state 本就是内存，刷新/重启即复位），
// 不写 localStorage——重启后名称为空、复选框默认不勾。
export const useTagMarkStore = defineStore('tagMark', {
  state: () => ({
    name: '', // 上次输入的标签名（本次运行回填）
    autoClear: false, // 「标记后自动清空当前列表」勾选态（初值恒 false）
    submitting: false, // 打标请求进行中（loading + 防重入）
    history: [] as app.TagInfo[], // 历史标签（ListTags，打开弹窗时重拉，跨窗口闭环）
  }),
  actions: {
    // 打开弹窗时重拉历史标签（复盘页管理操作后主窗也能拿到最新，设计 §4.4 闭环）
    async refreshHistory() {
      try {
        const raw = (await ListTags()) ?? []
        // 关键陷阱：wails 绑定返回 JSON.parse 后的普通对象，不会用 models.ts 的 class 实例化；
        // Go TagInfo 带小写 json tag（id/name/count/...），运行时字段为小写，这里统一归一化为大写形态
        this.history = (raw as any[]).map((t) => ({
          ID: t.id ?? t.ID ?? '',
          Name: t.name ?? t.Name ?? '',
          Count: t.count ?? t.Count ?? 0,
          CreatedAt: t.createdAt ?? t.CreatedAt ?? 0,
          LastUsedAt: t.lastUsedAt ?? t.LastUsedAt ?? 0,
        }))
      } catch {
        this.history = []
      }
    },
    // 给当前过滤可见的全部流打标签。ids 由调用方（弹窗）按 store.filtered 传入；
    // autoClear 由后端原子执行（设计 §4.3 第 7 步），前端不再自行 clear。
    async mark(ids: string[], name: string, autoClear: boolean): Promise<app.TagResult> {
      if (this.submitting) throw new Error('正在标记中，请勿重复提交')
      this.submitting = true
      try {
        const res: any = await TagFlows(ids, name, autoClear)
        // 缓存本次输入（标签名保留便于连续打标；勾选态保留）
        this.name = name
        this.autoClear = autoClear
        // 同 refreshHistory：Go TagResult/TagInfo 运行时为小写字段，归一化后供弹窗按大写读取
        const tag = res?.tag ?? res?.Tag ?? {}
        return {
          Tagged: res?.tagged ?? res?.Tagged ?? 0,
          Archived: res?.archived ?? res?.Archived ?? 0,
          Skipped: res?.skipped ?? res?.Skipped ?? 0,
          Tag: {
            ID: tag.id ?? tag.ID ?? '',
            Name: tag.name ?? tag.Name ?? name,
            Count: tag.count ?? tag.Count ?? 0,
            CreatedAt: tag.createdAt ?? tag.CreatedAt ?? 0,
            LastUsedAt: tag.lastUsedAt ?? tag.LastUsedAt ?? 0,
          },
        } as unknown as app.TagResult
      } finally {
        this.submitting = false
      }
    },
  },
})
