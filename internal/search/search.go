// Package search 实现米游社搜索能力：帖子搜索（painter）、话题搜索（topic）
// 与综合搜索（apihub v2）。
//
// 接口契约（2026-09-19 实测）：
//   - 三个接口均匿名可调（无需 Cookie）；searchPosts 与 v2/search 无需 DS，
//     searchTopic 需 bbs DS；
//   - searchPosts 与 v2/search 必须携带**非空 gids**（服务层视为必填，
//     空值不发请求直接 INPUT_INVALID）；CLI 要求显式传入 gids，
//     不提供全站搜索模式；
//   - gids 为正整数、不做游戏 ID 白名单（新游戏上线不因此拒绝）；
//     未知 ID 由服务端返回空结果，适配器原样上报；
//   - searchTopic 的 gids 无过滤效果，故命令不提供该参数：它是跨社区
//     话题搜索，不承诺按游戏过滤；
//   - 实测 size<3 时服务端静默返回空列表——服务层拒绝 1/2，不静默修改。
package search

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/post"
	"mihoyo_cli/internal/protocol"
)

// 注意：本包不提供默认社区。all/posts 的 --gids 由调用方显式
// 传入正整数；调用者漏传时服务层返回输入错误，不做默认回填。

// validateGIDs 校验 gids 为非空正整数：不做游戏 ID 白名单，
// 未知 ID 交由服务端返回空结果。空值表示调用方漏传，直接拒绝。
func validateGIDs(gids string) (string, *output.Error) {
	v := strings.TrimSpace(gids)
	if v == "" {
		return "", output.Err(output.CodeInputInvalid,
			"Missing required option --gids: this endpoint needs an explicit positive GIDs")
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return "", output.Err(output.CodeInputInvalid, "gids must be a positive integer (got %q)", gids)
	}
	return v, nil
}

// resolveSize 解析 size：未指定（<=0）用默认 20；1/2 明确拒绝而不是
// 悄悄改成 3。
func resolveSize(size int) (int, *output.Error) {
	if size <= 0 {
		return 20, nil
	}
	if size < 3 {
		return 0, output.Err(output.CodeInputInvalid,
			"size must be at least 3 (the server returns an empty list below 3; input %d is not silently changed)", size)
	}
	return size, nil
}

// Service 是搜索服务。匿名请求：设备上下文在构造时随机生成（实测随机
// 设备即可调用）。
type Service struct {
	Client *api.Client
	Device protocol.DeviceContext
}

// New 构造服务；Client 指向 bbs-api.miyoushe.com（测试可注入）。
func New(c *api.Client) *Service {
	return &Service{
		Client: c,
		Device: protocol.DeviceContext{
			DeviceID: randHex(32),
			DeviceFP: randHex(13),
		},
	}
}

