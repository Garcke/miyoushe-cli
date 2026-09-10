package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"mihoyo_cli/internal/output"
)

func envJSON(retcode int, message string, data string) string {
	return fmt.Sprintf(`{"retcode":%d,"message":%q,"data":%s}`, retcode, message, data)
}

func TestDo_ReturnsData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/some/api" {
			t.Errorf("path = %s", r.URL.Path)
		}
		fmt.Fprint(w, envJSON(0, "OK", `{"list":[1,2]}`))
	}))
	defer srv.Close()
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	raw, oerr := c.Do(context.Background(), "GET", "/some/api", nil, nil, http.Header{})
	if oerr != nil {
		t.Fatalf("Do: %v", oerr)
	}
	if string(raw) != `{"list":[1,2]}` {
		t.Errorf("data = %s", raw)
	}
}

func TestDo_RetcodeErrors(t *testing.T) {
	cases := []struct {
		rc      int
		message string
		code    string
		exit    int
	}{
		{-100, "登录失效", output.CodeAuthInvalid, output.ExitAuth},
		{-10001, "invalid request", output.CodeProtocolRejected, output.ExitAuth},
		{-3005, "参数不合法", output.CodeRemoteRejected, output.ExitRejected},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, envJSON(tc.rc, tc.message, `null`))
		}))
		c, _ := New(srv.URL)
		_, oerr := c.Do(context.Background(), "GET", "/x", nil, nil, http.Header{})
		srv.Close()
		if oerr == nil {
			t.Fatalf("retcode %d: 期望错误", tc.rc)
		}
		if oerr.Code != tc.code || oerr.Exit != tc.exit {
			t.Errorf("retcode %d: code=%s exit=%d, want %s/%d", tc.rc, oerr.Code, oerr.Exit, tc.code, tc.exit)
		}
		if oerr.Retcode != tc.rc {
			t.Errorf("Retcode = %d, want %d", oerr.Retcode, tc.rc)
		}
	}
}

func TestDo_MissingRetcode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"message":"OK","data":{}}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	if _, oerr := c.Do(context.Background(), "GET", "/x", nil, nil, http.Header{}); oerr == nil ||
		oerr.Code != output.CodeRemoteRejected {
		t.Fatalf("缺少 retcode 应失败: %v", oerr)
	}
}

func TestDo_Oversize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("x"))
		for i := 0; i < 1<<20; i++ {
			w.Write([]byte("yyyyyyyy"))
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	if _, oerr := c.Do(context.Background(), "GET", "/x", nil, nil, http.Header{}); oerr == nil ||
		!strings.Contains(oerr.Message, "上限") {
		t.Fatalf("超大响应应失败: %v", oerr)
	}
}

func TestDo_HTTPStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	_, oerr := c.Do(context.Background(), "GET", "/x", nil, nil, http.Header{})
	if oerr == nil || !strings.Contains(oerr.Message, "500") {
		t.Fatalf("HTTP 500 应失败: %v", oerr)
	}
}

func TestDo_RedirectRefused(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, envJSON(0, "OK", `{}`))
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/escape?secret=1", http.StatusFound)
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	_, oerr := c.Do(context.Background(), "GET", "/x", nil, nil, http.Header{})
	if oerr == nil {
		t.Fatal("重定向应被拒绝")
	}
	if strings.Contains(oerr.Message, "escape") || strings.Contains(oerr.Message, target.URL) {
		t.Errorf("错误消息泄露重定向目标: %s", oerr.Message)
	}
}

func TestDo_NoURLLeakOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	q := url.Values{"ticket": []string{"super_secret_ticket"}}
	_, oerr := c.Do(context.Background(), "GET", "/x", q, nil, http.Header{})
	if oerr == nil {
		t.Fatal("期望错误")
	}
	if strings.Contains(oerr.Message, "super_secret_ticket") {
		t.Errorf("错误消息泄露 ticket: %s", oerr.Message)
	}
}

func TestDo_HeadersAndBodyPassed(t *testing.T) {
	var gotDS, gotCookie, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDS = r.Header.Get("DS")
		gotCookie = r.Header.Get("Cookie")
		gotCT = r.Header.Get("Content-Type")
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		gotBody = string(buf)
		fmt.Fprint(w, envJSON(0, "OK", `{}`))
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	h := http.Header{}
	h.Set("DS", "123,a,b")
	h.Set("Cookie", "stuid=1; stoken=s; mid=m;")
	_, oerr := c.Do(context.Background(), "POST", "/x", nil, []byte(`{"k":1}`), h)
	if oerr != nil {
		t.Fatalf("Do: %v", oerr)
	}
	if gotDS != "123,a,b" || gotCookie != "stuid=1; stoken=s; mid=m;" {
		t.Errorf("headers: DS=%q Cookie=%q", gotDS, gotCookie)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if gotBody != `{"k":1}` {
		t.Errorf("body = %q", gotBody)
	}
}

func TestDo_TimeoutIsTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		fmt.Fprint(w, envJSON(0, "OK", `{}`))
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	c.RequestTimeout = 30 * time.Millisecond
	_, oerr := c.Do(context.Background(), "GET", "/x", nil, nil, http.Header{})
	if oerr == nil || !oerr.Transport {
		t.Fatalf("超时应标记 Transport: %+v", oerr)
	}
}

func TestDo_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	_, oerr := c.Do(ctx, "GET", "/x", nil, nil, http.Header{})
	if oerr == nil || oerr.Code != output.CodeCancelled {
		t.Fatalf("取消应返回 CANCELLED: %+v", oerr)
	}
}

func TestListMeta(t *testing.T) {
	last := true
	m := ListMeta{IsLast: &last, NextOffset: "42", LastID: "9"}
	if m.Cursor() != "42" || m.HasMore() {
		t.Errorf("ListMeta = %+v", m)
	}
	m2 := ListMeta{LastID: "9"}
	if m2.Cursor() != "9" || !m2.HasMore() {
		t.Errorf("ListMeta fallback = %+v", m2)
	}
}
