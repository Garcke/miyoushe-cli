// Package post 实现帖子只读能力：用户动态列表（post list）与帖子详情
// （post show）。证据等级 V（用户帖子列表参数组合待脱敏 fixture 固定）。
// 写命令（create/edit/delete）按门禁暂不注册，不在本包实现线上调用。
package post

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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

// UnmarshalJSON 容忍 content 的两种线上形态（2026-09-15 实测）：
// 读接口（getPostFull / user_instant / 收藏列表）返回字符串（纯文本或 HTML），
// 发布/草稿域为 {describe, imgs} 对象。字符串形态整体记入 Describe。
func (c *contentRaw) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` {
		return nil
	}
	if len(s) >= 2 && s[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		c.Describe = v
		return nil
	}
	type plain contentRaw
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*c = contentRaw(p)
	return nil
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

// postWrapper 容忍 getPostFull 的双层形态（2026-09-15 实测）：
// data.post 是包装层，内层 data.post.post 才是帖子本体；
// 旧样本若为单层，回退到包装层自身字段。
type postWrapper struct {
	Post *postRaw `json:"post"`
	postRaw
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
		// 每次请求 size=min(pageSize, 剩余配额)：最后一页整页消费后再续游标，
		// 避免 --limit 小于页大小时跳过未展示的条目。
		size := pageSize
		if remain := limit - len(page.Items); remain < size {
			size = remain
		}
		q := url.Values{}
		q.Set("uid", uid)
		if opts.GIDs > 0 {
			q.Set("gids", strconv.Itoa(opts.GIDs))
		}
		q.Set("size", strconv.Itoa(size))
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
		Post    *postWrapper `json:"post"`
		VodList []vodEntry   `json:"vod_list"`
	}
	if oerr := s.Client.DoJSON(ctx, "GET", "/post/api/getPostFull", q, nil, headers(sess), &data); oerr != nil {
		return Detail{}, oerr
	}
	if data.Post == nil {
		return Detail{}, output.Err(output.CodeRemoteRejected, "帖子详情响应缺少 post 字段")
	}
	raw := data.Post.Post
	if raw == nil {
		raw = &data.Post.postRaw
	}
	sum, oerr := raw.summary()
	if oerr != nil {
		return Detail{}, oerr
	}
	d := Detail{Summary: sum}
	if raw.Author != nil {
		d.AuthorUID = raw.Author.UID.String()
	}
	if raw.Content != nil {
		d.Describe = raw.Content.Describe
		for _, img := range raw.Content.Imgs {
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

// PublishOptions 是发布帖子的内容输入。StructuredContent 传 Quill delta
// 序列化后的 JSON 字符串；DraftID 为可选的先存草稿 id（实测链路：save→publish）。
type PublishOptions struct {
	Subject           string
	ContentHTML       string
	StructuredContent string
	ForumID           string // f_forum_id / forum_id 同值发送
	GIDs              int
	ViewType          int // 1=视频混排 2=纯图文 5=长文
	Cover             string
	DraftID           string
}

// PublishResult 是发布结果。Allowed=false 表示被版区等级门槛拦截
// （实测：rc=0 但 post_id=0、post_review_id=0，GateMessage 携带人话提示）；
// ReviewID 非 0 表示进入审核流（审核中帖子不可见，撤回走 review/undo，未实现）。
type PublishResult struct {
	PostID      string
	ReviewID    string
	Allowed     bool
	GateMessage string
}

type releaseCheckRaw struct {
	CanRelease bool   `json:"can_release"`
	Msg        string `json:"msg"`
}

// parseReleaseEnvelope 解析 releasePost/v2 响应（文本帖与视频帖共用）。
// 门槛拦截形态（实测）：post_id=0 + release_check_result.can_release=false，
// rc 仍为 0；无 post_id 也无门槛信息时报“结果未知”。
func parseReleaseEnvelope(raw json.RawMessage) (PublishResult, *output.Error) {
	var data struct {
		PostID             api.FlexString   `json:"post_id"`
		PostReviewID       api.FlexString   `json:"post_review_id"`
		ReleaseCheckResult *releaseCheckRaw `json:"release_check_result"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return PublishResult{}, output.Err(output.CodeRemoteRejected, "发布响应 data 结构与预期不符")
	}
	res := PublishResult{Allowed: true}
	if data.PostID.String() != "" && data.PostID.String() != "0" {
		res.PostID = data.PostID.String()
	}
	res.ReviewID = data.PostReviewID.String()
	if res.ReviewID == "0" {
		res.ReviewID = ""
	}
	if res.PostID == "" && data.ReleaseCheckResult != nil && !data.ReleaseCheckResult.CanRelease {
		res.Allowed = false
		res.GateMessage = data.ReleaseCheckResult.Msg
	}
	if res.PostID == "" && res.Allowed && res.ReviewID == "" {
		return PublishResult{}, output.Err(output.CodeRemoteRejected,
			"发布响应既无 post_id 也无 review_id，结果未知")
	}
	return res, nil
}

