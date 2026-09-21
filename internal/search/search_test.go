package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"mihoyo_cli/internal/output"

	"mihoyo_cli/internal/api"
)

func TestPosts_DefaultGIDsAndAnonymous(t *testing.T) {
	// 契约：CLI 默认值 DefaultGIDs=2 透传为 gids 参数；匿名调用（无 Cookie）；
	// 无 DS（实测无需）。服务层不再自动回填默认值（V3 §3.1）。
	_ = 0
	var gotQuery string
	var gotHeader http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/painter/api/searchPosts" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		gotHeader = r.Header.Clone()
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"post":{"post_id":"70908790","subject":"搜索结果一","view_type":2,"created_at":1764099161,"author":{"uid":"310892677","nickname":"作者甲"}}},
			{"post_id":"70908791","subject":"内联形态条目","view_type":1}
		],"last_id":"2","is_last":false}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := New(c)

	page, oerr := s.Posts(context.Background(), PostsOptions{Keyword: "原神", GIDs: DefaultGIDs, Size: 5})
	if oerr != nil {
		t.Fatalf("Posts: %v", oerr)
	}
	// CLI 默认值 DefaultGIDs 必须出现在请求里。
	if !strings.Contains(gotQuery, "gids="+DefaultGIDs) {
		t.Errorf("query 应含默认 gids=%s: %s", DefaultGIDs, gotQuery)
	}
	qv, err := url.ParseQuery(gotQuery)
	if err != nil {
		t.Fatalf("query 非法: %v", err)
	}
	if qv.Get("keyword") != "原神" || qv.Get("size") != "5" || qv.Get("gids") != "2" {
		t.Errorf("query = %s", gotQuery)
	}
	if gotHeader.Get("Cookie") != "" {
		t.Error("searchPosts 为匿名接口，不应携带 Cookie")
	}
	if gotHeader.Get("DS") != "" {
		t.Error("searchPosts 实测无需 DS")
	}
	if len(page.Items) != 2 || page.Items[0].PostID != "70908790" || page.Items[0].Author != "作者甲" {
		t.Errorf("items = %+v", page.Items)
	}
	// 内联形态条目（无 post 包装）也能解析。
	if page.Items[1].PostID != "70908791" {
		t.Errorf("items[1] = %+v", page.Items[1])
	}
	if !page.HasMore || page.NextCursor != "2" {
		t.Errorf("分页: hasMore=%v cursor=%q", page.HasMore, page.NextCursor)
	}
}

func TestPosts_EmptyGIDsRejectedWithoutRequest(t *testing.T) {
	// V3 §3.1：服务层把 GIDs 视为必填——空值立即 INPUT_INVALID，不发网络请求；
	// 不做默认回填（默认值由 CLI 层提供）。
	for _, gids := range []string{"", "   "} {
		var hits int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[],"is_last":true}}`)
		}))
		c, _ := api.New(srv.URL)
		s := New(c)

		_, oerr := s.Posts(context.Background(), PostsOptions{Keyword: "x", GIDs: gids})
		if oerr == nil || oerr.Code != output.CodeInputInvalid {
			t.Fatalf("gids=%q 应 INPUT_INVALID: %v", gids, oerr)
		}
		if n := atomic.LoadInt32(&hits); n != 0 {
			t.Errorf("gids=%q 不得发出请求: hits=%d", gids, n)
		}
		srv.Close()
	}
}

func TestPosts_InvalidGIDsRejected(t *testing.T) {
	// V3 §3.1：0、负数、非十进制一律 INPUT_INVALID，不发请求；不做游戏 ID 白名单。
	for _, gids := range []string{"abc", "0", "-3", "1.5", "2x"} {
		var hits int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[],"is_last":true}}`)
		}))
		c, _ := api.New(srv.URL)
		s := New(c)

		_, oerr := s.Posts(context.Background(), PostsOptions{Keyword: "x", GIDs: gids})
		if oerr == nil || oerr.Code != output.CodeInputInvalid {
			t.Fatalf("gids=%q 应 INPUT_INVALID: %v", gids, oerr)
		}
		if n := atomic.LoadInt32(&hits); n != 0 {
			t.Errorf("gids=%q 不得发出请求: hits=%d", gids, n)
		}
		srv.Close()
	}
}

func TestPosts_InvalidGIDsSilentlyEmpty(t *testing.T) {
	// 实测：非法 gids 服务端 rc=0 且空列表——适配器原样上报，不做猜测。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[],"is_last":true}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := New(c)

	page, oerr := s.Posts(context.Background(), PostsOptions{Keyword: "x", GIDs: "999"})
	if oerr != nil || len(page.Items) != 0 {
		t.Errorf("非法 gids 应透传空结果: %+v %+v", oerr, page)
	}
}

