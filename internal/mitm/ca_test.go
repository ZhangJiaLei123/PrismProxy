package mitm

import (
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLoadOrCreateCA(t *testing.T) {
	dir := t.TempDir()

	ca1, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !ca1.Cert.IsCA {
		t.Fatal("not a CA cert")
	}
	if got := ca1.Cert.NotAfter.Sub(ca1.Cert.NotBefore); got < 9*365*24*3600*1e9 {
		t.Errorf("ca validity too short: %v", got)
	}
	for _, f := range []string{caCertFile, caKeyFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s not persisted: %v", f, err)
		}
	}

	// 再次加载应命中同一张证书（持久化生效，而非重新生成）
	ca2, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if ca1.Cert.SerialNumber.Cmp(ca2.Cert.SerialNumber) != 0 {
		t.Error("reload produced different CA (persistence broken)")
	}
}

func TestSignLeafAndVerify(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	cache := NewCertCache(ca)

	// DNS 叶子证书
	dc, err := cache.Get("www.example.com")
	if err != nil {
		t.Fatalf("sign dns leaf: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	if _, err := dc.Leaf.Verify(x509.VerifyOptions{DNSName: "www.example.com", Roots: roots}); err != nil {
		t.Errorf("dns leaf verify: %v", err)
	}
	if len(dc.Leaf.DNSNames) != 1 || dc.Leaf.DNSNames[0] != "www.example.com" {
		t.Errorf("dns SAN = %v", dc.Leaf.DNSNames)
	}
	if dc.Leaf.NotAfter.Sub(dc.Leaf.NotBefore) > 366*24*3600*1e9 {
		t.Errorf("leaf validity exceeds 1 year")
	}

	// IP 叶子证书（SAN 必须填 IPAddresses 而非 DNSNames）
	ic, err := cache.Get("192.168.1.1")
	if err != nil {
		t.Fatalf("sign ip leaf: %v", err)
	}
	if len(ic.Leaf.IPAddresses) != 1 || !ic.Leaf.IPAddresses[0].Equal(net.ParseIP("192.168.1.1")) {
		t.Errorf("ip SAN = %v", ic.Leaf.IPAddresses)
	}
	if len(ic.Leaf.DNSNames) != 0 {
		t.Errorf("ip leaf should not carry DNS SAN, got %v", ic.Leaf.DNSNames)
	}
	if _, err := ic.Leaf.Verify(x509.VerifyOptions{DNSName: "192.168.1.1", Roots: roots}); err != nil {
		t.Errorf("ip leaf verify: %v", err)
	}
}

func TestCertCacheSingleflight(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	cache := NewCertCache(ca)

	// 并发取同一 host：全部成功且返回同一证书指针（签发只发生一次）
	const n = 32
	results := make([]any, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := cache.Get("concurrent.example.com")
			if err != nil {
				t.Errorf("get: %v", err)
				return
			}
			results[i] = c
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if results[i] != results[0] {
			t.Fatal("concurrent Get returned different certificates (singleflight broken)")
		}
	}
}

func TestCertCacheLRU(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	cache := NewCertCache(ca)

	// 容量 512：再签 512 个不同 host 后，最早未再访问的 host 应被淘汰
	first, err := cache.Get("host-0000.example.com")
	if err != nil {
		t.Fatalf("sign first: %v", err)
	}
	for i := 1; i <= cacheCap; i++ {
		if _, err := cache.Get(fmt.Sprintf("host-%04d.example.com", i)); err != nil {
			t.Fatalf("sign %d: %v", i, err)
		}
	}
	cache.mu.Lock()
	_, alive := cache.items["host-0000.example.com"]
	live := len(cache.items)
	cache.mu.Unlock()
	if alive {
		t.Error("oldest entry not evicted after exceeding capacity")
	}
	if live != cacheCap {
		t.Errorf("cache size = %d, want %d", live, cacheCap)
	}

	// 淘汰后重新签发应得到新证书（不同私钥）
	again, err := cache.Get("host-0000.example.com")
	if err != nil {
		t.Fatalf("re-sign: %v", err)
	}
	if again == first {
		t.Error("evicted host returned stale cached certificate")
	}
}
