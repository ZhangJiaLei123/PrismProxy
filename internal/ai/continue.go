// continue.go：输出截断（finish_reason=length）自动续写。
//
// 链路（用户拍板：临时专用会话压缩 → 新会话续写）：
//  1. 首轮流式回答被上游以 length 截断（正文已有部分输出）；
//  2. 开一个临时「压缩会话」：把原系统提示词 + 历史用户消息 + 被截断的回答
//     压成结构化「记忆」（任务目标/已完成内容/已输出 JSON 条目/中断位置/续写清单）；
//  3. 开「续写会话」：原系统提示词 + 续写规则 + 压缩记忆作为唯一上下文，直接输出剩余部分，
//     delta 经调用方走原帧通道无缝拼到首轮正文之后；
//  4. 续写仍被 length 截断则再来一轮（以上一轮记忆 + 续写新产出为历史再压缩），最多 2 轮。
//
// 本文件纯编排+纯函数，零项目内依赖；StreamRunner 由 *Client 天然满足，单测可注入伪实现。
package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// FinishReasonLength 上游结束原因：输出预算耗尽被截断（OpenAI 兼容约定）。
	FinishReasonLength = "length"

	// StageCompressing / StageContinuing 续写阶段名（经 notice 事件透传前端做进度提示）。
	StageCompressing = "compressing" // 临时压缩会话进行中
	StageContinuing  = "continuing"  // 压缩完成，续写会话流式输出中

	// maxContinueRounds 续写最大轮数（首轮之外最多 2 次压缩+2 次续写），
	// 封顶费用与耗时；耗尽仍 length 则如实保留 length 由调用方上报告警。
	maxContinueRounds = 2

	// 压缩会话输入中「被截断回答」的裁剪预算：该回答可能长达成品输出上限
	//（8K tokens 量级），原样回灌会挤占压缩会话自身的上下文窗口。
	// 压缩记忆最需要的是「写了什么（结构）」与「停在哪里（尾部）」——
	// 保头 4KB（章节/结论骨架）+ 保尾 10KB（断点细节，含半截 JSON），中段省略。
	compressHeadBudget = 4096
	compressTailBudget = 10240
)

// StreamRunner 流式对话能力（*Client 天然满足）：抽接口仅供续写编排单测注入伪实现。
type StreamRunner interface {
	Stream(ctx context.Context, messages []Message, onDelta func(Delta)) (finishReason string, usage Usage, err error)
}

// AutoContinueResult 续写编排结果。
type AutoContinueResult struct {
	FinishReason string // 续写末轮结束原因（stop/length/…）；Rounds=0 时未执行续写
	Rounds       int    // 实际完成的续写轮数（1..maxContinueRounds）
	Usage        Usage  // 压缩+续写全部附加调用的 token 合计（上游不支持统计时零值）
	// FailedStage 非空表示压缩或续写阶段出错（非致命：调用方保留已流出的截断稿，
	// 经 notice failed 告知后仍按 length 收尾）；ctx 取消时 FailedStage 为空。
	FailedStage string
	FailError   error
}

