// feed.go 实现分区帖子流的匿名入口。
//
// 上游实测（2026-09-24）：
//
//	GET /painter/api/getHotForumPostList?forum_id=&gids=&last_id=   # 热帖
//	GET /painter/api/getRecentForumPostList?forum_id=&gids=          # 最新帖
//	  → data = {list, last_id, is_last, is_origin, page, databox}    # 与 getForumPostList 同构
//
// 两条端点匿名可调（UA 即可，不携带 Cookie/DS；对照 getForumPostList 匿名 403）；
// size 被忽略、服务端固定约 20 条/页；热帖用 last_id 翻页，最新帖以 page 推进；
// 热帖对冷门分区可能 0 条，最新帖仍返回整页。
package forum

import (
	"context"
	"net/url"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/post"
	"mihoyo_cli/internal/protocol"
)

// FeedOrder 是匿名分区帖子流的排序/频道。
type FeedOrder string

const (
	// FeedHot 是热帖（默认）。
	FeedHot FeedOrder = "hot"
	// FeedRecent 是最新帖（按 post_id 倒序）。
	FeedRecent FeedOrder = "recent"
)

// ValidFeedOrder 报告取值是否受支持。
func ValidFeedOrder(s FeedOrder) bool {
	return s == FeedHot || s == FeedRecent
}

// FeedOptions 控制匿名分区帖子流。
type FeedOptions struct {
	ForumID string
	GIDs    string
	// Order 为空时按 FeedHot 处理（CLI 默认值）。
	Order FeedOrder
	// Cursor 是所选频道的服务端不透明游标：
	// 热帖映射为 last_id，最新帖映射为 page。
	Cursor string
}

// Feed 拉取分区热帖或最新帖（匿名，不携带会话 Cookie/DS）。
func (s *Service) Feed(ctx context.Context, o FeedOptions) (PostPage, *output.Error) {
	if o.ForumID == "" || o.GIDs == "" {
		return PostPage{}, output.Err(output.CodeInputInvalid, "forum-id and gids cannot be empty")
	}
	order := o.Order
	if order == "" {
		order = FeedHot
	}
	if !ValidFeedOrder(order) {
		return PostPage{}, output.Err(output.CodeInputInvalid,
			"--order supports only hot or recent (got %q)", string(order))
	}

	q := url.Values{}
	q.Set("forum_id", o.ForumID)
	q.Set("gids", o.GIDs)
	path := "/painter/api/getHotForumPostList"
	switch order {
	case FeedHot:
		if o.Cursor != "" {
			q.Set("last_id", o.Cursor)
		}
	case FeedRecent:
		if o.Cursor != "" {
			q.Set("page", o.Cursor)
		}
		path = "/painter/api/getRecentForumPostList"
	}

	// 匿名：与 games/list 相同的空设备上下文，绝不附带会话 Cookie 或 DS。
	h := protocol.CommonHeaders(protocol.ClientTypeAndroid, protocol.DeviceContext{})

	var data struct {
		api.ListMeta
		List []post.Entry   `json:"list"`
		Page api.FlexString `json:"page"`
	}
	if oerr := s.Client.DoJSON(ctx, "GET", path, q, nil, h, &data); oerr != nil {
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

	// 游标映射：热帖用 last_id，最新帖用 page；两者都按 is_last 判定是否还有下一页。
	var next string
	if order == FeedRecent {
		next = data.Page.String()
	} else {
		next = data.LastID.String()
	}
	if !isLastOrUnknown(&data.ListMeta) && next != "" {
		page.NextCursor = next
		page.HasMore = true
	}
	// has_more=false 时不保留游标。
	return page, nil
}

// isLastOrUnknown 报告服务端是否声明这是最后一页；is_last 缺失时视为未知
// （交由游标是否为空决定 has_more）。
func isLastOrUnknown(m *api.ListMeta) bool {
	return m.IsLast != nil && *m.IsLast
}
