package proxypool

import "sort"

// 厂商注册表（设计 §5.3）：新增厂商 = 新增 adapter + init 注册一行。
// 全局只读使用（init 期写入，运行期只查），无需锁。

// Vendor91HTTP 首期接入厂商
const Vendor91HTTP VendorID = "91http"

type vendorEntry struct {
	meta    VendorMeta
	factory func() Provider
}

var registry = map[VendorID]vendorEntry{}

// Register 注册厂商（adapter init 调用；重复注册后写覆盖）
func Register(meta VendorMeta, factory func() Provider) {
	registry[meta.ID] = vendorEntry{meta: meta, factory: factory}
}

// Lookup 按厂商 id 构造 Provider；未注册返回 (nil, false)
func Lookup(id VendorID) (Provider, bool) {
	e, ok := registry[id]
	if !ok {
		return nil, false
	}
	return e.factory(), true
}

// VendorRegistered 厂商是否已注册（P3 装配层校验用；
// 不放 settings 包避免反向依赖，设计 §5.5）
func VendorRegistered(id VendorID) bool {
	_, ok := registry[id]
	return ok
}

// Vendors 全部已注册厂商元信息（按 id 排序，输出稳定）
func Vendors() []VendorMeta {
	out := make([]VendorMeta, 0, len(registry))
	for _, e := range registry {
		out = append(out, e.meta)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
