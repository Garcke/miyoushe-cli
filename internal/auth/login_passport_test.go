package auth

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/store"
)

// passportServerState 控制 ma-cn-passport 扫码测试服务器的脚本化行为。
type passportServerState struct {
	createCount atomic.Int32
	queryCount  atomic.Int32
	createBody  string
	queryBody   string
	createHdr   http.Header
	queryHeader http.Header
	queryResps  []string // 逐次响应；耗尽后复用最后一个
}

func (s *passportServerState) nextQuery() string {
	idx := int(s.queryCount.Load()) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s.queryResps) {
		idx = len(s.queryResps) - 1
	}
	return s.queryResps[idx]
}

func newPassportServer(t *testing.T, st *passportServerState) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// loadDevice 在无旧凭据时会请求 getFp。
	mux.HandleFunc("/device-fp/api/getFp", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"device_fp":"`+synFP+`","code":200,"msg":"ok"}}`)
	})

	mux.HandleFunc("/account/ma-cn-passport/app/createQRLogin", func(w http.ResponseWriter, r *http.Request) {
		st.createCount.Add(1)
		st.createHdr = r.Header.Clone()
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		st.createBody = string(body)
		if r.Header.Get("x-rpc-app_id") != protocol.AppIDPassport {
			t.Errorf("建码缺少 x-rpc-app_id: %v", r.Header)
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"url":"https://user.mihoyo.com/login-platform/mobile.html?expire=1893456000&tk=`+ticketSyn+strconv.Itoa(int(st.createCount.Load()))+`&token_types=1#/login/qr","ticket":"`+ticketSyn+strconv.Itoa(int(st.createCount.Load()))+`"}}`)
	})

	mux.HandleFunc("/account/ma-cn-passport/app/queryQRLoginStatus", func(w http.ResponseWriter, r *http.Request) {
		st.queryCount.Add(1)
		st.queryHeader = r.Header.Clone()
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		st.queryBody = string(body)
		fmt.Fprint(w, st.nextQuery())
	})

	return httptest.NewServer(mux)
}

func newPassportService(t *testing.T, st *passportServerState) (*Service, *store.Store, *fakeRenderer) {
	t.Helper()
	srv := newPassportServer(t, st)
	t.Cleanup(srv.Close)
	dir := privateTestDir(t)
	sto := &store.Store{Dir: dir}
	client, err := api.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		Store:          sto,
		FPClient:       client,
		PassportClient: client,
		Now:            time.Now,
	}
	render := &fakeRenderer{pngPath: "png-path"}
	return svc, sto, render
}

func passportStatusResp(stat string) string {
	return fmt.Sprintf(`{"retcode":0,"message":"OK","data":{"status":%q,"app_id":"bll8iq97cem8","client_type":2,"created_at":"1789465458","scanned_at":"0","tokens":[],"user_info":null,"realname_info":null,"need_realperson":false,"ext":"","scan_game_biz":""}}`, stat)
}

func passportConfirmedRaw() string {
	return `{"retcode":0,"message":"OK","data":{"status":"Confirmed","app_id":"bll8iq97cem8","client_type":2,"created_at":"1789465458","scanned_at":"1789465506","tokens":[{"token_type":1,"token":"` + synSToken + `"}],"user_info":{"aid":` + synUID + `,"mid":"` + synMID + `"},"realname_info":{"required":false},"need_realperson":false,"ext":"","scan_game_biz":"bbs_cn"}}`
}

func TestPassportLogin_HappyPath(t *testing.T) {
	st := &passportServerState{
		queryResps: []string{passportStatusResp("Created"), passportStatusResp("Scanned"), passportConfirmedRaw()},
	}
	svc, sto, render := newPassportService(t, st)
	var stages []string
	creds, oerr := svc.LoginPassport(context.Background(), fastCfg(), render, func(s string) { stages = append(stages, s) })
	if oerr != nil {
		t.Fatalf("LoginPassport: %v", oerr)
	}
	if creds.Stoken != synSToken || creds.UID != synUID || creds.MID != synMID {
		t.Errorf("凭据内容: %+v", creds)
	}
	if creds.TokenType != 1 || creds.TokenKind != "stoken" || creds.Flow != store.FlowV2 {
		t.Errorf("凭据元数据: %+v", creds)
	}
	if !strings.Contains(stagesStr(stages), "waiting_scan") || !strings.Contains(stagesStr(stages), "waiting_confirm") {
		t.Errorf("阶段序列缺状态推进: %v", stages)
	}
	if stages[len(stages)-1] != "saving" {
		t.Errorf("最后阶段 = %s", stages[len(stages)-1])
	}
	if st.createCount.Load() != 1 || len(render.contents) != 1 {
		t.Errorf("建码/渲染次数: %d/%d", st.createCount.Load(), len(render.contents))
	}
	// 渲染内容必须是建码返回的 URL（含 token_types=1）。
	if !strings.Contains(render.contents[0], "token_types=1") || !strings.Contains(render.contents[0], ticketSyn) {
		t.Errorf("渲染内容 = %s", render.contents[0])
	}
	// 建码请求体恒为 "{}"，DS 与之绑定。
	if st.createBody != "{}" {
		t.Errorf("建码 body = %s", st.createBody)
	}
	assertPassportDS(t, st.createHdr, "{}")
	// 轮询请求体只含 ticket，DS 与之绑定。
	var body map[string]string
	wantTicket := ticketSyn + strconv.Itoa(int(st.createCount.Load()))
	if err := json.Unmarshal([]byte(st.queryBody), &body); err != nil || body["ticket"] != wantTicket {
		t.Errorf("轮询 body = %s, want ticket %s", st.queryBody, wantTicket)
	}
	assertPassportDS(t, st.queryHeader, st.queryBody)
	data, _ := os.ReadFile(sto.Path())
	if !strings.Contains(string(data), synSToken) {
		t.Error("凭据文件缺少 SToken")
	}
}

