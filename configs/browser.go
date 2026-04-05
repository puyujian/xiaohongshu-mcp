package configs

var (
	useHeadless = true
	binPath     = ""
	proxy       = "" // 登录/发布代理地址
	proxyPool   = "" // 登录/发布代理池地址
	userAgent   = "" // 浏览器 User-Agent
	userDataDir = "" // 用户数据目录
)

func InitHeadless(h bool) {
	useHeadless = h
}

// IsHeadless 是否无头模式
func IsHeadless() bool {
	return useHeadless
}

func SetBinPath(b string) {
	binPath = b
}

func GetBinPath() string {
	return binPath
}

// SetProxy 设置登录/发布代理地址
func SetProxy(p string) {
	proxy = p
}

// GetProxy 获取登录/发布代理地址
func GetProxy() string {
	return proxy
}

// SetProxyPool 设置登录/发布代理池地址
func SetProxyPool(p string) {
	proxyPool = p
}

// GetProxyPool 获取登录/发布代理池地址
func GetProxyPool() string {
	return proxyPool
}

// SetUserAgent 设置浏览器 User-Agent
func SetUserAgent(ua string) {
	userAgent = ua
}

// GetUserAgent 获取浏览器 User-Agent
func GetUserAgent() string {
	return userAgent
}

// SetUserDataDir 设置用户数据目录
func SetUserDataDir(dir string) {
	userDataDir = dir
}

// GetUserDataDir 获取用户数据目录
func GetUserDataDir() string {
	return userDataDir
}