// VideoPublishOptions 是视频帖发布输入（2026-09-18 App 实抓契约）。
// VideoID 来自秒传（isExist 命中）或 App 预上传；MetaContent 由本方法
// 程序化构造（describe 文本块 + vods 引用），服务端会把它合并进
// structured_content 并自动补 vod 封面。
type VideoPublishOptions struct {
	Subject       string
	Text          string
	VideoID       string
	CoverURL      string
	ForumID       string // f_forum_id / forum_id
	ForumCateID   string // 版区分类 id（实抓因缘精灵 951 区为 "15"）
	GIDs          string // 实测为字符串形态（"10"）
	TopicIDs      []string
	BlockReplyImg int // 0/1，int 契约（boolean → -502）
}

// PublishVideo 发布视频帖（releasePost/v2，view_type=5 + meta_content.vods）。
// 实测：视频帖无需先存草稿；gids 为字符串；服务端按风控决定是否进审核
// （进审核时 post_id=0、返回 post_review_id，撤回走 UndoReview）。
func (s *Service) PublishVideo(ctx context.Context, sess session.Session, o VideoPublishOptions) (PublishResult, *output.Error) {
	if o.Subject == "" {
		return PublishResult{}, output.Err(output.CodeInputInvalid, "帖子标题不能为空")
	}
	if o.VideoID == "" {
		return PublishResult{}, output.Err(output.CodeInputInvalid, "video-id 不能为空")
	}
	if o.CoverURL == "" {
		return PublishResult{}, output.Err(output.CodeInputInvalid, "视频帖必须带封面 URL")
	}
	if o.ForumID == "" || o.GIDs == "" {
		return PublishResult{}, output.Err(output.CodeInputInvalid, "forum-id 与 gids 不能为空")
	}

	meta, err := json.Marshal(map[string]any{
		"describe": []map[string]any{{"insert": o.Text}},
		"vods":     []map[string]any{{"id": o.VideoID}},
	})
	if err != nil {
		return PublishResult{}, output.Err(output.CodeInternal, "构造 meta_content 失败: %v", err)
	}
	sc, err := json.Marshal([]map[string]any{{"insert": o.Text}})
	if err != nil {
		return PublishResult{}, output.Err(output.CodeInternal, "构造 structured_content 失败: %v", err)
	}
	topics := o.TopicIDs
	if topics == nil {
		topics = []string{}
	}
	body := map[string]any{
		"block_reply_img":         o.BlockReplyImg, // int 0/1；boolean → -502
		"collection_id":           0,
		"content":                 o.Text,
		"cover":                   o.CoverURL,
		"draft_id":                "",
		"forum_cate_id":           o.ForumCateID,
		"f_forum_id":              o.ForumID,
		"future_release_time":     0,
		"gids":                    o.GIDs,
		"game_uid":                "",
		"is_original":             0,
		"is_pre_publication":      false,
		"is_profit":               false,
		"meta_content":            string(meta),
		"post_id":                 "",
		"region":                  "",
		"release_time_type":       "1",
		"republish_authorization": 0,
		"review_id":               "",
		"structured_content":      string(sc),
		"subject":                 o.Subject,
		"topic_ids":               topics,
		"user_ai_content_choice":  "USER_AI_CONTENT_CHOICE_NOT_AI",
		"view_type":               5,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return PublishResult{}, output.Err(output.CodeInternal, "构造视频帖发布请求失败: %v", err)
	}

	raw, oerr := s.Client.Do(ctx, "POST", "/post/api/releasePost/v2", nil, b, headers(sess))
	if oerr != nil {
		return PublishResult{}, oerr
	}
	return parseReleaseEnvelope(raw)
}

