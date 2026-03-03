package main

import (
	"flag"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
)

func main() {
	var (
		headless    bool
		binPath     string // 浏览器二进制文件路径
		port        string
		proxy       string // 代理地址
		userDataDir string // 用户数据目录
		userAgent   string // 浏览器 User-Agent
	)
	flag.BoolVar(&headless, "headless", true, "是否无头模式")
	flag.StringVar(&binPath, "bin", "", "浏览器二进制文件路径")
	flag.StringVar(&port, "port", ":18060", "端口")
	flag.StringVar(&proxy, "proxy", "", "代理地址，如 http://127.0.0.1:7890")
	flag.StringVar(&userDataDir, "user-data-dir", "", "浏览器用户数据目录（多用户隔离）")
	flag.StringVar(&userAgent, "user-agent", "", "浏览器 User-Agent（为空使用默认值）")
	flag.Parse()

	// 环境变量 fallback
	if len(binPath) == 0 {
		binPath = os.Getenv("ROD_BROWSER_BIN")
	}
	if len(proxy) == 0 {
		proxy = os.Getenv("XHS_PROXY")
	}
	if len(userDataDir) == 0 {
		userDataDir = os.Getenv("BROWSER_USER_DATA_DIR")
	}
	if len(userAgent) == 0 {
		userAgent = os.Getenv("BROWSER_USER_AGENT")
	}

	// 将代理同步到进程环境变量，确保 Go 侧网络请求与浏览器下载都能走代理。
	if err := configs.ApplyProxyToEnv(proxy); err != nil {
		logrus.Warnf("代理环境变量设置失败（将仅影响 Go 侧网络请求/浏览器下载）：%v", err)
	}

	// 初始化全局配置
	configs.InitHeadless(headless)
	configs.SetBinPath(binPath)
	configs.SetProxy(proxy)
	configs.SetUserDataDir(userDataDir)
	configs.SetUserAgent(userAgent)

	// 初始化服务
	xiaohongshuService := NewXiaohongshuService()

	// 创建并启动应用服务器
	appServer := NewAppServer(xiaohongshuService)
	if err := appServer.Start(port); err != nil {
		logrus.Fatalf("failed to run server: %v", err)
	}
}
