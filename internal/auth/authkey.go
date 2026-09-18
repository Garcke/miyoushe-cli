package auth

// AuthKeyService 在 takumi 账号域签发 authkey：以 SToken 为根凭据、按
// auth_appid 声明用途域的派生签名凭据（实测 2026-09-15：auth_appid=csc →
// rc=0，返回 {sign_type, authkey_ver:1, authkey}）。发布/删帖链路不依赖它。
//
// 证据：docs/reference/cnb-mihoyo-api/snapshot/docs/api/
// ma-cn-passport扫码登录_2026-09-15实测.md §2.3。

import (
	"context"
	"encoding/json"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/session"
)

// AuthKeyService 绑定 takumi 账号域客户端（api-takumi.miyoushe.com）。
type AuthKeyService struct {
	Client *api.Client
}

// AuthKeyOptions 是签发入参。AuthAppID 决定签发域（实测 "csc" 可用；
// game_record 等其它域的取值未实测，不做枚举白名单）。
type AuthKeyOptions struct {
	AuthAppID string
	GameBiz   string
	GameUID   string
	Region    string
}

// AuthKey 是签发结果。
type AuthKey struct {
	AuthKey    string
	AuthKeyVer string
	SignType   string
}

// Gen 生成 authkey。请求体字节与 bbs DS 绑定（本接口 DS 形态未单独归因，
// 沿用与角色/收藏一致 bbs DS1；Cookie 为 stoken 三件套）。
func (s *AuthKeyService) Gen(ctx context.Context, sess session.Session, o AuthKeyOptions) (AuthKey, *output.Error) {
	if s.Client == nil {
		return AuthKey{}, output.Err(output.CodeInternal, "AuthKeyService.Client 未配置")
	}
	if o.AuthAppID == "" || o.GameBiz == "" || o.GameUID == "" || o.Region == "" {
		return AuthKey{}, output.Err(output.CodeInputInvalid,
			"auth_appid/game_biz/game_uid/region 均不能为空")
	}
	b, err := json.Marshal(struct {
		AuthAppID string `json:"auth_appid"`
		GameBiz   string `json:"game_biz"`
		GameUID   string `json:"game_uid"`
		Region    string `json:"region"`
	}{AuthAppID: o.AuthAppID, GameBiz: o.GameBiz, GameUID: o.GameUID, Region: o.Region})
	if err != nil {
		return AuthKey{}, output.Err(output.CodeInternal, "构造 genAuthKey 请求失败: %v", err)
	}

	h := protocol.CommonHeaders(protocol.ClientTypeAndroid, sess.Device())
	protocol.WithDS(h, protocol.NewDSBBS())
	protocol.WithCookie(h, sess.Cookie())

	var data struct {
		AuthKey    string         `json:"authkey"`
		AuthKeyVer api.FlexString `json:"authkey_ver"`
		SignType   api.FlexString `json:"sign_type"`
	}
	if oerr := s.Client.DoJSON(ctx, "POST", "/account/auth/api/genAuthKey", nil, b, h, &data); oerr != nil {
		return AuthKey{}, oerr
	}
	if data.AuthKey == "" {
		return AuthKey{}, output.Err(output.CodeRemoteRejected, "genAuthKey 响应缺少 authkey")
	}
	return AuthKey{
		AuthKey:    data.AuthKey,
		AuthKeyVer: data.AuthKeyVer.String(),
		SignType:   data.SignType.String(),
	}, nil
}
