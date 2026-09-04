// Package rules 规则引擎：捕获/解密/进程三层（方案 §4.7）。
// 匹配语义：每层规则为有序列表，自上而下首条命中即生效；
// 全部未命中走默认动作（捕获默认 include、解密默认 MITM、进程默认 include）。
package rules

import (
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
)

// Action 规则动作。捕获/进程层用 include|exclude；解密层用 mitm|bypass
type Action string

const (
	ActionInclude Action = "include"
	ActionExclude Action = "exclude"
	ActionMITM    Action = "mitm"
	ActionBypass  Action = "bypass"
)

// CaptureRule 捕获规则：命中 exclude 则正常转发但不记录（屏蔽遥测/心跳噪声）
type CaptureRule struct {
	Action Action `json:"action"`
	Host   string `json:"host"`   // 域名模式（裸域名=自身+全部子域；@组名 引用域名组；空=任意）
	URLRe  string `json:"urlRe"`  // URL 正则（空=任意）
	Method string `json:"method"` // HTTP 方法（空=任意；多值逗号分隔）
}

// DecryptRule 解密规则：命中 bypass 则 CONNECT 盲透传（应对 SSL Pinning）
type DecryptRule struct {
	Action Action `json:"action"` // mitm | bypass
	Host   string `json:"host"`
}

// ProcessRule 进程规则：按进程名 include/exclude（不区分大小写）
type ProcessRule struct {
	Action Action `json:"action"`
	Name   string `json:"name"` // 如 dnplayer.exe；精确匹配（不区分大小写）
}

// Engine 编译后的规则集合（并发只读；整体替换式热更新）
type Engine struct {
	capture []captureCompiled
	decrypt []DecryptRule
	process []ProcessRule
	groups  map[string][]string
}

type captureCompiled struct {
	CaptureRule
	re *regexp.Regexp
}

// NewEngine 编译三层规则；groups 为域名组（@组名 引用），可为 nil
func NewEngine(capRules []CaptureRule, decRules []DecryptRule, procRules []ProcessRule, groups map[string][]string) (*Engine, error) {
	e := &Engine{decrypt: decRules, process: procRules, groups: groups}
	for i, r := range capRules {
		cc := captureCompiled{CaptureRule: r}
		if r.URLRe != "" {
			re, err := regexp.Compile(r.URLRe)
			if err != nil {
				return nil, fmt.Errorf("捕获规则 #%d URL 正则编译失败: %w", i+1, err)
			}
			cc.re = re
		}
		if err := validateAction(r.Action, ActionInclude, ActionExclude); err != nil {
			return nil, fmt.Errorf("捕获规则 #%d: %w", i+1, err)
		}
		e.capture = append(e.capture, cc)
	}
	for i, r := range decRules {
		if err := validateAction(r.Action, ActionMITM, ActionBypass); err != nil {
			return nil, fmt.Errorf("解密规则 #%d: %w", i+1, err)
		}
		if r.Host == "" {
			return nil, fmt.Errorf("解密规则 #%d: host 不能为空", i+1)
		}
	}
	for i, r := range procRules {
		if err := validateAction(r.Action, ActionInclude, ActionExclude); err != nil {
			return nil, fmt.Errorf("进程规则 #%d: %w", i+1, err)
		}
		if r.Name == "" {
			return nil, fmt.Errorf("进程规则 #%d: name 不能为空", i+1)
		}
	}
	return e, nil
}

func validateAction(a Action, allowed ...Action) error {
	for _, x := range allowed {
		if a == x {
			return nil
		}
	}
	return fmt.Errorf("非法动作 %q", a)
}

// ShouldCapture 捕获判定：默认 include
func (e *Engine) ShouldCapture(host, rawURL, method string) bool {
	if e == nil {
		return true
	}
	for _, r := range e.capture {
		if r.Host != "" && !e.hostMatch(r.Host, host) {
			continue
		}
		if r.Method != "" && !methodMatch(r.Method, method) {
			continue
		}
		if r.re != nil && !r.re.MatchString(rawURL) {
			continue
		}
		return r.Action == ActionInclude
	}
	return true
}

// ShouldDecrypt 解密判定（CONNECT host 维度）：默认 MITM
func (e *Engine) ShouldDecrypt(host string) bool {
	if e == nil {
		return true
	}
	for _, r := range e.decrypt {
		if e.hostMatch(r.Host, host) {
			return r.Action == ActionMITM
		}
	}
	return true
}

// ShouldCaptureProcess 进程判定：默认 include（进程未知视为未命中）
func (e *Engine) ShouldCaptureProcess(name string) bool {
	if e == nil {
		return true
	}
	for _, r := range e.process {
		if name != "" && strings.EqualFold(r.Name, name) {
			return r.Action == ActionInclude
		}
	}
	return true
}

// hostMatch 域名匹配：裸域名匹配自身+全部子域；*.x.com 等价裸域名；@组名 引用域名组
func (e *Engine) hostMatch(pattern, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" || host == "" {
		return false
	}
	if strings.HasPrefix(pattern, "@") {
		for _, d := range e.groups[strings.TrimPrefix(pattern, "@")] {
			if bareMatch(d, host) {
				return true
			}
		}
		return false
	}
	return bareMatch(strings.TrimPrefix(pattern, "*."), host)
}

func bareMatch(domain, host string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func methodMatch(pattern, method string) bool {
	for _, m := range strings.Split(pattern, ",") {
		if strings.EqualFold(strings.TrimSpace(m), method) {
			return true
		}
	}
	return false
}

// Holder 引擎热更新容器（保存设置时整体替换，读侧零锁）
type Holder struct{ p atomic.Pointer[Engine] }

func (h *Holder) Get() *Engine  { return h.p.Load() }
func (h *Holder) Set(e *Engine) { h.p.Store(e) }
