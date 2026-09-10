// Package role 实现 SToken 绑定角色查询（getUserGameRolesByStoken）。
// 证据等级 V：client_type=2 + BBS DS + SToken Cookie。
package role

import (
	"context"
	"net/url"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/session"
)

// Role 是绑定角色。
type Role struct {
	GameBiz    string `json:"game_biz"`
	GameUID    string `json:"game_uid"`
	Region     string `json:"region"`
	Nickname   string `json:"nickname"`
	Level      int    `json:"level"`
	IsChosen   bool   `json:"is_chosen"`
	RegionName string `json:"region_name"`
	IsOfficial bool   `json:"is_official"`
}

type roleRaw struct {
	GameBiz    string         `json:"game_biz"`
	GameUID    api.FlexString `json:"game_uid"`
	RoleID     api.FlexString `json:"role_id"`
	Region     string         `json:"region"`
	Nickname   string         `json:"nickname"`
	Level      int            `json:"level"`
	IsChosen   bool           `json:"is_chosen"`
	RegionName string         `json:"region_name"`
	IsOfficial bool           `json:"is_official"`
}

// Service 是角色查询服务。
type Service struct {
	Client *api.Client
}

// New 构造服务；Client 指向 api-takumi.miyoushe.com（测试可注入）。
func New(c *api.Client) *Service { return &Service{Client: c} }

// List 拉取当前账号绑定的角色；gameBiz 为空时不传 game_biz。
func (s *Service) List(ctx context.Context, sess session.Session, gameBiz string) ([]Role, *output.Error) {
	q := url.Values{}
	if gameBiz != "" {
		q.Set("game_biz", gameBiz)
	}
	h := protocol.CommonHeaders(protocol.ClientTypeAndroid, sess.Device())
	protocol.WithDS(h, protocol.NewDSBBS())
	protocol.WithCookie(h, sess.Cookie())

	var data struct {
		List []roleRaw `json:"list"`
	}
	if oerr := s.Client.DoJSON(ctx, "GET", "/binding/api/getUserGameRolesByStoken", q, nil, h, &data); oerr != nil {
		return nil, oerr
	}
	roles := make([]Role, 0, len(data.List))
	for _, raw := range data.List {
		r := Role{
			GameBiz:    raw.GameBiz,
			Region:     raw.Region,
			Nickname:   raw.Nickname,
			Level:      raw.Level,
			IsChosen:   raw.IsChosen,
			RegionName: raw.RegionName,
			IsOfficial: raw.IsOfficial,
		}
		// 快照记录新接口返回 game_uid，旧实现使用 role_id；两者取先到。
		if raw.GameUID != "" {
			r.GameUID = raw.GameUID.String()
		} else {
			r.GameUID = raw.RoleID.String()
		}
		if r.GameUID == "" {
			return nil, output.Err(output.CodeRemoteRejected, "角色响应缺少 game_uid")
		}
		roles = append(roles, r)
	}
	return roles, nil
}
