package browser

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyAuthE2E(t *testing.T) {
	if os.Getenv("XHSMCP_E2E_PROXY") == "" {
		t.Skip("跳过：未设置 XHSMCP_E2E_PROXY")
	}

	chrome := findChromeBin()
	if chrome == "" {
		t.Skip("跳过：未找到可用的 Chrome/Chromium，可通过 ROD_BROWSER_BIN 指定")
	}

	const username = "user"
	const password = "pass"

	p := &authProxy{
		username: username,
		password: password,
	}
	proxyServer := httptest.NewServer(p)
	t.Cleanup(proxyServer.Close)

	proxyHost := strings.TrimPrefix(proxyServer.URL, "http://")
	proxyURL := "http://" + username + ":" + password + "@" + proxyHost

	b, err := NewBrowser(true,
		WithBinPath(chrome),
		WithUserDataDir(filepath.Join(t.TempDir(), "profile")),
		WithProxy(proxyURL),
	)
	if err != nil {
		t.Fatalf("NewBrowser 失败: %v", err)
	}
	t.Cleanup(b.Close)

	page := b.NewPage().Timeout(15 * time.Second)
	defer page.Close()

	// 使用 .invalid 域名避免真实 DNS 依赖；只要代理生效，就会走本地 proxyServer。
	target := "http://example.invalid/"
	page.MustNavigate(target)
	page.MustWaitLoad()

	bodyText := page.MustElement("body").MustText()
	if !strings.Contains(bodyText, "ok") {
		t.Fatalf("页面内容不符合预期，body=%q", bodyText)
	}

	if atomic.LoadInt32(&p.authorized) == 0 {
		t.Fatalf("未观测到代理授权成功请求，可能未触发/未处理代理认证")
	}
}

type authProxy struct {
	username string
	password string

	authorized int32
}

func (p *authProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 该测试仅覆盖 HTTP 代理的 407 Basic 认证流程。
	// Chrome 在使用 HTTP 代理时，会向代理发送绝对 URI 形式的请求：
	//   GET http://example.invalid/ HTTP/1.1
	// 并在认证通过后携带 Proxy-Authorization 头。
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte(p.username+":"+p.password))
	if r.Header.Get("Proxy-Authorization") != expected {
		w.Header().Set("Proxy-Authenticate", `Basic realm="Proxy Authorization"`)
		w.WriteHeader(http.StatusProxyAuthRequired)
		_, _ = fmt.Fprint(w, "proxy: not authorized")
		return
	}

	atomic.AddInt32(&p.authorized, 1)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "ok")
}

func findChromeBin() string {
	if p := strings.TrimSpace(os.Getenv("ROD_BROWSER_BIN")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	switch runtime.GOOS {
	case "windows":
		candidates := []string{
			filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		}
		for _, p := range candidates {
			if p == "" {
				continue
			}
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	case "linux":
		candidates := []string{
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium-browser",
			"/usr/bin/chromium",
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	case "darwin":
		candidates := []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}
