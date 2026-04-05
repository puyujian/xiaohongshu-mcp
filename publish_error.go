package main

import (
	"fmt"
	"strings"
)

type publishErrorDetails struct {
	NoteType   string `json:"note_type"`
	Step       string `json:"step"`
	Reason     string `json:"reason"`
	Suggestion string `json:"suggestion,omitempty"`
	RawError   string `json:"raw_error,omitempty"`
}

func explainPublishError(noteType string, err error) (string, publishErrorDetails) {
	details := publishErrorDetails{
		NoteType: noteType,
		Step:     "发布流程",
	}
	if err == nil {
		details.Reason = "发布失败"
		return noteType + "失败", details
	}

	raw := strings.TrimSpace(err.Error())
	details.RawError = raw
	details.Step, details.Reason, details.Suggestion = classifyPublishError(raw)

	message := fmt.Sprintf("%s失败：%s", noteType, details.Reason)
	if raw != "" && raw != details.Reason {
		message = fmt.Sprintf("%s（原始错误：%s）", message, raw)
	}
	return message, details
}

func classifyPublishError(raw string) (step, reason, suggestion string) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	isCanceled := strings.Contains(normalized, "context canceled") ||
		strings.Contains(normalized, "context deadline exceeded")

	switch {
	case strings.Contains(raw, "标题长度超过限制"):
		return "标题校验", "标题长度超过平台限制，请缩短到 20 个字以内", "缩短标题后重新发布"

	case strings.Contains(raw, "定时发布时间格式错误"):
		return "定时发布", "定时发布时间格式不正确，必须使用 ISO8601 / RFC3339", "例如使用 2026-04-05T20:30:00+08:00"

	case strings.Contains(raw, "定时发布时间必须至少在1小时后"):
		return "定时发布", "定时发布时间过早，至少需要晚于当前时间 1 小时", "调整到 1 小时以后再试"

	case strings.Contains(raw, "定时发布时间不能超过14天"):
		return "定时发布", "定时发布时间过晚，不能超过未来 14 天", "调整到 14 天内再试"

	case strings.Contains(raw, "查找标题输入框失败"),
		strings.Contains(raw, "输入标题失败"):
		if isCanceled {
			return "填写标题", "填写标题时流程被取消或超时，常见原因是调用方超时、连接断开，或页面正在重载", "将发布超时调大到 5 到 10 分钟，并在页面稳定后重试"
		}
		return "填写标题", "填写标题时页面元素未准备好或输入框状态异常", "确认发布页已经稳定加载后再试"

	case strings.Contains(raw, "没有找到内容输入框"),
		strings.Contains(raw, "输入正文失败"),
		strings.Contains(raw, "检查正文长度元素失败"),
		strings.Contains(raw, "检查正文长度"):
		if isCanceled {
			return "填写正文", "填写正文或校验正文长度时流程被取消或超时，常见原因是调用方超时、连接断开，或编辑器失焦", "将发布超时调大到 5 到 10 分钟，并减少单次正文长度"
		}
		return "填写正文", "正文编辑器状态异常，未能正常输入或完成长度校验", "检查正文长度是否超限，并确认编辑器可正常聚焦"

	case strings.Contains(raw, "输入标签["),
		strings.Contains(raw, "输入#失败"),
		strings.Contains(raw, "输入字符["),
		strings.Contains(raw, "点击标签联想选项失败"):
		if isCanceled {
			return "填写标签", "填写标签时流程被取消或超时，常见原因是调用方超时、连接断开，或标签输入框失焦", "将发布超时调大到 5 到 10 分钟，并尽量减少单次标签数量"
		}
		return "填写标签", "标签输入或标签联想选择失败，可能是输入框失焦或联想列表未出现", "检查标签内容是否过长，并在页面稳定后重试"

	case strings.Contains(raw, "未找到添加商品入口"),
		strings.Contains(raw, "打开商品选择弹窗失败"),
		strings.Contains(raw, "未找到商品搜索输入框"),
		strings.Contains(raw, "搜索商品失败"),
		strings.Contains(raw, "选择商品失败"),
		strings.Contains(raw, "未找到匹配商品"):
		if isCanceled {
			return "选择商品", "选择商品时流程被取消或超时，常见原因是调用方超时、连接断开，或商品弹窗未加载完成", "将发布超时调大到 5 到 10 分钟，并确认商品弹窗加载完成后再试"
		}
		return "选择商品", "商品选择失败，可能是商品未上架、关键词不匹配，或商品弹窗未正常加载", "确认商品已在店铺中上架，且传入的商品关键词或 ID 正确"

	case strings.Contains(raw, "设置可见范围失败"):
		return "设置可见范围", "设置可见范围失败，页面上的可见范围选项可能未正确展开", "确认账号页面状态正常后重试"

	case strings.Contains(raw, "设置定时发布失败"):
		return "设置定时发布", "设置定时发布时间失败，页面上的定时发布控件可能未正常响应", "确认发布时间合法，并在页面稳定后重试"

	case strings.Contains(raw, "查找发布按钮失败"),
		strings.Contains(raw, "点击发布按钮失败"),
		strings.Contains(raw, "等待发布按钮可点击超时"):
		if isCanceled {
			return "点击发布按钮", "点击发布按钮时流程被取消或超时，常见原因是调用方超时、连接断开，或页面仍被遮罩层阻塞", "将发布超时调大到 5 到 10 分钟，并确认发布按钮已可点击"
		}
		return "点击发布按钮", "发布按钮不可点击或页面状态异常，导致最终提交失败", "检查是否有遮罩层、弹窗或平台校验提示拦住了发布按钮"

	case isCanceled:
		return "发布流程", "发布流程执行中被取消或超时，常见原因是调用方超时、连接断开，或浏览器页面无响应", "将发布超时调大到 5 到 10 分钟，并结合管理器可视化调试查看最后一步"

	default:
		return "发布流程", "发布流程执行异常", "请结合管理器可视化调试和用户日志，查看最后一个失败步骤"
	}
}
