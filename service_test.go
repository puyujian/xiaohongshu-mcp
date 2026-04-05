package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewPublishExecutionContextIgnoresParentCancellation(t *testing.T) {
	type contextKey string

	parent, cancelParent := context.WithCancel(context.WithValue(context.Background(), contextKey("trace_id"), "publish-trace"))
	child, cancelChild := newPublishExecutionContext(parent)
	defer cancelChild()

	cancelParent()

	select {
	case <-child.Done():
		t.Fatalf("publish 执行上下文不应直接继承父级取消")
	case <-time.After(20 * time.Millisecond):
	}

	deadline, ok := child.Deadline()
	if !ok {
		t.Fatalf("publish 执行上下文应该带有超时")
	}
	if time.Until(deadline) < 9*time.Minute {
		t.Fatalf("publish 执行上下文超时过短: %s", time.Until(deadline))
	}

	if got := child.Value(contextKey("trace_id")); got != "publish-trace" {
		t.Fatalf("publish 执行上下文应保留原始上下文值，got=%v", got)
	}
}

func TestExplainPublishError(t *testing.T) {
	testCases := []struct {
		name             string
		noteType         string
		err              error
		wantMessageParts []string
		wantStep         string
		wantReasonPart   string
	}{
		{
			name:             "标签输入超时",
			noteType:         "图文笔记发布",
			err:              errors.New("输入标签[零食推荐]失败: 输入字符[零]失败: context canceled"),
			wantMessageParts: []string{"图文笔记发布失败", "填写标签", "context canceled"},
			wantStep:         "填写标签",
			wantReasonPart:   "调用方超时",
		},
		{
			name:             "商品未匹配",
			noteType:         "图文笔记发布",
			err:              errors.New("选择商品失败: 零食大礼包: 重试3次后未找到匹配商品: 零食大礼包"),
			wantMessageParts: []string{"图文笔记发布失败", "商品选择失败", "未找到匹配商品"},
			wantStep:         "选择商品",
			wantReasonPart:   "商品未上架",
		},
		{
			name:             "发布按钮失败",
			noteType:         "视频笔记发布",
			err:              errors.New("点击发布按钮失败: node is detached"),
			wantMessageParts: []string{"视频笔记发布失败", "发布按钮", "node is detached"},
			wantStep:         "点击发布按钮",
			wantReasonPart:   "发布按钮",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			message, details := explainPublishError(testCase.noteType, testCase.err)
			for _, part := range testCase.wantMessageParts {
				if !strings.Contains(message, part) {
					t.Fatalf("explainPublishError() message = %q, want contains %q", message, part)
				}
			}
			if details.Step != testCase.wantStep {
				t.Fatalf("explainPublishError() step = %q, want %q", details.Step, testCase.wantStep)
			}
			if !strings.Contains(details.Reason, testCase.wantReasonPart) {
				t.Fatalf("explainPublishError() reason = %q, want contains %q", details.Reason, testCase.wantReasonPart)
			}
			if details.RawError != testCase.err.Error() {
				t.Fatalf("explainPublishError() raw_error = %q, want %q", details.RawError, testCase.err.Error())
			}
		})
	}
}
