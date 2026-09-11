package app

// ai_bridge.go：AI 分析接线层（M13，设计稿 §5.3 v2.1 定稿）。
// 职责：把 settings.AIConfig（全局配置）+ 复盘归档库（persist，M12）+ ai 包
// （纯能力：客户端/Prompt 裁剪/文末 JSON 解析）串成一次完整分析。
// ctlapi.Service（HTTP SSE，internal/ctlapi/ai.go）与 Wails 主窗绑定
// （bindings_ai.go，P2-13）共用同一编排，不另起逻辑。
//
// 错误分界契约（与 ctlapi.handleAIChat）：首次 emit（meta 帧）之前返回的 error
// 为同步错误——handler 未写响应头，据哨兵映射状态码
// （ctlapi.ErrAIConflict→409、ctlapi.ErrAIBadReq→400、其余→500）；
// meta 帧之后错误一律走 error 事件帧并 return nil；调用方取消静默返回。

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"prismproxy/internal/ai"
	"prismproxy/internal/capture"
	"prismproxy/internal/ctlapi"
	"prismproxy/internal/persist"
	"prismproxy/internal/settings"
)

// ---------- ctlapi.Service AI 半边（internal/ctlapi/ai.go 定义路由） ----------

func (s *ctlService) GetAIConfig() (any, error) { return s.app.GetAIConfigView() }

func (s *ctlService) SaveAIConfig(raw json.RawMessage) (any, error) {
	return s.app.SaveAIConfigPatch(raw)
}

func (s *ctlService) AITestConnection() (any, error) { return s.app.AITestConn() }

func (s *ctlService) StreamAIChat(ctx context.Context, req ctlapi.AIChatRequest, emit ctlapi.AIChatEmit) error {
	return s.app.runAIChat(ctx, req, emit)
}

// ---------- 配置视图与保存（P2-3/P2-4） ----------

// GetAIConfigView AI 配置掩码视图（GET /ai/config）：无密钥投影 + hasApiKey/apiKeyMasked。
// key 永不回原值（前端掩码展示；保存时空串=保持不变），keyMissingWarn 供前端引导提示。
func (a *App) GetAIConfigView() (map[string]any, error) {
	a.projMu.Lock()
	g := *a.gcfg // 浅拷贝只读视图（settingsViewLocked 同模式）
	a.projMu.Unlock()
	return aiConfigView(g, g.AI.WithDefaults()), nil
}

