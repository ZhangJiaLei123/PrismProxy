<template>
  <!-- AI 分析面板（设计 §7）：自绘右侧滑入抽屉（非 n-drawer、无遮罩，可边看列表边读）；
       absolute 定位于 .review-root 内——主窗内嵌时只覆盖复盘页区域，独立窗口等效 fixed；
       宽度可拖拽（左缘把手）并经 localStorage 缓存 -->
  <aside
    ref="drawerEl"
    class="ai-drawer"
    :class="{ open: show, 'w-dragging': wDragging }"
    :style="aiWStyle"
  >
    <!-- 左缘宽度拖拽把手：左右拖动调整面板宽度，双击恢复默认 -->
    <div class="ai-grip-x" title="拖拽调整宽度 · 双击恢复默认" @pointerdown="startWDrag" @dblclick="resetW"></div>
    <header class="ai-head">
      <span class="ai-title">
        <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor">
          <path d="M8 1l1.9 4.6 4.6 1.9-4.6 1.9L8 14 6.1 9.4 1.5 7.5l4.6-1.9L8 1zm4.5 9.5l.8 1.9 1.9.8-1.9.8-.8 1.9-.8-1.9-1.9-.8 1.9-.8.8-1.9z" />
        </svg>
        AI 分析
      </span>
      <!-- 用 n-tab（纯导航 tab）而非自闭合 n-tab-pane：naive-ui 2.40.4 的 normalizeSlots
           会把自闭合 pane 的空 children 包装成"渲染注释"的 truthy slot，短路 props.tab，
           导致标签文字永不渲染（tab 被压成 8px 高的"进度条"） -->
      <n-tabs v-model:value="activeTab" type="segment" size="small" class="ai-tabs">
        <n-tab v-for="t in TABS" :key="t.key" :name="t.key">{{ t.label }}</n-tab>
      </n-tabs>
      <button class="ai-close" title="关闭（Esc）" @click="closePanel">
        <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor">
          <path d="M8 6.59L12.95 1.64l1.41 1.41L9.41 8l4.95 4.95-1.41 1.41L8 9.41l-4.95 4.95-1.41-1.41L6.59 8 1.64 3.05l1.41-1.41L8 6.59z" />
        </svg>
      </button>
    </header>

    <div class="ai-body">
      <!-- 空态：未启用 / 未配 Key / 配置读取失败（设计 §九 空态引导） -->
      <div v-if="showEmpty || cfgFailed" class="ai-empty">
        <div class="ai-empty-ic">⚙️</div>
        <div class="ai-empty-title">{{ emptyTitle }}</div>
        <div class="ai-empty-sub">在主窗「设置 → AI 分析」中启用并填写 API Key 后即可使用</div>
        <n-button v-if="api.mode === 'http' && cfg" size="small" type="primary" secondary @click="goSettings">去主窗设置</n-button>
        <div v-if="settingMsg" class="ai-empty-msg">{{ settingMsg }}</div>
      </div>

      <div v-else-if="cfgLoading" class="ai-hint ai-pad">正在读取 AI 配置…</div>

      <template v-else>
        <!-- 范围说明：模式差异文案（勾选集 / 当前视图 / 单流目标） -->
        <div class="ai-scope">{{ scopeText }}</div>

        <!-- 模式专属输入：locate/flowmap 自然语言目标；explain/intent 无输入 -->
        <n-input
          v-if="activeTab === 'locate' || activeTab === 'flowmap'"
          v-model:value="question"
          type="textarea"
          :rows="2"
          :placeholder="qPlaceholder"
          :disabled="phase === 'streaming'"
        />

        <!-- 正文开关（对齐后端 OR 语义：explain/flowmap 默认必带正文、关不掉，故不给开关） -->
        <div v-if="activeTab === 'intent' || activeTab === 'locate'" class="ai-opts">
          <n-checkbox v-model:checked="includeReq" :disabled="phase === 'streaming'">包含请求正文</n-checkbox>
          <n-checkbox v-if="activeTab === 'locate'" v-model:checked="includeResp" :disabled="phase === 'streaming'">包含响应正文</n-checkbox>
          <!-- 单条轮询：逐条独立调用模型，规避整批 prompt 过大导致的截断/首响应超时（批量标注实测红线） -->
          <n-checkbox
            v-if="activeTab === 'intent'"
            v-model:checked="singleMode"
            :disabled="phase === 'streaming'"
            title="每条流单独调用一次模型，慢但稳，可规避批量 prompt 过大导致的输出截断或超时"
          >单条轮询处理</n-checkbox>
        </div>

        <!-- 动作行：开始（redact 关闭时每次需确认）/停止互斥 + 运行状态 -->
        <div class="ai-actions">
          <n-popconfirm
            v-if="cfg && !cfg.redact"
            placement="top-start"
            positive-text="继续"
            negative-text="取消"
            @positive-click="requestStart()"
          >
            <template #trigger>
              <n-button type="primary" size="small" :disabled="startDisabled">{{ startLabel }}</n-button>
            </template>
            当前未开启脱敏，请求头将原样发送给 AI 服务，确定继续？
          </n-popconfirm>
          <n-button v-else type="primary" size="small" :disabled="startDisabled" @click="requestStart()">{{ startLabel }}</n-button>
          <n-button v-if="phase === 'streaming'" size="small" quaternary @click="stop">停止</n-button>
          <!-- 分析中状态 pill：弹跳点 + 流动渐变文字（静态线索=紫色底与点色，动画非唯一反馈） -->
          <span v-if="phase === 'streaming'" class="ai-live">
            <i></i><i></i><i></i><span>AI 分析中</span>
          </span>
          <!-- 读屏播报（R1 审计修复）：live region 必须先于消息常驻无障碍树才会被播报；sr-only 视觉隐藏不影响布局 -->
          <span class="ai-sr-live" aria-live="polite">{{ phase === 'streaming' ? 'AI 分析中' : '' }}</span>
          <span v-if="metaText" class="ai-meta">{{ metaText }}</span>
          <span v-else-if="phase === 'done'" class="ai-state">已完成</span>
          <span v-else-if="phase === 'stopped'" class="ai-state">已停止</span>
        </div>

        <!-- intent 进度（n/total，tabular-nums 防数字抖动） -->
        <n-progress
          v-if="activeTab === 'intent' && phase !== 'idle'"
          type="line"
          :percentage="intentPct"
          :show-indicator="false"
          class="ai-progress"
        />

        <div v-if="phase === 'error'" class="ai-error">{{ errMsg }}</div>

        <!-- intent 结果列表：设计 §7.2 无 Markdown 区（delta 里的列表/JSON 不展示，仅收 intent 帧） -->
        <div v-if="activeTab === 'intent'" class="ai-intents">
          <div
            v-for="it in intentResults"
            :key="it.flowId"
            class="ai-intent-row"
            :title="intentRowTip(it)"
            @click="emit('locate', it.flowId)"
          >
            <span class="ai-intent-seq">#{{ it.seq }}</span>
            <span class="ai-intent-text" :class="{ 'k-ai-low': it.confidence === 'low', 'k-ai-needs-body': it.needsBody }">{{ it.intent }}</span>
            <span class="ai-intent-flow">{{ flowLabelOf(it.flowId) }}</span>
          </div>
          <div v-if="!intentResults.length && phase !== 'idle'" class="ai-hint">{{ phase === 'streaming' ? '标注中…' : '暂无结果' }}</div>
        </div>

        <!-- Markdown 结论区（explain/locate/flowmap）：50ms 节流渲染，v-html 唯一出口经 renderMarkdown 净化 -->
        <div v-else class="ai-md">
          <!-- 首 token 前的等待骨架：微光横条示意"正在思考" -->
          <div v-if="pendingStream" class="ai-skel" aria-hidden="true">
            <i></i><i></i><i></i><i></i><i></i>
          </div>
          <div v-else v-html="renderedHtml"></div>
        </div>

        <!-- locate 匹配卡片：rank/method/url/置信度/理由/[查看→] -->
        <div v-if="activeTab === 'locate' && matches.length" class="ai-matches">
          <div v-for="m in matches" :key="m.flowId" class="ai-match">
            <div class="ai-match-head">
              <span class="ai-match-rank">#{{ m.rank }}</span>
              <span class="ai-match-method">{{ m.method }}</span>
              <span class="ai-match-url" :title="m.url">{{ m.url }}</span>
              <span class="ai-match-conf" :class="confCls(m.confidence)">{{ confLabel(m.confidence) }}</span>
            </div>
            <div class="ai-match-reason">
              <span class="ai-match-reason-text" :title="m.reason">{{ m.reason }}</span>
              <n-button size="tiny" quaternary class="ai-match-go" @click="emit('locate', m.flowId)">查看 →</n-button>
            </div>
          </div>
        </div>
      </template>
    </div>

    <!-- 底部：外发告知常驻小字（设计 §8-5）+ 右下角日志入口 -->
    <footer class="ai-foot">
      <span v-if="cfgOk" class="ai-foot-text">
        分析数据将发送至 {{ hostLabel }}<template v-if="modelLabel"> · 模型 {{ modelLabel }}</template>
      </span>
      <button class="ai-logbtn" title="AI 调用日志与对话" @click="toggleLog">
        <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M2.5 4l3.5 3.5L2.5 11" />
          <path d="M8.5 11.5H13" />
        </svg>
        <i v-if="phase === 'streaming'" class="ai-logbtn-dot" aria-hidden="true"></i>
      </button>
    </footer>

    <!-- 调用日志抽层：对话内容（模型原始输出实时流）+ 调用日志（帧级事件），自底部滑入；
         高度可拖拽调整（顶部把手）并经 localStorage 缓存 -->
    <section
      ref="logsheetEl"
      class="ai-logsheet"
      :class="{ open: logOpen, dragging: logDragging }"
      :style="logHStyle"
      aria-label="AI 调用详情"
    >
      <!-- 顶部拖拽把手：上下拖动调整高度，双击恢复默认 -->
      <div class="ai-log-grip" title="拖拽调整高度 · 双击恢复默认" @pointerdown="startLogDrag" @dblclick="resetLogH"></div>
      <header class="ai-log-head">
        <n-tabs v-model:value="logTab" type="segment" size="small" class="ai-log-tabs">
          <n-tab name="conv">对话内容</n-tab>
          <n-tab name="log">调用日志</n-tab>
        </n-tabs>
        <button class="ai-close" title="收起日志" @click="logOpen = false">
          <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor">
            <path d="M8 6.59L12.95 1.64l1.41 1.41L9.41 8l4.95 4.95-1.41 1.41L8 9.41l-4.95 4.95-1.41-1.41L6.59 8 1.64 3.05l1.41-1.41L8 6.59z" />
          </svg>
        </button>
      </header>
      <div ref="logBodyEl" class="ai-log-body">
        <!-- 对话 tab：提问 → 对话轮次（每条流一对「消息发送→模型返回」气泡；单条轮询逐条成对） -->
        <template v-if="logTab === 'conv'">
          <div v-if="convEmpty" class="ai-hint ai-log-empty">
            {{ phase === 'streaming' ? '等待模型输出…' : '暂无对话内容 · 发起一次分析后这里实时展示模型原始输出' }}
          </div>
          <template v-else>
            <section class="ai-conv-sec">
              <div class="ai-conv-label">提问</div>
              <div class="ai-conv-q">{{ qAsked }}</div>
              <div v-if="hasQuestion" class="ai-conv-scope">{{ scopeText }}</div>
            </section>
            <!-- 轮次仅追加不重排（resetRun 整体清空），index 作 key 安全 -->
            <div v-for="(t, ti) in convTurns" :key="ti" class="ai-turn">
              <!-- 消息发送（右）：head 显示字数 + token（done 帧真实 usage 精确，缺失估算带 ≈） -->
              <div class="ai-bubble ai-bubble-send">
                <div class="ai-bubble-head">
                  <span class="ai-bubble-role">消息发送</span>
                  <span class="ai-conv-count">{{ t.system.length + t.user.length }} 字 · {{ tokText(t.usage?.promptTokens, t.system + t.user) }}</span>
                </div>
                <button v-if="t.system" class="ai-conv-toggle" :class="{ open: t.sysOpen }" @click="t.sysOpen = !t.sysOpen">
                  <svg class="chev" viewBox="0 0 16 16" width="10" height="10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M6 4.5l4 4-4 4" />
                  </svg>
                  <span>System 提示词</span>
                  <span class="ai-conv-count">{{ t.system.length }} 字</span>
                </button>
                <div class="ai-conv-fold" :class="{ open: t.sysOpen }">
                  <pre class="ai-log-conv">{{ t.system }}</pre>
                </div>
                <button v-if="t.user" class="ai-conv-toggle" :class="{ open: t.usrOpen }" @click="t.usrOpen = !t.usrOpen">
                  <svg class="chev" viewBox="0 0 16 16" width="10" height="10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M6 4.5l4 4-4 4" />
                  </svg>
                  <span>User 提示词</span>
                  <span class="ai-conv-count">{{ t.user.length }} 字</span>
                </button>
                <div class="ai-conv-fold" :class="{ open: t.usrOpen }">
                  <pre class="ai-log-conv">{{ t.user }}</pre>
                </div>
              </div>
              <!-- 模型返回（左）：思考过程（自动开合）+ 正文输出 -->
              <div class="ai-bubble ai-bubble-recv">
                <div class="ai-bubble-head">
                  <span class="ai-bubble-role">模型返回</span>
                  <span class="ai-conv-count">{{ t.reason.length + t.text.length }} 字 · {{ tokText(t.usage?.completionTokens, t.reason + t.text) }}</span>
                </div>
                <template v-if="t.reason">
                  <button
                    class="ai-conv-toggle"
                    :class="{ open: t.open, live: ti === convTurns.length - 1 && phase === 'streaming' && t.open }"
                    @click="toggleTurn(t)"
                  >
                    <svg class="chev" viewBox="0 0 16 16" width="10" height="10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                      <path d="M6 4.5l4 4-4 4" />
                    </svg>
                    <span>思考过程</span>
                    <span class="ai-conv-count">{{ t.reason.length }} 字</span>
                  </button>
                  <div class="ai-conv-fold" :class="{ open: t.open }">
                    <pre class="ai-log-conv ai-conv-think">{{ t.reason }}</pre>
                  </div>
                </template>
                <pre v-if="t.text" class="ai-log-conv">{{ t.text }}</pre>
                <div v-else-if="ti === convTurns.length - 1 && phase === 'streaming'" class="ai-hint">模型输出中…</div>
                <div v-else-if="!t.reason" class="ai-hint">（无输出）</div>
                <div v-if="t.finishReason === 'length'" class="ai-hint ai-conv-trunc">输出被截断（模型上下文/输出预算不足）</div>
              </div>
            </div>
          </template>
        </template>
        <template v-else>
          <div v-if="!logs.length && !prevLogs.length" class="ai-hint ai-log-empty">暂无日志 · 发起分析后逐帧记录调用过程</div>
          <!-- 上一轮归档（G3 审计建议）：默认收起，展开弱化展示最近一轮帧序列 -->
          <button v-if="prevLogs.length" class="ai-log-prev" @click="prevOpen = !prevOpen">
            <svg viewBox="0 0 16 16" width="10" height="10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <path v-if="prevOpen" d="M4 6.5l4 4 4-4" />
              <path v-else d="M6 4.5l4 4-4 4" />
            </svg>
            <span>上一轮 · {{ prevLogs.length }} 条</span>
          </button>
          <template v-if="prevOpen">
            <div v-for="l in prevLogs" :key="l.id" class="ai-log-line is-prev">
              <span class="ai-log-t">{{ fmtT(l.t) }}</span>
              <span class="ai-log-kind" :class="'lk-' + l.kind">{{ LOG_KIND_LABEL[l.kind] }}</span>
              <span class="ai-log-msg" :title="l.msg">{{ l.msg }}</span>
            </div>
          </template>
          <div v-for="l in logs" :key="l.id" class="ai-log-line">
            <span class="ai-log-t">{{ fmtT(l.t) }}</span>
            <span class="ai-log-kind" :class="'lk-' + l.kind">{{ LOG_KIND_LABEL[l.kind] }}</span>
            <span class="ai-log-msg" :title="l.msg">{{ l.msg }}</span>
          </div>
        </template>
      </div>
    </section>

    <!-- 首次分析告知（设计 §8-1）：localStorage 记忆，确认后才真正开始 -->
    <n-modal
      v-model:show="noticeShow"
      preset="dialog"
      type="warning"
      title="AI 分析数据外发告知"
      positive-text="知道了，开始分析"
      negative-text="取消"
      @positive-click="onNoticeOk"
    >
      <div class="ai-notice">
        <p>分析时将把所选流量的方法、URL、状态码、请求/响应头等数据发送到你配置的 AI 服务（{{ hostLabel }}）。</p>
        <p>如勾选「包含正文」，请求/响应正文也会一并送审。</p>
        <p>当前脱敏设置：<b :class="cfg?.redact ? '' : 'k-ai-low'">{{ cfg?.redact ? '已开启（自动隐藏常见敏感凭据）' : '未开启（请求头原样发送）' }}</b></p>
        <p>分析结果由模型生成，仅供参考，请勿盲信。</p>
      </div>
    </n-modal>
  </aside>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { NButton, NCheckbox, NInput, NModal, NPopconfirm, NProgress, NTab, NTabs } from 'naive-ui'
