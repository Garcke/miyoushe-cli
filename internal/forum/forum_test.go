package forum

import (
	"context"

	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/session"
)

func TestGamesForums_AnonymousAndParsed(t *testing.T) {
	var gotHeader http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apihub/api/getAllGamesForums" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotHeader = r.Header.Clone()
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"game_id":2,"forums":[{"id":26,"game_id":2,"name":"酒馆","create_type":"0","post_order":"reply"},
			                       {"id":43,"game_id":2,"name":"攻略","create_type":"0"}]},
			{"game_id":9,"forums":[{"id":947,"game_id":9,"name":"官方","create_type":"1"}]}
		]}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := New(c, session.Session{})

	games, oerr := s.GamesForums(context.Background())
	if oerr != nil {
		t.Fatalf("GamesForums: %v", oerr)
	}
	if gotHeader.Get("Cookie") != "" {
		t.Error("分区目录为匿名接口，不应携带 Cookie")
	}
	if len(games) != 2 || games[1].GameID.String() != "9" {
		t.Errorf("games = %+v", games)
	}
	if len(games[0].Forums) != 2 || games[0].Forums[0].Name != "酒馆" || games[0].Forums[0].ID.String() != "26" {
		t.Errorf("forums = %+v", games[0].Forums)
	}
}

func TestDiscussion_Parsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("gids") != "2" {
			t.Errorf("gids = %s", r.URL.Query().Get("gids"))
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"discussion":{
			"discussion_id":2,"game_id":2,"subject":"旅行者讨论区",
			"forums":[{"id":26,"game_id":2,"name":"酒馆","des":"流传着旅行者们的冒险传说哟~"}]}}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := New(c, session.Session{})

	d, oerr := s.Discussion(context.Background(), "2")
	if oerr != nil {
		t.Fatalf("Discussion: %v", oerr)
	}
	if d.Subject != "旅行者讨论区" || len(d.Forums) != 1 || d.Forums[0].ID.String() != "26" {
		t.Errorf("discussion = %+v", d)
	}
	// V3 §5.1：分区模型包含 name；命令层对缺失显示“未提供”。
	if d.Forums[0].Name != "酒馆" || d.Forums[0].Des == "" {
		t.Errorf("分区 name/des 解析: %+v", d.Forums[0])
	}
}

func TestDiscussion_MissingIDsAreStructureErrors(t *testing.T) {
	cases := []string{
		`{"retcode":0,"message":"OK","data":{"discussion":{"subject":"x","forums":[{"id":26}]}}}`,          // 缺 discussion_id
		`{"retcode":0,"message":"OK","data":{"discussion":{"discussion_id":2,"forums":[{"name":"无ID"}]}}}`, // 缺分区 id
	}
	for _, body := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, body)
		}))
		c, _ := api.New(srv.URL)
		_, oerr := New(c, session.Session{}).Discussion(context.Background(), "2")
		srv.Close()
		if oerr == nil || oerr.Code != output.CodeRemoteRejected {
			t.Fatalf("ID 缺失应报响应结构错误: %v", oerr)
		}
	}
}

func TestDiscussion_NameMissingAllowed(t *testing.T) {
	// 名称缺失不是结构错误：由调用方显示“未提供”。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"discussion":{
			"discussion_id":2,"forums":[{"id":26}]}}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	d, oerr := New(c, session.Session{}).Discussion(context.Background(), "2")
	if oerr != nil {
		t.Fatalf("Discussion: %v", oerr)
	}
	if d.Forums[0].Name != "" || d.Forums[0].Des != "" {
		t.Errorf("缺省字段应为空串: %+v", d.Forums[0])
	}
}

func TestForumPosts_QueryDSAndPagination(t *testing.T) {
	var gotQuery string
	var gotHeader http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/post/api/getForumPostList" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		gotHeader = r.Header.Clone()
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"post":{"post_id":"78131063","subject":"分区帖子一","view_type":2,"created_at":1764099161}},
			{"post":{"post_id":"78131064","subject":"分区帖子二","view_type":1}}
		],"last_id":"55","is_last":false}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := New(c, session.Session{
		UID: "u", MID: "m", Stoken: "v2_t", DeviceID: "d", DeviceFP: "f",
	})

	page, oerr := s.ForumPosts(context.Background(), ForumPostsOptions{
		ForumID: "948", GIDs: "9", LastID: "54",
	})
	if oerr != nil {
		t.Fatalf("ForumPosts: %v", oerr)
	}
	// 契约：forum_id/gids 必发；不发送 size（服务端固定 20 条）；last_id 翻页。
	qv, _ := parseQuery(gotQuery)
	if qv.Get("forum_id") != "948" || qv.Get("gids") != "9" || qv.Get("last_id") != "54" {
		t.Errorf("query = %s", gotQuery)
	}
	if _, ok := qv["size"]; ok {
		t.Errorf("服务端忽略 size，不应发送: %s", gotQuery)
	}
	if gotHeader.Get("DS") == "" || gotHeader.Get("Cookie") == "" {
		t.Error("分区帖子流应携带 DS 与 Cookie")
	}
	if len(page.Items) != 2 || page.Items[0].PostID != "78131063" {
		t.Errorf("items = %+v", page.Items)
	}
	if !page.HasMore || page.NextCursor != "55" {
		t.Errorf("分页: hasMore=%v cursor=%q", page.HasMore, page.NextCursor)
	}
}

func TestForumPosts_InputValidation(t *testing.T) {
	c, _ := api.New("http://127.0.0.1:1")
	s := New(c, session.Session{UID: "u", MID: "m", Stoken: "s", DeviceID: "d", DeviceFP: "f"})
	if _, oerr := s.ForumPosts(context.Background(), ForumPostsOptions{GIDs: "2"}); oerr == nil {
		t.Error("缺 forum-id 应拒绝")
	}
	if _, oerr := s.ForumPosts(context.Background(), ForumPostsOptions{ForumID: "26"}); oerr == nil {
		t.Error("缺 gids 应拒绝")
	}
	if _, oerr := s.Discussion(context.Background(), ""); oerr == nil {
		t.Error("空 gids 应拒绝")
	}
}

func parseQuery(q string) (url.Values, error) {
	return url.ParseQuery(q)
}