// assertPassportDS 校验 passport DS 与 body 字节绑定、r 含大写。
func assertPassportDS(t *testing.T, h http.Header, wantBody string) {
	t.Helper()
	ds := h.Get("DS")
	if ds == "" {
		t.Fatal("缺少 DS 头")
	}
	parts := strings.SplitN(ds, ",", 3)
	if len(parts) != 3 {
		t.Fatalf("DS 格式: %s", ds)
	}
	if !regexpMatch6Mixed(parts[1]) {
		t.Errorf("passport DS r 应含大写字母: %s", parts[1])
	}
	sum := md5.Sum([]byte("salt=" + protocol.SaltPassport + "&t=" + parts[0] + "&r=" + parts[1] + "&b=" + wantBody + "&q="))
	if parts[2] != hex.EncodeToString(sum[:]) {
		t.Errorf("passport DS 未绑定 body: %s (body=%s)", ds, wantBody)
	}
}

func stagesStr(stages []string) string { return strings.Join(stages, ",") }

func TestPassportLogin_QRExpiredRebuilds(t *testing.T) {
	st := &passportServerState{
		queryResps: []string{
			`{"retcode":-3501,"message":"二维码已失效，请刷新后重新扫描","data":null}`,
			passportConfirmedRaw(),
		},
	}
	svc, _, render := newPassportService(t, st)
	creds, oerr := svc.LoginPassport(context.Background(), fastCfg(), render, nil)
	if oerr != nil {
		t.Fatalf("LoginPassport: %v", oerr)
	}
	if st.createCount.Load() != 2 {
		t.Errorf("失效后应重建二维码，create 次数 = %d", st.createCount.Load())
	}
	if len(render.contents) != 2 || render.contents[0] == render.contents[1] {
		t.Error("重建的二维码必须使用新内容")
	}
	if creds.Stoken != synSToken {
		t.Errorf("SToken: %s", creds.Stoken)
	}
}

func TestPassportLogin_RejectsWrongTokenType(t *testing.T) {
	st := &passportServerState{
		queryResps: []string{
			`{"retcode":0,"message":"OK","data":{"status":"Confirmed","tokens":[{"token_type":4,"token":"v2_lt"}],"user_info":{"aid":` + synUID + `,"mid":"` + synMID + `"}}}`,
		},
	}
	svc, sto, _ := newPassportService(t, st)
	_, oerr := svc.LoginPassport(context.Background(), fastCfg(), &fakeRenderer{}, nil)
	if oerr == nil || !strings.Contains(oerr.Message, "缺少 SToken") {
		t.Fatalf("非 token_type=1 应拒绝: %+v", oerr)
	}
	if creds, _ := sto.Load(); creds != nil {
		t.Error("失败不得保存凭据")
	}
}

func TestPassportLogin_RejectsMissingAIDOrMID(t *testing.T) {
	cases := map[string]string{
		"缺 aid": `{"retcode":0,"message":"OK","data":{"status":"Confirmed","tokens":[{"token_type":1,"token":"` + synSToken + `"}],"user_info":{"aid":"","mid":"` + synMID + `"}}}`,
		"缺 mid": `{"retcode":0,"message":"OK","data":{"status":"Confirmed","tokens":[{"token_type":1,"token":"` + synSToken + `"}],"user_info":{"aid":` + synUID + `,"mid":""}}}`,
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			st := &passportServerState{queryResps: []string{resp}}
			svc, sto, _ := newPassportService(t, st)
			_, oerr := svc.LoginPassport(context.Background(), fastCfg(), &fakeRenderer{}, nil)
			if oerr == nil || !strings.Contains(oerr.Message, "确认响应缺少") {
				t.Fatalf("应因确认响应缺字段失败: %+v", oerr)
			}
			if creds, _ := sto.Load(); creds != nil {
				t.Error("失败不得保存凭据")
			}
		})
	}
}

