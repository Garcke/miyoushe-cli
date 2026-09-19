package auth

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

func TestGenAuthKey_HappyPath(t *testing.T) {
	var gotHeader http.Header
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/account/auth/api/genAuthKey" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotHeader = r.Header.Clone()
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		gotBody = string(body)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"sign_type":2,"authkey_ver":1,"authkey":"ePEBJA9wQl****（脱敏）"}}`)
	}))
	t.Cleanup(srv.Close)
	c, _ := api.New(srv.URL)
	s := &AuthKeyService{Client: c}

	ak, oerr := s.Gen(context.Background(), session.Session{
		UID: "82463740", MID: "mid_test", Stoken: "v2_test",
		DeviceID: "dev-test", DeviceFP: "fp13test00001",
	}, AuthKeyOptions{AuthAppID: "csc", GameBiz: "bbs_cn", GameUID: "116084021", Region: "cn_gf01"})
	if oerr != nil {
		t.Fatalf("Gen: %v", oerr)
	}
	if ak.AuthKey == "" || ak.AuthKeyVer != "1" || ak.SignType != "2" {
		t.Errorf("AuthKey: %+v", ak)
	}
	// 实测契约：body 恰好四字段；Cookie 为 stoken 三件套；DS 为 bbs DS1。
	var body map[string]any
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("body 非法: %v", err)
	}
	if len(body) != 4 || body["auth_appid"] != "csc" || body["game_biz"] != "bbs_cn" ||
		body["game_uid"] != "116084021" || body["region"] != "cn_gf01" {
		t.Errorf("genAuthKey body: %v", body)
	}
	if ck := gotHeader.Get("Cookie"); !strings.Contains(ck, "stoken=") || !strings.Contains(ck, "stuid=") || !strings.Contains(ck, "mid=") {
		t.Errorf("Cookie = %q", ck)
	}
	if gotHeader.Get("DS") == "" {
		t.Error("缺少 DS 头")
	}
}

func TestGenAuthKey_InputValidation(t *testing.T) {
	s := &AuthKeyService{Client: nil}
	if _, oerr := s.Gen(context.Background(), session.Session{}, AuthKeyOptions{}); oerr == nil ||
		!strings.Contains(oerr.Message, "未配置") {
		t.Fatalf("未配置 client 应报错: %+v", oerr)
	}
	s2 := &AuthKeyService{}
	_ = s2
	c, _ := api.New("http://127.0.0.1:1")
	svc := &AuthKeyService{Client: c}
	if _, oerr := svc.Gen(context.Background(), session.Session{
		UID: "u", MID: "m", Stoken: "s", DeviceID: "d", DeviceFP: "f",
	}, AuthKeyOptions{AuthAppID: "csc"}); oerr == nil {
		t.Fatal("缺 game_biz/game_uid/region 应拒绝")
	}
}
