package xiaohongshu

import (
	"context"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/pkg/flowdebug"
)

// PublishImageContent 发布图文内容
type PublishImageContent struct {
	Title        string
	Content      string
	Tags         []string
	Products     []string
	ImagePaths   []string
	ScheduleTime *time.Time // 定时发布时间，nil 表示立即发布
	IsOriginal   bool       // 是否声明原创
	Visibility   string     // 可见范围: "公开可见"(默认), "仅自己可见", "仅互关好友可见"
}

type PublishAction struct {
	page *rod.Page
}

const (
	urlOfPublic           = `https://creator.xiaohongshu.com/publish/publish?source=official`
	publishStepRetryCount = 3
	publishStepRetryDelay = 400 * time.Millisecond
)

func NewPublishImageAction(page *rod.Page) (action *PublishAction, err error) {
	defer recoverRodPanicAsError(nil, &err)

	pp := page.Timeout(300 * time.Second)

	if err = navigateWithRetry(pp, urlOfPublic, 3); err != nil {
		return nil, err
	}

	// 使用 WaitLoad 代替 WaitIdle（更宽松）
	if err := pp.WaitLoad(); err != nil {
		logrus.Warnf("等待页面加载出现问题: %v，继续尝试", err)
	}
	time.Sleep(2 * time.Second)

	// 等待页面稳定
	if err := pp.WaitDOMStable(time.Second, 0.1); err != nil {
		logrus.Warnf("等待 DOM 稳定出现问题: %v，继续尝试", err)
	}
	time.Sleep(1 * time.Second)

	if err := mustClickPublishTab(pp, "上传图文"); err != nil {
		logrus.Errorf("点击上传图文 TAB 失败: %v", err)
		return nil, err
	}

	time.Sleep(1 * time.Second)

	return &PublishAction{
		page: pp,
	}, nil
}

func (p *PublishAction) Publish(ctx context.Context, content PublishImageContent) (err error) {
	defer recoverRodPanicAsError(ctx, &err)

	dbg := flowdebug.FromContext(ctx)
	if dbg != nil {
		dbg.Step("开始发布图文", map[string]any{
			"images":   len(content.ImagePaths),
			"tags":     len(content.Tags),
			"products": len(content.Products),
		})
		_ = dbg.WaitIfPaused(ctx)
	}

	if len(content.ImagePaths) == 0 {
		return errors.New("图片不能为空")
	}

	page := p.page.Context(ctx)

	if dbg != nil {
		dbg.Step("上传图片", map[string]any{"count": len(content.ImagePaths)})
		_ = dbg.WaitIfPaused(ctx)
	}
	if err := uploadImages(ctx, page, content.ImagePaths); err != nil {
		return errors.Wrap(err, "小红书上传图片失败")
	}

	tags := content.Tags
	if len(tags) >= 10 {
		logrus.Warnf("标签数量超过10，截取前10个标签")
		tags = tags[:10]
	}

	logrus.Infof("发布内容: title=%s, images=%d, tags=%v, products=%d, schedule=%v, original=%v, visibility=%s", content.Title, len(content.ImagePaths), tags, len(content.Products), content.ScheduleTime, content.IsOriginal, content.Visibility)

	if len(content.Products) > 0 {
		if dbg != nil {
			dbg.Step("选择商品", map[string]any{"count": len(content.Products)})
			_ = dbg.WaitIfPaused(ctx)
		}
		if err := addProducts(ctx, page, content.Products); err != nil {
			return errors.Wrap(err, "选择商品失败")
		}
	}

	if dbg != nil {
		dbg.Step("填写并提交发布", map[string]any{"tags": len(tags)})
		_ = dbg.WaitIfPaused(ctx)
	}
	if err := submitPublish(ctx, page, content.Title, content.Content, tags, content.ScheduleTime, content.IsOriginal, content.Visibility); err != nil {
		return errors.Wrap(err, "小红书发布失败")
	}

	return nil
}

func removePopCover(page *rod.Page) {

	// 先移除弹窗封面
	has, elem, err := page.Has("div.d-popover")
	if err != nil {
		return
	}
	if has {
		elem.MustRemove()
	}

	// 兜底：点击一下空位置吧
	clickEmptyPosition(page)
}

func clickEmptyPosition(page *rod.Page) {
	x := 380 + rand.Intn(100)
	y := 20 + rand.Intn(60)
	page.Mouse.MustMoveTo(float64(x), float64(y)).MustClick(proto.InputMouseButtonLeft)
}

