package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/xpzouying/xiaohongshu-mcp/pkg/flowdebug"
)

const (
	flowDebugDefaultMaxSessions = 20
	flowDebugDefaultMaxEvents   = 3000
	flowDebugMaxBodyChars       = 32 * 1024 // 网络 body 过大容易撑爆内存，默认最多保留 32KB 文本
	flowDebugMaxPostDataChars   = 8 * 1024
)

// FlowDebugCenter 管理调试会话（当前主要用于发布流程的可视化调试）。
type FlowDebugCenter struct {
	mu         sync.RWMutex
	sessions   map[string]*FlowDebugSession
	order      []string
	maxSession int
}

func NewFlowDebugCenter(maxSessions int) *FlowDebugCenter {
	if maxSessions <= 0 {
		maxSessions = flowDebugDefaultMaxSessions
	}
	return &FlowDebugCenter{
		sessions:   map[string]*FlowDebugSession{},
		maxSession: maxSessions,
	}
}

func (c *FlowDebugCenter) NewSession(kind string) *FlowDebugSession {
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	s := newFlowDebugSession(id, kind)

	c.mu.Lock()
	c.sessions[id] = s
	c.order = append(c.order, id)
	c.pruneLocked()
	c.mu.Unlock()

	return s
}

func (c *FlowDebugCenter) Get(id string) (*FlowDebugSession, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.sessions[id]
	return s, ok
}

func (c *FlowDebugCenter) List() []FlowDebugSessionMeta {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]FlowDebugSessionMeta, 0, len(c.order))
	for i := len(c.order) - 1; i >= 0; i-- { // 逆序：最新在前
		id := c.order[i]
		s := c.sessions[id]
		if s == nil {
			continue
		}
		out = append(out, s.Meta())
	}
	return out
}

func (c *FlowDebugCenter) pruneLocked() {
	if len(c.order) <= c.maxSession {
		return
	}

	// 优先清理“已结束”的最老会话，避免把运行中的会话踢掉
	need := len(c.order) - c.maxSession
	if need <= 0 {
		return
	}

	var kept []string
	for _, id := range c.order {
		if need <= 0 {
			kept = append(kept, id)
			continue
		}
		s := c.sessions[id]
		if s == nil {
			delete(c.sessions, id)
			need--
			continue
		}
		if s.IsEnded() {
			delete(c.sessions, id)
			need--
			continue
		}
		kept = append(kept, id)
	}

	// 如果全是运行中的，会话数仍可能超限，此时保留不动（宁可超一点也不要影响调试）。
	if need <= 0 {
		c.order = kept
	}
}

// FlowDebugEvent 调试事件（用于 UI 增量拉取）。
type FlowDebugEvent struct {
	Seq     int64          `json:"seq"`
	Time    string         `json:"time"`
	Type    string         `json:"type"`
	Level   string         `json:"level,omitempty"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

// FlowDebugSessionMeta 会话元信息（用于列表展示/状态栏）。
type FlowDebugSessionMeta struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Paused    bool   `json:"paused"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at,omitempty"`
	Error     string `json:"error,omitempty"`
	ErrorMore string `json:"error_more,omitempty"`
}

type netTrack struct {
	ResourceType string
	URL          string
	Method       string
	Status       int
	MimeType     string
	BodyFetched  bool
}

// FlowDebugSession 一个调试会话：记录步骤、网络、控制台，并支持暂停/继续。
// 它实现了 flowdebug.Debugger 接口，可通过 context 传入业务流程。
type FlowDebugSession struct {
	id   string
	kind string

	metaMu    sync.RWMutex
	status    string
	startedAt time.Time
	endedAt   *time.Time
	err       string
	errMore   string

	pauseMu  sync.Mutex
	paused   bool
	resumeCh chan struct{}

	pageMu sync.RWMutex
	page   *rod.Page

	screenshotMu   sync.RWMutex
	lastScreenshot []byte

	eventsMu  sync.Mutex
	events    []FlowDebugEvent
	nextSeq   int64
	maxEvents int

	netMu   sync.Mutex
	netByID map[proto.NetworkRequestID]*netTrack
}

func newFlowDebugSession(id, kind string) *FlowDebugSession {
	// 非暂停态时，resumeCh 用一个“已关闭的 channel”作为占位，避免泄漏无用 channel。
	ch := make(chan struct{})
	close(ch)
	return &FlowDebugSession{
		id:        id,
		kind:      strings.TrimSpace(kind),
		status:    "running",
		startedAt: time.Now(),
		resumeCh:  ch,
		maxEvents: flowDebugDefaultMaxEvents,
		netByID:   map[proto.NetworkRequestID]*netTrack{},
	}
}

func (s *FlowDebugSession) ID() string { return s.id }