import type { AiChatEvent, AiChatMeta, AiChatMode, AiUsage, AIApiConfigView, ReviewApi } from '../api'
import type { AiMatchItem, IntentResult, ReviewFlowMeta } from '../../lib/types'
import { renderMarkdown } from '../ai-md'
import { useIntents } from '../useIntents'

// 面板状态机（设计 §7.3）：idle → streaming(meta→delta/intent/match*) → done|stopped|error；
// 切模式/换流重置，同面板同时只跑一个任务
type Phase = 'idle' | 'streaming' | 'done' | 'stopped' | 'error'

const props = defineProps<{
  api: ReviewApi
  show: boolean
  mode: AiChatMode
  /** explain 模式目标流（唯一必填输入） */
  flowId?: string
  /** explain 目标展示名（method + path） */
  flowLabel?: string
  checkedIds: string[]
  /** 当前视图已加载的流（intent/locate/flowmap 候选池，面板按 StartedAt 降序取 id） */
  viewFlows: ReviewFlowMeta[]
  viewTotal: number
  /** 范围说明文案用：当前标签名 / 关键词 */
  tagName: string
  scope: string
  keyword: string
}>()

const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
  (e: 'locate', flowId: string): void
  (e: 'error', msg: string): void
}>()

const TABS: { key: AiChatMode; label: string }[] = [
  { key: 'explain', label: '解读' },
  { key: 'intent', label: '意图' },
  { key: 'locate', label: '定位' },
  { key: 'flowmap', label: '流程' },
]
const NOTICE_KEY = 'prismproxy:review-ai-notice-v1'