func mustClickPublishTab(page *rod.Page, tabname string) error {
	page.MustElement(`div.upload-content`).MustWaitVisible()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		tab, blocked, err := getTabElement(page, tabname)
		if err != nil {
			logrus.Warnf("获取发布 TAB 元素失败: %v", err)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if tab == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if blocked {
			logrus.Info("发布 TAB 被遮挡，尝试移除遮挡")
			removePopCover(page)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if err := tab.Click(proto.InputMouseButtonLeft, 1); err != nil {
			logrus.Warnf("点击发布 TAB 失败: %v", err)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		return nil
	}

	return errors.Errorf("没有找到发布 TAB - %s", tabname)
}

func getTabElement(page *rod.Page, tabname string) (*rod.Element, bool, error) {
	elems, err := page.Elements("div.creator-tab")
	if err != nil {
		return nil, false, err
	}

	for _, elem := range elems {
		if !isElementVisible(elem) {
			continue
		}

		text, err := elem.Text()
		if err != nil {
			logrus.Debugf("获取发布 TAB 文本失败: %v", err)
			continue
		}

		if strings.TrimSpace(text) != tabname {
			continue
		}

		blocked, err := isElementBlocked(elem)
		if err != nil {
			return nil, false, err
		}

		return elem, blocked, nil
	}

	return nil, false, nil
}

func isElementBlocked(elem *rod.Element) (bool, error) {
	result, err := elem.Eval(`() => {
        const rect = this.getBoundingClientRect();
        if (rect.width === 0 || rect.height === 0) {
            return true;
        }
        const x = rect.left + rect.width / 2;
        const y = rect.top + rect.height / 2;
        const target = document.elementFromPoint(x, y);
        return !(target === this || this.contains(target));
    }`)
	if err != nil {
		return false, err
	}

	return result.Value.Bool(), nil
}

func uploadImages(ctx context.Context, page *rod.Page, imagesPaths []string) error {
	dbg := flowdebug.FromContext(ctx)

	// 验证文件路径有效性
	validPaths := make([]string, 0, len(imagesPaths))
	for _, path := range imagesPaths {
		if dbg != nil {
			_ = dbg.WaitIfPaused(ctx)
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			logrus.Warnf("图片文件不存在: %s", path)
			continue
		}
		validPaths = append(validPaths, path)
		logrus.Infof("获取有效图片：%s", path)
	}

	// 逐张上传：每张上传后等待预览出现，再上传下一张
	for i, path := range validPaths {
		if dbg != nil {
			dbg.Log("info", "上传图片", map[string]any{
				"index": i + 1,
				"total": len(validPaths),
				"path":  path,
			})
			_ = dbg.WaitIfPaused(ctx)
		}

		selector := `input[type="file"]`
		if i == 0 {
			selector = ".upload-input"
		}

		uploadInput, err := page.Element(selector)
		if err != nil {
			return errors.Wrapf(err, "查找上传输入框失败(第%d张)", i+1)
		}
		if err := uploadInput.SetFiles([]string{path}); err != nil {
			return errors.Wrapf(err, "上传第%d张图片失败", i+1)
		}

		slog.Info("图片已提交上传", "index", i+1, "path", path)

		// 等待当前图片上传完成（预览元素数量达到 i+1），最多等 60 秒
		if err := waitForUploadComplete(ctx, page, i+1); err != nil {
			return errors.Wrapf(err, "第%d张图片上传超时", i+1)
		}
		time.Sleep(1 * time.Second)
	}

	return nil
}

// waitForUploadComplete 等待第 expectedCount 张图片上传完成，最多等 60 秒
func waitForUploadComplete(ctx context.Context, page *rod.Page, expectedCount int) error {
	maxWaitTime := 60 * time.Second
	checkInterval := 500 * time.Millisecond
	start := time.Now()
	dbg := flowdebug.FromContext(ctx)

	lastCount := -1
	for time.Since(start) < maxWaitTime {
		if dbg != nil {
			_ = dbg.WaitIfPaused(ctx)
		}
		uploadedImages, err := page.Elements(".img-preview-area .pr")
		if err != nil {
			time.Sleep(checkInterval)
			continue
		}

		currentCount := len(uploadedImages)
		// 数量变化时才打印，避免刷屏
		if currentCount != lastCount {
			slog.Info("等待图片上传", "current", currentCount, "expected", expectedCount)
			if dbg != nil {
				dbg.Log("info", "图片上传进度", map[string]any{
					"current":  currentCount,
					"expected": expectedCount,
				})
			}
			lastCount = currentCount
		}
		if currentCount >= expectedCount {
			slog.Info("图片上传完成", "count", currentCount)
			return nil
		}

		time.Sleep(checkInterval)
	}

	return errors.Errorf("第%d张图片上传超时(60s)，请检查网络连接和图片大小", expectedCount)
}

func submitPublish(ctx context.Context, page *rod.Page, title, content string, tags []string, scheduleTime *time.Time, isOriginal bool, visibility string) error {
	dbg := flowdebug.FromContext(ctx)

	if dbg != nil {
		dbg.Step("填写标题", map[string]any{"title_len": len(strings.TrimSpace(title))})
		_ = dbg.WaitIfPaused(ctx)
	}
	titleElem, err := page.Element("div.d-input input")
	if err != nil {
		return errors.Wrap(err, "查找标题输入框失败")
	}
	if err := titleElem.Input(title); err != nil {
		return errors.Wrap(err, "输入标题失败")
	}

	// 检查标题长度
	time.Sleep(500 * time.Millisecond)
	if err := checkTitleMaxLength(page); err != nil {
		return err
	}
	slog.Info("检查标题长度：通过")

	time.Sleep(1 * time.Second)

	if dbg != nil {
		dbg.Step("填写正文/标签", map[string]any{
			"content_len": len(strings.TrimSpace(content)),
			"tags":        len(tags),
		})
		_ = dbg.WaitIfPaused(ctx)
	}
	contentElem, ok := getContentElement(page)
	if !ok {
		return errors.New("没有找到内容输入框")
	}
	if err := insertContentText(page, contentElem, content); err != nil {
		return errors.Wrap(err, "输入正文失败")
	}
	if err := waitAndClickTitleInput(page); err != nil {
		return err
	}
	if err := inputTags(page, tags); err != nil {
		return err
	}

	time.Sleep(1 * time.Second)

	// 检查正文长度
	if err := checkContentMaxLength(page); err != nil {
		return err
	}
	slog.Info("检查正文长度：通过")

	// 处理定时发布
	if scheduleTime != nil {
		if err := setSchedulePublish(page, *scheduleTime); err != nil {
			return errors.Wrap(err, "设置定时发布失败")
		}
		slog.Info("定时发布设置完成", "schedule_time", scheduleTime.Format("2006-01-02 15:04"))
	}

	// 设置可见范围
	if err := setVisibility(page, visibility); err != nil {
		return errors.Wrap(err, "设置可见范围失败")
	}

	// 处理原创声明
	if isOriginal {
		if err := setOriginal(page); err != nil {
			slog.Warn("设置原创声明失败，继续发布", "error", err)
		} else {
			slog.Info("已声明原创")
		}
	}
	if dbg != nil {
		dbg.Step("点击发布按钮", nil)
		_ = dbg.WaitIfPaused(ctx)
	}
	submitButton, err := page.Element(".publish-page-publish-btn button.bg-red")
	if err != nil {
		return errors.Wrap(err, "查找发布按钮失败")
	}
	if err := retryPublishStep("点击发布按钮", func() error {
		if err := submitButton.ScrollIntoView(); err != nil {
			logrus.Debugf("滚动到发布按钮失败: %v", err)
		}
		if err := submitButton.Click(proto.InputMouseButtonLeft, 1); err != nil {
			removePopCover(page)
			submitButton, err = page.Element(".publish-page-publish-btn button.bg-red")
			return err
		}
		return nil
	}); err != nil {
		return errors.Wrap(err, "点击发布按钮失败")
	}

	time.Sleep(3 * time.Second)
	return nil
}

func addProducts(ctx context.Context, page *rod.Page, productKeywords []string) error {
	keywords := make([]string, 0, len(productKeywords))
	for _, keyword := range productKeywords {
		trimmed := strings.TrimSpace(keyword)
		if trimmed != "" {
			keywords = append(keywords, trimmed)
		}
	}

	if len(keywords) == 0 {
		return nil
	}

	addButton, err := findAddProductButton(page)
	if err != nil {
		return errors.Wrap(err, "未找到添加商品入口")
	}

	if err := addButton.ScrollIntoView(); err != nil {
		logrus.Debugf("滚动到添加商品按钮失败: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := addButton.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return errors.Wrap(err, "点击添加商品按钮失败")
	}

	time.Sleep(500 * time.Millisecond)

	modal, err := waitForProductModal(page)
	if err != nil {
		return errors.Wrap(err, "打开商品选择弹窗失败")
	}

	if err := waitForProductListLoad(modal); err != nil {
		logrus.Warnf("等待商品列表加载失败: %v", err)
	}

	for _, keyword := range keywords {
		dbg := flowdebug.FromContext(ctx)
		if dbg != nil {
			dbg.Log("info", "搜索并选择商品", map[string]any{"keyword": keyword})
			_ = dbg.WaitIfPaused(ctx)
		}
		modal, err = waitForProductModal(page)
		if err != nil {
			return errors.Wrap(err, "打开商品选择弹窗失败")
		}
		searchInput, err := modal.Timeout(10 * time.Second).Element("input[placeholder='搜索商品ID 或 商品名称']")
		if err != nil {
			return errors.Wrap(err, "未找到商品搜索输入框")
		}
		if err := inputProductSearchKeyword(searchInput, keyword); err != nil {
			return errors.Wrapf(err, "搜索商品失败: %s", keyword)
		}

		// 直接通过 JavaScript 在 modal 上完成查找和勾选，避免元素引用失效
		if err := selectProductByKeyword(modal, keyword); err != nil {
			return errors.Wrapf(err, "选择商品失败: %s", keyword)
		}

		logrus.Infof("已选中商品: %s", keyword)
	}

	modal, err = waitForProductModal(page)
	if err != nil {
		return errors.Wrap(err, "打开商品选择弹窗失败")
	}
	saveButton, err := modal.ElementR("div.d-modal-footer button", "保存")
	if err != nil {
		return errors.Wrap(err, "未找到商品保存按钮")
	}

	if err := retryPublishStep("点击保存商品按钮", func() error {
		if err := saveButton.ScrollIntoView(); err != nil {
			logrus.Debugf("滚动到保存商品按钮失败: %v", err)
		}
		if clickErr := saveButton.Click(proto.InputMouseButtonLeft, 1); clickErr != nil {
			modal, err = waitForProductModal(page)
			if err == nil {
				saveButton, _ = modal.ElementR("div.d-modal-footer button", "保存")
			}
			return clickErr
		}
		return nil
	}); err != nil {
		return errors.Wrap(err, "点击保存商品按钮失败")
	}

	if err := waitForModalClose(page); err != nil {
		return err
	}

	return nil
}

func findAddProductButton(page *rod.Page) (*rod.Element, error) {
	selectors := []string{
		"div.multi-good-select-empty-btn button",
		"div.multi-good-select-add-btn button",
	}

	for _, selector := range selectors {
		elem, err := page.Element(selector)
		if err == nil {
			return elem, nil
		}
	}

	return page.ElementR("button", "添加商品")
}

func inputProductSearchKeyword(input *rod.Element, keyword string) error {
	if _, err := input.Eval(`() => {
        this.focus();
        this.value = '';
        this.dispatchEvent(new Event('input', { bubbles: true }));
    }`); err != nil {
		return err
	}

	if _, err := input.Eval(`(value) => {
        this.focus();
        this.value = value;
        this.dispatchEvent(new Event('input', { bubbles: true }));
        this.dispatchEvent(new Event('change', { bubbles: true }));
    }`, keyword); err != nil {
		return err
	}

	// 等待搜索结果加载和DOM稳定
	time.Sleep(1 * time.Second)

	return nil
}

func selectProductByKeyword(modal *rod.Element, keyword string) error {
	lowerKeyword := strings.ToLower(strings.TrimSpace(keyword))
	if lowerKeyword == "" {
		return errors.New("商品关键词不能为空")
	}

	const maxRetries = 5
	const retryInterval = 500 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		success, alreadyChecked, err := trySelectProduct(modal, lowerKeyword)
		if err != nil {
			lastErr = err
			logrus.Debugf("选择商品第%d次尝试失败: %s, error: %v", attempt, keyword, err)
			if attempt < maxRetries {
				time.Sleep(retryInterval)
			}
			continue
		}

		if success {
			if alreadyChecked {
				logrus.Debugf("商品已选中: %s", keyword)
				return nil
			}

			// 等待勾选状态生效并做校验
			if err := waitForProductChecked(modal, lowerKeyword, 2*time.Second); err != nil {
				lastErr = errors.Wrap(err, "勾选状态未生效")
				logrus.Debugf("选择商品第%d次尝试勾选状态未生效: %s", attempt, keyword)
				if attempt < maxRetries {
					time.Sleep(retryInterval)
				}
				continue
			}
			return nil
		}
	}

	if lastErr != nil {
		return errors.Wrapf(lastErr, "重试%d次后仍失败", maxRetries)
	}
	return errors.Errorf("重试%d次后未找到匹配商品: %s", maxRetries, keyword)
}

func trySelectProduct(modal *rod.Element, lowerKeyword string) (success bool, alreadyChecked bool, err error) {
	result, err := modal.Eval(`(keyword) => {
		const cards = this.querySelectorAll('.good-card-container');

		for (let card of cards) {
			const nameElem = card.querySelector('.sku-name');
			if (!nameElem) continue;

			const name = (nameElem.textContent || '').toLowerCase();
			const idText = (card.querySelector('.sku-id')?.textContent || '').toLowerCase();
			if (!name.includes(keyword) && !idText.includes(keyword)) continue;

			// 找到匹配的商品，检查是否已选中
			const checkbox = card.querySelector('input[type="checkbox"]');
			if (checkbox && checkbox.checked) {
				return { success: true, alreadyChecked: true };
			}

				// 查找并点击复选框容器（优先点击可交互区域）
				const checkboxContainer = card.querySelector('.d-checkbox-main') || card.querySelector('.d-checkbox');
				if (!checkboxContainer) {
					return { success: false, error: '未找到复选框容器' };
				}

				checkboxContainer.click();
				return { success: true, alreadyChecked: false };
		}

		return { success: false, error: '未找到匹配商品' };
	}`, lowerKeyword)

	if err != nil {
		return false, false, errors.Wrap(err, "执行选择脚本失败")
	}

	successVal := result.Value.Get("success").Bool()
	if !successVal {
		errorMsg := result.Value.Get("error").Str()
		if errorMsg == "" {
			errorMsg = "未知错误"
		}
		return false, false, errors.New(errorMsg)
	}

	return true, result.Value.Get("alreadyChecked").Bool(), nil
}

func waitForProductChecked(modal *rod.Element, lowerKeyword string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		found, checked, err := getProductCheckedState(modal, lowerKeyword)
		if err == nil && found && checked {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	found, checked, err := getProductCheckedState(modal, lowerKeyword)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("未找到匹配商品卡片")
	}
	if checked {
		return nil
	}
	return errors.New("复选框仍未处于选中状态")
}

func getProductCheckedState(modal *rod.Element, lowerKeyword string) (found bool, checked bool, err error) {
	result, err := modal.Eval(`(keyword) => {
		const cards = this.querySelectorAll('.good-card-container');
		for (let card of cards) {
			const nameElem = card.querySelector('.sku-name');
			if (!nameElem) continue;

			const name = (nameElem.textContent || '').toLowerCase();
			const idText = (card.querySelector('.sku-id')?.textContent || '').toLowerCase();
			if (!name.includes(keyword) && !idText.includes(keyword)) continue;

			const checkbox = card.querySelector('input[type="checkbox"]');
			return { found: true, checked: !!(checkbox && checkbox.checked) };
		}

		return { found: false, checked: false };
	}`, lowerKeyword)
	if err != nil {
		return false, false, err
	}

	return result.Value.Get("found").Bool(), result.Value.Get("checked").Bool(), nil
}

func waitForProductListLoad(modal *rod.Element) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		cards, err := modal.Elements(".good-card-container")
		if err == nil && len(cards) > 0 {
			for _, card := range cards {
				if isElementVisible(card) {
					return nil
				}
			}
		}

		emptyStates, err := modal.Elements(".goods-list-empty, .goods-list-search-empty")
		if err == nil {
			for _, empty := range emptyStates {
				if isElementVisible(empty) {
					return nil
				}
			}
		}

		time.Sleep(200 * time.Millisecond)
	}

	return errors.New("等待商品列表加载超时")
}

