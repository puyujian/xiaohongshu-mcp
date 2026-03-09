package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// setupRoutes 设置路由配置
func setupRoutes(appServer *AppServer) *gin.Engine {
	// 设置 Gin 模式
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(gin.Logger())
	router.Use(gin.Recovery())

	// 添加中间件
	router.Use(errorHandlingMiddleware())
	router.Use(corsMiddleware())

	// 健康检查
	router.GET("/health", healthHandler)

	// MCP 端点 - 使用官方 SDK 的 Streamable HTTP Handler
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server {
			return appServer.mcpServer
		},
		&mcp.StreamableHTTPOptions{
			JSONResponse: true, // 支持 JSON 响应
		},
	)
	router.Any("/mcp", gin.WrapH(mcpHandler))
	router.Any("/mcp/*path", gin.WrapH(mcpHandler))

	// API 路由组
	api := router.Group("/api/v1")
	{
		api.GET("/login/status", appServer.checkLoginStatusHandler)
		api.GET("/login/qrcode", appServer.getLoginQrcodeHandler)
		api.GET("/login/browser/screenshot", appServer.getLoginBrowserScreenshotHandler)
		api.POST("/login/browser/action", appServer.postLoginBrowserActionHandler)

		// 可视化调试（发布流程为主）：会话/步骤/网络/控制台/暂停/截图
		api.GET("/debug/sessions", appServer.listFlowDebugSessionsHandler)
		api.GET("/debug/sessions/:sid/stream", appServer.streamFlowDebugSessionHandler)
		api.GET("/debug/sessions/:sid/browser/screenshot", appServer.getFlowDebugSessionScreenshotHandler)
		api.POST("/debug/sessions/:sid/control", appServer.postFlowDebugSessionControlHandler)
		api.POST("/debug/sessions/:sid/browser/action", appServer.postFlowDebugSessionBrowserActionHandler)

		api.DELETE("/login/cookies", appServer.deleteCookiesHandler)
		api.POST("/publish", appServer.publishHandler)
		api.POST("/publish_video", appServer.publishVideoHandler)
		api.GET("/feeds/list", appServer.listFeedsHandler)
		api.GET("/feeds/search", appServer.searchFeedsHandler)
		api.POST("/feeds/search", appServer.searchFeedsHandler)
		api.POST("/feeds/detail", appServer.getFeedDetailHandler)
		api.POST("/feeds/like", appServer.likeFeedHandler)
		api.POST("/feeds/favorite", appServer.favoriteFeedHandler)
		api.POST("/user/profile", appServer.userProfileHandler)
		api.POST("/feeds/comment", appServer.postCommentHandler)
		api.POST("/feeds/comment/reply", appServer.replyCommentHandler)
		api.GET("/user/me", appServer.myProfileHandler)
		api.GET("/notifications/mentions", appServer.notificationMentionsHandler)
	}

	return router
}
