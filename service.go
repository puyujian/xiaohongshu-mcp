package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
	"github.com/xpzouying/xiaohongshu-mcp/pkg/downloader"
	"github.com/xpzouying/xiaohongshu-mcp/pkg/flowdebug"
	"github.com/xpzouying/xiaohongshu-mcp/pkg/proxyutil"
	"github.com/xpzouying/xiaohongshu-mcp/pkg/xhsutil"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

const (
	publishExecutionTimeout = 10 * time.Minute
)

func newPublishExecutionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), publishExecutionTimeout)
}

// XiaohongshuService 小红书业务服务
type XiaohongshuService struct {
	activeLoginPage *rod.Page
	loginPageMu     sync.RWMutex

	// 共享浏览器：同一份 UserDataDir 只能被一个 Chrome 进程使用
	browserMu          sync.Mutex
	sharedBrowser      *browser.Browser
	sharedBrowserProxy string

	// 可视化调试：发布流程会话（步骤/网络/控制台/暂停）
	flowDebug *FlowDebugCenter
}

// NewXiaohongshuService 创建小红书服务实例
func NewXiaohongshuService() *XiaohongshuService {
	return &XiaohongshuService{
		flowDebug: NewFlowDebugCenter(flowDebugDefaultMaxSessions),
	}
}

// Close 在服务退出时调用，统一释放共享 Chrome 进程
func (s *XiaohongshuService) Close() {
	b := s.dropSharedBrowser()
	if b != nil {
		b.Close()
	}
}

// ListFlowDebugSessions 获取最近的调试会话列表（最新在前）。
func (s *XiaohongshuService) ListFlowDebugSessions() []FlowDebugSessionMeta {
	if s.flowDebug == nil {
		return nil
	}
	return s.flowDebug.List()
}

func (s *XiaohongshuService) GetFlowDebugSession(id string) (*FlowDebugSession, bool) {
	if s.flowDebug == nil {
		return nil, false
	}
	return s.flowDebug.Get(id)
}

// PublishRequest 发布请求
type PublishRequest struct {
	Title      string   `json:"title" binding:"required"`
	Content    string   `json:"content" binding:"required"`
	Images     []string `json:"images" binding:"required,min=1"`
	Tags       []string `json:"tags,omitempty"`
	Products   []string `json:"products,omitempty"`    // 商品关键词列表，用于绑定带货商品
	ScheduleAt string   `json:"schedule_at,omitempty"` // 定时发布时间，ISO8601格式，为空则立即发布
	IsOriginal bool     `json:"is_original,omitempty"` // 是否声明原创
	Visibility string   `json:"visibility,omitempty"`  // 可见范围: "公开可见"(默认), "仅自己可见", "仅互关好友可见"
}

// LoginStatusResponse 登录状态响应
type LoginStatusResponse struct {
	IsLoggedIn bool   `json:"is_logged_in"`
	Username   string `json:"username,omitempty"`
}

// LoginQrcodeResponse 登录扫码二维码
type LoginQrcodeResponse struct {
	Timeout    string `json:"timeout"`
	IsLoggedIn bool   `json:"is_logged_in"`
	Img        string `json:"img,omitempty"`
}

// PublishResponse 发布响应
type PublishResponse struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Images  int    `json:"images"`
	Status  string `json:"status"`
	PostID  string `json:"post_id,omitempty"`
}

// PublishVideoRequest 发布视频请求（仅支持本地单个视频文件）
type PublishVideoRequest struct {
	Title      string   `json:"title" binding:"required"`
	Content    string   `json:"content" binding:"required"`
	Video      string   `json:"video" binding:"required"`
	Tags       []string `json:"tags,omitempty"`
	Products   []string `json:"products,omitempty"`    // 商品关键词列表，用于绑定带货商品
	ScheduleAt string   `json:"schedule_at,omitempty"` // 定时发布时间，ISO8601格式，为空则立即发布
	Visibility string   `json:"visibility,omitempty"`  // 可见范围: "公开可见"(默认), "仅自己可见", "仅互关好友可见"
}

// PublishVideoResponse 发布视频响应
type PublishVideoResponse struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Video   string `json:"video"`
	Status  string `json:"status"`
	PostID  string `json:"post_id,omitempty"`
}

