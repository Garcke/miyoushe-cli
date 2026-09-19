package draft

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/session"
)

func testSess() session.Session {
	return session.Session{
		UID: "100024680", MID: "mid_syn", Stoken: "v2_syn",
		DeviceID: "device-syn", DeviceFP: "fp0123456789a",
	}
}

func TestDraftList_QueryAndParse(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/post/api/draft/list" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"draft":{"draft_id":"d1","subject":"草稿一","view_type":5,"updated_at":1700000000}},
			{"draft_id":2,"subject":"草稿二","view_type":2}
		],"is_last":true}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	page, oerr := New(c).List(context.Background(), testSess(), ListOptions{ViewType: 7})
	if oerr != nil {
		t.Fatalf("List: %v", oerr)
	}
	if gotQuery != "offset=&size=20&view_type=7" {
		t.Errorf("query = %s", gotQuery)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items = %+v", page.Items)
	}
	if page.Items[0].DraftID != "d1" || page.Items[0].Subject != "草稿一" {
		t.Errorf("items[0] = %+v", page.Items[0])
	}
	// draft_id 数值型也能解析为字符串。
	if page.Items[1].DraftID != "2" {
		t.Errorf("items[1].DraftID = %q", page.Items[1].DraftID)
	}
}

func TestDraftList_MergedBuckets(t *testing.T) {
	// 实测（2026-09-15）：view_type 按草稿类型分桶，"全部草稿"= 1/2/5 三桶并集。
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seen = append(seen, q.Get("view_type"))
		switch q.Get("view_type") {
		case "1":
			fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
				{"draft_id":"b1","subject":"桶1","view_type":1}],"is_last":true}}`)
		case "2":
			fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
				{"draft_id":"b2","subject":"桶2","view_type":2},
				{"draft_id":"b3","subject":"桶2b","view_type":2}],"is_last":true}}`)
		default:
			fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[],"is_last":true}}`)
		}
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	page, oerr := New(c).List(context.Background(), testSess(), ListOptions{})
	if oerr != nil {
		t.Fatalf("List: %v", oerr)
	}
	if len(seen) != 3 || seen[0] != "1" || seen[1] != "2" || seen[2] != "5" {
		t.Errorf("应按 1/2/5 三桶查询: %v", seen)
	}
	if len(page.Items) != 3 || page.Items[0].DraftID != "b1" || page.Items[2].DraftID != "b3" {
		t.Errorf("items = %+v", page.Items)
	}
	if page.HasMore {
		t.Error("各桶 is_last=true 时不应 HasMore")
	}
}

func TestDraftGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/post/api/draft/detail" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if q := r.URL.Query().Get("draft_id"); q != "d1" {
			t.Errorf("draft_id = %q", q)
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"draft":{
			"draft_id":"d1","subject":"草稿一","view_type":5,
			"content":{"describe":"正文内容","imgs":["https://upload-bbs.miyoushe.com/i.png"]}}}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	d, oerr := New(c).Get(context.Background(), testSess(), "d1")
	if oerr != nil {
		t.Fatalf("Get: %v", oerr)
	}
	if d.DraftID != "d1" || d.Describe != "正文内容" || len(d.Images) != 1 {
		t.Errorf("detail = %+v", d)
	}
}

func TestKindFromViewType(t *testing.T) {
	cases := map[int]string{1: "video", 2: "image", 5: "article", 3: ""}
	for vt, want := range cases {
		if got := KindFromViewType(vt); got != want {
			t.Errorf("KindFromViewType(%d) = %q, want %q", vt, got, want)
		}
	}
}
