package xiaohongshu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/go-rod/rod"
	"github.com/xpzouying/xiaohongshu-mcp/errors"
)

type SearchResult struct {
	Search struct {
		Feeds FeedsValue `json:"feeds"`
	} `json:"search"`
}

// FilterOption 筛选选项结构体
type FilterOption struct {
	SortBy      string `json:"sort_by,omitempty" jsonschema:"排序依据: 综合|最新|最多点赞|最多评论|最多收藏,默认为'综合'"`
	NoteType    string `json:"note_type,omitempty" jsonschema:"笔记类型: 不限|视频|图文,默认为'不限'"`
	PublishTime string `json:"publish_time,omitempty" jsonschema:"发布时间: 不限|一天内|一周内|半年内,默认为'不限'"`
	SearchScope string `json:"search_scope,omitempty" jsonschema:"搜索范围: 不限|已看过|未看过|已关注,默认为'不限'"`
	Location    string `json:"location,omitempty" jsonschema:"位置距离: 不限|同城|附近,默认为'不限'"`
}

// internalFilterOption 内部使用的筛选选项(基于索引)
type internalFilterOption struct {
	FiltersIndex int    // 筛选组索引
	TagsIndex    int    // 标签索引
	Text         string // 标签文本描述
}

// 预定义的筛选选项映射表（内部使用）
var filterOptionsMap = map[int][]internalFilterOption{
	1: { // 排序依据
		{FiltersIndex: 1, TagsIndex: 1, Text: "综合"},
		{FiltersIndex: 1, TagsIndex: 2, Text: "最新"},
		{FiltersIndex: 1, TagsIndex: 3, Text: "最多点赞"},
		{FiltersIndex: 1, TagsIndex: 4, Text: "最多评论"},
		{FiltersIndex: 1, TagsIndex: 5, Text: "最多收藏"},
	},
	2: { // 笔记类型
		{FiltersIndex: 2, TagsIndex: 1, Text: "不限"},
		{FiltersIndex: 2, TagsIndex: 2, Text: "视频"},
		{FiltersIndex: 2, TagsIndex: 3, Text: "图文"},
	},
	3: { // 发布时间
		{FiltersIndex: 3, TagsIndex: 1, Text: "不限"},
		{FiltersIndex: 3, TagsIndex: 2, Text: "一天内"},
		{FiltersIndex: 3, TagsIndex: 3, Text: "一周内"},
		{FiltersIndex: 3, TagsIndex: 4, Text: "半年内"},
	},
	4: { // 搜索范围
		{FiltersIndex: 4, TagsIndex: 1, Text: "不限"},
		{FiltersIndex: 4, TagsIndex: 2, Text: "已看过"},
		{FiltersIndex: 4, TagsIndex: 3, Text: "未看过"},
		{FiltersIndex: 4, TagsIndex: 4, Text: "已关注"},
	},
	5: { // 位置距离
		{FiltersIndex: 5, TagsIndex: 1, Text: "不限"},
		{FiltersIndex: 5, TagsIndex: 2, Text: "同城"},
		{FiltersIndex: 5, TagsIndex: 3, Text: "附近"},
	},
}

// convertToInternalFilters 将 FilterOption 转换为内部的 internalFilterOption 列表
func convertToInternalFilters(filter FilterOption) ([]internalFilterOption, error) {
	var internalFilters []internalFilterOption

	// 处理排序依据
	if filter.SortBy != "" {
		internal, err := findInternalOption(1, filter.SortBy)
		if err != nil {
			return nil, fmt.Errorf("排序依据错误: %w", err)
		}
		internalFilters = append(internalFilters, internal)
	}

	// 处理笔记类型
	if filter.NoteType != "" {
		internal, err := findInternalOption(2, filter.NoteType)
		if err != nil {
			return nil, fmt.Errorf("笔记类型错误: %w", err)
		}
		internalFilters = append(internalFilters, internal)
	}

	// 处理发布时间
	if filter.PublishTime != "" {
		internal, err := findInternalOption(3, filter.PublishTime)
		if err != nil {
			return nil, fmt.Errorf("发布时间错误: %w", err)
		}
		internalFilters = append(internalFilters, internal)
	}

	// 处理搜索范围
	if filter.SearchScope != "" {
		internal, err := findInternalOption(4, filter.SearchScope)
		if err != nil {
			return nil, fmt.Errorf("搜索范围错误: %w", err)
		}
		internalFilters = append(internalFilters, internal)
	}

	// 处理位置距离
	if filter.Location != "" {
		internal, err := findInternalOption(5, filter.Location)
		if err != nil {
			return nil, fmt.Errorf("位置距离错误: %w", err)
		}
		internalFilters = append(internalFilters, internal)
	}

	return internalFilters, nil
}

// findInternalOption 根据筛选组索引和文本查找内部筛选选项
func findInternalOption(filtersIndex int, text string) (internalFilterOption, error) {
	options, exists := filterOptionsMap[filtersIndex]
	if !exists {
		return internalFilterOption{}, fmt.Errorf("筛选组 %d 不存在", filtersIndex)
	}

	for _, option := range options {
		if option.Text == text {
			return option, nil
		}
	}

	return internalFilterOption{}, fmt.Errorf("在筛选组 %d 中未找到文本 '%s'", filtersIndex, text)
}

