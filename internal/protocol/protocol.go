// Package protocol 固定米游社 App 协议剖面：App 版本、DS 算法、
// 公共 x-rpc 头与 host 白名单。认证状态机只保留 passport/二维码专用逻辑。
//
// 证据来源（docs/reference/cnb-mihoyo-api 快照）：
//   - DS1（client_type=2/4，"bbs DS"）：md5("salt={s}&t={t}&r={r}")，
//     t 秒级，r 为 6 位 [0-9a-z]；salt 随 App 版本轮换（2.114.0 = SaltBBS）。
//   - passport DS（ma-cn-passport / ma-cn-session 域）：
//     md5("salt={s}&t={t}&r={r}&b={请求体原文}&q={query}")，r 为 6 位
//     [a-zA-Z0-9]（含大写），q 在 2.114.0 恒为空串；salt 硬编码于 SDK。
//
// 上游实测提示 bbs-api/takumi 网关要求完整 x-rpc 头组合（缺头 rc=-10001）。
// 完整 15 项头集必须由脱敏 fixture 固化后才视为 adapter_ready；本文件
// 当前收录快照中可确认的头，后续按证据补充，不猜测未记录的取值。
package protocol

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 协议剖面常量。修改任何值都属于 protocol profile 变更，需要重新验证。
const (
	AppVersion  = "2.114.0"
	SysVersion  = "12"
	Channel     = "miyousheluodi"
	VerifyKey   = "bll8iq97cem8"
	UserAgent   = "okhttp/4.9.3"
	Referer     = "https://app.mihoyo.com"
	DeviceName  = "Xiaomi 13"
	DeviceModel = "2211133C"

	// 设备指纹接口（getFp）使用网页平台语义时的 UA。
	BrowserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

	// HK4E 扫码通道 app_id；实测返回 app_name=绝区零/nap_cn，bbs=true。
	AppIDQR = "12"

	// DS 盐。SaltBBS 随 App 版本轮换（2.114.0）；SaltPassport 硬编码于 SDK。
	SaltBBS      = "d64014da690671f8704695e993130f4c"
	SaltPassport = "JwYDpKvLj6MrMqqYU6jTKF17KNO2PXoS"
)

// x-rpc-client_type 取值。
const (
	ClientTypeAndroid = 2 // 业务接口：Cookie + DS1
	ClientTypeScan    = 5 // 扫码接口：无 Cookie、无 DS
)

// 固定 host 白名单。所有远程 URL 只能由这些 endpoint 或经白名单验证的
// 上传响应产生；CLI 不公开 --endpoint。
const (
	HostHK4E           = "hk4e-sdk.mihoyo.com"
	HostPublicData     = "public-data-api.mihoyo.com"
	HostTakumi         = "api-takumi.mihoyo.com"
	HostTakumiMiyoushe = "api-takumi.miyoushe.com"
	HostBBS            = "bbs-api.miyoushe.com"
)

const (
	dsRSAlphabet  = "0123456789abcdefghijklmnopqrstuvwxyz"                           // DS1：r 为 6 位小写
	dsRSAlphabetX = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789" // passport：r 含大写
)

// dsSig1 计算 DS1 摘要（不含 query/body）。
func dsSig1(salt, t, r string) string {
	sum := md5.Sum([]byte("salt=" + salt + "&t=" + t + "&r=" + r))
	return hex.EncodeToString(sum[:])
}

// dsSigPassport 计算 passport 摘要；b 必须与实际发送的请求体字节完全一致。
func dsSigPassport(salt, t, r, body, query string) string {
	sum := md5.Sum([]byte("salt=" + salt + "&t=" + t + "&r=" + r + "&b=" + body + "&q=" + query))
	return hex.EncodeToString(sum[:])
}

// DSBBSHeader 按给定 t、r 构造 DS1 头，供测试向量校验。
func DSBBSHeader(salt string, t int64, r string) string {
	ts := strconv.FormatInt(t, 10)
	return ts + "," + r + "," + dsSig1(salt, ts, r)
}

// DSPassportHeader 按给定 t、r、body 构造 passport DS 头，供测试锁定。
func DSPassportHeader(salt string, t int64, r, body string) string {
	ts := strconv.FormatInt(t, 10)
	return ts + "," + r + "," + dsSigPassport(salt, ts, r, body, "")
}

func randomString(n int, alphabet string) string {
	out := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			// crypto/rand 失败属于不可恢复环境错误。
			panic("protocol: crypto/rand 不可用: " + err.Error())
		}
		out[i] = alphabet[v.Int64()]
	}
	return string(out)
}

// NewDSBBS 生成当前版本的 DS1 请求头。
func NewDSBBS() string {
	return DSBBSHeader(SaltBBS, time.Now().Unix(), randomString(6, dsRSAlphabet))
}

// NewDSPassport 生成 passport DS 请求头；body 必须是实际发送的请求体原文。
func NewDSPassport(body string) string {
	return DSPassportHeader(SaltPassport, time.Now().Unix(), randomString(6, dsRSAlphabetX), body)
}

// DeviceContext 是一次登录/会话共用的设备标识。
type DeviceContext struct {
	DeviceID string
	DeviceFP string
}

// CommonHeaders 构造公共请求头。clientType 为 ClientTypeAndroid（业务）
// 或 ClientTypeScan（扫码）。DS 与 Cookie 由调用方按接口要求叠加。
func CommonHeaders(clientType int, dev DeviceContext) http.Header {
	h := http.Header{}
	h.Set("User-Agent", UserAgent)
	h.Set("Referer", Referer)
	h.Set("x-rpc-app_version", AppVersion)
	h.Set("x-rpc-sys_version", SysVersion)
	h.Set("x-rpc-channel", Channel)
	h.Set("x-rpc-client_type", strconv.Itoa(clientType))
	h.Set("x-rpc-device_name", DeviceName)
	h.Set("x-rpc-device_model", DeviceModel)
	h.Set("x-rpc-verify_key", VerifyKey)
	h.Set("x-rpc-h265_supported", "1")
	h.Set("x-rpc-csm_source", "main")
	if dev.DeviceID != "" {
		h.Set("x-rpc-device_id", dev.DeviceID)
	}
	if dev.DeviceFP != "" {
		h.Set("x-rpc-device_fp", dev.DeviceFP)
	}
	return h
}

// WithDS 在头集合上叠加 DS 头。
func WithDS(h http.Header, ds string) http.Header {
	h.Set("DS", ds)
	return h
}

// WithCookie 在头集合上叠加 Cookie 头。
func WithCookie(h http.Header, cookie string) http.Header {
	h.Set("Cookie", cookie)
	return h
}

// STokenCookie 派生 SToken 三件套 Cookie。结果只在内存中使用，不持久化。
func STokenCookie(uid, stoken, mid string) string {
	var b strings.Builder
	b.WriteString("stuid=")
	b.WriteString(uid)
	b.WriteString("; stoken=")
	b.WriteString(stoken)
	b.WriteString("; mid=")
	b.WriteString(mid)
	b.WriteString(";")
	return b.String()
}
