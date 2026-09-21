package auth

// ma-cn-passport 扫码登录链路（2026-09-15 实测打通）：
//
//	POST /account/ma-cn-passport/app/createQRLogin（body 恒为 {}）
//	  → data.url = user.mihoyo.com/login-platform/mobile.html?…&token_types=1
//	  → data.ticket（UUID，与 url 中 tk 同值）
//	POST /account/ma-cn-passport/app/queryQRLoginStatus（body={"ticket":…}）
//	  → data.status: Created → Scanned → Confirmed；二维码失效 retcode=-3501
//	Confirmed 直出 data.tokens=[{token_type:1, token:"v2_…"}]（SToken v2）
//	与 data.user_info.{aid,mid}；无 ltoken，需要时经 getLTokenBySToken 换取。
//
// 关键契约：
//   - 请求方身份由 x-rpc-app_id=bll8iq97cem8（米游社）决定；建码即声明
//     token_types=1，确认后服务端签发 SToken；
//   - 请求体字节与 passport DS 完全绑定；实测 DS 可选，但恒发送以贴近 App；
//   - Confirmed 后仅接受 token_type=1 的 Token，且 aid/mid 必须同时存在；
//   - 过期（-3501）触发重建二维码，但不重置总等待上限；
//   - 不保存任何中间凭据（本链路无 Game Token 中间态）。

import (
	"context"
	"encoding/json"
	"time"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/store"
)

// passportQRData 是 createQRLogin 的 data 字段。
type passportQRData struct {
	URL    string `json:"url"`
	Ticket string `json:"ticket"`
}

// passportToken 是 Confirmed 响应 tokens 数组的元素。
type passportToken struct {
	TokenType int    `json:"token_type"`
	Token     string `json:"token"`
}

// passportConfirmData 是 queryQRLoginStatus 的 data 字段。
type passportConfirmData struct {
	Status   string          `json:"status"`
	Tokens   []passportToken `json:"tokens"`
	UserInfo struct {
		AID api.FlexString `json:"aid"`
		MID api.FlexString `json:"mid"`
	} `json:"user_info"`
}

// passportScan 是扫码确认后提取的凭据（仅用于保存）。
type passportScan struct {
	UID    string
	MID    string
	SToken string
}

