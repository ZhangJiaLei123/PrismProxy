package proxy

import (
	"fmt"
	"net/http"
)

// handleCA 内置 CA 分发页（方案 §4.2 三通道之一：http://代理地址/ca）
func (s *Server) handleCA(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/ca.pem":
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Content-Disposition", `attachment; filename="prism-ca.pem"`)
		_, _ = w.Write(s.caPEM)
	case "/ca.der", "/ca.cer":
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="prism-ca.cer"`)
		_, _ = w.Write(s.caDER)
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, caPageHTML)
	}
}

const caPageHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head><meta charset="utf-8"><title>PrismProxy 根证书</title>
<style>body{font-family:system-ui,sans-serif;max-width:720px;margin:40px auto;padding:0 16px;line-height:1.7}
code,pre{background:#f4f4f4;padding:2px 6px;border-radius:4px}
pre{padding:12px;overflow-x:auto}.btn{display:inline-block;margin:4px 8px 4px 0;padding:8px 16px;background:#0b5fff;color:#fff;border-radius:6px;text-decoration:none}</style>
</head>
<body>
<h1>Prism Root CA</h1>
<p>安装并<strong>信任</strong>此根证书后，PrismProxy 才能解密 HTTPS 流量。</p>
<p>
<a class="btn" href="/ca.cer">下载证书 (DER / .cer)</a>
<a class="btn" href="/ca.pem">下载证书 (PEM)</a>
</p>
<h2>Windows</h2>
<pre>certutil -addstore -user -f Root prism-ca.cer   :: 当前用户，免管理员
certutil -addstore -f Root prism-ca.cer         :: 全机生效，需管理员</pre>
<p>注意：Windows 版 curl（schannel 后端）默认强制吊销检查，本 CA 无 CRL 分发点，
请求时请加 <code>--ssl-no-revoke</code>；浏览器与 .NET 等系统默认栈不受影响。</p>
<h2>Android 模拟器（雷电）</h2>
<pre>adb push prism-ca.cer /sdcard/Download/
# 设置 → 安全 → 加密与凭据 → 安装证书 → CA 证书</pre>
<p>用户证书仅影响浏览器等部分应用；抓全部 App 需系统证书（root 后挂入 /system/etc/security/cacerts/）。</p>
<h2>iOS</h2>
<p>描述文件安装后，还需 <code>设置 → 通用 → 关于本机 → 证书信任设置</code> 中手动开启完全信任。</p>
</body>
</html>`