// SaveAIConfigPatch AI 配置部分更新（POST /ai/config）：map[string]json.RawMessage
// 逐字段合并，未提供字段保持不变；apiKey 空串=保持原值、__clear__=显式清除。
// 合并基准为原始存量值：兜底仅读取侧生效，落盘保留哨兵 0 值不固化 WithDefaults
// 结果（P0-7 红线）；校验用兜底副本，显式 temperature:0 等哨兵按默认值放行。
// 校验失败不落盘；成功即时写全局配置（SaveGlobal）并回传兜底生效后的掩码视图。
func (a *App) SaveAIConfigPatch(raw json.RawMessage) (map[string]any, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		// 坏 JSON（含 64KB 截断残缺体）属客户端错误 → 400，不得伪装 500
		return nil, fmt.Errorf("%w: 请求体解析失败: %v", ctlapi.ErrAIBadReq, err)
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("%w: 未提供任何字段", ctlapi.ErrAIBadReq)
	}

	// 候选副本合并（SaveSettings 同模式）：合并/校验期间不持锁写全局。
	// 基准用原始存量值：兜底只在读取侧，落盘不固化（P0-7）。
	a.projMu.Lock()
	g := *a.gcfg
	a.projMu.Unlock()
	cfg := g.AI

	bad := func(k string, err error) error {
		return fmt.Errorf("%w: 字段 %s 解析失败: %v", ctlapi.ErrAIBadReq, k, err)
	}
	// apiKey 补丁先记录、循环后最后生效：SyncAICurrent 会以当前条目 key 覆盖顶层，
	// map 遍历无序，若先应用 key 再同步 entries，补丁会被静默覆盖（含 __clear__ 被复活）
	var keyPatch string
	hasKeyPatch := false
	for k, v := range fields {
		switch k {
		case "enabled":
			if err := json.Unmarshal(v, &cfg.Enabled); err != nil {
				return nil, bad(k, err)
			}
		case "provider":
			if err := json.Unmarshal(v, &cfg.Provider); err != nil {
				return nil, bad(k, err)
			}
		case "baseUrl":
			if err := json.Unmarshal(v, &cfg.BaseURL); err != nil {
				return nil, bad(k, err)
			}
		case "model":
			if err := json.Unmarshal(v, &cfg.Model); err != nil {
				return nil, bad(k, err)
			}
		case "entries":
			var entries []settings.AIModelEntry
			if err := json.Unmarshal(v, &entries); err != nil {
				return nil, bad(k, err)
			}
			cfg.Entries = entries
		case "apiKey":
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return nil, bad(k, err)
			}
			switch s {
			case "": // 空串=保持原值（掩码视图不回原 key，防止误清）
			case "__clear__": // 哨兵：显式清除
				keyPatch, hasKeyPatch = "", true
			default:
				if t := strings.TrimSpace(s); t != "" { // 纯空白=未输入，保持原值（防误清，对齐前端守卫与 mock）
					keyPatch, hasKeyPatch = t, true
				}
			}
		case "temperature":
			if err := json.Unmarshal(v, &cfg.Temperature); err != nil {
				return nil, bad(k, err)
			}
		case "timeoutSec":
			if err := json.Unmarshal(v, &cfg.TimeoutSec); err != nil {
				return nil, bad(k, err)
			}
		case "maxFlows":
			if err := json.Unmarshal(v, &cfg.MaxFlows); err != nil {
				return nil, bad(k, err)
			}
		case "maxKb":
			if err := json.Unmarshal(v, &cfg.MaxKB); err != nil {
				return nil, bad(k, err)
			}
		case "redact":
			if err := json.Unmarshal(v, &cfg.Redact); err != nil {
				return nil, bad(k, err)
			}
		default:
			return nil, fmt.Errorf("%w: 不支持的配置字段 %q", ctlapi.ErrAIBadReq, k)
		}
	}
	// 条目/顶层 key 同步（循环后统一做，map 遍历无序故不进 case）：
	// 提供 entries → 归一化 + 当前条目快照同步顶层；提供 apiKey → 顶层 key 单改回写
	// 当前条目（防下次 SaveSettings 条目落盘把单独改的 key 覆盖回退）。
	// key 补丁在 entries 同步之后最后生效：SyncAICurrent 以当前条目 key 覆盖顶层，
	// 先应用 key 补丁会被覆盖（entries+apiKey 同请求时补丁静默丢失，审计问题2）
	if _, ok := fields["entries"]; ok {
		cfg.Entries = settings.NormalizeAIEntries(cfg.Entries)
		cfg.SyncAICurrent()
	}
	if hasKeyPatch {
		cfg.APIKey = keyPatch
		cfg.SyncKeyToCurrent()
	}
	// 校验用兜底副本：哨兵 0 值按默认生效判定（显式 temperature:0 放行），落盘仍保留哨兵
	if err := cfg.WithDefaults().Validate(); err != nil {
		return nil, err
	}

	a.projMu.Lock()
	a.gcfg.AI = cfg
	err := a.gcfg.SaveGlobal(a.cfgDir)
	a.projMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("保存全局配置: %w", err)
	}

	g.AI = cfg                                      // 供 WarnNoKey 按新值判定（Enabled/APIKey/Provider 与兜底副本一致）
	return aiConfigView(g, cfg.WithDefaults()), nil // 视图回传兜底生效值
}

// aiConfigView 掩码视图构造（GET 与保存回传共用，字段名与 settings.AIConfig json tag 一致）。
// 条目为脱敏视图（不含原文 key，仅 hasKey 供 UI 显示已配置态）。
func aiConfigView(g settings.GlobalSettings, cfg settings.AIConfig) map[string]any {
	return map[string]any{
		"enabled":        cfg.Enabled,
		"provider":       cfg.Provider,
		"baseUrl":        cfg.BaseURL,
		"model":          cfg.Model,
		"entries":        aiEntriesView(cfg.Entries),
		"temperature":    cfg.Temperature,
		"timeoutSec":     cfg.TimeoutSec,
		"maxFlows":       cfg.MaxFlows,
		"maxKb":          cfg.MaxKB,
		"redact":         cfg.Redact,
		"hasApiKey":      cfg.APIKey != "",
		"apiKeyMasked":   maskAPIKey(cfg.APIKey),
		"keyMissingWarn": g.WarnNoKey(),
	}
}

