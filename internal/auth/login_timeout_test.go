package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/store"
)

// slowSetupTransport makes the QR creation request block until its context is
// canceled. This verifies that the login timeout covers setup, not only polling.
type slowSetupTransport struct{}

func (slowSetupTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	select {
	case <-r.Context().Done():
		return nil, r.Context().Err()
	case <-time.After(250 * time.Millisecond):
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{"retcode":0,"data":{"url":"https://example.test/qr?ticket=t","ticket":"t"}}`)),
		}, nil
	}
}

func TestLogin_TotalTimeoutIncludesQRSetup(t *testing.T) {
	client, err := api.New("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP.Transport = slowSetupTransport{}
	storeDir := t.TempDir()
	st := &store.Store{Dir: storeDir}
	if err := st.Save(store.NewCredentials(synUID, synMID, synSToken, synDeviceID, synFP, time.Now())); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: st, QRClient: client, ExClient: client, FPClient: client}
	start := time.Now()
	_, oerr := svc.Login(context.Background(), Config{Timeout: 40 * time.Millisecond, RequestTimeout: time.Second}, &fakeRenderer{}, nil)
	if oerr == nil || oerr.Code != output.CodeLoginTimeout {
		t.Fatalf("slow QR setup should return LOGIN_TIMEOUT: %+v", oerr)
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("login exceeded total timeout: %s", elapsed)
	}
}

func TestLoginPassport_TotalTimeoutIncludesQRSetup(t *testing.T) {
	client, err := api.New("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP.Transport = slowSetupTransport{}
	st := &store.Store{Dir: t.TempDir()}
	if err := st.Save(store.NewCredentialsFlow(store.FlowV2, synUID, synMID, synSToken, synDeviceID, synFP, time.Now())); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: st, PassportClient: client, FPClient: client}
	start := time.Now()
	_, oerr := svc.LoginPassport(context.Background(), Config{Timeout: 40 * time.Millisecond, RequestTimeout: time.Second}, &fakeRenderer{}, nil)
	if oerr == nil || oerr.Code != output.CodeLoginTimeout {
		t.Fatalf("slow QR setup should return LOGIN_TIMEOUT: %+v", oerr)
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("passport login exceeded total timeout: %s", elapsed)
	}
}

// ---------- 总超时 / 主动取消的分类（修复方案 §3–§6） ----------

func TestLoginFlowError_Classification(t *testing.T) {
	timeout := 5 * time.Minute
	cases := []struct {
		name string
		err  error
		code string
		exit int
	}{
		{"总时限到期", context.DeadlineExceeded, output.CodeLoginTimeout, output.ExitAuth},
		{"用户取消", context.Canceled, output.CodeCancelled, output.ExitInternal},
		{"意外终止", errors.New("boom"), output.CodeInternal, output.ExitInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oerr := loginFlowError(tc.err, timeout)
			if oerr.Code != tc.code || oerr.Exit != tc.exit {
				t.Fatalf("code=%s exit=%d, want %s/%d", oerr.Code, oerr.Exit, tc.code, tc.exit)
			}
			if tc.code == output.CodeLoginTimeout {
				if !strings.Contains(oerr.Message, "5m0s") {
					t.Errorf("超时消息应含总时限: %s", oerr.Message)
				}
				if strings.Contains(oerr.Message, "已取消") {
					t.Errorf("超时消息不得混入“已取消”: %s", oerr.Message)
				}
			}
		})
	}
}

// blockedPollTransport 让扫码查询请求阻塞到请求上下文结束，
// 用于确定性验证“轮询请求期间总时限到期”的分类，不依赖调度时序。
type blockedPollTransport struct{}

func (blockedPollTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	reply := func(body string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/device-fp/api/getFp"):
		return reply(`{"retcode":0,"message":"OK","data":{"device_fp":"` + synFP + `","code":200,"msg":"ok"}}`)
	case strings.HasSuffix(r.URL.Path, "/combo/panda/qrcode/fetch"):
		return reply(`{"retcode":0,"message":"OK","data":{"url":"https://user.mihoyo.com/qr_code_in_game.html?app_id=12&ticket=` + ticketSyn + `&expire=1893456000"}}`)
	case strings.HasSuffix(r.URL.Path, "/app/createQRLogin"):
		return reply(`{"retcode":0,"message":"OK","data":{"url":"https://user.mihoyo.com/login-platform/mobile.html?tk=` + ticketSyn + `&token_types=1","ticket":"` + ticketSyn + `"}}`)
	}
	// 扫码查询：阻塞到请求上下文结束（总时限或取消）。
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func blockedPollClient(t *testing.T) *api.Client {
	t.Helper()
	client, err := api.New("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP.Transport = blockedPollTransport{}
	return client
}

func TestLogin_TimeoutDuringPollRequest(t *testing.T) {
	// 轮询请求在途时总时限到期：Timeout < RequestTimeout，请求被总时限打断。
	client := blockedPollClient(t)
	svc := &Service{Store: &store.Store{Dir: t.TempDir()}, QRClient: client, ExClient: client, FPClient: client}
	start := time.Now()
	_, oerr := svc.Login(context.Background(),
		Config{Timeout: 120 * time.Millisecond, PollInterval: time.Millisecond, RequestTimeout: 5 * time.Second, MaxPollFails: 3},
		&fakeRenderer{}, nil)
	if oerr == nil || oerr.Code != output.CodeLoginTimeout {
		t.Fatalf("轮询请求期间超时应 LOGIN_TIMEOUT: %+v", oerr)
	}
	if oerr.Exit != output.ExitAuth {
		t.Errorf("退出码 = %d, want %d", oerr.Exit, output.ExitAuth)
	}
	if strings.Contains(oerr.Message, "已取消") {
		t.Errorf("超时消息不得混入“已取消”: %s", oerr.Message)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("未在总时限附近返回: %s", elapsed)
	}
}

func TestLoginPassport_TimeoutDuringPollRequest(t *testing.T) {
	client := blockedPollClient(t)
	svc := &Service{Store: &store.Store{Dir: t.TempDir()}, FPClient: client, PassportClient: client}
	_, oerr := svc.LoginPassport(context.Background(),
		Config{Timeout: 120 * time.Millisecond, PollInterval: time.Millisecond, RequestTimeout: 5 * time.Second, MaxPollFails: 3},
		&fakeRenderer{}, nil)
	if oerr == nil || oerr.Code != output.CodeLoginTimeout {
		t.Fatalf("Passport 轮询请求期间超时应 LOGIN_TIMEOUT: %+v", oerr)
	}
	if strings.Contains(oerr.Message, "已取消") {
		t.Errorf("超时消息不得混入“已取消”: %s", oerr.Message)
	}
}

func TestLogin_TimeoutDuringPollSleep(t *testing.T) {
	// PollInterval > Timeout：正常轮询间隔的休眠被总时限打断，仍应 LOGIN_TIMEOUT。
	st := &serverState{queryResps: []string{statResp("Init")}, exchResp: exchangeOK}
	svc, _, _ := newTestService(t, st)
	_, oerr := svc.Login(context.Background(),
		Config{Timeout: 100 * time.Millisecond, PollInterval: 10 * time.Second, RequestTimeout: 500 * time.Millisecond, MaxPollFails: 3},
		&fakeRenderer{}, nil)
	if oerr == nil || oerr.Code != output.CodeLoginTimeout {
		t.Fatalf("休眠期间超时应 LOGIN_TIMEOUT: %+v", oerr)
	}
}

func TestLoginPassport_TimeoutDuringPollSleep(t *testing.T) {
	st := &passportServerState{queryResps: []string{passportStatusResp("Created")}}
	svc, _, _ := newPassportService(t, st)
	_, oerr := svc.LoginPassport(context.Background(),
		Config{Timeout: 100 * time.Millisecond, PollInterval: 10 * time.Second, RequestTimeout: 500 * time.Millisecond, MaxPollFails: 3},
		&fakeRenderer{}, nil)
	if oerr == nil || oerr.Code != output.CodeLoginTimeout {
		t.Fatalf("Passport 休眠期间超时应 LOGIN_TIMEOUT: %+v", oerr)
	}
}

func TestPassportLogin_Cancel(t *testing.T) {
	st := &passportServerState{queryResps: []string{passportStatusResp("Created")}}
	svc, _, _ := newPassportService(t, st)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	_, oerr := svc.LoginPassport(ctx, fastCfg(), &fakeRenderer{}, nil)
	if oerr == nil || oerr.Code != output.CodeCancelled {
		t.Fatalf("主动取消应 CANCELLED: %+v", oerr)
	}
	if oerr.Exit != output.ExitInternal {
		t.Errorf("CANCELLED 退出码 = %d, want %d", oerr.Exit, output.ExitInternal)
	}
	if !strings.Contains(oerr.Message, "用户已取消登录") {
		t.Errorf("取消文案不符: %s", oerr.Message)
	}
}
