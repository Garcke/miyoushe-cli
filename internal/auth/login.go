// Package auth 实现 HK4E 扫码登录状态机与 Game Token → SToken 严格交换。
//
// 关键契约（认证设计）：
//   - 复用已有凭据中的有效设备标识，不将旧 Token 发送给二维码接口；
//   - 默认总等待 300s，--timeout 可调；单次请求上限 15s 且不超出总时限；
//   - 明确的二维码过期响应触发重建二维码，但不重置总等待上限；
//   - 交换要求 retcode=0、token_type=1、非空 Token、UID 一致、MID 存在；
//   - Game Token 绝不拼接、改名或复制成 SToken；交换失败保留旧凭据；
//   - 交换请求结果不明（传输层失败）→ 报告交换未确认，不自动重试。
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/store"
)

// Config 控制登录等待行为。
type Config struct {
	Timeout        time.Duration // 总等待上限（默认 300s）
	PollInterval   time.Duration // 轮询间隔（默认 2s）
	RequestTimeout time.Duration // 单次请求上限（默认 15s）
	MaxPollFails   int           // 查询连续失败上限（默认 3）
}

// DefaultConfig 返回设计文档默认值。
func DefaultConfig() Config {
	return Config{
		Timeout:        300 * time.Second,
		PollInterval:   2 * time.Second,
		RequestTimeout: 15 * time.Second,
		MaxPollFails:   3,
	}
}

// ProgressFunc 接收进度阶段标记；CLI 层负责人类可读展示或 JSON 模式静默。
type ProgressFunc func(stage string)

// Renderer 由 CLI 层注入的二维码渲染（终端 + 临时 PNG）。
type Renderer interface {
	Render(content string) (pngPath string, err error)
	PNGPath() string
	Cleanup() error
}

// Service 是登录服务；HTTP 客户端可注入以便使用本地测试服务器。
type Service struct {
	Store    *store.Store
	FPClient *api.Client // public-data-api（getFp）
	QRClient *api.Client // hk4e-sdk（二维码 fetch/query）
	ExClient *api.Client // api-takumi（getTokenByGameToken）
	Now      func() time.Time
}

