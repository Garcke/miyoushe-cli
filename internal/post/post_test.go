package post

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

func sessionSessionForTest() session.Session {
	return session.Session{
		UID: "100024680", MID: "mid_syn", Stoken: "v2_syn",
		DeviceID: "device-syn", DeviceFP: "fp0123456789a",
	}
}

func TestPostList_SinglePage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/painter/api/user_instant/list" {
			t.Errorf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("uid") != "100024680" || q.Get("size") != "20" || q.Get("last_id") != "" {
			t.Errorf("query = %v", q)
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"post":{"post_id":"p1","subject":"图文帖","view_type":2,"created_at":1700000000,"author":{"uid":100024680,"nickname":"作者甲"},"content":{"describe":"描述A"}}},
			{"post_id":"p2","subject":"内联帖","view_type":5,"created_at":1700000100}
		],"is_last":true}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	sess := sessionSessionForTest()
	page, oerr := New(c).List(context.Background(), sess, ListOptions{Limit: 20})
	if oerr != nil {
		t.Fatalf("List: %v", oerr)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items = %+v", page.Items)
	}
	if page.Items[0].PostID != "p1" || page.Items[0].Author != "作者甲" || page.Items[0].ViewType != 2 {
		t.Errorf("items[0] = %+v", page.Items[0])
	}
	if page.Items[1].PostID != "p2" || page.Items[1].Subject != "内联帖" {
		t.Errorf("items[1] = %+v", page.Items[1])
	}
	if page.HasMore || page.NextCursor != "" {
		t.Errorf("page = %+v", page)
	}
}

func TestPostList_PaginatesUntilLimit(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		switch calls {
		case 1:
			if q.Get("last_id") != "" {
				t.Errorf("首页不应带 last_id: %v", q)
			}
			fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
				{"post":{"post_id":"a1","subject":"s1","view_type":2}},
				{"post":{"post_id":"a2","subject":"s2","view_type":2}}],
				"is_last":false,"last_id":"cur1"}}`)
		case 2:
			if q.Get("last_id") != "cur1" {
				t.Errorf("第二页 last_id = %q", q.Get("last_id"))
			}
			fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
				{"post":{"post_id":"a3","subject":"s3","view_type":2}}],
				"is_last":true}}`)
		}
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	page, oerr := New(c).List(context.Background(), sessionSessionForTest(), ListOptions{Limit: 10})
	if oerr != nil {
		t.Fatalf("List: %v", oerr)
	}
	if len(page.Items) != 3 || calls != 2 {
		t.Errorf("items=%d calls=%d", len(page.Items), calls)
	}
	if page.HasMore {
		t.Error("is_last=true 后不应有更多")
	}
}

func TestPostList_LimitStopsPagination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"post":{"post_id":"a1","subject":"s1","view_type":2}},
			{"post":{"post_id":"a2","subject":"s2","view_type":2}}],
			"is_last":false,"last_id":"cur1"}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	page, oerr := New(c).List(context.Background(), sessionSessionForTest(), ListOptions{Limit: 1})
	if oerr != nil {
		t.Fatalf("List: %v", oerr)
	}
	if len(page.Items) != 1 || !page.HasMore || page.NextCursor != "cur1" {
		t.Errorf("page = %+v", page)
	}
}

func TestPostList_MidPaginationFailureCarriesResume(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
				{"post":{"post_id":"a1","subject":"s1","view_type":2}}],
				"is_last":false,"last_id":"cur1"}}`)
			return
		}
		w.WriteHeader(500)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	_, oerr := New(c).List(context.Background(), sessionSessionForTest(), ListOptions{Limit: 10})
	if oerr == nil {
		t.Fatal("中途失败应返回错误")
	}
	if oerr.ResumeCursor != "cur1" {
		t.Errorf("resume_cursor = %q", oerr.ResumeCursor)
	}
	if oerr.PartialData == nil {
		t.Error("partial_data 缺失")
	}
}

func TestPostShow_ParsesDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/post/api/getPostFull" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if q := r.URL.Query().Get("post_id"); q != "p9" {
			t.Errorf("post_id = %q", q)
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"post":{
			"post_id":"p9","subject":"视频混排","view_type":1,"created_at":1700000200,
			"author":{"uid":100024680,"nickname":"作者乙"},
			"content":{"describe":"看这个","imgs":[{"url":"https://upload-bbs.miyoushe.com/a.png"}]}
		},"vod_list":[{"vod":{"video_id":"v77","cover":"https://img/c.jpg","res_video_url":"https://vod/x.m3u8","duration":52208}}]}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	d, oerr := New(c).Get(context.Background(), sessionSessionForTest(), "p9")
	if oerr != nil {
		t.Fatalf("Get: %v", oerr)
	}
	if d.PostID != "p9" || d.Describe != "看这个" || d.AuthorUID != "100024680" {
		t.Errorf("detail = %+v", d)
	}
	if len(d.Images) != 1 || !strings.Contains(d.Images[0], "upload-bbs") {
		t.Errorf("images = %v", d.Images)
	}
	if len(d.Videos) != 1 || d.Videos[0].VideoID != "v77" || d.Videos[0].DurationMS != 52208 {
		t.Errorf("videos = %+v", d.Videos)
	}
	if d.Videos[0].URL != "https://vod/x.m3u8" {
		t.Errorf("video url = %q", d.Videos[0].URL)
	}
}

func TestPostShow_MissingPostField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	_, oerr := New(c).Get(context.Background(), sessionSessionForTest(), "p9")
	if oerr == nil || oerr.Code != output.CodeRemoteRejected {
		t.Fatalf("缺 post 字段应失败: %v", oerr)
	}
}