// FeedsListResponse Feeds列表响应
type FeedsListResponse struct {
	Feeds []xiaohongshu.Feed `json:"feeds"`
	Count int                `json:"count"`
}

// UserProfileResponse 用户主页响应
type UserProfileResponse struct {
	UserBasicInfo xiaohongshu.UserBasicInfo      `json:"userBasicInfo"`
	Interactions  []xiaohongshu.UserInteractions `json:"interactions"`
	Feeds         []xiaohongshu.Feed             `json:"feeds"`
}

// NotificationMentionsResponse 评论和@通知响应
type NotificationMentionsResponse struct {
	Notifications  []xiaohongshu.NotificationMention `json:"notifications"`
	Count          int                               `json:"count"`
	Cursor         string                            `json:"cursor,omitempty"`
	HasMore        bool                              `json:"has_more"`
	SourceEndpoint string                            `json:"source_endpoint"`
}

// DeleteCookies 删除 cookies 文件，用于登录重置
func (s *XiaohongshuService) DeleteCookies(ctx context.Context) error {
	cookiePath := cookies.GetCookiesFilePath()
	cookieLoader := cookies.NewLoadCookie(cookiePath)
	return cookieLoader.DeleteCookies()
}

// CheckLoginStatus 检查登录状态
func (s *XiaohongshuService) CheckLoginStatus(ctx context.Context) (*LoginStatusResponse, error) {
	return runLoginPublishWithRetry(s, ctx, "登录状态检查", s.checkLoginStatusOnce)
}

func (s *XiaohongshuService) checkLoginStatusOnce(ctx context.Context, proxy string) (*LoginStatusResponse, error) {
	// 检查是否有活跃的登录会话
	s.loginPageMu.RLock()
	activePage := s.activeLoginPage
	s.loginPageMu.RUnlock()

	if activePage != nil {
		// 有活跃的登录会话，直接在当前页面检查状态（不重新导航，避免干扰登录流程）
		exists, _, _ := activePage.Has(`.main-container .user .link-wrapper .channel`)
		return &LoginStatusResponse{
			IsLoggedIn: exists,
			Username:   configs.Username,
		}, nil
	}

	// 没有活跃页面，使用共享浏览器检查
	b, err := s.getBrowser(proxy)
	if err != nil {
		return nil, err
	}

	page := b.NewPage()
	defer page.Close()

	loginAction := xiaohongshu.NewLogin(page)

	isLoggedIn, err := loginAction.CheckLoginStatus(ctx)
	if err != nil {
		return nil, err
	}

	response := &LoginStatusResponse{
		IsLoggedIn: isLoggedIn,
		Username:   configs.Username,
	}

	return response, nil
}

// GetLoginQrcode 获取登录的扫码二维码
func (s *XiaohongshuService) GetLoginQrcode(ctx context.Context) (*LoginQrcodeResponse, error) {
	return runLoginPublishWithRetry(s, ctx, "登录二维码获取", s.getLoginQrcodeOnce)
}

func (s *XiaohongshuService) getLoginQrcodeOnce(ctx context.Context, proxy string) (*LoginQrcodeResponse, error) {
	b, err := s.getBrowser(proxy)
	if err != nil {
		return nil, err
	}
	page := b.NewPage()

	// 注册为活跃页面，供调试交互使用
	s.loginPageMu.Lock()
	s.activeLoginPage = page
	s.loginPageMu.Unlock()

	deferFunc := func() {
		s.loginPageMu.Lock()
		if s.activeLoginPage == page {
			s.activeLoginPage = nil
		}
		s.loginPageMu.Unlock()
		_ = page.Close()
		// 共享浏览器不关闭，在服务退出时统一释放
	}

	loginAction := xiaohongshu.NewLogin(page)

	img, loggedIn, err := loginAction.FetchQrcodeImage(ctx)
	if err != nil || loggedIn {
		defer deferFunc()
	}
	if err != nil {
		return nil, err
	}

	timeout := 4 * time.Minute

	if !loggedIn {
		go func() {
			ctxTimeout, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			defer deferFunc()

			if loginAction.WaitForLogin(ctxTimeout) {
				if er := saveCookies(page); er != nil {
					logrus.Errorf("failed to save cookies: %v", er)
				}
			}
		}()
	}

	return &LoginQrcodeResponse{
		Timeout: func() string {
			if loggedIn {
				return "0s"
			}
			return timeout.String()
		}(),
		Img:        img,
		IsLoggedIn: loggedIn,
	}, nil
}

