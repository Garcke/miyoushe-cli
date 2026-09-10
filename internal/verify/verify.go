// Package verify 实现 `mys auth verify`：在线验证社区能力，不打印凭据。
//
// 判断依据（社区功能设计 §7）：
//   - 通过 getUserGameRolesByStoken（已证明无副作用的只读请求）确认
//     服务端是否接受 SToken 与 protocol profile；
//   - 能力状态只允许 available / account_denied / not_implemented / unknown；
//   - “retcode 0”不推断为全部可写；写能力因缺少 adapter_ready 适配器
//     与安全权限检查，报告 not_implemented。
package verify

import (
	"context"

	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/role"
	"mihoyo_cli/internal/session"
)

// 能力状态。
const (
	StateAvailable      = "available"
	StateAccountDenied  = "account_denied"
	StateNotImplemented = "not_implemented"
	StateUnknown        = "unknown"
)

// CapabilityStatus 是单个能力的状态与判断依据。
type CapabilityStatus struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

// Report 是 auth verify 的输出结构。
type Report struct {
	CredentialsValid bool               `json:"credentials_valid"`
	ServerAccepted   bool               `json:"server_accepted"`
	ProtocolProfile  string             `json:"protocol_profile"`
	Capabilities     []CapabilityStatus `json:"capabilities"`
}

// RoleChecker 是 verify 依赖的只读检查接口（role.Service 实现）。
type RoleChecker interface {
	List(ctx context.Context, sess session.Session, gameBiz string) ([]role.Role, *output.Error)
}

// Check 执行在线验证。返回 (report, nil) 表示报告可用；
// 服务端明确拒绝时返回错误（AUTH_INVALID / PROTOCOL_PROFILE_REJECTED /
// REMOTE_REJECTED），由 CLI 按退出码表处理。
func Check(ctx context.Context, sess session.Session, checker RoleChecker) (Report, *output.Error) {
	report := Report{
		CredentialsValid: true,
		ProtocolProfile:  "unknown",
	}
	_, oerr := checker.List(ctx, sess, "")
	switch {
	case oerr == nil:
		report.ServerAccepted = true
		report.ProtocolProfile = "accepted"
		report.Capabilities = []CapabilityStatus{
			{Name: string(session.CapReadAccount), State: StateAvailable,
				Reason: "getUserGameRolesByStoken 返回 retcode=0"},
			notImplemented(session.CapWritePost),
			notImplemented(session.CapUploadImage),
			notImplemented(session.CapUploadVideo),
		}
		return report, nil
	case oerr.Code == output.CodeAuthInvalid:
		report.ServerAccepted = false
		report.Capabilities = []CapabilityStatus{
			{Name: string(session.CapReadAccount), State: StateUnknown,
				Reason: "服务端拒绝当前会话"},
		}
		return report, oerr
	case oerr.Code == output.CodeProtocolRejected:
		report.Capabilities = []CapabilityStatus{
			{Name: string(session.CapReadAccount), State: StateUnknown,
				Reason: "protocol profile 被网关拒绝"},
		}
		return report, oerr
	case oerr.Retcode == 1001:
		// 权限不足：登录态与 DS/头组合被接受，账号层面无权限。
		report.ServerAccepted = true
		report.ProtocolProfile = "accepted"
		report.Capabilities = []CapabilityStatus{
			{Name: string(session.CapReadAccount), State: StateAccountDenied,
				Reason: "接口返回权限不足（retcode=1001）"},
			notImplemented(session.CapWritePost),
			notImplemented(session.CapUploadImage),
			notImplemented(session.CapUploadVideo),
		}
		return report, nil
	default:
		report.Capabilities = []CapabilityStatus{
			{Name: string(session.CapReadAccount), State: StateUnknown,
				Reason: oerr.Message},
		}
		return report, oerr
	}
}

func notImplemented(cap session.Capability) CapabilityStatus {
	return CapabilityStatus{
		Name:   string(cap),
		State:  StateNotImplemented,
		Reason: "适配器未达到 adapter_ready 门禁（缺脱敏 fixture 与契约测试）",
	}
}