// aiEntriesView 供应商条目脱敏视图：模型/别名/URL/当前标记 + hasKey，永不回原文 key
//（主窗 AiTab 已不消费此视图；复盘页与 HTTP 端 GET /ai/config 客户端使用）。
func aiEntriesView(entries []settings.AIModelEntry) []map[string]any {
	if entries == nil {
		return nil
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"provider": e.Provider,
			"model":    e.Model,
			"alias":    e.Alias,
			"baseUrl":  e.BaseURL,
			"hasKey":   e.APIKey != "",
			"current":  e.Current,
		})
	}
	return out
}

// maskAPIKey 密钥掩码：不回原值，仅尾 4 位辅助辨认。
func maskAPIKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 8 {
		return "****"
	}
	return "****" + k[len(k)-4:]
}

// ---------- 测试连接（P2-5） ----------

// AITestConn ctlapi 半边（POST /ai/test）：读已存配置探测，回 map 视图（P2-4 形状）。
func (a *App) AITestConn() (map[string]any, error) {
	a.projMu.Lock()
	cfg := a.gcfg.AI
	upMode, upProxy, listen := a.gcfg.UpstreamMode, a.gcfg.UpstreamProxy, a.gcfg.ListenAddr
	a.projMu.Unlock()
	res, err := a.aiProbe(cfg, resolveUpstream(upMode, upProxy, listen))
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": res.OK, "model": res.Model, "latencyMs": res.LatencyMS, "message": res.Message}, nil
}

// aiProbe 探测核心（ai.Client.Probe，max_tokens:8）：10s 超时、不落盘、独立于
// enabled 开关（供保存前/未落盘临时值验证，ctlapi P2-4 与 Wails P2-9 共用）。
// 接口地址/模型未配置 → 400 哨兵错误；网络/服务商失败为正常业务结果
// → ok=false + 上游原文（不作 error 返回，前端内联展示）。
func (a *App) aiProbe(cfg settings.AIConfig, proxyURL string) (*AITestResult, error) {
	cfg = cfg.WithDefaults()
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("%w: 请先配置 AI 接口地址与模型", ctlapi.ErrAIBadReq)
	}

	client := ai.NewClient(ai.Config{
		BaseURL:     cfg.BaseURL,
		APIKey:      cfg.APIKey,
		Model:       cfg.Model,
		Temperature: cfg.Temperature,
		Timeout:     aiTestTimeout,
		ProxyURL:    proxyURL,
	})
	ctx, cancel := context.WithTimeout(context.Background(), aiTestTimeout)
	defer cancel()

	start := time.Now()
	reply, err := client.Probe(ctx, []ai.Message{{Role: ai.RoleUser, Content: "ping，请回复 pong"}})
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return &AITestResult{OK: false, Model: cfg.Model, LatencyMS: latency, Message: err.Error()}, nil
	}
	return &AITestResult{OK: true, Model: cfg.Model, LatencyMS: latency, Message: strings.TrimSpace(reply)}, nil
}

const aiTestTimeout = 10 * time.Second

// aiListModels 模型列表核心（ai.Client.ListModels，OpenAI 兼容 GET {base}/models）：
// 10s 超时、不落盘，供设置页「获取模型」用界面当前值临时构造（key 由调用方回填）。
// BaseURL 未配置为本地错误；网络/服务商失败（含空列表）原样上抛由前端呈现。
func (a *App) aiListModels(cfg settings.AIConfig, proxyURL string) ([]string, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("请先填写 AI 接口地址（BaseURL）")
	}
	client := ai.NewClient(ai.Config{
		BaseURL:  cfg.BaseURL,
		APIKey:   cfg.APIKey,
		Timeout:  aiTestTimeout,
		ProxyURL: proxyURL,
	})
	ctx, cancel := context.WithTimeout(context.Background(), aiTestTimeout)
	defer cancel()
	return client.ListModels(ctx)
}

// ---------- 分析编排（P2-6） ----------

