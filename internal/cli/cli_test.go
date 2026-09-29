package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/auth"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/store"
	"net/url"
	"sync"
)

// testEnv 组装命令层测试环境：单一 httptest 服务器伪装全部上游。
type testEnv struct {
	root *cobra.Command
	deps Deps
	out  *bytes.Buffer
	errb *bytes.Buffer
	srv  *httptest.Server
	// log 是本环境私有的请求记录器（R4）：不跨 testEnv 共享。
	log *requestLog
	// discussionBody 覆盖讨论区响应体；为空用默认完整样本。
	discussionBody string
	// gamesBody、imageTypesBody 覆盖论坛目录与图片榜单分类响应。
	gamesBody      string
	imageTypesBody string
	// rolesBody 覆盖绑定角色列表响应体；为空用默认完整样本。
	rolesBody string
	// genshinNoteBody / zzzNoteBody 覆盖便签响应体；为空用默认成功样本。
	genshinNoteBody string
	zzzNoteBody     string
	// ltokenBody 覆盖 LToken 换取响应体；为空用默认成功样本。
	ltokenBody string
	// postsBody / topicsBody 覆盖搜索响应体；为空用默认空成功样本。
	postsBody  string
	topicsBody string
	// draftBody 覆盖草稿列表响应体；draftBodyByView 按请求的 view_type 精确覆盖。
	draftBody       string
	draftBodyByView map[string]string
	draftDetailBody string
	// renderer 记录二维码渲染调用；confirmBody 覆盖扫码确认响应体。
	renderer    *fakeRenderer
	confirmBody string
}

// fakeRenderer 是 auth.Renderer 的测试替身：不落盘、只记录调用。
type fakeRenderer struct {
	rendered []string
	cleaned  int
}

func (f *fakeRenderer) Render(content string) (string, error) {
	f.rendered = append(f.rendered, content)
	return "qr.png", nil
}
func (f *fakeRenderer) PNGPath() string { return "qr.png" }
func (f *fakeRenderer) Cleanup() error  { f.cleaned++; return nil }

// requestLog 用私有互斥锁保护请求记录：处理协程写入、测试读取快照与
// 清空都经过同一组方法；快照返回副本，不共享底层 slice（R4）。
type requestLog struct {
	mu      sync.Mutex
	queries []string
}

func (l *requestLog) add(q string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.queries = append(l.queries, q)
}

func (l *requestLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.queries...)
}

func (l *requestLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.queries = nil
}

// feedLog 记录匿名 feed 端点请求。
var feedLog = &requestLog{}

