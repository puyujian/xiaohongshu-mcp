package main

import (
	"testing"
	"time"
)

func TestNormalizeMCPCallTimeout(t *testing.T) {
	testCases := []struct {
		name      string
		toolName  string
		timeoutMs int
		want      time.Duration
	}{
		{name: "普通工具默认 30 秒", toolName: "check_login_status", timeoutMs: 0, want: defaultMCPToolTimeout},
		{name: "普通工具上限仍是 2 分钟回落", toolName: "check_login_status", timeoutMs: int((3 * time.Minute) / time.Millisecond), want: defaultMCPToolTimeout},
		{name: "发布工具默认 5 分钟", toolName: "publish_content", timeoutMs: 0, want: defaultLongRunningToolTimeout},
		{name: "发布工具允许 8 分钟", toolName: "publish_with_video", timeoutMs: int((8 * time.Minute) / time.Millisecond), want: 8 * time.Minute},
		{name: "发布工具最大截断到 10 分钟", toolName: "publish_content", timeoutMs: int((12 * time.Minute) / time.Millisecond), want: maxMCPToolTimeout},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := normalizeMCPCallTimeout(testCase.toolName, testCase.timeoutMs)
			if got != testCase.want {
				t.Fatalf("normalizeMCPCallTimeout(%q, %d) = %s, want %s", testCase.toolName, testCase.timeoutMs, got, testCase.want)
			}
		})
	}
}

func TestIsLongRunningMCPTool(t *testing.T) {
	if !isLongRunningMCPTool("publish_content") {
		t.Fatalf("publish_content 应识别为长任务")
	}
	if isLongRunningMCPTool("check_login_status") {
		t.Fatalf("check_login_status 不应识别为长任务")
	}
}