// aiGateAcquire 并发闸门（P2-7）：ctlapi runAIChat 与 Wails AIChatStart 共用
// aiBusy，CAS 失败返回同一哨兵（HTTP 409 与 Wails error 文案一致，计划 P2-13）。
func (a *App) aiGateAcquire() error {
	if !a.aiBusy.CompareAndSwap(false, true) {
		return fmt.Errorf("%w", ctlapi.ErrAIConflict)
	}
	return nil
}

// runAIChat AI 分析编排入口：并发闸门（CAS 失败=已有分析任务，同步错误 → 409）
// → 编排体 runAIChatOnce；Wails 主窗（bindings_ai.go AIChatStart）前置 CAS 后
// 直入编排体，与 ctlapi 共用同一闸门与同一冲突文案。
func (a *App) runAIChat(ctx context.Context, req ctlapi.AIChatRequest, emit ctlapi.AIChatEmit) error {
	if err := a.aiGateAcquire(); err != nil {
		return err
	}
	defer a.aiBusy.Store(false)
	return a.runAIChatOnce(ctx, req, emit)
}

// runAIChatOnce 编排体（闸门已由入口 runAIChat / AIChatStart 前置 CAS 占用；
// 下方步骤编号承接编排全序，1=闸门步）：读配置 → 参数校验 → 取数 → Prompt 裁剪 →
// 流式转发（delta 事件）→ 文末 JSON 块解析（intent/match 事件）→ done 帧。
// 签名对应 ctlapi.Service.StreamAIChat；Wails 事件桥复用同一编排。
func (a *App) runAIChatOnce(ctx context.Context, req ctlapi.AIChatRequest, emit ctlapi.AIChatEmit) error {
	// 2) 读配置（gcfg 读须持 projMu）+ 出站代理装配参数
	a.projMu.Lock()
	cfg := a.gcfg.AI
	upMode, upProxy, listen := a.gcfg.UpstreamMode, a.gcfg.UpstreamProxy, a.gcfg.ListenAddr
	a.projMu.Unlock()
	cfg = cfg.WithDefaults()

	// 3) 配置校验（同步错误段 → 400）
	if !cfg.Enabled {
		return fmt.Errorf("%w: AI 分析未启用，请先在设置中开启", ctlapi.ErrAIBadReq)
	}
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.Model) == "" {
		return fmt.Errorf("%w: AI 接口地址或模型未配置", ctlapi.ErrAIBadReq)
	}

	// 4) 参数校验 + 正文包含语义（OR）：explain/flowmap 恒带正文，intent/locate 经 options 追加
	mode := ai.ChatMode(req.Mode)
	switch mode {
	case ai.ModeExplain:
		if strings.TrimSpace(req.FlowID) == "" {
			return fmt.Errorf("%w: explain 模式必须提供 flowId", ctlapi.ErrAIBadReq)
		}
	case ai.ModeIntent, ai.ModeLocate, ai.ModeFlowmap:
		if len(req.IDs) == 0 {
			return fmt.Errorf("%w: %s 模式必须提供 ids", ctlapi.ErrAIBadReq, req.Mode)
		}
	default:
		return fmt.Errorf("%w: 不支持的分析模式 %q（可选 explain|intent|locate|flowmap）", ctlapi.ErrAIBadReq, req.Mode)
	}
	baseReq, baseResp := ai.DefaultBodies(mode)
	wantReq := baseReq || req.Options.IncludeReqBody
	wantResp := baseResp || req.Options.IncludeRespBody

	// 5) 取数（复盘归档库）：取数+映射完成后立即 done()，不持归档在途计数跨过
	// 整个流式过程（长分析不阻塞项目切换，与 ReviewFlowDetail 同通道）。
	w, done, err := a.reviewReader()
	if err != nil {
		return err // 「请先打开项目」/「项目已切换」：500（同步错误段）
	}
	inputs := make([]ai.FlowInput, 0, 1)
	valid := make(map[string]bool, 1)
	flows := make(map[string]*capture.Flow, 1) // locate 回填 match 帧 method/url 用
	if w == nil {
		done()
		if mode == ai.ModeExplain {
			return fmt.Errorf("%w: 记录 %s 不存在或已删除（归档库为空）", ctlapi.ErrAIBadReq, req.FlowID)
		}
		return fmt.Errorf("%w: 所选记录均已失效（可能已删除或被淘汰）", ctlapi.ErrAIBadReq)
	}
	if mode == ai.ModeExplain {
		f, ferr := w.LoadFlowByID(req.FlowID)
		switch {
		case ferr != nil:
			err = fmt.Errorf("读取归档库失败: %w", ferr)
		case f == nil:
			err = fmt.Errorf("%w: 记录 %s 不存在或已删除", ctlapi.ErrAIBadReq, req.FlowID)
		default:
			inputs = append(inputs, flowToAIInput(w, f, wantReq, wantResp))
			valid[f.ID] = true
			flows[f.ID] = f
		}
	} else {
		list, ferr := w.LoadFlowsByIDs(req.IDs)
		switch {
		case ferr != nil:
			err = fmt.Errorf("读取归档库失败: %w", ferr)
		case len(list) == 0:
			err = fmt.Errorf("%w: 所选记录均已失效（可能已删除或被淘汰）", ctlapi.ErrAIBadReq)
		default:
			for _, f := range list {
				inputs = append(inputs, flowToAIInput(w, f, wantReq, wantResp))
				valid[f.ID] = true
				flows[f.ID] = f
			}
		}
	}
	done()
	if err != nil {
		return err
	}

	// 6) Prompt 构造 + 双预算裁剪（meta 帧数据源为 BuildResult）
	opt := ai.BuildOptions{
		Mode:            mode,
		Question:        req.Question,
		IncludeReqBody:  wantReq,
		IncludeRespBody: wantResp,
		MaxFlows:        cfg.MaxFlows,
		MaxKB:           cfg.MaxKB,
		Redact:          cfg.Redact,
		Language:        "zh",
	}
	br := ai.BuildPrompt(inputs, opt)

	// 7) 客户端装配：出站代理跟随全局 UpstreamMode（selfAddr=本工具监听地址防回环）
	client := ai.NewClient(ai.Config{
		BaseURL:     cfg.BaseURL,
		APIKey:      cfg.APIKey,
		Model:       cfg.Model,
		Temperature: cfg.Temperature,
		Timeout:     time.Duration(cfg.TimeoutSec) * time.Second,
		ProxyURL:    resolveUpstream(upMode, upProxy, listen),
	})

	// 8) meta 帧：首个 emit——此后错误一律走 error 事件帧；
	// system/user 同帧带回实际送审提示词（前端对话 tab 展示，已按配置脱敏）
	meta := map[string]any{
		"mode":      string(mode),
		"total":     br.Total,
		"sent":      br.Sent,
		"budget":    map[string]any{"flows": cfg.MaxFlows, "kb": cfg.MaxKB},
		"truncated": br.Truncated,
		"system":    br.System,
		"user":      br.User,
	}
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var aborted bool
	emitSafe := func(ev string, data any) bool {
		if aborted {
			return false
		}
		if err := emit(ev, data); err != nil {
			aborted = true
			cancel() // 下游已断开/停止：取消 Stream，静默收尾
			return false
		}
		return true
	}
	if !emitSafe(ctlapi.AIEventMeta, meta) {
		return nil
	}

	// 9) 流式转发：delta 帧（text/reason 分列），同时累积全文供文后解析
	messages := []ai.Message{
		{Role: ai.RoleSystem, Content: br.System},
		{Role: ai.RoleUser, Content: br.User},
	}
	var sb strings.Builder
	streamErr := client.Stream(sctx, messages, func(d ai.Delta) {
		sb.WriteString(d.Text)
		if d.Text != "" {
			emitSafe(ctlapi.AIEventDelta, map[string]any{"text": d.Text})
		}
		if d.Reason != "" {
			emitSafe(ctlapi.AIEventDelta, map[string]any{"reason": d.Reason})
		}
	})
	if streamErr != nil {
		if ctx.Err() != nil || sctx.Err() != nil {
			return nil // 用户停止/SSE 断开：静默关闭，不发 error 帧
		}
		emitSafe(ctlapi.AIEventError, map[string]any{"message": streamErr.Error()})
		return nil
	}
	if sctx.Err() != nil {
		return nil // 停止/断连恰在完成边界：静默收尾，不发残余 intent/match/done 帧
	}

	// 10) 文末 JSON 块解析 → intent/match 事件（解析失败仅展示 Markdown，不报错）
	md := sb.String()
	switch mode {
	case ai.ModeIntent:
		if items, cleaned, ok := ai.ExtractIntents(md, valid); ok {
			md = cleaned
			for _, it := range items {
				if !emitSafe(ctlapi.AIEventIntent, it) {
					return nil
				}
			}
			// 解析完成后持久化到归档库（复盘列表/详情回读展示，M13 §7）；
			// 失败仅记日志不影响流式收尾。
			a.persistFlowIntents(items)
		}
	case ai.ModeLocate:
		if items, cleaned, ok := ai.ExtractMatches(md, valid); ok {
			md = cleaned
			for _, it := range items {
				if f := flows[it.FlowID]; f != nil && f.Request != nil {
					it.Method, it.URL = f.Request.Method, f.Request.URL
				}
				if !emitSafe(ctlapi.AIEventMatch, it) {
					return nil
				}
			}
		}
	}
	emitSafe(ctlapi.AIEventDone, map[string]any{"finishReason": "stop", "truncated": br.Truncated})
	return nil
}

