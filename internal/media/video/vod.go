// vod.go 实现火山 VOD 直传状态机：ApplyUploadInfo → /upload/v1 part
// transfer → finish → CommitUploadInfo。
//
// 协议要点（全部有 2026-09-11 实抓样本背书，见
// docs/architecture/video-upload-protocol.md §8）：
//   - Apply/Commit 走 vod.volcengineapi.com，SigV4 签名（service=vod）；
//   - 直传 Authorization 为 Apply 下发的 StoreInfos[0].Auth（SpaceKey
//     JWT）原样，不做 SigV4；
//   - uploadid 是客户端生成的 UUID，无 init 请求；
//   - 分片 CRC32 为 IEEE 多项式，十进制写在 x-upload-content-crc32 头；
//   - finish body 为 "part_number:crc32hex" 全表；
//   - CallbackArgs / SessionKey / StoreInfos[0].Auth 一律原样透传。
package video

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"mihoyo_cli/internal/output"
)

// KiB / MiB 与分片阈值常量（DEX 逐字确认：size > 1 GiB → 5 MiB，否则 512 KiB）。
const (
	Slice512KiB = 524288
	Slice5MiB   = 5242880
	SliceThresh = 1073741824 // 1 GiB
)

// UploadHostSuffix 是直传 host 的白名单后缀。Apply 响应中的 UploadHosts
// 只允许匹配该后缀（实测值 tob-upload-x-d.volcvod.com），防止服务端响应
// 把上传流量引到任意 host。
const UploadHostSuffix = ".volcvod.com"

// sliceSize 按文件大小返回分片大小；cfg override 优先。
func sliceSize(cfg int64, fileSize int64) int64 {
	if cfg > 0 {
		return cfg
	}
	if fileSize > SliceThresh {
		return Slice5MiB
	}
	return Slice512KiB
}

// Config 是直传适配器配置。
type Config struct {
	// SpaceName 默认 miyoushe-prod（production 环境）。
	SpaceName string
	// SliceSize 为 0 时按文件大小自动选择（512 KiB / 5 MiB）。
	SliceSize int64
	// MaxPartRetry 是单个分片的传输重试上限（不含首次）。
	MaxPartRetry int
	// HTTP 客户端；零值时使用带超时的默认客户端。
	HTTP *http.Client
	// UploadHostSuffix 覆盖直传 host 白名单后缀（测试注入 httptest host）。
	UploadHostSuffix string
}

// Uploader 是火山 VOD 直传适配器。
type Uploader struct {
	Config Config
}

// NewUploader 构造并填充默认值。
func NewUploader(cfg Config) *Uploader {
	if cfg.SpaceName == "" {
		cfg.SpaceName = "miyoushe-prod"
	}
	if cfg.MaxPartRetry <= 0 {
		cfg.MaxPartRetry = 3
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 60 * time.Second}
	}
	if cfg.UploadHostSuffix == "" {
		cfg.UploadHostSuffix = UploadHostSuffix
	}
	return &Uploader{Config: cfg}
}

// Input 是一次上传的输入。Reader 必须支持按分片读取（*os.File 满足）。
type Input struct {
	// Credential 来自 getToken 的 token 字符串（ParseUploadToken）。
	Credential STSCredential
	// CallbackArgs 为 getToken 返回的 callback_args，原样透传。
	CallbackArgs string
	// Reader 提供文件内容；Size 为文件总字节数。
	Reader io.ReaderAt
	Size   int64
	// OnProgress 在每个分片成功后调用（已传字节数, 总字节数）；可为 nil。
	OnProgress func(sent, total int64)
}

// storeInfo 是 Apply 下发的单个存储条目。
type storeInfo struct {
	StoreURI string `json:"StoreUri"`
	Auth     string `json:"Auth"`
}

