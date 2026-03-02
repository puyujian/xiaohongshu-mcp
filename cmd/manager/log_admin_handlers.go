package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// LogOverviewItem 单个用户日志概览信息
type LogOverviewItem struct {
	UserID    string `json:"user_id"`
	LogFile   string `json:"log_file"`
	Exists    bool   `json:"exists"`
	SizeBytes int64  `json:"size_bytes"`
	Mtime     string `json:"mtime,omitempty"`
}

// LogsOverviewResponse 日志概览响应
type LogsOverviewResponse struct {
	DataDir string            `json:"data_dir"`
	Total   int               `json:"total"`
	Items   []LogOverviewItem `json:"items"`
}

// ListLogs 获取所有用户日志概览
// GET /api/admin/v1/logs
func (a *App) ListLogs(c *gin.Context) {
	dataDir := a.store.ResolveDataDir()
	users := a.store.ListUsers()

	items := make([]LogOverviewItem, 0, len(users))
	for _, u := range users {
		paths := a.proc.DerivePaths(dataDir, u.ID, u.Port)
		item := LogOverviewItem{
			UserID:  u.ID,
			LogFile: paths.LogFile,
		}

		stat, err := os.Stat(paths.LogFile)
		if os.IsNotExist(err) {
			items = append(items, item)
			continue
		}
		if err != nil {
			// 记录错误但继续处理其他用户
			item.Exists = false
			items = append(items, item)
			continue
		}

		item.Exists = true
		item.SizeBytes = stat.Size()
		item.Mtime = stat.ModTime().Format(time.RFC3339)
		items = append(items, item)
	}

	c.JSON(http.StatusOK, LogsOverviewResponse{
		DataDir: dataDir,
		Total:   len(items),
		Items:   items,
	})
}

// DeleteDebugLogs 清空用户实例日志
// DELETE /api/admin/v1/users/:id/debug/logs
func (a *App) DeleteDebugLogs(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id 不能为空"})
		return
	}

	user, ok := a.store.GetUser(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
		return
	}

	dataDir := a.store.ResolveDataDir()
	paths := a.proc.DerivePaths(dataDir, id, user.Port)

	// 文件不存在视为已清空
	if _, err := os.Stat(paths.LogFile); os.IsNotExist(err) {
		c.JSON(http.StatusOK, gin.H{
			"cleared":  true,
			"log_file": paths.LogFile,
			"message":  "日志文件不存在，无需清空",
		})
		return
	}

	// 先检查进程状态，避免不必要的系统调用失败
	if st := a.proc.GetStatus(id); st.Running {
		// 进程运行中，Windows 下文件可能被占用，提前警告
		// 但仍尝试清空，因为某些系统允许 truncate 正在写入的文件
	}

	// 优先使用 Truncate 清空内容（保留文件句柄）
	if err := os.Truncate(paths.LogFile, 0); err != nil {
		// 某些平台/场景 Truncate 可能失败，尝试用 O_TRUNC 兜底
		f, openErr := os.OpenFile(paths.LogFile, os.O_WRONLY|os.O_TRUNC, 0644)
		if openErr == nil {
			_ = f.Close()
			c.JSON(http.StatusOK, gin.H{
				"cleared":  true,
				"log_file": paths.LogFile,
				"message":  "日志已清空",
			})
			return
		}

		// Windows 下文件占用导致失败
		if st := a.proc.GetStatus(id); st.Running {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "用户进程运行中，日志文件被占用",
				"message": "请先停止用户进程后重试",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("清空日志失败: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"cleared":  true,
		"log_file": paths.LogFile,
		"message":  "日志已清空",
	})
}

// DownloadDebugLogs 下载用户实例日志（流式传输）
// GET /api/admin/v1/users/:id/debug/logs/download
func (a *App) DownloadDebugLogs(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id 不能为空"})
		return
	}

	user, ok := a.store.GetUser(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
		return
	}

	dataDir := a.store.ResolveDataDir()
	paths := a.proc.DerivePaths(dataDir, id, user.Port)

	f, err := os.Open(paths.LogFile)
	if os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "日志文件不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("打开日志文件失败: %v", err)})
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("读取日志文件状态失败: %v", err)})
		return
	}

	// 构造下载文件名
	filename := filepath.Base(paths.LogFile)
	if filename == "" || filename == "." {
		filename = id + ".log"
	}

	// 快照文件大小，避免文件增长导致 Content-Length 不一致
	fileSize := stat.Size()

	// 设置响应头
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Content-Length", strconv.FormatInt(fileSize, 10))
	c.Header("Last-Modified", stat.ModTime().UTC().Format(http.TimeFormat))
	c.Status(http.StatusOK)

	// 流式传输，使用 CopyN 限制传输字节数，避免文件增长导致超出 Content-Length
	if _, err := io.CopyN(c.Writer, f, fileSize); err != nil && err != io.EOF {
		// 传输过程中出错，记录但不再返回 JSON（头部已发送）
		_ = c.Error(err)
		return
	}

	// 尽快刷新缓冲区
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
}