func (s *FlowDebugSession) Meta() FlowDebugSessionMeta {
	// 注意：避免在 metaMu 持有期间再去获取 pauseMu，防止与 Pause/Resume 形成锁顺序死锁。
	s.metaMu.RLock()
	id := s.id
	kind := s.kind
	status := s.status
	startedAt := s.startedAt
	endedAt := s.endedAt
	errMsg := s.err
	errMore := s.errMore
	s.metaMu.RUnlock()

	s.pauseMu.Lock()
	paused := s.paused
	s.pauseMu.Unlock()

	meta := FlowDebugSessionMeta{
		ID:        id,
		Kind:      kind,
		Status:    status,
		Paused:    paused,
		StartedAt: startedAt.Format(time.RFC3339Nano),
		Error:     errMsg,
		ErrorMore: errMore,
	}
	if endedAt != nil {
		meta.EndedAt = endedAt.Format(time.RFC3339Nano)
	}
	return meta
}

func (s *FlowDebugSession) IsEnded() bool {
	s.metaMu.RLock()
	defer s.metaMu.RUnlock()
	return s.endedAt != nil
}

func (s *FlowDebugSession) SetMaxEvents(n int) {
	if n <= 0 {
		return
	}
	s.eventsMu.Lock()
	s.maxEvents = n
	s.eventsMu.Unlock()
}

func (s *FlowDebugSession) AttachPage(page *rod.Page) {
	s.pageMu.Lock()
	s.page = page
	s.pageMu.Unlock()

	// 启用网络/控制台事件（失败不影响主流程）
	_ = proto.NetworkEnable{}.Call(page)
	_ = proto.RuntimeEnable{}.Call(page)
	_ = proto.LogEnable{}.Call(page)

	s.Log("info", "已绑定浏览器页面", map[string]any{
		"kind": s.kind,
	})

	// 事件采集在 goroutine 中运行，page 关闭后会自动退出。
	go page.EachEvent(
		func(e *proto.NetworkRequestWillBeSent) {
			s.onNetworkRequest(e)
		},
		func(e *proto.NetworkResponseReceived) {
			s.onNetworkResponse(e)
		},
		func(e *proto.NetworkLoadingFinished) {
			s.onNetworkFinished(page, e)
		},
		func(e *proto.NetworkLoadingFailed) {
			s.onNetworkFailed(e)
		},
		func(e *proto.RuntimeConsoleAPICalled) {
			s.onConsoleAPICalled(e)
		},
		func(e *proto.RuntimeExceptionThrown) {
			s.onRuntimeException(e)
		},
		func(e *proto.LogEntryAdded) {
			s.onLogEntryAdded(e)
		},
	)()
}

func (s *FlowDebugSession) DetachPage() {
	s.pageMu.Lock()
	s.page = nil
	s.pageMu.Unlock()
}

func (s *FlowDebugSession) Step(name string, fields map[string]any) {
	s.addEvent("step", "", strings.TrimSpace(name), fields)
}

func (s *FlowDebugSession) Log(level string, message string, fields map[string]any) {
	s.addEvent("log", strings.TrimSpace(level), strings.TrimSpace(message), fields)
}