func randHex(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		panic("search: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)[:n]
}

func headers(s *Service, withDS bool) http.Header {
	h := protocol.CommonHeaders(protocol.ClientTypeAndroid, s.Device)
	if withDS {
		protocol.WithDS(h, protocol.NewDSBBS())
	}
	return h
}

// PostsOptions 控制帖子搜索。GIDs 为必填：服务层不做默认回填，
// 空值报 INPUT_INVALID；CLI 层要求用户显式传入并拒绝空值。
type PostsOptions struct {
	Keyword   string
	GIDs      string
	LastID    string
	Size      int
	OrderType string // 空 = 服务端默认排序
}

// PostPage 是一次帖子搜索的结果。条目与收藏夹同构（post.Entry）。
type PostPage struct {
	Items      []post.Summary
	NextCursor string
	HasMore    bool
}

// Posts 搜索帖子（GET /painter/api/searchPosts，无需 DS/Cookie）。
func (s *Service) Posts(ctx context.Context, o PostsOptions) (PostPage, *output.Error) {
	if o.Keyword == "" {
		return PostPage{}, output.Err(output.CodeInputInvalid, "Keyword cannot be empty")
	}
	size, oerr := resolveSize(o.Size)
	if oerr != nil {
		return PostPage{}, oerr
	}
	gids, oerr := validateGIDs(o.GIDs)
	if oerr != nil {
		return PostPage{}, oerr
	}
	q := url.Values{}
	q.Set("keyword", o.Keyword)
	q.Set("size", strconv.Itoa(size))
	q.Set("gids", gids)
	if o.LastID != "" {
		q.Set("last_id", o.LastID)
	}
	if o.OrderType != "" {
		q.Set("order_type", o.OrderType)
	}

	var data struct {
		api.ListMeta
		List []post.Entry `json:"list"`
	}
	if oerr := s.Client.DoJSON(ctx, "GET", "/painter/api/searchPosts", q, nil, headers(s, false), &data); oerr != nil {
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

// TopicsOptions 控制话题搜索。
type TopicsOptions struct {
	Keyword string
	LastID  string
	Size    int
}

// Topic 是话题搜索条目。
type Topic struct {
	ID   api.FlexString `json:"id"`
	Name string         `json:"name"`
}

// TopicPage 是一次话题搜索的结果。
type TopicPage struct {
	Items      []Topic
	NextCursor string
	HasMore    bool
}

// Topics 搜索话题（GET /topic/api/searchTopic，需 bbs DS，无需 Cookie）。
// 实测 gids 对本接口无过滤效果，故不发送；命令层也不提供该参数。
func (s *Service) Topics(ctx context.Context, o TopicsOptions) (TopicPage, *output.Error) {
	if o.Keyword == "" {
		return TopicPage{}, output.Err(output.CodeInputInvalid, "Keyword cannot be empty")
	}
	size, oerr := resolveSize(o.Size)
	if oerr != nil {
		return TopicPage{}, oerr
	}
	q := url.Values{}
	q.Set("keyword", o.Keyword)
	q.Set("size", strconv.Itoa(size))
	if o.LastID != "" {
		q.Set("last_id", o.LastID)
	}

	var data struct {
		api.ListMeta
		Topics []Topic `json:"topics"`
	}
	if oerr := s.Client.DoJSON(ctx, "GET", "/topic/api/searchTopic", q, nil, headers(s, true), &data); oerr != nil {
		return TopicPage{}, oerr
	}
	page := TopicPage{Items: []Topic{}}
	for _, t := range data.Topics {
		if t.ID.String() == "" {
			return TopicPage{}, output.Err(output.CodeRemoteRejected, "Topic response is missing id")
		}
		page.Items = append(page.Items, t)
	}
	page.NextCursor = data.Cursor()
	page.HasMore = data.HasMore()
	return page, nil
}

// ComprehensiveOptions 控制综合搜索。
type ComprehensiveOptions struct {
	Keyword string
	GIDs    string // 必填：空值 INPUT_INVALID，服务层不回填默认
	Preview bool
}

// User 是综合搜索的用户条目。
type User struct {
	UID       api.FlexString `json:"uid"`
	Nickname  string         `json:"nickname"`
	Introduce string         `json:"introduce"`
}

// Wiki 是综合搜索的百科条目。
type Wiki struct {
	ID     api.FlexString `json:"id"`
	Title  string         `json:"title"`
	BBSURL api.FlexString `json:"bbs_url"`
}

// ComprehensiveResult 是综合搜索的分组结果。
// posts 组在 preview 模式下实测恒为空，帖子搜索用 Posts。
// Posts 是规范化后的摘要；每个已支持分组在 JSON 中始终存在（空为 []）。
type ComprehensiveResult struct {
	Topics []Topic
	Users  []User
	Wikis  []Wiki
	Posts  []post.Summary
}

// ComprehensiveData 是该命令 JSON data 的固定形状；partial_data 使用同一形状。
type ComprehensiveData struct {
	Kind    string         `json:"kind"`
	Context map[string]any `json:"context"`
	Topics  []Topic        `json:"topics"`
	Users   []User         `json:"users"`
	Wikis   []Wiki         `json:"wikis"`
	Posts   []post.Summary `json:"posts"`
}

// Data 把结果规范化为固定形状；空分组为 []，context 记录查询上下文。
func (r ComprehensiveResult) Data(keyword, gids string) ComprehensiveData {
	d := ComprehensiveData{
		Kind:    "search",
		Context: map[string]any{"query": keyword, "gids": gids, "scope": "mixed"},
		Topics:  r.Topics,
		Users:   r.Users,
		Wikis:   r.Wikis,
		Posts:   r.Posts,
	}
	if d.Topics == nil {
		d.Topics = []Topic{}
	}
	if d.Users == nil {
		d.Users = []User{}
	}
	if d.Wikis == nil {
		d.Wikis = []Wiki{}
	}
	if d.Posts == nil {
		d.Posts = []post.Summary{}
	}
	return d
}

// Comprehensive 综合搜索（GET /apihub/api/v2/search，无需 DS/Cookie）。
func (s *Service) Comprehensive(ctx context.Context, o ComprehensiveOptions) (ComprehensiveResult, *output.Error) {
	if o.Keyword == "" {
		return ComprehensiveResult{}, output.Err(output.CodeInputInvalid, "Keyword cannot be empty")
	}
	gids, oerr := validateGIDs(o.GIDs)
	if oerr != nil {
		return ComprehensiveResult{}, oerr
	}
	q := url.Values{}
	q.Set("keyword", o.Keyword)
	q.Set("gids", gids)
	if o.Preview {
		q.Set("preview", "1")
	}

	var data struct {
		Posts      []post.Entry `json:"posts"`
		Topics     []Topic      `json:"topics"`
		Users      []User       `json:"users"`
		Wikis      []Wiki       `json:"wikis"`
		Directions []any        `json:"directions"`
	}
	if oerr := s.Client.DoJSON(ctx, "GET", "/apihub/api/v2/search", q, nil, headers(s, false), &data); oerr != nil {
		return ComprehensiveResult{}, oerr
	}

	// 规范化每个分组：单条帖子解析失败不得静默跳过。
	res := ComprehensiveResult{Topics: data.Topics, Users: data.Users, Wikis: data.Wikis}
	for i := range data.Posts {
		sum, oerr := data.Posts[i].Summary()
		if oerr != nil {
			oe := output.Err(output.CodeProtocolMismatch,
				"Mixed search item in group %q at index %d could not be normalized (field %s)",
				"posts", i, oerr.Message)
			oe.Context = map[string]any{
				"group": "posts",
				"index": i,
				"field": oerr.Message,
			}
			// 已规范化成功的分组形状与正常 data 相同。
			partial := res
			partial.Posts = res.Posts
			oe.PartialData = partial.Data(o.Keyword, gids)
			return ComprehensiveResult{}, oe
		}
		res.Posts = append(res.Posts, sum)
	}
	return res, nil
}

// SummaryCount 供 CLI 展示分组计数。
func (r ComprehensiveResult) SummaryCount() string {
	return fmt.Sprintf("topics=%d users=%d wikis=%d posts=%d",
		len(r.Topics), len(r.Users), len(r.Wikis), len(r.Posts))
}

// UsersOptions 控制用户搜索。
type UsersOptions struct {
	Keyword string
	LastID  string
	Size    int
}

// UserPage 是一次用户搜索的结果。
type UserPage struct {
	Items      []User
	NextCursor string
	HasMore    bool
}

// Users 搜索用户（GET /user/api/searchUser，需 bbs DS，无需 Cookie）。
// 2026-09-19 实测：data 为 {users, last_id, is_last}；App「用户」标签后端。
func (s *Service) Users(ctx context.Context, o UsersOptions) (UserPage, *output.Error) {
	if o.Keyword == "" {
		return UserPage{}, output.Err(output.CodeInputInvalid, "Keyword cannot be empty")
	}
	size := o.Size
	if size <= 0 {
		size = 20
	}
	q := url.Values{}
	q.Set("keyword", o.Keyword)
	q.Set("size", strconv.Itoa(size))
	if o.LastID != "" {
		q.Set("last_id", o.LastID)
	}

	var data struct {
		Users []User `json:"users"`
		api.ListMeta
	}
	if oerr := s.Client.DoJSON(ctx, "GET", "/user/api/searchUser", q, nil, headers(s, true), &data); oerr != nil {
		return UserPage{}, oerr
	}
	page := UserPage{Items: []User{}}
	for _, u := range data.Users {
		if u.UID.String() == "" {
			return UserPage{}, output.Err(output.CodeRemoteRejected, "User response is missing uid")
		}
		page.Items = append(page.Items, u)
	}
	page.NextCursor = data.Cursor()
	page.HasMore = data.HasMore()
	return page, nil
}

// WalkthroughsOptions 控制攻略搜索。
type WalkthroughsOptions struct {
	Keyword string
	GIDs    string
	LastID  string
	Size    int
}

// WalkthroughPage 是一次攻略搜索的结果（posts + topics 双组）。
// 注意：索引范围为攻略类内容，对泛关键词可能返回 0 条（实测搜"原神"0 条、
// 搜"攻略/配队"11 条）——空结果不代表端点失效。
type WalkthroughPage struct {
	Posts      []post.Summary
	Topics     []Topic
	NextCursor string
	HasMore    bool
}

// Walkthroughs 搜索攻略（GET /apihub/api/searchWalkthrough，需 bbs DS，无需 Cookie）。
// App「攻略」标签后端。gids 过滤生效（实测 gids=2 返回攻略区/forum=43 内容）。
func (s *Service) Walkthroughs(ctx context.Context, o WalkthroughsOptions) (WalkthroughPage, *output.Error) {
	if o.Keyword == "" {
		return WalkthroughPage{}, output.Err(output.CodeInputInvalid, "Keyword cannot be empty")
	}
	size := o.Size
	if size <= 0 {
		size = 20
	}
	if size < 3 {
		size = 3
	}
	q := url.Values{}
	q.Set("keyword", o.Keyword)
	q.Set("size", strconv.Itoa(size))
	if o.GIDs != "" {
		q.Set("gids", o.GIDs)
	}
	if o.LastID != "" {
		q.Set("last_id", o.LastID)
	}

	var data struct {
		Posts  []post.Entry `json:"posts"`
		Topics []Topic      `json:"topics"`
		api.ListMeta
	}
	if oerr := s.Client.DoJSON(ctx, "GET", "/apihub/api/searchWalkthrough", q, nil, headers(s, true), &data); oerr != nil {
		return WalkthroughPage{}, oerr
	}
	page := WalkthroughPage{Posts: []post.Summary{}}
	for i := range data.Posts {
		sum, oerr := data.Posts[i].Summary()
		if oerr != nil {
			return WalkthroughPage{}, oerr
		}
		page.Posts = append(page.Posts, sum)
	}
	page.Topics = data.Topics
	page.NextCursor = data.Cursor()
	page.HasMore = data.HasMore()
	return page, nil
}
