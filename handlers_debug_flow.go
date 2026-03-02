package main

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type flowDebugSessionsResponse struct {
	Sessions []FlowDebugSessionMeta `json:"sessions"`
}

type flowDebugStreamResponse struct {
	Session FlowDebugSessionMeta `json:"session"`
	Events  []FlowDebugEvent     `json:"events"`
	LastSeq int64                `json:"last_seq"`
	Dropped bool                 `json:"dropped,omitempty"`
}

// listFlowDebugSessionsHandler 返回最近的调试会话列表（最新在前）。
func (s *AppServer) listFlowDebugSessionsHandler(c *gin.Context) {
	sessions := s.xiaohongshuService.ListFlowDebugSessions()
	c.JSON(http.StatusOK, flowDebugSessionsResponse{Sessions: sessions})
}

// streamFlowDebugSessionHandler 增量拉取指定会话的调试事件。
func (s *AppServer) streamFlowDebugSessionHandler(c *gin.Context) {
	sid := strings.TrimSpace(c.Param("sid"))
	if sid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sid 不能为空"})
		return
	}

	session, ok := s.xiaohongshuService.GetFlowDebugSession(sid)
	if !ok || session == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "调试会话不存在"})
		return
	}

	sinceStr := strings.TrimSpace(c.Query("since"))
	limitStr := strings.TrimSpace(c.Query("limit"))

	var since int64
	if sinceStr != "" {
		if v, err := strconv.ParseInt(sinceStr, 10, 64); err == nil && v >= 0 {
			since = v
		}
	}

	limit := 500
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			limit = v
		}
	}

	events, lastSeq, dropped := session.StreamSince(since, limit)
	c.JSON(http.StatusOK, flowDebugStreamResponse{
		Session: session.Meta(),
		Events:  events,
		LastSeq: lastSeq,
		Dropped: dropped,
	})
}

// getFlowDebugSessionScreenshotHandler 获取指定会话的浏览器截图。
func (s *AppServer) getFlowDebugSessionScreenshotHandler(c *gin.Context) {
	sid := strings.TrimSpace(c.Param("sid"))
	if sid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sid 不能为空"})
		return
	}

	session, ok := s.xiaohongshuService.GetFlowDebugSession(sid)
	if !ok || session == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "调试会话不存在"})
		return
	}

	img, err := session.GetScreenshot(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no screenshot"})
		return
	}
	c.Data(http.StatusOK, "image/png", img)
}

type flowDebugControlRequest struct {
	Action string `json:"action"`
}

// postFlowDebugSessionControlHandler 暂停/继续指定会话（用于可视化调试）。
func (s *AppServer) postFlowDebugSessionControlHandler(c *gin.Context) {
	sid := strings.TrimSpace(c.Param("sid"))
	if sid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sid 不能为空"})
		return
	}

	session, ok := s.xiaohongshuService.GetFlowDebugSession(sid)
	if !ok || session == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "调试会话不存在"})
		return
	}

	var req flowDebugControlRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效 JSON"})
		return
	}

	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "pause":
		session.Pause()
	case "resume":
		session.Resume()
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "action 仅支持 pause/resume"})
		return
	}

	c.JSON(http.StatusOK, session.Meta())
}

// postFlowDebugSessionBrowserActionHandler 向会话绑定的浏览器页面发送交互动作（用于处理验证码/弹窗等）。
func (s *AppServer) postFlowDebugSessionBrowserActionHandler(c *gin.Context) {
	sid := strings.TrimSpace(c.Param("sid"))
	if sid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sid 不能为空"})
		return
	}

	session, ok := s.xiaohongshuService.GetFlowDebugSession(sid)
	if !ok || session == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "调试会话不存在"})
		return
	}

	var req BrowserActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	if err := session.ProcessBrowserAction(req.Type, req.X, req.Y, req.Text); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
