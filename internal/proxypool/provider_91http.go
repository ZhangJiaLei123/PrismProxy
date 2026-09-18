package proxypool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"prismproxy/internal/settings"
)

// 91HTTP v1 适配器（代理池设计 §5.4）。
// 厂商 HTTP client 三铁律（v3 阻断项）：
//  1. 强制直连：独立 Transport{Proxy:nil}，不吃 HTTP_PROXY 环境变量、不经本工具端口；
//  2. 独立超时：连接 5s、整体 10s；
//  3. 日志脱敏：只记 path 与 code/msg、条目数，URL 含 secret 的 query 永不打印。

// apiBase91HTTP 默认 API 基址（HTTP 明文为厂商现状，设计 §5.4 风险声明）
const apiBase91HTTP = "http://api.91http.com"

func init() {
	Register(VendorMeta{
		ID:               Vendor91HTTP,
		Name:             "91HTTP",
		SupportUserPass:  true,
		SupportWhitelist: true,
		SupportRegion:    true,
	}, New91HTTP)
}

type provider91HTTP struct {
	base string // 可注入（httptest 单测）
	hc   *http.Client
}

// New91HTTP 构造默认基址的 91HTTP 适配器
func New91HTTP() Provider { return new91HTTP(apiBase91HTTP) }

func new91HTTP(base string) *provider91HTTP {
	return &provider91HTTP{
		base: strings.TrimRight(base, "/"),
		hc: &http.Client{
			Transport: &http.Transport{
				Proxy: nil, // 铁律 1：强制直连
				DialContext: (&net.Dialer{
					Timeout: 5 * time.Second, // 铁律 2：连接 5s
				}).DialContext,
			},
			Timeout: 10 * time.Second, // 铁律 2：整体 10s
		},
	}
}

func (p *provider91HTTP) Vendor() VendorID { return Vendor91HTTP }

// get 发起 GET 并读回 body；日志只记 path（铁律 3，query 含 secret 不打印）
func (p *provider91HTTP) get(ctx context.Context, path string, query map[string]string) ([]byte, error) {
	u := p.base + path
	if len(query) > 0 {
		var sb strings.Builder
		sb.WriteString(u)
		sb.WriteByte('?')
		first := true
		for k, v := range query {
			if !first {
				sb.WriteByte('&')
			}
			first = false
			sb.WriteString(k)
			sb.WriteByte('=')
			sb.WriteString(urlQueryEscape(v))
		}
		u = sb.String()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		// 安全（审计 M1）：传输层错误是 *url.Error，其 Error() 含完整 URL（query 带 secret/trade_no），
		// 会经 lastErr 进入 PoolStatus→SSE/Wails 事件/CLI 与日志。必须剥离 RawQuery，只留操作与底层原因。
		safe := sanitizeURLError(err)
		log.Printf("proxypool/91http: GET %s 网络失败: %v", path, safe)
		return nil, fmt.Errorf("91http %s: %w", path, safe)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("91http %s: 读取响应失败: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("proxypool/91http: GET %s HTTP %d", path, resp.StatusCode)
		return nil, fmt.Errorf("91http %s: HTTP %d", path, resp.StatusCode)
	}
	return body, nil
}

// sanitizeURLError 剥离传输层错误中可能携带的完整 URL（query 含 secret/trade_no）。
// *url.Error.Error() 形如 `Get "http://host/path?secret=xxx": 原因`，这里只保留
// Op + 路径 + 底层 Err；非 url.Error 原样返回（DNS/超时等多被 url.Error 包裹，双保险）。
func sanitizeURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		target := ""
		if u, perr := url.Parse(ue.URL); perr == nil {
			target = u.Path // 丢弃 RawQuery
		}
		return fmt.Errorf("%s %s: %w", ue.Op, target, ue.Err)
	}
	return err
}