func waitForModalClose(page *rod.Page) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		has, _, err := page.Has("div.multi-goods-selector-modal")
		if err == nil && !has {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	return errors.New("关闭商品选择弹窗超时")
}

func waitForProductModal(page *rod.Page) (*rod.Element, error) {
	var modal *rod.Element
	var modalErr error
	if err := retryPublishStep("打开商品选择弹窗", func() error {
		modal, modalErr = page.Timeout(15 * time.Second).Element("div.multi-goods-selector-modal")
		return modalErr
	}); err != nil {
		return nil, err
	}
	return modal, nil
}

// waitAndClickTitleInput 在填写正文后等待 1 秒并回点标题输入框，增强后续交互稳定性
func waitAndClickTitleInput(page *rod.Page) error {
	slog.Info("正文填写完成，准备等待后回点标题输入框")
	time.Sleep(1 * time.Second)
	if err := retryPublishStep("回点标题输入框", func() error {
		titleElem, err := findTitleInput(page)
		if err != nil {
			return err
		}
		if err := titleElem.ScrollIntoView(); err != nil {
			logrus.Debugf("滚动到标题输入框失败: %v", err)
		}
		if err := titleElem.Click(proto.InputMouseButtonLeft, 1); err != nil {
			removePopCover(page)
			return err
		}
		return nil
	}); err != nil {
		return errors.Wrap(err, "回点标题输入框失败")
	}
	slog.Info("已回点标题输入框，继续后续发布流程")
	return nil
}

