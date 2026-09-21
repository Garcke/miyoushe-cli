// login_context.go 统一扫码登录流程的上下文终止分类：总时限到期与
// 用户主动取消必须是两个稳定、可区分的错误码。HK4E 与 Passport 两套
// 流程共用本文件，避免以后再次产生语义差异。
//
// 分类契约（错误码与退出码保持兼容，不改变 JSON 输出结构）：
//
//	context.DeadlineExceeded → LOGIN_TIMEOUT（退出码 3）
//	context.Canceled         → CANCELLED（退出码 1）
//	其它意外错误             → INTERNAL（退出码 1）
package auth

import (
	"context"
	"errors"
	"time"

	"mihoyo_cli/internal/output"
)

// loginFlowError 把登录流程中的上下文终止原因转换为稳定错误。
// 文案避免在超时消息中混入“已取消”，防止用户把总时限到期误读为主动取消。
func loginFlowError(err error, timeout time.Duration) *output.Error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return output.Err(output.CodeLoginTimeout, "登录等待超过总时限 %s", timeout)
	case errors.Is(err, context.Canceled):
		return output.Err(output.CodeCancelled, "用户已取消登录")
	default:
		return output.Err(output.CodeInternal, "登录流程异常中止")
	}
}
