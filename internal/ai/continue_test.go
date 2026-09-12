package ai

// continue_test.go：截断自动续写的纯函数 Prompt 构造 + AutoContinue 编排单测。
// 编排用伪 StreamRunner 按「系统提示词是否含压缩助手标识」区分压缩调用与回答调用，
// 不发起任何真实 HTTP。

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeContinueRunner 脚本化 StreamRunner：
// answers=回答/续写调用依次返回的脚本（按序消费）；compressor=压缩调用的统一应答。
type fakeContinueRunner struct {
	answers         []fakeStream
	compressor      fakeStream
	nAnswer         int
	nCompress       int
	lastCompressUser string // 最近一次压缩会话的 user 消息（断言再压缩历史链路用）
}

type fakeStream struct {
	fin  string
	text string
	err  error
}

func (f *fakeContinueRunner) Stream(ctx context.Context, messages []Message, onDelta func(Delta)) (string, Usage, error) {
	isCompress := len(messages) > 0 && strings.Contains(messages[0].Content, "你是会话压缩助手")
	if isCompress {
		f.nCompress++
		if len(messages) >= 2 {
			f.lastCompressUser = messages[1].Content
		}
		s := f.compressor
		if s.err != nil {
			return s.fin, Usage{}, s.err
		}
		if s.text != "" {
			onDelta(Delta{Text: s.text})
		}
		return s.fin, Usage{PromptTokens: 100, CompletionTokens: 50}, nil
	}
	// 回答/续写调用：按脚本顺序消费
	idx := f.nAnswer
	f.nAnswer++
	if idx >= len(f.answers) {
		t := "默认续写收尾"
		onDelta(Delta{Text: t})
		return "stop", Usage{PromptTokens: 200, CompletionTokens: 80}, nil
	}
	s := f.answers[idx]
	if s.err != nil {
		return s.fin, Usage{}, s.err
	}
	if s.text != "" {
		onDelta(Delta{Text: s.text})
	}
	return s.fin, Usage{PromptTokens: 200, CompletionTokens: 80}, s.err
}

