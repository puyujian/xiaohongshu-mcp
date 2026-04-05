package main

import (
	"errors"
	"testing"
)

func TestExecuteWithProxyRetryFallbackToDirect(t *testing.T) {
	proxies := []string{"p1", "p2", "p3"}
	resolveCalls := 0
	runCalls := []string{}
	dropCalls := 0

	got, err := executeWithProxyRetry(
		"图文发布",
		true,
		func() (string, error) {
			proxy := proxies[resolveCalls]
			resolveCalls++
			return proxy, nil
		},
		func(proxy string) (string, error) {
			runCalls = append(runCalls, proxy)
			if proxy == "" {
				return "direct-ok", nil
			}
			return "", errors.New("navigation failed: net::ERR_EMPTY_RESPONSE")
		},
		func() { dropCalls++ },
	)
	if err != nil {
		t.Fatalf("executeWithProxyRetry 返回错误: %v", err)
	}
	if got != "direct-ok" {
		t.Fatalf("executeWithProxyRetry = %q, want %q", got, "direct-ok")
	}
	if resolveCalls != 3 {
		t.Fatalf("resolveCalls = %d, want 3", resolveCalls)
	}
	if dropCalls != 4 {
		t.Fatalf("dropCalls = %d, want 4", dropCalls)
	}
	wantCalls := []string{"p1", "p2", "p3", ""}
	if len(runCalls) != len(wantCalls) {
		t.Fatalf("runCalls len = %d, want %d", len(runCalls), len(wantCalls))
	}
	for i := range wantCalls {
		if runCalls[i] != wantCalls[i] {
			t.Fatalf("runCalls[%d] = %q, want %q", i, runCalls[i], wantCalls[i])
		}
	}
}

func TestExecuteWithProxyRetryStopsOnNonProxyError(t *testing.T) {
	resolveCalls := 0
	runCalls := 0
	dropCalls := 0

	_, err := executeWithProxyRetry(
		"图文发布",
		true,
		func() (string, error) {
			resolveCalls++
			return "p1", nil
		},
		func(proxy string) (string, error) {
			runCalls++
			return "", errors.New("标题长度超过限制")
		},
		func() { dropCalls++ },
	)
	if err == nil {
		t.Fatalf("executeWithProxyRetry 应返回错误")
	}
	if resolveCalls != 1 {
		t.Fatalf("resolveCalls = %d, want 1", resolveCalls)
	}
	if runCalls != 1 {
		t.Fatalf("runCalls = %d, want 1", runCalls)
	}
	if dropCalls != 1 {
		t.Fatalf("dropCalls = %d, want 1", dropCalls)
	}
}

func TestIsRetryableProxyFailure(t *testing.T) {
	if !isRetryableProxyFailure(errors.New("navigation failed: net::ERR_INVALID_AUTH_CREDENTIALS")) {
		t.Fatalf("ERR_INVALID_AUTH_CREDENTIALS 应识别为可重试代理错误")
	}
	if !isRetryableProxyFailure(errors.New("第1张图片上传超时(60s)，请检查网络连接和图片大小")) {
		t.Fatalf("上传超时应识别为可重试代理错误")
	}
	if isRetryableProxyFailure(errors.New("标题长度超过限制")) {
		t.Fatalf("标题长度错误不应识别为代理错误")
	}
}
