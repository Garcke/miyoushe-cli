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
	page, oerr := New(c).List(context.Background(), testSess(), ListOptions{})
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