func TestTopics_DSRequiredAndParsed(t *testing.T) {
	var gotHeader http.Header
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/topic/api/searchTopic" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotHeader = r.Header.Clone()
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"topics":[
			{"id":1453,"name":"原神fes"},
			{"id":"238","name":"原神周边"}
		],"last_id":"","is_last":true}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := New(c)

	page, oerr := s.Topics(context.Background(), TopicsOptions{Keyword: "原神"})
	if oerr != nil {
		t.Fatalf("Topics: %v", oerr)
	}
	// 契约：searchTopic 需要 bbs DS；gids 无过滤效果故不发送。
	if gotHeader.Get("DS") == "" {
		t.Error("searchTopic 应携带 DS")
	}
	if strings.Contains(gotQuery, "gids") {
		t.Errorf("searchTopic 不应发送 gids: %s", gotQuery)
	}
	if len(page.Items) != 2 || page.Items[0].Name != "原神fes" || page.Items[1].ID.String() != "238" {
		t.Errorf("items = %+v", page.Items)
	}
	if page.HasMore {
		t.Error("is_last=true 不应 HasMore")
	}
}

func TestComprehensive_GroupsParsed(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{
			"posts":[],
			"topics":[{"id":1453,"name":"原神fes"}],
			"users":[{"uid":"384454482","nickname":"原神赛事","introduce":"介绍"}],
			"wikis":[{"id":"1473","title":"原神·印象","bbs_url":"https://baike.mihoyo.com/x"}],
			"directions":[]
		}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := New(c)

	res, oerr := s.Comprehensive(context.Background(), ComprehensiveOptions{Keyword: "原神", GIDs: DefaultGIDs, Preview: true})
	if oerr != nil {
		t.Fatalf("Comprehensive: %v", oerr)
	}
	if !strings.Contains(gotQuery, "gids=2") || !strings.Contains(gotQuery, "preview=1") {
		t.Errorf("query = %s", gotQuery)
	}
	if len(res.Topics) != 1 || res.Topics[0].ID.String() != "1453" {
		t.Errorf("topics = %+v", res.Topics)
	}
	if len(res.Users) != 1 || res.Users[0].UID.String() != "384454482" {
		t.Errorf("users = %+v", res.Users)
	}
	if len(res.Wikis) != 1 || res.Wikis[0].Title != "原神·印象" {
		t.Errorf("wikis = %+v", res.Wikis)
	}
}

func TestKeywordRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("不应发起请求")
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := New(c)

	if _, oerr := s.Posts(context.Background(), PostsOptions{}); oerr == nil {
		t.Error("Posts 空关键词应拒绝")
	}
	if _, oerr := s.Topics(context.Background(), TopicsOptions{}); oerr == nil {
		t.Error("Topics 空关键词应拒绝")
	}
	if _, oerr := s.Comprehensive(context.Background(), ComprehensiveOptions{}); oerr == nil {
		t.Error("Comprehensive 空关键词应拒绝")
	}
}

func TestDefaultGIDsValue(t *testing.T) {
	// 用户指定：默认带 gids 以区分游戏类型。当前默认社区 = 原神(2)。
	if DefaultGIDs != "2" {
		t.Errorf("DefaultGIDs = %q", DefaultGIDs)
	}
	var _ = json.Marshal
}

func TestPosts_SizeBelowMinimumRejected(t *testing.T) {
	// V3 §3.1：1/2 明确报错，不悄悄改成 3 后多输出结果；不发请求。
	for _, size := range []int{1, 2} {
		var hits int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[],"is_last":true}}`)
		}))
		c, _ := api.New(srv.URL)
		s := New(c)

		_, oerr := s.Posts(context.Background(), PostsOptions{Keyword: "x", GIDs: "2", Size: size})
		if oerr == nil || oerr.Code != output.CodeInputInvalid {
			t.Fatalf("size=%d 应 INPUT_INVALID: %v", size, oerr)
		}
		if n := atomic.LoadInt32(&hits); n != 0 {
			t.Errorf("size=%d 不得发出请求: hits=%d", size, n)
		}
		srv.Close()
	}
}

func TestComprehensive_EmptyGIDsRejectedWithoutRequest(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := New(c)

	if _, oerr := s.Comprehensive(context.Background(), ComprehensiveOptions{Keyword: "x", GIDs: ""}); oerr == nil ||
		oerr.Code != output.CodeInputInvalid {
		t.Fatalf("空 gids 应 INPUT_INVALID: %v", oerr)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Errorf("不得发出请求: hits=%d", n)
	}
}
