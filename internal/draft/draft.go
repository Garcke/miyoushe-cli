// Package draft 实现草稿只读能力：草稿箱列表与草稿详情。
// 证据等级 V（query: view_type=7&offset=&size=20 / draft_id）。
// draft save/publish/delete 等写命令按 fixture 门禁暂不注册。
package draft

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
type ListOptions struct {
	Cursor string
	Limit  int
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

// List 拉取草稿箱列表（view_type=7）。
func (s *Service) List(ctx context.Context, sess session.Session, opts ListOptions) (Page, *output.Error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = pageSize
	}
	page := Page{Items: []Draft{}}
	cursor := opts.Cursor
	for {
		q := url.Values{}
		q.Set("view_type", "7")
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
