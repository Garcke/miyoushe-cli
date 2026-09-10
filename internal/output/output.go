// Package output 定义稳定 JSON envelope、错误码与退出码映射。
//
// 机器判断必须使用 JSON error.code，不能依赖本地化 message。
// envelope 形态由社区功能设计 §13 固定：
//
//	成功: {"ok":true,"data":{},"next_cursor":"","warnings":[]}
//	失败: {"ok":false,"error":{"code":"AUTH_INVALID","message":"..."}}
package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// 稳定错误码（社区功能设计 §7）。
const (
	CodeInputInvalid         = "INPUT_INVALID"
	CodeAuthInvalid          = "AUTH_INVALID"
	CodeCapabilityRequired   = "AUTH_CAPABILITY_REQUIRED"
	CodeProtocolRejected     = "PROTOCOL_PROFILE_REJECTED"
	CodeRemoteRejected       = "REMOTE_REJECTED"
	CodeRemoteUnknown        = "REMOTE_RESULT_UNKNOWN"
	CodeFeatureUnavailable   = "FEATURE_UNAVAILABLE"
	CodeContentConflict      = "CONTENT_CONFLICT"
	CodeOperationUnresolved  = "OPERATION_UNRESOLVED"
	CodeOrphanMediaAckNeeded = "ORPHAN_MEDIA_ACK_REQUIRED"

	// 本地错误码：认证与凭据存储阶段专用，不属于远端协议分类。
	CodeStorePermission = "STORE_PERMISSION_UNSAFE"
	CodeStoreTarget     = "STORE_TARGET_INVALID"
	CodeStoreCorrupt    = "STORE_CORRUPT"
	CodeStoreIO         = "STORE_IO_ERROR"
	CodeLoginTimeout    = "LOGIN_TIMEOUT"
	CodeCancelled       = "CANCELLED"
	CodeInternal        = "INTERNAL"
)

// 进程退出码（社区功能设计 §13）。
const (
	ExitOK       = 0
	ExitInternal = 1 // 仅用于未分类的本地内部错误
	ExitInput    = 2 // 输入/确认/功能门禁/内容冲突
	ExitAuth     = 3 // 登录或 protocol profile
	ExitRejected = 4 // 服务端明确拒绝
	ExitUnknown  = 5 // 服务端结果未知或未决操作
)

var exitByCode = map[string]int{
	CodeInputInvalid:         ExitInput,
	CodeAuthInvalid:          ExitAuth,
	CodeCapabilityRequired:   ExitInput,
	CodeProtocolRejected:     ExitAuth,
	CodeRemoteRejected:       ExitRejected,
	CodeRemoteUnknown:        ExitUnknown,
	CodeFeatureUnavailable:   ExitInput,
	CodeContentConflict:      ExitInput,
	CodeOperationUnresolved:  ExitUnknown,
	CodeOrphanMediaAckNeeded: ExitInput,
	CodeStorePermission:      ExitInternal,
	CodeStoreTarget:          ExitInternal,
	CodeStoreCorrupt:         ExitInternal,
	CodeStoreIO:              ExitInternal,
	CodeLoginTimeout:         ExitAuth,
	CodeCancelled:            ExitInternal,
	CodeInternal:             ExitInternal,
}

// Error 是所有命令层与适配器层统一返回的错误类型。
// Retcode 保留上游返回码（仅为分类用，不进入 JSON 输出）；
// Transport 标记该错误发生在请求发出/响应接收之前，调用方（如交换流程）
// 可据此把“结果未知”与“服务端明确拒绝”区分开。
type Error struct {
	Code         string   `json:"code"`
	Message      string   `json:"message"`
	Exit         int      `json:"-"`
	Retcode      int      `json:"-"`
	Transport    bool     `json:"-"`
	Warnings     []string `json:"warnings,omitempty"`
	PartialData  any      `json:"partial_data,omitempty"`
	ResumeCursor string   `json:"resume_cursor,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Err 构造带退出码映射的错误；未登记的 code 按 ExitInternal 处理。
func Err(code, format string, args ...any) *Error {
	exit, ok := exitByCode[code]
	if !ok {
		exit = ExitInternal
	}
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Exit: exit}
}

// WithRetcode 返回携带上游返回码的错误副本（用于 -104/-106 等二维码状态判断）。
func (e *Error) WithRetcode(rc int) *Error {
	cp := *e
	cp.Retcode = rc
	return &cp
}

// Envelope 是 --json 模式下唯一的 stdout JSON 文档。
type Envelope struct {
	OK         bool     `json:"ok"`
	Data       any      `json:"data,omitempty"`
	NextCursor string   `json:"next_cursor"`
	Warnings   []string `json:"warnings,omitempty"`
	Error      *Error   `json:"error,omitempty"`
}

// ListData 是列表命令 data 的统一形态：items + has_more；
// 游标放在 envelope 的 next_cursor 字段。
type ListData struct {
	Items   any  `json:"items"`
	HasMore bool `json:"has_more"`
}

// Success 输出成功 envelope。data 为 nil 时输出 "data": {}。
func Success(w io.Writer, data any, nextCursor string, warnings []string) error {
	if data == nil {
		data = struct{}{}
	}
	if warnings == nil {
		warnings = []string{}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(Envelope{OK: true, Data: data, NextCursor: nextCursor, Warnings: warnings})
}

// Failure 输出失败 envelope（调用方负责写到 stderr）。
func Failure(w io.Writer, e *Error) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(Envelope{OK: false, Error: e})
}

// MaskID 返回账号标识的脱敏形式，绝不输出原文。
func MaskID(id string) string {
	r := []rune(id)
	if len(r) <= 4 {
		return "****"
	}
	return string(r[:2]) + "****" + string(r[len(r)-2:])
}
