// Package session 从本地凭据在内存中派生社区会话，并做能力门禁。
// 派生 Cookie 只存在于内存边界中，不持久化、不进入日志或输出。
package session

import (
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/store"
)

// Capability 是社区能力标识（社区功能设计 §7）。
type Capability string

const (
	CapReadAccount Capability = "read-account"
	CapWritePost   Capability = "write-post"
	CapUploadImage Capability = "upload-image"
	CapUploadVideo Capability = "upload-video"
)

// Session 是已加载的社区会话。
type Session struct {
	UID             string
	MID             string
	Stoken          string
	DeviceID        string
	DeviceFP        string
	CredentialsPath string
}

// Device 返回会话绑定的设备上下文。
func (s Session) Device() protocol.DeviceContext {
	return protocol.DeviceContext{DeviceID: s.DeviceID, DeviceFP: s.DeviceFP}
}

// Cookie 派生 SToken 三件套 Cookie；仅在内存中使用。
func (s Session) Cookie() string {
	return protocol.STokenCookie(s.UID, s.Stoken, s.MID)
}

// Provider 从凭据存储加载会话。
type Provider struct {
	Store *store.Store
}

// NewProvider 构造 Provider。
func NewProvider(st *store.Store) *Provider { return &Provider{Store: st} }

// Load 加载会话；凭据不存在时返回 AUTH_INVALID。
func (p *Provider) Load() (Session, *output.Error) {
	creds, err := p.Store.Load()
	if err != nil {
		return Session{}, err
	}
	if creds == nil {
		return Session{}, output.Err(output.CodeAuthInvalid,
			"未登录：凭据文件不存在（%s），请先执行 mys auth login", p.Store.Path())
	}
	return Session{
		UID:             creds.UID,
		MID:             creds.MID,
		Stoken:          creds.Stoken,
		DeviceID:        creds.DeviceID,
		DeviceFP:        creds.DeviceFP,
		CredentialsPath: p.Store.Path(),
	}, nil
}

// Require 在 Load 之上做能力门禁。当前仅 read-account 具备完整依据；
// 写能力依赖 adapter_ready 适配器与安全权限检查，均未达成。
func (p *Provider) Require(cap Capability) (Session, *output.Error) {
	if cap != CapReadAccount {
		return Session{}, output.Err(output.CodeFeatureUnavailable,
			"能力 %s 尚未开放：适配器未达到 adapter_ready 门禁", cap)
	}
	return p.Load()
}