// applyResponse 解析 ApplyUploadInfo 的响应。
type applyResponse struct {
	ResponseMetadata struct {
		RequestId string `json:"RequestId"`
		Action    string `json:"Action"`
		Service   string `json:"Service"`
		Region    string `json:"Region"`
	} `json:"ResponseMetadata"`
	Result struct {
		Data struct {
			// 2026-09-18 实测：UploadHosts/SessionKey/Cloud 在 UploadAddress
			// 内层（上游 09-11 样本文档记为 Data 兄弟字段，以实测为准）；
			// 另有 CandidateUploadAddresses 字段，本链路不使用。
			UploadAddress struct {
				StoreInfos  []storeInfo `json:"StoreInfos"`
				UploadHosts []string    `json:"UploadHosts"`
				SessionKey  string      `json:"SessionKey"`
				Cloud       string      `json:"Cloud"`
			} `json:"UploadAddress"`
		} `json:"Data"`
	} `json:"Result"`
}

// tosEnvelope 是 /upload/v1 的响应 envelope，code=2000 为成功。
type tosEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		UploadID   string `json:"uploadid"`
		PartNumber string `json:"part_number"`
		CRC32      string `json:"crc32"`
		Hash       string `json:"hash"`
		// 部分失败响应携带客户端/服务端摘要（如 Mismatch CRC32）。
		ClientHash string `json:"client_hash"`
		ServerHash string `json:"server_hash"`
	} `json:"data"`
}

// commitResponse 解析 CommitUploadInfo 的响应。
type commitResponse struct {
	Result struct {
		Data struct {
			Vid string `json:"Vid"`
		} `json:"Data"`
	} `json:"Result"`
}

// Upload 执行完整直传并返回 VOD Vid（即 getVideoID 的 file_id）。
//
// 任何分片耗尽重试即失败；Commit 传输失败返回 REMOTE_RESULT_UNKNOWN
// （媒体已在 TOS，但登记结果未知，不自动重试）。
func (u *Uploader) Upload(ctx context.Context, in Input) (string, *output.Error) {
	if in.Credential.AccessKeyID == "" || in.Credential.SecretAccessKey == "" || in.Credential.SessionToken == "" {
		return "", output.Err(output.CodeInputInvalid, "上传凭据不完整")
	}
	if in.Reader == nil || in.Size <= 0 {
		return "", output.Err(output.CodeInputInvalid, "上传内容无效")
	}
	if in.CallbackArgs == "" {
		return "", output.Err(output.CodeInputInvalid, "callback_args 缺失")
	}

	slice := sliceSize(u.Config.SliceSize, in.Size)
	uploadID, oerr := newUploadID()
	if oerr != nil {
		return "", oerr
	}

	apply, oerr := u.apply(ctx, in.Credential)
	if oerr != nil {
		return "", oerr
	}
	addr := apply.Result.Data.UploadAddress
	if len(addr.StoreInfos) == 0 || len(addr.UploadHosts) == 0 {
		return "", output.Err(output.CodeRemoteRejected, "Apply 响应缺少 StoreInfos 或 UploadHosts")
	}
	store := addr.StoreInfos[0]
	uploadHost := addr.UploadHosts[0]
	if !validUploadHost(uploadHost, u.Config.UploadHostSuffix) {
		return "", output.Err(output.CodeRemoteRejected, "Apply 返回的上传 host 不在白名单内")
	}

	// 分片直传。
	parts := (in.Size + slice - 1) / slice
	var sent int64
	finishPairs := make([]string, 0, parts)
	for n := int64(0); n < parts; n++ {
		offset := n * slice
		size := slice
		if offset+size > in.Size {
			size = in.Size - offset
		}
		buf := make([]byte, size)
		if _, err := in.Reader.ReadAt(buf, offset); err != nil && err != io.EOF {
			return "", output.Err(output.CodeInternal, "读取分片 %d 失败", n)
		}
		sum := crc32.ChecksumIEEE(buf)
		crcHex := fmt.Sprintf("%08x", sum)

		var last *output.Error
		for attempt := 0; attempt <= u.Config.MaxPartRetry; attempt++ {
			oerr = u.transfer(ctx, uploadHost, store, uploadID, n, offset, buf, sum)
			if oerr == nil {
				last = nil
				break
			}
			last = oerr
			if oerr.Code != output.CodeRemoteRejected && oerr.Code != output.CodeLoginTimeout {
				break // 输入类错误不重试
			}
			select {
			case <-ctx.Done():
				return "", output.Err(output.CodeCancelled, "已取消")
			case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
			}
		}
		if last != nil {
			return "", last
		}

		finishPairs = append(finishPairs, fmt.Sprintf("%d:%s", n, crcHex))
		sent += int64(size)
		if in.OnProgress != nil {
			in.OnProgress(sent, in.Size)
		}
	}

	// finish：合并分片，返回整文件 hash。
	_, oerr = u.finish(ctx, uploadHost, store, uploadID, finishPairs)
	if oerr != nil {
		return "", oerr
	}

	// Commit：登记并换取 Vid。
	vid, oerr := u.commit(ctx, in.Credential, addr.SessionKey, in.CallbackArgs)
	if oerr != nil {
		return "", oerr
	}
	return vid, nil
}

