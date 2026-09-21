// vod.go 实现火山 VOD 直传状态机：ApplyUploadInfo → /upload/v1 part
// transfer → finish → CommitUploadInfo。
//
// 协议要点（全部有 2026-09-11 实抓样本背书，见
// docs/architecture/video-upload-protocol.md §8）：
//   - Apply/Commit 走 vod.volcengineapi.com，SigV4 签名（service=vod）；
//   - 直传 Authorization 为 Apply 下发的 StoreInfos[0].Auth（SpaceKey
//     JWT）原样，不做 SigV4；
//   - uploadid 是客户端生成的 UUID，无 init 请求；
//   - 分片 CRC32 为 IEEE 多项式，x-upload-content-crc32 头与 finish 表统一为 8 位小写十六进制（2026-09-19 实测修正，此前十进制记录系误判）；
//   - finish body 为 "part_number:crc32hex" 全表；
//   - CallbackArgs / SessionKey / StoreInfos[0].Auth 一律原样透传。
package video

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
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

// DefaultUploadHosts 是生产放行的直传主机**精确集合**（仅已实测主机）。
// 不再使用后缀匹配：`evil.example#x.volcvod.com` 之类字符串能通过后缀检查，
// 但 URL 解析后的真实网络主机是 `evil.example`（ARCHITECTURE-V3 §4.2）。
// 若真实 Apply 响应出现新主机，先失败关闭并记录脱敏证据，审阅后再更新集合。
var DefaultUploadHosts = []string{"tob-upload-x-d.volcvod.com"}

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
	// AllowedUploadHosts 覆盖允许的直传主机精确集合；仅测试注入，
	// 不对用户暴露为可配置项（V3 §4.2）。
	AllowedUploadHosts []string
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
	// 即使注入的客户端默认会跟随 3xx，签名/STS/SpaceKey 授权也不得被带往
	// 第二个主机（V3 §4.2 第 4 条）：统一包一层禁重定向副本。
	cfg.HTTP = withoutRedirects(cfg.HTTP)
	if len(cfg.AllowedUploadHosts) == 0 {
		cfg.AllowedUploadHosts = DefaultUploadHosts
	}
	return &Uploader{Config: cfg}
}

// VideoSource 是上传使用的已准备媒体源（R2）：来源一致的 ReaderAt 句柄、
// 准备阶段摘要与文件状态快照。生产实现是 *PreparedVideo；测试可注入受控实现。
// 缺少 SHA-256 基线的来源一律拒绝，不存在“缺了就重新建基线”的上传退路。
type VideoSource interface {
	ReaderAt() io.ReaderAt
	Size() int64
	MD5Hex() string
	SHA256() []byte
	// Snapshot 返回准备阶段记录的文件大小与修改时间。
	Snapshot() (int64, time.Time)
	// State 返回调用时的当前文件状态；无底层文件时返回与 Snapshot 相同值。
	State() (int64, time.Time, *output.Error)
}

