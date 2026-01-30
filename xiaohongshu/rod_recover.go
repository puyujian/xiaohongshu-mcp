package xiaohongshu

import (
	"context"
	"fmt"
	"runtime"
)

// recoverRodPanicAsError 用于将 go-rod 的 Must 系列方法触发的 panic（通常是 error）
// 转换成普通 error 返回，避免把“请求取消/超时”等正常情况当成服务端崩溃。
//
// 注意：对运行时错误（如空指针）不做吞掉，继续 panic 交给上层 Recovery 处理。
func recoverRodPanicAsError(ctx context.Context, errp *error) {
	if errp == nil {
		return
	}

	if r := recover(); r != nil {
		if _, ok := r.(runtime.Error); ok {
			panic(r)
		}

		if ctx != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				*errp = ctxErr
				return
			}
		}

		if e, ok := r.(error); ok {
			*errp = e
			return
		}

		*errp = fmt.Errorf("%v", r)
	}
}
