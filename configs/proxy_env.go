package configs

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// ApplyProxyToEnv 将程序配置的代理同步到进程环境变量，确保：
// - Go 侧的网络请求（net/http 等）能走代理
// - rod 首次下载浏览器（fetchup）时也能走代理
//
// 说明：
// - rawProxy 允许以下格式：
//   - http://user:pass@host:port
//   - http://host:port
//   - host:port
//   - user:pass@host:port
//
// - 复杂的 Chrome 原生字符串（如 "http=...;https=..."）无法可靠映射到环境变量，将返回错误。
func ApplyProxyToEnv(rawProxy string) error {
	proxyURL, err := normalizeProxyForEnv(rawProxy)
	if err != nil || proxyURL == "" {
		return err
	}

	// 兼容不同生态：同时设置大小写版本。
	_ = os.Setenv("HTTP_PROXY", proxyURL)
	_ = os.Setenv("http_proxy", proxyURL)
	_ = os.Setenv("HTTPS_PROXY", proxyURL)
	_ = os.Setenv("https_proxy", proxyURL)

	// 部分工具会读取 ALL_PROXY（虽然 net/http 主要看 HTTP(S)_PROXY）。
	_ = os.Setenv("ALL_PROXY", proxyURL)
	_ = os.Setenv("all_proxy", proxyURL)

	// 避免误把本地回环流量也走代理（对 manager / 本机调试非常重要）。
	// 仅在缺失时补齐，不覆盖用户已有的 NO_PROXY 配置。
	noProxy := firstNonEmpty(os.Getenv("NO_PROXY"), os.Getenv("no_proxy"))
	noProxy = mergeNoProxy(noProxy, []string{"localhost", "127.0.0.1"})
	_ = os.Setenv("NO_PROXY", noProxy)
	_ = os.Setenv("no_proxy", noProxy)

	return nil
}

func normalizeProxyForEnv(raw string) (string, error) {
	proxy := strings.TrimSpace(raw)
	if proxy == "" {
		return "", nil
	}

	// Chrome 的高级格式（如 http=...;https=...）无法直接映射到单一 URL。
	if !strings.Contains(proxy, "://") && strings.ContainsAny(proxy, "=;") {
		return "", fmt.Errorf("不支持的代理格式（环境变量不支持），请使用 http://host:port 或 http://user:pass@host:port")
	}

	// 无 scheme 时补 http://，以便 url.Parse 正确识别 userinfo 与 host:port。
	if !strings.Contains(proxy, "://") {
		proxy = "http://" + proxy
	}

	u, err := url.Parse(proxy)
	if err != nil {
		return "", fmt.Errorf("代理地址解析失败: %w", err)
	}

	if strings.TrimSpace(u.Host) == "" {
		// 兜底处理：某些异常输入会落到 Opaque
		u.Host = strings.TrimSpace(u.Opaque)
		u.Opaque = ""
	}
	if strings.TrimSpace(u.Host) == "" {
		return "", fmt.Errorf("代理地址缺少 host:port")
	}

	if _, _, err := net.SplitHostPort(u.Host); err != nil {
		return "", fmt.Errorf("代理地址 host:port 不合法（%s）: %w", u.Host, err)
	}

	// 环境变量中的代理 URL 不应该包含 path/query/fragment
	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""

	if strings.TrimSpace(u.Scheme) == "" {
		u.Scheme = "http"
	}

	return u.String(), nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func mergeNoProxy(existing string, additions []string) string {
	set := map[string]struct{}{}
	out := make([]string, 0, 8)

	for _, part := range strings.Split(existing, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if _, ok := set[p]; ok {
			continue
		}
		set[p] = struct{}{}
		out = append(out, p)
	}

	for _, a := range additions {
		p := strings.TrimSpace(a)
		if p == "" {
			continue
		}
		if _, ok := set[p]; ok {
			continue
		}
		set[p] = struct{}{}
		out = append(out, p)
	}

	return strings.Join(out, ",")
}