// ltokenLog 记录 LToken 换取请求；noteLog 记录便签业务请求。
var (
	ltokenLog = &requestLog{}
	noteLog   = &requestLog{}
)

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	env := &testEnv{out: &bytes.Buffer{}, errb: &bytes.Buffer{}, log: &requestLog{}, renderer: &fakeRenderer{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/binding/api/getUserGameRolesByStoken", func(w http.ResponseWriter, r *http.Request) {
		assertSessionHeaders(t, r)
		if env.rolesBody != "" {
			fmt.Fprint(w, env.rolesBody)
			return
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"game_biz":"hk4e_cn","game_uid":"770000001","region":"cn_gf01","nickname":"旅行者syn","level":60,"is_chosen":true,"region_name":"天空岛"}
		]}}`)
	})
	mux.HandleFunc("/account/ma-cn-passport/app/createQRLogin", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"url":"https://example.invalid/qr","ticket":"ticket-syn"}}`)
	})
	mux.HandleFunc("/account/ma-cn-passport/app/queryQRLoginStatus", func(w http.ResponseWriter, r *http.Request) {
		if env.confirmBody != "" {
			fmt.Fprint(w, env.confirmBody)
			return
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"status":"Confirmed","tokens":[{"token_type":1,"token":"v2_syn_new_stoken"}],"user_info":{"aid":"100024680","mid":"mid_syn_new"}}}`)
	})
	mux.HandleFunc("/device-fp/api/getFp", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"device_fp":"fp0123456789a"}}`)
	})
	mux.HandleFunc("/account/auth/api/getLTokenBySToken", func(w http.ResponseWriter, r *http.Request) {
		ltokenLog.add(r.URL.RawQuery)
		if env.ltokenBody != "" {
			fmt.Fprint(w, env.ltokenBody)
			return
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"ltoken":"lt_syn"}}`)
	})
	mux.HandleFunc("/game_record/app/genshin/api/dailyNote", func(w http.ResponseWriter, r *http.Request) {
		noteLog.add("genshin?" + r.URL.RawQuery)
		if env.genshinNoteBody != "" {
			fmt.Fprint(w, env.genshinNoteBody)
			return
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"current_resin":200,"max_resin":200,"resin_recovery_time":"0","finished_task_num":4,"total_task_num":4,"remain_resin_discount_num":3,"current_expedition_num":2,"max_expedition_num":5,"current_home_coin":100,"max_home_coin":240}}`)
	})
	mux.HandleFunc("/event/game_record_zzz/api/zzz/note", func(w http.ResponseWriter, r *http.Request) {
		noteLog.add("zzz?" + r.URL.RawQuery)
		if env.zzzNoteBody != "" {
			fmt.Fprint(w, env.zzzNoteBody)
			return
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"energy":{"progress":{"max":240,"current":100},"restore":3600},"vitality":{"max":240,"current":120},"bounty_commission":{"num":1,"total":4},"weekly_task":{"cur_point":100,"max_point":1000},"member_card":{"is_open":true}}}`)
	})
	mux.HandleFunc("/painter/api/user_instant/list", func(w http.ResponseWriter, r *http.Request) {
		assertSessionHeaders(t, r)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"post":{"post_id":"p1","subject":"第一帖","view_type":2,"created_at":1700000000}}
		],"is_last":true}}`)
	})
	mux.HandleFunc("/post/api/getPostFull", func(w http.ResponseWriter, r *http.Request) {
		assertSessionHeaders(t, r)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"post":{"post_id":"p1","subject":"第一帖","view_type":2,"author":{"uid":100024680,"nickname":"作者"},"content":{"describe":"正文"}},"vod_list":[]}}`)
	})
	mux.HandleFunc("/post/api/draft/list", func(w http.ResponseWriter, r *http.Request) {
		assertSessionHeaders(t, r)
		if b, ok := env.draftBodyByView[r.URL.Query().Get("view_type")]; ok {
			fmt.Fprint(w, b)
			return
		}
		if env.draftBody != "" {
			fmt.Fprint(w, env.draftBody)
			return
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[{"draft":{"draft_id":"d1","subject":"草稿","view_type":2}}],"is_last":true}}`)
	})
	mux.HandleFunc("/post/api/draft/detail", func(w http.ResponseWriter, r *http.Request) {
		assertSessionHeaders(t, r)
		if env.draftDetailBody != "" {
			fmt.Fprint(w, env.draftDetailBody)
			return
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"draft_id":"d1","draft":{"post":{"draft_id":"d1"}}}}`)
	})
	mux.HandleFunc("/painter/api/userFavouritePostList", func(w http.ResponseWriter, r *http.Request) {
		assertSessionHeaders(t, r)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"post":{"post_id":"f1","subject":"收藏帖","view_type":2}}
		],"is_last":true}}`)
	})
	mux.HandleFunc("/post/api/getForumPostList", func(w http.ResponseWriter, r *http.Request) {
		assertSessionHeaders(t, r)
		env.log.add(r.URL.RawQuery)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"post":{"post_id":"fp1","subject":"论坛帖","view_type":2,"created_at":1700000000}}
		],"is_last":true}}`)
	})
	mux.HandleFunc("/painter/api/getHotForumPostList", func(w http.ResponseWriter, r *http.Request) {
		feedLog.add(r.URL.Path + "?" + r.URL.RawQuery)
		if r.Header.Get("Cookie") != "" {
			t.Errorf("匿名 feed 不得携带 Cookie")
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"post":{"post_id":"hot1","subject":"热帖","view_type":2,"created_at":1700000000}}
		],"last_id":"hot-next","is_last":false}}`)
	})
	mux.HandleFunc("/painter/api/getRecentForumPostList", func(w http.ResponseWriter, r *http.Request) {
		feedLog.add(r.URL.Path + "?" + r.URL.RawQuery)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"post":{"post_id":"new1","subject":"最新帖","view_type":2}}
		],"page":"4","is_last":false}}`)
	})
	mux.HandleFunc("/apihub/api/getGameList", func(w http.ResponseWriter, r *http.Request) {
		if env.gamesBody != "" {
			fmt.Fprint(w, env.gamesBody)
			return
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"id":2,"name":"原神","en_name":"ys","op_name":"hk4e","has_wiki":true},
			{"id":8,"name":"绝区零","en_name":"zzz","op_name":"nap","has_wiki":true}
		]}}`)
	})
	mux.HandleFunc("/forum/api/getDiscussionByGame", func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("gids"); env.gamesBody == "" && q != "2" {
			t.Errorf("gids = %s", q)
		}
		if env.discussionBody != "" {
			fmt.Fprint(w, env.discussionBody)
			return
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"discussion":{
			"discussion_id":2,"game_id":2,"subject":"旅行者讨论区",
			"forums":[{"id":26,"game_id":2,"name":"酒馆","des":"冒险传说"}]}}}`)
	})
	mux.HandleFunc("/forum/api/getImagePostListType", func(w http.ResponseWriter, r *http.Request) {
		if env.imageTypesBody != "" {
			fmt.Fprint(w, env.imageTypesBody)
			return
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[]}}`)
	})
	mux.HandleFunc("/painter/api/searchPosts", func(w http.ResponseWriter, r *http.Request) {
		env.log.add(r.URL.RawQuery)
		if env.postsBody != "" {
			fmt.Fprint(w, env.postsBody)
			return
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[],"is_last":true}}`)
	})
	mux.HandleFunc("/topic/api/searchTopic", func(w http.ResponseWriter, r *http.Request) {
		env.log.add(r.URL.RawQuery)
		if env.topicsBody != "" {
			fmt.Fprint(w, env.topicsBody)
			return
		}
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"topics":[],"is_last":true}}`)
	})
	mux.HandleFunc("/apihub/api/v2/search", func(w http.ResponseWriter, r *http.Request) {
		env.log.add(r.URL.RawQuery)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"topics":[],"users":[],"wikis":[],"posts":[]}}`)
	})
	srv := httptest.NewServer(mux)
	env.srv = srv

	st := &store.Store{Dir: filepath.Join(t.TempDir(), "mys")}
	// 预置一份合成凭据（社区命令前置条件）。
	if oerr := st.Save(store.NewCredentials("100024680", "mid_syn", "v2_syn_stoken", "device-syn", "fp0123456789a", funcTime())); oerr != nil {
		t.Fatal(oerr)
	}
	deps := Deps{
		Store: st,
		ClientFor: func(host string) *api.Client {
			c, err := api.New(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			return c
		},
		Render: func(quiet bool) auth.Renderer { return env.renderer },
		Out:    env.out,
		ErrOut: env.errb,
	}
	env.deps = deps
	env.root = NewRoot(deps)
	t.Cleanup(srv.Close)
	return env
}

func funcTime() time.Time { return time.Unix(1700000000, 0) }

func assertSessionHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Header.Get("Cookie") != "stuid=100024680; stoken=v2_syn_stoken; mid=mid_syn;" {
		t.Errorf("Cookie = %q", r.Header.Get("Cookie"))
	}
	if r.Header.Get("DS") == "" {
		t.Error("缺少 DS 头")
	}
	if r.Header.Get("X-Rpc-Client_type") != "2" {
		t.Errorf("client_type = %s", r.Header.Get("X-Rpc-Client_type"))
	}
}

func (e *testEnv) run(t *testing.T, args ...string) *output.Envelope {
	t.Helper()
	e.out.Reset()
	e.errb.Reset()
	e.root.SetArgs(args)
	e.root.SetOut(e.out)
	e.root.SetErr(e.errb)
	if err := e.root.Execute(); err != nil {
		var oe *output.Error
		if !asOutputError(err, &oe) {
			// 与生产 Execute 一致：未知命令/未知 flag/参数数量错误统一映射 INPUT_INVALID。
			oe = output.Err(output.CodeInputInvalid, "%v", err).
				WithAction(output.RunCommand("--help"))
		}
		// 失败 envelope 应写入 stderr（与生产 Execute 行为一致）。
		_ = output.Failure(e.errb, oe)
		return &output.Envelope{OK: false, Error: oe}
	}
	var env output.Envelope
	if err := json.Unmarshal(e.out.Bytes(), &env); err != nil {
		t.Fatalf("stdout 不是有效 JSON envelope: %v\n%s", err, e.out.String())
	}
	return &env
}

func asOutputError(err error, target **output.Error) bool {
	if oe, ok := err.(*output.Error); ok {
		*target = oe
		return true
	}
	return false
}

func TestRoleList_JSON(t *testing.T) {
	env := newTestEnv(t)
	env2 := env.run(t, "role", "list", "--json")
	if !env2.OK {
		t.Fatalf("应成功: %+v", env2.Error)
	}
	data, _ := json.Marshal(env2.Data)
	var ld output.ListData
	json.Unmarshal(data, &ld)
	items, _ := ld.Items.([]any)
	if len(items) != 1 {
		t.Errorf("items = %v", ld.Items)
	}
}

func TestPostShow_JSON(t *testing.T) {
	env := newTestEnv(t)
	res := env.run(t, "post", "show", "p1", "--json")
	if !res.OK {
		t.Fatalf("应成功: %+v", res.Error)
	}
	data, _ := json.Marshal(res.Data)
	var d map[string]any
	json.Unmarshal(data, &d)
	if d["post_id"] != "p1" || d["subject"] != "第一帖" {
		t.Errorf("data = %v", d)
	}
}

func TestDraftList_and_FavoriteList(t *testing.T) {
	env := newTestEnv(t)
	if res := env.run(t, "draft", "list", "--json"); !res.OK {
		t.Fatalf("draft list: %+v", res.Error)
	}
	// 单角色 → 自动选择。
	if res := env.run(t, "favorite", "list", "--json"); !res.OK {
		t.Fatalf("favorite list: %+v", res.Error)
	}
	// --full 补全详情。
	if res := env.run(t, "favorite", "list", "--full", "--json"); !res.OK {
		t.Fatalf("favorite list --full: %+v", res.Error)
	}
}

func TestNoCredentials_AuthInvalid_Exit3(t *testing.T) {
	env := newTestEnv(t)
	// 删除凭据模拟未登录。
	if _, oerr := env.deps.Store.Delete(); oerr != nil {
		t.Fatal(oerr)
	}
	res := env.run(t, "post", "list", "--json")
	if res.OK {
		t.Fatal("未登录应失败")
	}
	if res.Error.Code != output.CodeAuthInvalid {
		t.Errorf("code = %s", res.Error.Code)
	}
	if res.Error.Exit != output.ExitAuth {
		t.Errorf("exit = %d", res.Error.Exit)
	}
}

func TestWriteCommandsNotRegistered(t *testing.T) {
	env := newTestEnv(t)
	absent := map[string][]string{
		"post":     {"create", "edit", "delete"},
		"draft":    {"save", "publish", "delete"},
		"favorite": {"add", "remove"},
	}
	for parent, subs := range absent {
		var pc *cobra.Command
		for _, c := range env.root.Commands() {
			if c.Name() == parent {
				pc = c
				break
			}
		}
		if pc == nil {
			t.Errorf("缺少 %s 命令组", parent)
			continue
		}
		for _, sub := range subs {
			for _, c := range pc.Commands() {
				if c.Name() == sub {
					t.Errorf("%s %s 不应注册（未达 adapter_ready 门禁）", parent, sub)
				}
			}
		}
	}
	// operation 组在阶段 C 前整体不注册。
	for _, c := range env.root.Commands() {
		if c.Name() == "operation" {
			t.Error("operation 命令组不应注册（无写操作 journal 场景）")
		}
	}
}

func TestStatusOffline_MakesNoRequests(t *testing.T) {
	st := &store.Store{Dir: filepath.Join(t.TempDir(), "mys")}
	if oerr := st.Save(store.NewCredentials("100024680", "mid_syn", "v2_syn_stoken", "device-syn", "fp0123456789a", funcTime())); oerr != nil {
		t.Fatal(oerr)
	}
	var boomed bool
	deps := Deps{
		Store: st,
		ClientFor: func(host string) *api.Client {
			boomed = true
			return nil
		},
		Render: func(quiet bool) auth.Renderer { return nil },
		Out:    &bytes.Buffer{},
		ErrOut: &bytes.Buffer{},
	}
	root := NewRoot(deps)
	root.SetArgs([]string{"auth", "status", "--json"})
	root.SetOut(&bytes.Buffer{})
	if err := root.Execute(); err != nil {
		t.Fatalf("status: %v", err)
	}
	if boomed {
		t.Error("status 必须完全离线，不应构建任何客户端")
	}
}

func TestAuthStatus_UIDVisibleAndTerminalSafe(t *testing.T) {
	for _, tc := range []struct {
		name     string
		uid      string
		humanUID string
	}{
		{"普通 UID", "100024680", "100024680"},
		{"含控制字符的 UID", "100024680\x1b[31m\nnext-line", `100024680\x1B[31m next-line`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			human := newTestEnv(t)
			if err := human.deps.Store.Save(store.NewCredentials(tc.uid, "mid_syn", "v2_syn_stoken", "device-syn", "fp0123456789a", funcTime())); err != nil {
				t.Fatal(err)
			}
			human.root.SetArgs([]string{"auth", "status"})
			human.root.SetOut(human.out)
			human.root.SetErr(human.errb)
			if err := human.root.Execute(); err != nil {
				t.Fatalf("人类模式应成功: %v", err)
			}
			out := human.out.String()
			if !strings.Contains(out, "UID: "+tc.humanUID+"\n") {
				t.Errorf("应完整显示 UID: %q", out)
			}
			if strings.Contains(out, "\x1b") {
				t.Errorf("人类输出不得包含原始 ESC: %q", out)
			}
			if lines := len(strings.Split(strings.TrimRight(out, "\n"), "\n")); lines != 6 {
				t.Errorf("UID 换行不得伪造输出行（%d 行）: %q", lines, out)
			}

			machine := newTestEnv(t)
			if err := machine.deps.Store.Save(store.NewCredentials(tc.uid, "mid_syn", "v2_syn_stoken", "device-syn", "fp0123456789a", funcTime())); err != nil {
				t.Fatal(err)
			}
			res := machine.run(t, "auth", "status", "--json")
			if !res.OK {
				t.Fatalf("JSON 模式应成功: %+v", res.Error)
			}
			data, ok := res.Data.(map[string]any)
			if !ok {
				t.Fatalf("JSON data 类型错误: %T", res.Data)
			}
			if data["uid"] != tc.uid {
				t.Errorf("JSON 应保留完整 UID 原值: %q", data["uid"])
			}
			if _, exists := data["uid_masked"]; exists {
				t.Errorf("JSON 不应包含 uid_masked: %v", data)
			}
		})
	}
}

func TestLogout_IdempotentJSON(t *testing.T) {
	env := newTestEnv(t)
	if res := env.run(t, "auth", "logout", "--json"); !res.OK {
		t.Fatalf("logout: %+v", res.Error)
	}
	// 幂等：再次执行仍成功。
	res := env.run(t, "auth", "logout", "--json")
	if !res.OK {
		t.Fatalf("幂等 logout: %+v", res.Error)
	}
	data, _ := json.Marshal(res.Data)
	var d map[string]any
	json.Unmarshal(data, &d)
	if d["deleted"] != false {
		t.Errorf("第二次 deleted = %v", d["deleted"])
	}
}

func TestLoginTimeoutFlagValidated(t *testing.T) {
	env := newTestEnv(t)
	env.root.SetArgs([]string{"auth", "login", "--timeout", "0s", "--json"})
	res := env.run(t, "auth", "login", "--timeout", "0s", "--json")
	if res.OK || res.Error.Code != output.CodeInputInvalid {
		t.Fatalf("--timeout 0 应 INPUT_INVALID: %+v", res.Error)
	}
	if res.Error.Exit != output.ExitInput {
		t.Errorf("exit = %d", res.Error.Exit)
	}
}

// ---------- / R4：搜索参数契约（每用例独立 env + 精确 query 断言） ----------

// queryGIDs 解析记录到的 query，精确取出 gids 与 size（避免 Contains 把
// gids=20 误判成 gids=2）。
func parseQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("query 非法 %q: %v", raw, err)
	}
	return v
}

func TestSearchPosts_GIDsContract(t *testing.T) {
	t.Run("缺失gids本地失败零请求", func(t *testing.T) {
		env := newTestEnv(t)
		res := env.run(t, "search", "posts", "原神", "--json")
		if res.OK || res.Error.Code != output.CodeInputInvalid {
			t.Fatalf("缺 --gids 应 INPUT_INVALID: %+v", res.Error)
		}
		if a := res.Error.Action; a == nil || a.Executable != "mys-cli" ||
			!reflect.DeepEqual(a.Args, []string{"forum", "games"}) {
			t.Errorf("下一步不得在 args 中重复可执行文件名: %+v", a)
		}
		if q := env.log.snapshot(); len(q) != 0 {
			t.Errorf("缺 --gids 不得发请求: %v", q)
		}
	})

	t.Run("显式gids2", func(t *testing.T) {
		env := newTestEnv(t)
		if res := env.run(t, "search", "posts", "原神", "--gids", "2", "--json"); !res.OK {
			t.Fatalf("显式 gids: %+v", res.Error)
		}
		q := env.log.snapshot()
		if len(q) != 1 {
			t.Fatalf("请求数 = %d, want 1: %v", len(q), q)
		}
		if got := parseQuery(t, q[0]).Get("gids"); got != "2" {
			t.Errorf("gids = %q, want 2", got)
		}
	})

	t.Run("指定gids8", func(t *testing.T) {
		env := newTestEnv(t)
		if res := env.run(t, "search", "posts", "原神", "--gids", "8", "--json"); !res.OK {
			t.Fatalf("指定 gids: %+v", res.Error)
		}
		q := env.log.snapshot()
		if len(q) != 1 || parseQuery(t, q[0]).Get("gids") != "8" {
			t.Errorf("gids 参数不符: %v", q)
		}
	})

	t.Run("显式空值拒绝", func(t *testing.T) {
		env := newTestEnv(t)
		res := env.run(t, "search", "posts", "原神", "--gids", "", "--json")
		if res.OK || res.Error.Code != output.CodeInputInvalid {
			t.Fatalf("--gids 空值应 INPUT_INVALID: %+v", res.Error)
		}
		if q := env.log.snapshot(); len(q) != 0 {
			t.Errorf("--gids 空值不得发请求: %v", q)
		}
	})

	for _, bad := range []string{"abc", "0", "-2"} {
		t.Run("非法gids_"+bad, func(t *testing.T) {
			env := newTestEnv(t)
			res := env.run(t, "search", "posts", "原神", "--gids", bad, "--json")
			if res.OK || res.Error.Code != output.CodeInputInvalid {
				t.Fatalf("--gids %s 应 INPUT_INVALID: %+v", bad, res.Error)
			}
			if q := env.log.snapshot(); len(q) != 0 {
				t.Errorf("--gids %s 不得发请求", bad)
			}
		})
	}
}

func TestSearchPosts_OrderFlag(t *testing.T) {
	// 只接受已验证的 1（最热）与 2（最新），
	// 其他取值本地拒绝且零请求。
	t.Run("order2透传且context记录", func(t *testing.T) {
		env := newTestEnv(t)
		res := env.run(t, "search", "posts", "原神", "--gids", "2", "--order", "2", "--json")
		if !res.OK {
			t.Fatalf("--order 2: %+v", res.Error)
		}
		q := env.log.snapshot()
		if len(q) != 1 || parseQuery(t, q[0]).Get("order_type") != "2" {
			t.Errorf("order_type 透传不符: %v", q)
		}
		data, _ := json.Marshal(res.Data)
		var ld output.ListData
		json.Unmarshal(data, &ld)
		if ld.Context["order"] != "2" {
			t.Errorf("显式排序应记录 context.order: %v", ld.Context)
		}
	})

	t.Run("order1透传且缺省无context.order", func(t *testing.T) {
		env := newTestEnv(t)
		res := env.run(t, "search", "posts", "原神", "--gids", "2", "--order", "1", "--json")
		if !res.OK {
			t.Fatalf("--order 1: %+v", res.Error)
		}
		data, _ := json.Marshal(res.Data)
		var ld output.ListData
		json.Unmarshal(data, &ld)
		if ld.Context["order"] != "1" {
			t.Errorf("context.order = %v", ld.Context)
		}

		env2 := newTestEnv(t)
		resDefault := env2.run(t, "search", "posts", "原神", "--gids", "2", "--json")
		if !resDefault.OK {
			t.Fatalf("缺省: %+v", resDefault.Error)
		}
		data2, _ := json.Marshal(resDefault.Data)
		var ld2 output.ListData
		json.Unmarshal(data2, &ld2)
		if _, ok := ld2.Context["order"]; ok {
			t.Errorf("缺省不应出现 context.order: %v", ld2.Context)
		}
	})

	// 2026-09-29 收口：仅 1/2 为已验证取值，其余（含 0/3/4/5 与非数字）
	// 一律本地拒绝且零请求。
	for _, bad := range []string{"abc", "0", "3", "4", "5", "-1", "1.5", "＋1"} {
		t.Run("非法order_"+bad, func(t *testing.T) {
			env := newTestEnv(t)
			res := env.run(t, "search", "posts", "原神", "--gids", "2", "--order", bad, "--json")
			if res.OK || res.Error.Code != output.CodeInputInvalid {
				t.Fatalf("--order %s 应 INPUT_INVALID: %+v", bad, res.Error)
			}
			if q := env.log.snapshot(); len(q) != 0 {
				t.Errorf("--order %s 不得发请求", bad)
			}
		})
	}
}

func TestSearchLegacyFlagsRemoved(t *testing.T) {
	// --preview 不再是公开参数；传入即未知 flag 的 INPUT_INVALID。
	env := newTestEnv(t)
	res := env.run(t, "search", "all", "原神", "--gids", "2", "--preview", "--json")
	if res.OK || res.Error.Code != output.CodeInputInvalid {
		t.Fatalf("旧 --preview 应 INPUT_INVALID: %+v", res.Error)
	}
	if q := env.log.snapshot(); len(q) != 0 {
		t.Errorf("旧 --preview 不得发出任何请求: %v", q)
	}
}

func TestSearchAll_GIDsContract(t *testing.T) {
	t.Run("缺失gids本地失败零请求", func(t *testing.T) {
		env := newTestEnv(t)
		res := env.run(t, "search", "all", "原神", "--json")
		if res.OK || res.Error.Code != output.CodeInputInvalid {
			t.Fatalf("缺 --gids 应 INPUT_INVALID: %+v", res.Error)
		}
		if q := env.log.snapshot(); len(q) != 0 {
			t.Errorf("缺 --gids 不得发请求: %v", q)
		}
	})

	t.Run("显式gids2", func(t *testing.T) {
		env := newTestEnv(t)
		if res := env.run(t, "search", "all", "原神", "--gids", "2", "--json"); !res.OK {
			t.Fatalf("显式: %+v", res.Error)
		}
		q := env.log.snapshot()
		if len(q) != 1 || parseQuery(t, q[0]).Get("gids") != "2" {
			t.Errorf("gids 参数不符: %v", q)
		}
		// 移除公开 --preview 只移除用户选项，请求形式保持
		// 既有默认（适配器仍携带 preview=1），不得顺带改变综合搜索结果。
		if got := parseQuery(t, q[0]).Get("preview"); got != "1" {
			t.Errorf("preview = %q, want 1（移除用户 flag 后请求形式不变）", got)
		}
	})

	t.Run("显式空值拒绝", func(t *testing.T) {
		env := newTestEnv(t)
		res := env.run(t, "search", "all", "原神", "--gids", "", "--json")
		if res.OK || res.Error.Code != output.CodeInputInvalid {
			t.Fatalf("--gids 空值应 INPUT_INVALID: %+v", res.Error)
		}
		if q := env.log.snapshot(); len(q) != 0 {
			t.Errorf("不得发请求: %v", q)
		}
	})
}

func TestSearchTopics_NoGIDs(t *testing.T) {
	env := newTestEnv(t)
	// topics 不应接受 --gids（该接口 gids 无过滤效果，命令层直接不提供）。
	env.root.SetArgs([]string{"search", "topics", "原神", "--gids", "2"})
	env.root.SetOut(env.out)
	if err := env.root.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("search topics 应拒绝 --gids: %v", err)
	}
}

func TestSearchLimit_RulePerCase(t *testing.T) {
	for _, bad := range []string{"1", "2", "0", "-1"} {
		t.Run("limit_"+bad, func(t *testing.T) {
			env := newTestEnv(t)
			res := env.run(t, "search", "posts", "原神", "--gids", "2", "--limit", bad, "--json")
			if res.OK || res.Error.Code != output.CodeInputInvalid {
				t.Fatalf("--limit %s 应 INPUT_INVALID: %+v", bad, res.Error)
			}
			if q := env.log.snapshot(); len(q) != 0 {
				t.Errorf("--limit %s 不得发请求", bad)
			}
		})
	}

	t.Run("limit3通过", func(t *testing.T) {
		env := newTestEnv(t)
		if res := env.run(t, "search", "posts", "原神", "--gids", "2", "--limit", "3", "--json"); !res.OK {
			t.Fatalf("--limit 3: %+v", res.Error)
		}
		q := env.log.snapshot()
		if len(q) != 1 || parseQuery(t, q[0]).Get("size") != "3" {
			t.Errorf("size 参数不符: %v", q)
		}
	})
}

// ---------- forum games / list / posts ----------

func TestForumGames_HumanAndJSON(t *testing.T) {
	env := newTestEnv(t)
	env.out.Reset()
	env.root.SetArgs([]string{"forum", "games"})
	if err := env.root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := env.out.String()
	if !strings.Contains(out, "ys") || !strings.Contains(out, "原神") {
		t.Errorf("人类输出应含 GID/en_name/名称: %s", out)
	}
	if strings.Contains(out, "has_wiki") {
		t.Errorf("人类输出不得显示 has_wiki: %s", out)
	}

	res := env.run(t, "forum", "games", "--json")
	if !res.OK {
		t.Fatalf("json: %+v", res.Error)
	}
	data, _ := json.Marshal(res.Data)
	if !strings.Contains(string(data), `"has_wiki":true`) {
		t.Errorf("JSON 应保留 has_wiki 原始字段: %s", data)
	}
	if !strings.Contains(string(data), `"en_name":"ys"`) {
		t.Errorf("JSON 应含 en_name: %s", data)
	}
}

func TestForumList_ByGameSelector(t *testing.T) {
	// en_name 与 GID 必须得到同一结果。
	for _, sel := range []string{"ys", "2"} {
		env := newTestEnv(t)
		res := env.run(t, "forum", "list", "--game", sel, "--json")
		if !res.OK {
			t.Fatalf("--game %s: %+v", sel, res.Error)
		}
		data, _ := json.Marshal(res.Data)
		if !strings.Contains(string(data), `"forum_id":"26"`) || !strings.Contains(string(data), "酒馆") {
			t.Errorf("--game %s data = %s", sel, data)
		}
	}
}

func TestForumList_MissingFieldsShowPlaceholder(t *testing.T) {
	// 样本必须真的缺少名称/描述；描述行始终输出并显示 Not provided。
	env := newTestEnv(t)
	env.discussionBody = `{"retcode":0,"message":"OK","data":{"discussion":{
		"discussion_id":2,"forums":[{"id":26}]}}}`
	env.out.Reset()
	env.root.SetArgs([]string{"forum", "list", "--game", "ys"})
	if err := env.root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := env.out.String()
	if !strings.Contains(out, "26") || !strings.Contains(out, "Description: Not provided") {
		t.Errorf("缺失字段应显示占位: %s", out)
	}
}

func TestForumList_UnknownGameHasCandidates(t *testing.T) {
	env := newTestEnv(t)
	res := env.run(t, "forum", "list", "--game", "原神", "--json")
	if res.OK || res.Error.Code != output.CodeInputInvalid {
		t.Fatalf("中文名应被拒绝: %+v", res.Error)
	}
	data, _ := json.Marshal(res.Error.Context)
	if !strings.Contains(string(data), `"en_name":"ys"`) {
		t.Errorf("错误上下文应含候选: %s", data)
	}
}

func TestForumPosts_ValidationBeforeBusinessRequest(t *testing.T) {
	env := newTestEnv(t)
	// Forum 归属错误：允许目录读取，但不得发出帖子业务请求。
	res := env.run(t, "forum", "posts", "--game", "ys", "--forum", "999", "--json")
	if res.OK || res.Error.Code != output.CodeInputInvalid {
		t.Fatalf("未知 forum 应 INPUT_INVALID: %+v", res.Error)
	}
	if q := env.log.snapshot(); len(q) != 0 {
		t.Errorf("归属校验失败不得发出帖子请求: %v", q)
	}
}

func TestForumPosts_ByNameResolvesAndStreams(t *testing.T) {
	env := newTestEnv(t)
	res := env.run(t, "forum", "posts", "--game", "ys", "--forum", "酒馆", "--json")
	if !res.OK {
		t.Fatalf("按名称解析: %+v", res.Error)
	}
	data, _ := json.Marshal(res.Data)
	if !strings.Contains(string(data), `"forum_id":"26"`) {
		t.Errorf("data 应含解析后的 forum_id: %s", data)
	}
	// sort_type 是协议观察，不暴露到 CLI；业务请求不得携带。
	q := env.log.snapshot()
	if len(q) != 1 {
		t.Fatalf("请求数 = %d, want 1: %v", len(q), q)
	}
	if got := parseQuery(t, q[0]).Get("sort_type"); got != "" {
		t.Errorf("forum posts 不应发送 sort_type，got %q", got)
	}
}

func TestDraftList_JSONContract(t *testing.T) {
	// schema v1、列表 data 形状、preview 分页模式和语义类型字段。
	env := newTestEnv(t)
	env.out.Reset()
	env.root.SetArgs([]string{"draft", "list", "--json"})
	if err := env.root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(env.out.Bytes(), &raw); err != nil {
		t.Fatalf("stdout 不是有效 JSON: %v\n%s", err, env.out.String())
	}
	if v, ok := raw["schema_version"].(float64); !ok || v != 1 {
		t.Errorf("schema_version = %v", raw["schema_version"])
	}
	if raw["ok"] != true || raw["error"] != nil {
		t.Errorf("envelope 形态: ok=%v error=%v", raw["ok"], raw["error"])
	}
	if _, ok := raw["notices"].([]any); !ok {
		t.Errorf("notices 必须始终为数组: %v", raw["notices"])
	}
	if cur, ok := raw["next_cursor"].(string); !ok || cur != "" {
		t.Errorf("has_more=false 时 next_cursor 必须为空串: %v", raw["next_cursor"])
	}
	data := raw["data"].(map[string]any)
	if data["kind"] != "list" {
		t.Errorf("data.kind = %v", data["kind"])
	}
	pag := data["pagination"].(map[string]any)
	if pag["mode"] != "preview" || pag["resumable"] != false {
		t.Errorf("pagination = %+v", pag)
	}
	items := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	item := items[0].(map[string]any)
	if item["effective_view_type"] != float64(2) || item["view_type_source"] != "response" {
		t.Errorf("effective 字段: %+v", item)
	}
	if item["content_type"] != "image_text_post" || item["content_type_label"] != "Image/text post" {
		t.Errorf("content_type 字段: %+v", item)
	}
}

func TestUnknownSubcommandFailsWithInputInvalid(t *testing.T) {
	// 未知命令必须失败，不能退回父命令 Help 并返回 0。
	env := newTestEnv(t)
	res := env.run(t, "forum", "disussion", "--json")
	if res.OK || res.Error.Code != output.CodeInputInvalid {
		t.Fatalf("未知子命令应 INPUT_INVALID: %+v", res.Error)
	}
	if !strings.Contains(res.Error.Message, "disussion") || !strings.Contains(res.Error.Message, "games") {
		t.Errorf("消息应含未知命令与可用命令: %s", res.Error.Message)
	}
	if res.Error.Action == nil || res.Error.Action.Executable != "mys-cli" {
		t.Errorf("应给出安全下一步: %+v", res.Error.Action)
	}
}

func TestGroupWithoutArgsShowsHelp(t *testing.T) {
	// 父命令单独运行是正常用法：显示帮助且退出 0。
	env := newTestEnv(t)
	env.out.Reset()
	env.root.SetOut(env.out)
	env.root.SetArgs([]string{"forum"})
	if err := env.root.Execute(); err != nil {
		t.Fatalf("无参数应成功: %v", err)
	}
	if !strings.Contains(env.out.String(), "forum games") {
		t.Errorf("应显示命令组帮助: %s", env.out.String())
	}
}

func TestRootHelpDescribesReadOnlySurface(t *testing.T) {
	env := newTestEnv(t)
	env.root.SetOut(env.out)
	env.root.SetArgs([]string{"--help"})
	if err := env.root.Execute(); err != nil {
		t.Fatalf("root help failed: %v", err)
	}
	help := env.out.String()
	if !strings.Contains(help, "Miyoushe community.\nIt supports QR login") ||
		!strings.Contains(help, "write operations are not available") {
		t.Errorf("root help must clearly describe the read-only surface: %s", help)
	}
	if strings.Contains(help, "open gradually behind") {
		t.Errorf("root help must not imply unregistered write commands are available: %s", help)
	}
}

// ---------- forum feed（匿名热帖/最新帖） ----------

func TestForumFeed_DefaultHotAnonymous(t *testing.T) {
	env := newTestEnv(t)
	// 删除凭据：匿名 feed 不应要求会话。
	if _, oerr := env.deps.Store.Delete(); oerr != nil {
		t.Fatal(oerr)
	}
	feedLog.reset()
	res := env.run(t, "forum", "feed", "--game", "ys", "--forum", "26", "--json")
	if !res.OK {
		t.Fatalf("匿名 feed 应成功: %+v", res.Error)
	}
	q := feedLog.snapshot()
	if len(q) != 1 || !strings.HasPrefix(q[0], "/painter/api/getHotForumPostList?") {
		t.Fatalf("默认应请求热帖端点: %v", q)
	}
	if !strings.Contains(q[0], "forum_id=26") || !strings.Contains(q[0], "gids=2") {
		t.Errorf("query 缺少参数: %s", q[0])
	}
	data, _ := json.Marshal(res.Data)
	if !strings.Contains(string(data), `"order":"hot"`) || !strings.Contains(string(data), `"post_id":"hot1"`) {
		t.Errorf("JSON data = %s", data)
	}
	if res.NextCursor != "hot-next" {
		t.Errorf("next_cursor = %q", res.NextCursor)
	}
}

func TestForumFeed_Recent(t *testing.T) {
	env := newTestEnv(t)
	feedLog.reset()
	res := env.run(t, "forum", "feed", "--game", "2", "--forum", "酒馆", "--order", "recent", "--json")
	if !res.OK {
		t.Fatalf("recent feed: %+v", res.Error)
	}
	q := feedLog.snapshot()
	if len(q) != 1 || !strings.HasPrefix(q[0], "/painter/api/getRecentForumPostList?") {
		t.Fatalf("--order recent 应请求最新帖端点: %v", q)
	}
	data, _ := json.Marshal(res.Data)
	if !strings.Contains(string(data), `"order":"recent"`) {
		t.Errorf("JSON 应记录 order: %s", data)
	}
}

func TestForumFeed_InvalidOrderZeroRequests(t *testing.T) {
	env := newTestEnv(t)
	feedLog.reset()
	res := env.run(t, "forum", "feed", "--game", "ys", "--forum", "26", "--order", "top", "--json")
	if res.OK || res.Error.Code != output.CodeInputInvalid {
		t.Fatalf("非法 --order 应 INPUT_INVALID: %+v", res.Error)
	}
	if q := feedLog.snapshot(); len(q) != 0 {
		t.Errorf("非法 --order 不得发出任何请求: %v", q)
	}
}

func TestForumFeed_LegacySortFlagRejected(t *testing.T) {
	// 旧 --sort 不保留；flag 已移除，传入时由未知 flag 路径
	// 统一映射为 INPUT_INVALID，且不得发出任何请求。
	env := newTestEnv(t)
	feedLog.reset()
	res := env.run(t, "forum", "feed", "--game", "ys", "--forum", "26", "--sort", "recent", "--json")
	if res.OK || res.Error.Code != output.CodeInputInvalid {
		t.Fatalf("旧 --sort 应 INPUT_INVALID: %+v", res.Error)
	}
	if q := feedLog.snapshot(); len(q) != 0 {
		t.Errorf("旧 --sort 不得发出任何请求: %v", q)
	}
}

func TestForumPosts_SortFlagRemoved(t *testing.T) {
	// 登录态帖子流不提供排序参数；--sort 一律输入错误（零业务请求）。
	env := newTestEnv(t)
	res := env.run(t, "forum", "posts", "--game", "2", "--forum", "26", "--sort", "1", "--json")
	if res.OK || res.Error.Code != output.CodeInputInvalid {
		t.Fatalf("forum posts --sort 应 INPUT_INVALID: %+v", res.Error)
	}
	if q := env.log.snapshot(); len(q) != 0 {
		t.Errorf("forum posts --sort 不得发出任何请求: %v", q)
	}
}

func TestForumFeed_UnknownForumZeroBusinessRequests(t *testing.T) {
	env := newTestEnv(t)
	feedLog.reset()
	res := env.run(t, "forum", "feed", "--game", "ys", "--forum", "999", "--json")
	if res.OK || res.Error.Code != output.CodeInputInvalid {
		t.Fatalf("未知 forum 应 INPUT_INVALID: %+v", res.Error)
	}
	if q := feedLog.snapshot(); len(q) != 0 {
		t.Errorf("归属校验失败不得发出 feed 请求: %v", q)
	}
}

// ---------- role note 重组 ----------

func multiRoleBody() string {
	return `{"retcode":0,"message":"OK","data":{"list":[
		{"game_biz":"hk4e_cn","game_uid":"770000001","region":"cn_gf01","nickname":"旅行者syn","level":60,"is_chosen":true,"region_name":"天空岛"},
		{"game_biz":"nap_cn","game_uid":"1101","region":"prod_gf_cn","nickname":"绳匠syn","level":40,"is_chosen":false,"region_name":"国服"},
		{"game_biz":"hkrpg_cn","game_uid":"2201","region":"prod_gf_cn","nickname":"开拓者syn","level":30,"is_chosen":false,"region_name":"国服"}
	]}}`
}

func TestRoleNote_LegacyEntriesRemoved(t *testing.T) {
	env := newTestEnv(t)
	ltokenLog.reset()
	noteLog.reset()
	res := env.run(t, "role", "list", "--note", "--json")
	if res.OK || res.Error.Code != output.CodeInputInvalid {
		t.Fatalf("旧 role list --note 应 INPUT_INVALID: %+v", res.Error)
	}
	res2 := env.run(t, "note", "--json")
	if res2.OK || res2.Error.Code != output.CodeInputInvalid {
		t.Fatalf("旧顶层 note 应 INPUT_INVALID: %+v", res2.Error)
	}
	if q := ltokenLog.snapshot(); len(q) != 0 {
		t.Errorf("不得换取 LToken: %v", q)
	}
	if q := noteLog.snapshot(); len(q) != 0 {
		t.Errorf("不得发出便签请求: %v", q)
	}
}

func TestRoleNote_GenshinOnly(t *testing.T) {
	env := newTestEnv(t)
	env.rolesBody = multiRoleBody()
	ltokenLog.reset()
	noteLog.reset()
	res := env.run(t, "role", "note", "--game-biz", "hk4e_cn", "--json")
	if !res.OK {
		t.Fatalf("应成功: %+v", res.Error)
	}
	q := noteLog.snapshot()
	if len(q) != 1 || !strings.HasPrefix(q[0], "genshin?") {
		t.Fatalf("只应查询原神便签: %v", q)
	}
	if !strings.Contains(q[0], "role_id=770000001") || !strings.Contains(q[0], "server=cn_gf01") {
		t.Errorf("便签请求参数不符: %s", q[0])
	}
	if len(ltokenLog.snapshot()) != 1 {
		t.Errorf("LToken 只换一次: %v", ltokenLog.snapshot())
	}
	if res.NextCursor != "" {
		t.Errorf("next_cursor = %q", res.NextCursor)
	}
	data, _ := json.Marshal(res.Data)
	var ld output.ListData
	json.Unmarshal(data, &ld)
	if ld.Kind != "list" || ld.HasMore {
		t.Errorf("data 形态: %s", data)
	}
	if ld.Context["game_biz"] != "hk4e_cn" {
		t.Errorf("context = %v", ld.Context)
	}
	items, _ := ld.Items.([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", ld.Items)
	}
	item, _ := items[0].(map[string]any)
	if item["status"] != "available" || item["supported"] != true || item["kind"] != "genshin" {
		t.Errorf("item 状态字段 = %v", item)
	}
	if item["reason"] != "" || item["error_code"] != nil || item["raw"] == nil {
		t.Errorf("成功 item 的 reason/error_code/raw = %v", item)
	}
	if s, ok := item["summary"].([]any); !ok || len(s) == 0 {
		t.Errorf("summary = %v", item["summary"])
	}
	if uid, ok := item["uid"].(string); !ok || uid != "770000001" {
		t.Errorf("uid 应为字符串: %v", item["uid"])
	}
	if ld.Pagination.Mode != "cursor" || ld.Pagination.Resumable || len(ld.Pagination.NextArgs) != 0 {
		t.Errorf("pagination = %+v", ld.Pagination)
	}
}

func TestRoleNote_GameBizTrimmed(t *testing.T) {
	env := newTestEnv(t)
	env.rolesBody = multiRoleBody()
	ltokenLog.reset()
	noteLog.reset()
	res := env.run(t, "role", "note", "--game-biz", "  hk4e_cn  ", "--json")
	if !res.OK {
		t.Fatalf("去空白后应成功: %+v", res.Error)
	}
	data, _ := json.Marshal(res.Data)
	var ld output.ListData
	json.Unmarshal(data, &ld)
	if ld.Context["game_biz"] != "hk4e_cn" {
		t.Errorf("context 应保留规范化后的值: %v", ld.Context)
	}
	if len(noteLog.snapshot()) != 1 {
		t.Errorf("应恰好查询一次: %v", noteLog.snapshot())
	}
}

func TestRoleNote_EmptyGameBizRejected(t *testing.T) {
	env := newTestEnv(t)
	ltokenLog.reset()
	noteLog.reset()
	env.log.reset()
	for _, bad := range []string{"", "   "} {
		res := env.run(t, "role", "note", "--game-biz", bad, "--json")
		if res.OK || res.Error.Code != output.CodeInputInvalid {
			t.Fatalf("--game-biz %q 应 INPUT_INVALID: %+v", bad, res.Error)
		}
		if q := env.log.snapshot(); len(q) != 0 {
			t.Errorf("不得发出角色请求: %v", q)
		}
	}
	if q := ltokenLog.snapshot(); len(q) != 0 {
		t.Errorf("不得换取 LToken: %v", q)
	}
	if q := noteLog.snapshot(); len(q) != 0 {
		t.Errorf("不得发出便签请求: %v", q)
	}
}

func TestRoleNote_NoMatchEmptyList(t *testing.T) {
	env := newTestEnv(t)
	env.rolesBody = multiRoleBody()
	ltokenLog.reset()
	noteLog.reset()
	// 大小写敏感：HK4E_CN 不匹配 hk4e_cn。
	res := env.run(t, "role", "note", "--game-biz", "HK4E_CN", "--json")
	if !res.OK {
		t.Fatalf("无匹配应返回正常空列表: %+v", res.Error)
	}
	data, _ := json.Marshal(res.Data)
	var ld output.ListData
	json.Unmarshal(data, &ld)
	items, _ := ld.Items.([]any)
	if len(items) != 0 {
		t.Errorf("items = %v", ld.Items)
	}
	if ld.Context["game_biz"] != "HK4E_CN" {
		t.Errorf("context 应保留筛选值: %v", ld.Context)
	}
	if len(ltokenLog.snapshot()) != 0 || len(noteLog.snapshot()) != 0 {
		t.Errorf("无匹配角色不得换取 LToken 或查询便签")
	}
}

func TestRoleNote_MixedUnsupported(t *testing.T) {
	env := newTestEnv(t)
	env.rolesBody = multiRoleBody()
	ltokenLog.reset()
	noteLog.reset()
	res := env.run(t, "role", "note", "--json")
	if !res.OK {
		t.Fatalf("应成功: %+v", res.Error)
	}
	if q := noteLog.snapshot(); len(q) != 2 {
		t.Fatalf("只应查询两个已验证游戏: %v", q)
	}
	if len(ltokenLog.snapshot()) != 1 {
		t.Errorf("LToken 只换一次: %v", ltokenLog.snapshot())
	}
	data, _ := json.Marshal(res.Data)
	var ld output.ListData
	json.Unmarshal(data, &ld)
	if len(ld.Context) != 0 {
		t.Errorf("未过滤时 context 应为 {}: %v", ld.Context)
	}
	items, _ := ld.Items.([]any)
	if len(items) != 3 {
		t.Fatalf("items = %v", ld.Items)
	}
	last, _ := items[2].(map[string]any)
	if last["status"] != "unsupported" || last["supported"] != false {
		t.Errorf("未验证游戏 item = %v", last)
	}
	if s, ok := last["summary"].([]any); !ok || len(s) != 0 {
		t.Errorf("unsupported 的 summary 应为 []: %v", last["summary"])
	}
	if last["error_code"] != nil {
		t.Errorf("unsupported 的 error_code 应为 null: %v", last)
	}
}

func TestRoleNote_OnlyUnsupportedNoLToken(t *testing.T) {
	env := newTestEnv(t)
	env.rolesBody = `{"retcode":0,"message":"OK","data":{"list":[
		{"game_biz":"hkrpg_cn","game_uid":"2201","region":"prod_gf_cn","nickname":"开拓者syn","level":30,"is_chosen":false,"region_name":"国服"}
	]}}`
	ltokenLog.reset()
	noteLog.reset()
	res := env.run(t, "role", "note", "--json")
	if !res.OK {
		t.Fatalf("仅不支持角色应 ok:true: %+v", res.Error)
	}
	if len(ltokenLog.snapshot()) != 0 {
		t.Errorf("不应为展示不支持而换取 LToken: %v", ltokenLog.snapshot())
	}
	if len(noteLog.snapshot()) != 0 {
		t.Errorf("不应发出便签请求: %v", noteLog.snapshot())
	}
	data, _ := json.Marshal(res.Data)
	var ld output.ListData
	json.Unmarshal(data, &ld)
	items, _ := ld.Items.([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", ld.Items)
	}
	item, _ := items[0].(map[string]any)
	if item["status"] != "unsupported" {
		t.Errorf("item = %v", item)
	}
}

func TestRoleNote_PartialFailure(t *testing.T) {
	env := newTestEnv(t)
	env.rolesBody = multiRoleBody()
	env.zzzNoteBody = `{"retcode":10001,"message":"record error"}`
	ltokenLog.reset()
	noteLog.reset()
	res := env.run(t, "role", "note", "--json")
	if res.OK {
		t.Fatal("部分失败应 ok:false")
	}
	if res.Error.Code != output.CodePartialFailure {
		t.Fatalf("code = %s, want PARTIAL_FAILURE", res.Error.Code)
	}
	if res.Error.Exit != output.ExitUnknown {
		t.Fatalf("退出码映射 = %d, want 5", res.Error.Exit)
	}
	// 失败不阻断其余角色：原神与绝区零都被查询。
	if q := noteLog.snapshot(); len(q) != 2 {
		t.Fatalf("失败不得阻断其余角色: %v", q)
	}
	var raw map[string]any
	if err := json.Unmarshal(env.errb.Bytes(), &raw); err != nil {
		t.Fatalf("失败 envelope 非法: %v\n%s", err, env.errb.String())
	}
	errObj, _ := raw["error"].(map[string]any)
	if errObj["kind"] != "partial" {
		t.Errorf("kind = %v", errObj["kind"])
	}
	if errObj["resume_cursor"] != nil {
		t.Errorf("resume_cursor 应为 null: %v", errObj["resume_cursor"])
	}
	if args, ok := errObj["resume_args"].([]any); !ok || len(args) != 0 {
		t.Errorf("resume_args = %v", errObj["resume_args"])
	}
	ctx, _ := errObj["context"].(map[string]any)
	if ctx["available_count"] != float64(1) || ctx["unsupported_count"] != float64(1) || ctx["failed_count"] != float64(1) {
		t.Errorf("error.context 计数 = %v", ctx)
	}
	pd, _ := errObj["partial_data"].(map[string]any)
	if pd == nil || pd["kind"] != "list" {
		t.Fatalf("partial_data = %v", errObj["partial_data"])
	}
	pItems, _ := pd["items"].([]any)
	if len(pItems) != 3 {
		t.Fatalf("partial_data items = %v", pd["items"])
	}
	statuses := []string{}
	for _, it := range pItems {
		m, _ := it.(map[string]any)
		statuses = append(statuses, m["status"].(string))
	}
	if strings.Join(statuses, ",") != "available,failed,unsupported" {
		t.Errorf("statuses = %v", statuses)
	}
	f, _ := pItems[1].(map[string]any)
	if f["error_code"] == nil || f["reason"] == "" || f["raw"] != nil {
		t.Errorf("failed item 的 error_code/reason/raw = %v", f)
	}
	if s, ok := f["summary"].([]any); !ok || len(s) != 0 {
		t.Errorf("failed 的 summary 应为 []: %v", f["summary"])
	}
}

func TestRoleNote_LTokenFailureKeepsOriginalClassification(t *testing.T) {
	env := newTestEnv(t)
	env.rolesBody = multiRoleBody()
	env.ltokenBody = `{"retcode":-100,"message":"login required"}`
	ltokenLog.reset()
	noteLog.reset()
	res := env.run(t, "role", "note", "--json")
	if res.OK {
		t.Fatal("LToken 失败应报错")
	}
	if res.Error.Code != output.CodeAuthInvalid {
		t.Fatalf("应保留原始认证分类，got %s", res.Error.Code)
	}
	if len(noteLog.snapshot()) != 0 {
		t.Errorf("不应发出便签请求: %v", noteLog.snapshot())
	}
}

func TestRoleNote_HumanModeStates(t *testing.T) {
	env := newTestEnv(t)
	env.rolesBody = multiRoleBody()
	env.out.Reset()
	env.root.SetArgs([]string{"role", "note"})
	env.root.SetOut(env.out)
	if err := env.root.Execute(); err != nil {
		t.Fatalf("全部成功的人类模式应成功: %v", err)
	}
	outStr := env.out.String()
	for _, want := range []string{"[available]", "[unsupported]", "Resin 200/200"} {
		if !strings.Contains(outStr, want) {
			t.Errorf("输出缺 %q:\n%s", want, outStr)
		}
	}
}

func TestRoleNote_HumanPartialFailure(t *testing.T) {
	env := newTestEnv(t)
	env.rolesBody = multiRoleBody()
	env.zzzNoteBody = `{"retcode":10001,"message":"record error"}`
	env.out.Reset()
	env.errb.Reset()
	env.root.SetArgs([]string{"role", "note"})
	env.root.SetOut(env.out)
	env.root.SetErr(env.errb)
	err := env.root.Execute()
	if err == nil {
		t.Fatal("部分失败的人类模式应返回错误")
	}
	// 人类模式失败时 stdout 必须为空；逐角色状态写入 stderr。
	if env.out.String() != "" {
		t.Errorf("失败终态的 stdout 应为空: %q", env.out.String())
	}
	if !strings.Contains(env.errb.String(), "[failed]") {
		t.Errorf("部分状态应写入 stderr:\n%s", env.errb.String())
	}
	if !strings.Contains(env.errb.String(), "[available]") {
		t.Errorf("成功角色的诊断也应写入 stderr:\n%s", env.errb.String())
	}
	if !strings.Contains(err.Error(), "PARTIAL_FAILURE") {
		t.Errorf("错误应携带 PARTIAL_FAILURE: %v", err)
	}
}

func TestRoleList_GameBizFilter(t *testing.T) {
	env := newTestEnv(t)
	env.rolesBody = multiRoleBody()
	res := env.run(t, "role", "list", "--game-biz", "nap_cn", "--json")
	if !res.OK {
		t.Fatalf("应成功: %+v", res.Error)
	}
	data, _ := json.Marshal(res.Data)
	var ld output.ListData
	json.Unmarshal(data, &ld)
	items, _ := ld.Items.([]any)
	if len(items) != 1 || ld.Context["game_biz"] != "nap_cn" {
		t.Errorf("items=%v context=%v", items, ld.Context)
	}
}

// ---------- 搜索列表 JSON 契约 ----------

func TestSearchPosts_ListContract(t *testing.T) {
	t.Run("有结果且可续页", func(t *testing.T) {
		env := newTestEnv(t)
		env.postsBody = `{"retcode":0,"message":"OK","data":{"list":[{"post":{"post_id":"s1","subject":"结果一","view_type":2,"created_at":1700000000}}],"last_id":"9","is_last":false}}`
		res := env.run(t, "search", "posts", "原神", "--gids", "2", "--json")
		if !res.OK {
			t.Fatalf("应成功: %+v", res.Error)
		}
		if res.NextCursor != "9" {
			t.Errorf("next_cursor = %q", res.NextCursor)
		}
		data, _ := json.Marshal(res.Data)
		var ld output.ListData
		json.Unmarshal(data, &ld)
		if ld.Kind != "list" {
			t.Errorf("kind = %q", ld.Kind)
		}
		if ld.Context["query"] != "原神" || ld.Context["gids"] != "2" || ld.Context["scope"] != "posts" {
			t.Errorf("context = %v", ld.Context)
		}
		items, _ := ld.Items.([]any)
		if len(items) != 1 {
			t.Errorf("items = %v", ld.Items)
		}
		if !ld.Pagination.Resumable {
			t.Errorf("应可续页: %+v", ld.Pagination)
		}
		want := []string{"search", "posts", "原神", "--gids", "2", "--limit", "20", "--cursor", "9", "--json"}
		if !reflect.DeepEqual(ld.Pagination.NextArgs, want) {
			t.Errorf("next_args = %v", ld.Pagination.NextArgs)
		}
	})

	t.Run("可续页带排序与limit", func(t *testing.T) {
		env := newTestEnv(t)
		env.postsBody = `{"retcode":0,"message":"OK","data":{"list":[],"last_id":"3","is_last":false}}`
		res := env.run(t, "search", "posts", "原神", "--gids", "2", "--limit", "5", "--order", "2", "--json")
		if !res.OK {
			t.Fatalf("应成功: %+v", res.Error)
		}
		data, _ := json.Marshal(res.Data)
		var ld output.ListData
		json.Unmarshal(data, &ld)
		want := []string{"search", "posts", "原神", "--gids", "2", "--limit", "5", "--order", "2", "--cursor", "3", "--json"}
		if !reflect.DeepEqual(ld.Pagination.NextArgs, want) {
			t.Errorf("next_args = %v", ld.Pagination.NextArgs)
		}
		items, _ := ld.Items.([]any)
		if items == nil || len(items) != 0 {
			t.Errorf("空结果 items 应为 []: %v", ld.Items)
		}
	})

	t.Run("末页不可续页", func(t *testing.T) {
		env := newTestEnv(t)
		env.postsBody = `{"retcode":0,"message":"OK","data":{"list":[],"last_id":"9","is_last":true}}`
		res := env.run(t, "search", "posts", "原神", "--gids", "2", "--json")
		if !res.OK {
			t.Fatalf("应成功: %+v", res.Error)
		}
		if res.NextCursor != "" {
			t.Errorf("末页 next_cursor 应为空: %q", res.NextCursor)
		}
		data, _ := json.Marshal(res.Data)
		var ld output.ListData
		json.Unmarshal(data, &ld)
		if ld.Pagination.Resumable {
			t.Errorf("末页不可续页: %+v", ld.Pagination)
		}
		if len(ld.Pagination.NextArgs) != 0 {
			t.Errorf("next_args = %v", ld.Pagination.NextArgs)
		}
	})

	t.Run("has_more但空游标不可续页", func(t *testing.T) {
		env := newTestEnv(t)
		env.postsBody = `{"retcode":0,"message":"OK","data":{"list":[],"is_last":false}}`
		res := env.run(t, "search", "posts", "原神", "--gids", "2", "--json")
		if !res.OK {
			t.Fatalf("应成功: %+v", res.Error)
		}
		if res.NextCursor != "" {
			t.Errorf("空游标不得编造成可继续: %q", res.NextCursor)
		}
		data, _ := json.Marshal(res.Data)
		var ld output.ListData
		json.Unmarshal(data, &ld)
		if ld.Pagination.Resumable {
			t.Errorf("空游标不可续页: %+v", ld.Pagination)
		}
	})
}

func TestSearchTopics_ListContract(t *testing.T) {
	t.Run("有结果且可续页", func(t *testing.T) {
		env := newTestEnv(t)
		env.topicsBody = `{"retcode":0,"message":"OK","data":{"topics":[{"id":1,"name":"话题一"}],"last_id":"4","is_last":false}}`
		res := env.run(t, "search", "topics", "原神", "--json")
		if !res.OK {
			t.Fatalf("应成功: %+v", res.Error)
		}
		if res.NextCursor != "4" {
			t.Errorf("next_cursor = %q", res.NextCursor)
		}
		data, _ := json.Marshal(res.Data)
		var ld output.ListData
		json.Unmarshal(data, &ld)
		if ld.Kind != "list" {
			t.Errorf("kind = %q", ld.Kind)
		}
		if ld.Context["query"] != "原神" || ld.Context["scope"] != "topics" {
			t.Errorf("context = %v", ld.Context)
		}
		if _, ok := ld.Context["gids"]; ok {
			t.Errorf("topics context 不应伪造 gids: %v", ld.Context)
		}
		items, _ := ld.Items.([]any)
		if len(items) != 1 {
			t.Errorf("items = %v", ld.Items)
		}
		want := []string{"search", "topics", "原神", "--limit", "20", "--cursor", "4", "--json"}
		if !reflect.DeepEqual(ld.Pagination.NextArgs, want) {
			t.Errorf("next_args = %v", ld.Pagination.NextArgs)
		}
	})

	t.Run("末页", func(t *testing.T) {
		env := newTestEnv(t)
		env.topicsBody = `{"retcode":0,"message":"OK","data":{"topics":[],"is_last":true}}`
		res := env.run(t, "search", "topics", "原神", "--json")
		if !res.OK {
			t.Fatalf("应成功: %+v", res.Error)
		}
		if res.NextCursor != "" {
			t.Errorf("next_cursor = %q", res.NextCursor)
		}
		data, _ := json.Marshal(res.Data)
		var ld output.ListData
		json.Unmarshal(data, &ld)
		if ld.Pagination.Resumable || len(ld.Pagination.NextArgs) != 0 {
			t.Errorf("pagination = %+v", ld.Pagination)
		}
		if ld.Context["scope"] != "topics" {
			t.Errorf("context = %v", ld.Context)
		}
	})
}

// ---------- 草稿冲突提示贯通 ----------

func TestDraftList_TypeConflictNotices(t *testing.T) {
	t.Run("单桶冲突进入notices", func(t *testing.T) {
		env := newTestEnv(t)
		// 桶 1 查询，服务端 view_type=2：响应值生效但必须提示。
		env.draftBodyByView = map[string]string{
			"1": `{"retcode":0,"message":"OK","data":{"list":[{"draft":{"draft_id":"d1","subject":"草稿","view_type":2}}],"is_last":true}}`,
		}
		res := env.run(t, "draft", "list", "--view-type", "1", "--json")
		if !res.OK {
			t.Fatalf("应成功: %+v", res.Error)
		}
		if len(res.Notices) != 1 {
			t.Fatalf("notices = %v", res.Notices)
		}
		n := res.Notices[0]
		if n.Level != "warning" || n.Code != "DRAFT_TYPE_BUCKET_CONFLICT" {
			t.Errorf("notice = %+v", n)
		}
		if n.Context["draft_id"] != "d1" {
			t.Errorf("notice context = %v", n.Context)
		}
	})

	t.Run("多桶部分冲突提示一次", func(t *testing.T) {
		env := newTestEnv(t)
		d := `{"retcode":0,"message":"OK","data":{"list":[{"draft":{"draft_id":"d1","subject":"草稿","view_type":2}}],"is_last":true}}`
		env.draftBodyByView = map[string]string{"1": d, "2": d}
		env.draftBody = `{"retcode":0,"message":"OK","data":{"list":[],"is_last":true}}`
		res := env.run(t, "draft", "list", "--json")
		if !res.OK {
			t.Fatalf("应成功: %+v", res.Error)
		}
		if len(res.Notices) != 1 {
			t.Fatalf("桶 2 与服务端值不一致应提示一次: %v", res.Notices)
		}
	})

	t.Run("无冲突无提示", func(t *testing.T) {
		env := newTestEnv(t)
		env.draftBody = `{"retcode":0,"message":"OK","data":{"list":[{"draft":{"draft_id":"d1","subject":"草稿","view_type":2}}],"is_last":true}}`
		res := env.run(t, "draft", "list", "--view-type", "2", "--json")
		if !res.OK {
			t.Fatalf("应成功: %+v", res.Error)
		}
		if len(res.Notices) != 0 {
			t.Errorf("notices = %v", res.Notices)
		}
	})

	t.Run("缺失与显式0回退桶无提示", func(t *testing.T) {
		env := newTestEnv(t)
		env.draftBodyByView = map[string]string{
			"5": `{"retcode":0,"message":"OK","data":{"list":[{"draft":{"draft_id":"d1","subject":"缺失"}},{"draft":{"draft_id":"d2","subject":"显式0","view_type":0}}],"is_last":true}}`,
		}
		res := env.run(t, "draft", "list", "--view-type", "5", "--json")
		if !res.OK {
			t.Fatalf("应成功: %+v", res.Error)
		}
		if len(res.Notices) != 0 {
			t.Errorf("notices = %v", res.Notices)
		}
		data, _ := json.Marshal(res.Data)
		var ld output.ListData
		json.Unmarshal(data, &ld)
		items, _ := ld.Items.([]any)
		if len(items) != 2 {
			t.Fatalf("items = %v", ld.Items)
		}
		m, _ := items[0].(map[string]any)
		if m["view_type_source"] != "query_bucket" {
			t.Errorf("source = %v", m["view_type_source"])
		}
	})

	t.Run("非零值互相矛盾失败并带partial", func(t *testing.T) {
		env := newTestEnv(t)
		env.draftBodyByView = map[string]string{
			"1": `{"retcode":0,"message":"OK","data":{"list":[{"draft":{"draft_id":"d1","subject":"草稿","view_type":2}}],"is_last":true}}`,
			"2": `{"retcode":0,"message":"OK","data":{"list":[{"draft":{"draft_id":"d1","subject":"草稿","view_type":5}}],"is_last":true}}`,
		}
		res := env.run(t, "draft", "list", "--json")
		if res.OK {
			t.Fatal("互相矛盾的非零服务端值应失败")
		}
		if res.Error.Code != output.CodeProtocolMismatch {
			t.Errorf("code = %s", res.Error.Code)
		}
	})

	t.Run("人类模式显示警告", func(t *testing.T) {
		env := newTestEnv(t)
		env.draftBody = `{"retcode":0,"message":"OK","data":{"list":[{"draft":{"draft_id":"d1","subject":"草稿","view_type":2}}],"is_last":true}}`
		env.out.Reset()
		env.errb.Reset()
		env.root.SetArgs([]string{"draft", "list", "--view-type", "1"})
		env.root.SetOut(env.out)
		env.root.SetErr(env.errb)
		if err := env.root.Execute(); err != nil {
			t.Fatalf("应成功: %v", err)
		}
		if !strings.Contains(env.errb.String(), "conflicts with query bucket") {
			t.Errorf("人类输出应有冲突警告: %s", env.errb.String())
		}
	})
}

// ---------- 远端文本终端安全 ----------

func TestSearchPosts_HumanOutputControlChars(t *testing.T) {
	// 合成远端文本：ANSI ESC、换行、C1 控制符、中文、emoji。
	// JSON 以 \uXXXX/\n 转义写入 fixture，解码后即包含真实控制字符。
	hostile := `{"retcode":0,"message":"OK","data":{"list":[
		{"post":{"post_id":"p1","subject":"a\u001b[31m红\u001b[0m\n第二行\u009b 中文🎉","view_type":2,"created_at":1700000000}}
	],"is_last":true}}`

	env := newTestEnv(t)
	env.postsBody = hostile
	env.out.Reset()
	env.root.SetArgs([]string{"search", "posts", "原神", "--gids", "2"})
	env.root.SetOut(env.out)
	if err := env.root.Execute(); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	out := env.out.String()
	if strings.Contains(out, "\x1b") {
		t.Error("人类输出不得包含原始 ESC 控制序列")
	}
	if strings.Contains(out, "\u009b") {
		t.Error("人类输出不得包含原始 C1 控制字符")
	}
	if lines := len(strings.Split(strings.TrimRight(out, "\n"), "\n")); lines != 1 {
		t.Errorf("远端换行不得伪造额外输出行（%d 行）: %q", lines, out)
	}
	if !strings.Contains(out, "中文🎉") {
		t.Errorf("中文与 emoji 应原样保留: %q", out)
	}
	if !strings.Contains(out, `\x1B`) {
		t.Errorf("ESC 应转义为可见形式: %q", out)
	}

	// JSON 模式：远端原文保留在独立字段（控制字符由 encoding/json 转义）。
	env2 := newTestEnv(t)
	env2.postsBody = hostile
	res := env2.run(t, "search", "posts", "原神", "--gids", "2", "--json")
	if !res.OK {
		t.Fatalf("JSON 模式应成功: %+v", res.Error)
	}
	data, _ := json.Marshal(res.Data)
	var ld output.ListData
	json.Unmarshal(data, &ld)
	items, _ := ld.Items.([]any)
	item, _ := items[0].(map[string]any)
	if item["subject"] != "a\x1b[31m红\x1b[0m\n第二行\u009b 中文🎉" {
		t.Errorf("JSON 应保留远端原文: %q", item["subject"])
	}
}

func TestRoleNote_HumanFailureReasonSanitized(t *testing.T) {
	// 远端错误消息携带 ANSI 序列时，人类模式的失败文案同样必须消毒。
	env := newTestEnv(t)
	env.rolesBody = multiRoleBody()
	env.zzzNoteBody = `{"retcode":10001,"message":"bad\u001b[31mmsg\ninjected"}`
	env.out.Reset()
	env.root.SetArgs([]string{"role", "note"})
	env.root.SetOut(env.out)
	env.root.SetErr(env.errb)
	if err := env.root.Execute(); err == nil {
		t.Fatal("部分失败应返回错误")
	}
	// 部分失败终态写 stderr；消毒要求不变。
	if env.out.String() != "" {
		t.Fatalf("失败终态的 stdout 应为空: %q", env.out.String())
	}
	out := env.errb.String()
	if strings.Contains(out, "\x1b") {
		t.Error("失败文案不得包含原始 ESC 控制序列")
	}
	// 10 行 = 3 个角色头 + 原神 5 行摘要 + 1 行失败文案 + 1 行不支持文案；
	// 远端注入的换行必须被折叠，不得伪造额外行。
	if lines := len(strings.Split(strings.TrimRight(out, "\n"), "\n")); lines != 10 {
		t.Errorf("远端换行不得伪造额外输出行（%d 行）: %q", lines, out)
	}
}

// ---------- 用户可见名称同步 ----------

func TestNotLoggedInPromptUsesMysCli(t *testing.T) {
	env := newTestEnv(t)
	if _, oerr := env.deps.Store.Delete(); oerr != nil {
		t.Fatal(oerr)
	}
	res := env.run(t, "role", "list", "--json")
	if res.OK || res.Error.Code != output.CodeAuthInvalid {
		t.Fatalf("未登录应 AUTH_INVALID: %+v", res.Error)
	}
	if !strings.Contains(res.Error.Message, "mys-cli auth login") {
		t.Errorf("提示应指向 mys-cli auth login: %s", res.Error.Message)
	}
	if strings.Contains(res.Error.Message, " mys auth login") {
		t.Errorf("提示不得再指向旧入口: %s", res.Error.Message)
	}
}

func TestSearchPosts_HumanIDsAndCursorSanitized(t *testing.T) {
	// 远端提供的帖子 ID 与游标同样可能携带控制字符：人类输出消毒；
	// JSON 的原值与实际分页参数不被改写。
	env := newTestEnv(t)
	env.postsBody = `{"retcode":0,"message":"OK","data":{"list":[
		{"post":{"post_id":"p\u001b1\nx","subject":"正常标题","view_type":2,"created_at":1700000000}}
	],"last_id":"9\u001b0\n1","is_last":false}}`
	env.out.Reset()
	env.root.SetArgs([]string{"search", "posts", "原神", "--gids", "2"})
	env.root.SetOut(env.out)
	if err := env.root.Execute(); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	out := env.out.String()
	if strings.Contains(out, "\x1b") {
		t.Error("人类输出不得包含原始 ESC")
	}
	// 2 行 = 条目行 + 游标行；ID 与游标里的换行必须折叠。
	if lines := len(strings.Split(strings.TrimRight(out, "\n"), "\n")); lines != 2 {
		t.Errorf("远端换行不得伪造额外输出行（%d 行）: %q", lines, out)
	}

	res := env.run(t, "search", "posts", "原神", "--gids", "2", "--json")
	if !res.OK {
		t.Fatalf("JSON 应成功: %+v", res.Error)
	}
	if res.NextCursor != "9\x1b0\n1" {
		t.Errorf("next_cursor 应保留原值: %q", res.NextCursor)
	}
	data, _ := json.Marshal(res.Data)
	var ld output.ListData
	json.Unmarshal(data, &ld)
	items, _ := ld.Items.([]any)
	item, _ := items[0].(map[string]any)
	if item["post_id"] != "p\x1b1\nx" {
		t.Errorf("post_id 应保留原值: %q", item["post_id"])
	}
	want := []string{"search", "posts", "原神", "--gids", "2", "--limit", "20", "--cursor", "9\x1b0\n1", "--json"}
	if !reflect.DeepEqual(ld.Pagination.NextArgs, want) {
		t.Errorf("next_args 应使用原始游标: %v", ld.Pagination.NextArgs)
	}
}

func TestSearchTopics_HumanIDSanitized(t *testing.T) {
	env := newTestEnv(t)
	env.topicsBody = `{"retcode":0,"message":"OK","data":{"topics":[{"id":"t\u001b1","name":"话题"}],"is_last":true}}`
	env.out.Reset()
	env.root.SetArgs([]string{"search", "topics", "原神"})
	env.root.SetOut(env.out)
	if err := env.root.Execute(); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	if strings.Contains(env.out.String(), "\x1b") {
		t.Error("话题 ID 不得输出原始 ESC")
	}
	if !strings.Contains(env.out.String(), `\x1B`) {
		t.Errorf("ESC 应转义为可见形式: %q", env.out.String())
	}
}

func TestDraftShow_HumanIDAndImageURLSafe(t *testing.T) {
	const draftID = "d\x1b[31m\nnext"
	const imageURL = "https://example.test/a\x1b[0m\nextra.png"
	env := newTestEnv(t)
	env.draftDetailBody = `{"retcode":0,"message":"OK","data":{"draft_id":"d\u001b[31m\nnext","draft":{"post":{"draft_id":"d\u001b[31m\nnext","subject":"草稿","view_type":2,"content":{"describe":"正文","imgs":["https://example.test/a\u001b[0m\nextra.png"]}}}}}`
	env.root.SetArgs([]string{"draft", "show", draftID})
	if err := env.root.Execute(); err != nil {
		t.Fatalf("draft show: %v", err)
	}
	out := env.out.String()
	if strings.Contains(out, "\x1b") || len(strings.Split(strings.TrimRight(out, "\n"), "\n")) != 6 {
		t.Errorf("ID 与图片 URL 不得注入终端控制字符或新行: %q", out)
	}
	if !strings.Contains(out, `Draft: d\x1B[31m next`) || !strings.Contains(out, `https://example.test/a\x1B[0m extra.png`) {
		t.Errorf("可打印字符应保留并显示控制字符转义: %q", out)
	}
	res := env.run(t, "draft", "show", draftID, "--json")
	if !res.OK {
		t.Fatalf("draft show JSON: %+v", res.Error)
	}
	data, _ := res.Data.(map[string]any)
	images, _ := data["images"].([]any)
	if data["draft_id"] != draftID || len(images) != 1 || images[0] != imageURL {
		t.Errorf("JSON 应保留 ID 与 URL 原值: %v", data)
	}
}

