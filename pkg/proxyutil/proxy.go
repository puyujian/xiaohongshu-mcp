package proxyutil

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func Resolve(ctx context.Context, staticProxy, proxyPoolURL string) (string, error) {
	proxyPoolURL = strings.TrimSpace(proxyPoolURL)
	if proxyPoolURL == "" {
		return strings.TrimSpace(staticProxy), nil
	}

	proxy, err := FetchFromPool(ctx, proxyPoolURL)
	if err != nil {
		return "", fmt.Errorf("代理池提取失败: %w", err)
	}
	return proxy, nil
}

func FetchFromPool(ctx context.Context, proxyPoolURL string) (string, error) {
	reqCtx := ctx
	if reqCtx == nil {
		reqCtx = context.Background()
	}
	timeoutCtx, cancel := context.WithTimeout(reqCtx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodGet, proxyPoolURL, nil)
	if err != nil {
		return "", fmt.Errorf("代理池地址无效: %w", err)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("读取代理池响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("代理池返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return ParsePoolResponse(string(body))
}

func ParsePoolResponse(raw string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		for _, candidate := range splitCandidates(line) {
			if IsValidProxy(candidate) {
				return candidate, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("扫描代理池响应失败: %w", err)
	}
	return "", fmt.Errorf("代理池响应中未找到可用代理")
}

func NormalizeHTTPProxy(raw string) (*url.URL, error) {
	proxy := strings.TrimSpace(raw)
	if proxy == "" {
		return nil, nil
	}
	if !strings.Contains(proxy, "://") && strings.ContainsAny(proxy, "=;") {
		return nil, fmt.Errorf("不支持的代理格式，请使用 http://host:port 或 http://user:pass@host:port")
	}
	if !strings.Contains(proxy, "://") {
		proxy = "http://" + proxy
	}

	u, err := url.Parse(proxy)
	if err != nil {
		return nil, fmt.Errorf("代理地址解析失败: %w", err)
	}
	if strings.TrimSpace(u.Host) == "" {
		u.Host = strings.TrimSpace(u.Opaque)
		u.Opaque = ""
	}
	if strings.TrimSpace(u.Host) == "" {
		return nil, fmt.Errorf("代理地址缺少 host:port")
	}
	if _, _, err := net.SplitHostPort(u.Host); err != nil {
		return nil, fmt.Errorf("代理地址 host:port 不合法（%s）: %w", u.Host, err)
	}

	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""
	if strings.TrimSpace(u.Scheme) == "" {
		u.Scheme = "http"
	}
	return u, nil
}

func SanitizeForLog(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		if u, err := url.Parse(value); err == nil {
			if u.User != nil {
				u.User = url.UserPassword("***", "***")
			}
			return u.String()
		}
	}
	if idx := strings.LastIndex(value, "@"); idx > 0 {
		return "***:***@" + value[idx+1:]
	}
	return value
}

func splitCandidates(line string) []string {
	trimmed := strings.Trim(line, "\"'")
	parts := strings.FieldsFunc(trimmed, func(r rune) bool {
		switch r {
		case ',', ';', '|', '\t', ' ':
			return true
		default:
			return false
		}
	})

	out := make([]string, 0, len(parts)+1)
	if trimmed != "" {
		out = append(out, trimmed)
	}
	for _, part := range parts {
		part = strings.Trim(part, "\"'")
		if part != "" && part != trimmed {
			out = append(out, part)
		}
	}
	return out
}

func IsValidProxy(raw string) bool {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return false
	}

	if strings.Contains(candidate, "://") {
		u, err := url.Parse(candidate)
		if err != nil {
			return false
		}
		host := strings.TrimSpace(u.Host)
		if host == "" {
			host = strings.TrimSpace(u.Opaque)
		}
		if host == "" {
			return false
		}
		_, _, err = net.SplitHostPort(host)
		return err == nil
	}

	host := candidate
	if idx := strings.LastIndex(host, "@"); idx >= 0 {
		host = host[idx+1:]
	}
	_, _, err := net.SplitHostPort(host)
	return err == nil
}
