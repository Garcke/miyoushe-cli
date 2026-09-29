// Package store 负责 SToken 凭据的序列化、权限检查、完整写入与原子替换。
//
// 安全契约：
//   - 凭据文件不是加密保险库：防止普通其他本地用户读取，不承诺抵御
//     当前用户权限下的恶意进程或离线磁盘读取；
//   - macOS/Linux：私有目录 0700、文件与临时文件 0600；
//   - Windows：以当前用户 SID 限制 DACL，不把 chmod(0600) 当作访问控制；
//   - 新文件从创建时就受保护；拒绝凭据目标或专用目录是符号链接/reparse
//     point 的情况；
//   - 同目录随机名私有临时文件 → 写入并同步 → 平台支持的原子替换；
//     替换失败保留旧文件，不采用“先删旧文件再改名”；
//   - 失败不覆盖有效旧凭据。
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"mihoyo_cli/internal/output"
)

// SchemaVersion 是凭据格式版本。
const SchemaVersion = 1

// FlowV1 标识 HK4E 扫码 + Game Token 交换链路。
const FlowV1 = "hk4e_game_token_exchange"

// FlowV2 标识 ma-cn-passport 扫码直出 SToken 链路（2026-09-15 实测打通）。
const FlowV2 = "ma_cn_passport_qr_login"

// Credentials 是 credentials.json 的 v1 内容。
type Credentials struct {
	SchemaVersion int     `json:"schema_version"`
	Flow          string  `json:"flow"`
	UID           string  `json:"uid"`
	MID           string  `json:"mid"`
	TokenKind     string  `json:"token_kind"`
	TokenType     int     `json:"token_type"`
	Stoken        string  `json:"stoken"`
	DeviceID      string  `json:"device_id"`
	DeviceFP      string  `json:"device_fp"`
	SavedAt       string  `json:"saved_at"`   // RFC 3339 UTC
	ExpiresAt     *string `json:"expires_at"` // 未知时为 null，不伪造固定有效期
}

// NewCredentials 构造一份通过校验的 v1 凭据（HK4E 换票链路）。
func NewCredentials(uid, mid, stoken, deviceID, deviceFP string, now time.Time) *Credentials {
	return NewCredentialsFlow(FlowV1, uid, mid, stoken, deviceID, deviceFP, now)
}

// NewCredentialsFlow 按指定登录链路构造一份通过校验的凭据。
func NewCredentialsFlow(flow, uid, mid, stoken, deviceID, deviceFP string, now time.Time) *Credentials {
	return &Credentials{
		SchemaVersion: SchemaVersion,
		Flow:          flow,
		UID:           uid,
		MID:           mid,
		TokenKind:     "stoken",
		TokenType:     1,
		Stoken:        stoken,
		DeviceID:      deviceID,
		DeviceFP:      deviceFP,
		SavedAt:       now.UTC().Format(time.RFC3339),
		ExpiresAt:     nil,
	}
}

func (c *Credentials) validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version=%d is not a supported version %d", c.SchemaVersion, SchemaVersion)
	}
	if c.Flow != FlowV1 && c.Flow != FlowV2 {
		return fmt.Errorf("flow=%q is not a supported login flow", c.Flow)
	}
	if c.TokenKind != "stoken" {
		return fmt.Errorf("token_kind=%q is invalid; only stoken is supported", c.TokenKind)
	}
	if c.TokenType != 1 {
		return fmt.Errorf("token_type=%d is invalid; only the SToken value 1 is accepted", c.TokenType)
	}
	if c.UID == "" || c.MID == "" || c.Stoken == "" {
		return errors.New("uid/mid/stoken contains an empty field")
	}
	if _, err := time.Parse(time.RFC3339, c.SavedAt); err != nil {
		return fmt.Errorf("saved_at is not an RFC3339 time: %w", err)
	}
	return nil
}

// Store 绑定专用凭据目录（<UserConfigDir>/mys）。
type Store struct {
	Dir string
}

// DefaultDir 返回默认专用目录。
func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("Cannot locate the user config directory: %w", err)
	}
	return filepath.Join(base, "mys"), nil
}

