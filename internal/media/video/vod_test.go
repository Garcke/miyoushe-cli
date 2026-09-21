package video

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mihoyo_cli/internal/output"
)

// rewriteTransport 把对固定生产 host 的 https 请求改写到本地 httptest，
// 使生产代码保持 https/host 严格校验的同时可做端到端契约测试。
type rewriteTransport struct {
	mapping map[string]string // 生产 host -> 本地 host
	base    http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if local, ok := t.mapping[req.URL.Host]; ok {
		r2 := req.Clone(req.Context())
		r2.URL.Scheme = "http"
		r2.URL.Host = local
		r2.Host = local
		return t.base.RoundTrip(r2)
	}
	return nil, fmt.Errorf("rewriteTransport: 未映射的 host %s", req.URL.Host)
}

// fakeVideoSource 是受控的 VideoSource 实现（R2）：准备摘要与状态快照
// 可独立设置，用于模拟准备后改写、追加、状态读取失败等场景。
type fakeVideoSource struct {
	r        io.ReaderAt
	size     int64
	md5Hex   string
	sha      []byte
	snapSize int64
	snapMod  time.Time
	liveSize int64
	liveMod  time.Time
	liveErr  *output.Error
}

func (f *fakeVideoSource) ReaderAt() io.ReaderAt        { return f.r }
func (f *fakeVideoSource) Size() int64                  { return f.size }
func (f *fakeVideoSource) MD5Hex() string               { return f.md5Hex }
func (f *fakeVideoSource) SHA256() []byte               { return f.sha }
func (f *fakeVideoSource) Snapshot() (int64, time.Time) { return f.snapSize, f.snapMod }
func (f *fakeVideoSource) State() (int64, time.Time, *output.Error) {
	if f.liveErr != nil {
		return 0, time.Time{}, f.liveErr
	}
	return f.liveSize, f.liveMod, nil
}

// newFakeSource 按当前内容计算准备摘要，状态快照与现值一致。
func newFakeSource(data []byte) *fakeVideoSource {
	sum := sha256.Sum256(data)
	md5sum := md5.Sum(data)
	mod := time.Unix(1700000000, 0)
	return &fakeVideoSource{
		r: bytes.NewReader(data), size: int64(len(data)),
		md5Hex: hex.EncodeToString(md5sum[:]), sha: sum[:],
		snapSize: int64(len(data)), snapMod: mod,
		liveSize: int64(len(data)), liveMod: mod,
	}
}

// newFakeSourceWithReader 使用自定义 ReaderAt（短读/改写注入），
// 准备摘要取自 data 原始内容。
func newFakeSourceWithReader(r io.ReaderAt, data []byte) *fakeVideoSource {
	src := newFakeSource(data)
	src.r = r
	return src
}

// storeAuth 是 Apply 响应里原样下发的 SpaceKey 授权（测试用合成值）。
const storeAuth = "SpaceKey/miyoushe-prod//:version:v2:SYNTHETIC"

const (
	synSessionKey  = "SYN-SESSION-KEY"
	synCallback    = `{"app_id":6,"env":"prod"}fa2251d9b4f2f29d2a65aecc758d2d6d0858d0a1f2b7f21aef767133d9d46748`
	synVid         = "v03c41g10002dahqsyn"
	synUploadHost  = "tob-upload-x-d.volcvod.com"
	synStoreURI    = "tos-vod-cn-v-syn/139136387c0c48"
	synAccessKeyID = "AKSYN0000"
)