// GetLoginBrowserScreenshot 获取登录页面的截图
func (s *XiaohongshuService) GetLoginBrowserScreenshot(ctx context.Context) ([]byte, error) {
	s.loginPageMu.RLock()
	page := s.activeLoginPage
	s.loginPageMu.RUnlock()

	if page == nil {
		return nil, fmt.Errorf("browser not active")
	}
	// 使用 false 只截取可见视口，确保坐标映射准确
	return page.Screenshot(false, nil)
}

// ProcessLoginBrowserAction 处理浏览器交互动作
func (s *XiaohongshuService) ProcessLoginBrowserAction(ctx context.Context, actionType string, x, y float64, text string) error {
	s.loginPageMu.RLock()
	page := s.activeLoginPage
	s.loginPageMu.RUnlock()

	if page == nil {
		return fmt.Errorf("browser not active")
	}

	switch actionType {
	case "click":
		if err := page.Mouse.MoveTo(proto.Point{X: x, Y: y}); err != nil {
			return err
		}
		return page.Mouse.Click(proto.InputMouseButtonLeft, 1)
	case "input":
		return page.InsertText(text)
	default:
		return fmt.Errorf("unknown action: %s", actionType)
	}
}

// PublishContent 发布内容
func (s *XiaohongshuService) PublishContent(ctx context.Context, req *PublishRequest) (*PublishResponse, error) {
	// 创建调试会话（无论是否打开 UI，均记录最近一次流程，便于排查发布失败原因）
	sess := s.flowDebug.NewSession("publish_image")
	dbgCtx := flowdebug.WithDebugger(ctx, sess)
	sess.Step("收到发布图文请求", map[string]any{
		"images":   len(req.Images),
		"tags":     len(req.Tags),
		"products": len(req.Products),
	})

	var endErr error
	defer func() { sess.End(endErr) }()

	// 验证标题长度（小红书限制：最大20个字）
	if xhsutil.CalcTitleLength(req.Title) > 20 {
		endErr = fmt.Errorf("标题长度超过限制")
		return nil, endErr
	}

	// 非浏览器准备阶段：这里保持直连，避免在代理有效期内消耗下载/校验时间
	prepared, err := s.preparePublishContent(req, sess)
	if err != nil {
		endErr = err
		return nil, err
	}

	resp, err := runLoginPublishWithRetry(s, dbgCtx, "图文发布", func(ctx context.Context, proxy string) (*PublishResponse, error) {
		sess.Step("进入发布页并执行自动化流程", map[string]any{
			"images":           len(prepared.Content.ImagePaths),
			"proxy_deferred":   true,
			"prepare_direct":   true,
			"browser_viaProxy": strings.TrimSpace(proxy) != "",
		})

		publishCtx, cancel := newPublishExecutionContext(ctx)
		defer cancel()

		err := s.publishContent(publishCtx, prepared.Content, sess, proxy)
		if err != nil {
			message, details := explainPublishError("图文笔记发布", err)
			logrus.WithFields(logrus.Fields{
				"title":      prepared.Content.Title,
				"step":       details.Step,
				"reason":     details.Reason,
				"raw_error":  details.RawError,
				"suggestion": details.Suggestion,
			}).Error(message)
			return nil, err
		}

		return prepared.Response, nil
	})
	endErr = err
	return resp, err
}

// publishContent 执行内容发布
func (s *XiaohongshuService) publishContent(ctx context.Context, content xiaohongshu.PublishImageContent, sess *FlowDebugSession, proxy string) error {
	b, err := s.getBrowser(proxy)
	if err != nil {
		return err
	}

	page := b.NewPage()
	if sess != nil {
		sess.AttachPage(page)
	}
	defer func() {
		if sess != nil {
			sess.DetachPage()
		}
		_ = page.Close()
	}()

	if sess != nil {
		sess.Step("打开发布页面", map[string]any{"url": "https://creator.xiaohongshu.com/publish/publish"})
	}
	action, err := xiaohongshu.NewPublishImageAction(page)
	if err != nil {
		return err
	}

	// 执行发布
	return action.Publish(ctx, content)
}

