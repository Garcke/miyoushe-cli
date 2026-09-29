// Package forum 实现米游社社区/分区浏览：分区目录、游戏讨论区元数据与
// 分区帖子流。
//
// 契约（2026-09-19 实测，均匿名可调）：
//   - GET /apihub/api/getAllGamesForums：9 个游戏 × 各自分区目录
//     （不同游戏的分区集合完全不同：原神 6 区、因缘精灵仅 2 区、大别野 8 区…）；
//   - GET /forum/api/getDiscussionByGame?gids=：单游戏讨论区 + 分区元数据（含描述）；
//   - GET /post/api/getForumPostList?forum_id=&gids=：分区帖子流，需 bbs DS，
//     last_id/is_last 翻页，服务端固定每页 20 条（size 参数被忽略）；
//     sort_type（1=最新回复、2/3/4/缺省=最新发布）仅作协议观察，不暴露到 CLI。
package forum

import (
	"context"
	"net/url"
	"strconv"

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
// ID 缺失视为响应结构错误；名称/描述缺失由调用方显示“未提供”，
// 不凭空推断发帖权限。
func (s *Service) Discussion(ctx context.Context, gids string) (*Discussion, *output.Error) {
	if gids == "" {
		return nil, output.Err(output.CodeInputInvalid, "gids cannot be empty")
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
		return nil, output.Err(output.CodeRemoteRejected, "Discussion response is missing discussion_id")
	}
	for i := range data.Discussion.Forums {
		if data.Discussion.Forums[i].ID.String() == "" {
			return nil, output.Err(output.CodeRemoteRejected, "Discussion response is missing a forum id")
		}
	}
	return &data.Discussion, nil
}

// ForumPostsOptions 控制分区帖子流。不提供排序参数：sort_type 的语义
// （1=最新回复、2/3/4/缺省=最新发布，2026-09-24 定案）保留为协议观察，
// 不暴露到 CLI。
type ForumPostsOptions struct {
	ForumID string
	GIDs    string
	LastID  string
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
		return PostPage{}, output.Err(output.CodeInputInvalid, "forum-id and gids cannot be empty")
	}
	q := url.Values{}
	q.Set("forum_id", o.ForumID)
	q.Set("gids", o.GIDs)
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

// ImageTypes 返回图片分区的榜单分类（GET /forum/api/getImagePostListType，需 bbs DS）。
// 实测（2026-09-21）：原神同人图(29)/绝区零同人图(59) 返回 [1,2,3]；
// 非图片分区（如因缘精灵 948）返回空列表。
func (s *Service) ImageTypes(ctx context.Context, gids, forumID string) ([]int, *output.Error) {
	if gids == "" || forumID == "" {
		return nil, output.Err(output.CodeInputInvalid, "gids and forum-id cannot be empty")
	}
	q := url.Values{"gids": {gids}, "forum_id": {forumID}}
	var data struct {
		List []int `json:"list"`
	}
	if oerr := s.Client.DoJSON(ctx, "GET", "/forum/api/getImagePostListType", q, nil,
		protocol.CommonHeaders(protocol.ClientTypeAndroid, protocol.DeviceContext{}), &data); oerr != nil {
		return nil, oerr
	}
	return data.List, nil
}

// ImagePostsOptions 控制图片榜单拉取。
type ImagePostsOptions struct {
	ForumID string
	GIDs    string
	Type    string // 榜单类型，取自 ImageTypes（实测 1/2/3）；空 = 服务端默认（实测空返回 0 条）
	LastID  string
	Size    int
}

// ImagePage 是一次图片榜单拉取的结果。Title 为服务端给出的榜名（实测"同人榜"）。
type ImagePage struct {
	Title      string
	Items      []post.Summary
	NextCursor string
	HasMore    bool
}

// ImagePosts 拉取图片分区榜单（GET /post/api/getImagePostList，需 bbs DS）。
// 实测：必须携带 type（缺省返回 0 条）；last_id/is_last 翻页；条目与收藏夹同构。
func (s *Service) ImagePosts(ctx context.Context, o ImagePostsOptions) (ImagePage, *output.Error) {
	if o.ForumID == "" || o.GIDs == "" {
		return ImagePage{}, output.Err(output.CodeInputInvalid, "forum-id and gids cannot be empty")
	}
	size := o.Size
	if size <= 0 {
		size = 20
	}
	q := url.Values{}
	q.Set("forum_id", o.ForumID)
	q.Set("gids", o.GIDs)
	q.Set("size", strconv.Itoa(size))
	if o.Type != "" {
		q.Set("type", o.Type)
	}
	if o.LastID != "" {
		q.Set("last_id", o.LastID)
	}

	h := protocol.CommonHeaders(protocol.ClientTypeAndroid, s.sess.Device())
	protocol.WithDS(h, protocol.NewDSBBS())
	protocol.WithCookie(h, s.sess.Cookie())

	var data struct {
		Title string `json:"title"`
		api.ListMeta
		List []post.Entry `json:"list"`
	}
	if oerr := s.Client.DoJSON(ctx, "GET", "/post/api/getImagePostList", q, nil, h, &data); oerr != nil {
		return ImagePage{}, oerr
	}
	page := ImagePage{Title: data.Title, Items: []post.Summary{}}
	for i := range data.List {
		sum, oerr := data.List[i].Summary()
		if oerr != nil {
			return ImagePage{}, oerr
		}
		page.Items = append(page.Items, sum)
	}
	page.NextCursor = data.Cursor()
	page.HasMore = data.HasMore()
	return page, nil
}