func TestUploader_FullFlow(t *testing.T) {
	const slice = 500
	file := make([]byte, 1200)
	for i := range file {
		file[i] = byte(i % 251)
	}

	var mu sync.Mutex
	var transfers []string // part_number:offset
	var transferCRCs []string
	var finishBody string
	var commitForm url.Values

	vodSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch q.Get("Action") {
		case "ApplyUploadInfo":
			if r.Method != http.MethodGet {
				t.Errorf("Apply method = %s", r.Method)
			}
			if q.Get("SpaceName") != "miyoushe-prod" || q.Get("Version") != VODVersion {
				t.Errorf("Apply query = %v", q)
			}
			if !strings.HasPrefix(r.Header.Get("Authorization"), "HMAC-SHA256 Credential="+synAccessKeyID+"/") {
				t.Errorf("Apply Authorization = %s", r.Header.Get("Authorization"))
			}
			if r.Header.Get("x-security-token") != "STS2SYN" {
				t.Errorf("x-security-token = %s", r.Header.Get("x-security-token"))
			}
			fmt.Fprintf(w, `{"ResponseMetadata":{"RequestId":"r1","Action":"ApplyUploadInfo","Service":"vod","Region":"cn-north-1"},
				"Result":{"Data":{"UploadAddress":{"StoreInfos":[{"StoreUri":%q,"Auth":%q}],"UploadHosts":[%q],"SessionKey":%q,"Cloud":"byte"}},"SDKParam":"{}"}}`,
				synStoreURI, storeAuth, synUploadHost, synSessionKey)
		case "CommitUploadInfo":
			if r.Method != http.MethodPost {
				t.Errorf("Commit method = %s", r.Method)
			}
			body, _ := io.ReadAll(r.Body)
			commitForm = url.Values{}
			for _, kv := range strings.Split(string(body), "&") {
				k, v, _ := strings.Cut(kv, "=")
				commitForm.Set(k, v)
			}
			fmt.Fprintf(w, `{"ResponseMetadata":{"RequestId":"r2","Action":"CommitUploadInfo","Service":"vod","Region":"cn-north-1"},
				"Result":{"Data":{"Vid":%q,"SourceInfo":{"FileType":"video","Codec":"h264"}}}}`, synVid)
		default:
			t.Errorf("未知 Action %v", q)
		}
	}))
	defer vodSrv.Close()

	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upload/v1/"+synStoreURI {
			t.Errorf("upload path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("uploadid") == "" {
			t.Error("uploadid 缺失")
		}
		switch q.Get("phase") {
		case "transfer":
			if q.Get("uploadmode") != "" {
				t.Errorf("transfer 不应带 uploadmode: %v", q)
			}
			if r.Header.Get("Authorization") != storeAuth {
				t.Errorf("transfer Authorization = %s", r.Header.Get("Authorization"))
			}
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			transfers = append(transfers, q.Get("part_number")+":"+q.Get("part_offset")+":"+fmt.Sprint(len(body)))
			transferCRCs = append(transferCRCs, r.Header.Get("x-upload-content-crc32"))
			mu.Unlock()
			wantCRC := crc32.ChecksumIEEE(body)
			// 2026-09-19 实测修正：CRC 头与 finish 表统一为 8 位小写十六进制。
			if got := r.Header.Get("x-upload-content-crc32"); got != fmt.Sprintf("%08x", wantCRC) {
				t.Errorf("part %s crc header = %s, want %s", q.Get("part_number"), got, fmt.Sprintf("%08x", wantCRC))
			}
			fmt.Fprintf(w, `{"code":2000,"apiversion":"v1","message":"Success","data":{"uploadid":%q,"part_number":%q,"crc32":%q,"etag":"","meta":{"ObjectContentType":""},"mode":"normal","large_upload_id":""}}`,
				q.Get("uploadid"), q.Get("part_number"), got(wantCRC))
		case "finish":
			if q.Get("uploadmode") != "part" {
				t.Errorf("finish uploadmode = %s", q.Get("uploadmode"))
			}
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			finishBody = string(body)
			mu.Unlock()
			fmt.Fprintf(w, `{"code":2000,"apiversion":"v1","message":"Success","data":{"mode":"normal","hash":"66fb1e1d","key":"66fb1e1d","stage":""}}`)
		default:
			t.Errorf("未知 phase %v", q)
		}
	}))
	defer uploadSrv.Close()

	up := NewUploader(Config{
		SliceSize:          slice,
		HTTP:               &http.Client{Transport: &rewriteTransport{mapping: map[string]string{VODAPIHost: hostOnly(vodSrv.URL), synUploadHost: hostOnly(uploadSrv.URL)}, base: http.DefaultTransport}},
		AllowedUploadHosts: []string{synUploadHost},
	})

	cred := STSCredential{AccessKeyID: synAccessKeyID, SecretAccessKey: "SKSYN", SessionToken: "STS2SYN"}
	var progress []int64
	vid, oerr := up.Upload(context.Background(), Input{
		Credential:   cred,
		CallbackArgs: synCallback,
		Source:       newFakeSource(file),
		OnProgress:   func(sent, total int64) { progress = append(progress, sent) },
	})
	if oerr != nil {
		t.Fatalf("Upload: %v", oerr)
	}
	if vid != synVid {
		t.Errorf("vid = %s", vid)
	}

	// 分片切分：1200B / 500B → 3 片（500/500/200），编号从 0 开始。
	sort.Strings(transfers)
	want := []string{"0:0:500", "1:500:500", "2:1000:200"}
	if strings.Join(transfers, ",") != strings.Join(want, ",") {
		t.Errorf("transfers = %v", transfers)
	}
	if len(progress) != 3 || progress[2] != 1200 {
		t.Errorf("progress = %v", progress)
	}

	// finish body 是 "n:crc32hex" 全表。
	pairs := strings.Split(finishBody, ",")
	if len(pairs) != 3 {
		t.Fatalf("finish body = %s", finishBody)
	}
	for i, p := range pairs {
		n, hexSum, ok := strings.Cut(p, ":")
		if !ok || n != fmt.Sprint(i) || len(hexSum) != 8 {
			t.Errorf("finish pair %d = %q", i, p)
		}
	}

	// Commit 表单字段：SessionKey / CallbackArgs 原样透传，Functions 为抽帧指令。
	if commitForm.Get("SpaceName") != "miyoushe-prod" {
		t.Errorf("Commit SpaceName = %s", commitForm.Get("SpaceName"))
	}
	if commitForm.Get("SessionKey") != synSessionKey {
		t.Errorf("Commit SessionKey = %s", commitForm.Get("SessionKey"))
	}
	if commitForm.Get("CallbackArgs") != url.QueryEscape(synCallback) {
		t.Errorf("Commit CallbackArgs = %s", commitForm.Get("CallbackArgs"))
	}
	if commitForm.Get("Functions") != url.QueryEscape(`[{"Input":{"SnapshotTime":0.0},"Name":"Snapshot"}]`) {
		t.Errorf("Commit Functions = %s", commitForm.Get("Functions"))
	}
}