func TestForumGames_HumanDirectoryFieldsSafe(t *testing.T) {
	env := newTestEnv(t)
	env.gamesBody = `{"retcode":0,"message":"OK","data":{"list":[{"id":"2\u001b[31m\n3","en_name":"ys\u001b[0m\nnext","name":"原神","has_wiki":true}]}}`
	env.root.SetArgs([]string{"forum", "games"})
	if err := env.root.Execute(); err != nil {
		t.Fatalf("forum games: %v", err)
	}
	out := env.out.String()
	if strings.Contains(out, "\x1b") || len(strings.Split(strings.TrimRight(out, "\n"), "\n")) != 1 {
		t.Errorf("GID 与 en_name 不得注入终端控制字符或新行: %q", out)
	}
	if !strings.Contains(out, `2\x1B[31m 3`) || !strings.Contains(out, `ys\x1B[0m next`) {
		t.Errorf("目录字段应显示可见转义: %q", out)
	}
	res := env.run(t, "forum", "games", "--json")
	if !res.OK {
		t.Fatalf("forum games JSON: %+v", res.Error)
	}
	data, _ := json.Marshal(res.Data)
	var ld output.ListData
	_ = json.Unmarshal(data, &ld)
	items, _ := ld.Items.([]any)
	game, _ := items[0].(map[string]any)
	if game["gids"] != "2\x1b[31m\n3" || game["en_name"] != "ys\x1b[0m\nnext" {
		t.Errorf("JSON 应保留目录原值: %v", game)
	}
}