// 检查标题是否超过最大长度
func checkTitleMaxLength(page *rod.Page) error {
	has, elem, err := page.Has(`div.title-container div.max_suffix`)
	if err != nil {
		return errors.Wrap(err, "检查标题长度元素失败")
	}

	// 元素不存在，说明标题没超长
	if !has {
		return nil
	}

	// 元素存在，说明标题超长
	titleLength, err := elem.Text()
	if err != nil {
		return errors.Wrap(err, "获取标题长度文本失败")
	}

	return makeMaxLengthError(titleLength)
}

func checkContentMaxLength(page *rod.Page) error {
	var (
		has  bool
		elem *rod.Element
		err  error
	)
	if retryErr := retryPublishStep("检查正文长度元素", func() error {
		has, elem, err = page.Has(`div.edit-container div.length-error`)
		return err
	}); retryErr != nil {
		return errors.Wrap(err, "检查正文长度元素失败")
	}

	// 元素不存在，说明正文没超长
	if !has {
		return nil
	}

	// 元素存在，说明正文超长
	var contentLength string
	if retryErr := retryPublishStep("获取正文长度文本", func() error {
		contentLength, err = elem.Text()
		return err
	}); retryErr != nil {
		return errors.Wrap(err, "获取正文长度文本失败")
	}

	return makeMaxLengthError(contentLength)
}

