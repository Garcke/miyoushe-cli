// Package draft 实现草稿能力：草稿箱列表、草稿详情与存/删写接口。
// 只读证据等级 V（query: view_type&offset&size / draft_id）。
// 写接口契约来自 2026-09-15 实测（见 docs/reference/cnb-mihoyo-api/snapshot/
// docs/api/ma-cn-passport扫码登录_2026-09-15实测.md §4）：
//   - view_type 参数按草稿类型分桶（1/2/5），"全部草稿"= 各桶并集；
//   - draft/save 新建不带 draft_id，block_reply_img 必须为 int（boolean → -502）。
//
// 发布/删帖在 post 包；CLI 命令注册仍按门禁另行处理。
package draft

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/session"
)

const pageSize = 20

// listViewTypes 是“全部草稿”需要遍历的分桶（实测 view_type 过滤草稿类型）。
var listViewTypes = []int{1, 2, 5}

// Draft 是草稿列表条目。
type Draft struct {
	DraftID   string `json:"draft_id"`
	Subject   string `json:"subject"`
	ViewType  int    `json:"view_type"`
	CreatedAt int64  `json:"created_at,omitempty"`
	UpdatedAt int64  `json:"updated_at,omitempty"`
}

// Detail 是草稿详情。
type Detail struct {
	Draft
	Describe string   `json:"describe,omitempty"`
	Images   []string `json:"images,omitempty"`
}

// ListOptions 控制 draft list。
// ViewType=0 表示合并 1/2/5 三桶（“全部草稿”，不支持游标续翻）；
// 显式指定（如 7）时按单桶语义支持游标。
type ListOptions struct {
	Cursor   string
	Limit    int
	ViewType int
}

// Page 是一次草稿列表读取的完整结果。
type Page struct {
	Items      []Draft
	NextCursor string
	HasMore    bool
}

type draftRaw struct {
	DraftID   api.FlexString `json:"draft_id"`
	Subject   string         `json:"subject"`
	ViewType  int            `json:"view_type"`
	CreatedAt int64          `json:"created_at"`
	UpdatedAt int64          `json:"updated_at"`

	Content *struct {
		Describe string           `json:"describe"`
		Imgs     []api.FlexString `json:"imgs"`
	} `json:"content"`
}

type listEntry struct {
	Draft *draftRaw `json:"draft"`
	draftRaw
}

// Service 是草稿只读服务。
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

func (raw *draftRaw) toDraft() (Draft, *output.Error) {
	id := raw.DraftID.String()
	if id == "" {
		return Draft{}, output.Err(output.CodeRemoteRejected, "草稿响应缺少 draft_id")
	}
	return Draft{
		DraftID:   id,
		Subject:   raw.Subject,
		ViewType:  raw.ViewType,
		CreatedAt: raw.CreatedAt,
		UpdatedAt: raw.UpdatedAt,
	}, nil
}

// List 拉取草稿箱。ViewType=0 时合并 1/2/5 三桶各首页（不支持游标）；
// 指定 ViewType 时保持单桶游标语义。
func (s *Service) List(ctx context.Context, sess session.Session, opts ListOptions) (Page, *output.Error) {
	if opts.ViewType == 0 {
		return s.listAllBuckets(ctx, sess, opts)
	}
	return s.listBucket(ctx, sess, opts)
}

// listAllBuckets 合并各草稿类型桶的首页。跨桶深分页未实现：
// 每桶取一页（size=limit），合并后截断到 limit；HasMore 如实上报。
func (s *Service) listAllBuckets(ctx context.Context, sess session.Session, opts ListOptions) (Page, *output.Error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = pageSize
	}
	page := Page{Items: []Draft{}}
	for _, vt := range listViewTypes {
		q := url.Values{}
		q.Set("view_type", strconv.Itoa(vt))
		q.Set("size", strconv.Itoa(limit))
		q.Set("offset", "")
		var data struct {
			api.ListMeta
			List []listEntry `json:"list"`
		}
		if oerr := s.Client.DoJSON(ctx, "GET", "/post/api/draft/list", q, nil, headers(sess), &data); oerr != nil {
			if len(page.Items) > 0 {
				oe := output.Err(output.CodeRemoteRejected,
					"草稿列表（view_type=%d）读取失败: %s", vt, oerr.Message)
				oe.PartialData = ListDataJSON(page)
				return Page{}, oe
			}
			return Page{}, oerr
		}
		for _, entry := range data.List {
			raw := entry.Draft
			if raw == nil {
				raw = &entry.draftRaw
			}
			d, oerr := raw.toDraft()
			if oerr != nil {
				return Page{}, oerr
			}
			page.Items = append(page.Items, d)
		}
		if data.HasMore() {
			page.HasMore = true
		}
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
	}
	return page, nil
}

