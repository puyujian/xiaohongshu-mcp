package xiaohongshu

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-rod/rod"
	"github.com/xpzouying/xiaohongshu-mcp/errors"
)

type FeedsListAction struct {
	page *rod.Page
}

func NewFeedsListAction(page *rod.Page) *FeedsListAction {
	pp := page.Timeout(60 * time.Second)

	return &FeedsListAction{page: pp}
}

// GetFeedsList 获取页面的 Feed 列表数据
func (f *FeedsListAction) GetFeedsList(ctx context.Context) (feeds []Feed, err error) {
	defer recoverRodPanicAsError(ctx, &err)

	page := f.page.Context(ctx)

	if err = navigateWithRetry(page, "https://www.xiaohongshu.com", 3); err != nil {
		return nil, err
	}
	if err = page.WaitDOMStable(time.Second, 0); err != nil {
		return nil, err
	}

	time.Sleep(1 * time.Second)

	evalResult, err := page.Eval(`() => {
                if (window.__INITIAL_STATE__ &&
                    window.__INITIAL_STATE__.feed &&
                    window.__INITIAL_STATE__.feed.feeds) {
                        const feeds = window.__INITIAL_STATE__.feed.feeds;      
                        const feedsData = feeds.value !== undefined ? feeds.value : feeds._value;
                        if (feedsData) {
                                return JSON.stringify(feedsData);
                        }
                }
                return "";
        }`)
	if err != nil {
		return nil, err
	}
	result := evalResult.Value.String()

	if result == "" {
		return nil, errors.ErrNoFeeds
	}

	if err := json.Unmarshal([]byte(result), &feeds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal feeds: %w", err)
	}

	return feeds, nil
}
