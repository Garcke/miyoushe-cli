package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
}

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

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	env := &testEnv{out: &bytes.Buffer{}, errb: &bytes.Buffer{}, log: &requestLog{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/binding/api/getUserGameRolesByStoken", func(w http.ResponseWriter, r *http.Request) {
		assertSessionHeaders(t, r)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"game_biz":"hk4e_cn","game_uid":"770000001","region":"cn_gf01","nickname":"旅行者syn","level":60,"is_chosen":true,"region_name":"天空岛"}
		]}}`)
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
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[{"draft":{"draft_id":"d1","subject":"草稿","view_type":2}}],"is_last":true}}`)
	})
	mux.HandleFunc("/painter/api/userFavouritePostList", func(w http.ResponseWriter, r *http.Request) {
		assertSessionHeaders(t, r)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[
			{"post":{"post_id":"f1","subject":"收藏帖","view_type":2}}
		],"is_last":true}}`)
	})
	mux.HandleFunc("/forum/api/getDiscussionByGame", func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("gids"); q != "2" {
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
	mux.HandleFunc("/painter/api/searchPosts", func(w http.ResponseWriter, r *http.Request) {
		env.log.add(r.URL.RawQuery)
		fmt.Fprint(w, `{"retcode":0,"message":"OK","data":{"list":[],"is_last":true}}`)
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
		Render: func(quiet bool) auth.Renderer { return nil },
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
			t.Fatalf("非 output.Error: %v", err)
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

// ---------- V3 §3 / R4：搜索参数契约（每用例独立 env + 精确 query 断言） ----------

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
	t.Run("默认gids2", func(t *testing.T) {
		env := newTestEnv(t)
		if res := env.run(t, "search", "posts", "原神", "--json"); !res.OK {
			t.Fatalf("默认 gids: %+v", res.Error)
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

func TestSearchAll_GIDsContract(t *testing.T) {
	t.Run("默认gids2", func(t *testing.T) {
		env := newTestEnv(t)
		if res := env.run(t, "search", "all", "原神", "--json"); !res.OK {
			t.Fatalf("默认: %+v", res.Error)
		}
		q := env.log.snapshot()
		if len(q) != 1 || parseQuery(t, q[0]).Get("gids") != "2" {
			t.Errorf("gids 参数不符: %v", q)
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
			res := env.run(t, "search", "posts", "原神", "--limit", bad, "--json")
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
		if res := env.run(t, "search", "posts", "原神", "--limit", "3", "--json"); !res.OK {
			t.Fatalf("--limit 3: %+v", res.Error)
		}
		q := env.log.snapshot()
		if len(q) != 1 || parseQuery(t, q[0]).Get("size") != "3" {
			t.Errorf("size 参数不符: %v", q)
		}
	})
}

// ---------- V3 §5.1：forum discussion 匿名只读命令 ----------

func TestForumDiscussion_Anonymous(t *testing.T) {
	env := newTestEnv(t)
	// 删除凭据：匿名接口不应要求会话。
	if _, oerr := env.deps.Store.Delete(); oerr != nil {
		t.Fatal(oerr)
	}
	res := env.run(t, "forum", "discussion", "2", "--json")
	if !res.OK {
		t.Fatalf("匿名调用应成功: %+v", res.Error)
	}
	data, _ := json.Marshal(res.Data)
	var d struct {
		DiscussionID string `json:"discussion_id"`
		Subject      string `json:"subject"`
		Forums       []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Des  string `json:"des"`
		} `json:"forums"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("data: %v", err)
	}
	if d.DiscussionID != "2" || d.Subject != "旅行者讨论区" || len(d.Forums) != 1 || d.Forums[0].Name != "酒馆" {
		t.Errorf("discussion = %+v", d)
	}
}

func TestForumDiscussion_HumanShowsNotProvided(t *testing.T) {
	// R3：样本必须真的缺少名称/描述，才能证明缺失字段的占位输出。
	env := newTestEnv(t)
	env.discussionBody = `{"retcode":0,"message":"OK","data":{"discussion":{
		"discussion_id":2,"forums":[{"id":26}]}}}`
	env.out.Reset()
	env.root.SetArgs([]string{"forum", "discussion", "2"})
	if err := env.root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := env.out.String()
	if !strings.Contains(out, "26") {
		t.Fatalf("输出缺少分区 ID: %s", out)
	}
	if !strings.Contains(out, "未提供") {
		t.Errorf("缺失名称/描述应显示未提供: %s", out)
	}
	if !strings.Contains(out, "描述: 未提供") {
		t.Errorf("描述行应始终输出并显示未提供: %s", out)
	}
}

func TestForumDiscussion_HumanCompleteFields(t *testing.T) {
	// 字段齐全时的普通输出验证（与缺失场景分开）。
	env := newTestEnv(t)
	env.out.Reset()
	env.root.SetArgs([]string{"forum", "discussion", "2"})
	if err := env.root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := env.out.String()
	if !strings.Contains(out, "旅行者讨论区") || !strings.Contains(out, "酒馆") || !strings.Contains(out, "冒险传说") {
		t.Errorf("人类输出缺少字段: %s", out)
	}
}
