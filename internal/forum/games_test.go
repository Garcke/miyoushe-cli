package forum

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/session"
)

func TestGamesMeta_AnonymousParsed(t *testing.T) {
	var gotPath string
	var gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCookie = r.Header.Get("Cookie")
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"id":1,"name":"崩坏3","en_name":"bh3","op_name":"bh3","has_wiki":true,"main_color":"01C3FF"},
			{"id":2,"name":"原神","en_name":"ys","op_name":"hk4e","has_wiki":true,"main_color":"BDA575"},
			{"id":10,"name":"星布谷地","en_name":"planet","op_name":"hyg","has_wiki":false,"main_color":"FFFFFF"}
		]}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := New(c, session.Session{})

	games, oerr := s.GamesMeta(context.Background())
	if oerr != nil {
		t.Fatalf("GamesMeta: %v", oerr)
	}
	if gotPath != "/apihub/api/getGameList" {
		t.Errorf("path = %s", gotPath)
	}
	if gotCookie != "" {
		t.Errorf("匿名目录请求不得携带 Cookie: %q", gotCookie)
	}
	if len(games) != 3 || games[1].ENName != "ys" || !games[0].HasWiki {
		t.Errorf("games = %+v", games)
	}
}

func TestGamesMeta_ArrayShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":[{"id":"2","name":"原神","en_name":"ys"}]}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	games, oerr := New(c, session.Session{}).GamesMeta(context.Background())
	if oerr != nil || len(games) != 1 || games[0].GIDs() != "2" {
		t.Fatalf("数组形态解析: %+v %v", games, oerr)
	}
}

func TestGamesMeta_StructureErrors(t *testing.T) {
	cases := []string{
		`{"retcode":0,"message":"OK","data":{}}`,              // 空对象
		`{"retcode":0,"message":"OK","data":{"list":"nope"}}`, // 类型错误
		`{"retcode":0,"message":"OK","data":{"list":[{"name":"missing id"}]}}`,
	}
	for _, body := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, body)
		}))
		c, _ := api.New(srv.URL)
		_, oerr := New(c, session.Session{}).GamesMeta(context.Background())
		srv.Close()
		if oerr == nil || oerr.Code != output.CodeProtocolMismatch {
			t.Fatalf("结构不符应 PROTOCOL_MISMATCH: %v (body=%s)", oerr, body)
		}
	}
}

func TestGamesMeta_LegacyGIDsKeyFallback(t *testing.T) {
	// 旧样例使用 gids 键；主字段是上游实测的 id。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[{"gids":"7","name":"旧样例","en_name":"legacy"}]}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	games, oerr := New(c, session.Session{}).GamesMeta(context.Background())
	if oerr != nil || len(games) != 1 || games[0].GIDs() != "7" {
		t.Fatalf("gids 兜底解析: %+v %v", games, oerr)
	}
}