const { intentOf, runIntent, upsert, busy } = useIntents()

const activeTab = ref<AiChatMode>(props.mode)
const phase = ref<Phase>('idle')
const errMsg = ref('')
const meta = ref<AiChatMeta | null>(null)
const matches = ref<AiMatchItem[]>([])
const intentDone = ref(0)
// 对话 tab 聊天模型：每条流一对「消息发送 → 模型返回」气泡。单条轮询每条独立成对
// （每次 meta 开新轮），批量/explain 等单次调用=单轮。curTurn 为 in-flight 轮的
// reactive 代理：推入数组后代理身份不变，50ms 合帧直接写代理属性即触发更新
interface ConvTurn {
  system: string // meta 帧 system（实际送审 System 提示词）
  user: string // meta 帧 user（实际送审 User 提示词）
  reason: string // 推理增量（delta 帧 reason 字段；普通模型/demo 无此流）
  text: string // 正文增量
  open: boolean // 思考过程折叠开合
  sysOpen: boolean // System 提示词折叠开合
  usrOpen: boolean // User 提示词折叠开合
  touched: boolean // 思考区被用户手动开合过：本轮流式自动开合逻辑退出
  finishReason: string // done 帧 finishReason（length=输出截断）
  usage?: AiUsage // done 帧真实 token 统计（服务商不支持时缺省，前端按字数估算兜底）
}
const convTurns = ref<ConvTurn[]>([])
let curTurn: ConvTurn | null = null
const question = ref('')
const includeReq = ref(false)
const includeResp = ref(false)
// intent 单条轮询：逐条独立调用模型（勾选后批量红线问题不再出现，代价是总耗时变长）
const singleMode = ref(false)
// 单条轮询整轮候选数（循环开始时快照）：进度分母不随运行中勾选变化漂移
const singleTotal = ref(0)
const cfg = ref<AIApiConfigView | null>(null)
const cfgLoading = ref(false)
const noticeShow = ref(false)
const settingMsg = ref('')

