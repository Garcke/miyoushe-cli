package draft

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

func testSess() session.Session {
	return session.Session{
		UID: "100024680", MID: "mid_syn", Stoken: "v2_syn",
		DeviceID: "device-syn", DeviceFP: "fp0123456789a",
	}
}

func TestDraftList_SingleBucket(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/post/api/draft/list" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"draft":{"draft_id":"d1","subject":"草稿一","view_type":5,"updated_at":1700000000}},
			{"draft_id":2,"subject":"草稿二","view_type":5}
		],"is_last":true}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	page, oerr := New(c).List(context.Background(), testSess(), ListOptions{ViewType: 5})
	if oerr != nil {
		t.Fatalf("List: %v", oerr)
	}
	if gotQuery != "offset=&size=20&view_type=5" {
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
	if page.HasMore || len(page.Warnings) != 0 {
		t.Errorf("单桶 is_last=true 不应有更多/警告: %+v", page)
	}
}

func TestDraftList_InvalidViewType(t *testing.T) {
	c, _ := api.New("http://127.0.0.1:1")
	_, oerr := New(c).List(context.Background(), testSess(), ListOptions{ViewType: 7})
	if oerr == nil || oerr.Code != output.CodeInputInvalid {
		t.Fatalf("view_type=7 应 INPUT_INVALID: %v", oerr)
	}
}

func TestDraftList_CrossBucketRejectsCursor(t *testing.T) {
	c, _ := api.New("http://127.0.0.1:1")
	_, oerr := New(c).List(context.Background(), testSess(), ListOptions{Cursor: "abc"})
	if oerr == nil || oerr.Code != output.CodeInputInvalid {
		t.Fatalf("跨桶预览 + --cursor 应 INPUT_INVALID: %v", oerr)
	}
}

func TestDraftList_CrossBucketSortDedupeTruncate(t *testing.T) {
	// 实测（2026-09-15）：view_type 按草稿类型分桶，"全部草稿"= 1/2/5 三桶并集。
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seen = append(seen, q.Get("view_type"))
		if got := q.Get("size"); got != "3" {
			t.Errorf("跨桶预览 size 应为 min(pageSize,limit)=3: %s", got)
		}
		switch q.Get("view_type") {
		case "1":
			fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
				{"draft_id":"b1","subject":"桶1","view_type":1,"updated_at":100}],"is_last":true}}`)
		case "2":
			fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
				{"draft_id":"b2","subject":"桶2","view_type":2,"updated_at":300},
				{"draft_id":"b3","subject":"桶2b","view_type":2,"updated_at":200}],"is_last":true}}`)
		default:
			// 桶 5 中 b2 重复出现（跨桶去重契约），另有 b4 更新时间最早。
			fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
				{"draft_id":"b2","subject":"重复b2","view_type":5,"updated_at":999},
				{"draft_id":"b4","subject":"桶5","view_type":5,"updated_at":50}],"is_last":true}}`)
		}
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	page, oerr := New(c).List(context.Background(), testSess(), ListOptions{Limit: 3})
	if oerr != nil {
		t.Fatalf("List: %v", oerr)
	}
	if len(seen) != 3 || seen[0] != "1" || seen[1] != "2" || seen[2] != "5" {
		t.Errorf("应按 1/2/5 三桶查询: %v", seen)
	}
	// 排序契约：updated_at 降序 → b2(300), b3(200), b1(100)；b4(50) 被截断。
	if len(page.Items) != 3 ||
		page.Items[0].DraftID != "b2" || page.Items[1].DraftID != "b3" || page.Items[2].DraftID != "b1" {
		t.Fatalf("items = %+v", page.Items)
	}
	for _, it := range page.Items {
		if it.DraftID == "b2" && it.Subject == "重复b2" {
			t.Error("跨桶重复 draft_id 未去重（应保留首次出现的桶2条目）")
		}
	}
	// 截断必须如实标记 has_more 并给出续页提示。
	if !page.HasMore {
		t.Error("合并结果被截断时应 HasMore=true")
	}
	if page.NextCursor != "" {
		t.Errorf("跨桶预览不应返回全局游标: %q", page.NextCursor)
	}
	if len(page.Warnings) == 0 || !strings.Contains(page.Warnings[0], "不能续页") {
		t.Errorf("截断/有更多时应提示预览不能续页: %v", page.Warnings)
	}
}

func TestDraftList_SingleBucketLimitContinuity(t *testing.T) {
	// --limit 5：请求 size 必须=5，游标必须紧跟已消费的第 5 条，
	// 不得取整页 20 条后跳过中间条目。
	var gotSizes []string
	var gotOffsets []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		gotSizes = append(gotSizes, q.Get("size"))
		gotOffsets = append(gotOffsets, q.Get("offset"))
		if q.Get("offset") == "" {
			fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
				{"draft_id":"a1","subject":"s1","view_type":5,"updated_at":5},
				{"draft_id":"a2","subject":"s2","view_type":5,"updated_at":4},
				{"draft_id":"a3","subject":"s3","view_type":5,"updated_at":3},
				{"draft_id":"a4","subject":"s4","view_type":5,"updated_at":2},
				{"draft_id":"a5","subject":"s5","view_type":5,"updated_at":1}],
				"is_last":false,"next_offset":"off5"}}`)
			return
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"draft_id":"a6","subject":"s6","view_type":5,"updated_at":0}],"is_last":true}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	page, oerr := New(c).List(context.Background(), testSess(), ListOptions{ViewType: 5, Limit: 5})
	if oerr != nil {
		t.Fatalf("List: %v", oerr)
	}
	if len(gotSizes) != 1 || gotSizes[0] != "5" {
		t.Errorf("首页请求 size 应=剩余配额 5: %v", gotSizes)
	}
	if gotOffsets[0] != "" {
		t.Errorf("首页 offset 应为空串: %v", gotOffsets)
	}
	if len(page.Items) != 5 || page.Items[4].DraftID != "a5" {
		t.Fatalf("items = %+v", page.Items)
	}
	if page.NextCursor != "off5" || !page.HasMore {
		t.Errorf("page = %+v", page)
	}
}

func TestDraftGet_NestedPost(t *testing.T) {
	// 2026-09-15 实测 §4.1：帖子本体在 data.draft.post（双层结构）。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/post/api/draft/detail" {
			t.Errorf("path = %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{
			"draft_id":"d1",
			"draft":{"post":{"draft_id":"d1","subject":"嵌套草稿","view_type":5,"updated_at":1700000100,
				"content":{"describe":"嵌套正文","imgs":["https://upload-bbs.miyoushe.com/n.png"]}}},
			"version":0,"lottery":null}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	d, oerr := New(c).Get(context.Background(), testSess(), "d1")
	if oerr != nil {
		t.Fatalf("Get: %v", oerr)
	}
	if d.DraftID != "d1" || d.Subject != "嵌套草稿" || d.Describe != "嵌套正文" || len(d.Images) != 1 {
		t.Errorf("detail = %+v", d)
	}
}

func TestDraftGet_FlatFallback(t *testing.T) {
	// 兼容扁平 data.draft 形态。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"draft":{
			"draft_id":"d1","subject":"扁平草稿","view_type":2,
			"content":{"describe":"正文内容","imgs":["https://upload-bbs.miyoushe.com/i.png"]}}}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	d, oerr := New(c).Get(context.Background(), testSess(), "d1")
	if oerr != nil {
		t.Fatalf("Get: %v", oerr)
	}
	if d.DraftID != "d1" || d.Subject != "扁平草稿" || d.Describe != "正文内容" || len(d.Images) != 1 {
		t.Errorf("detail = %+v", d)
	}
}
