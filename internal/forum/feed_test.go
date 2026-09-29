package forum

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/session"
)

func feedServer(t *testing.T, seen *[]string, seenHeaders *[]http.Header, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.URL.Path+"?"+r.URL.RawQuery)
		*seenHeaders = append(*seenHeaders, r.Header.Clone())
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFeed_HotIsDefaultAndAnonymous(t *testing.T) {
	var seen []string
	var headers []http.Header
	srv := feedServer(t, &seen, &headers, `{"retcode":0,"message":"OK","data":{"list":[
		{"post":{"post_id":"h1","subject":"热帖一","view_type":2,"created_at":1700000000}},
		{"post":{"post_id":"h2","subject":"热帖二","view_type":1}}
	],"last_id":"next-hot","is_last":false}}`)
	c, _ := api.New(srv.URL)
	// 传入一个带 Cookie 的会话，验证匿名端点不会附带它。
	sess := session.Session{UID: "1", MID: "m", Stoken: "s", DeviceID: "d", DeviceFP: "f"}
	page, oerr := New(c, sess).Feed(context.Background(), FeedOptions{ForumID: "26", GIDs: "2"})
	if oerr != nil {
		t.Fatalf("Feed: %v", oerr)
	}
	if len(seen) != 1 || !strings.HasPrefix(seen[0], "/painter/api/getHotForumPostList?") {
		t.Fatalf("默认应请求热帖端点: %v", seen)
	}
	q := seen[0]
	if !strings.Contains(q, "forum_id=26") || !strings.Contains(q, "gids=2") {
		t.Errorf("query 缺少必填参数: %s", q)
	}
	if strings.Contains(q, "last_id") {
		t.Errorf("首页不应携带 last_id: %s", q)
	}
	if got := headers[0].Get("Cookie"); got != "" {
		t.Errorf("匿名端点不得携带 Cookie: %q", got)
	}
	if got := headers[0].Get("DS"); got != "" {
		t.Errorf("匿名端点不得携带 DS: %q", got)
	}
	if len(page.Items) != 2 || page.Items[0].PostID != "h1" {
		t.Errorf("items = %+v", page.Items)
	}
	if page.NextCursor != "next-hot" || !page.HasMore {
		t.Errorf("page = %+v", page)
	}
}

func TestFeed_RecentUsesPageCursor(t *testing.T) {
	var seen []string
	var headers []http.Header
	srv := feedServer(t, &seen, &headers, `{"retcode":0,"message":"OK","data":{"list":[
		{"post":{"post_id":"r1","subject":"最新一","view_type":2}}
	],"page":"3","is_last":false}}`)
	c, _ := api.New(srv.URL)
	page, oerr := New(c, session.Session{}).Feed(context.Background(), FeedOptions{
		ForumID: "26", GIDs: "2", Order: FeedRecent, Cursor: "2",
	})
	if oerr != nil {
		t.Fatalf("Feed: %v", oerr)
	}
	if !strings.HasPrefix(seen[0], "/painter/api/getRecentForumPostList?") {
		t.Fatalf("recent 应请求最新帖端点: %v", seen)
	}
	if !strings.Contains(seen[0], "page=2") {
		t.Errorf("recent 游标应映射为 page: %s", seen[0])
	}
	if page.NextCursor != "3" {
		t.Errorf("next cursor 应取自 page 字段: %+v", page)
	}
}

func TestFeed_LastPageClearsCursor(t *testing.T) {
	var seen []string
	var headers []http.Header
	srv := feedServer(t, &seen, &headers, `{"retcode":0,"message":"OK","data":{"list":[],"is_last":true,"last_id":"stale"}}`)
	c, _ := api.New(srv.URL)
	page, oerr := New(c, session.Session{}).Feed(context.Background(), FeedOptions{ForumID: "26", GIDs: "2"})
	if oerr != nil {
		t.Fatalf("Feed: %v", oerr)
	}
	if page.HasMore || page.NextCursor != "" {
		t.Errorf("is_last=true 时不得保留游标: %+v", page)
	}
}

func TestFeed_Validation(t *testing.T) {
	c, _ := api.New("http://127.0.0.1:1")
	svc := New(c, session.Session{})

	if _, oerr := svc.Feed(context.Background(), FeedOptions{ForumID: "", GIDs: "2"}); oerr == nil ||
		oerr.Code != output.CodeInputInvalid {
		t.Errorf("缺 forum_id 应 INPUT_INVALID: %v", oerr)
	}
	if _, oerr := svc.Feed(context.Background(), FeedOptions{ForumID: "26", GIDs: "2", Order: "top"}); oerr == nil ||
		oerr.Code != output.CodeInputInvalid || !strings.Contains(oerr.Message, "hot or recent") {
		t.Errorf("非法 order 应 INPUT_INVALID: %v", oerr)
	}
}
