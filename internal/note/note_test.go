package note

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/role"
	"mihoyo_cli/internal/session"
)

func testSession() session.Session {
	return session.Session{
		UID: "82463740", MID: "mid_test", Stoken: "v2_stoken_test",
		DeviceID: "2cfb31e8-5e0a-32c6-8640-58f3312746d3", DeviceFP: "38d81bc256d69",
	}
}

func TestGetLToken_QueryAndParse(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/account/auth/api/getLTokenBySToken" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"ltoken":"o3MveSpFMAhP5Q6Zs4F1oeOghc7YWJcgfd6A2FQk"}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := &Service{Passport: c}

	lt, oerr := s.GetLToken(context.Background(), testSession())
	if oerr != nil {
		t.Fatalf("GetLToken: %v", oerr)
	}
	if lt != "o3MveSpFMAhP5Q6Zs4F1oeOghc7YWJcgfd6A2FQk" {
		t.Errorf("ltoken = %q", lt)
	}
	qv, _ := url.ParseQuery(gotQuery)
	if qv.Get("stuid") != "82463740" || qv.Get("stoken") != "v2_stoken_test" || qv.Get("mid") != "mid_test" {
		t.Errorf("query = %s", gotQuery)
	}
}

func TestFetch_Genshin_ContractAndSummary(t *testing.T) {
	var gotQuery string
	var gotHeader http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/game_record/app/genshin/api/dailyNote" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		gotHeader = r.Header.Clone()
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"current_resin":200,"max_resin":200,
			"resin_recovery_time":"0","finished_task_num":4,"total_task_num":4,
			"remain_resin_discount_num":3,"current_expedition_num":2,"max_expedition_num":5,
			"current_home_coin":1500,"max_home_coin":2400}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := &Service{Record: c}

	n, oerr := s.Fetch(context.Background(), testSession(), "lt_test",
		role.Role{GameBiz: "hk4e_cn", Region: "cn_gf01", GameUID: "116084021"})
	if oerr != nil {
		t.Fatalf("Fetch: %v", oerr)
	}
	if !n.Supported || n.Kind != "genshin" {
		t.Fatalf("note = %+v", n)
	}
	// 契约锁定：client_type=5 + DS(4X 盐、query 绑定) + LToken Cookie + server/role_id。
	if gotHeader.Get("x-rpc-client_type") != "5" {
		t.Errorf("client_type = %s", gotHeader.Get("x-rpc-client_type"))
	}
	qv, _ := url.ParseQuery(gotQuery)
	if qv.Get("server") != "cn_gf01" || qv.Get("role_id") != "116084021" {
		t.Errorf("query = %s", gotQuery)
	}
	ds := gotHeader.Get("DS")
	parts := strings.SplitN(ds, ",", 3)
	if len(parts) != 3 {
		t.Fatalf("DS 格式: %s", ds)
	}
	want := md5.Sum([]byte("salt=" + protocol.SaltWeb4X + "&t=" + parts[0] + "&r=" + parts[1] + "&b=&q=" + gotQuery))
	if parts[2] != hex.EncodeToString(want[:]) {
		t.Errorf("DS2 未绑定 query: %s (query=%s)", ds, gotQuery)
	}
	ck := gotHeader.Get("Cookie")
	if !strings.Contains(ck, "ltoken=") || !strings.Contains(ck, "ltuid=") {
		t.Errorf("Cookie = %q", ck)
	}
	if strings.Contains(ck, "stoken=") {
		t.Error("便签不应携带 stoken（实测 SToken 直调 10001）")
	}
	if len(n.Summary) < 4 || !strings.Contains(n.Summary[0], "200/200") {
		t.Errorf("summary = %v", n.Summary)
	}
	if !strings.Contains(strings.Join(n.Summary, "|"), "4/4") {
		t.Errorf("summary 应含每日委托: %v", n.Summary)
	}
}

