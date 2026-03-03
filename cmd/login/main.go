package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"

	"github.com/go-rod/rod"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

func main() {
	var (
		binPath string // 浏览器二进制文件路径
		proxy   string // 代理地址（可空）
	)
	flag.StringVar(&binPath, "bin", "", "浏览器二进制文件路径")
	flag.StringVar(&proxy, "proxy", "", "代理地址，如 http://127.0.0.1:7890（可空）")
	flag.Parse()

	// 环境变量 fallback
	if proxy == "" {
		proxy = os.Getenv("XHS_PROXY")
	}
	if err := configs.ApplyProxyToEnv(proxy); err != nil {
		logrus.Warnf("代理环境变量设置失败（将仅影响 Go 侧网络请求/浏览器下载）：%v", err)
	}

	// 登录的时候，需要界面，所以不能无头模式
	opts := []browser.Option{
		browser.WithBinPath(binPath),
	}
	if proxy != "" {
		opts = append(opts, browser.WithProxy(proxy))
	}
	b, err := browser.NewBrowser(false, opts...)
	if err != nil {
		logrus.Fatalf("failed to create browser: %v", err)
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewLogin(page)

	status, err := action.CheckLoginStatus(context.Background())
	if err != nil {
		logrus.Fatalf("failed to check login status: %v", err)
	}

	logrus.Infof("当前登录状态: %v", status)

	if status {
		return
	}

	// 开始登录流程
	logrus.Info("开始登录流程...")
	if err = action.Login(context.Background()); err != nil {
		logrus.Fatalf("登录失败: %v", err)
	} else {
		if err := saveCookies(page); err != nil {
			logrus.Fatalf("failed to save cookies: %v", err)
		}
	}

	// 再次检查登录状态确认成功
	status, err = action.CheckLoginStatus(context.Background())
	if err != nil {
		logrus.Fatalf("failed to check login status after login: %v", err)
	}

	if status {
		logrus.Info("登录成功！")
	} else {
		logrus.Error("登录流程完成但仍未登录")
	}

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
