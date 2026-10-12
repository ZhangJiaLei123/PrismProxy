package app

// tray.go：serve 托盘的状态快照与文本格式（平台无关；D5，架构3.0设计 §4.4）。
// Windows 实现 tray_windows.go，其余平台 tray_other.go 空实现。

import "fmt"

// trayStateMaxName 项目名截断上限：托盘 tooltip 总长上限 128 字符（NOTIFYICONDATA
// szTip），留出版本/地址/措辞余量后项目名最长 32 字符。
const trayStateMaxName = 32

// trayState 托盘展示的状态快照（弱一致读：GetProxyStatus 与 currentMeta 各自锁内
// 取值，拼装不要求跨锁原子——托盘展示允许瞬时不一致）。
type trayState struct {
	projectOn   bool
	projectName string
	proxyOn     bool
	proxyAddr   string
	startErr    string
}

func (a *App) trayStateNow() trayState {
	st := a.GetProxyStatus()
	meta := a.currentMeta() // 弱一致读（调用方不持 projMu 时的许可读法）
	return trayState{
		projectOn:   a.currentID() != "",
		projectName: meta.Name,
		proxyOn:     st.Running,
		proxyAddr:   st.Addr,
		startErr:    st.StartError,
	}
}

// truncateName 项目名按 rune 截断（按字节切 UTF-8 会产生乱码；托盘 tooltip 总长
// 上限 128 字符，留出版本/地址/措辞余量后项目名最长 32 rune）
func truncateName(name string) string {
	rs := []rune(name)
	if len(rs) > trayStateMaxName {
		return string(rs[:trayStateMaxName]) + "…"
	}
	return name
}

// line 托盘菜单首行状态文本（tooltip 复用同一行）
func (s trayState) line() string {
	proj := "未打开项目"
	if s.projectOn {
		proj = "项目 " + truncateName(s.projectName)
	}
	switch {
	case s.proxyOn:
		return fmt.Sprintf("%s · 代理 %s 运行中", proj, s.proxyAddr)
	case s.startErr != "":
		return fmt.Sprintf("%s · 代理启动失败", proj)
	default:
		return fmt.Sprintf("%s · 代理已停止", proj)
	}
}
