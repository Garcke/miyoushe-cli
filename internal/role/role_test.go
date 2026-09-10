package role

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/session"
)

const roleFixture = `{"retcode":0,"message":"OK","data":{"list":[
  {"game_biz":"hk4e_cn","game_uid":"770000001","region":"cn_gf01","nickname":"旅行者syn","level":60,"is_chosen":true,"region_name":"天空岛","is_official":true},
  {"game_biz":"nap_cn","role_id":"880000002","region":"prod_gf_cn","nickname":"绳匠syn","level":40,"is_chosen":false,"region_name":"零号大厅","is_official":false}
]}}`

func testSession() session.Session {
	return session.Session{
		UID: "100024680", MID: "mid_syn", Stoken: "v2_syn",
		DeviceID: "device-syn", DeviceFP: "fp0123456789a",
	}
}

func TestRoleList_ParsesAndSendsHeaders(t *testing.T) {
	var gotCookie, gotDS, gotClientType string
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/binding/api/getUserGameRolesByStoken" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("method = %s", r.Method)
		}
		gotCookie = r.Header.Get("Cookie")
		gotDS = r.Header.Get("DS")
		gotClientType = r.Header.Get("X-Rpc-Client_type")
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, roleFixture)
	}))
	defer srv.Close()

	c, _ := api.New(srv.URL)
	roles, oerr := New(c).List(context.Background(), testSession(), "")
	if oerr != nil {
		t.Fatalf("List: %v", oerr)
	}
	// Cookie 三件套精确匹配；DS1 无 query/body 参与。
	if gotCookie != "stuid=100024680; stoken=v2_syn; mid=mid_syn;" {
		t.Errorf("Cookie = %q", gotCookie)
	}
	if gotClientType != "2" {
		t.Errorf("client_type = %s", gotClientType)
	}
	if gotDS == "" || len(protocol.DSBBSHeader(protocol.SaltBBS, 1, "aaaaaa")) == 0 {
		t.Errorf("DS 缺失")
	}
	if gotQuery != "" {
		t.Errorf("无 game_biz 时不应有 query: %s", gotQuery)
	}
	if len(roles) != 2 {
		t.Fatalf("roles = %+v", roles)
	}
	// game_uid 优先，role_id 兜底。
	if roles[0].GameUID != "770000001" || roles[0].Region != "cn_gf01" || !roles[0].IsChosen {
		t.Errorf("roles[0] = %+v", roles[0])
	}
	if roles[1].GameUID != "880000002" || roles[1].GameBiz != "nap_cn" {
		t.Errorf("roles[1] = %+v", roles[1])
	}
}

func TestRoleList_GameBizFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("game_biz"); q != "hk4e_cn" {
			t.Errorf("game_biz = %q", q)
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[]}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	roles, oerr := New(c).List(context.Background(), testSession(), "hk4e_cn")
	if oerr != nil {
		t.Fatalf("List: %v", oerr)
	}
	if roles == nil || len(roles) != 0 {
		t.Errorf("空列表应为空切片: %+v", roles)
	}
}

func TestRoleList_AuthInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":-100,"message":"登录失效"}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	_, oerr := New(c).List(context.Background(), testSession(), "")
	if oerr == nil || oerr.Code != "AUTH_INVALID" {
		t.Fatalf("应返回 AUTH_INVALID: %v", oerr)
	}
}