// Input 是一次上传的输入。
type Input struct {
	// Credential 来自 getToken 的 token 字符串（ParseUploadToken）。
	Credential STSCredential
	// CallbackArgs 为 getToken 返回的 callback_args，原样透传。
	CallbackArgs string
	// Source 是准备阶段的已准备视频源（必填）：句柄、摘要与状态快照。
	Source VideoSource
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
	if in.Source == nil {
		return "", output.Err(output.CodeInputInvalid, "缺少已准备视频源（必须先完成 PrepareVideo）")
	}
	size := in.Source.Size()
	if size <= 0 {
		return "", output.Err(output.CodeInputInvalid, "上传内容无效")
	}
	// 必须携带准备阶段的 SHA-256 基线；缺基线不接受重新建立。
	expected := in.Source.SHA256()
	if len(expected) != sha256.Size {
		return "", output.Err(output.CodeInputInvalid, "来源缺少准备阶段 SHA-256 基线，拒绝上传")
	}
	if in.CallbackArgs == "" {
		return "", output.Err(output.CodeInputInvalid, "callback_args 缺失")
	}
	reader := in.Source.ReaderAt()
	if reader == nil {
		return "", output.Err(output.CodeInputInvalid, "来源缺少可读句柄")
	}

	// 上传前复核文件状态（大小/修改时间；早期变化信号）。
	if oerr := checkSourceState(in.Source, "上传前"); oerr != nil {
		return "", oerr
	}

	slice := sliceSize(u.Config.SliceSize, size)
	uploadID, oerr := newUploadID()
	if oerr != nil {
		return "", oerr
	}

	// 预读整文件 SHA-256，并与准备阶段基线比较：不一致在 Apply/首个分片
	// 之前停止（R2）。预读自身不是新基线。
	preHash, oerr := hashReaderAt(ctx, reader, size)
	if oerr != nil {
		return "", oerr
	}
	if !bytes.Equal(preHash, expected) {
		return "", output.Err(output.CodeContentConflict,
			"文件内容与准备阶段摘要不符（SHA-256 不一致），已停止上传，未发送任何分片")
	}
	upHash := sha256.New()

	apply, oerr := u.apply(ctx, in.Credential)
	if oerr != nil {
		return "", oerr
	}
	addr := apply.Result.Data.UploadAddress
	if len(addr.StoreInfos) == 0 || len(addr.UploadHosts) == 0 {
		return "", output.Err(output.CodeRemoteRejected, "Apply 响应缺少 StoreInfos 或 UploadHosts")
	}
	store := addr.StoreInfos[0]
	uploadHost, ok := normalizeUploadHost(addr.UploadHosts[0])
	if !ok || !hostAllowed(uploadHost, u.Config.AllowedUploadHosts) {
		return "", output.Err(output.CodeRemoteRejected, "Apply 返回的上传主机不在允许集合内")
	}

	// 分片直传。
	parts := (size + slice - 1) / slice
	var sent int64
	finishPairs := make([]string, 0, parts)
	for n := int64(0); n < parts; n++ {
		offset := n * slice
		partSize := slice
		if offset+partSize > size {
			partSize = size - offset
		}
		buf := make([]byte, partSize)
		read, rerr := reader.ReadAt(buf, offset)
		if read != len(buf) || (rerr != nil && rerr != io.EOF) {
			// 短读（含 io.EOF）视为本地文件变更/读取失败：不发送该分片、
			// 不调用 finish/Commit，也不以零字节填充继续（V3 §4.3）。
			return "", output.Err(output.CodeContentConflict,
				"分片 %d 读取不完整（%d/%d 字节），本地文件可能已变化，已停止上传", n, read, len(buf))
		}
		upHash.Write(buf)
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
		sent += int64(partSize)
		if in.OnProgress != nil {
			in.OnProgress(sent, size)
		}
	}

	// finish 前再次复核文件状态并比对上传累计摘要与准备阶段基线：
	// 任一项不一致都停止，不发送 finish/Commit，也不进入 getVideoID。
	if oerr := checkSourceState(in.Source, "finish 前"); oerr != nil {
		return "", oerr
	}
	if !bytes.Equal(expected, upHash.Sum(nil)) {
		return "", output.Err(output.CodeContentConflict,
			"本地文件在预读后发生变化（SHA-256 不一致），已停止上传，未登记媒体")
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
	endpoint, oerr := buildUploadEndpoint(host, store.StoreURI, transferQuery(uploadID, part, offset))
	if oerr != nil {
		return oerr
	}
	hm := http.Header{}
	hm.Set("Authorization", store.Auth)
	hm.Set("x-upload-content-crc32", fmt.Sprintf("%08x", sum))
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
	endpoint, oerr := buildUploadEndpoint(host, store.StoreURI,
		(url.Values{"uploadid": {uploadID}, "uploadmode": {"part"}, "phase": {"finish"}}).Encode())
	if oerr != nil {
		return "", oerr
	}
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

// normalizeUploadHost 校验并规范化直传主机名：只接受纯 DNS 主机名
// （字母/数字/连字符/点），拒绝空白、控制字符、#、?、/、反斜杠、@、冒号/端口、
// 百分号编码、用户信息、片段、空标签与首尾点；大小写统一为小写。
// 不通过增删尾点放宽匹配（V3 §4.2 第 1 条）。
func normalizeUploadHost(host string) (string, bool) {
	if host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return "", false
	}
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
		case r >= 'A' && r <= 'Z':
		default:
			return "", false
		}
	}
	return strings.ToLower(host), true
}

// hostAllowed 精确集合匹配（规范化后比较）。
func hostAllowed(host string, allowed []string) bool {
	for _, a := range allowed {
		if host == strings.ToLower(a) {
			return true
		}
	}
	return false
}

// buildUploadEndpoint 用已批准主机结构化构造直传 URL，并在返回前复核最终
// 目的地：https、主机名与批准值完全一致、无端口/用户信息/片段。
// StoreUri 只作为路径数据，不参与主机拼接（V3 §4.2 第 3 条）。
func buildUploadEndpoint(host, storeURI, rawQuery string) (string, *output.Error) {
	u := url.URL{
		Scheme:   "https",
		Host:     host,
		Path:     "/upload/v1/" + storeURI,
		RawQuery: rawQuery,
	}
	if u.Scheme != "https" || u.Hostname() != host || u.Port() != "" ||
		u.User != nil || u.Fragment != "" {
		return "", output.Err(output.CodeRemoteRejected, "上传地址构造校验失败")
	}
	return u.String(), nil
}

// withoutRedirects 返回不跟随重定向的客户端副本：即使调用方注入的客户端
// 默认会跟随 3xx，签名/STS/上传授权也不得被带到第二个主机。
func withoutRedirects(c *http.Client) *http.Client {
	cp := *c
	cp.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &cp
}

// checkSourceState 复核来源的当前文件状态与准备阶段快照一致（大小/修改时间）。
// 状态只是早期变化信号，摘要比对才是权威判据（R2）。
func checkSourceState(src VideoSource, stage string) *output.Error {
	expSize, expMod := src.Snapshot()
	size, mod, oerr := src.State()
	if oerr != nil {
		return oerr
	}
	if size != expSize || !mod.Equal(expMod) {
		return output.Err(output.CodeContentConflict,
			"本地视频在准备后发生变化（%s状态复核失败），已停止上传", stage)
	}
	return nil
}

// hashReaderAt 从同一 ReaderAt 预读整文件 SHA-256；短读即失败（V3 §4.3）；
// 循环检查 ctx，取消返回 CANCELLED。
func hashReaderAt(ctx context.Context, r io.ReaderAt, size int64) ([]byte, *output.Error) {
	h := sha256.New()
	buf := make([]byte, 1<<20)
	var off int64
	for off < size {
		if ctx.Err() != nil {
			return nil, output.Err(output.CodeCancelled, "已取消")
		}
		n := int64(len(buf))
		if remain := size - off; remain < n {
			n = remain
		}
		read, err := r.ReadAt(buf[:n], off)
		if int64(read) != n || (err != nil && err != io.EOF) {
			return nil, output.Err(output.CodeContentConflict,
				"预读文件失败（%d/%d 字节），本地文件可能已变化", read, n)
		}
		h.Write(buf[:n])
		off += n
	}
	return h.Sum(nil), nil
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
