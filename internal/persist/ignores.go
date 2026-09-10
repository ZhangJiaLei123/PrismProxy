// ignores.go M12.2 数据复盘「忽略」名单：忽略仅在复盘查询时以 SQL 条件排除（隐藏），
// 不删除 flows 中的任何数据。五类：
//   - host：归一化去端口/小写/去尾点，匹配主机自身及其子域（f.host 可能带 :port）；
//   - path：去 query/fragment、确保 / 开头，匹配精确路径及其下级路径（f.path 本身无 query）；
//   - proc：进程名 trim 原样存储，忽略大小写等值匹配（进程名在 data JSON 的 $.Process.Name）；
//   - method：HTTP 方法大写 trim，忽略大小写等值匹配（隧道流 method=CONNECT）；
//   - status：HTTP 状态码三位整数字符串，等值匹配（f.status=0 的无响应/错误流不被误伤）。
//
// 名单存于归档库 review_ignores 表（由共享 migrate 建表）。只读连接（OpenReadOnly）
// 不跑 migrate，旧库可能无此表——读侧一律容错为空名单，不报错。
package persist

import (
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// ReviewIgnoreKind 忽略类型。
type ReviewIgnoreKind string

const (
	ReviewIgnoreHost   ReviewIgnoreKind = "host"
	ReviewIgnorePath   ReviewIgnoreKind = "path"
	ReviewIgnoreProc   ReviewIgnoreKind = "proc"
	ReviewIgnoreMethod ReviewIgnoreKind = "method"
	ReviewIgnoreStatus ReviewIgnoreKind = "status"
)

// ValidIgnoreKind 校验忽略类型。
func ValidIgnoreKind(k string) bool {
	switch ReviewIgnoreKind(k) {
	case ReviewIgnoreHost, ReviewIgnorePath, ReviewIgnoreProc, ReviewIgnoreMethod, ReviewIgnoreStatus:
		return true
	}
	return false
}

// ReviewIgnore 名单项（复盘接口 DTO，小写字段，与 TagInfo 同口径）。
type ReviewIgnore struct {
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	CreatedAt int64  `json:"createdAt"`
	Note      string `json:"note"`
}

// ReviewListOpts 复盘列表可选参数（关键字 + 排序 + 是否显示被忽略数据；
// 零值=无关键字、按时间倒序、隐藏忽略项）。
type ReviewListOpts struct {
	Q           string // 关键字（method/host/path 子串匹配）
	SortKey     string // time|method|status|host|path|size|proc，空/非法回落 time
	SortDir     string // asc|desc，空回落：time=desc，其余列=asc
	ShowIgnored bool   // true=不拼忽略排除条件（眼睛开启，显示全部）
}

// reviewQueryOpts 内部查询参数（q 关键字 + 忽略名单 + 排序），buildFlowQuery 用。
type reviewQueryOpts struct {
	q           string
	ignores     []ReviewIgnore
	sortKey     string
	sortDir     string
	showIgnored bool
}

// likeEscape 仅转义 LIKE 元字符（\ % _），不补两端 %（供精确前缀模式自行拼接）。
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// NormalizeIgnoreHost 忽略域名归一化：去端口（f.host/ServerAddr 可能带 :port）、
// 小写、去尾点、去 *. 前缀；空串返回 ""（由调用方拒绝）。
func NormalizeIgnoreHost(h string) string {
	h = strings.TrimSpace(h)
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.ToLower(h)
	h = strings.TrimSuffix(h, ".")
	h = strings.TrimPrefix(h, "*.")
	return h
}

// NormalizeIgnorePath 忽略路径归一化：去空白、去 query/fragment、确保 / 开头。
// "/" 等价匹配全部路径，忽略场景拒绝；ok=false 表示不可用。
func NormalizeIgnorePath(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", false
	}
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if p == "/" {
		return "", false
	}
	return p, true
}

// NormalizeIgnoreProc 进程名归一化：仅 trim（保留原始大小写显示；匹配忽略大小写）。
func NormalizeIgnoreProc(p string) string {
	return strings.TrimSpace(p)
}

// NormalizeIgnoreMethod 方法归一化：trim + 大写（保留显示；匹配忽略大小写）。
func NormalizeIgnoreMethod(m string) string {
	return strings.ToUpper(strings.TrimSpace(m))
}