// persistFlowIntents 意图解析结果持久化到归档库（M13 §7，fire-and-forget）：
// 复盘列表/详情经 flow_intents 回读展示。归档库打不开或项目代际不匹配时静默跳过
// （仅记日志），持久化是增强能力，绝不影响流式收尾与前端会话缓存。
// 注意：走 acquireArchive——intent 分析取数已依赖归档库存在（reviewReader 读到过数据），
// 此处打开的必是既有库，不会凭空建库。
func (a *App) persistFlowIntents(items []ai.IntentItem) {
	if len(items) == 0 {
		return
	}
	gen := a.projGen.Load()
	if a.currentID() == "" {
		return
	}
	w, archGen, done, err := a.acquireArchive()
	if err != nil {
		log.Printf("ai: 意图结果持久化跳过（归档库不可用）: %v", err)
		return
	}
	defer done()
	if archGen != gen {
		return // 项目已切换：放弃回写，防写错库（同 TagFlows gen 校验口径）
	}
	its := make([]persist.FlowIntent, 0, len(items))
	for _, it := range items {
		its = append(its, persist.FlowIntent{
			FlowID:     it.FlowID,
			Seq:        it.Seq,
			Intent:     it.Intent,
			Confidence: it.Confidence,
			NeedsBody:  it.NeedsBody,
		})
	}
	if err := w.UpsertFlowIntents(its); err != nil {
		log.Printf("ai: 意图结果持久化失败: %v", err)
	}
}

