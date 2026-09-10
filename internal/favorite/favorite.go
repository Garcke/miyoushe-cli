// Package favorite 实现收藏列表（userFavouritePostList）与角色选择器解析。
// 证据等级 V：需要角色 game_uid/region，游标分页（offset）。
// favorite add/remove 依赖 collectPost 双向脱敏 body，按门禁暂不注册。
package favorite

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/post"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/role"
	"mihoyo_cli/internal/session"
)

const pageSize = 20

// ListOptions 控制 favorite list。
type ListOptions struct {
	Cursor string
	Limit  int
}

// Page 是一次收藏列表读取的完整结果。
type Page struct {
	Items      []post.Summary
	NextCursor string
	HasMore    bool
}

// Service 是收藏列表服务。
type Service struct {
	Client *api.Client
}

// New 构造服务；Client 指向 bbs-api.miyoushe.com（测试可注入）。
func New(c *api.Client) *Service { return &Service{Client: c} }

// List 拉取指定角色的收藏帖子列表。跨页中途失败时携带 resume_cursor。
func (s *Service) List(ctx context.Context, sess session.Session, r role.Role, opts ListOptions) (Page, *output.Error) {
	if r.GameUID == "" || r.Region == "" {
		return Page{}, output.Err(output.CodeInputInvalid, "收藏列表需要角色的 game_uid 与 region")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = pageSize
	}

	h := protocol.CommonHeaders(protocol.ClientTypeAndroid, sess.Device())
	protocol.WithDS(h, protocol.NewDSBBS())
	protocol.WithCookie(h, sess.Cookie())

	page := Page{Items: []post.Summary{}}
	cursor := opts.Cursor
	for {
		q := url.Values{}
		q.Set("aid", sess.UID)
		q.Set("offset", cursor) // 首页传空串，与快照一致
		q.Set("size", strconv.Itoa(pageSize))
		q.Set("game_uid", r.GameUID)
		q.Set("game_region", r.Region)

		var data struct {
			api.ListMeta
			List []post.Entry `json:"list"`
		}
		if oerr := s.Client.DoJSON(ctx, "GET", "/painter/api/userFavouritePostList", q, nil, h, &data); oerr != nil {
			if len(page.Items) > 0 {
				oe := output.Err(output.CodeRemoteRejected,
					"收藏列表第 %d 页读取失败: %s", len(page.Items)/pageSize+1, oerr.Message)
				oe.ResumeCursor = cursor
				return Page{}, oe
			}
			return Page{}, oerr
		}
		for i := range data.List {
			sum, oerr := data.List[i].Summary()
			if oerr != nil {
				return Page{}, oerr
			}
			page.Items = append(page.Items, sum)
			if len(page.Items) >= limit {
				break
			}
		}
		cursor = data.Cursor()
		page.NextCursor = cursor
		page.HasMore = data.HasMore()
		if !page.HasMore || len(page.Items) >= limit {
			break
		}
	}
	return page, nil
}

// ResolveSelector 解析 --role 选择器：
//   - 空选择器：仅一个角色时自动使用；多个角色列出候选并要求明确选择；
//   - "game_biz:game_uid:region"：三段精确匹配；
//   - "game_uid"：唯一匹配时使用；不唯一列出候选，不静默挑选。
func ResolveSelector(roles []role.Role, sel string) (role.Role, *output.Error) {
	switch {
	case sel == "":
		switch len(roles) {
		case 0:
			return role.Role{}, output.Err(output.CodeInputInvalid, "当前账号没有绑定游戏角色，无法使用收藏列表")
		case 1:
			return roles[0], nil
		default:
			return role.Role{}, output.Err(output.CodeInputInvalid,
				"存在多个绑定角色，请用 --role 明确选择（game_biz:game_uid:region 或可唯一匹配的 game_uid）:\n%s",
				candidates(roles))
		}
	case strings.Contains(sel, ":"):
		parts := strings.Split(sel, ":")
		if len(parts) != 3 {
			return role.Role{}, output.Err(output.CodeInputInvalid,
				"--role 组合选择器格式应为 game_biz:game_uid:region")
		}
		for _, r := range roles {
			if r.GameBiz == parts[0] && r.GameUID == parts[1] && r.Region == parts[2] {
				return r, nil
			}
		}
		return role.Role{}, output.Err(output.CodeInputInvalid,
			"没有匹配 --role %q 的角色；候选:\n%s", sel, candidates(roles))
	default:
		var matches []role.Role
		for _, r := range roles {
			if r.GameUID == sel {
				matches = append(matches, r)
			}
		}
		switch len(matches) {
		case 0:
			return role.Role{}, output.Err(output.CodeInputInvalid,
				"没有 game_uid 为 %q 的角色；候选:\n%s", sel, candidates(roles))
		case 1:
			return matches[0], nil
		default:
			return role.Role{}, output.Err(output.CodeInputInvalid,
				"game_uid %q 命中多个角色，请用完整选择器 game_biz:game_uid:region:\n%s", sel, candidates(matches))
		}
	}
}

func candidates(roles []role.Role) string {
	lines := make([]string, 0, len(roles))
	for _, r := range roles {
		lines = append(lines, fmt.Sprintf("  %s:%s:%s  %s  %s",
			r.GameBiz, r.GameUID, r.Region, r.Nickname, r.RegionName))
	}
	return strings.Join(lines, "\n")
}

// FullGetter 是 --full 补全帖子详情所需的最小接口（post.Service 实现）。
type FullGetter interface {
	Get(ctx context.Context, sess session.Session, postID string) (post.Detail, *output.Error)
}

// FillFull 按原始顺序逐条补调 getPostFull，并发上限 conc。
// 任一条失败即整条命令失败，不把部分结果伪装成成功。
func FillFull(ctx context.Context, sess session.Session, items []post.Summary, getter FullGetter, conc int) ([]post.Detail, *output.Error) {
	if conc <= 0 {
		conc = 2
	}
	out := make([]post.Detail, len(items))
	errs := make([]*output.Error, len(items))
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for i := range items {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			d, oerr := getter.Get(ctx, sess, items[i].PostID)
			if oerr != nil {
				errs[i] = oerr
				return
			}
			out[i] = d
		}(i)
	}
	wg.Wait()
	for i, oerr := range errs {
		if oerr != nil {
			return nil, output.Err(output.CodeRemoteRejected,
				"补全帖子详情失败（post_id %s）: %s", items[i].PostID, oerr.Message)
		}
	}
	return out, nil
}