// urlQueryEscape 仅转义 query 值（避免引入 net/url 的 Encode 排序不确定性，值多为 ascii）
func urlQueryEscape(s string) string {
	r := strings.NewReplacer(
		"%", "%25", " ", "%20", "&", "%26", "=", "%3D", "#", "%23",
		"+", "%2B", "?", "%3F", "/", "%2F", "@", "%40", ":", "%3A",
		",", "%2C",
	)
	return r.Replace(s)
}

// authQuery 凭据公共参数
func authQuery(conf settings.ProxyProviderConf) map[string]string {
	return map[string]string{
		"trade_no": conf.TradeNo,
		"secret":   conf.Secret,
	}
}

// Fetch 提取代理 IP（固定 format=json&time=1&pw=1&protocol=1，设计 §5.4）
func (p *provider91HTTP) Fetch(ctx context.Context, conf settings.ProxyProviderConf, n int) (*FetchResult, error) {
	if n <= 0 {
		n = 1
	}
	q := authQuery(conf)
	q["num"] = strconv.Itoa(n)
	q["format"] = "json"
	q["time"] = "1"
	q["pw"] = "1"
	q["protocol"] = "1"
	if conf.ISP != "" && conf.ISP != "0" {
		q["isp"] = conf.ISP
	}
	if conf.Province != "" {
		q["province"] = conf.Province
	}
	if conf.City != "" {
		q["city"] = conf.City
	}
	if conf.FilterUsed {
		q["filter"] = "1"
	}
	body, err := p.get(ctx, "/v1/get-ip", q)
	if err != nil {
		return nil, err
	}
	fetchedAt := time.Now()
	entries, surplus, err := parseFetchBody(body, fetchedAt)
	if err != nil {
		log.Printf("proxypool/91http: get-ip 解析失败: %v", err)
		return nil, err
	}
	// 账密补全（设计 §5.4）：条目未带账密时回落配置手填；白名单模式不补
	if conf.AuthMode != settings.PoolAuthWhitelist {
		for i := range entries {
			if entries[i].Username == "" && conf.Username != "" {
				entries[i].Username = conf.Username
				entries[i].Password = conf.Password
			}
		}
	}
	log.Printf("proxypool/91http: get-ip 提取成功 %d 条", len(entries))
	return &FetchResult{Entries: entries, SurplusTotal: surplus, FetchedAt: fetchedAt}, nil
}

// AccountInfo 业务详情（仅按量套餐支持，设计 §2）
func (p *provider91HTTP) AccountInfo(ctx context.Context, conf settings.ProxyProviderConf) (*AccountInfo, error) {
	body, err := p.get(ctx, "/v1/order/get-info", authQuery(conf))
	if err != nil {
		return nil, err
	}
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("91http get-info: 响应解析失败: %w", err)
	}
	if !codeOK(env.Code) {
		log.Printf("proxypool/91http: get-info code=%d msg=%q", env.Code, env.Msg)
		return nil, fmt.Errorf("91http: %s", env.Msg)
	}
	var data map[string]any
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil, fmt.Errorf("91http get-info: data 解析失败: %w", err)
	}
	info := &AccountInfo{
		SurplusTotal: intOf(data, -1, "surplus_ip_quantity", "surplus", "remain"),
		AuthUsername: strOf(data, "auth_username", "username"),
		AuthPassword: strOf(data, "auth_password", "password"),
	}
	// white_ip 可能是 string 或 []string
	switch v := data["white_ip"].(type) {
	case string:
		info.WhitelistIP = v
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				info.WhitelistIP = s
			}
		}
	}
	return info, nil
}

// AddWhitelist 添加终端白名单
func (p *provider91HTTP) AddWhitelist(ctx context.Context, conf settings.ProxyProviderConf, ip string) error {
	q := authQuery(conf)
	q["add_ip"] = ip
	return p.simpleOK(ctx, "/v1/white-ip/add", q)
}