func TestUploader_RejectsForeignUploadHost(t *testing.T) {
	vodSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"ResponseMetadata":{},"Result":{"Data":{"UploadAddress":{"StoreInfos":[{"StoreUri":"x","Auth":"a"}],"UploadHosts":["evil.example.com"],"SessionKey":"s"}}}}`)
	}))
	defer vodSrv.Close()
	up := NewUploader(Config{
		HTTP:               &http.Client{Transport: &rewriteTransport{mapping: map[string]string{VODAPIHost: hostOnly(vodSrv.URL)}, base: http.DefaultTransport}},
		AllowedUploadHosts: []string{synUploadHost},
	})
	_, oerr := up.Upload(context.Background(), Input{
		Credential:   STSCredential{AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "ST"},
		CallbackArgs: "cb",
		Source:       newFakeSource([]byte("data")),
	})
	if oerr == nil || !strings.Contains(oerr.Message, "允许集合") {
		t.Errorf("oerr = %v", oerr)
	}
}

func TestUploader_TransportFailureOnCommitIsUnknown(t *testing.T) {
	vodSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("Action") == "CommitUploadInfo" {
			// 直接关闭连接，模拟结果未知。
			panic(http.ErrAbortHandler)
		}
		fmt.Fprintf(w, `{"ResponseMetadata":{},"Result":{"Data":{"UploadAddress":{"StoreInfos":[{"StoreUri":"x","Auth":"a"}],"UploadHosts":["tob-upload-x-d.volcvod.com"],"SessionKey":"s"}}}}`)
	}))
	defer vodSrv.Close()
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"code":2000,"apiversion":"v1","message":"Success","data":{"hash":"a","key":"a"}}`)
	}))
	defer uploadSrv.Close()

	up := NewUploader(Config{
		HTTP:               &http.Client{Transport: &rewriteTransport{mapping: map[string]string{VODAPIHost: hostOnly(vodSrv.URL), synUploadHost: hostOnly(uploadSrv.URL)}, base: http.DefaultTransport}},
		AllowedUploadHosts: []string{synUploadHost},
		MaxPartRetry:       0,
	})
	_, oerr := up.Upload(context.Background(), Input{
		Credential:   STSCredential{AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "ST"},
		CallbackArgs: "cb",
		Source:       newFakeSource([]byte("data")),
	})
	if oerr == nil || oerr.Code != "REMOTE_RESULT_UNKNOWN" {
		t.Errorf("oerr = %+v", oerr)
	}
}

func got(crc uint32) string { return fmt.Sprintf("%08x", crc) }

func hostOnly(srvURL string) string {
	u, _ := url.Parse(srvURL)
	return u.Host
}

func TestUploader_DefaultHTTPClientRefusesRedirect(t *testing.T) {
	// 签名请求携带 x-security-token/SpaceKey：默认客户端必须不跟随重定向，
	// 防止临时凭据随 302 发往其它主机（P2 审阅项）。
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Redirect(w, r, "https://evil.example.com/steal?x=1", http.StatusFound)
	}))
	defer srv.Close()

	up := NewUploader(Config{})
	resp, err := up.Config.HTTP.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("应原样返回 302 而非跟随重定向: %d", resp.StatusCode)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("重定向目标被请求了 %d 次", n)
	}
}

