// Package note 实现游戏便签（实时便笺）查询。
//
// 契约（2026-09-21 实测，App HAR + 多策略凭据对照）：
//   - 便签**必须用 LToken**（stoken 直调一律 10001/-100）；LToken 由
//     `GET passport-api/account/auth/api/getLTokenBySToken?stuid=&stoken=&mid=` 换取；
//   - 请求头 client_type=5 + DS2（4X 盐，query 参与签名）+ LToken Cookie；
//   - 原神：`GET api-takumi-record.mihoyo.com/game_record/app/genshin/api/dailyNote?server=&role_id=`
//     （server=cn_gf01 等，role_id=游戏 uid）；
//   - 绝区零：`GET api-takumi-record.mihoyo.com/event/game_record_zzz/api/zzz/note?server=&role_id=`
//     （server=prod_gf_cn）；App 实测该域不带 DS 亦可，本包统一带 DS2；
//   - 其他游戏（星铁/崩3 等）端点未实测，返回 Supported=false 而不是猜测。
package note

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/role"
	"mihoyo_cli/internal/session"
)

// HostRecord 是战绩/便签域（与 business 域分离）。
const HostRecord = "api-takumi-record.mihoyo.com"

// toolPage 是各游戏工具页标识（2026-09-21 App HAR 实抓）。便签域风控依赖
// 这些头区分"官方工具页"与裸 API 调用：缺失时原神返回 1034（geetest 验证码）、
// 绝区零返回 10035。字段照 HAR 原样，不做推测。
type toolPage struct {
	ToolVersion string // x-rpc-tool_verison（仅原神 HAR 携带）
	Page        string // x-rpc-page（工具页 SPA 路由）
	Origin      string // Origin/Referer
	Platform    string // x-rpc-platform（仅绝区零 HAR 携带）
}

var toolPages = map[string]toolPage{
	"hk4e_cn": {
		ToolVersion: "v7.0.4-gr-cn",
		Page:        "v7.0.4-gr-cn_#/ys",
		Origin:      "https://webstatic.mihoyo.com/",
	},
	"nap_cn": {
		Page:     "v3.0.15_#/zzz",
		Origin:   "https://act.mihoyo.com/",
		Platform: "2",
	},
}

// Service 是便签服务。Record 指向战绩域，Passport 用于换取 LToken。
//
// RecordDevice 是记录域的设备身份覆盖（可选）。2026-09-21 实测：记录域有
// **设备级信任风控**——App 长期使用的设备（含其 device_fp）可直接调用；
// 全新设备即使即时通过 getFp 注册指纹，也会被 1034/5003/10035/10041 拒绝。
// 未设置时回退会话设备（此时可能被风控拒绝，错误消息会给出配置指引）。
type Service struct {
	Record       *api.Client
	Passport     *api.Client
	RecordDevice *protocol.DeviceContext
}

// GetLToken 用 SToken 换取 LToken（便签的前置凭据）。
func (s *Service) GetLToken(ctx context.Context, sess session.Session) (string, *output.Error) {
	q := url.Values{}
	q.Set("stuid", sess.UID)
	q.Set("stoken", sess.Stoken)
	if sess.MID != "" {
		q.Set("mid", sess.MID)
	}
	h := protocol.CommonHeaders(protocol.ClientTypeAndroid, sess.Device())
	var data struct {
		LToken api.FlexString `json:"ltoken"`
	}
	if oerr := s.Passport.DoJSON(ctx, "GET", "/account/auth/api/getLTokenBySToken", q, nil, h, &data); oerr != nil {
		return "", oerr
	}
	if data.LToken.String() == "" {
		return "", output.Err(output.CodeRemoteRejected, "Failed to exchange for LToken: response is missing ltoken")
	}
	return data.LToken.String(), nil
}

// Note 的 per-item 状态。
const (
	StatusAvailable   = "available"   // 查询成功
	StatusUnsupported = "unsupported" // 该游戏便签端点未验证（不是远端故障）
	StatusFailed      = "failed"      // 已支持游戏的查询失败
)

// Note 是一次便签查询的归一化结果。wire 契约固定：
//   - status: available | unsupported | failed；
//   - supported 只表示该游戏端点是否已验证，不代表本次查询是否成功；
//   - summary 恒为数组（unsupported/failed 为 []）；raw 仅成功时为对象；
//   - reason 为英文说明，成功时为空串；error_code 仅 failed 时为稳定
//     本地错误码，其他状态为 null。
type Note struct {
	GameBiz   string          `json:"game_biz"`
	Region    string          `json:"region"`
	UID       string          `json:"uid"`
	Status    string          `json:"status"`
	Supported bool            `json:"supported"`
	Kind      string          `json:"kind"` // genshin / zzz / ""
	Summary   []string        `json:"summary"`
	Raw       json.RawMessage `json:"raw"`
	Reason    string          `json:"reason"`
	ErrorCode *string         `json:"error_code"`
}

