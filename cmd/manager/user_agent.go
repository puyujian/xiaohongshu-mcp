package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// fallbackUserAgent 默认 UA（生成失败时使用）
const fallbackUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// uaPlatforms 常见桌面操作系统平台标识
// 包含 Windows、macOS、Linux 三大平台的多个版本
var uaPlatforms = []string{
	// Windows 平台
	"Windows NT 10.0; Win64; x64",
	"Windows NT 10.0; WOW64",
	// macOS 平台（多个版本）
	"Macintosh; Intel Mac OS X 10_15_7",
	"Macintosh; Intel Mac OS X 11_7_10",
	"Macintosh; Intel Mac OS X 12_6_9",
	"Macintosh; Intel Mac OS X 13_6_1",
	"Macintosh; Intel Mac OS X 14_2_1",
	// Linux 平台
	"X11; Linux x86_64",
	"X11; Ubuntu; Linux x86_64",
}

// chromeStableVersions 常见的 Chrome 稳定版版本号
// 定期更新以保持真实性
var chromeStableVersions = []string{
	"120.0.6099.224",
	"121.0.6167.184",
	"122.0.6261.128",
	"123.0.6312.122",
	"124.0.6367.207",
	"125.0.6422.141",
	"126.0.6478.127",
	"127.0.6533.120",
	"128.0.6613.119",
	"129.0.6668.100",
	"130.0.6723.117",
	"131.0.6778.139",
}

// generateRandomUserAgent 生成随机但合理的桌面 Chrome User-Agent
// 通过组合不同操作系统和 Chrome 版本号实现差异化
func generateRandomUserAgent() string {
	if len(uaPlatforms) == 0 || len(chromeStableVersions) == 0 {
		return fallbackUserAgent
	}

	platform := uaPlatforms[secureRandIndex(len(uaPlatforms))]
	version := chromeStableVersions[secureRandIndex(len(chromeStableVersions))]

	return fmt.Sprintf(
		"Mozilla/5.0 (%s) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36",
		platform,
		version,
	)
}

// secureRandIndex 使用加密安全的随机数生成索引
func secureRandIndex(n int) int {
	if n <= 0 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}