func makeMaxLengthError(elemText string) error {
	parts := strings.Split(elemText, "/")
	if len(parts) != 2 {
		return errors.Errorf("长度超过限制: %s", elemText)
	}

	currLen, maxLen := parts[0], parts[1]

	return errors.Errorf("当前输入长度为%s，最大长度为%s", currLen, maxLen)
}

var contentElementSelectors = []string{
	"div.tiptap.ProseMirror[role='textbox'][contenteditable='true']",
	"div.ProseMirror[role='textbox'][contenteditable='true']",
	"div.ProseMirror[contenteditable='true']",
	"div[role='textbox'][contenteditable='true']",
	"div.tiptap.ProseMirror",
	"div.ql-editor",
	"textarea",
}

// 查找内容输入框 - 优先匹配真实可编辑容器，再回退到 placeholder 方案
func getContentElement(page *rod.Page) (*rod.Element, bool) {
	for _, selector := range contentElementSelectors {
		elements, err := page.Elements(selector)
		if err != nil {
			logrus.Debugf("查找正文输入框失败: selector=%s err=%v", selector, err)
			continue
		}
		for _, elem := range elements {
			if elem == nil || !isElementVisible(elem) {
				continue
			}
			slog.Debug("命中正文输入框选择器", "selector", selector)
			return elem, true
		}
	}

	if elem, err := findTextboxByPlaceholder(page); err == nil && elem != nil {
		slog.Debug("通过 placeholder 兜底找到正文输入框")
		return elem, true
	} else if err != nil {
		logrus.Debugf("通过 placeholder 查找正文输入框失败: %v", err)
	}

	slog.Warn("no content element found by any method")
	return nil, false
}

