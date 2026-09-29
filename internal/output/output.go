// Package output 定义稳定 JSON envelope（schema v1）、错误结构与退出码映射。
//
// 契约：
//   - 成功与失败的最终 envelope 都只写 stdout，且 stdout 只能有一个 JSON 文档；
//   - 失败同时返回非零退出码；
//   - data 与 error 始终存在，不适用的一侧为 null；
//   - warnings 为字符串数组（兼容既有消费者），notices 为结构化数组；
//   - 稳定响应结构中的空集合是 []，不因 omitempty 消失；
//   - 机器判断必须使用 code/kind/retryable/action.args，不解析英文句子。
package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// SchemaVersion 是 JSON envelope 的整数版本号。
const SchemaVersion = 1

// Executable 是 CLI 的正式可执行文件名。
const Executable = "mys-cli"

// 稳定错误码。
const (
	CodeInputInvalid         = "INPUT_INVALID"
	CodeAuthInvalid          = "AUTH_INVALID"
	CodeCapabilityRequired   = "AUTH_CAPABILITY_REQUIRED"
	CodeProtocolRejected     = "PROTOCOL_PROFILE_REJECTED"
	CodeRemoteRejected       = "REMOTE_REJECTED"
	CodeRemoteUnknown        = "REMOTE_RESULT_UNKNOWN"
	CodeProtocolMismatch     = "PROTOCOL_MISMATCH"
	CodePartialFailure       = "PARTIAL_FAILURE"
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

// 进程退出码。
const (
	ExitOK       = 0
	ExitInternal = 1 // 内部错误
	ExitInput    = 2 // 输入/能力门禁
	ExitAuth     = 3 // 认证
	ExitRejected = 4 // 服务端明确拒绝
	ExitUnknown  = 5 // 结果未知/部分失败
)

var exitByCode = map[string]int{
	CodeInputInvalid:         ExitInput,
	CodeAuthInvalid:          ExitAuth,
	CodeCapabilityRequired:   ExitInput,
	CodeProtocolRejected:     ExitAuth,
	CodeRemoteRejected:       ExitRejected,
	CodeRemoteUnknown:        ExitUnknown,
	CodeProtocolMismatch:     ExitUnknown,
	CodePartialFailure:       ExitUnknown,
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

// kindByCode 把错误码映射为机器可用的错误类别。
var kindByCode = map[string]string{
	CodeInputInvalid:         "input",
	CodeAuthInvalid:          "auth",
	CodeCapabilityRequired:   "capability",
	CodeProtocolRejected:     "protocol",
	CodeProtocolMismatch:     "protocol",
	CodePartialFailure:       "partial",
	CodeRemoteRejected:       "remote",
	CodeRemoteUnknown:        "unknown_result",
	CodeFeatureUnavailable:   "capability",
	CodeContentConflict:      "conflict",
	CodeOperationUnresolved:  "unknown_result",
	CodeOrphanMediaAckNeeded: "confirmation",
	CodeStorePermission:      "storage",
	CodeStoreTarget:          "storage",
	CodeStoreCorrupt:         "storage",
	CodeStoreIO:              "storage",
	CodeLoginTimeout:         "auth",
	CodeCancelled:            "cancelled",
	CodeInternal:             "internal",
}

// Action 是给 Agent 的安全下一步：只含可执行文件与参数数组，绝不拼接 shell 字符串。
type Action struct {
	Type       string   `json:"type"`
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

// RunCommand 构造 type=run_command 的安全 action。
func RunCommand(args ...string) *Action {
	if args == nil {
		args = []string{}
	}
	return &Action{Type: "run_command", Executable: Executable, Args: args}
}

// Notice 结构化提示；不影响结果完整性的异常用它表达。
type Notice struct {
	Level   string         `json:"level"` // info | warning | error
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Action  *Action        `json:"action"`
	Context map[string]any `json:"context"`
}

// NewNotice 构造 notice；context 为空时输出 {}。
func NewNotice(level, code, message string) Notice {
	return Notice{Level: level, Code: code, Message: message}
}

// Error 是所有命令层与适配器层统一返回的错误类型。
// JSON 形态由 MarshalJSON 固定；Retcode/Transport/Warnings
// 等仅内部使用，不进入输出。
type Error struct {
	Code    string         `json:"code"`
	Kind    string         `json:"kind"`
	Message string         `json:"message"`
	Context map[string]any `json:"context"`
	Action  *Action        `json:"action"`

	// 内部字段（不序列化到固定 wire 结构）。
	Exit      int      `json:"-"`
	Retcode   int      `json:"-"`
	Transport bool     `json:"-"`
	Warnings  []string `json:"-"`

	// 部分失败的可恢复信息。
	PartialData  any      `json:"-"`
	ResumeCursor string   `json:"-"`
	ResumeArgs   []string `json:"-"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Retryable 报告同一命令稍后重试是否可能成功：只有传输层失败为 true。
func (e *Error) Retryable() bool { return e.Transport }

// MarshalJSON 输出固定的错误 wire 结构：所有契约字段始终存在。
func (e *Error) MarshalJSON() ([]byte, error) {
	type wire struct {
		Code          string         `json:"code"`
		Kind          string         `json:"kind"`
		Message       string         `json:"message"`
		RemoteCode    any            `json:"remote_code"`
		RemoteMessage any            `json:"remote_message"`
		Retryable     bool           `json:"retryable"`
		Context       map[string]any `json:"context"`
		Action        *Action        `json:"action"`
		PartialData   any            `json:"partial_data"`
		ResumeCursor  any            `json:"resume_cursor"`
		ResumeArgs    []string       `json:"resume_args"`
	}
	w := wire{
		Code:        e.Code,
		Kind:        e.kind(),
		Message:     e.Message,
		Retryable:   e.Retryable(),
		Context:     e.Context,
		Action:      e.Action,
		PartialData: e.PartialData,
		ResumeArgs:  e.ResumeArgs,
	}
	if e.Context == nil {
		w.Context = map[string]any{}
	}
	if e.ResumeArgs == nil {
		w.ResumeArgs = []string{}
	}
	if e.Retcode != 0 {
		w.RemoteCode = e.Retcode
	}
	if e.ResumeCursor != "" {
		w.ResumeCursor = e.ResumeCursor
	}
	return json.Marshal(w)
}

func (e *Error) kind() string {
	if k, ok := kindByCode[e.Code]; ok {
		return k
	}
	return "internal"
}

// Err 构造带退出码映射的错误；未登记的 code 按 ExitInternal 处理。
func Err(code, format string, args ...any) *Error {
	exit, ok := exitByCode[code]
	if !ok {
		exit = ExitInternal
	}
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Exit: exit}
}

// WithRetcode 返回仅携带上游数值返回码的错误副本；不接收上游错误原文。
func (e *Error) WithRetcode(rc int) *Error {
	cp := *e
	cp.Retcode = rc
	return &cp
}

// WithAction 返回附带安全下一步的错误副本。
func (e *Error) WithAction(a *Action) *Error {
	cp := *e
	cp.Action = a
	return &cp
}

// WithContext 返回附带脱敏上下文的错误副本。
func (e *Error) WithContext(ctx map[string]any) *Error {
	cp := *e
	cp.Context = ctx
	return &cp
}

// Envelope 是 --json 模式下唯一的 stdout JSON 文档（成功与失败共用）。
type Envelope struct {
	SchemaVersion int      `json:"schema_version"`
	OK            bool     `json:"ok"`
	Data          any      `json:"data"`
	NextCursor    string   `json:"next_cursor"`
	Warnings      []string `json:"warnings"`
	Notices       []Notice `json:"notices"`
	Error         *Error   `json:"error"`
}

// Pagination 描述本次列表结果能否续页。
type Pagination struct {
	Mode      string   `json:"mode"` // cursor | preview
	Resumable bool     `json:"resumable"`
	NextArgs  []string `json:"next_args"`
}

// ListData 是列表命令 data 的统一形态。
type ListData struct {
	Kind       string         `json:"kind"`
	Context    map[string]any `json:"context"`
	Items      any            `json:"items"`
	HasMore    bool           `json:"has_more"`
	Pagination Pagination     `json:"pagination"`
}

// NewListData 构造列表 data；items 为 nil 时输出 []，context 为 nil 时输出 {}。
func NewListData(items any, hasMore bool, ctx map[string]any, pag Pagination) ListData {
	if items == nil {
		items = []any{}
	}
	if ctx == nil {
		ctx = map[string]any{}
	}
	if pag.NextArgs == nil {
		pag.NextArgs = []string{}
	}
	if pag.Mode == "" {
		pag.Mode = "cursor"
	}
	return ListData{Kind: "list", Context: ctx, Items: items, HasMore: hasMore, Pagination: pag}
}

// Success 输出成功 envelope。
func Success(w io.Writer, data any, nextCursor string, warnings []string) error {
	return SuccessNotices(w, data, nextCursor, warnings, nil)
}

// SuccessNotices 输出带结构化 notices 的成功 envelope。
func SuccessNotices(w io.Writer, data any, nextCursor string, warnings []string, notices []Notice) error {
	if data == nil {
		data = struct{}{}
	}
	env := Envelope{
		SchemaVersion: SchemaVersion,
		OK:            true,
		Data:          data,
		NextCursor:    nextCursor,
		Warnings:      nonNilStrings(warnings),
		Notices:       nonNilNotices(notices),
		Error:         nil,
	}
	return encode(w, env)
}

// Failure 输出失败 envelope。错误对象携带的 Warnings（如会话提示）
// 继续经 envelope 的 warnings 输出，不因失败丢失。
func Failure(w io.Writer, e *Error) error {
	env := Envelope{
		SchemaVersion: SchemaVersion,
		OK:            false,
		Data:          nil,
		NextCursor:    "",
		Warnings:      nonNilStrings(e.Warnings),
		Notices:       []Notice{},
		Error:         e,
	}
	return encode(w, env)
}

func encode(w io.Writer, env Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(env)
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilNotices(n []Notice) []Notice {
	if n == nil {
		return []Notice{}
	}
	for i := range n {
		if n[i].Context == nil {
			n[i].Context = map[string]any{}
		}
	}
	return n
}