// NormalizeIgnoreStatus 状态码归一化：trim 后须为 100–599 的三位整数；ok=false 表示非法。
// 存储仍为字符串（与 review_ignores.value 同列），SQL 比较时与 f.status 数字列比较。
func NormalizeIgnoreStatus(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 3 {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	if n < 100 || n > 599 {
		return 0, false
	}
	return n, true
}

// NormalizeIgnoreValue 按类型归一化待加入名单的值；非法/空返回错误。
func NormalizeIgnoreValue(kind, value string) (string, error) {
	switch ReviewIgnoreKind(kind) {
	case ReviewIgnoreHost:
		v := NormalizeIgnoreHost(value)
		if v == "" {
			return "", fmt.Errorf("域名不能为空")
		}
		return v, nil
	case ReviewIgnorePath:
		v, ok := NormalizeIgnorePath(value)
		if !ok {
			return "", fmt.Errorf("路径无效（不能为空或仅 /）")
		}
		return v, nil
	case ReviewIgnoreProc:
		v := NormalizeIgnoreProc(value)
		if v == "" {
			return "", fmt.Errorf("进程名不能为空")
		}
		return v, nil
	case ReviewIgnoreMethod:
		v := NormalizeIgnoreMethod(value)
		if v == "" {
			return "", fmt.Errorf("方法不能为空")
		}
		return v, nil
	case ReviewIgnoreStatus:
		v, ok := NormalizeIgnoreStatus(value)
		if !ok {
			return "", fmt.Errorf("状态码无效（须为 100–599 的三位整数）")
		}
		return strconv.Itoa(v), nil
	default:
		return "", fmt.Errorf("非法忽略类型（仅支持 host|path|proc|method|status）")
	}
}

// isNoSuchTable 现代c SQLite 对缺表返回 "no such table: ..."；只读旧库无 review_ignores 时容错。
func isNoSuchTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// ListReviewIgnores 返回全部忽略项（按创建时间倒序）。
// 旧只读库无表时返回空切片、不报错（读侧容错）。
func (w *Writer) ListReviewIgnores() ([]ReviewIgnore, error) {
	rows, err := w.db.Query(
		`SELECT kind, value, created_at, COALESCE(note,'') FROM review_ignores ORDER BY created_at DESC, kind, value`)
	if err != nil {
		if isNoSuchTable(err) {
			return []ReviewIgnore{}, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := make([]ReviewIgnore, 0)
	for rows.Next() {
		var it ReviewIgnore
		if err := rows.Scan(&it.Kind, &it.Value, &it.CreatedAt, &it.Note); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// AddReviewIgnore 归一化后加入名单（已存在则保留原记录），返回是否新增（added=false 表示已存在）。
// 须在可写归档 Writer 上调用（App 层经 acquireArchive 保证）。
func (w *Writer) AddReviewIgnore(kind, value, note string) (ReviewIgnore, bool, error) {
	if !ValidIgnoreKind(kind) {
		return ReviewIgnore{}, false, fmt.Errorf("非法忽略类型（仅支持 host|path|proc|method|status）")
	}
	v, err := NormalizeIgnoreValue(kind, value)
	if err != nil {
		return ReviewIgnore{}, false, err
	}
	now := time.Now().UnixMilli()
	// 先查存在性（不能用 created_at==now 判定新增——历史记录可能恰好同毫秒）。
	var createdAt int64
	added := false
	getErr := w.db.QueryRow(
		`SELECT created_at FROM review_ignores WHERE kind=? AND value=?`, kind, v).Scan(&createdAt)
	switch {
	case getErr == sql.ErrNoRows:
		if _, err := w.db.Exec(
			`INSERT INTO review_ignores (kind, value, created_at, note) VALUES (?,?,?,?)`,
			kind, v, now, note); err != nil {
			return ReviewIgnore{}, false, err
		}
		createdAt, added = now, true
	case getErr != nil:
		return ReviewIgnore{}, false, getErr
	default:
		// 已存在：仅刷新 note，created_at 保持首次时间。
		if _, err := w.db.Exec(
			`UPDATE review_ignores SET note=? WHERE kind=? AND value=?`, note, kind, v); err != nil {
			return ReviewIgnore{}, false, err
		}
	}
	return ReviewIgnore{Kind: kind, Value: v, CreatedAt: createdAt, Note: note}, added, nil
}

// DeleteReviewIgnore 删除一条忽略项，返回是否实际删除（不存在返回 false）。
func (w *Writer) DeleteReviewIgnore(kind, value string) (bool, error) {
	if !ValidIgnoreKind(kind) {
		return false, fmt.Errorf("非法忽略类型（仅支持 host|path|proc|method|status）")
	}
	// 值按存储口径比较：host/proc 大小写不敏感需先归一化，path 直接比较；
	// 为稳妥统一要求删除时传已归一化值（接口层做归一化），这里仅校验非空。
	v := strings.TrimSpace(value)
	if v == "" {
		return false, fmt.Errorf("忽略值不能为空")
	}
	res, err := w.db.Exec(`DELETE FROM review_ignores WHERE kind=? AND value=?`, kind, v)
	if err != nil {
		if isNoSuchTable(err) {
			return false, nil
		}
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ignoreConds 把忽略名单翻译为 SQL 排除条件（每条名单一个 NOT(...) 正向匹配，
// 整体 AND）。返回追加的条件与参数。空名单/显式显示全部时不追加。
func ignoreConds(ignores []ReviewIgnore) (conds []string, args []any) {
	for _, ig := range ignores {
		switch ReviewIgnoreKind(ig.Kind) {
		case ReviewIgnoreHost:
			h := NormalizeIgnoreHost(ig.Value)
			if h == "" {
				continue
			}
			// f.host 写的是 ServerAddr，可能为 host 或 host:port。正向命中自身或任意层级子域：
			//   host            等值
			//   host:%          自身带任意端口
			//   %.host          任意层级子域（SQLite LIKE 整串锚定，% 可跨点）
			//   %.host:%        任意层级子域带端口
			// 四类取 NOT 交集即为排除；host 转义 LIKE 元字符。
			eh := likeEscape(h)
			conds = append(conds,
				`(LOWER(f.host)<>?
				  AND LOWER(f.host) NOT LIKE ? ESCAPE '\'
				  AND LOWER(f.host) NOT LIKE ? ESCAPE '\'
				  AND LOWER(f.host) NOT LIKE ? ESCAPE '\')`)
			args = append(args, h, eh+":%", "%."+eh, "%."+eh+":%")
		case ReviewIgnorePath:
			p, ok := NormalizeIgnorePath(ig.Value)
			if !ok {
				continue
			}
			e := likeEscape(p)
			// 精确路径 或 其下级路径（p/ 前缀）；NOT LIKE 参数用 e 与 e+"/%"。
			conds = append(conds, `(f.path<>? AND f.path NOT LIKE ? ESCAPE '\')`)
			args = append(args, p, e+"/%")
		case ReviewIgnoreProc:
			p := NormalizeIgnoreProc(ig.Value)
			if p == "" {
				continue
			}
			// 进程名在 data JSON：$.Process.Name；COALESCE 兜空（未知进程不被误伤）。
			conds = append(conds,
				`(LOWER(COALESCE(json_extract(f.data,'$.Process.Name'),''))<>LOWER(?))`)
			args = append(args, p)
		case ReviewIgnoreMethod:
			m := NormalizeIgnoreMethod(ig.Value)
			if m == "" {
				continue
			}
			// 方法大小写不敏感等值匹配（f.method 落库即原始方法，CONNECT 隧道流一并隐藏）；
			// method 列允许 NULL，COALESCE 兜空串避免 NULL 比较三值逻辑误伤未知方法行。
			conds = append(conds, `(LOWER(COALESCE(f.method,''))<>LOWER(?))`)
			args = append(args, m)
		case ReviewIgnoreStatus:
			code, ok := NormalizeIgnoreStatus(ig.Value)
			if !ok {
				continue
			}
			// f.status 为 NOT NULL 数字列，0=无响应/错误流；等值排除该状态码，0 与任何
			// 100–599 不等故无响应流不被误伤。
			conds = append(conds, `(f.status<>?)`)
			args = append(args, code)
		}
	}
	return conds, args
}

// flowOrderBy 返回复盘列表 ORDER BY 片段（已含表别名 f 前缀与 f.id/f.started_at 兜底）。
// 未知列回落 started_at；dir 显式 asc/desc 优先；空 dir 时时间列默认 desc、其余列默认 asc
// （与前端表头首次点击排序口径一致）。
func flowOrderBy(key, dir string) string {
	isTime := key == "" || key == "time"
	d := "ASC"
	if isTime {
		d = "DESC"
	}
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "asc":
		d = "ASC"
	case "desc":
		d = "DESC"
	}
	var col string
	switch key {
	case "method":
		col = "LOWER(f.method)"
	case "status":
		col = "f.status"
	case "host":
		// SQLite 默认 BINARY 排序按字节（大写 < 小写），文本列包 LOWER 做大小写不敏感排序。
		col = "LOWER(f.host)"
	case "path":
		col = "LOWER(f.path)"
	case "size":
		// BytesDown 在完整 Flow JSON 内（无独立列）
		col = "json_extract(f.data,'$.BytesDown')"
	case "proc":
		col = "LOWER(json_extract(f.data,'$.Process.Name'))"
	default:
		col = "f.started_at"
	}
	// 非时间列：同值以最新时间在前兜底；时间列以 id 兜底，保证分页稳定。
	if isTime {
		return " ORDER BY f.started_at " + d + ", f.id DESC"
	}
	return " ORDER BY " + col + " " + d + ", f.started_at DESC, f.id DESC"
}