func TestForumListAndImages_HumanDirectoryFieldsSafe(t *testing.T) {
	env := newTestEnv(t)
	env.gamesBody = `{"retcode":0,"message":"OK","data":{"list":[{"id":2,"en_name":"ys\u001b[31m\nnext","name":"原神"}]}}`
	env.discussionBody = `{"retcode":0,"message":"OK","data":{"discussion":{"discussion_id":2,"forums":[{"id":"26\u001b[0m\nnext","name":"画廊","des":"作品"}]}}}`
	env.root.SetArgs([]string{"forum", "list", "--game", "2"})
	if err := env.root.Execute(); err != nil {
		t.Fatalf("forum list: %v", err)
	}
	out := env.out.String()
	if strings.Contains(out, "\x1b") || len(strings.Split(strings.TrimRight(out, "\n"), "\n")) != 3 {
		t.Errorf("论坛目录不得注入终端控制字符或新行: %q", out)
	}
	if !strings.Contains(out, `ys\x1B[31m next`) || !strings.Contains(out, `26\x1B[0m next`) {
		t.Errorf("目录字段应显示可见转义: %q", out)
	}
	env.out.Reset()
	env.root.SetArgs([]string{"forum", "images", "--game", "2", "--forum", "画廊"})
	if err := env.root.Execute(); err != nil {
		t.Fatalf("forum images: %v", err)
	}
	out = env.out.String()
	if strings.Contains(out, "\x1b") || len(strings.Split(strings.TrimRight(out, "\n"), "\n")) != 1 || !strings.Contains(out, `Forum 26\x1B[0m next`) {
		t.Errorf("图片分区提示应安全展示 forum ID: %q", out)
	}
	res := env.run(t, "forum", "list", "--game", "2", "--json")
	if !res.OK {
		t.Fatalf("forum list JSON: %+v", res.Error)
	}
	data, _ := json.Marshal(res.Data)
	var ld output.ListData
	_ = json.Unmarshal(data, &ld)
	items, _ := ld.Items.([]any)
	forum, _ := items[0].(map[string]any)
	if forum["forum_id"] != "26\x1b[0m\nnext" || forum["en_name"] != "ys\x1b[31m\nnext" {
		t.Errorf("JSON 应保留论坛目录原值: %v", forum)
	}
}

