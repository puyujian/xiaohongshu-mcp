package xiaohongshu

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

// navigateWithRetry 对导航做有限次重试（带退避），主要用于处理类似 net::ERR_EMPTY_RESPONSE 这类瞬时网络问题。
func navigateWithRetry(page *rod.Page, targetURL string, attempts int) error {
	if page == nil {
		return fmt.Errorf("page 不能为空")
	}
	attempts = effectiveNavigateAttempts(attempts)

	var lastErr error
	var tried int
	for i := 1; i <= attempts; i++ {
		tried = i
		if err := page.Navigate(targetURL); err == nil {
			return nil
		} else {
			lastErr = err
			if isRetryableNavigationError(err) && i < attempts {
				time.Sleep(navigateBackoff(i))
				continue
			}
			break
		}
	}

	if lastErr == nil {
		return nil
	}
	if tried > 1 {
		return fmt.Errorf("导航失败（%s，已重试%d次）: %w", targetURL, tried, lastErr)
	}
	return lastErr
}

func isRetryableNavigationError(err error) bool {
	if err == nil {
		return false
	}

	// rod 的导航错误会包装成 NavigationError{Reason: "net::ERR_..."}
	var navErr *rod.NavigationError
	if errors.As(err, &navErr) {
		switch navErr.Reason {
		case "net::ERR_EMPTY_RESPONSE",
			"net::ERR_RESPONSE_HEADERS_TRUNCATED",
			"net::ERR_HTTP2_PROTOCOL_ERROR",
			"net::ERR_PROXY_CONNECTION_FAILED",
			"net::ERR_TUNNEL_CONNECTION_FAILED",
			"net::ERR_SOCKS_CONNECTION_FAILED",
			"net::ERR_CONNECTION_RESET",
			"net::ERR_CONNECTION_CLOSED",
			"net::ERR_CONNECTION_TIMED_OUT",
			"net::ERR_TIMED_OUT",
			"net::ERR_ADDRESS_UNREACHABLE",
			"net::ERR_INTERNET_DISCONNECTED",
			"net::ERR_NAME_NOT_RESOLVED",
			"net::ERR_NETWORK_CHANGED":
			return true
		default:
			return false
		}
	}

	// 兜底：部分错误可能被包装成普通 error
	msg := err.Error()
	return strings.Contains(msg, "net::ERR_EMPTY_RESPONSE") ||
		strings.Contains(msg, "net::ERR_RESPONSE_HEADERS_TRUNCATED") ||
		strings.Contains(msg, "net::ERR_HTTP2_PROTOCOL_ERROR") ||
		strings.Contains(msg, "net::ERR_PROXY_CONNECTION_FAILED") ||
		strings.Contains(msg, "net::ERR_TUNNEL_CONNECTION_FAILED") ||
		strings.Contains(msg, "net::ERR_SOCKS_CONNECTION_FAILED") ||
		strings.Contains(msg, "net::ERR_CONNECTION_RESET") ||
		strings.Contains(msg, "net::ERR_CONNECTION_CLOSED") ||
		strings.Contains(msg, "net::ERR_CONNECTION_TIMED_OUT") ||
		strings.Contains(msg, "net::ERR_TIMED_OUT") ||
		strings.Contains(msg, "net::ERR_ADDRESS_UNREACHABLE") ||
		strings.Contains(msg, "net::ERR_INTERNET_DISCONNECTED") ||
		strings.Contains(msg, "net::ERR_NAME_NOT_RESOLVED") ||
		strings.Contains(msg, "net::ERR_NETWORK_CHANGED")
}

func navigateBackoff(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 600 * time.Millisecond
	case 2:
		return 1200 * time.Millisecond
	case 3:
		return 2 * time.Second
	case 4:
		return 3 * time.Second
	case 5:
		return 5 * time.Second
	default:
		return 8 * time.Second
	}
}

func effectiveNavigateAttempts(attempts int) int {
	if attempts <= 0 {
		attempts = 1
	}
	// 代理链路比直连更容易出现瞬时断连；但重试过多会增加 MCP 调用超时风险。
	// 默认将代理场景最小重试次数提升到 4 次，并允许通过环境变量覆盖。
	// 例如：XHS_NAVIGATE_PROXY_ATTEMPTS=6
	proxyMinAttempts := 4
	if raw := strings.TrimSpace(os.Getenv("XHS_NAVIGATE_PROXY_ATTEMPTS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 10 {
			proxyMinAttempts = n
		}
	}
	if strings.TrimSpace(os.Getenv("XHS_PROXY")) != "" && attempts < proxyMinAttempts {
		return proxyMinAttempts
	}
	return attempts
}