func inputTags(page *rod.Page, tags []string) error {
	if len(tags) == 0 {
		return nil
	}

	time.Sleep(1 * time.Second)

	if err := retryPublishStep("准备标签输入光标", func() error {
		contentElem, ok := getContentElement(page)
		if !ok {
			return errors.New("没有找到内容输入框")
		}
		if err := focusContentElement(contentElem); err != nil {
			return err
		}

		for index := 0; index < 20; index++ {
			ka, err := contentElem.KeyActions()
			if err != nil {
				return errors.Wrap(err, "创建键盘操作失败")
			}
			if err := ka.Type(input.ArrowDown).Do(); err != nil {
				return errors.Wrap(err, "按下方向键失败")
			}
			time.Sleep(10 * time.Millisecond)
		}

		ka, err := contentElem.KeyActions()
		if err != nil {
			return errors.Wrap(err, "创建键盘操作失败")
		}
		if err := ka.Press(input.Enter).Press(input.Enter).Do(); err != nil {
			return errors.Wrap(err, "按下回车键失败")
		}
		return nil
	}); err != nil {
		return err
	}

	time.Sleep(1 * time.Second)

	for _, tag := range tags {
		tag = strings.TrimLeft(tag, "#")
		if err := inputTag(page, tag); err != nil {
			return errors.Wrapf(err, "输入标签[%s]失败", tag)
		}
	}
	return nil
}

func inputTag(page *rod.Page, tag string) error {
	if err := insertContentTextWithRetry(page, "#"); err != nil {
		return errors.Wrap(err, "输入#失败")
	}
	time.Sleep(200 * time.Millisecond)

	for _, char := range tag {
		if err := insertContentTextWithRetry(page, string(char)); err != nil {
			return errors.Wrapf(err, "输入字符[%c]失败", char)
		}
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(1 * time.Second)

	topicContainer, err := page.Element("#creator-editor-topic-container")
	if err != nil || topicContainer == nil {
		slog.Warn("未找到标签联想下拉框，直接输入空格", "tag", tag)
		return insertContentTextWithRetry(page, " ")
	}

	firstItem, err := topicContainer.Element(".item")
	if err != nil || firstItem == nil {
		slog.Warn("未找到标签联想选项，直接输入空格", "tag", tag)
		return insertContentTextWithRetry(page, " ")
	}

	if err := firstItem.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return errors.Wrap(err, "点击标签联想选项失败")
	}
	slog.Info("成功点击标签联想选项", "tag", tag)
	time.Sleep(200 * time.Millisecond)

	time.Sleep(500 * time.Millisecond) // 等待标签处理完成
	return nil
}

func insertContentText(page *rod.Page, contentElem *rod.Element, text string) error {
	if err := focusContentElement(contentElem); err != nil {
		return err
	}
	if err := page.InsertText(text); err == nil {
		return nil
	}
	return contentElem.Input(text)
}

func insertContentTextWithRetry(page *rod.Page, text string) error {
	return retryPublishStep("输入正文/标签文本", func() error {
		contentElem, ok := getContentElement(page)
		if !ok {
			return errors.New("没有找到内容输入框")
		}
		return insertContentText(page, contentElem, text)
	})
}

func focusContentElement(contentElem *rod.Element) error {
	if err := contentElem.ScrollIntoView(); err != nil {
		logrus.Debugf("滚动到正文输入框失败: %v", err)
	}
	_, err := contentElem.Eval(`() => {
		this.focus();
		if (this.isContentEditable) {
			const selection = window.getSelection();
			if (selection) {
				const range = document.createRange();
				range.selectNodeContents(this);
				range.collapse(false);
				selection.removeAllRanges();
				selection.addRange(range);
			}
			return;
		}
		if (typeof this.setSelectionRange === 'function') {
			const value = typeof this.value === 'string' ? this.value : '';
			this.setSelectionRange(value.length, value.length);
		}
	}`)
	if err != nil {
		return errors.Wrap(err, "聚焦正文输入框失败")
	}
	return nil
}

func findTitleInput(page *rod.Page) (*rod.Element, error) {
	return page.Element("div.d-input input")
}

func retryPublishStep(action string, fn func() error) error {
	var lastErr error
	for attempt := 1; attempt <= publishStepRetryCount; attempt++ {
		if err := fn(); err != nil {
			lastErr = err
			if attempt == publishStepRetryCount || !isRetryablePublishError(err) {
				return err
			}
			slog.Warn("发布步骤失败，准备重试", "action", action, "attempt", attempt, "error", err)
			time.Sleep(publishStepRetryDelay)
			continue
		}
		return nil
	}
	return lastErr
}

