package video

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/session"
)

func videoSession() session.Session {
	return session.Session{
		UID: "100024680", MID: "mid_syn", Stoken: "v2_syn",
		DeviceID: "device-syn", DeviceFP: "fp0123456789a",
	}
}

func newVideoService(t *testing.T, handler http.HandlerFunc) (*Service, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := api.New(srv.URL)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	return New(c), srv
}

func TestIsExist_Miss(t *testing.T) {
	var gotDS, gotCookie, gotVerifyKey string
	svc, _ := newVideoService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/video/api/isExist" {
			t.Errorf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("md5") != "df6ed4bbc93613c68c8525e21bbddf98" || q.Get("scene") != "0" || q.Get("video_provider") != "1" {
			t.Errorf("query = %v", q)
		}
		gotDS = r.Header.Get("DS")
		gotCookie = r.Header.Get("Cookie")
		gotVerifyKey = r.Header.Get("x-rpc-verify_key")
		io.WriteString(w, `{"retcode":0,"message":"OK","data":{"is_exist":false,"video_id":"","video_info":{"duration":0}}}`)
	})
	pre, oerr := svc.IsExist(context.Background(), videoSession(), "df6ed4bbc93613c68c8525e21bbddf98", SceneDefault)
	if oerr != nil {
		t.Fatalf("IsExist: %v", oerr)
	}
	if pre.IsExist || pre.VideoID != "" {
		t.Errorf("pre = %+v", pre)
	}
	if !strings.Contains(gotCookie, "stuid=100024680") || !strings.Contains(gotCookie, "stoken=v2_syn") {
		t.Errorf("cookie = %s", gotCookie)
	}
	if gotDS == "" || gotVerifyKey != "bll8iq97cem8" {
		t.Errorf("ds=%q verify_key=%q", gotDS, gotVerifyKey)
	}
}

func TestIsExist_HitReturnsVideoInfoDuration(t *testing.T) {
	svc, _ := newVideoService(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"retcode":0,"message":"OK","data":{"is_exist":true,"video_id":"2098304961564684288","video_info":{"duration":52208}}}`)
	})
	pre, oerr := svc.IsExist(context.Background(), videoSession(), "df6ed4bbc93613c68c8525e21bbddf98", SceneDefault)
	if oerr != nil {
		t.Fatalf("IsExist: %v", oerr)
	}
	if !pre.IsExist || pre.VideoID != "2098304961564684288" {
		t.Errorf("pre = %+v", pre)
	}
	if pre.VideoDurationMS() != 52208 {
		t.Errorf("duration = %d", pre.VideoDurationMS())
	}
}

func TestGetUploadToken_QueryAndParse(t *testing.T) {
	svc, _ := newVideoService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/video/api/getToken" {
			t.Errorf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("size") != "23014356" || q.Get("duration") != "46613" || q.Get("name") != "oceans.mp4" ||
			q.Get("md5") != "2125298091532905922013119cc3d2e9" || q.Get("video_provider") != "1" {
			t.Errorf("query = %v", q)
		}
		io.WriteString(w, `{"retcode":0,"message":"OK","data":{"token":"{\"AccessKeyID\":\"AKSYN\",\"SecretAccessKey\":\"SKSYN\",\"SessionToken\":\"STS2SYN\"}","callback_args":"cb"}}`)
	})
	pre, oerr := svc.GetUploadToken(context.Background(), videoSession(), "2125298091532905922013119cc3d2e9", 23014356, 46613, "oceans.mp4")
	if oerr != nil {
		t.Fatalf("GetUploadToken: %v", oerr)
	}
	cred, oerr := ParseUploadToken(pre.Token.String())
	if oerr != nil {
		t.Fatalf("ParseUploadToken: %v", oerr)
	}
	if cred.AccessKeyID != "AKSYN" || cred.SecretAccessKey != "SKSYN" || cred.SessionToken != "STS2SYN" {
		t.Errorf("cred = %+v", cred)
	}
	if pre.CallbackArgs != "cb" {
		t.Errorf("callback_args = %s", pre.CallbackArgs)
	}
}

func TestGetUploadToken_ErrorRetcode(t *testing.T) {
	svc, _ := newVideoService(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"retcode":16002,"message":"上传视频次数已达上限"}`)
	})
	_, oerr := svc.GetUploadToken(context.Background(), videoSession(), "md5", 100, 1000, "a.mp4")
	if oerr == nil {
		t.Fatal("期望 retcode 非零时报错")
	}
	if oerr.Retcode != 16002 {
		t.Errorf("retcode = %d", oerr.Retcode)
	}
}