// ---------- V3 §4：主机信任边界与文件完整性 ----------

func TestUploadHostAllowSet(t *testing.T) {
	allowed := []string{"tob-upload-x-d.volcvod.com"}
	cases := []struct {
		host string
		ok   bool
	}{
		{"tob-upload-x-d.volcvod.com", true},
		{"TOB-UPLOAD-X-D.VOLCVOD.COM", true},       // 大小写规范化
		{"evil.example#x.volcvod.com", false},      // 片段伪后缀：真实主机是 evil.example
		{"user@tob-upload-x-d.volcvod.com", false}, // 用户信息
		{"tob-upload-x-d.volcvod.com/path", false}, // 路径
		{"tob-upload-x-d.volcvod.com?x=1", false},  // 查询
		{"tob-upload-x-d.volcvod.com:8443", false}, // 端口
		{"tob-upload-x-d.volcvod.com.", false},     // 尾点不放宽
		{".tob-upload-x-d.volcvod.com", false},     // 首点
		{"x..volcvod.com", false},                  // 空标签
		{"tob-upload-x-d.volcvod.com%2e", false},   // 百分号编码
		{" has space", false},
		{"a\tb", false},
		{"a\x00b", false},
		{"evilvolcvod.com", false},          // 伪后缀（无点分隔）
		{"volcvod.com.evil.example", false}, // 后缀伪装
		{"", false},
	}
	for _, c := range cases {
		norm, ok := normalizeUploadHost(c.host)
		got := ok && hostAllowed(norm, allowed)
		if got != c.ok {
			t.Errorf("host %q => %v, want %v", c.host, got, c.ok)
		}
	}
}