type preparedPublishContent struct {
	Content  xiaohongshu.PublishImageContent
	Response *PublishResponse
}

type preparedPublishVideo struct {
	Content  xiaohongshu.PublishVideoContent
	Response *PublishVideoResponse
}

func (s *XiaohongshuService) preparePublishContent(req *PublishRequest, sess *FlowDebugSession) (*preparedPublishContent, error) {
	// 所有非浏览器准备工作保持直连，尽量把代理窗口缩短到真正发布阶段。
	sess.Step("处理图片", map[string]any{"count": len(req.Images), "direct_prepare": true})
	imagePaths, err := s.processImages(req.Images)
	if err != nil {
		return nil, err
	}

	scheduleTime, err := parseScheduleTime(req.ScheduleAt)
	if err != nil {
		return nil, err
	}

	content := xiaohongshu.PublishImageContent{
		Title:        req.Title,
		Content:      req.Content,
		Tags:         req.Tags,
		Products:     req.Products,
		ImagePaths:   imagePaths,
		ScheduleTime: scheduleTime,
		IsOriginal:   req.IsOriginal,
		Visibility:   req.Visibility,
	}

	response := &PublishResponse{
		Title:   req.Title,
		Content: req.Content,
		Images:  len(imagePaths),
		Status:  "发布完成",
	}

	return &preparedPublishContent{
		Content:  content,
		Response: response,
	}, nil
}

func (s *XiaohongshuService) preparePublishVideo(req *PublishVideoRequest, sess *FlowDebugSession) (*preparedPublishVideo, error) {
	sess.Step("校验视频与发布时间", map[string]any{"direct_prepare": true})
	if req.Video == "" {
		return nil, fmt.Errorf("必须提供本地视频文件")
	}
	if _, err := os.Stat(req.Video); err != nil {
		return nil, fmt.Errorf("视频文件不存在或不可访问: %v", err)
	}

	scheduleTime, err := parseScheduleTime(req.ScheduleAt)
	if err != nil {
		return nil, err
	}

	content := xiaohongshu.PublishVideoContent{
		Title:        req.Title,
		Content:      req.Content,
		Tags:         req.Tags,
		Products:     req.Products,
		VideoPath:    req.Video,
		ScheduleTime: scheduleTime,
		Visibility:   req.Visibility,
	}

	resp := &PublishVideoResponse{
		Title:   req.Title,
		Content: req.Content,
		Video:   req.Video,
		Status:  "发布完成",
	}

	return &preparedPublishVideo{
		Content:  content,
		Response: resp,
	}, nil
}

func parseScheduleTime(scheduleAt string) (*time.Time, error) {
	if scheduleAt == "" {
		return nil, nil
	}

	t, err := time.Parse(time.RFC3339, scheduleAt)
	if err != nil {
		return nil, fmt.Errorf("定时发布时间格式错误，请使用 ISO8601 格式: %v", err)
	}

	now := time.Now()
	minTime := now.Add(1 * time.Hour)
	maxTime := now.Add(14 * 24 * time.Hour)

	if t.Before(minTime) {
		return nil, fmt.Errorf("定时发布时间必须至少在1小时后，当前设置: %s，最早可选: %s",
			t.Format("2006-01-02 15:04"), minTime.Format("2006-01-02 15:04"))
	}
	if t.After(maxTime) {
		return nil, fmt.Errorf("定时发布时间不能超过14天，当前设置: %s，最晚可选: %s",
			t.Format("2006-01-02 15:04"), maxTime.Format("2006-01-02 15:04"))
	}

	logrus.Infof("设置定时发布时间: %s", t.Format("2006-01-02 15:04"))
	return &t, nil
}

// processImages 处理图片列表，支持URL下载和本地路径。
// 这里固定使用直连，避免在代理有效期内消耗下载和本地预处理时间。
func (s *XiaohongshuService) processImages(images []string) ([]string, error) {
	processor := downloader.NewImageProcessor()
	return processor.ProcessImages(images)
}