// DelWhitelist 删除终端白名单
func (p *provider91HTTP) DelWhitelist(ctx context.Context, conf settings.ProxyProviderConf, ip string) error {
	q := authQuery(conf)
	q["del_ip"] = ip
	return p.simpleOK(ctx, "/v1/white-ip/del", q)
}

// simpleOK 只判 code 的通用调用
func (p *provider91HTTP) simpleOK(ctx context.Context, path string, q map[string]string) error {
	body, err := p.get(ctx, path, q)
	if err != nil {
		return err
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("91http %s: 响应解析失败: %w", path, err)
	}
	if !codeOK(env.Code) {
		log.Printf("proxypool/91http: %s code=%d msg=%q", path, env.Code, env.Msg)
		return fmt.Errorf("91http: %s", env.Msg)
	}
	return nil
}

// ListWhitelist 白名单列表
func (p *provider91HTTP) ListWhitelist(ctx context.Context, conf settings.ProxyProviderConf) (*WhitelistInfo, error) {
	body, err := p.get(ctx, "/v1/white-ip/get", authQuery(conf))
	if err != nil {
		return nil, err
	}
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("91http white-ip/get: 响应解析失败: %w", err)
	}
	if !codeOK(env.Code) {
		return nil, fmt.Errorf("91http: %s", env.Msg)
	}
	var data map[string]any
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil, fmt.Errorf("91http white-ip/get: data 解析失败: %w", err)
	}
	out := &WhitelistInfo{}
	if arr, ok := data["white_list"].([]any); ok {
		for _, it := range arr {
			switch v := it.(type) {
			case string:
				out.IPs = append(out.IPs, v)
			case map[string]any:
				if s := strOf(v, "ip", "white_ip"); s != "" {
					out.IPs = append(out.IPs, s)
				}
			}
		}
	}
	return out, nil
}

// GetMyIP 查询当前公网出口 IP（白名单辅助，设计 §2 官方校验地址）
func (p *provider91HTTP) GetMyIP(ctx context.Context) (string, error) {
	body, err := p.get(ctx, "/v1/tool/get-my-ip", nil)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(body))
	if strings.HasPrefix(s, "{") {
		var m map[string]any
		if err := json.Unmarshal(body, &m); err == nil {
			if ip := strOf(m, "ip", "data", "my_ip"); ip != "" {
				return ip, nil
			}
			if d, ok := m["data"].(map[string]any); ok {
				if ip := strOf(d, "ip", "my_ip"); ip != "" {
					return ip, nil
				}
			}
		}
	}
	if net.ParseIP(s) != nil {
		return s, nil
	}
	return "", fmt.Errorf("91http get-my-ip: 无法识别的响应")
}

// codeOK 成功判定（设计 §2：不同接口示例出现 0 与 200 两种）
func codeOK(code int) bool { return code == 0 || code == 200 }

// ---------- 防御式解析（设计 §5.4，真实字段以真账号校准） ----------

// parseFetchBody 先按 JSON 解析，失败（非 JSON 形态）回退 text 切行
func parseFetchBody(body []byte, now time.Time) ([]ProxyEntry, int, error) {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return nil, -1, fmt.Errorf("91http get-ip: 响应为空")
	}
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		return parseFetchJSON(body, now)
	}
	return parseFetchText(s, now), -1, nil
}