func (s *FlowDebugSession) WaitIfPaused(ctx context.Context) error {
	for {
		s.pauseMu.Lock()
		paused := s.paused
		ch := s.resumeCh
		s.pauseMu.Unlock()

		if !paused {
			return nil
		}

		select {
		case <-ch:
			// 已恢复，继续循环以处理快速“再次暂停”的情况
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *FlowDebugSession) Pause() {
	s.pauseMu.Lock()
	defer s.pauseMu.Unlock()
	if s.paused {
		return
	}
	s.paused = true
	s.resumeCh = make(chan struct{})
	s.addEvent("status", "", "已暂停", nil)

	s.metaMu.Lock()
	if s.status == "running" {
		s.status = "paused"
	}
	s.metaMu.Unlock()
}

func (s *FlowDebugSession) Resume() {
	s.pauseMu.Lock()
	defer s.pauseMu.Unlock()
	if !s.paused {
		return
	}
	s.paused = false
	close(s.resumeCh)
	s.addEvent("status", "", "已继续", nil)

	s.metaMu.Lock()
	if s.status == "paused" {
		s.status = "running"
	}
	s.metaMu.Unlock()
}

func (s *FlowDebugSession) End(err error) {
	now := time.Now()

	if err != nil {
		errMsg := err.Error()
		errMore := fmt.Sprintf("%+v", err)
		s.addEvent("error", "error", errMsg, map[string]any{"detail": errMore})

		s.metaMu.Lock()
		s.err = errMsg
		s.errMore = errMore
		s.status = "error"
		s.endedAt = &now
		s.metaMu.Unlock()
		return
	}

	s.metaMu.Lock()
	s.status = "done"
	s.endedAt = &now
	s.metaMu.Unlock()

	s.addEvent("status", "", "已完成", nil)
}

func (s *FlowDebugSession) StreamSince(since int64, limit int) (events []FlowDebugEvent, lastSeq int64, dropped bool) {
	if limit <= 0 {
		limit = 500
	}
	if limit > 2000 {
		limit = 2000
	}

	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()

	if len(s.events) == 0 {
		return nil, s.nextSeq, false
	}

	// 如果 since 早于当前最老事件，说明中间有丢失（被裁剪）
	if since > 0 && s.events[0].Seq > since+1 {
		dropped = true
	}

	startIdx := 0
	for startIdx < len(s.events) && s.events[startIdx].Seq <= since {
		startIdx++
	}

	if startIdx >= len(s.events) {
		return nil, s.nextSeq, dropped
	}

	endIdx := startIdx + limit
	if endIdx > len(s.events) {
		endIdx = len(s.events)
	}

	cp := make([]FlowDebugEvent, endIdx-startIdx)
	copy(cp, s.events[startIdx:endIdx])
	return cp, s.nextSeq, dropped
}

func (s *FlowDebugSession) GetScreenshot(ctx context.Context) ([]byte, error) {
	// 有活跃 page 时优先实时截图
	s.pageMu.RLock()
	page := s.page
	s.pageMu.RUnlock()

	if page != nil {
		img, err := page.Context(ctx).Screenshot(false, nil)
		if err != nil {
			return nil, err
		}
		s.screenshotMu.Lock()
		s.lastScreenshot = img
		s.screenshotMu.Unlock()
		return img, nil
	}

	// 没有活跃 page 时返回最后一次截图（便于事后排查）
	s.screenshotMu.RLock()
	defer s.screenshotMu.RUnlock()
	if len(s.lastScreenshot) == 0 {
		return nil, fmt.Errorf("no screenshot")
	}
	return s.lastScreenshot, nil
}

func (s *FlowDebugSession) ProcessBrowserAction(actionType string, x, y float64, text string) error {
	s.pageMu.RLock()
	page := s.page
	s.pageMu.RUnlock()

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

func (s *FlowDebugSession) addEvent(eventType, level, message string, data map[string]any) {
	if message == "" {
		message = eventType
	}
	ev := FlowDebugEvent{
		Time:    time.Now().Format(time.RFC3339Nano),
		Type:    eventType,
		Level:   level,
		Message: message,
		Data:    data,
	}

	s.eventsMu.Lock()
	s.nextSeq++
	ev.Seq = s.nextSeq
	s.events = append(s.events, ev)
	if s.maxEvents > 0 && len(s.events) > s.maxEvents {
		// 只保留最后 maxEvents 条
		s.events = s.events[len(s.events)-s.maxEvents:]
	}
	s.eventsMu.Unlock()
}

func shouldRedactHeader(keyLower string) bool {
	switch keyLower {
	case "cookie", "authorization", "proxy-authorization":
		return true
	default:
		return false
	}
}

func sanitizeHeaders(headers proto.NetworkHeaders) map[string]any {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]any, len(headers))
	for k, v := range headers {
		lk := strings.ToLower(strings.TrimSpace(k))
		if shouldRedactHeader(lk) {
			out[k] = "<redacted>"
			continue
		}
		out[k] = v.Val()
	}
	return out
}

func trimTo(s string, max int) (string, bool) {
	if max <= 0 {
		return s, false
	}
	if len(s) <= max {
		return s, false
	}
	return s[:max] + "...(truncated)", true
}

func (s *FlowDebugSession) onNetworkRequest(e *proto.NetworkRequestWillBeSent) {
	if e == nil || e.Request == nil {
		return
	}

	var postData string
	var postTrunc bool
	if e.Request.PostData != "" {
		postData, postTrunc = trimTo(e.Request.PostData, flowDebugMaxPostDataChars)
	}

	s.netMu.Lock()
	s.netByID[e.RequestID] = &netTrack{
		ResourceType: string(e.Type),
		URL:          e.Request.URL,
		Method:       e.Request.Method,
	}
	s.netMu.Unlock()

	s.addEvent("network.request", "", fmt.Sprintf("%s %s", e.Request.Method, e.Request.URL), map[string]any{
		"request_id": string(e.RequestID),
		"url":        e.Request.URL,
		"method":     e.Request.Method,
		"type":       string(e.Type),
		"headers":    sanitizeHeaders(e.Request.Headers),
		"has_post":   e.Request.HasPostData,
		"post_data":  postData,
		"truncated":  postTrunc,
	})
}

func (s *FlowDebugSession) onNetworkResponse(e *proto.NetworkResponseReceived) {
	if e == nil || e.Response == nil {
		return
	}

	s.netMu.Lock()
	track := s.netByID[e.RequestID]
	if track == nil {
		track = &netTrack{}
		s.netByID[e.RequestID] = track
	}
	track.Status = int(e.Response.Status)
	track.MimeType = e.Response.MIMEType
	track.ResourceType = string(e.Type)
	s.netMu.Unlock()

	s.addEvent("network.response", "", fmt.Sprintf("%d %s", int(e.Response.Status), e.Response.URL), map[string]any{
		"request_id": string(e.RequestID),
		"url":        e.Response.URL,
		"status":     int(e.Response.Status),
		"statusText": e.Response.StatusText,
		"type":       string(e.Type),
		"mime":       e.Response.MIMEType,
		"headers":    sanitizeHeaders(e.Response.Headers),
	})
}

func (s *FlowDebugSession) onNetworkFinished(page *rod.Page, e *proto.NetworkLoadingFinished) {
	if e == nil || page == nil {
		return
	}

	s.netMu.Lock()
	track := s.netByID[e.RequestID]
	if track == nil {
		track = &netTrack{}
		s.netByID[e.RequestID] = track
	}
	// 仅抓取 XHR / Fetch 的 body（其它资源体积大且价值有限）
	shouldFetchBody := !track.BodyFetched && (strings.EqualFold(track.ResourceType, "XHR") || strings.EqualFold(track.ResourceType, "Fetch"))
	if shouldFetchBody {
		track.BodyFetched = true
	}
	s.netMu.Unlock()

	if !shouldFetchBody {
		return
	}

	res, err := proto.NetworkGetResponseBody{RequestID: e.RequestID}.Call(page)
	if err != nil || res == nil {
		return
	}

	body := res.Body
	truncBody, trunc := trimTo(body, flowDebugMaxBodyChars)

	s.addEvent("network.body", "", "response body", map[string]any{
		"request_id":     string(e.RequestID),
		"base64_encoded": res.Base64Encoded,
		"body":           truncBody,
		"body_truncated": trunc,
		"encoded_length": e.EncodedDataLength,
	})
}

func (s *FlowDebugSession) onNetworkFailed(e *proto.NetworkLoadingFailed) {
	if e == nil {
		return
	}
	s.addEvent("network.failed", "error", e.ErrorText, map[string]any{
		"request_id": string(e.RequestID),
		"canceled":   e.Canceled,
		"blocked":    e.BlockedReason,
		"type":       string(e.Type),
	})
}

func remoteObjectToString(o *proto.RuntimeRemoteObject) string {
	if o == nil {
		return ""
	}
	if !o.Value.Nil() {
		// 尽量用 JSON 展示，便于排查对象结构
		raw := o.Value.Val()
		if raw == nil {
			return ""
		}
		b, err := json.Marshal(raw)
		if err == nil {
			return string(b)
		}
	}
	if o.Description != "" {
		return o.Description
	}
	if o.ClassName != "" {
		return o.ClassName
	}
	return string(o.Type)
}

func (s *FlowDebugSession) onConsoleAPICalled(e *proto.RuntimeConsoleAPICalled) {
	if e == nil {
		return
	}
	args := make([]string, 0, len(e.Args))
	for _, a := range e.Args {
		args = append(args, remoteObjectToString(a))
	}
	msg := strings.Join(args, " ")
	if strings.TrimSpace(msg) == "" {
		msg = string(e.Type)
	}

	s.addEvent("console", string(e.Type), msg, map[string]any{
		"type":    string(e.Type),
		"args":    args,
		"context": e.Context,
	})
}

func (s *FlowDebugSession) onRuntimeException(e *proto.RuntimeExceptionThrown) {
	if e == nil || e.ExceptionDetails == nil {
		return
	}
	text := strings.TrimSpace(e.ExceptionDetails.Text)
	if text == "" {
		text = "Unhandled exception"
	}

	s.addEvent("console.exception", "error", text, map[string]any{
		"text": text,
		"url":  e.ExceptionDetails.URL,
		"line": e.ExceptionDetails.LineNumber,
		"col":  e.ExceptionDetails.ColumnNumber,
	})
}

func (s *FlowDebugSession) onLogEntryAdded(e *proto.LogEntryAdded) {
	if e == nil || e.Entry == nil {
		return
	}
	s.addEvent("console.entry", string(e.Entry.Level), e.Entry.Text, map[string]any{
		"level":  string(e.Entry.Level),
		"source": string(e.Entry.Source),
		"url":    e.Entry.URL,
		"line":   e.Entry.LineNumber,
	})
}

var _ flowdebug.Debugger = (*FlowDebugSession)(nil)
