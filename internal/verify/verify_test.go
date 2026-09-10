package verify

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/role"
	"mihoyo_cli/internal/session"
)

func testSess() session.Session {
	return session.Session{
		UID: "100024680", MID: "mid_syn", Stoken: "v2_syn",
		DeviceID: "device-syn", DeviceFP: "fp0123456789a",
	}
}

func TestVerify_Available(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[]}}`)
	}))
	defer srv.Close()
	checker := role.New(mustClient(t, srv))
	report, oerr := Check(context.Background(), testSess(), checker)
	if oerr != nil {
		t.Fatalf("Check: %v", oerr)
	}
	if !report.ServerAccepted || report.ProtocolProfile != "accepted" {
		t.Errorf("report = %+v", report)
	}
	states := map[string]string{}
	for _, c := range report.Capabilities {
		states[c.Name] = c.State
	}
	if states["read-account"] != StateAvailable {
		t.Errorf("read-account = %s", states["read-account"])
	}
	// retcode 0 不推断为可写。
	for _, name := range []string{"write-post", "upload-image", "upload-video"} {
		if states[name] != StateNotImplemented {
			t.Errorf("%s = %s, want not_implemented", name, states[name])
		}
	}
}

func TestVerify_AccountDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":1001,"message":"权限不足"}`)
	}))
	defer srv.Close()
	report, oerr := Check(context.Background(), testSess(), role.New(mustClient(t, srv)))
	if oerr != nil {
		t.Fatalf("权限不足属于报告结果，不应报错: %v", oerr)
	}
	if !report.ServerAccepted || report.ProtocolProfile != "accepted" {
		t.Errorf("report = %+v", report)
	}
	for _, c := range report.Capabilities {
		if c.Name == "read-account" && c.State != StateAccountDenied {
			t.Errorf("read-account = %s", c.State)
		}
	}
}

func TestVerify_AuthInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":-100,"message":"登录失效"}`)
	}))
	defer srv.Close()
	_, oerr := Check(context.Background(), testSess(), role.New(mustClient(t, srv)))
	if oerr == nil || oerr.Code != output.CodeAuthInvalid {
		t.Fatalf("应返回 AUTH_INVALID: %v", oerr)
	}
}

func TestVerify_ProfileRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":-10001,"message":"invalid request"}`)
	}))
	defer srv.Close()
	_, oerr := Check(context.Background(), testSess(), role.New(mustClient(t, srv)))
	if oerr == nil || oerr.Code != output.CodeProtocolRejected {
		t.Fatalf("应返回 PROTOCOL_PROFILE_REJECTED: %v", oerr)
	}
}

func mustClient(t *testing.T, srv *httptest.Server) *api.Client {
	t.Helper()
	c, err := api.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
