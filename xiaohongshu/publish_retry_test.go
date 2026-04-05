package xiaohongshu

import (
	"errors"
	"testing"
)

func TestIsRetryablePublishError(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "context canceled", err: errors.New("context canceled"), want: true},
		{name: "wrapped context canceled", err: errors.New("输入字符失败: context canceled"), want: true},
		{name: "execution context destroyed", err: errors.New("Execution context was destroyed"), want: true},
		{name: "node detached", err: errors.New("Node is detached from document"), want: true},
		{name: "business error", err: errors.New("未找到匹配商品"), want: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := isRetryablePublishError(testCase.err)
			if got != testCase.want {
				t.Fatalf("isRetryablePublishError(%v) = %v, want %v", testCase.err, got, testCase.want)
			}
		})
	}
}