// listBucket 单桶拉取，支持游标续翻（原 view_type=7 语义的参数化版本）。
func (s *Service) listBucket(ctx context.Context, sess session.Session, opts ListOptions) (Page, *output.Error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = pageSize
	}
	page := Page{Items: []Draft{}}
	cursor := opts.Cursor
	for {
		q := url.Values{}
		q.Set("view_type", strconv.Itoa(opts.ViewType))
		q.Set("size", strconv.Itoa(pageSize))
		q.Set("offset", cursor) // 首页传空串，与抓包证据一致

		var data struct {
			api.ListMeta
			List []listEntry `json:"list"`
		}
		if oerr := s.Client.DoJSON(ctx, "GET", "/post/api/draft/list", q, nil, headers(sess), &data); oerr != nil {
			if len(page.Items) > 0 {
				oe := output.Err(output.CodeRemoteRejected,
					"草稿列表第 %d 页读取失败: %s", len(page.Items)/pageSize+1, oerr.Message)
				oe.ResumeCursor = cursor
				return Page{}, oe
			}
			return Page{}, oerr
		}
		for _, entry := range data.List {
			raw := entry.Draft
			if raw == nil {
				raw = &entry.draftRaw
			}
			d, oerr := raw.toDraft()
			if oerr != nil {
				return Page{}, oerr
			}
			page.Items = append(page.Items, d)
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

// Get 拉取草稿详情。
func (s *Service) Get(ctx context.Context, sess session.Session, draftID string) (Detail, *output.Error) {
	if draftID == "" {
		return Detail{}, output.Err(output.CodeInputInvalid, "draft-id 不能为空")
	}
	q := url.Values{}
	q.Set("draft_id", draftID)

	var data struct {
		Draft *draftRaw `json:"draft"`
	}
	if oerr := s.Client.DoJSON(ctx, "GET", "/post/api/draft/detail", q, nil, headers(sess), &data); oerr != nil {
		return Detail{}, oerr
	}
	if data.Draft == nil {
		return Detail{}, output.Err(output.CodeRemoteRejected, "草稿详情响应缺少 draft 字段")
	}
	d0, oerr := data.Draft.toDraft()
	if oerr != nil {
		return Detail{}, oerr
	}
	d := Detail{Draft: d0}
	if c := data.Draft.Content; c != nil {
		d.Describe = c.Describe
		for _, img := range c.Imgs {
			if img != "" {
				d.Images = append(d.Images, img.String())
			}
		}
	}
	return d, nil
}

// KindFromViewType 把 view_type 映射为内容 kind；未知映射返回空串。
// 仅用于 --kind 的客户端过滤，不代表服务端语义。
func KindFromViewType(vt int) string {
	switch vt {
	case 1:
		return "video"
	case 2:
		return "image"
	case 5:
		return "article"
	}
	return ""
}

// SaveOptions 是保存草稿的内容输入。
// StructuredContent 传 Quill delta 序列化后的 JSON 字符串（与 content HTML 同内容）。
// BlockReplyImg 契约：int 0/1；服务端对 boolean 直接 -502（实测 §4.1），0 值省略发送。
type SaveOptions struct {
	Subject           string
	ContentHTML       string
	StructuredContent string
	ForumID           string
	ViewType          int
	Cover             string
	GIDs              int
	BlockReplyImg     int
}

// Save 保存草稿（新建）。返回服务端签发的 draft_id。
// 实测契约：新建不带 draft_id；is_profit/is_original/topic_ids 可选（全缺也 rc=0）。
func (s *Service) Save(ctx context.Context, sess session.Session, opts SaveOptions) (string, *output.Error) {
	if opts.Subject == "" {
		return "", output.Err(output.CodeInputInvalid, "草稿标题不能为空")
	}
	if opts.ForumID == "" {
		return "", output.Err(output.CodeInputInvalid, "forum-id 不能为空")
	}
	if opts.ViewType == 0 {
		return "", output.Err(output.CodeInputInvalid, "view-type 必须显式指定（1/2/5）")
	}
	if opts.GIDs == 0 {
		return "", output.Err(output.CodeInputInvalid, "gids 不能为空")
	}

	body := map[string]any{
		"is_profit":          false,
		"forum_id":           opts.ForumID,
		"view_type":          opts.ViewType,
		"content":            opts.ContentHTML,
		"is_original":        0,
		"topic_ids":          []any{},
		"structured_content": opts.StructuredContent,
		"subject":            opts.Subject,
		"cover":              opts.Cover,
		"gids":               opts.GIDs,
	}
	if opts.BlockReplyImg != 0 {
		body["block_reply_img"] = opts.BlockReplyImg // int 0/1；绝不能是 bool
	}
	b, err := json.Marshal(body)
	if err != nil {
		return "", output.Err(output.CodeInternal, "构造草稿保存请求失败: %v", err)
	}

	h := headers(sess)
	var data struct {
		DraftID api.FlexString `json:"draft_id"`
	}
	if oerr := s.Client.DoJSON(ctx, "POST", "/post/api/draft/save", nil, b, h, &data); oerr != nil {
		return "", oerr
	}
	id := data.DraftID.String()
	if id == "" {
		return "", output.Err(output.CodeRemoteRejected, "保存草稿响应缺少 draft_id")
	}
	return id, nil
}

// Delete 删除草稿（幂等由服务端决定；已删草稿重复删除返回远端错误原样上报）。
func (s *Service) Delete(ctx context.Context, sess session.Session, draftID string) *output.Error {
	if draftID == "" {
		return output.Err(output.CodeInputInvalid, "draft-id 不能为空")
	}
	b, err := json.Marshal(struct {
		DraftID string `json:"draft_id"`
	}{DraftID: draftID})
	if err != nil {
		return output.Err(output.CodeInternal, "构造草稿删除请求失败: %v", err)
	}
	return s.Client.DoJSON(ctx, "POST", "/post/api/draft/delete", nil, b, headers(sess), &struct{}{})
}

// ListDataJSON 供部分失败时把已消费结果放入 error.partial_data。
func ListDataJSON(p Page) any {
	items := p.Items
	if items == nil {
		items = []Draft{}
	}
	return map[string]any{
		"items":    items,
		"has_more": p.HasMore,
	}
}
