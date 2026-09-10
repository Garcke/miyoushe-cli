package favorite

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/post"
	"mihoyo_cli/internal/role"
	"mihoyo_cli/internal/session"
)

func testSess() session.Session {
	return session.Session{
		UID: "100024680", MID: "mid_syn", Stoken: "v2_syn",
		DeviceID: "device-syn", DeviceFP: "fp0123456789a",
	}
}

var testRoles = []role.Role{
	{GameBiz: "hk4e_cn", GameUID: "770000001", Region: "cn_gf01", Nickname: "旅行者syn", RegionName: "天空岛"},
	{GameBiz: "nap_cn", GameUID: "770000001", Region: "prod_gf_cn", Nickname: "绳匠syn", RegionName: "零号大厅"},
}

func TestResolveSelector(t *testing.T) {
	// 空选择器 + 唯一角色 → 自动使用。
	single := testRoles[:1]
	r, oerr := ResolveSelector(single, "")
	if oerr != nil || r.GameUID != "770000001" {
		t.Fatalf("唯一角色应自动选择: %v %v", r, oerr)
	}
	// 空选择器 + 多角色 → 列出候选并退出，不静默挑选。
	_, oerr = ResolveSelector(testRoles, "")
	if oerr == nil || oerr.Code != output.CodeInputInvalid {
		t.Fatalf("多角色应要求选择: %v", oerr)
	}
	if oerr.Exit != output.ExitInput {
		t.Errorf("退出码 = %d", oerr.Exit)
	}
	// 组合选择器。
	r, oerr = ResolveSelector(testRoles, "nap_cn:770000001:prod_gf_cn")
	if oerr != nil || r.Region != "prod_gf_cn" {
		t.Fatalf("组合选择器: %v %v", r, oerr)
	}
	// 短选择器不唯一 → 候选。
	_, oerr = ResolveSelector(testRoles, "770000001")
	if oerr == nil || !contains(oerr.Message, "nap_cn:770000001:prod_gf_cn") {
		t.Fatalf("不唯一短选择器应列候选: %v", oerr)
	}
	// 无匹配。
	_, oerr = ResolveSelector(testRoles, "999")
	if oerr == nil {
		t.Fatal("无匹配应失败")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestFavoriteList_QueryAndCursor(t *testing.T) {
	var gotQuery string
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/painter/api/userFavouritePostList" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if calls == 1 {
			gotQuery = r.URL.RawQuery
			fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
				{"post":{"post_id":"f1","subject":"收藏一","view_type":2,"created_at":1700000000,"author":{"uid":42,"nickname":"原作者"}}},
				{"post":{"post_id":"f2","subject":"收藏二","view_type":1}}
			],"is_last":false,"next_offset":"offset-9"}}`)
			return
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"post":{"post_id":"f3","subject":"收藏三","view_type":2}}
		],"is_last":true}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	page, oerr := New(c).List(context.Background(), testSess(), testRoles[0], ListOptions{Limit: 3})
	if oerr != nil {
		t.Fatalf("List: %v", oerr)
	}
	// 首页参数：aid/offset(空)/size/game_uid/game_region。
	if gotQuery != "aid=100024680&game_region=cn_gf01&game_uid=770000001&offset=&size=20" {
		t.Errorf("首页 query = %s", gotQuery)
	}
	if len(page.Items) != 3 || page.Items[2].PostID != "f3" {
		t.Errorf("items = %+v", page.Items)
	}
	if page.HasMore {
		t.Error("is_last=true 不应有更多")
	}
}

func TestFillFull_PreservesOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("post_id")
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"post":{"post_id":"`+id+`","subject":"详情`+id+`","view_type":2}}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	getter := post.New(c)
	items := []post.Summary{{PostID: "a"}, {PostID: "b"}, {PostID: "c"}}
	details, oerr := FillFull(context.Background(), testSess(), items, getter, 2)
	if oerr != nil {
		t.Fatalf("FillFull: %v", oerr)
	}
	for i, want := range []string{"a", "b", "c"} {
		if details[i].PostID != want {
			t.Errorf("details[%d].PostID = %s, want %s", i, details[i].PostID, want)
		}
	}
}

func TestFillFull_FailsOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("post_id") == "b" {
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"post":{"post_id":"x","subject":"s","view_type":2}}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	items := []post.Summary{{PostID: "a"}, {PostID: "b"}}
	if _, oerr := FillFull(context.Background(), testSess(), items, post.New(c), 2); oerr == nil {
		t.Fatal("任一条失败应整体失败")
	}
}
