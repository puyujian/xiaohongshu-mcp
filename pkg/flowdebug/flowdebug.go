package flowdebug

import "context"

// Debugger 用于在流程执行过程中输出可视化调试事件，并支持暂停/继续。
// 该接口尽量保持简单，方便在各业务流程中按需埋点。
type Debugger interface {
	// Step 记录一个“步骤”事件（用于 UI 上展示关键流程节点）。
	Step(name string, fields map[string]any)
	// Log 记录一条日志事件（用于 UI 上展示更细粒度的信息）。
	Log(level string, message string, fields map[string]any)
	// WaitIfPaused 在当前调试会话处于暂停状态时阻塞，直到恢复或 ctx 取消。
	WaitIfPaused(ctx context.Context) error
}

type debuggerKey struct{}

// WithDebugger 将 Debugger 写入 context，供流程内部按需取用。
func WithDebugger(ctx context.Context, dbg Debugger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, debuggerKey{}, dbg)
}

// FromContext 从 context 中获取 Debugger。
func FromContext(ctx context.Context) Debugger {
	if ctx == nil {
		return nil
	}
	if v := ctx.Value(debuggerKey{}); v != nil {
		if dbg, ok := v.(Debugger); ok {
			return dbg
		}
	}
	return nil
}