// Verified 报告该 game_biz 的便签端点是否已实测验证。
func Verified(gameBiz string) bool {
	_, ok := toolPages[gameBiz]
	return ok
}

// Unsupported 构造未验证游戏的占位结果：不发请求、不猜测端点。
func Unsupported(r role.Role) *Note {
	n := &Note{GameBiz: r.GameBiz, Region: r.Region, UID: r.GameUID, Summary: []string{}}
	n.Status = StatusUnsupported
	n.Reason = fmt.Sprintf("Notes for %s are not supported yet (only Genshin hk4e_cn / Zenless nap_cn are verified)", r.GameBiz)
	return n
}

// ltokenCookie 构造便签请求的 LToken Cookie（兼容 v1/v2 字段名）。
func ltokenCookie(sess session.Session, ltoken string) string {
	return fmt.Sprintf("ltuid=%s; ltoken=%s; ltuid_v2=%s; ltoken_v2=%s; account_id=%s; account_id_v2=%s",
		sess.UID, ltoken, sess.UID, ltoken, sess.UID, sess.UID)
}

// recordHeaders 构造 client_type=5 + DS2(4X) 的请求头，并携带该游戏的
// 工具页标识（HAR 实抓，见 toolPages）。
func recordHeaders(sess session.Session, ltoken, query, gameBiz string, dev *protocol.DeviceContext) http.Header {
	tp := toolPages[gameBiz]
	deviceID, deviceFP := sess.DeviceID, sess.DeviceFP
	if dev != nil {
		deviceID, deviceFP = dev.DeviceID, dev.DeviceFP
	}
	h := http.Header{}
	h.Set("User-Agent", protocol.BrowserUA)
	h.Set("x-rpc-app_version", protocol.AppVersion)
	h.Set("x-rpc-sys_version", protocol.SysVersion)
	h.Set("x-rpc-channel", protocol.Channel)
	h.Set("x-rpc-client_type", "5")
	h.Set("x-rpc-device_id", deviceID)
	h.Set("x-rpc-device_fp", deviceFP)
	h.Set("x-rpc-device_name", protocol.DeviceName)
	h.Set("x-rpc-device_model", protocol.DeviceModel)
	h.Set("x-rpc-verify_key", protocol.VerifyKey)
	h.Set("x-rpc-language", "zh-cn")
	h.Set("x-rpc-lang", "zh-cn")
	h.Set("DS", protocol.NewDS2X4(query))
	h.Set("Cookie", ltokenCookie(sess, ltoken))
	if tp.Page != "" {
		h.Set("x-rpc-page", tp.Page)
	}
	if tp.ToolVersion != "" {
		h.Set("x-rpc-tool_verison", tp.ToolVersion)
	}
	if tp.Origin != "" {
		h.Set("Origin", tp.Origin)
		h.Set("Referer", tp.Origin)
	}
	if tp.Platform != "" {
		h.Set("x-rpc-platform", tp.Platform)
	}
	return h
}

