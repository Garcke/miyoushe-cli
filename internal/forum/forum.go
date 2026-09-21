// Package forum 实现米游社社区/分区浏览：分区目录、游戏讨论区元数据与
// 分区帖子流。
//
// 契约（2026-09-19 实测，均匿名可调）：
//   - GET /apihub/api/getAllGamesForums：9 个游戏 × 各自分区目录
//     （不同游戏的分区集合完全不同：原神 6 区、因缘精灵仅 2 区、大别野 8 区…）；
//   - GET /forum/api/getDiscussionByGame?gids=：单游戏讨论区 + 分区元数据（含描述）；
//   - GET /post/api/getForumPostList?forum_id=&gids=：分区帖子流，需 bbs DS，
//     last_id/is_last 翻页，服务端固定每页 20 条（size 参数被忽略），sort_type 可换排序。
package forum

import (
	"context"
	"net/url"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/post"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/session"
)

// Service 是分区浏览服务。GamesForums/Discussion 匿名可调（设备随机生成）；
// ForumPosts 走业务头（session + DS）。
type Service struct {
	Client *api.Client
	sess   session.Session
}

// New 构造服务；Client 指向 bbs-api.miyoushe.com（测试可注入）。
func New(c *api.Client, sess session.Session) *Service {
	return &Service{Client: c, sess: sess}
}

// Forum 是单个分区。
type Forum struct {
	ID         api.FlexString `json:"id"`
	GameID     api.FlexString `json:"game_id"`
	Name       string         `json:"name"`
	CreateType api.FlexString `json:"create_type"` // 发帖权限类型
	PostOrder  string         `json:"post_order"`
}

// Game 是一个游戏的分区目录。
type Game struct {
	GameID api.FlexString `json:"game_id"`
	Forums []Forum        `json:"forums"`
}

type gameRaw struct {
	GameID api.FlexString `json:"game_id"`
	Forums []Forum        `json:"forums"`
}

// GamesForums 拉取全游戏分区目录（GET /apihub/api/getAllGamesForums，匿名）。
func (s *Service) GamesForums(ctx context.Context) ([]Game, *output.Error) {
	h := protocol.CommonHeaders(protocol.ClientTypeAndroid, protocol.DeviceContext{})
	var data struct {
		List []gameRaw `json:"list"`
	}
	if oerr := s.Client.DoJSON(ctx, "GET", "/apihub/api/getAllGamesForums", nil, nil, h, &data); oerr != nil {
		return nil, oerr
	}
	games := make([]Game, 0, len(data.List))
	for _, g := range data.List {
		games = append(games, Game{GameID: g.GameID, Forums: g.Forums})
	}
	return games, nil
}

// DiscussionForum 是讨论区元数据中的分区（含名称与描述）。
type DiscussionForum struct {
	ID   api.FlexString `json:"id"`
	Name string         `json:"name"`
	Des  string         `json:"des"`
}

// Discussion 是单游戏的讨论区元数据。
type Discussion struct {
	DiscussionID api.FlexString    `json:"discussion_id"`
	Subject      string            `json:"subject"`
	Forums       []DiscussionForum `json:"forums"`
}

// Discussion 拉取单游戏讨论区与分区元数据
// （GET /forum/api/getDiscussionByGame?gids=，匿名）。
// V3 §5.1：ID 缺失视为响应结构错误；名称/描述缺失由调用方显示“未提供”，
// 不凭空推断发帖权限。
func (s *Service) Discussion(ctx context.Context, gids string) (*Discussion, *output.Error) {
	if gids == "" {
		return nil, output.Err(output.CodeInputInvalid, "gids 不能为空")
	}
	q := url.Values{"gids": {gids}}
	var data struct {
		Discussion Discussion `json:"discussion"`
	}
	if oerr := s.Client.DoJSON(ctx, "GET", "/forum/api/getDiscussionByGame", q, nil,
		protocol.CommonHeaders(protocol.ClientTypeAndroid, protocol.DeviceContext{}), &data); oerr != nil {
		return nil, oerr
	}
	if data.Discussion.DiscussionID.String() == "" {
		return nil, output.Err(output.CodeRemoteRejected, "讨论区响应缺少 discussion_id")
	}
	for i := range data.Discussion.Forums {
		if data.Discussion.Forums[i].ID.String() == "" {
			return nil, output.Err(output.CodeRemoteRejected, "讨论区响应缺少分区 id")
		}
	}
	return &data.Discussion, nil
}

// ForumPostsOptions 控制分区帖子流。
type ForumPostsOptions struct {
	ForumID  string
	GIDs     string
	SortType string // 空 = 服务端默认；实测 "1" 为另一种排序（语义未定案，透传）
	LastID   string
}

// PostPage 是分区帖子流的一页。条目与收藏夹同构（post.Entry）；
// 服务端固定每页 20 条（size 参数被忽略，实测）。
type PostPage struct {
	Items      []post.Summary
	NextCursor string
	HasMore    bool
}

// ForumPosts 拉取分区帖子流（GET /post/api/getForumPostList，需 bbs DS）。
func (s *Service) ForumPosts(ctx context.Context, o ForumPostsOptions) (PostPage, *output.Error) {
	if o.ForumID == "" || o.GIDs == "" {
		return PostPage{}, output.Err(output.CodeInputInvalid, "forum-id 与 gids 不能为空")
	}
	q := url.Values{}
	q.Set("forum_id", o.ForumID)
	q.Set("gids", o.GIDs)
	if o.SortType != "" {
		q.Set("sort_type", o.SortType)
	}
	if o.LastID != "" {
		q.Set("last_id", o.LastID)
	}

	h := protocol.CommonHeaders(protocol.ClientTypeAndroid, s.sess.Device())
	protocol.WithDS(h, protocol.NewDSBBS())
	protocol.WithCookie(h, s.sess.Cookie())

	var data struct {
		api.ListMeta
		List []post.Entry `json:"list"`
	}
	if oerr := s.Client.DoJSON(ctx, "GET", "/post/api/getForumPostList", q, nil, h, &data); oerr != nil {
		return PostPage{}, oerr
	}
	page := PostPage{Items: []post.Summary{}}
	for i := range data.List {
		sum, oerr := data.List[i].Summary()
		if oerr != nil {
			return PostPage{}, oerr
		}
		page.Items = append(page.Items, sum)
	}
	page.NextCursor = data.Cursor()
	page.HasMore = data.HasMore()
	return page, nil
}
