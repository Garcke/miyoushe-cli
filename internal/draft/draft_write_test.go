package draft

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/session"
)

// writeServer 捕获写请求的头与 body，并按脚本应答。
type writeServer struct {
	saveCount  int
	deleteBody string
	saveHeader http.Header
	saveBody   string
	deleteCnt  int
}

func newWriteServer(t *testing.T, st *writeServer) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/post/api/draft/save", func(w http.ResponseWriter, r *http.Request) {
		st.saveCount++
		st.saveHeader = r.Header.Clone()
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		st.saveBody = string(body)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"draft_id":"2100786262362804224"}}`)
	})
	mux.HandleFunc("/post/api/draft/delete", func(w http.ResponseWriter, r *http.Request) {
		st.deleteCnt++
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		st.deleteBody = string(body)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{}}`)
	})
	return httptest.NewServer(mux)
}

func testSession() session.Session {
	return session.Session{
		UID: "82463740", MID: "mid_test", Stoken: "v2_test",
		DeviceID: "dev-test", DeviceFP: "fp13test00001",
	}
}

func TestDraftSave_HappyPath(t *testing.T) {
	st := &writeServer{}
	srv := newWriteServer(t, st)
	t.Cleanup(srv.Close)
	c, _ := api.New(srv.URL)
	s := New(c)

	did, oerr := s.Save(context.Background(), testSession(), SaveOptions{
		Subject:           "CLI 契约实测草稿",
		ContentHTML:       "<p>正文</p>",
		StructuredContent: `[{"insert":"正文\n"}]`,
		ForumID:           "26",
		ViewType:          1,
		Cover:             "",
		GIDs:              2,
	})
	if oerr != nil {
		t.Fatalf("Save: %v", oerr)
	}
	if did != "2100786262362804224" {
		t.Errorf("draft_id = %s", did)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(st.saveBody), &body); err != nil {
		t.Fatalf("body 非法: %v", err)
	}
	// 契约锁定（实测 §4）：新建不带 draft_id；block_reply_img 省略（0 值）；
	// structured_content 是 JSON 字符串形态；forum_id 字符串。
	for _, forbidden := range []string{"draft_id", "block_reply_img", "link_card_list"} {
		if _, ok := body[forbidden]; ok {
			t.Errorf("新建草稿 body 不应包含 %s", forbidden)
		}
	}
	if body["structured_content"] != `[{"insert":"正文\n"}]` {
		t.Errorf("structured_content 应为 JSON 字符串: %v", body["structured_content"])
	}
	if body["forum_id"] != "26" || body["gids"] != float64(2) || body["view_type"] != float64(1) {
		t.Errorf("基础字段: %v", body)
	}
	if body["is_profit"] != false || body["is_original"] != float64(0) {
		t.Errorf("is_profit/is_original: %v", body)
	}
	if st.saveHeader.Get("Cookie") == "" || st.saveHeader.Get("DS") == "" {
		t.Error("写请求必须带 Cookie 与 DS")
	}
}

func TestDraftSave_BlockReplyImgIntOnly(t *testing.T) {
	// 实测 §4.1：block_reply_img 只能是 JSON number；设 1 时以 int 发送。
	st := &writeServer{}
	srv := newWriteServer(t, st)
	t.Cleanup(srv.Close)
	c, _ := api.New(srv.URL)
	s := New(c)

	if _, oerr := s.Save(context.Background(), testSession(), SaveOptions{
		Subject: "t", ContentHTML: "<p>t</p>", StructuredContent: "[]",
		ForumID: "26", ViewType: 1, GIDs: 2, BlockReplyImg: 1,
	}); oerr != nil {
		t.Fatalf("Save: %v", oerr)
	}
	if !strings.Contains(st.saveBody, `"block_reply_img":1`) {
		t.Errorf("block_reply_img 应为 int 1: %s", st.saveBody)
	}
}

func TestDraftSave_InputValidation(t *testing.T) {
	cases := map[string]SaveOptions{
		"缺标题":     {ContentHTML: "x", ForumID: "26", ViewType: 1, GIDs: 2},
		"缺 forum": {Subject: "t", ContentHTML: "x", ViewType: 1, GIDs: 2},
		"缺 vt":    {Subject: "t", ContentHTML: "x", ForumID: "26", GIDs: 2},
		"缺 gids":  {Subject: "t", ContentHTML: "x", ForumID: "26", ViewType: 1},
	}
	svc := New(nil)
	for name, opts := range cases {
		if _, oerr := svc.Save(context.Background(), testSession(), opts); oerr == nil {
			t.Errorf("%s 应拒绝", name)
		}
	}
}

func TestDraftDelete_HappyPath(t *testing.T) {
	st := &writeServer{}
	srv := newWriteServer(t, st)
	t.Cleanup(srv.Close)
	c, _ := api.New(srv.URL)
	s := New(c)

	if oerr := s.Delete(context.Background(), testSession(), "2100786262362804224"); oerr != nil {
		t.Fatalf("Delete: %v", oerr)
	}
	if st.deleteBody != `{"draft_id":"2100786262362804224"}` {
		t.Errorf("delete body = %s", st.deleteBody)
	}
	if oerr := s.Delete(context.Background(), testSession(), ""); oerr == nil {
		t.Error("空 draft-id 应拒绝")
	}
}
