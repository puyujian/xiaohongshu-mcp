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

	// 代理链路下首屏异步数据到达可能明显变慢，先等待 feeds 数据就绪。
	_ = page.Wait(rod.Eval(`() => {
		const state = window.__INITIAL_STATE__ || window.__INITIAL_SSR_STATE__ || window.__UNIVERSAL_STATE__;
		if (!state) return false;
		const toArray = (v) => {
			if (!v) return null;
			if (Array.isArray(v)) return v;
			if (Array.isArray(v.value)) return v.value;
			if (Array.isArray(v._value)) return v._value;
			if (v.value && Array.isArray(v.value.list)) return v.value.list;
			if (v._value && Array.isArray(v._value.list)) return v._value.list;
			return null;
		};
		const candidates = [
			state.feed && state.feed.feeds,
			state.homefeed && state.homefeed.feeds,
			state.explore && state.explore.feeds,
			state.recommend && state.recommend.feeds,
			state.feeds
		];
		return candidates.some((c) => {
			const arr = toArray(c);
			return Array.isArray(arr) && arr.length > 0;
		});
	}`))

	time.Sleep(1 * time.Second)

	evalResult, err := page.Eval(`() => {
		const state = window.__INITIAL_STATE__ || window.__INITIAL_SSR_STATE__ || window.__UNIVERSAL_STATE__;
		if (!state) return "";

		const toArray = (v) => {
			if (!v) return null;
			if (Array.isArray(v)) return v;
			if (Array.isArray(v.value)) return v.value;
			if (Array.isArray(v._value)) return v._value;
			if (v.value && Array.isArray(v.value.list)) return v.value.list;
			if (v._value && Array.isArray(v._value.list)) return v._value.list;
			return null;
		};

		const knownCandidates = [
			state.feed && state.feed.feeds,
			state.homefeed && state.homefeed.feeds,
			state.explore && state.explore.feeds,
			state.recommend && state.recommend.feeds,
			state.feeds
		];
		for (const c of knownCandidates) {
			const arr = toArray(c);
			if (Array.isArray(arr) && arr.length > 0) {
				return JSON.stringify(arr);
			}
		}

		// 兜底：递归扫描状态树，寻找形态接近 feed 的数组。
		const seen = new Set();
		const queue = [state];
		let visited = 0;
		const maxVisited = 2500;
		while (queue.length > 0 && visited < maxVisited) {
			const cur = queue.shift();
			visited++;
			if (!cur || typeof cur !== "object") continue;
			if (seen.has(cur)) continue;
			seen.add(cur);

			const arr = toArray(cur);
			if (Array.isArray(arr) && arr.length > 0) {
				const first = arr[0];
				if (first && typeof first === "object" && (
					first.id || first.noteCard || first.note_card || first.xsecToken || first.xsec_token
				)) {
					return JSON.stringify(arr);
				}
			}

			if (Array.isArray(cur)) {
				for (const item of cur) {
					if (item && typeof item === "object") queue.push(item);
				}
			} else {
				for (const key in cur) {
					if (!Object.prototype.hasOwnProperty.call(cur, key)) continue;
					const v = cur[key];
					if (v && typeof v === "object") queue.push(v);
				}
			}
		}

		return "";
	}`)
	if err != nil {
		return nil, err
	}
	result := evalResult.Value.String()

	if result == "" || result == "null" || result == "undefined" {
		diagResult, diagErr := page.Eval(`() => {
			const state = window.__INITIAL_STATE__ || window.__INITIAL_SSR_STATE__ || window.__UNIVERSAL_STATE__;
			const bodyText = (document.body && typeof document.body.innerText === "string")
				? document.body.innerText.slice(0, 120)
				: "";
			return JSON.stringify({
				url: window.location ? window.location.href : "",
				title: document.title || "",
				has_initial_state: !!state,
				body_preview: bodyText
			});
		}`)
		if diagErr == nil {
			diag := diagResult.Value.String()
			if diag != "" && diag != "null" && diag != "undefined" {
				return nil, fmt.Errorf("%w（诊断=%s）", errors.ErrNoFeeds, diag)
			}
		}
		return nil, errors.ErrNoFeeds
	}

	if err := json.Unmarshal([]byte(result), &feeds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal feeds: %w", err)
	}

	return feeds, nil
}