// parseFetchJSON 列表键候选：data.list / data.proxy_list / data.ips / data 顶层数组
func parseFetchJSON(body []byte, now time.Time) ([]ProxyEntry, int, error) {
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, -1, fmt.Errorf("91http get-ip: JSON 解析失败: %w", err)
	}
	if !codeOK(env.Code) {
		log.Printf("proxypool/91http: get-ip code=%d msg=%q", env.Code, env.Msg)
		return nil, -1, fmt.Errorf("91http: %s", env.Msg)
	}
	surplus := -1
	var arr []json.RawMessage
	if len(env.Data) > 0 {
		ds := strings.TrimSpace(string(env.Data))
		if strings.HasPrefix(ds, "[") {
			_ = json.Unmarshal(env.Data, &arr)
		} else if strings.HasPrefix(ds, "{") {
			var data map[string]any
			if err := json.Unmarshal(env.Data, &data); err == nil {
				surplus = intOf(data, -1, "surplus_ip_quantity", "surplus", "remain_total")
				for _, key := range []string{"list", "proxy_list", "ips"} {
					if raw, ok := data[key]; ok {
						if rb, err := json.Marshal(raw); err == nil {
							if json.Unmarshal(rb, &arr) == nil && len(arr) > 0 {
								break
							}
						}
					}
				}
			}
		}
	}
	entries := make([]ProxyEntry, 0, len(arr))
	for _, raw := range arr {
		rs := strings.TrimSpace(string(raw))
		var e ProxyEntry
		var ok bool
		if strings.HasPrefix(rs, "\"") {
			var line string
			if json.Unmarshal(raw, &line) == nil {
				e, ok = parseTextLine(line, now)
			}
		} else if strings.HasPrefix(rs, "{") {
			var m map[string]any
			if json.Unmarshal(raw, &m) == nil {
				e, ok = parseEntryObject(m, now)
			}
		}
		if ok {
			e.Source = Vendor91HTTP
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return nil, surplus, fmt.Errorf("91http get-ip: 响应中无有效代理条目")
	}
	return entries, surplus, nil
}

// parseFetchText text 回退：按行切分，逐行保守解析
func parseFetchText(s string, now time.Time) []ProxyEntry {
	var entries []ProxyEntry
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if e, ok := parseTextLine(line, now); ok {
			e.Source = Vendor91HTTP
			entries = append(entries, e)
		}
	}
	return entries
}

// parseEntryObject 对象元素：ip/port、ip:port 合一、expire 多种键、账密多种键
func parseEntryObject(m map[string]any, now time.Time) (ProxyEntry, bool) {
	var e ProxyEntry
	host := strOf(m, "ip", "host", "proxy_ip")
	port := intOf(m, 0, "port", "proxy_port")
	// ip:port 合一候选（含 ip 字段本身带端口）
	for _, cand := range []string{host, strOf(m, "addr", "proxy", "ip_port", "ipPort")} {
		if strings.Contains(cand, ":") {
			if h, ps, err := net.SplitHostPort(cand); err == nil {
				if pv, err := strconv.Atoi(ps); err == nil {
					host, port = h, pv
					break
				}
			}
		}
	}
	e.Host, e.Port = host, port
	e.Username = strOf(m, "username", "account", "user")
	e.Password = strOf(m, "password", "pwd", "pass")
	e.Region = strOf(m, "region", "area")
	if e.Region == "" {
		prov, city := strOf(m, "province"), strOf(m, "city")
		e.Region = strings.TrimSpace(prov + " " + city)
	}
	e.ExpireAt = expiryOf(m, now)
	if e.Host == "" || e.Port <= 0 {
		return ProxyEntry{}, false
	}
	if rb, err := json.Marshal(m); err == nil {
		e.Raw = string(rb)
	}
	return e, true
}

