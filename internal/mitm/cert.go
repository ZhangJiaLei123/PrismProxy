package mitm

import (
	"container/list"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	leafValidDays = 365 // 方案 §4.2：叶子证书有效期 ≤1 年
	cacheCap      = 512 // LRU 容量：超出淘汰最久未用证书
)

// CertCache 按 host 动态签发并缓存叶子证书（LRU + singleflight）
type CertCache struct {
	ca *CA

	mu       sync.Mutex
	items    map[string]*list.Element // host -> lru 节点
	lru      *list.List               // front = 最近使用
	inflight map[string]*sfCall       // 签发中去重
}

type cacheEntry struct {
	host string
	cert *tls.Certificate
}

// sfCall singleflight：同一 host 的并发签发只执行一次
type sfCall struct {
	done chan struct{}
	cert *tls.Certificate
	err  error
}

func NewCertCache(ca *CA) *CertCache {
	return &CertCache{
		ca:       ca,
		items:    make(map[string]*list.Element),
		lru:      list.New(),
		inflight: make(map[string]*sfCall),
	}
}

// Get 取 host 的叶子证书：缓存命中直接返回；未命中签发（并发去重）
func (c *CertCache) Get(host string) (*tls.Certificate, error) {
	c.mu.Lock()
	if el, ok := c.items[host]; ok {
		c.lru.MoveToFront(el)
		cert := el.Value.(*cacheEntry).cert
		c.mu.Unlock()
		return cert, nil
	}
	if call, ok := c.inflight[host]; ok {
		c.mu.Unlock()
		<-call.done
		return call.cert, call.err
	}
	call := &sfCall{done: make(chan struct{})}
	c.inflight[host] = call
	c.mu.Unlock()

	call.cert, call.err = c.sign(host)
	close(call.done)

	c.mu.Lock()
	delete(c.inflight, host)
	if call.err == nil {
		c.putLocked(host, call.cert)
	}
	c.mu.Unlock()
	return call.cert, call.err
}

func (c *CertCache) putLocked(host string, cert *tls.Certificate) {
	el := c.lru.PushFront(&cacheEntry{host: host, cert: cert})
	c.items[host] = el
	for c.lru.Len() > cacheCap {
		tail := c.lru.Back()
		c.lru.Remove(tail)
		delete(c.items, tail.Value.(*cacheEntry).host)
	}
}

// sign 用 CA 签发叶子证书：ECDSA P-256，SAN 按 IP/DNS 正确填写
func (c *CertCache) sign(host string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate leaf key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host, Organization: []string{caOrganization}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(0, 0, leafValidDays),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.ca.Cert, &key.PublicKey, c.ca.Key)
	if err != nil {
		return nil, fmt.Errorf("sign leaf for %s: %w", host, err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{
		Certificate: [][]byte{der, c.ca.CertDER},
		PrivateKey:  key,
		Leaf:        leaf,
	}, nil
}
