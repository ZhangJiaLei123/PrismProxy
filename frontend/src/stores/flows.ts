import { defineStore } from 'pinia'
import { ListFlows, ClearFlows } from '../../wailsjs/go/app/App'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import type { app } from '../../wailsjs/go/models'

// 列表展示序：最新在顶部（新流 unshift）
export const useFlowsStore = defineStore('flows', {
  state: () => ({
    flows: [] as app.FlowMeta[],
    index: new Map<string, number>(), // ID → flows 下标
    selectedId: '',
    inited: false,
    // M6 调试重发 Composer：show=窗口开关，prefillId=从哪条流预填（空=空白请求）
    composerShow: false,
    composerPrefillId: '',
    // 展示过滤（方案验收 #8）：与 Go 侧捕获规则相互独立，仅影响列表显示
    // methods/statuses 为多选数组：空数组 = 全部；选中多项时命中任一即通过
    filter: { keyword: '', regex: false, methods: [] as string[], statuses: [] as string[] },
    // 暂停列表刷新：true 时丢弃 flow:upsert/flow:evict 事件（后端抓包继续，恢复后不补发）
    paused: false,
  }),
  getters: {
    selected(s): app.FlowMeta | null {
      const i = s.index.get(s.selectedId)
      return i === undefined ? null : s.flows[i]
    },
    filtered(s): app.FlowMeta[] {
      const f = s.filter
      const kw = f.keyword.trim().toLowerCase()
      let re: RegExp | null = null
      if (kw && f.regex) {
        try {
          re = new RegExp(f.keyword.trim(), 'i')
        } catch {
          re = null // 非法正则降级为子串匹配（UI 另有红色提示）
        }
      }
      // 过滤空哨兵（「全部」项值为 ''，正常交互下不会进入数组，这里兜底剔除）
      const methods = f.methods.filter(Boolean)
      const statuses = f.statuses.filter(Boolean)
      return s.flows.filter((m) => {
        // 多选方法：选中集合非空且当前方法不在集合内则排除
        if (methods.length && !methods.includes(m.Method)) return false
        if (statuses.length) {
          // 状态归类：error 流 → 'error'；正常流按百位 → 'Nxx'
          const bucket = m.State === 'error' ? 'error' : Math.floor(m.Status / 100) + 'xx'
          if (!statuses.includes(bucket)) return false
        }
        if (kw) {
          const hay = m.Host + ' ' + m.URL
          if (re ? !re.test(hay) : !hay.toLowerCase().includes(kw)) return false
        }
        return true
      })
    },
  },
  actions: {
    async init() {
      if (this.inited) return
      this.inited = true
      const all = (await ListFlows()) ?? []
      all.sort((a, b) => b.StartedAt - a.StartedAt)
      this.flows = all
      this.rebuildIndex()
      EventsOn('flow:upsert', (metas: app.FlowMeta[]) => this.upsert(metas ?? []))
      EventsOn('flow:evict', (ids: string[]) => this.evict(ids ?? []))
    },
    upsert(metas: app.FlowMeta[]) {
      if (this.paused) return
      const news: app.FlowMeta[] = []
      for (const m of metas) {
        const i = this.index.get(m.ID)
        if (i !== undefined) this.flows[i] = m
        else news.push(m)
      }
      if (news.length) {
        news.sort((a, b) => b.StartedAt - a.StartedAt)
        this.flows.unshift(...news)
        this.rebuildIndex()
      }
    },
    evict(ids: string[]) {
      if (this.paused) return
      const dead = new Set(ids)
      this.flows = this.flows.filter((f) => !dead.has(f.ID))
      this.rebuildIndex()
      if (dead.has(this.selectedId)) this.selectedId = ''
    },
    rebuildIndex() {
      this.index.clear()
      this.flows.forEach((f, i) => this.index.set(f.ID, i))
    },
    select(id: string) {
      this.selectedId = id
    },
    // M6：打开 Composer；flowId 非空时从该流预填请求（method/URL/headers/body）
    openComposer(flowId = '') {
      this.composerPrefillId = flowId
      this.composerShow = true
    },
    closeComposer() {
      this.composerShow = false
      this.composerPrefillId = ''
    },
    async clear() {
      await ClearFlows()
      // 暂停期间 evict 事件被丢弃，本地手动同步：仅保留置顶流（与后端 Clear 语义一致）
      this.flows = this.flows.filter((f) => f.Pinned)
      this.rebuildIndex()
      if (!this.index.has(this.selectedId)) this.selectedId = ''
    },
  },
})