// PublishVideo 发布视频（本地文件）
func (s *XiaohongshuService) PublishVideo(ctx context.Context, req *PublishVideoRequest) (*PublishVideoResponse, error) {
	sess := s.flowDebug.NewSession("publish_video")
	dbgCtx := flowdebug.WithDebugger(ctx, sess)
	sess.Step("收到发布视频请求", map[string]any{
		"tags":     len(req.Tags),
		"products": len(req.Products),
	})

	var endErr error
	defer func() { sess.End(endErr) }()

	// 标题长度校验（小红书限制：最大20个字）
	if xhsutil.CalcTitleLength(req.Title) > 20 {
		endErr = fmt.Errorf("标题长度超过限制")
		return nil, endErr
	}

	prepared, err := s.preparePublishVideo(req, sess)
	if err != nil {
		endErr = err
		return nil, err
	}

	resp, err := runLoginPublishWithRetry(s, dbgCtx, "视频发布", func(ctx context.Context, proxy string) (*PublishVideoResponse, error) {
		sess.Step("进入发布页并执行自动化流程", map[string]any{
			"proxy_deferred":   true,
			"prepare_direct":   true,
			"browser_viaProxy": strings.TrimSpace(proxy) != "",
		})

		publishCtx, cancel := newPublishExecutionContext(ctx)
		defer cancel()

		err := s.publishVideo(publishCtx, prepared.Content, sess, proxy)
		if err != nil {
			message, details := explainPublishError("视频笔记发布", err)
			logrus.WithFields(logrus.Fields{
				"title":      prepared.Content.Title,
				"step":       details.Step,
				"reason":     details.Reason,
				"raw_error":  details.RawError,
				"suggestion": details.Suggestion,
			}).Error(message)
			return nil, err
		}

		return prepared.Response, nil
	})
	endErr = err
	return resp, err
}

// publishVideo 执行视频发布
func (s *XiaohongshuService) publishVideo(ctx context.Context, content xiaohongshu.PublishVideoContent, sess *FlowDebugSession, proxy string) error {
	b, err := s.getBrowser(proxy)
	if err != nil {
		return err
	}

	page := b.NewPage()
	if sess != nil {
		sess.AttachPage(page)
	}
	defer func() {
		if sess != nil {
			sess.DetachPage()
		}
		_ = page.Close()
	}()

	if sess != nil {
		sess.Step("打开发布页面", map[string]any{"url": "https://creator.xiaohongshu.com/publish/publish"})
	}
	action, err := xiaohongshu.NewPublishVideoAction(page)
	if err != nil {
		return err
	}

	return action.PublishVideo(ctx, content)
}

// ListFeeds 获取Feeds列表
func (s *XiaohongshuService) ListFeeds(ctx context.Context) (*FeedsListResponse, error) {
	b, err := s.getBrowser("")
	if err != nil {
		return nil, err
	}

	page := b.NewPage()
	defer page.Close()

	// 创建 Feeds 列表 action
	action := xiaohongshu.NewFeedsListAction(page)

	// 获取 Feeds 列表
	feeds, err := action.GetFeedsList(ctx)
	if err != nil {
		logrus.Errorf("获取 Feeds 列表失败: %v", err)
		return nil, err
	}

	response := &FeedsListResponse{
		Feeds: feeds,
		Count: len(feeds),
	}

	return response, nil
}

func (s *XiaohongshuService) SearchFeeds(ctx context.Context, keyword string, filters ...xiaohongshu.FilterOption) (*FeedsListResponse, error) {
	b, err := s.getBrowser("")
	if err != nil {
		return nil, err
	}

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewSearchAction(page)

	feeds, err := action.Search(ctx, keyword, filters...)
	if err != nil {
		return nil, err
	}

	response := &FeedsListResponse{
		Feeds: feeds,
		Count: len(feeds),
	}

	return response, nil
}