func (u *Uploader) apply(ctx context.Context, cred STSCredential) (*applyResponse, *output.Error) {
	endpoint := fmt.Sprintf("https://%s/?Action=ApplyUploadInfo&SpaceName=%s&Version=%s",
		VODAPIHost, rfc3986Escape(u.Config.SpaceName), VODVersion)
	hm, err := SignVODRequest(http.MethodGet, endpoint, nil, cred, time.Now())
	if err != nil {
		return nil, output.Err(output.CodeInternal, "%s", err.Error())
	}
	raw, oerr := vodRoundTrip(ctx, u.Config.HTTP, http.MethodGet, endpoint, nil, hm)
	if oerr != nil {
		return nil, oerr
	}
	var resp applyResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, output.Err(output.CodeRemoteRejected, "Apply 响应结构不符")
	}
	if resp.Result.Data.UploadAddress.StoreInfos == nil {
		return nil, output.Err(output.CodeRemoteRejected, "Apply 响应缺少 UploadAddress")
	}
	return &resp, nil
}

func (u *Uploader) transfer(ctx context.Context, host string, store storeInfo, uploadID string, part, offset int64, body []byte, sum uint32) *output.Error {
	endpoint := fmt.Sprintf("https://%s/upload/v1/%s?%s", host, store.StoreURI, transferQuery(uploadID, part, offset))
	hm := http.Header{}
	hm.Set("Authorization", store.Auth)
	hm.Set("x-upload-content-crc32", strconv.FormatUint(uint64(sum), 10))
	hm.Set("x-tt-trace-id", traceID())

	raw, oerr := vodRoundTrip(ctx, u.Config.HTTP, http.MethodPost, endpoint, body, hm)
	if oerr != nil {
		return oerr
	}
	var env tosEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return output.Err(output.CodeRemoteRejected, "分片响应不是有效 JSON")
	}
	if env.Code != 2000 {
		return output.Err(output.CodeRemoteRejected,
			"分片 %d 上传被拒绝: code=%d msg=%q client=%q server=%q",
			part, env.Code, env.Message, env.Data.ClientHash, env.Data.ServerHash)
	}
	return nil
}

func (u *Uploader) finish(ctx context.Context, host string, store storeInfo, uploadID string, pairs []string) (string, *output.Error) {
	endpoint := fmt.Sprintf("https://%s/upload/v1/%s?%s", host, store.StoreURI,
		(url.Values{"uploadid": {uploadID}, "uploadmode": {"part"}, "phase": {"finish"}}).Encode())
	hm := http.Header{}
	hm.Set("Authorization", store.Auth)

	raw, oerr := vodRoundTrip(ctx, u.Config.HTTP, http.MethodPost, endpoint, []byte(strings.Join(pairs, ",")), hm)
	if oerr != nil {
		return "", oerr
	}
	var env tosEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", output.Err(output.CodeRemoteRejected, "finish 响应不是有效 JSON")
	}
	if env.Code != 2000 {
		return "", output.Err(output.CodeRemoteRejected, "finish 被拒绝: code=%d", env.Code)
	}
	return env.Data.Hash, nil
}