func TestFavoriteWarning_HumanRoleFieldsSafe(t *testing.T) {
	env := newTestEnv(t)
	env.rolesBody = `{"retcode":0,"message":"OK","data":{"list":[{"game_biz":"hk4e_cn","game_uid":"77\u001b[31m\n001","region":"cn_gf01","region_name":"天空\u001b[0m\n岛","is_chosen":true}]}}`
	env.root.SetArgs([]string{"favorite", "list"})
	if err := env.root.Execute(); err != nil {
		t.Fatalf("favorite list: %v", err)
	}
	warnings := env.errb.String()
	if strings.Contains(warnings, "\x1b") || len(strings.Split(strings.TrimRight(warnings, "\n"), "\n")) != 1 {
		t.Errorf("角色警告不得注入终端控制字符或新行: %q", warnings)
	}
	if !strings.Contains(warnings, `Using role 77\x1B[31m 001 (天空\x1B[0m 岛)`) {
		t.Errorf("角色字段应显示可见转义: %q", warnings)
	}
	res := env.run(t, "favorite", "list", "--json")
	if !res.OK || len(res.Warnings) != 1 || res.Warnings[0] != "Using role 77\x1b[31m\n001 (天空\x1b[0m\n岛)" {
		t.Errorf("JSON 警告应保留原值: %+v", res)
	}
}

