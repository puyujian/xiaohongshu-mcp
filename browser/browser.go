package browser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
)

// Browser 浏览器实例
type Browser struct {
	browser  *rod.Browser
	launcher *launcher.Launcher
}

// Config 浏览器配置
type Config struct {
	Headless    bool
	BinPath     string
	Proxy       string // 代理地址，如 http://127.0.0.1:7890
	UserDataDir string // 用户数据目录，多用户隔离必须
}

// Option 配置选项
type Option func(*Config)

// WithBinPath 设置浏览器路径
func WithBinPath(binPath string) Option {
	return func(c *Config) {
		c.BinPath = binPath
	}
}

// WithProxy 设置代理
func WithProxy(proxy string) Option {
	return func(c *Config) {
		c.Proxy = proxy
	}
}

// WithUserDataDir 设置用户数据目录
func WithUserDataDir(dir string) Option {
	return func(c *Config) {
		c.UserDataDir = dir
	}
}

// NewBrowser 创建浏览器实例
func NewBrowser(headless bool, options ...Option) (*Browser, error) {
	cfg := &Config{Headless: headless}
	for _, opt := range options {
		opt(cfg)
	}

	// 创建 launcher
	l := launcher.New().
		Headless(cfg.Headless).
		Set("no-sandbox").
		Set("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	// 设置浏览器路径
	if cfg.BinPath != "" {
		l = l.Bin(cfg.BinPath)
	}

	// 设置代理
	if cfg.Proxy != "" {
		l = l.Set("proxy-server", cfg.Proxy)
		logrus.Debugf("browser proxy: %s", cfg.Proxy)
	}

	// 设置用户数据目录（多用户隔离关键）
	if cfg.UserDataDir != "" {
		l = l.UserDataDir(cfg.UserDataDir)
		logrus.Debugf("browser user-data-dir: %s", cfg.UserDataDir)
		// 清理残留的锁文件，防止浏览器异常退出后无法启动
		cleanupChromeLocks(cfg.UserDataDir)
	}

	url, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("failed to launch browser: %w", err)
	}

	b := rod.New().ControlURL(url)
	if err := b.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect browser: %w", err)
	}

	// 加载 cookies
	cookiePath := cookies.GetCookiesFilePath()
	cookieLoader := cookies.NewLoadCookie(cookiePath)

	if data, err := cookieLoader.LoadCookies(); err == nil {
		var cks []*proto.NetworkCookie
		if err := json.Unmarshal(data, &cks); err == nil {
			b.MustSetCookies(cks...)
			logrus.Debugf("loaded cookies from file successfully")
		} else {
			logrus.Warnf("failed to unmarshal cookies: %v", err)
		}
	} else if os.IsNotExist(err) {
		logrus.Debugf("cookies file not found, skip loading")
	} else {
		logrus.Warnf("failed to load cookies: %v", err)
	}

	return &Browser{
		browser:  b,
		launcher: l,
	}, nil
}

// Close 关闭浏览器
func (b *Browser) Close() {
	b.browser.MustClose()
	b.launcher.Cleanup()
}

// NewPage 创建新页面（带 stealth 模式）
func (b *Browser) NewPage() *rod.Page {
	return stealth.MustPage(b.browser)
}

// cleanupChromeLocks 清理 Chrome 的所有锁文件
// 当浏览器异常退出时（如容器重启、进程被杀），锁文件不会被清理
// 导致新实例无法启动，报错 "The profile appears to be in use by another Google Chrome process"
func cleanupChromeLocks(userDataDir string) {
	// 确保目录存在
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		logrus.Warnf("failed to create user data dir: %v", err)
		return
	}

	// 检查是否为过期的 Profile（基于 SingletonCookie）
	if !isStaleProfile(userDataDir) {
		logrus.Debugf("profile appears to be in active use, skip cleanup")
		return
	}

	// 需要清理的锁文件列表
	lockFiles := []string{
		"SingletonLock",
		"SingletonSocket",
		"SingletonCookie",
	}

	// 清理根目录下的锁文件
	for _, name := range lockFiles {
		removeLockFile(filepath.Join(userDataDir, name))
	}

	// 清理 Default 子目录下的锁文件（某些 Chrome 版本会在此创建）
	defaultDir := filepath.Join(userDataDir, "Default")
	if _, err := os.Stat(defaultDir); err == nil {
		for _, name := range lockFiles {
			removeLockFile(filepath.Join(defaultDir, name))
		}
	}
}