// scanCredentials 是扫码确认后提取的游戏侧凭据（仅用于当前交换）。
type scanCredentials struct {
	UID   string
	MID   string
	Token string // Game Token，不落盘
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Login 执行完整登录流程。成功返回已保存的凭据。
// 渲染器的清理由调用方负责（正常退出、错误、取消都要清理）。
func (s *Service) Login(ctx context.Context, cfg Config, render Renderer, progress ProgressFunc) (*store.Credentials, *output.Error) {
	if progress == nil {
		progress = func(string) {}
	}
	def := DefaultConfig()
	if cfg.Timeout <= 0 {
		cfg.Timeout = def.Timeout
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = def.PollInterval
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = def.RequestTimeout
	}
	if cfg.MaxPollFails <= 0 {
		cfg.MaxPollFails = def.MaxPollFails
	}

	deadline := s.now().Add(cfg.Timeout)

	device, oerr := s.loadDevice(ctx)
	if oerr != nil {
		return nil, oerr
	}
	progress("device_ready")

	var scan *scanCredentials
	for {
		if !s.now().Before(deadline) {
			return nil, output.Err(output.CodeLoginTimeout,
				"登录等待超过总时限 %s，已取消", cfg.Timeout)
		}
		qrURL, ticket, oerr := s.fetchQR(ctx, device)
		if oerr != nil {
			return nil, oerr
		}
		if _, rerr := render.Render(qrURL); rerr != nil {
			return nil, output.Err(output.CodeInternal, "%v", rerr)
		}
		progress("qr_ready")

		res, oerr, expired := s.pollConfirm(ctx, cfg, deadline, ticket, device, progress)
		if expired {
			progress("qr_expired")
			continue
		}
		if oerr != nil {
			return nil, oerr
		}
		scan = res
		break
	}

	progress("exchanging")
	creds, oerr := s.exchange(ctx, device, scan)
	if oerr != nil {
		return nil, oerr
	}

	progress("saving")
	if oerr := s.Store.Save(creds); oerr != nil {
		return nil, oerr
	}
	return creds, nil
}

// loadDevice 复用已有凭据中的设备标识，否则生成新 device 并取指纹。
// 旧 Token 不发送给任何二维码/指纹接口。
func (s *Service) loadDevice(ctx context.Context) (protocol.DeviceContext, *output.Error) {
	creds, err := s.Store.Load()
	if err != nil {
		return protocol.DeviceContext{}, err
	}
	if creds != nil && creds.DeviceID != "" {
		dev := protocol.DeviceContext{DeviceID: creds.DeviceID, DeviceFP: creds.DeviceFP}
		if dev.DeviceFP != "" {
			return dev, nil
		}
		fp, oerr := s.fetchFP(ctx, dev.DeviceID)
		if oerr != nil {
			return protocol.DeviceContext{}, oerr
		}
		dev.DeviceFP = fp
		return dev, nil
	}
	dev := protocol.DeviceContext{DeviceID: newUUID()}
	fp, oerr := s.fetchFP(ctx, dev.DeviceID)
	if oerr != nil {
		return protocol.DeviceContext{}, oerr
	}
	dev.DeviceFP = fp
	return dev, nil
}

// fetchFP 调用 getFp 获取 13 位设备指纹。
// 实测错误经 data.code/data.msg 报告（retcode 恒为 0），不依赖 -502。
func (s *Service) fetchFP(ctx context.Context, deviceID string) (string, *output.Error) {
	ext, err := json.Marshal(map[string]string{"userAgent": protocol.BrowserUA})
	if err != nil {
		return "", output.Err(output.CodeInternal, "构造 ext_fields 失败: %v", err)
	}
	body, err := json.Marshal(struct {
		DeviceID  string `json:"device_id"`
		SeedID    string `json:"seed_id"`
		SeedTime  string `json:"seed_time"`
		Platform  string `json:"platform"`
		DeviceFP  string `json:"device_fp"`
		AppName   string `json:"app_name"`
		ExtFields string `json:"ext_fields"`
	}{
		DeviceID:  deviceID,
		SeedID:    newUUID(),
		SeedTime:  strconv.FormatInt(s.now().UnixMilli(), 10),
		Platform:  "4",
		DeviceFP:  randomHex(13),
		AppName:   "bbs_cn",
		ExtFields: string(ext),
	})
	if err != nil {
		return "", output.Err(output.CodeInternal, "构造 getFp 请求失败: %v", err)
	}
	var data struct {
		DeviceFP string `json:"device_fp"`
		Code     int    `json:"code"`
		Msg      string `json:"msg"`
	}
	h := protocol.CommonHeaders(protocol.ClientTypeScan, protocol.DeviceContext{DeviceID: deviceID})
	h.Set("User-Agent", protocol.BrowserUA)
	if oerr := s.FPClient.DoJSON(ctx, "POST", "/device-fp/api/getFp", nil, body, h, &data); oerr != nil {
		return "", oerr
	}
	if data.Code != 200 {
		return "", output.Err(output.CodeRemoteRejected, "设备指纹获取失败: %s", data.Msg)
	}
	if data.DeviceFP == "" {
		return "", output.Err(output.CodeRemoteRejected, "设备指纹获取失败: 响应缺少 device_fp")
	}
	return data.DeviceFP, nil
}

// fetchQR 获取二维码；返回二维码 URL 与从 URL 提取的 ticket。
func (s *Service) fetchQR(ctx context.Context, dev protocol.DeviceContext) (string, string, *output.Error) {
	q := url.Values{}
	q.Set("app_id", protocol.AppIDQR)
	q.Set("device", dev.DeviceID)
	var data struct {
		URL string `json:"url"`
	}
	h := protocol.CommonHeaders(protocol.ClientTypeScan, dev)
	if oerr := s.QRClient.DoJSON(ctx, "GET", "/hk4e_cn/combo/panda/qrcode/fetch", q, nil, h, &data); oerr != nil {
		return "", "", oerr
	}
	if data.URL == "" {
		return "", "", output.Err(output.CodeRemoteRejected, "二维码响应缺少 url")
	}
	u, err := url.Parse(data.URL)
	if err != nil {
		return "", "", output.Err(output.CodeRemoteRejected, "二维码 URL 无法解析")
	}
	ticket := u.Query().Get("ticket")
	if ticket == "" {
		return "", "", output.Err(output.CodeRemoteRejected, "二维码 URL 缺少 ticket")
	}
	return data.URL, ticket, nil
}

// pollConfirm 轮询扫码状态直到 Confirmed / 二维码过期 / 失败。
// 返回值 expired 表示二维码已过期（-104/-106），应重建二维码但不重置总时限。
func (s *Service) pollConfirm(ctx context.Context, cfg Config, deadline time.Time, ticket string, dev protocol.DeviceContext, progress ProgressFunc) (*scanCredentials, *output.Error, bool) {
	fails := 0
	lastStat := ""
	for {
		now := s.now()
		rem := deadline.Sub(now)
		if rem <= 0 {
			return nil, output.Err(output.CodeLoginTimeout, "登录等待超过总时限，已取消"), false
		}

		reqCtx, cancel := context.WithTimeout(ctx, minDuration(cfg.RequestTimeout, rem))
		stat, raw, oerr := s.queryQR(reqCtx, ticket, dev)
		cancel()

		if oerr != nil {
			if ctx.Err() != nil {
				return nil, output.Err(output.CodeCancelled, "已取消登录"), false
			}
			// 可识别的二维码过期响应触发重建，不计入连续失败。
			if oerr.Retcode == -104 || oerr.Retcode == -106 {
				return nil, nil, true
			}
			fails++
			if fails >= cfg.MaxPollFails {
				return nil, output.Err(output.CodeRemoteRejected,
					"查询扫码状态连续失败 %d 次: %s", fails, oerr.Message), false
			}
			if !sleepCtx(ctx, minDuration(cfg.PollInterval, rem)) {
				return nil, output.Err(output.CodeCancelled, "已取消登录"), false
			}
			continue
		}
		fails = 0

		switch stat {
		case "Init":
			if lastStat != "Init" {
				progress("waiting_scan")
				lastStat = "Init"
			}
		case "Scanned":
			if lastStat != "Scanned" {
				progress("waiting_confirm")
				lastStat = "Scanned"
			}
		case "Confirmed":
			scan, oerr := parseScanPayload(raw)
			if oerr != nil {
				return nil, oerr, false
			}
			progress("confirmed")
			return scan, nil, false
		default:
			return nil, output.Err(output.CodeRemoteRejected, "未知扫码状态 %q", stat), false
		}

		if !sleepCtx(ctx, minDuration(cfg.PollInterval, rem)) {
			return nil, output.Err(output.CodeCancelled, "已取消登录"), false
		}
	}
}

// queryQR 查询一次扫码状态。POST + query 参数形式与上游已验证证据一致。
func (s *Service) queryQR(ctx context.Context, ticket string, dev protocol.DeviceContext) (string, string, *output.Error) {
	q := url.Values{}
	q.Set("app_id", protocol.AppIDQR)
	q.Set("device", dev.DeviceID)
	q.Set("ticket", ticket)
	var data struct {
		Stat    string `json:"stat"`
		Payload struct {
			Raw string `json:"raw"`
		} `json:"payload"`
	}
	h := protocol.CommonHeaders(protocol.ClientTypeScan, dev)
	if oerr := s.QRClient.DoJSON(ctx, "POST", "/hk4e_cn/combo/panda/qrcode/query", q, nil, h, &data); oerr != nil {
		return "", "", oerr
	}
	return data.Stat, data.Payload.Raw, nil
}

// parseScanPayload 解析 Confirmed 后 payload.raw（JSON 字符串）。
func parseScanPayload(raw string) (*scanCredentials, *output.Error) {
	if raw == "" {
		return nil, output.Err(output.CodeRemoteRejected, "扫码确认响应缺少 payload")
	}
	var p struct {
		UID   string `json:"uid"`
		MID   string `json:"mid"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, output.Err(output.CodeRemoteRejected, "扫码 payload 不是有效 JSON")
	}
	if p.UID == "" || p.Token == "" {
		return nil, output.Err(output.CodeRemoteRejected, "扫码 payload 缺少 uid/token")
	}
	return &scanCredentials{UID: p.UID, MID: p.MID, Token: p.Token}, nil
}

// exchange 用 Game Token 严格交换 SToken。
// 交换使用 passport DS（body 绑定）；请求体字节与 DS 计算输入完全一致。
func (s *Service) exchange(ctx context.Context, dev protocol.DeviceContext, scan *scanCredentials) (*store.Credentials, *output.Error) {
	body, err := json.Marshal(struct {
		AccountID string `json:"account_id"`
		GameToken string `json:"game_token"`
	}{
		AccountID: scan.UID,
		GameToken: scan.Token,
	})
	if err != nil {
		return nil, output.Err(output.CodeInternal, "构造交换请求失败: %v", err)
	}
	h := protocol.CommonHeaders(protocol.ClientTypeAndroid, dev)
	protocol.WithDS(h, protocol.NewDSPassport(string(body)))

	raw, oerr := s.ExClient.Do(ctx, "POST", "/account/ma-cn-session/app/getTokenByGameToken", nil, body, h)
	if oerr != nil {
		if oerr.Transport {
			// 结果未知：不自动重试，也不保存 Game Token 伪装成功。
			return nil, output.Err(output.CodeRemoteUnknown,
				"SToken 交换结果未确认（网络失败），不自动重试；如需重试请重新登录")
		}
		return nil, oerr
	}

	var data struct {
		UID   api.FlexString `json:"uid"`
		Token struct {
			TokenType int    `json:"token_type"`
			Token     string `json:"token"`
		} `json:"token"`
		UserInfo struct {
			AID api.FlexString `json:"aid"`
			MID api.FlexString `json:"mid"`
		} `json:"user_info"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, output.Err(output.CodeRemoteRejected, "交换响应 data 结构与预期不符")
	}

	// 校验链（认证设计 §3.6）：任何不满足都失败且不保存。
	if data.Token.TokenType != 1 {
		return nil, output.Err(output.CodeRemoteRejected,
			"交换响应 token_type=%d，仅接受 SToken 对应值 1", data.Token.TokenType)
	}
	if data.Token.Token == "" {
		return nil, output.Err(output.CodeRemoteRejected, "交换响应缺少 Token")
	}
	exUID := data.UserInfo.AID.String()
	if exUID == "" {
		exUID = data.UID.String()
	}
	if !sameAccount(exUID, scan.UID) {
		return nil, output.Err(output.CodeRemoteRejected, "交换响应 UID 与扫码 UID 不一致")
	}
	mid := data.UserInfo.MID.String()
	if mid == "" {
		return nil, output.Err(output.CodeRemoteRejected, "交换响应缺少 MID")
	}
	if scan.MID != "" && mid != scan.MID {
		return nil, output.Err(output.CodeRemoteRejected, "交换响应 MID 与扫码 MID 不一致")
	}
	if strings.HasPrefix(data.Token.Token, "v2_") && mid == "" {
		return nil, output.Err(output.CodeRemoteRejected, "V2 Token 缺少 MID")
	}

	return store.NewCredentials(scan.UID, mid, data.Token.Token, dev.DeviceID, dev.DeviceFP, s.now()), nil
}

// sameAccount 以数值或字符串一致性比较账号标识。
func sameAccount(a, b string) bool {
	if a == b {
		return true
	}
	ua, ea := strconv.ParseUint(a, 10, 64)
	ub, eb := strconv.ParseUint(b, 10, 64)
	return ea == nil && eb == nil && ua == ub
}

// sleepCtx 可被取消的休眠；返回 false 表示 ctx 已取消。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("auth: crypto/rand 不可用: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func randomHex(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand 不可用: " + err.Error())
	}
	return hex.EncodeToString(b)[:n]
}