// AutoContinue 检测到 length 截断后的自动续写入口。origSystem=首轮系统提示词；
// origUser=首轮用户消息；partial=首轮被截断的正文（必须非空）。
// onStage 在每次压缩/续写开始时回调（调用方转发 notice 帧）；
// onDelta 透传续写会话的增量（调用方写入同一正文缓冲并转发 delta 帧）。
// 压缩会话自身的思考增量（reason）不外显——它只是临时工具会话。
func AutoContinue(
	ctx context.Context,
	cli StreamRunner,
	mode ChatMode,
	origSystem, origUser, partial string,
	onStage func(stage string, round int),
	onDelta func(Delta),
) AutoContinueResult {
	if strings.TrimSpace(partial) == "" {
		return AutoContinueResult{} // 无正文（纯思考耗尽预算等）：不续写，交回调用方按 length 收尾
	}
	var sum Usage
	historyUser := origUser
	historyPartial := partial
	fin := FinishReasonLength
	for round := 1; round <= maxContinueRounds; round++ {
		if err := ctx.Err(); err != nil {
			return AutoContinueResult{FailError: err} // 用户停止/断连：调用方静默收尾
		}

		// 1) 临时专用压缩会话：历史对话 → 结构化记忆（非流式用途，借 Stream 拿首块看门狗）
		if onStage != nil {
			onStage(StageCompressing, round)
		}
		compMsgs := BuildCompressMessages(mode, origSystem, historyUser, historyPartial, round)
		var memSB strings.Builder
		_, cu, err := cli.Stream(ctx, compMsgs, func(d Delta) {
			memSB.WriteString(d.Text) // 压缩会话的 reason 增量有意忽略
		})
		if err != nil {
			if ctx.Err() != nil {
				return AutoContinueResult{FailError: ctx.Err()}
			}
			return AutoContinueResult{Rounds: round - 1, Usage: sum, FailedStage: StageCompressing, FailError: err}
		}
		sum.PromptTokens += cu.PromptTokens
		sum.CompletionTokens += cu.CompletionTokens
		memory := strings.TrimSpace(memSB.String())
		if memory == "" {
			return AutoContinueResult{
				Rounds:       round - 1,
				Usage:        sum,
				FailedStage:  StageCompressing,
				FailError:    errors.New("压缩会话未返回有效内容"),
			}
		}

		// 2) 新会话续写：原系统提示词 + 续写规则，压缩记忆作为唯一历史上下文
		if onStage != nil {
			onStage(StageContinuing, round)
		}
		contMsgs := BuildContinueMessages(mode, origSystem, memory, round)
		var contSB strings.Builder
		cfin, u2, err := cli.Stream(ctx, contMsgs, func(d Delta) {
			contSB.WriteString(d.Text)
			if onDelta != nil {
				onDelta(d)
			}
		})
		if err != nil {
			if ctx.Err() != nil {
				return AutoContinueResult{FailError: ctx.Err()}
			}
			return AutoContinueResult{Rounds: round, Usage: sum, FailedStage: StageContinuing, FailError: err}
		}
		sum.PromptTokens += u2.PromptTokens
		sum.CompletionTokens += u2.CompletionTokens
		newText := contSB.String()
		if cfin == "" {
			cfin = "stop" // 上游不给 finish_reason 的兜底（与首轮同口径）
		}
		fin = cfin
		if cfin != FinishReasonLength {
			return AutoContinueResult{FinishReason: fin, Rounds: round, Usage: sum}
		}
		// 仍被截断：下一轮以「上一轮记忆 + 本轮续写新产出」为历史再压缩，
		// 原始抓包提示词不再回灌（已在首轮记忆中浓缩），逐轮瘦身。
		if strings.TrimSpace(newText) == "" {
			break // 续写零产出仍报 length：再压缩无意义，直接退出保留告警
		}
		historyUser = "【上一轮压缩记忆】\n" + memory
		historyPartial = newText
	}
	return AutoContinueResult{FinishReason: fin, Rounds: maxContinueRounds, Usage: sum}
}

// ---------- 压缩会话 Prompt ----------

// compressSystemBase 压缩会话系统提示词：要求输出固定五小节的结构化工作记忆。
const compressSystemBase = "你是会话压缩助手。一次接口抓包 AI 分析对话的回答因模型输出长度上限" +
	"（finish_reason=length）在中途被截断，需要开启新会话续写。你的唯一任务：把历史对话压缩成" +
	"供新模型无缝续写的工作记忆。\n" +
	"铁律：\n" +
	"- 只依据给定内容压缩，禁止编造未出现的接口、flowId、参数、状态码与结论；\n" +
	"- 记忆是给模型看的工作底稿：直接输出记忆正文，不要寒暄、不要解释你的任务；\n" +
	"- 关键标识保留原文：flowId、[#n] 编号、HTTP 方法、URL 路径、状态码、参数名；\n" +
	"- 严格按以下小节输出，无内容的小节写「无」：\n" +
	"## 任务目标\n一句话说明原任务（分析模式与用户目标）。\n" +
	"## 已完成内容\n按原文顺序精炼已输出的要点、结论与已覆盖的章节。\n" +
	"## 已输出的结构化条目\n逐条登记已出现在文末 ```json 块中的条目标识（不得遗漏，供续写去重）。\n" +
	"## 中断位置\n原回答最后停在哪个章节/句子/条目；代码块或 JSON 是否已闭合。\n" +
	"## 续写清单\n还剩哪些内容必须输出（剩余章节、剩余条目、待补全的 JSON）。\n" +
	"- 总长度控制在 1200 字以内。"

// compressSystem 追加模式专属的 JSON 登记口径。
func compressSystem(mode ChatMode) string {
	switch mode {
	case ModeIntent:
		return compressSystemBase + "\n本次原模式为 intent：文末 JSON 形如 {\"intents\":[...]}，" +
			"「已输出的结构化条目」必须逐条登记 seq + flowId + 意图短句。"
	case ModeLocate:
		return compressSystemBase + "\n本次原模式为 locate：文末 JSON 形如 {\"matches\":[...]}，" +
			"「已输出的结构化条目」必须逐条登记 rank + flowId + 理由。"
	default:
		return compressSystemBase + "\n本次原模式不输出结构化 JSON，「已输出的结构化条目」写「无」。"
	}
}