// 非响应式：delta 原始缓冲与渲染节流句柄（50ms 合帧，避免逐 token 重排）
let mdBuf = ''
let reasonBuf = '' // 推理模型思考增量缓冲（delta 帧 reason 字段）
let renderTimer: number | null = null
let abortCtl: AbortController | null = null
// 运行令牌（M1 审计修复）：重开新 run 后，旧 run 续体/迟到帧不得触碰新 run 状态
let runSeq = 0

// ===== 配置与空态 =====
const cfgOk = computed(() => !!cfg.value && cfg.value.enabled && cfg.value.hasApiKey)
const showEmpty = computed(() => !!cfg.value && (!cfg.value.enabled || !cfg.value.hasApiKey))
const cfgFailed = computed(() => !cfg.value && !cfgLoading.value)
const emptyTitle = computed(() => {
  if (!cfg.value) return 'AI 配置读取失败'
  return !cfg.value.enabled ? 'AI 分析未启用' : '未配置 API Key'
})
const hostLabel = computed(() => {
  const u = cfg.value?.baseUrl ?? ''
  try {
    return new URL(u).host
  } catch {
    return u || '未配置服务地址'
  }
})
// 当前模型显示名：取「当前」条目的别名，否则模型原名（模型未配置时隐藏该段）
const modelLabel = computed(() => {
  const c = cfg.value
  if (!c) return ''
  const cur = (c.entries ?? []).find((m) => m.current)
  return cur?.alias?.trim() || cur?.model || c.model
})

async function loadCfg(): Promise<void> {
  if (cfgLoading.value) return
  cfgLoading.value = true
  try {
    cfg.value = await props.api.getAIConfig()
  } catch (e) {
    cfg.value = null
    settingMsg.value = ''
  } finally {
    cfgLoading.value = false
  }
  // explain 自动开始（设计 §7.1）：面板打开且 cfg 就绪后触发；redact 关闭时 requestStart(auto) 会放弃
  if (props.show && activeTab.value === 'explain' && props.flowId && cfgOk.value) requestStart(true)
}

// 空态引导（设计 §九）：http 真唤起主窗 / demo、wails 静态提示文案
async function goSettings(): Promise<void> {
  try {
    const r = await props.api.openSettingsAI()
    settingMsg.value = r.msg
  } catch (e) {
    settingMsg.value = String((e as Error)?.message ?? e)
  }
}
// 非 http 形态的空态提示文案预取（demo/wails 无副作用）
watch(showEmpty, (v) => {
  if (v && props.api.mode !== 'http') void goSettings()
})

// ===== 候选 ids =====
const checkedSet = computed(() => new Set(props.checkedIds))
// 候选池按 StartedAt 降序（最新优先；后端仍会降序复查截断）
const byStartedDesc = computed(() => [...props.viewFlows].sort((a, b) => b.StartedAt - a.StartedAt).map((f) => f.ID))
const intentIds = computed(() => (props.checkedIds.length ? byStartedDesc.value.filter((id) => checkedSet.value.has(id)) : byStartedDesc.value))

const flowMap = computed(() => new Map(props.viewFlows.map((f) => [f.ID, f])))
function flowLabelOf(id: string): string {
  const f = flowMap.value.get(id)
  return f ? f.Method + ' ' + (f.Path || f.URL) : id
}

// intent 结果行 = 候选池里有结果的，按模型输出序号排
const intentResults = computed<IntentResult[]>(() =>
  intentIds.value
    .map((id) => intentOf(id))
    .filter((it): it is IntentResult => !!it)
    .sort((a, b) => a.seq - b.seq),
)

// ===== 文案 =====
const scopeText = computed(() => {
  if (activeTab.value === 'explain') return `解读目标：${props.flowLabel || props.flowId || '—'}`
  if (activeTab.value === 'intent') {
    const verb = singleMode.value ? '逐条分析' : '批量分析'
    return props.checkedIds.length
      ? `将${verb}已勾选的 ${props.checkedIds.length} 条流`
      : `未勾选，将${verb}当前视图 ${byStartedDesc.value.length} 条流（共 ${props.viewTotal} 条）`
  }
  const kw = props.keyword ? `，关键词「${props.keyword}」` : ''
  return `将分析当前视图 ${byStartedDesc.value.length} 条流（${props.tagName}${kw}）`
})
const qPlaceholder = computed(() =>
  activeTab.value === 'locate' ? '例：找出登录接口的响应位置' : '例：梳理下单流程的接口调用顺序',
)
const startLabel = computed(() =>
  activeTab.value === 'intent' ? '开始标注' : activeTab.value === 'explain' ? '开始解读' : '开始分析',
)
const startDisabled = computed(() => {
  if (phase.value === 'streaming' || !cfgOk.value) return true
  if (activeTab.value === 'explain') return !props.flowId
  if (activeTab.value === 'intent') return busy.value || !intentIds.value.length
  return !question.value.trim()
})
const metaText = computed(() => {
  // 单条轮询：meta 逐条变化（total 恒为 1），改为展示整轮进度（成功数；分母用循环快照）
  if (activeTab.value === 'intent' && singleMode.value && phase.value !== 'idle') {
    return `单条轮询 ${intentDone.value}/${singleTotal.value || intentIds.value.length} 条`
  }
  const m = meta.value
  if (!m) return ''
  let s = `送审 ${m.sent}/${m.total} 条 · 预算 ${m.budget.flows} 流 / ${m.budget.kb}KB`
  if (m.truncated) s += ' · 候选超限已截断'
  return s
})
const intentPct = computed(() => {
  // 单条轮询：每次调用 meta.total=1，分母改用循环开始时的快照数
  const t = activeTab.value === 'intent' && singleMode.value ? singleTotal.value || intentIds.value.length : (meta.value?.total ?? intentIds.value.length)
  return t ? Math.round((intentDone.value / t) * 100) : 0
})