func TestEmitFailure_HumanWarningSafe(t *testing.T) {
	env := newTestEnv(t)
	oe := output.Err(output.CodeInputInvalid, "invalid input")
	oe.Warnings = []string{"warning\x1b[31m\ninjected"}
	emitFailure(env.deps, env.root, oe)
	out := env.errb.String()
	if strings.Contains(out, "\x1b") || len(strings.Split(strings.TrimRight(out, "\n"), "\n")) != 2 {
		t.Errorf("失败警告不得注入终端控制字符或新行: %q", out)
	}
	if !strings.Contains(out, `Warning: warning\x1B[31m injected`) {
		t.Errorf("失败警告应显示可见转义: %q", out)
	}
}

// ---------- 成功扫码登录的输出契约 ----------

func TestAuthLogin_SuccessOutputContract(t *testing.T) {
	t.Run("JSON完整UID且不回显凭据", func(t *testing.T) {
		env := newTestEnv(t)
		res := env.run(t, "auth", "login", "--json")
		if !res.OK {
			t.Fatalf("应成功: %+v", res.Error)
		}
		data, _ := json.Marshal(res.Data)
		var d map[string]any
		json.Unmarshal(data, &d)
		if d["uid"] != "100024680" {
			t.Errorf("uid = %v（应为完整 UID，无 uid_masked）", d["uid"])
		}
		if _, ok := d["uid_masked"]; ok {
			t.Error("不得输出 uid_masked")
		}
		raw := string(data)
		for _, secret := range []string{"v2_syn_new_stoken", "ticket-syn"} {
			if strings.Contains(raw, secret) {
				t.Errorf("JSON 不得回显凭据或票据: %s", raw)
			}
		}
		creds, err := env.deps.Store.Load()
		if err != nil || creds == nil || creds.Stoken != "v2_syn_new_stoken" {
			t.Errorf("新凭据应落盘: err=%v creds=%+v", err, creds)
		}
		if env.renderer.cleaned == 0 {
			t.Error("登录结束应清理临时二维码")
		}
	})

	t.Run("人类模式Account行完整且安全", func(t *testing.T) {
		env := newTestEnv(t)
		// UID 携带 ESC 与换行：人类输出必须转义且不伪造额外行。
		env.confirmBody = `{"retcode":0,"message":"OK","data":{"status":"Confirmed","tokens":[{"token_type":1,"token":"v2_syn_new_stoken"}],"user_info":{"aid":"100\u001b1\n2","mid":"mid_syn_new"}}}`
		env.out.Reset()
		env.errb.Reset()
		env.root.SetArgs([]string{"auth", "login"})
		env.root.SetOut(env.out)
		env.root.SetErr(env.errb)
		if err := env.root.Execute(); err != nil {
			t.Fatalf("应成功: %v", err)
		}
		out := env.out.String()
		if !strings.Contains(out, "Account: 100\\x1B1 2") {
			t.Errorf("Account 行应显示完整 UID 并转义控制字符: %q", out)
		}
		if strings.Contains(out, "\x1b") {
			t.Error("人类输出不得包含原始 ESC")
		}
		if strings.Contains(out, "v2_syn_new_stoken") || strings.Contains(out, "mid_syn_new") {
			t.Error("人类输出不得回显 token/mid")
		}
	})
}