// GetFeedDetail 获取Feed详情
func (s *XiaohongshuService) GetFeedDetail(ctx context.Context, feedID, xsecToken string, loadAllComments bool) (*FeedDetailResponse, error) {
	return s.GetFeedDetailWithConfig(ctx, feedID, xsecToken, loadAllComments, xiaohongshu.DefaultCommentLoadConfig())
}

// GetFeedDetailWithConfig 使用配置获取Feed详情
func (s *XiaohongshuService) GetFeedDetailWithConfig(ctx context.Context, feedID, xsecToken string, loadAllComments bool, config xiaohongshu.CommentLoadConfig) (*FeedDetailResponse, error) {
	b, err := s.getBrowser("")
	if err != nil {
		return nil, err
	}

	page := b.NewPage()
	defer page.Close()

	// 创建 Feed 详情 action
	action := xiaohongshu.NewFeedDetailAction(page)

	// 获取 Feed 详情
	result, err := action.GetFeedDetailWithConfig(ctx, feedID, xsecToken, loadAllComments, config)
	if err != nil {
		return nil, err
	}

	response := &FeedDetailResponse{
		FeedID: feedID,
		Data:   result,
	}

	return response, nil
}

// UserProfile 获取用户信息
func (s *XiaohongshuService) UserProfile(ctx context.Context, userID, xsecToken string) (*UserProfileResponse, error) {
	b, err := s.getBrowser("")
	if err != nil {
		return nil, err
	}

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewUserProfileAction(page)

	result, err := action.UserProfile(ctx, userID, xsecToken)
	if err != nil {
		return nil, err
	}
	response := &UserProfileResponse{
		UserBasicInfo: result.UserBasicInfo,
		Interactions:  result.Interactions,
		Feeds:         result.Feeds,
	}

	return response, nil

}

// GetNotificationMentions 获取当前登录账号的“评论和@”通知
func (s *XiaohongshuService) GetNotificationMentions(ctx context.Context) (*NotificationMentionsResponse, error) {
	var result *xiaohongshu.NotificationMentionsData
	var err error

	err = s.withBrowserPage(func(page *rod.Page) error {
		action := xiaohongshu.NewNotificationMentionsAction(page)
		result, err = action.GetMentions(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}

	return &NotificationMentionsResponse{
		Notifications:  result.MessageList,
		Count:          len(result.MessageList),
		Cursor:         result.Cursor,
		HasMore:        result.HasMore,
		SourceEndpoint: "https://edith.xiaohongshu.com/api/sns/web/v1/you/mentions",
	}, nil
}

// PostCommentToFeed 发表评论到Feed
func (s *XiaohongshuService) PostCommentToFeed(ctx context.Context, feedID, xsecToken, content string) (*PostCommentResponse, error) {
	b, err := s.getBrowser("")
	if err != nil {
		return nil, err
	}

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewCommentFeedAction(page)

	if err := action.PostComment(ctx, feedID, xsecToken, content); err != nil {
		return nil, err
	}

	return &PostCommentResponse{FeedID: feedID, Success: true, Message: "评论发表成功"}, nil
}

// LikeFeed 点赞笔记
func (s *XiaohongshuService) LikeFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b, err := s.getBrowser("")
	if err != nil {
		return nil, err
	}

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewLikeAction(page)
	if err := action.Like(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "点赞成功或已点赞"}, nil
}

// UnlikeFeed 取消点赞笔记
func (s *XiaohongshuService) UnlikeFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b, err := s.getBrowser("")
	if err != nil {
		return nil, err
	}

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewLikeAction(page)
	if err := action.Unlike(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "取消点赞成功或未点赞"}, nil
}

// FavoriteFeed 收藏笔记
func (s *XiaohongshuService) FavoriteFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b, err := s.getBrowser("")
	if err != nil {
		return nil, err
	}

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewFavoriteAction(page)
	if err := action.Favorite(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "收藏成功或已收藏"}, nil
}

// UnfavoriteFeed 取消收藏笔记
func (s *XiaohongshuService) UnfavoriteFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b, err := s.getBrowser("")
	if err != nil {
		return nil, err
	}

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewFavoriteAction(page)
	if err := action.Unfavorite(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "取消收藏成功或未收藏"}, nil
}