// locate 显示缓冲：截掉模型尾部 ```json 块（流式中半截块也不闪现）；
// 大小写不敏感（模型可能输出 ```JSON，与 Go 侧 lastJSONBlock 同语义，L3 审计修复）
const displayMd = computed(() => {
  if (activeTab.value !== 'locate') return fullText.value
  const fences = [...fullText.value.matchAll(/```json/gi)]
  const last = fences[fences.length - 1]
  return last?.index != null ? fullText.value.slice(0, last.index) : fullText.value
})
const renderedHtml = computed(() => renderMarkdown(displayMd.value))
// 首 token 前的骨架占位：流式进行中且尚无任何输出
const pendingStream = computed(() => phase.value === 'streaming' && !displayMd.value)

// ===== 调用日志（右下角抽层）=====
// 每次运行独立记录：帧级事件（meta/intent/match/error…）+ 模型原始输出（对话 tab 实时展示）
type LogKind = 'start' | 'meta' | 'delta' | 'intent' | 'match' | 'error' | 'done' | 'stop'
interface LogEntry {
  id: number
  t: number
  kind: LogKind
  msg: string
}
const LOG_KIND_LABEL: Record<LogKind, string> = {
  start: '开始',
  meta: '参数',
  delta: '输出',
  intent: '意图',
  match: '匹配',
  error: '错误',
  done: '完成',
  stop: '停止',
}
const logs = ref<LogEntry[]>([])
// 上一轮归档（G3 审计建议）：新 run 启动时保留最近一轮帧序列供回看
const prevLogs = ref<LogEntry[]>([])
const prevOpen = ref(false)
const logOpen = ref(false)
const logTab = ref<'conv' | 'log'>('log')
// ===== 对话轮次生命周期 =====
// meta 帧=开轮；done/error/stop=关轮（幂等，覆盖 done/error/abort/卸载全路径）。
// 关轮先把缓冲落盘本轮，再清 in-flight 缓冲；同时清残余 50ms 合帧定时器（防悬空回调）
function beginTurn(system: string, user: string): void {
  curTurn = reactive<ConvTurn>({
    system,
    user,
    reason: '',
    text: '',
    open: false,
    sysOpen: false,
    usrOpen: false,
    touched: false,
    finishReason: '',
    usage: undefined,
  })
  convTurns.value.push(curTurn)
}
function closeTurn(fr?: string, usage?: AiUsage): void {
  if (renderTimer !== null) {
    window.clearTimeout(renderTimer)
    renderTimer = null
  }
  if (curTurn) {
    curTurn.reason = reasonBuf
    curTurn.text = mdBuf
    curTurn.finishReason = fr ?? ''
    curTurn.usage = usage
  }
  mdBuf = ''
  reasonBuf = ''
  curTurn = null
}
function toggleTurn(t: ConvTurn): void {
  t.open = !t.open
  t.touched = true
}
const logBodyEl = ref<HTMLElement | null>(null)
// 非响应式：本 run 起始时间、首 delta 标记与日志自增 id（跨 run 单调；
// 500 上限 shift 后长度恒定，滚底 watch 改以末条 id 驱动——A1 审计修复）
let runStartTs = 0
let sawDelta = false // run 级首 delta 标记：仅驱动「模型开始输出」日志
let logSeq = 0

function log(kind: LogKind, msg: string): void {
  logs.value.push({ id: ++logSeq, t: Date.now(), kind, msg })
  if (logs.value.length > 500) logs.value.shift()
}
// 全量模型输出聚合（批量单轮=原 convText 行为不变；locate 的 ```json 截断视图作用于它）
const fullText = computed(() => convTurns.value.map((t) => t.text).join('\n\n'))
// 提问区：locate/flowmap 显示自然语言问题；explain/intent 无输入问题，回显范围说明
const hasQuestion = computed(() => !!question.value.trim())
const qAsked = computed(() => (hasQuestion.value ? question.value.trim() : scopeText.value))
// 空态判定：尚无任何对话轮次
const convEmpty = computed(() => !convTurns.value.length)
// token 展示：done 帧真实 usage（include_usage 末帧）精确展示；缺失（服务商不支持/demo）
// 时按字数估算并带 ≈ 前缀（CJK ≈0.7 tok/字、其他 ≈0.25 tok/字符，向上取整）
function estTokens(s: string): number {
  let cjk = 0
  for (const ch of s) if ((ch.codePointAt(0) ?? 0) >= 0x2e80) cjk++
  return Math.ceil(cjk * 0.7 + (s.length - cjk) * 0.25)
}
function tokText(tok: number | undefined, s: string): string {
  const real = tok != null && tok > 0
  return (real ? '' : '≈') + (real ? tok : estTokens(s)).toLocaleString('en-US') + ' tok'
}

function toggleLog(): void {
  logOpen.value = !logOpen.value
  // 流式中打开优先展示实时对话
  if (logOpen.value && phase.value === 'streaming') logTab.value = 'conv'
}
function fmtT(t: number): string {
  const d = new Date(t)
  const p = (n: number): string => String(n).padStart(2, '0')
  return p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds())
}
// 开合/切 tab：导航意图，无条件滚到底展示最新内容
watch([logOpen, logTab], () => {
  if (!logOpen.value) return
  nextTick(() => {
    const el = logBodyEl.value
    if (el && logOpen.value) el.scrollTop = el.scrollHeight
  })
})
// 流式追加（日志帧/对话更新）：仅当视口已近底部（40px 阈值）才跟随滚动，
// 尊重用户上翻回看——B1 审计修复（原先无条件滚底会每 50ms 把用户拉回底部）
watch(
  [
    () => logs.value[logs.value.length - 1]?.id ?? 0,
    fullText,
    () => convTurns.value.length,
    () => convTurns.value[convTurns.value.length - 1]?.reason.length ?? 0,
  ],
  () => {
    if (!logOpen.value) return
    nextTick(() => {
      const el = logBodyEl.value
      if (!el || !logOpen.value) return
      if (el.scrollHeight - el.scrollTop - el.clientHeight < 40) el.scrollTop = el.scrollHeight
    })
  },
)

// ===== 抽层高度拖拽 + 缓存 =====
// 顶部把手上下拖动调高，pointerup 落盘 localStorage；双击恢复 CSS 默认。
// 缓存值以内联 height: min(px, calc(100% - 保留)) 生效——窗口变小时 CSS 就近钳制不溢出抽屉
const LOG_H_KEY = 'prismproxy:review-ai-logsheet-h-v1'
const LOG_H_MIN = 200 // 与 .ai-logsheet min-height 一致
const LOG_H_RESERVE_EXTRA = 120 // 正文可视保留；上限 = 抽屉高 - footer 高(--ai-foot-h) - 120（I2 审计修复：footer 高单一事实源是 CSS 变量）
const logsheetEl = ref<HTMLElement | null>(null)
const logH = ref(0) // 0 = 未自定义，走 CSS 默认 min(58%, 430px)
const logDragging = ref(false)
let logDragStartY = 0
let logDragStartH = 0

try {
  const v = Number(localStorage.getItem(LOG_H_KEY))
  if (v >= LOG_H_MIN) logH.value = v
} catch {
  /* 存储不可用仅本会话生效 */
}

const logHStyle = computed<{ height: string } | undefined>(() =>
  logH.value > 0
    ? { height: `min(${logH.value}px, calc(100% - var(--ai-foot-h) - ${LOG_H_RESERVE_EXTRA}px))` }
    : undefined,
)

function logMaxH(): number {
  const drawerH = logsheetEl.value?.parentElement?.clientHeight ?? 0
  const footH = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--ai-foot-h')) || 35
  return Math.max(LOG_H_MIN, drawerH - footH - LOG_H_RESERVE_EXTRA)
}

function startLogDrag(e: PointerEvent): void {
  if (e.button !== 0) return // 仅左键拖拽（I1 审计修复：右键/中键不进入拖拽）
  const el = logsheetEl.value
  if (!el) return
  logDragging.value = true
  logDragStartY = e.clientY
  logDragStartH = el.clientHeight // clientWidth/Height 不含 1px border，= style 值零漂移（G-1 审计修复，rect 起点会逐会话 +1px）
  window.addEventListener('pointermove', onLogDragMove)
  window.addEventListener('pointerup', onLogDragEnd)
  window.addEventListener('pointercancel', onLogDragEnd)
  document.body.classList.add('row-resizing')
  try {
    ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
  } catch {
    /* 合成事件无活动指针，捕获失败不影响拖拽逻辑 */
  }
  e.preventDefault() // 防触发文本选择
}

function onLogDragMove(e: PointerEvent): void {
  if (!logDragging.value) return
  // 向上拖（clientY 减小）增高
  logH.value = Math.min(Math.max(logDragStartH + (logDragStartY - e.clientY), LOG_H_MIN), logMaxH())
}

function onLogDragEnd(): void {
  if (!logDragging.value) return
  logDragging.value = false
  window.removeEventListener('pointermove', onLogDragMove)
  window.removeEventListener('pointerup', onLogDragEnd)
  window.removeEventListener('pointercancel', onLogDragEnd)
  document.body.classList.remove('row-resizing')
  try {
    if (logH.value >= LOG_H_MIN) localStorage.setItem(LOG_H_KEY, String(logH.value)) // 未达下限（0 哨兵）不落盘，防纯点击残留 "0"（G-3 审计修复）
  } catch {
    /* 存储不可用仅本会话生效 */
  }
}

// 双击把手恢复默认高度
function resetLogH(): void {
  logH.value = 0
  try {
    localStorage.removeItem(LOG_H_KEY)
  } catch {
    /* 忽略 */
  }
}

// ===== 面板宽度拖拽 + 缓存 =====
// 左缘把手左右拖动调宽，pointerup 落盘 localStorage；双击恢复 CSS 默认 560px。
// 缓存值以内联 width: min(px, calc(100% - 保留)) 生效——窗口变小时 CSS 就近钳制不溢出容器，
// 无需 JS 响应 resize（与日志抽层高度拖拽同策略）
const AI_W_KEY = 'prismproxy:review-ai-panel-w-v1'
const AI_W_MIN = 420 // 拖拽下限：四模式 tabs + 正文区可读的最小宽度（窄窗口下实际显示宽由下方 CSS min() 就近钳制，可低于此值，G-4 已知设计）
const AI_W_RESERVE = 400 // 底层列表区可视保留；上限 = 容器宽 - 400
const drawerEl = ref<HTMLElement | null>(null)
const aiW = ref(0) // 0 = 未自定义，走 CSS 默认 560px
const wDragging = ref(false)
let wDragStartX = 0
let wDragStartW = 0

try {
  const v = Number(localStorage.getItem(AI_W_KEY))
  if (v >= AI_W_MIN) aiW.value = v
} catch {
  /* 存储不可用仅本会话生效 */
}

const aiWStyle = computed<{ width: string } | undefined>(() =>
  aiW.value > 0 ? { width: `min(${aiW.value}px, calc(100% - ${AI_W_RESERVE}px))` } : undefined,
)

function aiMaxW(): number {
  const cw = drawerEl.value?.parentElement?.clientWidth ?? 0
  return Math.max(AI_W_MIN, cw - AI_W_RESERVE)
}

function startWDrag(e: PointerEvent): void {
  if (e.button !== 0) return // 仅左键拖拽（对齐 I1 审计修复）
  const el = drawerEl.value
  if (!el) return
  wDragging.value = true
  wDragStartX = e.clientX
  wDragStartW = el.clientWidth // 不含 1px border-left，= style 值零漂移（G-1 审计修复，与日志抽层同步）
  window.addEventListener('pointermove', onWDragMove)
  window.addEventListener('pointerup', onWDragEnd)
  window.addEventListener('pointercancel', onWDragEnd)
  document.body.classList.add('ew-resizing')
  try {
    ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
  } catch {
    /* 合成事件无活动指针，捕获失败不影响拖拽逻辑 */
  }
  e.preventDefault() // 防触发文本选择
}

function onWDragMove(e: PointerEvent): void {
  if (!wDragging.value) return
  // 向左拖（clientX 减小）增宽
  aiW.value = Math.min(Math.max(wDragStartW + (wDragStartX - e.clientX), AI_W_MIN), aiMaxW())
}

function onWDragEnd(): void {
  if (!wDragging.value) return
  wDragging.value = false
  window.removeEventListener('pointermove', onWDragMove)
  window.removeEventListener('pointerup', onWDragEnd)
  window.removeEventListener('pointercancel', onWDragEnd)
  document.body.classList.remove('ew-resizing')
  try {
    if (aiW.value >= AI_W_MIN) localStorage.setItem(AI_W_KEY, String(aiW.value)) // 未达下限（0 哨兵）不落盘，防纯点击残留 "0"（G-3 审计修复，与日志抽层同步）
  } catch {
    /* 存储不可用仅本会话生效 */
  }
}

// 双击把手恢复默认宽度
function resetW(): void {
  aiW.value = 0
  try {
    localStorage.removeItem(AI_W_KEY)
  } catch {
    /* 忽略 */
  }
}

// ===== 运行控制 =====
function requestStart(auto = false): void {
  if (phase.value === 'streaming') return
  // 自动开始（explain 进入面板）遇 redact 关闭：放弃，留待用户手点（popconfirm 无法代答）
  if (auto && cfg.value && !cfg.value.redact) return
  if (!noticeSeen()) {
    noticeShow.value = true
    return
  }
  void doStart()
}

function noticeSeen(): boolean {
  try {
    return localStorage.getItem(NOTICE_KEY) === '1'
  } catch {
    return false
  }
}

function onNoticeOk(): void {
  try {
    localStorage.setItem(NOTICE_KEY, '1')
  } catch {
    /* 忽略存储失败 */
  }
  void doStart()
}

async function doStart(): Promise<void> {
  resetRun()
  phase.value = 'streaming'
  runStartTs = Date.now()
  sawDelta = false
  log('start', TABS.find((t) => t.key === activeTab.value)?.label + ' · ' + scopeText.value)
  const ctl = new AbortController()
  abortCtl = ctl
  const seq = ++runSeq
  // 运行级迟到帧守卫：本 run 被停/被重开后，旧流残余帧不污染新 run 缓冲
  const frameOf = (ev: AiChatEvent): void => {
    if (seq === runSeq) onFrame(ev)
  }
  try {
    if (activeTab.value === 'intent' && singleMode.value) {
      // 单条轮询：逐条独立调用模型（每条一次完整往返），规避整批 prompt 过大导致的
      // 输出截断/首响应超时。单条失败不中断后续；停止或 run 被重开立即退出循环。
      // 候选列表开始时快照：AI 抽屉无遮罩，运行中翻页会清空 checkedIds（intentIds
      // 静默膨胀为全视图）、筛选重置会产生 undefined id——实时读 computed 会错标/伪失败。
      let failed = 0
      const ids = [...intentIds.value]
      const total = ids.length
      singleTotal.value = total // 进度分母同步快照，防运行中分母漂移
      for (let i = 0; i < total; i++) {
        if (seq !== runSeq || ctl.signal.aborted) break
        const id = ids[i]
        // 单条轮询帧守卫：done/error 帧只代表单条结束——done 记录后继续下一条；
        // error 不置 error 态，由 analyze 的 reject 交循环 catch 统一计数（http/wails 实现均帧后抛出）
        const itemFrame = (ev: AiChatEvent): void => {
          if (seq !== runSeq) return
          if (ev.event === 'done') {
            const len = mdBuf.length // closeTurn 会清缓冲，字数先取
            closeTurn(ev.data?.finishReason, ev.data?.usage)
            const fr = ev.data?.finishReason
            log('done', `第 ${i + 1}/${total} 条完成 · ${len} 字` + (fr === 'length' ? ' · 输出被截断（模型上下文不足）' : ''))
            return
          }
          if (ev.event === 'error') return
          onFrame(ev)
        }
        try {
          await runIntent(props.api, [id], itemFrame, ctl.signal, { includeReqBody: includeReq.value })
        } catch (e) {
          if (ctl.signal.aborted || seq !== runSeq) break
          closeTurn() // 本条中途失败（error 帧被 itemFrame 拦截或网络异常）：关闭进行中轮次再继续下一条
          failed++
          log('error', `第 ${i + 1}/${total} 条失败：${flowLabelOf(id)} · ${String((e as Error)?.message ?? e)}`)
        }
      }
      if (seq === runSeq && phase.value === 'streaming' && total > 0) {
        if (failed >= total) {
          // 全部失败（系统性故障典型场景）：对齐批量失败语义，置 error 态让主界面红条可见
          phase.value = 'error'
          errMsg.value = `单条轮询全部失败（0/${total}），详见日志抽层`
        } else if (failed > 0) {
          log('error', `轮询结束 · 成功 ${total - failed}/${total} · 失败 ${failed} 条`)
        }
      }
    } else if (activeTab.value === 'intent') {
      await runIntent(props.api, intentIds.value, frameOf, ctl.signal, { includeReqBody: includeReq.value })
    } else {
      await props.api.analyze(
        {
          mode: activeTab.value,
          flowId: activeTab.value === 'explain' ? props.flowId : undefined,
          ids: activeTab.value === 'explain' ? undefined : byStartedDesc.value,
          question: activeTab.value === 'locate' || activeTab.value === 'flowmap' ? question.value.trim() : undefined,
          options: {
            includeReqBody: includeReq.value,
            includeRespBody: activeTab.value === 'locate' ? includeResp.value : undefined,
            language: 'zh',
          },
        },
        frameOf,
        ctl.signal,
      )
    }
    // 令牌校验：仅最新 run 可迁移状态（旧 run 续体读到新 run 的 streaming 也不得误标）
    if (seq === runSeq && phase.value === 'streaming') phase.value = 'done'
  } catch (e) {
    closeTurn() // 无 error 帧直接 reject（网络中断等）：关闭进行中轮次（幂等）
    // 停止（abort 静默收尾）后到达的 reject 不转 error 态；旧 run 的 reject 不覆盖新 run
    if (seq === runSeq && phase.value === 'streaming') {
      phase.value = 'error'
      errMsg.value = String((e as Error)?.message ?? e)
    }
  } finally {
    // 只回收自己的句柄：竞态下 abortCtl 可能已被新 run 换掉（M1 审计修复）
    if (abortCtl === ctl) abortCtl = null
  }
}

function onFrame(ev: AiChatEvent): void {
  // 迟到帧守卫：stop/done/error 之后到达的帧一律忽略
  if (phase.value !== 'streaming') return
  switch (ev.event) {
    case 'meta': {
      meta.value = ev.data
      const m = ev.data
      beginTurn(m.system ?? '', m.user ?? '') // 每次送审开新轮：单条轮询每条流独立成对气泡
      log('meta', `送审 ${m.sent}/${m.total} 条 · 预算 ${m.budget.flows} 流 / ${m.budget.kb}KB` + (m.truncated ? ' · 已截断' : ''))
      break
    }
    case 'delta':
      if (ev.data.reason) pushReason(ev.data.reason)
      if (ev.data.text) pushDelta(ev.data.text)
      break
    case 'intent':
      intentDone.value++
      upsert(ev.data) // P1 审计修复：缓存写入挪进 runSeq+phase 双守卫内（旧任务迟到帧不再污染共享缓存）
      log('intent', `#${ev.data.seq} ${ev.data.intent}` + (ev.data.needsBody ? '（需正文）' : ''))
      break
    case 'match':
      upsertMatch(ev.data)
      log('match', `#${ev.data.rank} ${ev.data.method} ${ev.data.url} · ${confLabel(ev.data.confidence)}`)
      break
    case 'error':
      closeTurn() // 本轮就此终止，缓冲落盘后不再累积
      phase.value = 'error'
      errMsg.value = ev.data.message
      log('error', ev.data.message)
      break
    case 'done': {
      const len = mdBuf.length // closeTurn 会清缓冲，字数先取
      closeTurn(ev.data?.finishReason, ev.data?.usage)
      phase.value = 'done'
      // finishReason 如实来自上游（length=输出预算耗尽被截断），截断时附加操作提示
      const fr = ev.data?.finishReason
      log('done', `共 ${len} 字` + (runStartTs ? ` · 耗时 ${((Date.now() - runStartTs) / 1000).toFixed(1)}s` : '') +
        (fr === 'length' ? ' · 输出被截断（模型上下文/输出预算不足），建议减小批量流数或精简正文' : ''))
      break
    }
  }
}

// 50ms 合帧统一出口：正文与思考增量共用同一 timer，同时写入当前 in-flight 轮
function scheduleRender(): void {
  if (renderTimer !== null) return
  renderTimer = window.setTimeout(() => {
    renderTimer = null
    if (curTurn) {
      curTurn.text = mdBuf
      curTurn.reason = reasonBuf
    }
  }, 50)
}
function pushDelta(text: string): void {
  if (!sawDelta) {
    sawDelta = true
    log('delta', '模型开始输出')
  }
  // 本轮首个正文增量：思考阶段结束，自动收起本轮思考区（用户手动开合过则不打扰）
  if (curTurn && !mdBuf && !curTurn.touched && curTurn.open) curTurn.open = false
  mdBuf += text
  scheduleRender()
}
function pushReason(reason: string): void {
  // 思考增量先于正文到达：自动展开本轮思考区（正文开始后不再打扰；touched 已由 toggleTurn 置位）
  if (curTurn && !reasonBuf && !curTurn.touched && !curTurn.open) curTurn.open = true
  reasonBuf += reason
  scheduleRender()
}

function upsertMatch(m: AiMatchItem): void {
  const i = matches.value.findIndex((x) => x.flowId === m.flowId)
  if (i >= 0) matches.value.splice(i, 1, m)
  else matches.value.push(m)
  matches.value.sort((a, b) => a.rank - b.rank)
}

function stop(): void {
  if (phase.value !== 'streaming') return
  phase.value = 'stopped'
  abortCtl?.abort()
  abortCtl = null
  closeTurn() // P2 审计修复：清残余 50ms 合帧定时器并把最后一批 delta 落盘本轮（防悬空回调）
  log('stop', '已手动停止')
}

// 清运行态（保留 question/开关等输入值）
function resetRun(): void {
  stop()
  phase.value = 'idle'
  errMsg.value = ''
  mdBuf = ''
  reasonBuf = ''
  curTurn = null
  convTurns.value = []
  meta.value = null
  matches.value = []
  intentDone.value = 0
  singleTotal.value = 0
  // 日志按 run 独立：本记录档为上一轮供回看（G3 审计建议），本轮从零开始
  if (logs.value.length) {
    prevLogs.value = logs.value
    prevOpen.value = false
  }
  logs.value = []
}

function closePanel(): void {
  stop()
  emit('update:show', false)
}

// Esc 关闭（streaming 先停止）；输入框聚焦时不拦截
function onEsc(e: KeyboardEvent): void {
  if (e.key !== 'Escape' || noticeShow.value) return
  const t = e.target as HTMLElement | null
  if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return
  // 日志抽层展开时先收抽层，再按一次 Esc 才关面板（Z1 审计修复：交互层级）
  if (logOpen.value) {
    if (logDragging.value) onLogDragEnd() // 拖拽中收抽层先终止拖拽态（摘监听/落盘，I3 审计修复）
    if (wDragging.value) onWDragEnd() // 宽度拖拽把手上部在抽层外仍可达：收抽层同时终止（与上行 I3 同构：一次 Esc = 终止拖拽 + 逐层收合，G-2 审计修复）
    logOpen.value = false
    return
  }
  // 宽度拖拽中先终止拖拽态（摘监听/落盘，对齐 I3），Esc 语义继续走层级收合
  if (wDragging.value) onWDragEnd()
  closePanel()
}

// ===== 结果辅助 =====
function intentRowTip(it: IntentResult): string {
  const tips: string[] = ['点击在列表中定位该流']
  if (it.confidence === 'low') tips.push('AI 对此判定置信度较低，仅供参考')
  if (it.needsBody) tips.push('需结合请求正文才能准确判定；可勾选「包含请求正文」重新标注')
  return tips.join('；')
}
function confCls(c: 'high' | 'medium' | 'low'): string {
  return c === 'high' ? 'k-ai-conf-high' : c === 'medium' ? 'k-ai-conf-med' : 'k-ai-low'
}
function confLabel(c: 'high' | 'medium' | 'low'): string {
  return c === 'high' ? '高' : c === 'medium' ? '中' : '低'
}

// ===== 联动 =====
watch(
  () => props.mode,
  (m) => {
    activeTab.value = m
  },
)
// 切模式重置运行态（同面板单任务）
watch(activeTab, () => resetRun())

watch(
  () => props.show,
  (s) => {
    if (s) {
      activeTab.value = props.mode
      void loadCfg()
      window.addEventListener('keydown', onEsc)
      // explain 自动开始：cfg 已缓存时立即触发，否则由 loadCfg 完成回调触发
      if (props.mode === 'explain' && props.flowId && cfgOk.value) requestStart(true)
    } else {
      stop()
      window.removeEventListener('keydown', onEsc)
    }
  },
)

// explain 目标流变化：重置并自动重新解读（仅 explain 且 cfg 就绪时）。
// flowId 现跟随列表选中流：locate「查看→」跳转 / intent 勾选浏览引起的选中变化
// 不得 resetRun 打断在跑任务，故加 activeTab 守卫（resetRun 会 abort 在跑任务）
watch(
  () => props.flowId,
  () => {
    if (props.show && activeTab.value === 'explain') {
      resetRun()
      if (props.flowId && cfgOk.value) requestStart(true)
    }
  },
)

// 卸载清理（M2 审计修复）：主窗切走复盘视图（v-if 卸载）时停掉在跑流（隐私外发中止）、
// 摘除 Esc 监听（该监听仅 show→false 时移除，跨挂载会泄漏）
onBeforeUnmount(() => {
  stop()
  closeTurn() // P2 审计修复：非 streaming 卸载（如 error 态）时合帧定时器可能仍挂，兜底清掉
  window.removeEventListener('keydown', onEsc)
  // 高度拖拽中卸载：摘除 window 级监听并还原 body 光标
  if (logDragging.value) onLogDragEnd()
  // 宽度拖拽中卸载：同上兜底（摘监听/还原光标/落盘）
  if (wDragging.value) onWDragEnd()
})
</script>