// flowToAIInput 归档流 → AI 送审输入；wantBody 时惰性回查库正文并解传输编码
// （Header 原样传入，脱敏在 ai.BuildPrompt 渲染时做）。
func flowToAIInput(w *persist.Writer, f *capture.Flow, wantReq, wantResp bool) ai.FlowInput {
	in := ai.FlowInput{FlowID: f.ID}
	if f.Timing != nil {
		in.StartedAt = f.Timing.Start
		in.DurationMS = f.Timing.Duration.Milliseconds()
	}
	if f.Process != nil {
		in.Process = f.Process.Name
	}
	if m := f.Request; m != nil {
		in.Method = m.Method
		in.URL = m.URL
		in.ReqHeaders = m.Header
		in.ReqCT = m.Header.Get("Content-Type")
		if wantReq {
			in.ReqBody = aiSideBody(w, f.ID, "req", m)
		}
	}
	if m := f.Response; m != nil {
		in.StatusCode = m.StatusCode
		in.RespCT = m.Header.Get("Content-Type")
		if wantResp {
			in.RespBody = aiSideBody(w, f.ID, "resp", m)
		}
	}
	return in
}

// aiSideBody 单侧正文：优先内存快照，缺失则库回查（bodies 表 zstd 解压由 LoadBody 处理）；
// 统一解 gzip 等传输编码，失败退原文（文本性判断/二进制标注/截断由 ai.BuildPrompt 负责）。
func aiSideBody(w *persist.Writer, flowID, kind string, msg *capture.Message) []byte {
	if msg == nil {
		return nil
	}
	raw := msg.Body
	if len(raw) == 0 && w != nil {
		if b, err := w.LoadBody(flowID, kind); err == nil {
			raw = b
		}
	}
	if len(raw) == 0 {
		return nil
	}
	dec, err := capture.DecodeBody(msg.ContentEncoding, raw)
	if err != nil {
		return raw
	}
	return dec
}
