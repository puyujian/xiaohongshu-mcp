package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

//go:embed web/index.html
var webFS embed.FS

func main() {
	var (
		listenAddr  string
		storePath   string
		stopTimeout time.Duration
	)

	flag.StringVar(&listenAddr, "listen", "127.0.0.1:18050", "Web 管理器监听地址")
	flag.StringVar(&storePath, "store", "./data/manager/users.json", "用户配置 JSON 存储路径")
	flag.DurationVar(&stopTimeout, "stop-timeout", 10*time.Second, "退出时停止子进程的等待时间")
	flag.Parse()

	store, err := LoadStore(storePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载 JSON 存储失败: %v\n", err)
		os.Exit(2)
	}

	// 读取嵌入的 HTML
	indexHTML, err := webFS.ReadFile("web/index.html")
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取嵌入的 HTML 失败: %v\n", err)
		os.Exit(2)
	}

	proc := NewProcessManager()
	app := NewApp(store, proc, string(indexHTML))

	// 启动恢复：上次记录为运行态的用户，自动拉起
	go autoStartUsers(store, proc)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	r.GET("/", app.HandleIndex)

	api := r.Group("/api/admin/v1")
	{
		api.GET("/users", app.ListUsers)
		api.POST("/users", app.CreateUser)
		api.PUT("/users/:id", app.UpdateUser)
		api.DELETE("/users/:id", app.DeleteUser)
		api.POST("/users/:id/start", app.StartUser)
		api.POST("/users/:id/stop", app.StopUser)

		// 批量操作API
		api.POST("/users/batch/start", app.BatchStartUsers)
		api.POST("/users/batch/stop", app.BatchStopUsers)

		// 日志管理API
		api.GET("/logs", app.ListLogs)

		// 调试API
		api.GET("/users/:id/debug/summary", app.GetDebugSummary)
		api.GET("/users/:id/debug/login/qrcode", app.GetDebugLoginQRCode)
		api.GET("/users/:id/debug/login/status", app.GetDebugLoginStatus)
		api.GET("/users/:id/debug/cookies", app.GetDebugCookies)
		api.POST("/users/:id/debug/cookies/import", app.ImportDebugCookies)
		api.DELETE("/users/:id/debug/cookies", app.DeleteDebugCookies)
		api.GET("/users/:id/debug/mcp/tools", app.GetDebugMCPTools)
		api.POST("/users/:id/debug/mcp/call", app.PostDebugMCPCall)
		api.GET("/users/:id/debug/logs", app.GetDebugLogs)
		api.DELETE("/users/:id/debug/logs", app.DeleteDebugLogs)
		api.GET("/users/:id/debug/logs/download", app.DownloadDebugLogs)
	}

	srv := &http.Server{
		Addr:    listenAddr,
		Handler: r,
	}

	go func() {
		fmt.Printf("Web GUI 管理器已启动: http://%s\n", listenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Web 服务启动失败: %v\n", err)
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	fmt.Println("收到退出信号，停止所有用户进程并关闭 Web 服务...")

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	_ = proc.StopAll(ctx, stopTimeout)
	_ = srv.Shutdown(ctx)
	fmt.Println("manager 已退出")
}

// autoStartUsers 启动恢复：上次记录为运行态的用户，自动拉起
func autoStartUsers(store *Store, proc *ProcessManager) {
	cfg := store.GetConfig()
	binPath := store.ResolveBinPath()
	dataDir := store.ResolveDataDir()

	users := store.ListUsers()
	var toStart []UserConfig
	for _, u := range users {
		if u.AutoStart {
			toStart = append(toStart, u)
		}
	}

	if len(toStart) == 0 {
		return
	}

	fmt.Printf("auto-start: 发现 %d 个需要自动启动的用户\n", len(toStart))

	// 串行启动，避免资源竞争
	for _, u := range toStart {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		err := proc.StartUser(ctx, StartUserParams{
			User:     u,
			BinPath:  binPath,
			Headless: cfg.Headless,
			DataDir:  dataDir,
		})
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "auto-start %s 失败: %v\n", u.ID, err)
		} else {
			fmt.Printf("auto-start %s 成功\n", u.ID)
		}
	}
}
