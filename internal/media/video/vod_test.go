package video

import (
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
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
		SliceSize:        slice,
		HTTP:             &http.Client{Transport: &rewriteTransport{mapping: map[string]string{VODAPIHost: hostOnly(vodSrv.URL), synUploadHost: hostOnly(uploadSrv.URL)}, base: http.DefaultTransport}},
		UploadHostSuffix: synUploadHost,
	})

	cred := STSCredential{AccessKeyID: synAccessKeyID, SecretAccessKey: "SKSYN", SessionToken: "STS2SYN"}
	var progress []int64
	vid, oerr := up.Upload(context.Background(), Input{
		Credential:   cred,
		CallbackArgs: synCallback,
		Reader:       strings.NewReader(string(file)),
		Size:         int64(len(file)),
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
		HTTP:             &http.Client{Transport: &rewriteTransport{mapping: map[string]string{VODAPIHost: hostOnly(vodSrv.URL)}, base: http.DefaultTransport}},
		UploadHostSuffix: synUploadHost,
	})
	_, oerr := up.Upload(context.Background(), Input{
		Credential:   STSCredential{AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "ST"},
		CallbackArgs: "cb",
		Reader:       strings.NewReader("data"),
		Size:         4,
	})
	if oerr == nil || !strings.Contains(oerr.Message, "白名单") {
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
		HTTP:             &http.Client{Transport: &rewriteTransport{mapping: map[string]string{VODAPIHost: hostOnly(vodSrv.URL), synUploadHost: hostOnly(uploadSrv.URL)}, base: http.DefaultTransport}},
		UploadHostSuffix: synUploadHost,
		MaxPartRetry:     0,
	})
	_, oerr := up.Upload(context.Background(), Input{
		Credential:   STSCredential{AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "ST"},
		CallbackArgs: "cb",
		Reader:       strings.NewReader("data"),
		Size:         4,
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