// LoginPassport 执行 ma-cn-passport 扫码登录。与 Login（HK4E 游戏码 +
// Game Token 交换）相比少一步交换：Confirmed 响应直接携带 SToken。
// 渲染器的清理由调用方负责（正常退出、错误、取消都要清理）。
func (s *Service) LoginPassport(ctx context.Context, cfg Config, render Renderer, progress ProgressFunc) (*store.Credentials, *output.Error) {
	if s.PassportClient == nil {
		return nil, output.Err(output.CodeInternal, "PassportClient 未配置")
	}
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
	flowCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	device, oerr := s.loadDevice(flowCtx)
	if oerr != nil {
		return nil, oerr
	}
	progress("device_ready")

	var scan *passportScan
	for {
		if err := flowCtx.Err(); err != nil {
			return nil, loginFlowError(err, cfg.Timeout)
		}
		if !s.now().Before(deadline) {
			return nil, output.Err(output.CodeLoginTimeout,
				"登录等待超过总时限 %s", cfg.Timeout)
		}
		qr, oerr := s.createPassportQR(flowCtx, device)
		if oerr != nil {
			return nil, oerr
		}
		if _, rerr := render.Render(qr.URL); rerr != nil {
			return nil, output.Err(output.CodeInternal, "%v", rerr)
		}
		progress("qr_ready")

		res, oerr, expired := s.pollPassportConfirm(flowCtx, cfg, deadline, qr.Ticket, device, progress)
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

	progress("saving")
	creds := store.NewCredentialsFlow(store.FlowV2,
		scan.UID, scan.MID, scan.SToken, device.DeviceID, device.DeviceFP, s.now())
	if oerr := s.Store.Save(creds); oerr != nil {
		return nil, oerr
	}
	return creds, nil
}

// createPassportQR 请求建码。请求体恒为 "{}"，DS 与之绑定。
func (s *Service) createPassportQR(ctx context.Context, dev protocol.DeviceContext) (*passportQRData, *output.Error) {
	const body = "{}"
	h := protocol.WithDS(protocol.PassportQRHeaders(dev), protocol.NewDSPassport(body))
	var data passportQRData
	if oerr := s.PassportClient.DoJSON(ctx, "POST", "/account/ma-cn-passport/app/createQRLogin", nil, []byte(body), h, &data); oerr != nil {
		return nil, oerr
	}
	if data.URL == "" || data.Ticket == "" {
		return nil, output.Err(output.CodeRemoteRejected, "建码响应缺少 url/ticket")
	}
	return &data, nil
}

// pollPassportConfirm 轮询扫码状态直到 Confirmed / 二维码失效 / 失败。
// 返回值 expired 表示二维码已失效（-3501），应重建二维码但不重置总时限。
func (s *Service) pollPassportConfirm(ctx context.Context, cfg Config, deadline time.Time, ticket string, dev protocol.DeviceContext, progress ProgressFunc) (*passportScan, *output.Error, bool) {
	// 请求体字节固定一次生成，DS 必须与每次发送的字节完全一致。
	body, err := json.Marshal(struct {
		Ticket string `json:"ticket"`
	}{Ticket: ticket})
	if err != nil {
		return nil, output.Err(output.CodeInternal, "构造轮询请求失败: %v", err), false
	}
	fails := 0
	lastStat := ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, loginFlowError(err, cfg.Timeout), false
		}
		now := s.now()
		rem := deadline.Sub(now)
		if rem <= 0 {
			return nil, output.Err(output.CodeLoginTimeout, "登录等待超过总时限 %s", cfg.Timeout), false
		}

		reqCtx, cancel := context.WithTimeout(ctx, minDuration(cfg.RequestTimeout, rem))
		h := protocol.WithDS(protocol.PassportQRHeaders(dev), protocol.NewDSPassport(string(body)))
		var data passportConfirmData
		oerr := s.PassportClient.DoJSON(reqCtx, "POST", "/account/ma-cn-passport/app/queryQRLoginStatus", nil, body, h, &data)
		cancel()

		if oerr != nil {
			if err := ctx.Err(); err != nil {
				return nil, loginFlowError(err, cfg.Timeout), false
			}
			// 二维码失效（实测 -3501）触发重建，不计入连续失败。
			if oerr.Retcode == -3501 {
				return nil, nil, true
			}
			fails++
			if fails >= cfg.MaxPollFails {
				return nil, output.Err(output.CodeRemoteRejected,
					"查询扫码状态连续失败 %d 次: %s", fails, oerr.Message), false
			}
			if !sleepCtx(ctx, minDuration(cfg.PollInterval, rem)) {
				return nil, loginFlowError(ctx.Err(), cfg.Timeout), false
			}
			continue
		}
		fails = 0

		switch data.Status {
		case "Created":
			if lastStat != "Created" {
				progress("waiting_scan")
				lastStat = "Created"
			}
		case "Scanned":
			if lastStat != "Scanned" {
				progress("waiting_confirm")
				lastStat = "Scanned"
			}
		case "Confirmed":
			scan, oerr := parsePassportConfirmed(data)
			if oerr != nil {
				return nil, oerr, false
			}
			progress("confirmed")
			return scan, nil, false
		default:
			return nil, output.Err(output.CodeRemoteRejected, "未知扫码状态 %q", data.Status), false
		}

		if !sleepCtx(ctx, minDuration(cfg.PollInterval, rem)) {
			return nil, loginFlowError(ctx.Err(), cfg.Timeout), false
		}
	}
}

// parsePassportConfirmed 提取 SToken：仅接受 token_type=1，
// 且 aid/mid 必须同时存在（SToken v2 与 mid 配套）。
func parsePassportConfirmed(data passportConfirmData) (*passportScan, *output.Error) {
	var stoken string
	for _, t := range data.Tokens {
		if t.TokenType == 1 && t.Token != "" {
			stoken = t.Token
			break
		}
	}
	if stoken == "" {
		return nil, output.Err(output.CodeRemoteRejected, "确认响应缺少 SToken（token_type=1）")
	}
	uid := data.UserInfo.AID.String()
	if uid == "" {
		return nil, output.Err(output.CodeRemoteRejected, "确认响应缺少 aid")
	}
	mid := data.UserInfo.MID.String()
	if mid == "" {
		return nil, output.Err(output.CodeRemoteRejected, "确认响应缺少 MID")
	}
	return &passportScan{UID: uid, MID: mid, SToken: stoken}, nil
}
