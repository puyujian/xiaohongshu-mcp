package xiaohongshu

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-rod/rod"
	myerrors "github.com/xpzouying/xiaohongshu-mcp/errors"
)

// NotificationMentionsAction 表示通知页“评论和@”抓取动作
type NotificationMentionsAction struct {
	page *rod.Page
}

// NewNotificationMentionsAction 创建通知页“评论和@”抓取动作
func NewNotificationMentionsAction(page *rod.Page) *NotificationMentionsAction {
	return &NotificationMentionsAction{page: page.Timeout(60 * time.Second)}
}

// GetMentions 获取当前登录账号通知页“评论和@”列表
func (n *NotificationMentionsAction) GetMentions(ctx context.Context) (resp *NotificationMentionsData, err error) {
	defer recoverRodPanicAsError(ctx, &err)

	page := n.page.Context(ctx)
	navigate := NewNavigate(page)

	if err = navigate.ToNotificationMentionsPage(ctx); err != nil {
		return nil, err
	}
	if err = page.WaitStable(time.Second); err != nil {
		return nil, err
	}

	time.Sleep(500 * time.Millisecond)

	return n.extractMentionsData(page)
}

func (n *NotificationMentionsAction) extractMentionsData(page *rod.Page) (*NotificationMentionsData, error) {
	if err := page.Wait(rod.Eval(`() => !!(window.__INITIAL_STATE__ || window.__INITIAL_SSR_STATE__ || window.__UNIVERSAL_STATE__)`)); err != nil {
		return nil, err
	}

	evalResult, err := page.Eval(`() => {
		const unwrap = (value) => {
			if (!value || typeof value !== 'object') {
				return value;
			}
			if ('value' in value && value.value !== undefined) {
				return value.value;
			}
			if ('_value' in value && value._value !== undefined) {
				return value._value;
			}
			return value;
		};

		const normalize = (value) => {
			const raw = unwrap(value) || {};
			const messageList = unwrap(raw.messageList ?? raw.message_list);
			return {
				messageList: Array.isArray(messageList) ? messageList : [],
				cursor: String(raw.cursor ?? raw.strCursor ?? raw.nextCursor ?? ''),
				hasMore: Boolean(raw.hasMore ?? raw.has_more),
			};
		};

		const hasMentionsShape = (value) => {
			if (!value || typeof value !== 'object') {
				return false;
			}
			const raw = unwrap(value) || {};
			return Object.prototype.hasOwnProperty.call(raw, 'messageList') ||
				Object.prototype.hasOwnProperty.call(raw, 'message_list');
		};

		const state = window.__INITIAL_STATE__ || window.__INITIAL_SSR_STATE__ || window.__UNIVERSAL_STATE__;
		if (!state || typeof state !== 'object') {
			return '';
		}

		const directMentions = state.notification?.notificationMap?.mentions;
		if (hasMentionsShape(directMentions)) {
			return JSON.stringify(normalize(directMentions));
		}

		const seen = new Set();
		const searchMentions = (value, depth = 0) => {
			if (!value || typeof value !== 'object' || depth > 8 || seen.has(value)) {
				return null;
			}
			seen.add(value);

			if (hasMentionsShape(value)) {
				return normalize(value);
			}

			const raw = unwrap(value);
			if (!raw || typeof raw !== 'object' || seen.has(raw)) {
				return null;
			}
			seen.add(raw);

			if (hasMentionsShape(raw)) {
				return normalize(raw);
			}

			for (const child of Object.values(raw)) {
				const found = searchMentions(child, depth + 1);
				if (found) {
					return found;
				}
			}

			return null;
		};

		const foundMentions = searchMentions(state);
		if (!foundMentions) {
			return '';
		}

		return JSON.stringify(foundMentions);
	}`)
	if err != nil {
		return nil, err
	}

	resultJSON := evalResult.Value.String()
	if resultJSON == "" {
		return nil, myerrors.ErrNoNotificationMentions
	}

	var result NotificationMentionsData
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal notification mentions data: %w", err)
	}

	return &result, nil
}
