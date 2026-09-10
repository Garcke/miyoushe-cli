package protocol

import (
	"crypto/md5"
	"encoding/hex"
	"regexp"
	"testing"
)

// 向量来自协议证据快照 DS_CAPTURE_RECORD.md：
// 仅含公开旧 salt、时间戳、随机串和 MD5，早已失效，只用于算法校验。
func TestDSBBSHeader_KnownVectors(t *testing.T) {
	const oldSalt = "897878226392bd988a289cb7a589ee52"
	cases := []struct {
		t    int64
		r    string
		want string
	}{
		{1787734195, "51j16n", "1787734195,51j16n,80527a703d202151107366b8ba0f0abc"},
		{1787734199, "c14700", "1787734199,c14700,93bede568758a017d7551451e9cab655"},
		{1787734218, "fh145k", "1787734218,fh145k,a923f25ca9555f7edaa8e24bf73c2f0b"},
	}
	for _, c := range cases {
		if got := DSBBSHeader(oldSalt, c.t, c.r); got != c.want {
			t.Errorf("DSBBSHeader(%d,%s) = %s, want %s", c.t, c.r, got, c.want)
		}
	}
}

func TestDSPassportHeader_Deterministic(t *testing.T) {
	body := `{"account_id":"1001","game_token":"tok"}`
	got := DSPassportHeader(SaltPassport, 1787734195, "AOtZdq", body)
	sum := md5.Sum([]byte("salt=" + SaltPassport + "&t=1787734195&r=AOtZdq&b=" + body + "&q="))
	want := "1787734195,AOtZdq," + hex.EncodeToString(sum[:])
	if got != want {
		t.Errorf("DSPassportHeader = %s, want %s", got, want)
	}
	// body 必须参与签名：不同 body 结果不同。
	if got2 := DSPassportHeader(SaltPassport, 1787734195, "AOtZdq", body+"x"); got2 == got {
		t.Error("不同 body 的 passport DS 不应相同")
	}
}

func TestNewDSBBS_Format(t *testing.T) {
	re := regexp.MustCompile(`^\d{10},[0-9a-z]{6},[0-9a-f]{32}$`)
	for i := 0; i < 20; i++ {
		if !re.MatchString(NewDSBBS()) {
			t.Fatalf("NewDSBBS 格式不符合 DS1 约定: %s", NewDSBBS())
		}
	}
}

func TestNewDSPassport_Format(t *testing.T) {
	re := regexp.MustCompile(`^\d{10},[a-zA-Z0-9]{6},[0-9a-f]{32}$`)
	for i := 0; i < 20; i++ {
		if !re.MatchString(NewDSPassport(`{"k":"v"}`)) {
			t.Fatalf("NewDSPassport 格式不符合 passport 约定: %s", NewDSPassport(`{"k":"v"}`))
		}
	}
}

func TestCommonHeaders(t *testing.T) {
	h := CommonHeaders(ClientTypeAndroid, DeviceContext{DeviceID: "dev-1", DeviceFP: "fp13hex1"})
	want := map[string]string{
		"User-Agent":           UserAgent,
		"Referer":              Referer,
		"X-Rpc-App_version":    AppVersion,
		"X-Rpc-Sys_version":    SysVersion,
		"X-Rpc-Channel":        Channel,
		"X-Rpc-Client_type":    "2",
		"X-Rpc-Device_name":    DeviceName,
		"X-Rpc-Device_model":   DeviceModel,
		"X-Rpc-Verify_key":     VerifyKey,
		"X-Rpc-Device_id":      "dev-1",
		"X-Rpc-Device_fp":      "fp13hex1",
		"X-Rpc-H265_supported": "1",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	// 扫码头不携带 Cookie/DS；调用方按需叠加。
	if h.Get("Cookie") != "" || h.Get("DS") != "" {
		t.Error("CommonHeaders 不应自带 Cookie/DS")
	}
}

func TestSTokenCookie(t *testing.T) {
	got := STokenCookie("1001", "v2_x", "mid_y")
	if got != "stuid=1001; stoken=v2_x; mid=mid_y;" {
		t.Errorf("STokenCookie = %q", got)
	}
}