// TestBuildCompressMessages 压缩会话消息：首轮带原系统/原用户/截断稿，
// intent 模式含 intents 登记口径；第 2 轮历史标签切换为「上一轮压缩记忆」。
func TestBuildCompressMessages(t *testing.T) {
	msgs := BuildCompressMessages(ModeIntent, "原SYS", "原USER", "半截回答", 1)
	if len(msgs) != 2 || msgs[0].Role != RoleSystem || msgs[1].Role != RoleUser {
		t.Fatalf("压缩会话应为 system+user 两条: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "你是会话压缩助手") {
		t.Fatal("系统消息应为压缩助手人格")
	}
	if !strings.Contains(msgs[0].Content, "intents") || !strings.Contains(msgs[0].Content, "seq + flowId") {
		t.Fatal("intent 模式应要求登记 seq+flowId: ", msgs[0].Content)
	}
	u := msgs[1].Content
	for _, want := range []string{"【原系统提示词】", "原SYS", "【历史用户消息】", "原USER", "【被截断的模型回答】", "半截回答"} {
		if !strings.Contains(u, want) {
			t.Fatalf("压缩 user 消息应含 %q: %s", want, u)
		}
	}

	msgs2 := BuildCompressMessages(ModeLocate, "原SYS", "记忆ROUND1", "续写半截", 2)
	if !strings.Contains(msgs2[0].Content, "matches") {
		t.Fatal("locate 模式应要求登记 matches 条目")
	}
	if !strings.Contains(msgs2[1].Content, "【上一轮压缩记忆") || !strings.Contains(msgs2[1].Content, "记忆ROUND1") {
		t.Fatal("第 2 轮应以上一轮记忆为历史用户消息: ", msgs2[1].Content)
	}

	// explain/flowmap 不要求登记 JSON
	msgs3 := BuildCompressMessages(ModeExplain, "s", "u", "p", 1)
	if !strings.Contains(msgs3[0].Content, "写「无」") {
		t.Fatal("非 JSON 模式应明确结构化条目写无")
	}
}

// TestBuildContinueMessages 续写会话：原系统提示词保留 + 续写规则注入 + 记忆为唯一历史。
func TestBuildContinueMessages(t *testing.T) {
	// intent：要求重出完整 JSON 块
	msgs := BuildContinueMessages(ModeIntent, "你是资深接口逆向助手", "记忆正文XYZ", 1)
	if len(msgs) != 2 {
		t.Fatalf("续写会话应两条消息: %d", len(msgs))
	}
	sys, usr := msgs[0].Content, msgs[1].Content
	if !strings.HasPrefix(sys, "你是资深接口逆向助手") {
		t.Fatal("原系统提示词应在最前保留")
	}
	for _, want := range []string{"【续写规则】", "完整、可解析", "全部】条目", "flowId"} {
		if !strings.Contains(sys, want) {
			t.Fatalf("intent 续写规则应含 %q: %s", want, sys)
		}
	}
	if !strings.Contains(usr, "【历史对话压缩记忆】") || !strings.Contains(usr, "记忆正文XYZ") {
		t.Fatalf("续写 user 应以压缩记忆为历史: %s", usr)
	}

	// locate 模式 JSON 口径为 matches
	if msgs2 := BuildContinueMessages(ModeLocate, "S", "M", 1); !strings.Contains(msgs2[0].Content, "matches") {
		t.Fatal("locate 续写规则应提 matches")
	}
	// explain：不要求 JSON 块，要求补全围栏
	sysEx := BuildContinueMessages(ModeExplain, "S", "M", 1)[0].Content
	if strings.Contains(sysEx, "重新输出一个完整") || !strings.Contains(sysEx, "围栏") {
		t.Fatalf("explain 续写规则不应要求重出 JSON、应提围栏: %s", sysEx)
	}
	// 第 2 轮追加「确保收尾」铁律
	sys2 := BuildContinueMessages(ModeFlowmap, "S", "M", 2)[0].Content
	if !strings.Contains(sys2, "第 2 次续写") || !strings.Contains(sys2, "确保本次收尾") {
		t.Fatalf("第 2 轮应加强制收尾规则: %s", sys2)
	}
}

// TestClipForCompression 裁剪：短文本原样；超长保头保尾 + 中段省略说明；切点不产生乱码。
func TestClipForCompression(t *testing.T) {
	short := "短回答"
	if got := clipForCompression(short); got != short {
		t.Fatalf("未超预算应原样返回: %q", got)
	}
	long := strings.Repeat("甲乙丙丁", 6000) // 24000 字节，超 14336 预算
	got := clipForCompression(long)
	if !strings.Contains(got, "已省略") {
		t.Fatal("超长应含省略说明")
	}
	if !strings.HasPrefix(got, "甲乙丙丁") {
		t.Fatal("应保留开头")
	}
	if !strings.HasSuffix(got, "甲乙丙丁") {
		t.Fatal("应保留结尾")
	}
	// 头尾预算字符都是 3 字节 rune（中文），切点必须落在 rune 边界（无 RuneError 乱码）
	if strings.Contains(got, "\uFFFD") {
		t.Fatalf("裁剪切点产生乱码: %q", got)
	}
	if len(got) >= len(long) {
		t.Fatal("裁剪后应明显短于原文")
	}
}

// TestAutoContinueSingleRound 首轮 length → 压缩 → 续写 stop：
// Rounds=1、续写增量经 onDelta 透传、阶段回调齐备、token 合计=压缩+续写。
func TestAutoContinueSingleRound(t *testing.T) {
	// AutoContinue 只发续写调用：唯一一次续写即 stop（首轮回答 length 由入参 partial 模拟）
	runner := &fakeContinueRunner{
		answers:    []fakeStream{{fin: "stop", text: "续写剩余部分"}},
		compressor: fakeStream{fin: "stop", text: "结构化记忆"},
	}
	var stages []string
	var gotText strings.Builder
	res := AutoContinue(context.Background(), runner, ModeExplain, "SYS", "USER", "首轮截断稿",
		func(stage string, round int) { stages = append(stages, stage) },
		func(d Delta) { gotText.WriteString(d.Text) })
	if res.Rounds != 1 || res.FinishReason != "stop" {
		t.Fatalf("应 1 轮续写后 stop: %+v", res)
	}
	if res.FailedStage != "" || res.FailError != nil {
		t.Fatalf("不应失败: stage=%s err=%v", res.FailedStage, res.FailError)
	}
	if gotText.String() != "续写剩余部分" {
		t.Fatalf("续写增量应透传: %q", gotText.String())
	}
	if len(stages) != 2 || stages[0] != StageCompressing || stages[1] != StageContinuing {
		t.Fatalf("阶段回调应为 compressing→continuing: %v", stages)
	}
	if runner.nCompress != 1 || runner.nAnswer != 1 {
		t.Fatalf("应 1 次压缩 1 次续写: compress=%d answer=%d", runner.nCompress, runner.nAnswer)
	}
	// token 合计 = 压缩(100/50) + 续写(200/80)
	if res.Usage.PromptTokens != 300 || res.Usage.CompletionTokens != 130 {
		t.Fatalf("token 应合计: %+v", res.Usage)
	}
}

// TestAutoContinueTwoRounds 续写后仍 length：第 2 轮以「上轮记忆+续写新产出」再压缩，
// 第 2 次续写 stop → Rounds=2。
func TestAutoContinueTwoRounds(t *testing.T) {
	runner := &fakeContinueRunner{
		// AutoContinue 只发续写调用：answers[0]=第 1 次续写仍 length，answers[1]=第 2 次 stop
		answers: []fakeStream{
			{fin: FinishReasonLength, text: "续写一仍截断"},
			{fin: "stop", text: "续写二收尾"},
		},
		compressor: fakeStream{fin: "stop", text: "记忆"},
	}
	var stages []string
	var gotText strings.Builder
	res := AutoContinue(context.Background(), runner, ModeFlowmap, "SYS", "USER", "首轮截断稿",
		func(stage string, round int) { stages = append(stages, stage+":"+itoa(round)) },
		func(d Delta) { gotText.WriteString(d.Text) })
	if res.Rounds != 2 || res.FinishReason != "stop" {
		t.Fatalf("应 2 轮续写后 stop: %+v", res)
	}
	if runner.nCompress != 2 {
		t.Fatalf("应触发 2 次压缩会话: %d", runner.nCompress)
	}
	if gotText.String() != "续写一仍截断续写二收尾" {
		t.Fatalf("两轮续写增量应顺序拼接: %q", gotText.String())
	}
	wantStages := map[string]bool{"compressing:1": false, "continuing:1": false, "compressing:2": false, "continuing:2": false}
	for _, s := range stages {
		if _, ok := wantStages[s]; ok {
			wantStages[s] = true
		}
	}
	for s, seen := range wantStages {
		if !seen {
			t.Fatalf("缺少阶段回调 %s，实际 %v", s, stages)
		}
	}
	// 第 2 轮压缩的历史用户消息应替换为上一轮记忆（原始抓包不再回灌）
	if !strings.Contains(runner.lastCompressUser, "【上一轮压缩记忆】") {
		t.Fatalf("第二轮压缩入参应为上轮记忆: %s", runner.lastCompressUser)
	}
}

// TestAutoContinueRoundsExhausted 两轮续写都 length：Rounds=2、FinishReason 仍 length，
// 交回调用方保留截断告警。
func TestAutoContinueRoundsExhausted(t *testing.T) {
	runner := &fakeContinueRunner{
		answers: []fakeStream{
			{fin: FinishReasonLength, text: "续1"},
			{fin: FinishReasonLength, text: "续2"},
		},
		compressor: fakeStream{fin: "stop", text: "记忆"},
	}
	res := AutoContinue(context.Background(), runner, ModeExplain, "s", "u", "p", nil, nil)
	if res.Rounds != maxContinueRounds || res.FinishReason != FinishReasonLength {
		t.Fatalf("耗尽轮次应保留 length: %+v", res)
	}
	if runner.nCompress != maxContinueRounds {
		t.Fatalf("应压缩 %d 次: %d", maxContinueRounds, runner.nCompress)
	}
}

// TestAutoContinueCompressFailure 压缩会话失败：FailedStage=compressing（非致命），
// 不再发起续写。
func TestAutoContinueCompressFailure(t *testing.T) {
	runner := &fakeContinueRunner{
		answers:    []fakeStream{{fin: "stop", text: "不应被调用"}},
		compressor: fakeStream{err: errors.New("压缩服务 500")},
	}
	res := AutoContinue(context.Background(), runner, ModeExplain, "s", "u", "p", nil,
		func(d Delta) { t.Fatal("压缩失败后不应有续写增量") })
	if res.FailedStage != StageCompressing || res.FailError == nil {
		t.Fatalf("应报告压缩阶段失败: %+v", res)
	}
	if runner.nAnswer != 0 {
		t.Fatalf("压缩失败不应发起续写: %d", runner.nAnswer)
	}
}

// TestAutoContinueContinueFailure 续写流失败：FailedStage=continuing，Rounds 记 1。
func TestAutoContinueContinueFailure(t *testing.T) {
	runner := &fakeContinueRunner{
		answers:    []fakeStream{{err: errors.New("续写连接中断")}},
		compressor: fakeStream{fin: "stop", text: "记忆"},
	}
	res := AutoContinue(context.Background(), runner, ModeExplain, "s", "u", "p", nil, nil)
	if res.FailedStage != StageContinuing || res.FailError == nil || res.Rounds != 1 {
		t.Fatalf("应报告续写阶段失败且记 1 轮: %+v", res)
	}
}

// TestAutoContinueEmptyGuard 空截断稿（纯思考耗尽等）不触发续写。
func TestAutoContinueEmptyGuard(t *testing.T) {
	runner := &fakeContinueRunner{}
	res := AutoContinue(context.Background(), runner, ModeExplain, "s", "u", "   \n\t ", nil, nil)
	if res.Rounds != 0 || res.FinishReason != "" {
		t.Fatalf("空稿不应续写: %+v", res)
	}
	if runner.nCompress != 0 || runner.nAnswer != 0 {
		t.Fatalf("空稿不应发起任何调用")
	}
}

// TestAutoContinueContextCanceled 进入续写前 ctx 已取消：静默返回 ctx 错误、不发调用。
func TestAutoContinueContextCanceled(t *testing.T) {
	runner := &fakeContinueRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := AutoContinue(ctx, runner, ModeExplain, "s", "u", "p", nil, nil)
	if res.FailedStage != "" || !errors.Is(res.FailError, context.Canceled) {
		t.Fatalf("ctx 取消应静默返回: %+v", res)
	}
}

// itoa 测试内极简整数转串（避免引 strconv 仅为一处断言）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