func (u *Uploader) commit(ctx context.Context, cred STSCredential, sessionKey, callbackArgs string) (string, *output.Error) {
	form := url.Values{}
	form.Set("SpaceName", u.Config.SpaceName)
	form.Set("SessionKey", sessionKey)
	form.Set("CallbackArgs", callbackArgs)
	form.Set("Functions", `[{"Input":{"SnapshotTime":0.0},"Name":"Snapshot"}]`)
	body := []byte(form.Encode())

	endpoint := fmt.Sprintf("https://%s/?Action=CommitUploadInfo&Version=%s", VODAPIHost, VODVersion)
	hm, err := SignVODRequest(http.MethodPost, endpoint, body, cred, time.Now())
	if err != nil {
		return "", output.Err(output.CodeInternal, "%s", err.Error())
	}
	hm.Set("Content-Type", "application/x-www-form-urlencoded")

	raw, oerr := vodRoundTrip(ctx, u.Config.HTTP, http.MethodPost, endpoint, body, hm)
	if oerr != nil {
		// Commit 失败时媒体已在 TOS 但登记结果未知，不自动重试。
		if oerr.Code == output.CodeRemoteRejected && oerr.Transport {
			return "", output.Err(output.CodeRemoteUnknown, "Commit 结果未知")
		}
		return "", oerr
	}
	var resp commitResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", output.Err(output.CodeRemoteUnknown, "Commit 响应结构不符")
	}
	if resp.Result.Data.Vid == "" {
		return "", output.Err(output.CodeRemoteUnknown, "Commit 未返回 Vid")
	}
	return resp.Result.Data.Vid, nil
}

func transferQuery(uploadID string, part, offset int64) string {
	v := url.Values{}
	v.Set("uploadid", uploadID)
	v.Set("part_number", strconv.FormatInt(part, 10))
	v.Set("phase", "transfer")
	v.Set("part_offset", strconv.FormatInt(offset, 10))
	return v.Encode()
}

// vodRoundTrip 是 VOD/直传请求的有限响应读取；与 api.Client 分离，
// 因为这两类 host 不使用米游社 retcode envelope。
func vodRoundTrip(ctx context.Context, client *http.Client, method, endpoint string, body []byte, header http.Header) ([]byte, *output.Error) {
	var rdr io.Reader
	if body != nil {
		rdr = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rdr)
	if err != nil {
		return nil, output.Err(output.CodeInternal, "构造 VOD 请求失败")
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, output.Err(output.CodeCancelled, "已取消")
		}
		oe := output.Err(output.CodeRemoteRejected, "VOD 网络请求失败")
		oe.Transport = true
		return nil, oe
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		if ctx.Err() != nil {
			return nil, output.Err(output.CodeCancelled, "已取消")
		}
		oe := output.Err(output.CodeRemoteRejected, "读取 VOD 响应失败")
		oe.Transport = true
		return nil, oe
	}
	if resp.StatusCode != http.StatusOK {
		return nil, output.Err(output.CodeRemoteRejected, "VOD 返回 HTTP 状态 %d", resp.StatusCode)
	}
	return raw, nil
}

// validUploadHost 校验直传 host：必须是 https 可用的主机名且匹配白名单
// 后缀（host == suffix 也接受，便于测试注入）。
func validUploadHost(host, suffix string) bool {
	if host == "" || strings.Contains(host, "/") || strings.Contains(host, "?") {
		return false
	}
	return host == suffix || strings.HasSuffix(host, suffix)
}

// newUploadID 生成 v4 UUID（客户端生成，无 init 请求）。
func newUploadID() (string, *output.Error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", output.Err(output.CodeInternal, "生成 uploadid 失败")
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

// traceID 生成 x-tt-trace-id（实抓样本中该头存在但值可为空；保留占位）。
func traceID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