// ReplyCommentToFeed 回复指定评论
func (s *XiaohongshuService) ReplyCommentToFeed(ctx context.Context, feedID, xsecToken, commentID, userID, content string) (*ReplyCommentResponse, error) {
	b, err := s.getBrowser("")
	if err != nil {
		return nil, err
	}

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewCommentFeedAction(page)

	if err := action.ReplyToComment(ctx, feedID, xsecToken, commentID, userID, content); err != nil {
		return nil, err
	}

	return &ReplyCommentResponse{
		FeedID:          feedID,
		TargetCommentID: commentID,
		TargetUserID:    userID,
		Success:         true,
		Message:         "评论回复成功",
	}, nil
}

func createBrowser(proxy string) (*browser.Browser, error) {
	opts := []browser.Option{
		browser.WithBinPath(configs.GetBinPath()),
	}
	if proxy = strings.TrimSpace(proxy); proxy != "" {
		logrus.Infof("登录/发布使用代理: %s", proxyutil.SanitizeForLog(proxy))
		opts = append(opts, browser.WithProxy(proxy))
	}
	if userDataDir := configs.GetUserDataDir(); userDataDir != "" {
		opts = append(opts, browser.WithUserDataDir(userDataDir))
	}
	if userAgent := configs.GetUserAgent(); userAgent != "" {
		opts = append(opts, browser.WithUserAgent(userAgent))
	}
	return browser.NewBrowser(configs.IsHeadless(), opts...)
}

// getBrowser 获取共享浏览器实例（懒加载）
func (s *XiaohongshuService) getBrowser(proxy string) (*browser.Browser, error) {
	s.browserMu.Lock()
	defer s.browserMu.Unlock()

	proxy = strings.TrimSpace(proxy)
	if s.sharedBrowser != nil && s.sharedBrowserProxy != proxy {
		s.loginPageMu.Lock()
		s.activeLoginPage = nil
		s.loginPageMu.Unlock()
		s.sharedBrowser.Close()
		s.sharedBrowser = nil
		s.sharedBrowserProxy = ""
	}

	if s.sharedBrowser == nil {
		b, err := createBrowser(proxy)
		if err != nil {
			return nil, err
		}
		s.sharedBrowser = b
		s.sharedBrowserProxy = proxy
	}
	return s.sharedBrowser, nil
}

func saveCookies(page *rod.Page) error {
	cks, err := page.Browser().GetCookies()
	if err != nil {
		return err
	}

	data, err := json.Marshal(cks)
	if err != nil {
		return err
	}

	cookieLoader := cookies.NewLoadCookie(cookies.GetCookiesFilePath())
	return cookieLoader.SaveCookies(data)
}

// withBrowserPage 执行需要浏览器页面的操作的通用函数
func (s *XiaohongshuService) withBrowserPage(fn func(*rod.Page) error) error {
	b, err := s.getBrowser("")
	if err != nil {
		return err
	}
	page := b.NewPage()
	defer page.Close()

	return fn(page)
}

func (s *XiaohongshuService) resolveLoginPublishProxy(ctx context.Context) (string, error) {
	return proxyutil.Resolve(ctx, configs.GetProxy(), configs.GetProxyPool())
}

func (s *XiaohongshuService) dropSharedBrowser() *browser.Browser {
	s.browserMu.Lock()
	b := s.sharedBrowser
	s.sharedBrowser = nil
	s.sharedBrowserProxy = ""
	s.browserMu.Unlock()

	s.loginPageMu.Lock()
	s.activeLoginPage = nil
	s.loginPageMu.Unlock()

	return b
}

// GetMyProfile 获取当前登录用户的个人信息
func (s *XiaohongshuService) GetMyProfile(ctx context.Context) (*UserProfileResponse, error) {
	var result *xiaohongshu.UserProfileResponse
	var err error

	err = s.withBrowserPage(func(page *rod.Page) error {
		action := xiaohongshu.NewUserProfileAction(page)
		result, err = action.GetMyProfileViaSidebar(ctx)
		return err
	})

	if err != nil {
		return nil, err
	}

	response := &UserProfileResponse{
		UserBasicInfo: result.UserBasicInfo,
		Interactions:  result.Interactions,
		Feeds:         result.Feeds,
	}

	return response, nil
}
