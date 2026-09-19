// Package video 实现视频媒体能力：米游社侧视频接口（isExist / getToken /
// getVideoID / updateCover）与火山 VOD 直传适配器（Apply → transfer →
// finish → Commit）。
//
// 证据来源：docs/architecture/video-upload-protocol.md（DEX 静态分析与
// 2026-09-11 实抓全链路样本）。SigV4 算法已用实抓 Apply(GET) 与
// Commit(POST) 两个样本字节级复现验证；分片 CRC32 为 IEEE（zlib）多项式。
//
// 门禁：本包完成契约测试后仅作为适配器底座，写命令仍按
// adapter_ready 门禁由 session.Require 控制，不在本包注册。
package video

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// STSCredential 是 getToken 返回 token 字符串中的 STS2 临时凭据。
// 只驻留内存，不进入日志、输出或持久化。
type STSCredential struct {
	AccessKeyID     string `json:"AccessKeyID"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
}

// VOD OpenAPI 固定剖面（实抓样本：Service=vod，Region=cn-north-1）。
const (
	VODRegion   = "cn-north-1"
	VODService  = "vod"
	VODVersion  = "2022-01-01"
	VODAPIHost  = "vod.volcengineapi.com"
	VODAuthType = "HMAC-SHA256"

	// vodScopeTerm 是 credential scope 与签名密钥链的末段（火山规范，
	// 非 AWS 的固定 "aws4_request"）。
	vodScopeTerm = "request"

	// signedHeaders 固定为三项（实抓样本）；x-expires 会发送但不签名。
	signedHeaders = "host;x-date;x-security-token"

	// xExpires 取实抓样本值（一年）。
	xExpires = "31536000"
)

// vodDate 从 x-date（yyyyMMdd'T'HHmmss'Z'）截取 credential scope 日期段。
func vodDate(xdate string) string {
	if len(xdate) >= 8 {
		return xdate[:8]
	}
	return xdate
}

// rfc3986Escape 按 SigV4 规范转义：Go 的 QueryEscape 仅空格使用 '+'，需替换。
func rfc3986Escape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// canonicalQuery 构造排序后的 canonical query string。
func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		vs := q[k]
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, rfc3986Escape(k)+"="+rfc3986Escape(v))
		}
	}
	return strings.Join(parts, "&")
}

// SignVODRequest 为 vod.volcengineapi.com 的 OpenAPI 请求计算签名头。
//
// 返回需要叠加的 header：Authorization、x-date、x-security-token、
// x-expires。签名覆盖 host、x-date、x-security-token 三项；payload 为
// body 的 SHA-256（GET 时 body 为空）。
func SignVODRequest(method, rawURL string, body []byte, cred STSCredential, now time.Time) (http.Header, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("video: 无效 VOD URL: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("video: VOD 请求必须是 https 绝对地址")
	}
	host := u.Hostname()
	if u.Port() != "" {
		host = u.Host
	}

	xdate := now.UTC().Format("20060102T150405Z")
	hm := http.Header{}
	hm.Set("x-date", xdate)
	hm.Set("x-security-token", cred.SessionToken)
	hm.Set("x-expires", xExpires)

	// canonical headers 按 SignedHeaders 顺序（host < x-date < x-security-token）。
	chdrs := "host:" + strings.TrimSpace(host) + "\n" +
		"x-date:" + strings.TrimSpace(xdate) + "\n" +
		"x-security-token:" + strings.TrimSpace(cred.SessionToken) + "\n"

	canonical := strings.Join([]string{
		method,
		u.Path,
		canonicalQuery(u.Query()),
		chdrs,
		signedHeaders,
		sha256Hex(body),
	}, "\n")

	scope := vodDate(xdate) + "/" + VODRegion + "/" + VODService + "/" + vodScopeTerm
	stringToSign := strings.Join([]string{
		VODAuthType,
		xdate,
		scope,
		sha256Hex([]byte(canonical)),
	}, "\n")

	key := signingKey(cred.SecretAccessKey, vodDate(xdate), VODRegion, VODService)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(stringToSign))
	sig := hex.EncodeToString(mac.Sum(nil))

	hm.Set("Authorization", fmt.Sprintf("%s Credential=%s/%s,SignedHeaders=%s,Signature=%s",
		VODAuthType, cred.AccessKeyID, scope, signedHeaders, sig))
	return hm, nil
}

// signingKey 派生签名密钥：HMAC-SHA256 链 SK→date→region→service→"request"。
// 已用实抓 Apply(GET) 与 Commit(POST) 两个样本字节级验证。
func signingKey(secret, date, region, service string) []byte {
	key := hmac.New(sha256.New, []byte(secret))
	key.Write([]byte(date))
	step := hmac.New(sha256.New, key.Sum(nil))
	step.Write([]byte(region))
	step2 := hmac.New(sha256.New, step.Sum(nil))
	step2.Write([]byte(service))
	step3 := hmac.New(sha256.New, step2.Sum(nil))
	step3.Write([]byte(vodScopeTerm))
	return step3.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