// removeLockFile 移除锁文件（处理普通文件和符号链接）
func removeLockFile(path string) {
	info, err := os.Lstat(path) // 使用 Lstat 以检测符号链接
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		logrus.Warnf("failed to stat %s: %v", path, err)
		return
	}

	// 符号链接或普通文件都直接删除
	if info.Mode()&os.ModeSymlink != 0 || info.Mode().IsRegular() || info.Mode()&os.ModeSocket != 0 {
		if err := os.Remove(path); err != nil {
			logrus.Warnf("failed to remove %s: %v", path, err)
		} else {
			logrus.Infof("cleaned up stale lock: %s", path)
		}
	}
}

// isStaleProfile 判断 Profile 是否为过期残留
// 通过解析 SingletonCookie 检查 hostname 和 pid
func isStaleProfile(userDataDir string) bool {
	cookiePath := filepath.Join(userDataDir, "SingletonCookie")
	data, err := os.ReadFile(cookiePath)
	if os.IsNotExist(err) {
		// 没有 cookie 文件，可能是首次启动或已清理，允许继续
		return true
	}
	if err != nil {
		logrus.Warnf("failed to read SingletonCookie: %v", err)
		// 读取失败，保守起见尝试清理
		return true
	}

	// SingletonCookie 格式通常为 NUL 分隔: "hostname\x00pid\x00"
	// 也可能是 "hostname-pid" 或 "hostname pid"
	content := string(data)
	if content == "" {
		return true
	}

	// 尝试解析 hostname 和 pid
	var hostname string
	var pid int

	// 优先尝试 NUL 分隔（Chrome 标准格式）
	if strings.Contains(content, "\x00") {
		parts := strings.Split(content, "\x00")
		if len(parts) >= 1 {
			hostname = parts[0]
		}
		if len(parts) >= 2 {
			pid, _ = strconv.Atoi(parts[1])
		}
	} else {
		// 回退：尝试空格分隔（避免 hostname 含 - 被误切）
		parts := strings.Fields(strings.TrimSpace(content))
		if len(parts) >= 1 {
			hostname = parts[0]
		}
		if len(parts) >= 2 {
			pid, _ = strconv.Atoi(parts[len(parts)-1])
		}
	}

	// 获取当前 hostname
	currentHostname, err := os.Hostname()
	if err != nil {
		logrus.Warnf("failed to get hostname: %v", err)
		return true
	}

	// 如果 hostname 不同，说明是其他机器/容器的残留
	if hostname != "" && hostname != currentHostname {
		logrus.Infof("stale profile detected: hostname mismatch (cookie=%s, current=%s)", hostname, currentHostname)
		return true
	}

	// 如果 hostname 相同，检查 pid 是否存活
	if pid > 0 {
		if !isProcessAlive(pid) {
			logrus.Infof("stale profile detected: pid %d is not alive", pid)
			return true
		}
		// pid 存活，可能真的有 Chrome 在运行
		logrus.Warnf("profile may be in use: pid %d is alive on same host", pid)
		return false
	}

	// 无法判断，保守起见尝试清理
	return true
}

// isProcessAlive 检查进程是否存活
func isProcessAlive(pid int) bool {
	if runtime.GOOS == "windows" {
		// Windows: 容器场景较少，且 Chrome profile 锁问题主要出现在 Linux 容器
		// 保守处理：假设进程不存在，允许清理
		return false
	}

	// Linux: 检查 /proc/<pid> 是否存在
	procPath := fmt.Sprintf("/proc/%d", pid)
	if _, err := os.Stat(procPath); err == nil {
		return true
	}

	// macOS/FreeBSD 等无 /proc 的系统：使用 kill -0 检查
	// 发送信号 0 不会杀死进程，只检查进程是否存在
	// 注意：这里不直接调用 syscall 以保持代码简洁，
	// 对于容器化部署（主要是 Linux），/proc 检查已足够
	return false
}