// BuildCompressMessages 构造临时压缩会话的消息。
// historyUser：首轮=原用户消息；第 2 轮=上一轮压缩记忆。
// partial：上一轮被截断的回答正文（超长时保头保尾裁剪中段，见 clipForCompression）。
func BuildCompressMessages(mode ChatMode, origSystem, historyUser, partial string, round int) []Message {
	histLabel := "【历史用户消息】"
	if round > 1 {
		histLabel = "【上一轮压缩记忆（原始抓包数据已浓缩其中，不再重发）】"
	}
	user := "【原系统提示词】\n" + strings.TrimSpace(origSystem) + "\n\n" +
		histLabel + "\n" + strings.TrimSpace(historyUser) + "\n\n" +
		"【被截断的模型回答】（以下内容在中途终止，并非完整回答）\n" +
		clipForCompression(partial) +
		"\n\n请按系统消息要求的五小节输出结构化压缩记忆。"
	return []Message{
		{Role: RoleSystem, Content: compressSystem(mode)},
		{Role: RoleUser, Content: user},
	}
}

// ---------- 续写会话 Prompt ----------

// continueRules 追加在原系统提示词之后的续写铁律（模式感知：JSON 模式要求重出完整块）。
func continueRules(mode ChatMode, round int) string {
	b := strings.Builder{}
	b.WriteString("\n\n【续写规则】你正在接续一个因输出长度上限被截断的回答，用户消息只包含" +
		"历史对话的压缩记忆（原始抓包数据不再重复发送）。必须遵守：\n" +
		"1. 直接输出原回答尚未完成的剩余部分，与截断处无缝衔接；\n" +
		"2. 禁止重复「已完成内容」中已覆盖的文字；禁止开场白、寒暄与元话语" +
		"（不得出现「好的」「接着上面」「上文提到」等）；\n" +
		"3. 沿用原回答的中文 Markdown 风格、编号与小节层级；\n" +
		"4. 只使用压缩记忆中的事实，禁止编造记忆里没有的接口、flowId 与结论。\n")
	switch mode {
	case ModeIntent:
		b.WriteString("5. 原文末的 ```json 意图数组被截断：你必须重新输出一个完整、可解析的 ```json " +
			"代码块，包含【全部】条目——「已输出的结构化条目」中登记过的条目按登记内容原样保留，" +
			"再补齐剩余条目；seq 连续、flowId 必须使用记忆中的原值；人类可读的 `- [#n]` 列表" +
			"只补尚未输出的条目，不要重复。\n")
	case ModeLocate:
		b.WriteString("5. 原文末的 ```json matches 数组被截断：你必须重新输出一个完整、可解析的 " +
			"```json 代码块，包含【全部】候选——已输出项按记忆原样保留，再补齐余项，rank 从 1 连续；" +
			"Markdown 分析部分只补尚未写完的段落。\n")
	default:
		b.WriteString("5. 若中断发生在代码块、表格或 Mermaid 图内部，先补齐必要的围栏或行列使其完整，" +
			"再继续后续内容。\n")
	}
	if round > 1 {
		fmt.Fprintf(&b, "6. 这是第 %d 次续写且上一次续写仍被截断：只输出最关键的剩余内容，"+
			"砍掉次要展开，确保本次收尾结束。\n", round)
	}
	return b.String()
}

// BuildContinueMessages 构造续写会话的消息：原系统提示词（含续写规则）+ 压缩记忆。
func BuildContinueMessages(mode ChatMode, origSystem, memory string, round int) []Message {
	system := strings.TrimRight(origSystem, "\n") + continueRules(mode, round)
	user := "【历史对话压缩记忆】\n" + strings.TrimSpace(memory) +
		"\n\n请严格按续写规则，直接输出原回答的剩余内容："
	return []Message{
		{Role: RoleSystem, Content: system},
		{Role: RoleUser, Content: user},
	}
}

// clipForCompression 超长截断回答的输入裁剪：保头（结构骨架）+ 保尾（断点细节），
// 中段以省略说明替代；切点对齐 UTF-8 字符边界，避免切坏多字节字符。
func clipForCompression(s string) string {
	if len(s) <= compressHeadBudget+compressTailBudget {
		return s
	}
	head := cutRunes(s, compressHeadBudget, true)
	tail := cutRunes(s, compressTailBudget, false)
	omitted := len(s) - len(head) - len(tail)
	return head +
		fmt.Sprintf("\n\n…（中段 %d 字节已省略：压缩只需结构与断点，原文不重发）…\n\n", omitted) +
		tail
}

// cutRunes 从字符串头部（head=true）或尾部（head=false）取至多 limit 字节，
// 切点回退到最后一个完整 rune 边界。
func cutRunes(s string, limit int, head bool) string {
	if len(s) <= limit {
		return s
	}
	if head {
		cut := s[:limit]
		if r, size := utf8.DecodeLastRuneInString(cut); r == utf8.RuneError {
			cut = cut[:len(cut)-size]
		}
		return cut
	}
	cut := s[len(s)-limit:]
	if r, size := utf8.DecodeRuneInString(cut); r == utf8.RuneError {
		cut = cut[size:]
	}
	return cut
}
