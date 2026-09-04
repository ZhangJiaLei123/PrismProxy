// Package mitm 证书中心：Root CA 生成/持久化 + 叶子证书动态签发（方案 §4.2）
package mitm

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	caCertFile = "prism-ca.pem"
	caKeyFile  = "prism-key.pem"
	// caCommonName 展示在系统/浏览器证书列表里的名字
	caCommonName   = "Prism Root CA"
	caOrganization = "PrismProxy"
	caValidYears   = 10 // 方案 §4.2：Root CA 有效期 10 年
)

// CA 根证书与私钥，用于动态签发叶子证书
type CA struct {
	Cert    *x509.Certificate
	Key     *rsa.PrivateKey
	CertDER []byte // 供叶子证书链条目与 /ca 下载
	CertPEM []byte
	dir     string
}

// LoadOrCreateCA 从 dir 加载 CA；不存在则生成并持久化（私钥 ACL 限当前用户）
func LoadOrCreateCA(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	certPath := filepath.Join(dir, caCertFile)
	keyPath := filepath.Join(dir, caKeyFile)

	certPEM, err1 := os.ReadFile(certPath)
	keyPEM, err2 := os.ReadFile(keyPath)
	if err1 == nil && err2 == nil {
		ca, err := parseCA(certPEM, keyPEM)
		if err == nil {
			ca.dir = dir
			return ca, nil
		}
		// 损坏则重新生成（旧文件备份，避免丢信任链后无迹可查）
		_ = os.Rename(certPath, certPath+".bak")
		_ = os.Rename(keyPath, keyPath+".bak")
	}

	ca, err := createCA()
	if err != nil {
		return nil, err
	}
	ca.dir = dir
	keyPEMBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(ca.Key)})
	if err := os.WriteFile(certPath, ca.CertPEM, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEMBytes, 0o600); err != nil {
		return nil, err
	}
	hardenKeyACL(keyPath) // Windows 下 0600 语义弱，用 icacls 收紧到当前用户
	return ca, nil
}

// CertPEMPath / KeyPEMPath 供 /ca 下载页使用
func (c *CA) CertPEMPath() string { return filepath.Join(c.dir, caCertFile) }

func createCA() (*CA, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate ca key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: caCommonName, Organization: []string{caOrganization}},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().AddDate(caValidYears, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create ca cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{
		Cert:    cert,
		Key:     key,
		CertDER: der,
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}, nil
}

func parseCA(certPEM, keyPEM []byte) (*CA, error) {
	cb, _ := pem.Decode(certPEM)
	if cb == nil {
		return nil, errors.New("bad ca cert pem")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, errors.New("bad ca key pem")
	}
	key, err := x509.ParsePKCS1PrivateKey(kb.Bytes)
	if err != nil {
		return nil, err
	}
	if time.Now().After(cert.NotAfter) {
		return nil, errors.New("ca expired")
	}
	return &CA{Cert: cert, Key: key, CertDER: cb.Bytes, CertPEM: certPEM}, nil
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}

// TLSCertificate 把 CA 包成 tls.Certificate（备用，如未来做 tls.Config 兜底证书）
func (c *CA) TLSCertificate() tls.Certificate {
	return tls.Certificate{
		Certificate: [][]byte{c.CertDER},
		PrivateKey:  c.Key,
		Leaf:        c.Cert,
	}
}