// UndoReview 撤回处于审核中的帖子（实测审核中帖子的“删除”即此操作；
// 已正式发布的帖子用 Delete）。
func (s *Service) UndoReview(ctx context.Context, sess session.Session, reviewID string) *output.Error {
	if reviewID == "" {
		return output.Err(output.CodeInputInvalid, "review-id 不能为空")
	}
	b, err := json.Marshal(struct {
		ReviewID string `json:"review_id"`
	}{ReviewID: reviewID})
	if err != nil {
		return output.Err(output.CodeInternal, "构造撤审请求失败: %v", err)
	}
	return s.Client.DoJSON(ctx, "POST", "/post/api/review/undo", nil, b, headers(sess), &struct{}{})
}

// Publish 发布帖子（releasePost/v2）。只返回服务端结果，不代用户判断门槛去留。
func (s *Service) Publish(ctx context.Context, sess session.Session, opts PublishOptions) (PublishResult, *output.Error) {
	if opts.Subject == "" {
		return PublishResult{}, output.Err(output.CodeInputInvalid, "帖子标题不能为空")
	}
	if opts.ForumID == "" {
		return PublishResult{}, output.Err(output.CodeInputInvalid, "forum-id 不能为空")
	}
	if opts.ViewType == 0 {
		return PublishResult{}, output.Err(output.CodeInputInvalid, "view-type 必须显式指定（1/2/5）")
	}
	if opts.GIDs == 0 {
		return PublishResult{}, output.Err(output.CodeInputInvalid, "gids 不能为空")
	}

	body := map[string]any{
		"is_original": 0,
		"subject":     opts.Subject,
		"gids":        opts.GIDs,
		"contribution_act": map[string]any{
			"act_id": nil, "game_uid": nil, "title": nil,
			"game_region": nil, "game_nickname": nil,
		},
		"f_forum_id":         opts.ForumID,
		"uid":                sess.UID,
		"topic_ids":          []any{},
		"review_id":          "",
		"is_profit":          false,
		"is_pre_publication": false,
		"cover":              opts.Cover,
		"lottery":            map[string]any{},
		"forum_id":           opts.ForumID,
		"draft_id":           opts.DraftID,
		"structured_content": opts.StructuredContent,
		"link_card_ids":      []any{},
		"view_type":          opts.ViewType,
		"content":            opts.ContentHTML,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return PublishResult{}, output.Err(output.CodeInternal, "构造发布请求失败: %v", err)
	}
	raw, oerr := s.Client.Do(ctx, "POST", "/post/api/releasePost/v2", nil, b, headers(sess))
	if oerr != nil {
		return PublishResult{}, oerr
	}
	return parseReleaseEnvelope(raw)
}

// Delete 删除自己的帖子（operate_type=0，实测删帖原因列表第一类语义）。
func (s *Service) Delete(ctx context.Context, sess session.Session, postID string) *output.Error {
	if postID == "" {
		return output.Err(output.CodeInputInvalid, "post-id 不能为空")
	}
	b, err := json.Marshal(struct {
		OperateType int    `json:"operate_type"`
		PostID      string `json:"post_id"`
	}{OperateType: 0, PostID: postID})
	if err != nil {
		return output.Err(output.CodeInternal, "构造删帖请求失败: %v", err)
	}
	return s.Client.DoJSON(ctx, "POST", "/post/api/deletePost", nil, b, headers(sess), &struct{}{})
}