func TestUploader_RejectsMaliciousApplyHost(t *testing.T) {
	var uploadHits int32
	vodSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"ResponseMetadata":{"RequestId":"r1"},"Result":{"Data":{"UploadAddress":{
			"StoreInfos":[{"StoreUri":%q,"Auth":%q}],
			"UploadHosts":["evil.example#x.volcvod.com"],"SessionKey":%q,"Cloud":"byte"}}}}`,
			synStoreURI, storeAuth, synSessionKey)
	}))
	defer vodSrv.Close()
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&uploadHits, 1)
	}))
	defer uploadSrv.Close()

	up := NewUploader(Config{
		AllowedUploadHosts: []string{synUploadHost},
		HTTP: &http.Client{Transport: &rewriteTransport{mapping: map[string]string{
			VODAPIHost:    hostOnly(vodSrv.URL),
			synUploadHost: hostOnly(uploadSrv.URL),
		}, base: http.DefaultTransport}},
	})
	_, oerr := up.Upload(context.Background(), Input{
		Credential:   STSCredential{AccessKeyID: synAccessKeyID, SecretAccessKey: "SKSYN", SessionToken: "STS2SYN"},
		CallbackArgs: synCallback,
		Source:       newFakeSource(make([]byte, 1024)),
	})
	if oerr == nil {
		t.Fatal("恶意主机必须被拒绝")
	}
	if n := atomic.LoadInt32(&uploadHits); n != 0 {
		t.Errorf("任何分片都不得发送: hits=%d", n)
	}
	if strings.Contains(oerr.Message, "evil.example") {
		t.Errorf("错误消息不应回显主机: %s", oerr.Message)
	}
}

// ---------- 重定向拒绝（四类请求分别验证） ----------

func TestUploader_RefusesRedirectsOnAllRequests(t *testing.T) {
	file := make([]byte, 1200)
	for i := range file {
		file[i] = byte(i % 251)
	}

	for _, stage := range []string{"apply", "transfer", "finish", "commit"} {
		t.Run(stage, func(t *testing.T) {
			var targetHits int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&targetHits, 1)
			}))
			defer target.Close()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				switch q.Get("Action") {
				case "ApplyUploadInfo":
					if stage == "apply" {
						http.Redirect(w, r, target.URL+"/steal", http.StatusFound)
						return
					}
					fmt.Fprintf(w, `{"ResponseMetadata":{"RequestId":"r1"},"Result":{"Data":{"UploadAddress":{
						"StoreInfos":[{"StoreUri":%q,"Auth":%q}],
						"UploadHosts":[%q],"SessionKey":%q,"Cloud":"byte"}}}}`,
						synStoreURI, storeAuth, synUploadHost, synSessionKey)
					return
				case "CommitUploadInfo":
					if stage == "commit" {
						http.Redirect(w, r, target.URL+"/steal", http.StatusFound)
						return
					}
					fmt.Fprintf(w, `{"ResponseMetadata":{"RequestId":"r2"},"Result":{"Data":{"Vid":%q}}}`, synVid)
					return
				}
				if r.URL.Path == "/upload/v1/"+synStoreURI {
					switch q.Get("phase") {
					case "transfer":
						if stage == "transfer" {
							http.Redirect(w, r, target.URL+"/steal", http.StatusFound)
							return
						}
						fmt.Fprint(w, `{"code":2000,"apiversion":"v1","message":"Success","data":{"uploadid":"x","part_number":"0","crc32":"00000000","etag":"","mode":"normal"}}`)
					case "finish":
						if stage == "finish" {
							http.Redirect(w, r, target.URL+"/steal", http.StatusFound)
							return
						}
						fmt.Fprint(w, `{"code":2000,"apiversion":"v1","message":"Success","data":{"mode":"normal","hash":"66fb1e1d","key":"66fb1e1d"}}`)
					}
					return
				}
				t.Errorf("未知请求: %s %s", r.Method, r.URL)
			}))
			defer srv.Close()

			up := NewUploader(Config{
				SliceSize:          500,
				AllowedUploadHosts: []string{synUploadHost},
				// 故意不设置 CheckRedirect：验证 Uploader 自身强制禁重定向。
				// 映射包含重定向目标，若发生跟随则测试会发现次生请求。
				HTTP: &http.Client{Transport: &rewriteTransport{mapping: map[string]string{
					VODAPIHost:           hostOnly(srv.URL),
					synUploadHost:        hostOnly(srv.URL),
					hostOnly(target.URL): hostOnly(target.URL),
				}, base: http.DefaultTransport}},
			})
			_, oerr := up.Upload(context.Background(), Input{
				Credential:   STSCredential{AccessKeyID: synAccessKeyID, SecretAccessKey: "SKSYN", SessionToken: "STS2SYN"},
				CallbackArgs: synCallback,
				Source:       newFakeSource(file),
			})
			if oerr == nil {
				t.Fatalf("阶段 %s 的 3xx 必须失败，不得跟随重定向", stage)
			}
			if n := atomic.LoadInt32(&targetHits); n != 0 {
				t.Errorf("重定向目标被请求 %d 次（授权头可能外流）", n)
			}
			if strings.Contains(oerr.Message, storeAuth) || strings.Contains(oerr.Message, "STS2SYN") {
				t.Errorf("错误消息泄露授权信息: %s", oerr.Message)
			}
		})
	}
}

// ---------- 文件完整性（V3 §4.3） ----------

// shortReaderAt 在指定偏移上返回短读（n < len(p)）。
type shortReaderAt struct {
	data    []byte
	shortAt int64
	err     error // 短读时同时返回的错误（nil 或 io.EOF）
}

func (s *shortReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off == s.shortAt && len(p) > 1 {
		n, _ := bytes.NewReader(s.data).ReadAt(p[:len(p)-1], off)
		return n, s.err
	}
	return bytes.NewReader(s.data).ReadAt(p, off)
}

// mutatingReaderAt 在预读完成后的第二遍读取中改写字节，模拟同大小文件改写。
type mutatingReaderAt struct {
	data  []byte
	total int64
	size  int64
}

func (m *mutatingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := bytes.NewReader(m.data).ReadAt(p, off)
	m.total += int64(n)
	if m.total > m.size { // 第二遍：上传阶段
		for i := range p[:n] {
			p[i] ^= 0xFF
		}
	}
	return n, err
}

func newIntegrityTestUploader(t *testing.T, srv *httptest.Server) *Uploader {
	t.Helper()
	return NewUploader(Config{
		SliceSize:          500,
		AllowedUploadHosts: []string{synUploadHost},
		HTTP: &http.Client{Transport: &rewriteTransport{mapping: map[string]string{
			VODAPIHost:    hostOnly(srv.URL),
			synUploadHost: hostOnly(srv.URL),
		}, base: http.DefaultTransport}},
	})
}

func newIntegrityTestServer(t *testing.T, transfers, finishes, commits *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch q.Get("Action") {
		case "ApplyUploadInfo":
			fmt.Fprintf(w, `{"ResponseMetadata":{"RequestId":"r1"},"Result":{"Data":{"UploadAddress":{
				"StoreInfos":[{"StoreUri":%q,"Auth":%q}],
				"UploadHosts":[%q],"SessionKey":%q,"Cloud":"byte"}}}}`,
				synStoreURI, storeAuth, synUploadHost, synSessionKey)
		case "CommitUploadInfo":
			atomic.AddInt32(commits, 1)
			fmt.Fprintf(w, `{"ResponseMetadata":{"RequestId":"r2"},"Result":{"Data":{"Vid":%q}}}`, synVid)
		default:
			if r.URL.Path == "/upload/v1/"+synStoreURI {
				switch q.Get("phase") {
				case "transfer":
					atomic.AddInt32(transfers, 1)
					fmt.Fprint(w, `{"code":2000,"apiversion":"v1","message":"Success","data":{"uploadid":"x","part_number":"0","crc32":"00000000","etag":"","mode":"normal"}}`)
				case "finish":
					atomic.AddInt32(finishes, 1)
					fmt.Fprint(w, `{"code":2000,"apiversion":"v1","message":"Success","data":{"mode":"normal","hash":"66fb1e1d","key":"66fb1e1d"}}`)
				}
				return
			}
			t.Errorf("未知请求: %s %s", r.Method, r.URL)
		}
	}))
}

func TestUploader_ShortReadFailsClosed(t *testing.T) {
	base := make([]byte, 1600)
	for i := range base {
		base[i] = byte(i % 251)
	}

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"短读+io.EOF", io.EOF},
		{"短读+nil", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var transfers, finishes, commits int32
			srv := newIntegrityTestServer(t, &transfers, &finishes, &commits)
			defer srv.Close()

			up := newIntegrityTestUploader(t, srv)
			// 分片大小 500：分片 0@0、1@500（短读）、2@1000、3@1500。
			reader := &shortReaderAt{data: base, shortAt: 500, err: tc.err}
			_, oerr := up.Upload(context.Background(), Input{
				Credential:   STSCredential{AccessKeyID: synAccessKeyID, SecretAccessKey: "SKSYN", SessionToken: "STS2SYN"},
				CallbackArgs: synCallback,
				Source:       newFakeSourceWithReader(reader, base),
			})
			if oerr == nil {
				t.Fatal("短读必须失败关闭")
			}
			if oerr.Code != output.CodeContentConflict {
				t.Errorf("code = %s, want CONTENT_CONFLICT", oerr.Code)
			}
			// 分片 0（偏移 0）完整读取并已发送；偏移 500 起的短读分片
			// 必须在发送前失败关闭，其后的分片一个都不发。
			if n := atomic.LoadInt32(&transfers); n != 1 {
				t.Errorf("只允许发送短读前的分片 0: transfers=%d", n)
			}
			if atomic.LoadInt32(&finishes) != 0 || atomic.LoadInt32(&commits) != 0 {
				t.Error("短读后不得调用 finish/Commit")
			}
		})
	}
}

func TestUploader_HashMismatchOnSameSizeRewrite(t *testing.T) {
	base := make([]byte, 1600)
	for i := range base {
		base[i] = byte(i % 251)
	}
	var transfers, finishes, commits int32
	srv := newIntegrityTestServer(t, &transfers, &finishes, &commits)
	defer srv.Close()

	up := newIntegrityTestUploader(t, srv)
	reader := &mutatingReaderAt{data: base, size: int64(len(base))}
	_, oerr := up.Upload(context.Background(), Input{
		Credential:   STSCredential{AccessKeyID: synAccessKeyID, SecretAccessKey: "SKSYN", SessionToken: "STS2SYN"},
		CallbackArgs: synCallback,
		Source:       newFakeSourceWithReader(reader, base),
	})
	if oerr == nil {
		t.Fatal("预读后改写必须失败关闭")
	}
	if oerr.Code != output.CodeContentConflict {
		t.Errorf("code = %s, want CONTENT_CONFLICT", oerr.Code)
	}
	if n := atomic.LoadInt32(&transfers); n == 0 {
		t.Error("分片已按读取内容上传（本测试预期上传发生后才发现摘要不一致）")
	}
	if atomic.LoadInt32(&finishes) != 0 || atomic.LoadInt32(&commits) != 0 {
		t.Error("摘要不一致时不得调用 finish/Commit")
	}
	if !strings.Contains(oerr.Message, "SHA-256") {
		t.Errorf("错误应可定位为摘要不一致: %s", oerr.Message)
	}
}

// ---------- R2：准备阶段与上传阶段的一致性 ----------

func writeTempVideo(t *testing.T) string {
	t.Helper()
	data := buildMP4("isom", 600, 27967800)
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func countAllStages(t *testing.T) (*httptest.Server, *int32, *int32, *int32, *int32) {
	t.Helper()
	var applies, transfers, finishes, commits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch q.Get("Action") {
		case "ApplyUploadInfo":
			atomic.AddInt32(&applies, 1)
			fmt.Fprintf(w, `{"ResponseMetadata":{"RequestId":"r1"},"Result":{"Data":{"UploadAddress":{
				"StoreInfos":[{"StoreUri":%q,"Auth":%q}],
				"UploadHosts":[%q],"SessionKey":%q,"Cloud":"byte"}}}}`,
				synStoreURI, storeAuth, synUploadHost, synSessionKey)
		case "CommitUploadInfo":
			atomic.AddInt32(&commits, 1)
			fmt.Fprintf(w, `{"ResponseMetadata":{"RequestId":"r2"},"Result":{"Data":{"Vid":%q}}}`, synVid)
		default:
			if r.URL.Path == "/upload/v1/"+synStoreURI {
				switch q.Get("phase") {
				case "transfer":
					atomic.AddInt32(&transfers, 1)
					fmt.Fprint(w, `{"code":2000,"apiversion":"v1","message":"Success","data":{"uploadid":"x","part_number":"0","crc32":"00000000","etag":"","mode":"normal"}}`)
				case "finish":
					atomic.AddInt32(&finishes, 1)
					fmt.Fprint(w, `{"code":2000,"apiversion":"v1","message":"Success","data":{"mode":"normal","hash":"66fb1e1d","key":"66fb1e1d"}}`)
				}
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &applies, &transfers, &finishes, &commits
}

func assertNoStageHits(t *testing.T, applies, transfers, finishes, commits int32) {
	t.Helper()
	if applies != 0 || transfers != 0 || finishes != 0 || commits != 0 {
		t.Errorf("准备基线不一致时不得发生任何网络阶段: apply=%d transfer=%d finish=%d commit=%d",
			applies, transfers, finishes, commits)
	}
}

func TestUploader_RealFileRewriteAfterPrepare(t *testing.T) {
	// 真实临时文件：准备后同尺寸改写并恢复修改时间；打开句柄看到新内容，
	// 与准备摘要不符 → Apply/transfer/finish/Commit 全部不得发生。
	path := writeTempVideo(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	origMod := info.ModTime()

	pv, oerr := PrepareVideo(context.Background(), path)
	if oerr != nil {
		t.Fatalf("PrepareVideo: %v", oerr)
	}
	defer pv.Close()
	if pv.SHA256() == nil || pv.Size() == 0 {
		t.Fatal("准备结果缺少摘要/大小")
	}

	srv, applies, transfers, finishes, commits := countAllStages(t)
	up := newIntegrityTestUploader(t, srv)

	// 同尺寸改写 + 恢复 mtime（状态复核无法发现，只能靠摘要）。
	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rewritten[len(rewritten)-1] ^= 0xFF
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, origMod, origMod); err != nil {
		t.Fatal(err)
	}

	_, oerr = up.Upload(context.Background(), Input{
		Credential:   STSCredential{AccessKeyID: synAccessKeyID, SecretAccessKey: "SKSYN", SessionToken: "STS2SYN"},
		CallbackArgs: synCallback,
		Source:       pv,
	})
	if oerr == nil || oerr.Code != output.CodeContentConflict {
		t.Fatalf("准备后改写应 CONTENT_CONFLICT: %+v", oerr)
	}
	assertNoStageHits(t, atomic.LoadInt32(applies), atomic.LoadInt32(transfers), atomic.LoadInt32(finishes), atomic.LoadInt32(commits))
}

func TestUploader_RealFileAppendAfterPrepare(t *testing.T) {
	// 准备后追加：即使原大小范围内字节未变，也因大小/状态复核失败而停止。
	path := writeTempVideo(t)
	pv, oerr := PrepareVideo(context.Background(), path)
	if oerr != nil {
		t.Fatalf("PrepareVideo: %v", oerr)
	}
	defer pv.Close()

	srv, applies, transfers, finishes, commits := countAllStages(t)
	up := newIntegrityTestUploader(t, srv)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("TAIL")); err != nil {
		t.Fatal(err)
	}
	f.Close()

	_, oerr = up.Upload(context.Background(), Input{
		Credential:   STSCredential{AccessKeyID: synAccessKeyID, SecretAccessKey: "SKSYN", SessionToken: "STS2SYN"},
		CallbackArgs: synCallback,
		Source:       pv,
	})
	if oerr == nil || oerr.Code != output.CodeContentConflict {
		t.Fatalf("准备后追加应 CONTENT_CONFLICT: %+v", oerr)
	}
	assertNoStageHits(t, atomic.LoadInt32(applies), atomic.LoadInt32(transfers), atomic.LoadInt32(finishes), atomic.LoadInt32(commits))
}

// appendDuringUploadSource 在第二次状态读取时报告更大的文件（模拟上传末期追加）。
type appendDuringUploadSource struct {
	*fakeVideoSource
	stateCalls int32
}

func (a *appendDuringUploadSource) State() (int64, time.Time, *output.Error) {
	if atomic.AddInt32(&a.stateCalls, 1) >= 2 {
		return a.snapSize + 4, a.snapMod, nil
	}
	return a.snapSize, a.snapMod, nil
}

func TestUploader_AppendDetectedBeforeFinish(t *testing.T) {
	// 预读后追加、原大小范围字节未变：分片已上传且累计摘要与基线相同，
	// 但 finish 前的状态复核发现大小变化 → 不 finish/Commit。
	data := make([]byte, 1600)
	for i := range data {
		data[i] = byte(i % 251)
	}
	src := &appendDuringUploadSource{fakeVideoSource: newFakeSource(data)}

	srv, _, transfers, finishes, commits := countAllStages(t)
	up := newIntegrityTestUploader(t, srv)

	_, oerr := up.Upload(context.Background(), Input{
		Credential:   STSCredential{AccessKeyID: synAccessKeyID, SecretAccessKey: "SKSYN", SessionToken: "STS2SYN"},
		CallbackArgs: synCallback,
		Source:       src,
	})
	if oerr == nil || oerr.Code != output.CodeContentConflict {
		t.Fatalf("末期追加应 CONTENT_CONFLICT: %+v", oerr)
	}
	if atomic.LoadInt32(transfers) == 0 {
		t.Error("本场景预期分片已上传后才发现追加")
	}
	if atomic.LoadInt32(finishes) != 0 || atomic.LoadInt32(commits) != 0 {
		t.Errorf("追加后不得 finish/Commit: finish=%d commit=%d",
			atomic.LoadInt32(finishes), atomic.LoadInt32(commits))
	}
}

func TestUploader_MissingBaselineRejected(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer srv.Close()
	up := newIntegrityTestUploader(t, srv)

	src := newFakeSource([]byte("data"))
	src.sha = nil // 缺准备阶段基线
	_, oerr := up.Upload(context.Background(), Input{
		Credential:   STSCredential{AccessKeyID: synAccessKeyID, SecretAccessKey: "SKSYN", SessionToken: "STS2SYN"},
		CallbackArgs: synCallback,
		Source:       src,
	})
	if oerr == nil || oerr.Code != output.CodeInputInvalid {
		t.Fatalf("缺基线应 INPUT_INVALID: %+v", oerr)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Errorf("缺基线不得发出请求: hits=%d", n)
	}
}

func TestPrepareVideo_CancelAndHandleOwnership(t *testing.T) {
	path := writeTempVideo(t)

	// 取消：循环停止，返回 CANCELLED，且失败路径已关闭句柄（可删除文件）。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, oerr := PrepareVideo(ctx, path); oerr == nil || oerr.Code != output.CodeCancelled {
		t.Fatalf("取消应返回 CANCELLED: %+v", oerr)
	}
	if err := os.Remove(path); err != nil {
		t.Errorf("准备失败后句柄应已关闭（可删除文件）: %v", err)
	}
}

func TestUploader_HandleOwnershipAfterFailure(t *testing.T) {
	path := writeTempVideo(t)
	pv, oerr := PrepareVideo(context.Background(), path)
	if oerr != nil {
		t.Fatalf("PrepareVideo: %v", oerr)
	}
	// 人为制造基线不符（同尺寸改写文件内容）。
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xFF
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	srv, _, _, _, _ := countAllStages(t)
	up := newIntegrityTestUploader(t, srv)
	if _, oerr := up.Upload(context.Background(), Input{
		Credential:   STSCredential{AccessKeyID: synAccessKeyID, SecretAccessKey: "SKSYN", SessionToken: "STS2SYN"},
		CallbackArgs: synCallback,
		Source:       pv,
	}); oerr == nil {
		t.Fatal("基线不符应失败")
	}
	// 失败后调用方 Close，句柄按既定所有权释放，文件可清理。
	if err := pv.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := pv.Close(); err != nil {
		t.Errorf("重复 Close 应幂等: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Errorf("Close 后应可删除文件: %v", err)
	}
}