// Fetch 查询单个角色的便签。
//   - 未验证游戏：返回 status=unsupported 的结果（不发请求，不猜测端点）；
//   - 已验证游戏查询失败：返回 status=failed 的结果与非 nil 的 output.Error
//     （调用方按原始认证/远端分类处理，不得改标为 unsupported）；
//   - 成功：status=available，Raw/Summary 就绪。
func (s *Service) Fetch(ctx context.Context, sess session.Session, ltoken string, r role.Role) (*Note, *output.Error) {
	if ltoken == "" {
		return nil, output.Err(output.CodeInputInvalid, "ltoken cannot be empty")
	}
	if r.GameBiz == "" || r.GameUID == "" || r.Region == "" {
		return nil, output.Err(output.CodeInputInvalid, "Notes require the role's game_biz/region/game_uid")
	}
	n := &Note{
		GameBiz: r.GameBiz, Region: r.Region, UID: r.GameUID,
		Summary: []string{},
	}

	var path string
	switch r.GameBiz {
	case "hk4e_cn":
		path = "/game_record/app/genshin/api/dailyNote"
		n.Kind = "genshin"
	case "nap_cn":
		path = "/event/game_record_zzz/api/zzz/note"
		n.Kind = "zzz"
	default:
		return Unsupported(r), nil
	}
	// supported 只描述端点验证状态；查询成败由 status 区分。
	n.Supported = true

	q := url.Values{}
	q.Set("server", r.Region)
	q.Set("role_id", r.GameUID)
	query := q.Encode()

	var raw json.RawMessage
	if oerr := s.Record.DoJSON(ctx, "GET", path, q, nil, recordHeaders(sess, ltoken, query, r.GameBiz, s.RecordDevice), &raw); oerr != nil {
		if oerr.Retcode == 1034 || oerr.Retcode == 5003 || oerr.Retcode == 10035 || oerr.Retcode == 10041 {
			oe := output.Err(oerr.Code,
				"Record-domain device risk control triggered (rc=%d): that domain trusts devices; place record_device.json (a real App device identity) in the config directory, or retry later",
				oerr.Retcode)
			oe.Retcode = oerr.Retcode
			oerr = oe
		}
		code := oerr.Code
		n.Status = StatusFailed
		n.Summary = []string{}
		n.Reason = oerr.Message
		n.ErrorCode = &code
		return n, oerr
	}
	n.Status = StatusAvailable
	n.Raw = raw

	switch n.Kind {
	case "genshin":
		lines, _ := genshinSummary(raw)
		n.Summary = lines
	case "zzz":
		lines, _ := zzzSummary(raw)
		n.Summary = lines
	}
	if n.Summary == nil {
		n.Summary = []string{}
	}
	return n, nil
}

func genshinSummary(raw json.RawMessage) ([]string, error) {
	var d struct {
		CurrentResin         int    `json:"current_resin"`
		MaxResin             int    `json:"max_resin"`
		ResinRecoveryTime    string `json:"resin_recovery_time"`
		FinishedTaskNum      int    `json:"finished_task_num"`
		TotalTaskNum         int    `json:"total_task_num"`
		RemainResinDiscount  int    `json:"remain_resin_discount_num"`
		CurrentExpeditionNum int    `json:"current_expedition_num"`
		MaxExpeditionNum     int    `json:"max_expedition_num"`
		CurrentHomeCoin      int    `json:"current_home_coin"`
		MaxHomeCoin          int    `json:"max_home_coin"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	lines := []string{
		fmt.Sprintf("Resin %d/%d (next recovery in %s s)", d.CurrentResin, d.MaxResin, d.ResinRecoveryTime),
		fmt.Sprintf("Daily commissions %d/%d", d.FinishedTaskNum, d.TotalTaskNum),
		fmt.Sprintf("Expeditions %d/%d", d.CurrentExpeditionNum, d.MaxExpeditionNum),
		fmt.Sprintf("Realm currency %d/%d", d.CurrentHomeCoin, d.MaxHomeCoin),
		fmt.Sprintf("Weekly boss resin discounts left %d", d.RemainResinDiscount),
	}
	return lines, nil
}

func zzzSummary(raw json.RawMessage) ([]string, error) {
	var d struct {
		Energy struct {
			Progress struct {
				Max     int `json:"max"`
				Current int `json:"current"`
			} `json:"progress"`
			Restore int64 `json:"restore"`
		} `json:"energy"`
		Vitality struct {
			Max     int `json:"max"`
			Current int `json:"current"`
		} `json:"vitality"`
		BountyCommission struct {
			Num   int `json:"num"`
			Total int `json:"total"`
		} `json:"bounty_commission"`
		WeeklyTask struct {
			CurPoint int `json:"cur_point"`
			MaxPoint int `json:"max_point"`
		} `json:"weekly_task"`
		MemberCard struct {
			IsOpen bool `json:"is_open"`
		} `json:"member_card"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	lines := []string{
		fmt.Sprintf("Energy %d/%d (restore %s)", d.Energy.Progress.Current, d.Energy.Progress.Max,
			(time.Duration(d.Energy.Restore) * time.Second).String()),
		fmt.Sprintf("Battery charge %d/%d", d.Vitality.Current, d.Vitality.Max),
		fmt.Sprintf("Bounty commissions %d/%d", d.BountyCommission.Num, d.BountyCommission.Total),
		fmt.Sprintf("Weekly activity %d/%d", d.WeeklyTask.CurPoint, d.WeeklyTask.MaxPoint),
		fmt.Sprintf("Membership card %s", boolText(d.MemberCard.IsOpen)),
	}
	return lines, nil
}

func boolText(b bool) string {
	if b {
		return "active"
	}
	return "not activated"
}