// Default 使用默认目录构造 Store。
func Default() (*Store, error) {
	dir, err := DefaultDir()
	if err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

// Path 返回凭据文件完整路径。
func (s *Store) Path() string { return filepath.Join(s.Dir, "credentials.json") }

// HardenFile 以平台安全方式把单个小文件收紧为用户私有权限。
// 供临时文件（如二维码 PNG）在创建后立即加固。
func HardenFile(path string) error { return hardenFile(path) }

// HardenDir 以平台安全方式收紧目录权限。
func HardenDir(path string) error { return hardenDir(path) }

// Load 读取凭据。文件不存在时返回 (nil, nil)。
func (s *Store) Load() (*Credentials, *output.Error) {
	path := s.Path()
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, output.Err(output.CodeStoreIO, "Failed to read credential information: %v", err)
	}
	if isReparsePath(path, fi) {
		return nil, output.Err(output.CodeStoreTarget, "Credential path is a symlink/reparse point; refusing to read")
	}
	if !fi.Mode().IsRegular() {
		return nil, output.Err(output.CodeStoreTarget, "Credential path is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, output.Err(output.CodeStoreIO, "Failed to read the credential file: %v", err)
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, output.Err(output.CodeStoreCorrupt, "Credential file is corrupt and cannot be parsed")
	}
	if err := creds.validate(); err != nil {
		return nil, output.Err(output.CodeStoreCorrupt, "Credential file validation failed: %v", err)
	}
	return &creds, nil
}

// CheckPermissions 检查专用目录与凭据文件的访问范围。
// 不安全时返回错误（不自动修复，不修改用户配置根目录的 ACL）。
func (s *Store) CheckPermissions() *output.Error {
	if err := checkDirPrivate(s.Dir); err != nil {
		return err
	}
	if _, err := os.Lstat(s.Path()); err == nil {
		if err := checkFilePrivate(s.Path()); err != nil {
			return err
		}
	}
	return nil
}

// Save 以原子替换方式保存凭据。任何失败都保留原文件。
func (s *Store) Save(creds *Credentials) *output.Error {
	if creds == nil {
		return output.Err(output.CodeInputInvalid, "Credential is empty")
	}
	if err := creds.validate(); err != nil {
		return output.Err(output.CodeInputInvalid, "Credential failed validation: %v", err)
	}

	fi, err := os.Lstat(s.Dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(s.Dir, 0o700); err != nil {
			return output.Err(output.CodeStoreIO, "Failed to create the credential directory: %v", err)
		}
		if err := hardenDir(s.Dir); err != nil {
			return output.Err(output.CodeStorePermission, "Failed to harden the credential directory: %v", err)
		}
	case err != nil:
		return output.Err(output.CodeStoreIO, "Failed to inspect the credential directory: %v", err)
	case isReparsePath(s.Dir, fi):
		return output.Err(output.CodeStoreTarget, "Credential directory is a symlink/reparse point; refusing to write")
	case !fi.Mode().IsDir():
		return output.Err(output.CodeStoreTarget, "Credential directory path is not a directory")
	default:
		if err := checkDirPrivate(s.Dir); err != nil {
			return err
		}
	}

	if _, err := os.Lstat(s.Path()); err == nil {
		if err := checkFilePrivate(s.Path()); err != nil {
			return err
		}
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return output.Err(output.CodeStoreIO, "Failed to serialize credentials: %v", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(s.Dir, ".credentials-*.tmp")
	if err != nil {
		return output.Err(output.CodeStoreIO, "Failed to create the temporary credential file: %v", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmpName)
		}
	}()

	if err := hardenFile(tmpName); err != nil {
		tmp.Close()
		return output.Err(output.CodeStorePermission, "Failed to harden the temporary credential file: %v", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return output.Err(output.CodeStoreIO, "Failed to write the temporary credential file: %v", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return output.Err(output.CodeStoreIO, "Failed to sync the temporary credential file: %v", err)
	}
	if err := tmp.Close(); err != nil {
		return output.Err(output.CodeStoreIO, "Failed to close the temporary credential file: %v", err)
	}

	if err := os.Rename(tmpName, s.Path()); err != nil {
		return output.Err(output.CodeStoreIO, "Failed to atomically replace the credential file (original kept): %v", err)
	}
	ok = true

	if err := syncDir(s.Dir); err != nil {
		// 目录同步失败不影响已完成的原子替换。
		_ = err
	}
	return nil
}

// Delete 删除本地凭据。幂等：文件不存在时返回 (false, nil)。
// 不调用任何远程注销/Token 撤销接口。
func (s *Store) Delete() (bool, *output.Error) {
	path := s.Path()
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, output.Err(output.CodeStoreIO, "Failed to inspect the credential file: %v", err)
	}
	if isReparsePath(path, fi) {
		return false, output.Err(output.CodeStoreTarget, "Credential path is a symlink/reparse point; refusing to delete")
	}
	if err := os.Remove(path); err != nil {
		return false, output.Err(output.CodeStoreIO, "Failed to delete the credential file: %v", err)
	}
	return true, nil
}
