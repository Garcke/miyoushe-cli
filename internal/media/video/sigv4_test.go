package video

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// 合成向量由独立实现（Python 复算脚本）生成，锁定 SigV4 回归；
// 算法本身已用 2026-09-11 实抓的 Apply(GET)/Commit(POST) 两个真实样本
// 字节级验证（见 docs/architecture/video-upload-protocol.md §8.1）。
const sigv4VectorAuthorization = "HMAC-SHA256 Credential=AKTEST000000000000000000/20260911/cn-north-1/vod/request,SignedHeaders=host;x-date;x-security-token,Signature=17834840f86fc60ff1fbf8358a62ce4fe4ab4af7651ca40c87fd51f5dc7fe6c8"

func TestSignVODRequest_ApplyVector(t *testing.T) {
	endpoint := "https://vod.volcengineapi.com/?Action=ApplyUploadInfo&SpaceName=miyoushe-prod&Version=2022-01-01"
	cred := STSCredential{
		AccessKeyID:     "AKTEST000000000000000000",
		SecretAccessKey: "TESTSECRET0000000000000000000000",
		SessionToken:    "STS2TESTTOKENEXAMPLE",
	}
	now := time.Date(2026, 9, 11, 7, 24, 42, 0, time.UTC)
	hm, err := SignVODRequest(http.MethodGet, endpoint, nil, cred, now)
	if err != nil {
		t.Fatalf("SignVODRequest: %v", err)
	}
	if got := hm.Get("Authorization"); got != sigv4VectorAuthorization {
		t.Errorf("Authorization:\n got  %s\n want %s", got, sigv4VectorAuthorization)
	}
	if got := hm.Get("x-date"); got != "20260911T072442Z" {
		t.Errorf("x-date = %s", got)
	}
	if got := hm.Get("x-security-token"); got != cred.SessionToken {
		t.Errorf("x-security-token = %s", got)
	}
	if got := hm.Get("x-expires"); got != xExpires {
		t.Errorf("x-expires = %s", got)
	}
}

func TestSignVODRequest_POSTBodyChangesSignature(t *testing.T) {
	endpoint := "https://vod.volcengineapi.com/?Action=CommitUploadInfo&Version=2022-01-01"
	cred := STSCredential{AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "ST"}
	now := time.Date(2026, 9, 11, 7, 24, 42, 0, time.UTC)

	h1, err := SignVODRequest(http.MethodPost, endpoint, []byte("body-a"), cred, now)
	if err != nil {
		t.Fatalf("SignVODRequest: %v", err)
	}
	h2, err := SignVODRequest(http.MethodPost, endpoint, []byte("body-b"), cred, now)
	if err != nil {
		t.Fatalf("SignVODRequest: %v", err)
	}
	s1 := strings.SplitN(h1.Get("Authorization"), "Signature=", 2)[1]
	s2 := strings.SplitN(h2.Get("Authorization"), "Signature=", 2)[1]
	if s1 == s2 {
		t.Error("不同 body 的 POST 签名不应相同")
	}
	if h1.Get("Authorization") == h2.Get("Authorization") {
		t.Error("Authorization 应随 payload 变化")
	}
}

func TestCanonicalQuery_RFC3986(t *testing.T) {
	v := url.Values{}
	v.Set("b key", "a b")
	v.Set("a", "x~y+z")
	got := canonicalQuery(v)
	// 键排序 + 空格转 %20 + '~' 不转义 + '+' 转义
	if got != "a=x~y%2Bz&b%20key=a%20b" {
		t.Errorf("canonicalQuery = %q", got)
	}
}

func TestValidUploadHost(t *testing.T) {
	cases := []struct {
		host, suffix string
		ok           bool
	}{
		{"tob-upload-x-d.volcvod.com", ".volcvod.com", true},
		{"evil.example.com", ".volcvod.com", false},
		{"volcvod.com.evil.example", ".volcvod.com", false},
		{"tob-upload-x-d.volcvod.com/path", ".volcvod.com", false},
		{"", ".volcvod.com", false},
		{"127.0.0.1:8080", "127.0.0.1:8080", true},
	}
	for _, c := range cases {
		if got := validUploadHost(c.host, c.suffix); got != c.ok {
			t.Errorf("validUploadHost(%q, %q) = %v, want %v", c.host, c.suffix, got, c.ok)
		}
	}
}