func isRetryablePublishError(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	retryablePatterns := []string{
		"context canceled",
		"context deadline exceeded",
		"cannot find context",
		"execution context",
		"node is detached",
		"cannot find node",
	}

	for _, pattern := range retryablePatterns {
		if strings.Contains(message, pattern) {
			return true
		}
	}

	return false
}

func findTextboxByPlaceholder(page *rod.Page) (*rod.Element, error) {
	elements, err := page.Elements("[data-placeholder]")
	if err != nil {
		return nil, errors.Wrap(err, "查询 data-placeholder 元素失败")
	}
	if len(elements) == 0 {
		return nil, errors.New("no data-placeholder elements found")
	}

	// 查找包含指定placeholder的元素
	placeholderElem := findPlaceholderElement(elements, "输入正文描述")
	if placeholderElem == nil {
		return nil, errors.New("no placeholder element found")
	}

	// 向上查找 textbox / contenteditable 父元素
	textboxElem := findTextboxParent(placeholderElem)
	if textboxElem == nil {
		return nil, errors.New("no textbox parent found")
	}

	return textboxElem, nil
}

func findPlaceholderElement(elements []*rod.Element, searchText string) *rod.Element {
	for _, elem := range elements {
		placeholder, err := elem.Attribute("data-placeholder")
		if err != nil || placeholder == nil {
			continue
		}

		if strings.Contains(*placeholder, searchText) {
			return elem
		}
	}
	return nil
}

func findTextboxParent(elem *rod.Element) *rod.Element {
	currentElem := elem
	for i := 0; i < 8; i++ {
		parent, err := currentElem.Parent()
		if err != nil {
			break
		}

		role, err := parent.Attribute("role")
		if err == nil && role != nil && *role == "textbox" {
			return parent
		}

		contenteditable, err := parent.Attribute("contenteditable")
		if err == nil && contenteditable != nil && *contenteditable == "true" {
			return parent
		}

		currentElem = parent
	}
	return nil
}

// isElementVisible 检查元素是否可见
func isElementVisible(elem *rod.Element) bool {

	// 检查是否有隐藏样式
	style, err := elem.Attribute("style")
	if err == nil && style != nil {
		styleStr := *style

		if strings.Contains(styleStr, "left: -9999px") ||
			strings.Contains(styleStr, "top: -9999px") ||
			strings.Contains(styleStr, "position: absolute; left: -9999px") ||
			strings.Contains(styleStr, "display: none") ||
			strings.Contains(styleStr, "visibility: hidden") {
			return false
		}
	}

	visible, err := elem.Visible()
	if err != nil {
		slog.Warn("无法获取元素可见性", "error", err)
		return true
	}

	return visible
}

// setVisibility 设置可见范围
// 支持: "公开可见"(默认), "仅自己可见", "仅互关好友可见"
func setVisibility(page *rod.Page, visibility string) error {
	if visibility == "" || visibility == "公开可见" {
		slog.Info("可见范围使用默认：公开可见")
		return nil
	}

	// 支持的选项校验
	supported := map[string]bool{"仅自己可见": true, "仅互关好友可见": true}
	if !supported[visibility] {
		return errors.Errorf("不支持的可见范围: %s，支持: 公开可见、仅自己可见、仅互关好友可见", visibility)
	}

	// 点击可见范围下拉框
	dropdown, err := page.Element("div.permission-card-wrapper div.d-select-content")
	if err != nil {
		return errors.Wrap(err, "查找可见范围下拉框失败")
	}
	if err := dropdown.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return errors.Wrap(err, "点击可见范围下拉框失败")
	}
	time.Sleep(500 * time.Millisecond)

	// 在弹窗中查找并点击目标选项
	opts, err := page.Elements("div.d-options-wrapper div.d-grid-item div.custom-option")
	if err != nil {
		return errors.Wrap(err, "查找可见范围选项失败")
	}
	for _, opt := range opts {
		text, err := opt.Text()
		if err != nil {
			continue
		}
		if strings.Contains(text, visibility) {
			if err := opt.Click(proto.InputMouseButtonLeft, 1); err != nil {
				return errors.Wrap(err, "选择可见范围失败")
			}
			slog.Info("已设置可见范围", "visibility", visibility)
			time.Sleep(200 * time.Millisecond)
			return nil
		}
	}
	return errors.Errorf("未找到可见范围选项: %s", visibility)
}

// setSchedulePublish 设置定时发布时间
func setSchedulePublish(page *rod.Page, t time.Time) error {
	// 1. 点击定时发布开关
	if err := clickScheduleSwitch(page); err != nil {
		return err
	}
	time.Sleep(800 * time.Millisecond)

	// 2. 设置日期时间
	if err := setDateTime(page, t); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)

	return nil
}