func TestFetch_ZZZ_ContractAndSummary(t *testing.T) {
	var gotHeader http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/event/game_record_zzz/api/zzz/note" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotHeader = r.Header.Clone()
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{
			"energy":{"progress":{"max":240,"current":180},"restore":21400},
			"vitality":{"max":400,"current":0},
			"bounty_commission":{"num":0,"total":8000},
			"weekly_task":{"cur_point":200,"max_point":2100},
			"member_card":{"is_open":true}}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := &Service{Record: c}

	n, oerr := s.Fetch(context.Background(), testSession(), "lt_test",
		role.Role{GameBiz: "nap_cn", Region: "prod_gf_cn", GameUID: "19651717"})
	if oerr != nil {
		t.Fatalf("Fetch: %v", oerr)
	}
	if !n.Supported || n.Kind != "zzz" {
		t.Fatalf("note = %+v", n)
	}
	if gotHeader.Get("x-rpc-client_type") != "5" {
		t.Errorf("client_type = %s", gotHeader.Get("x-rpc-client_type"))
	}
	joined := strings.Join(n.Summary, "|")
	if !strings.Contains(joined, "180/240") || !strings.Contains(joined, "200/2100") {
		t.Errorf("summary = %v", n.Summary)
	}
	if !strings.Contains(joined, "active") {
		t.Errorf("会员卡状态: %v", n.Summary)
	}
}

func TestFetch_UnsupportedGame(t *testing.T) {
	// 未实测的游戏不猜测端点：返回 Supported=false + 说明，不发请求。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("不支持的游戏不应发起请求")
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := &Service{Record: c}

	n, oerr := s.Fetch(context.Background(), testSession(), "lt_test",
		role.Role{GameBiz: "hkrpg_cn", Region: "prod_gf_cn", GameUID: "123"})
	if oerr != nil {
		t.Fatalf("Fetch: %v", oerr)
	}
	if n.Supported || n.Reason == "" {
		t.Errorf("note = %+v", n)
	}
	if !strings.Contains(n.Reason, "hkrpg_cn") {
		t.Errorf("reason = %q", n.Reason)
	}
}

func TestFetch_InputValidation(t *testing.T) {
	c, _ := api.New("http://127.0.0.1:1")
	s := &Service{Record: c}
	if _, oerr := s.Fetch(context.Background(), testSession(), "", role.Role{GameBiz: "hk4e_cn", Region: "cn_gf01", GameUID: "1"}); oerr == nil {
		t.Error("空 ltoken 应拒绝")
	}
	if _, oerr := s.Fetch(context.Background(), testSession(), "lt", role.Role{GameBiz: "hk4e_cn"}); oerr == nil {
		t.Error("缺 region/uid 应拒绝")
	}
}

func TestHostRecordConstant(t *testing.T) {
	if HostRecord != "api-takumi-record.mihoyo.com" {
		t.Errorf("HostRecord = %q", HostRecord)
	}
}

func TestFetch_RecordDeviceOverride(t *testing.T) {
	// 记录域设备覆盖：配置后请求头使用覆盖设备，而非会话设备。
	var gotHeader http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"current_resin":160,"max_resin":200,"resin_recovery_time":"120"}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := &Service{
		Record:       c,
		RecordDevice: &protocol.DeviceContext{DeviceID: "2cfb31e8-5e0a-32c6-8640-58f3312746d3", DeviceFP: "38d81bc256d69"},
	}

	if _, oerr := s.Fetch(context.Background(), testSession(), "lt_test",
		role.Role{GameBiz: "hk4e_cn", Region: "cn_gf01", GameUID: "116084021"}); oerr != nil {
		t.Fatalf("Fetch: %v", oerr)
	}
	if gotHeader.Get("x-rpc-device_id") != "2cfb31e8-5e0a-32c6-8640-58f3312746d3" ||
		gotHeader.Get("x-rpc-device_fp") != "38d81bc256d69" {
		t.Errorf("应使用覆盖设备: id=%s fp=%s", gotHeader.Get("x-rpc-device_id"), gotHeader.Get("x-rpc-device_fp"))
	}
}

func TestFetch_RiskControlGuidance(t *testing.T) {
	// 设备风控（实测 1034/5003/10035/10041）→ 错误消息给出 record_device.json 指引。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":1034,"message":""}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := &Service{Record: c}

	_, oerr := s.Fetch(context.Background(), testSession(), "lt_test",
		role.Role{GameBiz: "hk4e_cn", Region: "cn_gf01", GameUID: "116084021"})
	if oerr == nil || !strings.Contains(oerr.Message, "risk control") || !strings.Contains(oerr.Message, "record_device.json") {
		t.Fatalf("风控应给出指引: %+v", oerr)
	}
	if oerr.Retcode != 1034 {
		t.Errorf("retcode = %d", oerr.Retcode)
	}
}