// parseTextLine 扩展行：`ip:port` / `ip:port,expire,user,pwd` / 老版 `ip:port,地区,邮编,剩余秒数`。
// 按段数与位置保守解析，无法判定的段不臆造（设计 §5.4）。
func parseTextLine(line string, now time.Time) (ProxyEntry, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return ProxyEntry{}, false
	}
	segs := strings.Split(line, ",")
	for i := range segs {
		segs[i] = strings.TrimSpace(segs[i])
	}
	host, ps, err := net.SplitHostPort(segs[0])
	if err != nil {
		return ProxyEntry{}, false
	}
	port, err := strconv.Atoi(ps)
	if err != nil || port <= 0 {
		return ProxyEntry{}, false
	}
	e := ProxyEntry{Host: host, Port: port, Raw: line}
	switch len(segs) {
	case 1:
		// 仅 ip:port
	case 2:
		e.applySeg(segs[1], now)
	case 3:
		// ip:port,expire,remain 或 ip:port,user,pwd
		if isExpireSeg(segs[1]) {
			e.applySeg(segs[1], now)
			e.applySeg(segs[2], now)
		} else {
			e.Username, e.Password = segs[1], segs[2]
		}
	case 4:
		// 老版 ip:port,地区,邮编,剩余秒数（segs[3] 纯数字且 segs[1] 非时间）
		if isRemainSeg(segs[3]) && !isExpireSeg(segs[1]) {
			e.Region = segs[1]
			e.applySeg(segs[3], now)
		} else {
			// v1 ip:port,expire,user,pwd
			e.applySeg(segs[1], now)
			e.Username, e.Password = segs[2], segs[3]
		}
	default:
		// ≥5 段：只取首个时间特征段作 expire，其余不臆造
		for _, seg := range segs[1:] {
			if isExpireSeg(seg) {
				e.applySeg(seg, now)
				break
			}
		}
	}
	return e, true
}

// applySeg 按形态识别单段：绝对时间戳/时间串/剩余秒数/地区
func (e *ProxyEntry) applySeg(seg string, now time.Time) {
	if seg == "" {
		return
	}
	if t, ok := parseAbsTime(seg); ok {
		e.ExpireAt = t
		return
	}
	if isRemainSeg(seg) {
		if sec, err := strconv.Atoi(seg); err == nil && sec > 0 {
			e.ExpireAt = now.Add(time.Duration(sec) * time.Second)
			return
		}
	}
	if e.Region == "" {
		e.Region = seg
	}
}

// isExpireSeg 是否时间特征段（绝对时间或相对秒数）
func isExpireSeg(seg string) bool {
	if _, ok := parseAbsTime(seg); ok {
		return true
	}
	return isRemainSeg(seg)
}

// isRemainSeg 是否剩余秒数（正整数且小于 1e7，避免与 unix 秒混淆）
func isRemainSeg(seg string) bool {
	n, err := strconv.Atoi(seg)
	return err == nil && n > 0 && n < 10000000
}

// parseAbsTime 绝对时间：unix 秒（>=1e9）或 "2006-01-02 15:04:05"
func parseAbsTime(seg string) (time.Time, bool) {
	if n, err := strconv.ParseInt(seg, 10, 64); err == nil && n >= 1000000000 {
		return time.Unix(n, 0), true
	}
	if strings.Contains(seg, "-") {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", seg, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// expiryOf 对象 expire 候选：expire_time/expire/deadline（unix 秒或时间串）、ttl/remain/left（相对秒）
func expiryOf(m map[string]any, now time.Time) time.Time {
	if s := strOf(m, "expire_time", "expire", "deadline", "expire_time_str", "expire_str"); s != "" {
		if t, ok := parseAbsTime(s); ok {
			return t
		}
	}
	if sec := intOf(m, 0, "ttl", "remain", "left", "remain_sec"); sec > 0 {
		return now.Add(time.Duration(sec) * time.Second)
	}
	return time.Time{}
}

// strOf 多键候选取字符串（兼容 json.Number）
func strOf(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if t != "" {
					return t
				}
			case json.Number:
				return t.String()
			case float64:
				return strconv.FormatFloat(t, 'f', -1, 64)
			}
		}
	}
	return ""
}

// intOf 多键候选取 int（兼容字符串数字）
func intOf(m map[string]any, def int, keys ...string) int {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case float64:
				return int(t)
			case json.Number:
				if n, err := t.Int64(); err == nil {
					return int(n)
				}
			case string:
				if n, err := strconv.Atoi(t); err == nil {
					return n
				}
			}
		}
	}
	return def
}