// clickScheduleSwitch 点击定时发布开关
func clickScheduleSwitch(page *rod.Page) error {
	switchElem, err := page.Element(".post-time-wrapper .d-switch")
	if err != nil {
		return errors.Wrap(err, "查找定时发布开关失败")
	}

	if err := switchElem.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return errors.Wrap(err, "点击定时发布开关失败")
	}
	slog.Info("已点击定时发布开关")
	return nil
}

// setDateTime 设置日期时间
func setDateTime(page *rod.Page, t time.Time) error {
	dateTimeStr := t.Format("2006-01-02 15:04")

	input, err := page.Element(".date-picker-container input")
	if err != nil {
		return errors.Wrap(err, "查找日期时间输入框失败")
	}

	if err := input.SelectAllText(); err != nil {
		return errors.Wrap(err, "选择日期时间文本失败")
	}
	if err := input.Input(dateTimeStr); err != nil {
		return errors.Wrap(err, "输入日期时间失败")
	}
	slog.Info("已设置日期时间", "datetime", dateTimeStr)

	return nil
}

// setOriginal 设置原创声明
func setOriginal(page *rod.Page) error {
	// 根据小红书创作者页面的实际结构：
	// div.custom-switch-card 包含 span.has-tips 文本为"原创声明"
	// 开关是 div.d-switch 组件

	// 查找包含"原创声明"文本的 custom-switch-card
	switchCards, err := page.Elements("div.custom-switch-card")
	if err != nil {
		return errors.Wrap(err, "查找原创声明卡片失败")
	}

	for _, card := range switchCards {
		text, err := card.Text()
		if err != nil {
			continue
		}

		// 检查是否是原创声明卡片
		if !strings.Contains(text, "原创声明") {
			continue
		}

		// 找到原创声明卡片，查找其中的 d-switch
		switchElem, err := card.Element("div.d-switch")
		if err != nil {
			continue
		}

		// 检查开关是否已打开
		checked, err := switchElem.Eval(`() => {
			const input = this.querySelector('input[type="checkbox"]');
			return input ? input.checked : false;
		}`)
		if err != nil {
			continue
		}

		if checked.Value.Bool() {
			slog.Info("原创声明已开启")
			return nil
		}

		// 点击开关
		if err := switchElem.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return errors.Wrap(err, "点击原创声明开关失败")
		}

		time.Sleep(500 * time.Millisecond)

		// 处理原创声明确认弹窗
		if err := confirmOriginalDeclaration(page); err != nil {
			return errors.Wrap(err, "确认原创声明失败")
		}

		slog.Info("已开启原创声明")
		return nil
	}

	return errors.New("未找到原创声明选项")
}

// confirmOriginalDeclaration 处理原创声明确认弹窗
func confirmOriginalDeclaration(page *rod.Page) error {
	// 等待确认弹窗出现
	time.Sleep(800 * time.Millisecond)

	// 使用 JavaScript 直接处理弹窗，更可靠
	result, err := page.Eval(`
		() => {
			// 查找包含"原创声明须知"的 footer 区域
			const footers = document.querySelectorAll('div.footer');
			for (const footer of footers) {
				// 检查是否包含原创声明相关内容
				if (!footer.textContent.includes('原创声明须知')) {
					continue;
				}

				// 找到 checkbox 并勾选
				const checkbox = footer.querySelector('div.d-checkbox input[type="checkbox"]');
				if (checkbox && !checkbox.checked) {
					checkbox.click();
					console.log('已勾选原创声明须知 checkbox');
				}

				// 等待一下让按钮变为可用
				return 'found_footer';
			}
			return 'footer_not_found';
		}
	`)
	if err != nil {
		slog.Warn("执行查找弹窗脚本失败", "error", err)
	} else if result.Value.String() == "footer_not_found" {
		slog.Warn("未找到原创声明确认弹窗的 footer")
	}

	time.Sleep(500 * time.Millisecond)

	// 再次使用 JavaScript 点击声明原创按钮
	result2, err := page.Eval(`
		() => {
			const footers = document.querySelectorAll('div.footer');
			for (const footer of footers) {
				if (!footer.textContent.includes('声明原创')) {
					continue;
				}

				// 找到声明原创按钮
				const btn = footer.querySelector('button.custom-button');
				if (btn) {
					// 检查是否禁用
					if (btn.classList.contains('disabled') || btn.disabled) {
						// 尝试再次勾选 checkbox
						const checkbox = footer.querySelector('div.d-checkbox input[type="checkbox"]');
						if (checkbox && !checkbox.checked) {
							checkbox.click();
						}
						return 'button_disabled';
					}
					btn.click();
					return 'clicked';
				}
			}
			return 'button_not_found';
		}
	`)
	if err != nil {
		return errors.Wrap(err, "执行点击按钮脚本失败")
	}

	status := result2.Value.String()
	slog.Info("原创声明确认结果", "status", status)

	if status == "button_not_found" {
		return errors.New("未找到声明原创按钮")
	}
	if status == "button_disabled" {
		return errors.New("声明原创按钮仍处于禁用状态")
	}

	slog.Info("已成功点击声明原创按钮")
	time.Sleep(300 * time.Millisecond)

	return nil
}