func TestGetVideoID(t *testing.T) {
	svc, _ := newVideoService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/video/api/getVideoID" {
			t.Errorf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("file_id") != "v03c41g10002dahqpeq7dld9ajotis9g" || q.Get("video_provider") != "1" {
			t.Errorf("query = %v", q)
		}
		io.WriteString(w, `{"retcode":0,"message":"OK","data":{"video_id":"2098311825916432384","video_info":{"duration":46613}}}`)
	})
	vid, dur, oerr := svc.GetVideoID(context.Background(), videoSession(), "v03c41g10002dahqpeq7dld9ajotis9g", "2125298091532905922013119cc3d2e9")
	if oerr != nil {
		t.Fatalf("GetVideoID: %v", oerr)
	}
	if vid != "2098311825916432384" || dur != 46613 {
		t.Errorf("vid=%s dur=%d", vid, dur)
	}
}

func TestUpdateCover_Body(t *testing.T) {
	svc, _ := newVideoService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/video/api/updateCover" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			VideoID  string `json:"video_id"`
			CoverURL string `json:"cover_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body: %v", err)
		}
		if body.VideoID != "2098311825916432384" || !strings.HasPrefix(body.CoverURL, "https://upload-bbs.miyoushe.com/") {
			t.Errorf("body = %+v", body)
		}
		io.WriteString(w, `{"retcode":0,"message":"OK","data":{}}`)
	})
	if oerr := svc.UpdateCover(context.Background(), videoSession(), "2098311825916432384", "https://upload-bbs.miyoushe.com/upload/2026/09/11/x.jpg"); oerr != nil {
		t.Fatalf("UpdateCover: %v", oerr)
	}
}

func TestCheckPublishPerm(t *testing.T) {
	svc, _ := newVideoService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/post/api/check/publishVideoPerm" || r.Method != http.MethodGet {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		// 实抓样本（2026-09-11）。
		io.WriteString(w, `{"retcode":0,"message":"OK","data":{"can_publish":true,"toast":""}}`)
	})
	perm, oerr := svc.CheckPublishPerm(context.Background(), videoSession())
	if oerr != nil {
		t.Fatalf("CheckPublishPerm: %v", oerr)
	}
	if !perm.CanPublish || perm.Toast != "" {
		t.Errorf("perm = %+v", perm)
	}
}

func TestResidualTimes_TolerantFieldNames(t *testing.T) {
	// App 端字段（count 系，DEX bean 推断拼写）优先；web wapi 实抓的
	// times/max_times 仅作参考回退。
	cases := []struct {
		body          string
		wantRemaining int
		wantMaxTimes  int
	}{
		{`{"retcode":0,"message":"OK","data":{"count":3,"max_count":10}}`, 3, 10},
		{`{"retcode":0,"message":"OK","data":{"count":3,"maxCount":10}}`, 3, 10},
		{`{"retcode":0,"message":"OK","data":{"times":9,"max_times":10}}`, 9, 10},
		{`{"retcode":0,"message":"OK","data":{"times":9,"max_times":10,"count":3,"max_count":10}}`, 3, 10},
	}
	for _, c := range cases {
		svc, _ := newVideoService(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/video/api/residualTimes" {
				t.Errorf("path = %s", r.URL.Path)
			}
			io.WriteString(w, c.body)
		})
		quota, oerr := svc.ResidualTimes(context.Background(), videoSession())
		if oerr != nil {
			t.Fatalf("ResidualTimes(%s): %v", c.body, oerr)
		}
		if quota.Remaining() != c.wantRemaining || quota.MaxCountBoth() != c.wantMaxTimes {
			t.Errorf("quota = %+v", quota)
		}
	}
}

func TestParseUploadToken_RejectsIncomplete(t *testing.T) {
	if _, oerr := ParseUploadToken(`{"AccessKeyID":"a"}`); oerr == nil {
		t.Error("缺字段应报错")
	}
	if _, oerr := ParseUploadToken(`not-json`); oerr == nil {
		t.Error("非 JSON 应报错")
	}
}
