// Package post 实现帖子只读能力：用户动态列表（post list）与帖子详情
// （post show）。证据等级 V（用户帖子列表参数组合待脱敏 fixture 固定）。
// 写命令（create/edit/delete）按门禁暂不注册，不在本包实现线上调用。
package post

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/session"
)

const pageSize = 20

// Summary 是帖子列表条目。
type Summary struct {
	PostID    string `json:"post_id"`
	Subject   string `json:"subject"`
	ViewType  int    `json:"view_type"`
	Author    string `json:"author,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
}

// Video 是帖子内视频条目（容错解析；列表缺 vod_list 时由详情补全）。
type Video struct {
	VideoID    string `json:"video_id,omitempty"`
	Cover      string `json:"cover,omitempty"`
	URL        string `json:"url,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// Detail 是帖子详情。
type Detail struct {
	Summary
	AuthorUID string   `json:"author_uid,omitempty"`
	Describe  string   `json:"describe,omitempty"`
	Images    []string `json:"images,omitempty"`
	Videos    []Video  `json:"videos,omitempty"`
}

// ListOptions 控制 post list。
type ListOptions struct {
	UID    string // 默认当前账号；仅用于查看其他用户公开帖子
	GIDs   int
	Cursor string
	Limit  int // 输出总数上限；单页大小由适配器控制
}

// Page 是一次列表读取的完整结果。
type Page struct {
	Items      []Summary
	NextCursor string
	HasMore    bool
}

type authorRaw struct {
	UID      api.FlexString `json:"uid"`
	Nickname string         `json:"nickname"`
}

type imgRaw struct {
	URL api.FlexString `json:"url"`
}

type contentRaw struct {
	Describe string   `json:"describe"`
	Imgs     []imgRaw `json:"imgs"`
}

type vodRaw struct {
	VideoID    api.FlexString `json:"video_id"`
	Cover      api.FlexString `json:"cover"`
	URL        api.FlexString `json:"res_video_url"`
	URLAlt     api.FlexString `json:"url"`
	DurationMS int64          `json:"duration"`
}

type postRaw struct {
	PostID    api.FlexString `json:"post_id"`
	Subject   string         `json:"subject"`
	ViewType  int            `json:"view_type"`
	CreatedAt int64          `json:"created_at"`
	Author    *authorRaw     `json:"author"`
	Content   *contentRaw    `json:"content"`
}

// Entry 是列表接口条目的容错解析：部分列表把帖子放在 post 字段，
// 部分直接内联帖子字段，两种形态都能提取。
type Entry struct {
	Post *postRaw `json:"post"`
	postRaw
}

// Summary 从条目提取帖子摘要。
func (e *Entry) Summary() (Summary, *output.Error) {
	raw := e.Post
	if raw == nil {
		raw = &e.postRaw
	}
	return raw.summary()
}

type vodEntry struct {
	Vod *vodRaw `json:"vod"`
	vodRaw
}

// Service 是帖子只读服务。
type Service struct {
	Client *api.Client
}

// New 构造服务；Client 指向 bbs-api.miyoushe.com（测试可注入）。
func New(c *api.Client) *Service { return &Service{Client: c} }

func headers(sess session.Session) http.Header {
	h := protocol.CommonHeaders(protocol.ClientTypeAndroid, sess.Device())
	protocol.WithDS(h, protocol.NewDSBBS())
	protocol.WithCookie(h, sess.Cookie())
	return h
}

// List 拉取用户动态帖子列表。跨页中途失败时返回 ok:false，
// error 携带 partial_data 与最后一个已完整消费页的 resume_cursor。
func (s *Service) List(ctx context.Context, sess session.Session, opts ListOptions) (Page, *output.Error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = pageSize
	}
	uid := opts.UID
	if uid == "" {
		uid = sess.UID
	}

	page := Page{Items: []Summary{}}
	cursor := opts.Cursor
	for {
		q := url.Values{}
		q.Set("uid", uid)
		if opts.GIDs > 0 {
			q.Set("gids", strconv.Itoa(opts.GIDs))
		}
		q.Set("size", strconv.Itoa(pageSize))
		if cursor != "" {
			q.Set("last_id", cursor)
		}

		var data struct {
			api.ListMeta
			List []Entry `json:"list"`
		}
		if oerr := s.Client.DoJSON(ctx, "GET", "/painter/api/user_instant/list", q, nil, headers(sess), &data); oerr != nil {
			if len(page.Items) > 0 {
				oe := output.Err(output.CodeRemoteRejected,
					"帖子列表第 %d 页读取失败: %s", len(page.Items)/pageSize+1, oerr.Message)
				oe.PartialData = ListDataJSON(page)
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

// Get 拉取帖子详情。
func (s *Service) Get(ctx context.Context, sess session.Session, postID string) (Detail, *output.Error) {
	if postID == "" {
		return Detail{}, output.Err(output.CodeInputInvalid, "post-id 不能为空")
	}
	q := url.Values{}
	q.Set("post_id", postID)

	var data struct {
		Post    *postRaw   `json:"post"`
		VodList []vodEntry `json:"vod_list"`
	}
	if oerr := s.Client.DoJSON(ctx, "GET", "/post/api/getPostFull", q, nil, headers(sess), &data); oerr != nil {
		return Detail{}, oerr
	}
	if data.Post == nil {
		return Detail{}, output.Err(output.CodeRemoteRejected, "帖子详情响应缺少 post 字段")
	}
	sum, oerr := data.Post.summary()
	if oerr != nil {
		return Detail{}, oerr
	}
	d := Detail{Summary: sum}
	if data.Post.Author != nil {
		d.AuthorUID = data.Post.Author.UID.String()
	}
	if data.Post.Content != nil {
		d.Describe = data.Post.Content.Describe
		for _, img := range data.Post.Content.Imgs {
			if img.URL != "" {
				d.Images = append(d.Images, img.URL.String())
			}
		}
	}
	for _, entry := range data.VodList {
		v := entry.Vod
		if v == nil {
			v = &entry.vodRaw
		}
		vv := Video{
			VideoID:    v.VideoID.String(),
			Cover:      v.Cover.String(),
			DurationMS: v.DurationMS,
		}
		if v.URL != "" {
			vv.URL = v.URL.String()
		} else {
			vv.URL = v.URLAlt.String()
		}
		d.Videos = append(d.Videos, vv)
	}
	return d, nil
}

func (raw *postRaw) summary() (Summary, *output.Error) {
	id := raw.PostID.String()
	if id == "" {
		return Summary{}, output.Err(output.CodeRemoteRejected, "帖子响应缺少 post_id")
	}
	s := Summary{
		PostID:    id,
		Subject:   raw.Subject,
		ViewType:  raw.ViewType,
		CreatedAt: raw.CreatedAt,
	}
	if raw.Author != nil {
		s.Author = raw.Author.Nickname
	}
	return s, nil
}

// ListDataJSON 供部分失败时把已消费结果放入 error.partial_data。
func ListDataJSON(p Page) any {
	items := p.Items
	if items == nil {
		items = []Summary{}
	}
	return map[string]any{
		"items":    items,
		"has_more": p.HasMore,
	}
}
