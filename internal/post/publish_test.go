package post

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/session"
)

// publishServer 脚本化 releasePost/v2 与 deletePost。
type publishServer struct {
	releaseBody string
	releaseCnt  int
	deleteBody  string
	releaseResp string
}

func newPublishServer(t *testing.T, st *publishServer) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/post/api/releasePost/v2", func(w http.ResponseWriter, r *http.Request) {
		st.releaseCnt++
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		st.releaseBody = string(body)
		fmt.Fprint(w, st.releaseResp)
	})
	mux.HandleFunc("/post/api/deletePost", func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		st.deleteBody = string(body)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{}}`)
	})
	return httptest.NewServer(mux)
}

func TestPublish_Success(t *testing.T) {
	st := &publishServer{releaseResp: `{"retcode":0,"message":"OK","data":{"post_id":"78239787","post_review_id":0,"release_check_result":{"can_release":true}}}`}
	srv := newPublishServer(t, st)
	t.Cleanup(srv.Close)
	c, _ := api.New(srv.URL)
	s := New(c)

	res, oerr := s.Publish(context.Background(), session.Session{
		UID: "82463740", MID: "m", Stoken: "v2_t", DeviceID: "d", DeviceFP: "f",
	}, PublishOptions{
		Subject: "实测帖", ContentHTML: "<p>正文</p>",
		StructuredContent: `[{"insert":"正文\n"}]`,
		ForumID:           "948", GIDs: 9, ViewType: 1, DraftID: "2100786759675629568",
	})
	if oerr != nil {
		t.Fatalf("Publish: %v", oerr)
	}
	if !res.Allowed || res.PostID != "78239787" {
		t.Fatalf("发布结果: %+v", res)
	}
	if res.ReviewID != "" {
		t.Errorf("review_id=0 应规整为空: %q", res.ReviewID)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(st.releaseBody), &body); err != nil {
		t.Fatalf("body 非法: %v", err)
	}
	// 契约锁定（实测 §4）：完整 18 字段形态。
	for _, k := range []string{"is_original", "subject", "gids", "contribution_act", "f_forum_id",
		"uid", "topic_ids", "review_id", "is_profit", "is_pre_publication", "cover", "lottery",
		"forum_id", "draft_id", "structured_content", "link_card_ids", "view_type", "content"} {
		if _, ok := body[k]; !ok {
			t.Errorf("发布 body 缺少 %s", k)
		}
	}
	if body["f_forum_id"] != "948" || body["uid"] != "82463740" || body["draft_id"] != "2100786759675629568" {
		t.Errorf("关键取值: %v", body)
	}
}

func TestPublish_GatedByForumLevel(t *testing.T) {
	// 实测（酒馆 forum 26）：rc=0 + post_id=0 + post_review_id=0 + can_release=false。
	st := &publishServer{releaseResp: `{"retcode":0,"message":"OK","data":{"post_id":0,"post_review_id":0,` +
		`"release_check_result":{"can_release":false,"msg":"旅行者您好，原神版区等级达到要求才可在酒馆发帖~",` +
		`"bbs_lv":{"is_pass":false,"expect":2}}}}`}
	srv := newPublishServer(t, st)
	t.Cleanup(srv.Close)
	c, _ := api.New(srv.URL)
	s := New(c)

	res, oerr := s.Publish(context.Background(), session.Session{
		UID: "82463740", MID: "m", Stoken: "v2_t", DeviceID: "d", DeviceFP: "f",
	}, PublishOptions{
		Subject: "t", ContentHTML: "<p>t</p>", StructuredContent: "[]",
		ForumID: "26", GIDs: 2, ViewType: 1,
	})
	if oerr != nil {
		t.Fatalf("门槛拦截不应返回错误: %v", oerr)
	}
	if res.Allowed {
		t.Errorf("Allowed 应为 false: %+v", res)
	}
	if res.PostID != "" || res.ReviewID != "" {
		t.Errorf("拦截时不应有 post_id/review_id: %+v", res)
	}
	if !strings.Contains(res.GateMessage, "等级达到要求") {
		t.Errorf("GateMessage = %q", res.GateMessage)
	}
}

func TestPublish_UnknownShapeFails(t *testing.T) {
	st := &publishServer{releaseResp: `{"retcode":0,"message":"OK","data":{"post_id":0,"post_review_id":0}}`}
	srv := newPublishServer(t, st)
	t.Cleanup(srv.Close)
	c, _ := api.New(srv.URL)
	s := New(c)

	_, oerr := s.Publish(context.Background(), session.Session{
		UID: "u", MID: "m", Stoken: "s", DeviceID: "d", DeviceFP: "f",
	}, PublishOptions{Subject: "t", ContentHTML: "x", StructuredContent: "[]", ForumID: "1", GIDs: 2, ViewType: 1})
	if oerr == nil || !strings.Contains(oerr.Message, "结果未知") {
		t.Fatalf("无 post_id 且无门槛信息应报结果未知: %+v", oerr)
	}
}

func TestPublish_InputValidation(t *testing.T) {
	svc := New(nil)
	cases := map[string]PublishOptions{
		"缺标题":     {ContentHTML: "x", ForumID: "1", ViewType: 1, GIDs: 2},
		"缺 forum": {Subject: "t", ContentHTML: "x", ViewType: 1, GIDs: 2},
		"缺 vt":    {Subject: "t", ContentHTML: "x", ForumID: "1", GIDs: 2},
		"缺 gids":  {Subject: "t", ContentHTML: "x", ForumID: "1", ViewType: 1},
	}
	for name, opts := range cases {
		if _, oerr := svc.Publish(context.Background(), session.Session{}, opts); oerr == nil {
			t.Errorf("%s 应拒绝", name)
		}
	}
}

func TestPostDelete_HappyPath(t *testing.T) {
	st := &publishServer{}
	srv := newPublishServer(t, st)
	t.Cleanup(srv.Close)
	c, _ := api.New(srv.URL)
	s := New(c)

	if oerr := s.Delete(context.Background(), session.Session{
		UID: "u", MID: "m", Stoken: "s", DeviceID: "d", DeviceFP: "f",
	}, "78239787"); oerr != nil {
		t.Fatalf("Delete: %v", oerr)
	}
	if st.deleteBody != `{"operate_type":0,"post_id":"78239787"}` {
		t.Errorf("delete body = %s", st.deleteBody)
	}
	if oerr := s.Delete(context.Background(), session.Session{}, ""); oerr == nil {
		t.Error("空 post-id 应拒绝")
	}
}

func TestPublishVideo_HappyPath(t *testing.T) {
	// 2026-09-18 App 实抓契约：view_type=5 + meta_content.vods + gids 字符串
	// + block_reply_img int + user_ai_content_choice；无 lottery/contribution_act。
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/post/api/releasePost/v2" {
			t.Errorf("path = %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"post_id":"0","release_check_result":null,"post_review_id":"2391154"}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := New(c)

	res, oerr := s.PublishVideo(context.Background(), session.Session{
		UID: "470529491", MID: "m", Stoken: "v2_t", DeviceID: "d", DeviceFP: "f",
	}, VideoPublishOptions{
		Subject: "每日水帖", Text: "每日水贴",
		VideoID:  "2100868306046070784",
		CoverURL: "https://upload-bbs.miyoushe.com/upload/2026/09/18/470529491/cover.png",
		ForumID:  "951", ForumCateID: "15", GIDs: "10",
		TopicIDs: []string{"877", "238"}, BlockReplyImg: 1,
	})
	if oerr != nil {
		t.Fatalf("PublishVideo: %v", oerr)
	}
	if !res.Allowed || res.PostID != "" || res.ReviewID != "2391154" {
		// 视频帖进审核：Allowed=受理，PostID 为空、ReviewID 非空。
		t.Errorf("进审核结果: %+v", res)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("body 非法: %v", err)
	}
	for _, k := range []string{"meta_content", "view_type", "cover", "f_forum_id", "forum_cate_id",
		"gids", "topic_ids", "block_reply_img", "user_ai_content_choice", "subject", "content"} {
		if _, ok := body[k]; !ok {
			t.Errorf("视频帖 body 缺少 %s", k)
		}
	}
	for _, forbidden := range []string{"lottery", "contribution_act", "link_card_ids"} {
		if _, ok := body[forbidden]; ok {
			t.Errorf("视频帖 body 不应包含 %s（实抓无此字段）", forbidden)
		}
	}
	if body["view_type"] != float64(5) || body["block_reply_img"] != float64(1) {
		t.Errorf("view_type/block_reply_img: %v/%v", body["view_type"], body["block_reply_img"])
	}
	if body["gids"] != "10" || body["forum_cate_id"] != "15" {
		t.Errorf("gids/forum_cate_id 应为字符串: %v/%v", body["gids"], body["forum_cate_id"])
	}
	if body["user_ai_content_choice"] != "USER_AI_CONTENT_CHOICE_NOT_AI" {
		t.Errorf("user_ai_content_choice = %v", body["user_ai_content_choice"])
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(body["meta_content"].(string)), &meta); err != nil {
		t.Fatalf("meta_content 非法: %v", err)
	}
	vods, _ := meta["vods"].([]any)
	if len(vods) != 1 || vods[0].(map[string]any)["id"] != "2100868306046070784" {
		t.Errorf("meta_content.vods = %v", meta["vods"])
	}
}

func TestPublishVideo_InputValidation(t *testing.T) {
	svc := New(nil)
	sess := session.Session{UID: "u", MID: "m", Stoken: "s", DeviceID: "d", DeviceFP: "f"}
	cases := map[string]VideoPublishOptions{
		"缺标题":     {Text: "x", VideoID: "v", CoverURL: "c", ForumID: "1", GIDs: "2"},
		"缺 video": {Subject: "t", Text: "x", CoverURL: "c", ForumID: "1", GIDs: "2"},
		"缺封面":     {Subject: "t", Text: "x", VideoID: "v", ForumID: "1", GIDs: "2"},
		"缺 forum": {Subject: "t", Text: "x", VideoID: "v", CoverURL: "c", GIDs: "2"},
		"缺 gids":  {Subject: "t", Text: "x", VideoID: "v", CoverURL: "c", ForumID: "1"},
	}
	for name, opts := range cases {
		if _, oerr := svc.PublishVideo(context.Background(), sess, opts); oerr == nil {
			t.Errorf("%s 应拒绝", name)
		}
	}
}

func TestUndoReview_HappyPath(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/post/api/review/undo" {
			t.Errorf("path = %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{}}`)
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	s := New(c)

	if oerr := s.UndoReview(context.Background(), session.Session{
		UID: "u", MID: "m", Stoken: "s", DeviceID: "d", DeviceFP: "f",
	}, "2391154"); oerr != nil {
		t.Fatalf("UndoReview: %v", oerr)
	}
	if gotBody != `{"review_id":"2391154"}` {
		t.Errorf("undo body = %s", gotBody)
	}
	if oerr := s.UndoReview(context.Background(), session.Session{}, ""); oerr == nil {
		t.Error("空 review-id 应拒绝")
	}
}

func TestContentRaw_DoubleEncodedString(t *testing.T) {
	// 实测（2026-09-20，分区帖子流）：content 为 {describe,imgs} 的再序列化字符串，
	// 应二次解析还原正文与图片，而不是整串塞进 Describe。
	var p struct {
		Content *contentRaw `json:"content"`
	}
	in := `{"content":"{\"describe\":\"_(柚叶-sorry)\",\"imgs\":[\"https://x/a.jpg\"]}"}`
	if err := json.Unmarshal([]byte(in), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Content == nil || p.Content.Describe != "_(柚叶-sorry)" || len(p.Content.Imgs) != 1 {
		t.Fatalf("content = %+v", p.Content)
	}
}
