package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
	"github.com/xpzouying/xiaohongshu-mcp/pkg/proxyutil"
)

const loginPublishProxyRetryCount = 2

func executeWithProxyRetry[T any](
	operation string,
	hasProxy bool,
	resolve func() (string, error),
	run func(proxy string) (T, error),
	drop func(),
) (T, error) {
	var zero T
	if !hasProxy {
		return run("")
	}

	maxProxyAttempts := loginPublishProxyRetryCount + 1
	var lastErr error

	for attempt := 1; attempt <= maxProxyAttempts; attempt++ {
		proxy, err := resolve()
		if err != nil {
			lastErr = err
			logrus.Warnf("%s 第%d/%d次获取代理失败: %v", operation, attempt, maxProxyAttempts, err)
			if attempt < maxProxyAttempts {
				continue
			}
			break
		}

		result, err := run(proxy)
		if err == nil {
			return result, nil
		}

		lastErr = err
		if drop != nil {
			drop()
		}
		if !isRetryableProxyFailure(err) {
			return zero, err
		}

		logrus.Warnf("%s 第%d/%d次代理尝试失败，代理=%s，错误=%v", operation, attempt, maxProxyAttempts, proxyutil.SanitizeForLog(proxy), err)
	}

	logrus.Warnf("%s 代理尝试已失败，开始兜底直连。最后一次代理错误: %v", operation, lastErr)
	if drop != nil {
		drop()
	}
	return run("")
}

func (s *XiaohongshuService) hasLoginPublishProxyConfig() bool {
	return strings.TrimSpace(configs.GetProxy()) != "" || strings.TrimSpace(configs.GetProxyPool()) != ""
}

func runLoginPublishWithRetry[T any](
	s *XiaohongshuService,
	ctx context.Context,
	operation string,
	run func(context.Context, string) (T, error),
) (T, error) {
	return executeWithProxyRetry(
		operation,
		s.hasLoginPublishProxyConfig(),
		func() (string, error) {
			return s.resolveLoginPublishProxy(ctx)
		},
		func(proxy string) (T, error) {
			return run(ctx, proxy)
		},
		func() {
			if b := s.dropSharedBrowser(); b != nil {
				b.Close()
			}
		},
	)
}

func isRetryableProxyFailure(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}

	indicators := []string{
		"proxy",
		"代理",
		"代理池",
		"net::",
		"ssl",
		"tls",
		"eof",
		"connection reset",
		"connection closed",
		"connection timed out",
		"timed out",
		"timeout",
		"invalid_auth_credentials",
		"empty_response",
		"http2_protocol_error",
		"tunnel_connection_failed",
		"socks_connection_failed",
		"proxy_connection_failed",
		"name_not_resolved",
		"internet_disconnected",
		"address_unreachable",
		"network_changed",
		"检查网络连接",
		"上传超时",
	}
	for _, indicator := range indicators {
		if strings.Contains(msg, indicator) {
			return true
		}
	}
	return false
}

func proxyRetryFallbackMessage(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s兜底直连后仍失败: %w", operation, err)
}