func TestPassportLogin_UnknownStatusFails(t *testing.T) {
	st := &passportServerState{queryResps: []string{passportStatusResp("Whatever")}}
	svc, _, _ := newPassportService(t, st)
	_, oerr := svc.LoginPassport(context.Background(), fastCfg(), &fakeRenderer{}, nil)
	if oerr == nil || !strings.Contains(oerr.Message, "未知扫码状态") {
		t.Fatalf("未知状态应失败: %+v", oerr)
	}
}

func TestPassportLogin_TotalTimeout(t *testing.T) {
	st := &passportServerState{queryResps: []string{passportStatusResp("Created")}}
	svc, _, _ := newPassportService(t, st)
	cfg := Config{Timeout: 80 * time.Millisecond, PollInterval: 10 * time.Millisecond, RequestTimeout: 50 * time.Millisecond, MaxPollFails: 3}
	_, oerr := svc.LoginPassport(context.Background(), cfg, &fakeRenderer{}, nil)
	if oerr == nil || oerr.Code != output.CodeLoginTimeout {
		t.Fatalf("应返回 LOGIN_TIMEOUT: %+v", oerr)
	}
}

// passportFlakyTransport 对 queryQRLoginStatus 注入前 N 次 500 响应。
type passportFlakyTransport struct {
	inner http.RoundTripper
	fails int
	seen  atomic.Int32
}

func (f *passportFlakyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/queryQRLoginStatus") {
		if int(f.seen.Add(1)) <= f.fails {
			return &http.Response{StatusCode: 500, Body: http.NoBody, Header: http.Header{}}, nil
		}
	}
	return f.inner.RoundTrip(r)
}

func TestPassportLogin_PollRetriesThenSucceeds(t *testing.T) {
	st := &passportServerState{queryResps: []string{passportConfirmedRaw()}}
	srv := newPassportServer(t, st)
	t.Cleanup(srv.Close)
	sto := &store.Store{Dir: privateTestDir(t)}
	c, _ := api.New(srv.URL)
	c.HTTP = &http.Client{Transport: &passportFlakyTransport{inner: http.DefaultTransport, fails: 2}}
	svc := &Service{Store: sto, FPClient: c, PassportClient: c, Now: time.Now}

	creds, oerr := svc.LoginPassport(context.Background(), fastCfg(), &fakeRenderer{}, nil)
	if oerr != nil {
		t.Fatalf("两次瞬断后应成功: %v", oerr)
	}
	if creds.Stoken != synSToken {
		t.Errorf("SToken: %s", creds.Stoken)
	}
}

func TestPassportLogin_PollConsecutiveFailsAborts(t *testing.T) {
	st := &passportServerState{queryResps: []string{passportStatusResp("Created")}}
	srv := newPassportServer(t, st)
	t.Cleanup(srv.Close)
	sto := &store.Store{Dir: privateTestDir(t)}
	c, _ := api.New(srv.URL)
	c.HTTP = &http.Client{Transport: &passportFlakyTransport{inner: http.DefaultTransport, fails: 99}}
	svc := &Service{Store: sto, FPClient: c, PassportClient: c, Now: time.Now}

	_, oerr := svc.LoginPassport(context.Background(), fastCfg(), &fakeRenderer{}, nil)
	if oerr == nil || !strings.Contains(oerr.Message, "连续失败") {
		t.Fatalf("连续失败 3 次应中止: %+v", oerr)
	}
	if creds, _ := sto.Load(); creds != nil {
		t.Error("不得保存")
	}
}

func TestPassportLogin_RequiresPassportClient(t *testing.T) {
	svc := &Service{Store: &store.Store{Dir: privateTestDir(t)}, Now: time.Now}
	_, oerr := svc.LoginPassport(context.Background(), fastCfg(), &fakeRenderer{}, nil)
	if oerr == nil || !strings.Contains(oerr.Message, "PassportClient") {
		t.Fatalf("未配置 PassportClient 应报错: %+v", oerr)
	}
}

func TestPassportLogin_ReusesDeviceID(t *testing.T) {
	st := &passportServerState{queryResps: []string{passportConfirmedRaw()}}
	svc, sto, _ := newPassportService(t, st)
	old := store.NewCredentialsFlow(store.FlowV2, "old_uid", "old_mid", "v2_old_stoken", "existing-device", synFP, time.Now())
	if oerr := sto.Save(old); oerr != nil {
		t.Fatal(oerr)
	}
	if _, oerr := svc.LoginPassport(context.Background(), fastCfg(), &fakeRenderer{}, nil); oerr != nil {
		t.Fatalf("LoginPassport: %v", oerr)
	}
	creds, _ := sto.Load()
	if creds.DeviceID != "existing-device" {
		t.Errorf("应复用设备标识: %s", creds.DeviceID)
	}
}
