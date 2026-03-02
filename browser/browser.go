package browser

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
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

// defaultUserAgent 默认 User-Agent（向后兼容）
const defaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// Browser 浏览器实例
type Browser struct {
	browser   *rod.Browser
	launcher  *launcher.Launcher
	proxyAuth *proxyAuth
}

type proxyAuth struct {
	Username string
	Password string
}

// Config 浏览器配置
type Config struct {
	Headless    bool
	BinPath     string
	Proxy       string // 代理地址，如 http://127.0.0.1:7890
	UserAgent   string // 浏览器 User-Agent（为空使用默认值）
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

// WithUserAgent 设置浏览器 User-Agent
func WithUserAgent(ua string) Option {
	return func(c *Config) {
		c.UserAgent = ua
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

	// 确定使用的 User-Agent（为空时使用默认值）
	ua := strings.TrimSpace(cfg.UserAgent)
	if ua == "" {
		ua = defaultUserAgent
	}

	// 创建 launcher
	l := launcher.New().
		Headless(cfg.Headless).
		Set("no-sandbox").
		Set("user-agent", ua)

	// 设置浏览器路径
	if cfg.BinPath != "" {
		l = l.Bin(cfg.BinPath)
	}

	// 设置代理
	var proxyAuthCfg *proxyAuth
	if cfg.Proxy != "" {
		normalizedProxy, err := normalizeProxy(cfg.Proxy)
		if err != nil {
			return nil, err
		}
		auth, err := parseProxyAuth(cfg.Proxy)
		if err != nil {
			return nil, err
		}
		proxyAuthCfg = auth
		if normalizedProxy != "" {
			l = l.Set("proxy-server", normalizedProxy)
			logrus.Debugf("browser proxy: %s", sanitizeProxyForLog(normalizedProxy))
		}
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

	if proxyAuthCfg != nil && proxyAuthCfg.Username != "" {
		logrus.Debugf("browser proxy auth enabled")
		startProxyAuth(b, proxyAuthCfg.Username, proxyAuthCfg.Password)
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
		browser:   b,
		launcher:  l,
		proxyAuth: proxyAuthCfg,
	}, nil
}

func startProxyAuth(b *rod.Browser, username, password string) {
	if b == nil {
		return
	}

	ch := b.Event()
	go runProxyAuthLoop(b, ch, username, password)
}

func runProxyAuthLoop(b *rod.Browser, ch <-chan *rod.Message, username, password string) {
	for msg := range ch {
		// 认证与继续请求必须在触发该事件的 target session 上执行，否则不会生效。
		caller := b.PageFromSession(msg.SessionID)
		switch msg.Method {
		case proto.FetchRequestPaused{}.ProtoEvent():
			var e proto.FetchRequestPaused
			if !msg.Load(&e) {
				continue
			}
			_ = proto.FetchContinueRequest{RequestID: e.RequestID}.Call(caller)
		case proto.FetchAuthRequired{}.ProtoEvent():
			var e proto.FetchAuthRequired
			if !msg.Load(&e) {
				continue
			}
			if e.AuthChallenge != nil && e.AuthChallenge.Source == proto.FetchAuthChallengeSourceProxy {
				_ = proto.FetchContinueWithAuth{
					RequestID: e.RequestID,
					AuthChallengeResponse: &proto.FetchAuthChallengeResponse{
						Response: proto.FetchAuthChallengeResponseResponseProvideCredentials,
						Username: username,
						Password: password,
					},
				}.Call(caller)
			} else {
				_ = proto.FetchContinueWithAuth{
					RequestID: e.RequestID,
					AuthChallengeResponse: &proto.FetchAuthChallengeResponse{
						Response: proto.FetchAuthChallengeResponseResponseCancelAuth,
					},
				}.Call(caller)
			}
		}
	}
}

func normalizeProxy(raw string) (string, error) {
	proxy := strings.TrimSpace(raw)
	if proxy == "" {
		return "", nil
	}

	// 高级用法：允许直接透传 Chrome 原生 proxy 字符串（如包含 http=...;https=...）
	if !strings.Contains(proxy, "://") && strings.ContainsAny(proxy, "=;") {
		return proxy, nil
	}

	// URL 格式（带 scheme）
	if strings.Contains(proxy, "://") {
		u, err := url.Parse(proxy)
		if err != nil {
			return "", fmt.Errorf("代理地址解析失败（%s）: %w", sanitizeProxyForLog(raw), err)
		}

		scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
		host := strings.TrimSpace(u.Host)
		if host == "" {
			// 兜底处理：某些异常输入会落到 Opaque
			host = strings.TrimSpace(u.Opaque)
		}
		if host == "" {
			return "", fmt.Errorf("代理地址缺少 host:port（%s）", sanitizeProxyForLog(raw))
		}
		if err := validateHostPort(host); err != nil {
			return "", fmt.Errorf("代理地址 host:port 不合法（%s）: %w", sanitizeProxyForLog(raw), err)
		}

		// Chrome 的 --proxy-server 对 HTTP 代理使用 host:port 更兼容（避免不同版本对 scheme 的差异）
		switch scheme {
		case "http", "https":
			return host, nil
		case "socks5", "socks5h", "socks":
			return "socks5://" + host, nil
		case "socks4", "socks4a":
			return "socks4://" + host, nil
		default:
			return "", fmt.Errorf("不支持的代理协议（%s）: %s；请使用 http://host:port、socks5://host:port 或直接填写 host:port", sanitizeProxyForLog(raw), scheme)
		}
	}

	// 无 scheme：可能是 host:port 或 user:pass@host:port
	if strings.Contains(proxy, "@") {
		u, err := url.Parse("http://" + proxy)
		if err != nil {
			return "", fmt.Errorf("代理地址解析失败（%s）: %w", sanitizeProxyForLog(raw), err)
		}
		host := strings.TrimSpace(u.Host)
		if host == "" {
			return "", fmt.Errorf("代理地址缺少 host:port（%s）", sanitizeProxyForLog(raw))
		}
		if err := validateHostPort(host); err != nil {
			return "", fmt.Errorf("代理地址 host:port 不合法（%s）: %w", sanitizeProxyForLog(raw), err)
		}
		return host, nil
	}

	// 普通 host:port
	if err := validateHostPort(proxy); err != nil {
		return "", fmt.Errorf("代理地址格式不正确（%s）：请使用 http://127.0.0.1:7890 或 127.0.0.1:7890（IPv6 请用 [::1]:7890）", sanitizeProxyForLog(raw))
	}
	return proxy, nil
}

func parseProxyAuth(raw string) (*proxyAuth, error) {
	proxy := strings.TrimSpace(raw)
	if proxy == "" {
		return nil, nil
	}

	// 高级用法（如 http=...;https=...）不解析认证信息
	if !strings.Contains(proxy, "://") && strings.ContainsAny(proxy, "=;") {
		return nil, nil
	}

	// URL 格式（带 scheme）
	if strings.Contains(proxy, "://") {
		u, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("代理地址解析失败（%s）: %w", sanitizeProxyForLog(raw), err)
		}
		scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
		if scheme != "http" && scheme != "https" {
			return nil, nil
		}
		if u.User == nil {
			return nil, nil
		}
		username := u.User.Username()
		password, _ := u.User.Password()
		if username == "" {
			return nil, nil
		}
		return &proxyAuth{Username: username, Password: password}, nil
	}

	// 无 scheme：可能是 user:pass@host:port
	if strings.Contains(proxy, "@") {
		u, err := url.Parse("http://" + proxy)
		if err != nil {
			return nil, fmt.Errorf("代理地址解析失败（%s）: %w", sanitizeProxyForLog(raw), err)
		}
		if u.User == nil {
			return nil, nil
		}
		username := u.User.Username()
		password, _ := u.User.Password()
		if username == "" {
			return nil, nil
		}
		return &proxyAuth{Username: username, Password: password}, nil
	}

	return nil, nil
}

func validateHostPort(hostport string) error {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return err
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("host 不能为空")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p <= 0 || p > 65535 {
		return fmt.Errorf("port 非法: %s", port)
	}
	return nil
}

func sanitizeProxyForLog(raw string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return ""
	}
	// 优先按 URL 解析，便于脱敏 userinfo
	if strings.Contains(p, "://") {
		if u, err := url.Parse(p); err == nil {
			host := strings.TrimSpace(u.Host)
			if host == "" {
				host = strings.TrimSpace(u.Opaque)
			}
			if host == "" {
				return "***"
			}
			scheme := strings.TrimSpace(u.Scheme)
			if scheme == "" {
				return host
			}
			if u.User != nil {
				return scheme + "://***@" + host
			}
			return scheme + "://" + host
		}
	}

	// 无 scheme：简单按 @ 脱敏
	if idx := strings.LastIndex(p, "@"); idx >= 0 {
		return "***@" + p[idx+1:]
	}
	return p
}

// Close 关闭浏览器
func (b *Browser) Close() {
	b.browser.MustClose()
	b.launcher.Cleanup()
}

// NewPage 创建新页面（带 stealth 模式）
func (b *Browser) NewPage() *rod.Page {
	page := stealth.MustPage(b.browser)
	if b != nil && b.proxyAuth != nil && strings.TrimSpace(b.proxyAuth.Username) != "" {
		// 仅在需要代理认证时启用 Fetch 拦截，否则会带来额外开销。
		b.browser.EnableDomain(page.SessionID, &proto.FetchEnable{HandleAuthRequests: true})
	}
	return page
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