// validateInternalFilterOption 验证内部筛选选项是否在有效范围内
func validateInternalFilterOption(filter internalFilterOption) error {
	// 检查筛选组索引是否有效
	if filter.FiltersIndex < 1 || filter.FiltersIndex > 5 {
		return fmt.Errorf("无效的筛选组索引 %d，有效范围为 1-5", filter.FiltersIndex)
	}

	// 检查标签索引是否在对应筛选组的有效范围内
	options, exists := filterOptionsMap[filter.FiltersIndex]
	if !exists {
		return fmt.Errorf("筛选组 %d 不存在", filter.FiltersIndex)
	}

	if filter.TagsIndex < 1 || filter.TagsIndex > len(options) {
		return fmt.Errorf("筛选组 %d 的标签索引 %d 超出范围，有效范围为 1-%d",
			filter.FiltersIndex, filter.TagsIndex, len(options))
	}

	return nil
}

type SearchAction struct {
	page *rod.Page
}

func NewSearchAction(page *rod.Page) *SearchAction {
	pp := page.Timeout(60 * time.Second)

	return &SearchAction{page: pp}
}

func (s *SearchAction) Search(ctx context.Context, keyword string, filters ...FilterOption) (feeds []Feed, err error) {
	defer recoverRodPanicAsError(ctx, &err)

	page := s.page.Context(ctx)

	if err = navigateSearchResultWithFallback(page, keyword); err != nil {
		return nil, err
	}
	if err = page.WaitStable(time.Second); err != nil {
		return nil, err
	}

	// 页面状态对象在代理链路下可能延迟注入，使用有界等待避免整体请求卡到 context deadline。
	_ = page.Timeout(8 * time.Second).Wait(rod.Eval(`() => {
		return !!(window.__INITIAL_STATE__ || window.__INITIAL_SSR_STATE__ || window.__UNIVERSAL_STATE__);
	}`))

	// 如果有筛选条件，则应用筛选
	if len(filters) > 0 {
		// 将所有 FilterOption 转换为内部筛选选项
		var allInternalFilters []internalFilterOption
		for _, filter := range filters {
			internalFilters, err := convertToInternalFilters(filter)
			if err != nil {
				return nil, fmt.Errorf("筛选选项转换失败: %w", err)
			}
			allInternalFilters = append(allInternalFilters, internalFilters...)
		}

		// 验证所有内部筛选选项
		for _, filter := range allInternalFilters {
			if err := validateInternalFilterOption(filter); err != nil {
				return nil, fmt.Errorf("筛选选项验证失败: %w", err)
			}
		}

		// 悬停在筛选按钮上
		filterButton := page.MustElement(`div.filter`)
		filterButton.MustHover()

		// 等待筛选面板出现
		if err = page.Timeout(8 * time.Second).Wait(rod.Eval(`() => document.querySelector('div.filter-panel') !== null`)); err != nil {
			return nil, fmt.Errorf("筛选面板加载超时: %w", err)
		}

		// 应用所有筛选条件
		for _, filter := range allInternalFilters {
			selector := fmt.Sprintf(`div.filter-panel div.filters:nth-child(%d) div.tags:nth-child(%d)`,
				filter.FiltersIndex, filter.TagsIndex)
			option := page.MustElement(selector)
			option.MustClick()
		}

		// 等待页面更新
		if err = page.WaitStable(time.Second); err != nil {
			return nil, err
		}
		// 重新等待状态对象更新（有界等待）
		_ = page.Timeout(8 * time.Second).Wait(rod.Eval(`() => {
			return !!(window.__INITIAL_STATE__ || window.__INITIAL_SSR_STATE__ || window.__UNIVERSAL_STATE__);
		}`))
	}

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
			state.search && state.search.feeds,
			state.feed && state.feed.feeds,
			state.searchResult && state.searchResult.feeds,
			state.result && state.result.feeds,
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

func makeSearchURL(keyword string) string {
	return makeSearchURLWithSource(keyword, "web_explore_feed")
}

func makeSearchURLWithSource(keyword, source string) string {
	values := url.Values{}
	values.Set("keyword", keyword)
	if source == "" {
		source = "web_explore_feed"
	}
	values.Set("source", source)
	//https://www.xiaohongshu.com/search_result?keyword=%25E7%258E%258B%25E5%25AD%2590&source=web_search_result_notes
	//https://www.xiaohongshu.com/search_result?keyword=%25E7%258E%258B%25E5%25AD%2590&source=web_explore_feed
	return fmt.Sprintf("https://www.xiaohongshu.com/search_result?%s", values.Encode())
}

// navigateSearchResultWithFallback 在代理网络不稳定时依次尝试不同 source 参数，提升搜索页可达性。
func navigateSearchResultWithFallback(page *rod.Page, keyword string) error {
	searchURLs := []string{
		makeSearchURLWithSource(keyword, "web_explore_feed"),
		makeSearchURLWithSource(keyword, "web_search_result_notes"),
	}

	seen := make(map[string]struct{}, len(searchURLs))
	var lastErr error
	for _, u := range searchURLs {
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		// 搜索页导航单次等待过长会拖垮 MCP 调用，收敛单次导航等待时间。
		if err := navigateWithRetry(page.Timeout(25*time.Second), u, 4); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("搜索页导航失败: 未生成可用 URL")
}
