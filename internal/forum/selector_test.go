package forum

import (
	"strings"
	"testing"

	"mihoyo_cli/internal/output"
)

func testGames() []GameMeta {
	return []GameMeta{
		{ID: "1", Name: "崩坏3", ENName: "bh3"},
		{ID: "2", Name: "原神", ENName: "ys"},
		{ID: "8", Name: "绝区零", ENName: "zzz"},
		{ID: "10", Name: "星布谷地", ENName: "planet", HasWiki: true},
	}
}

func TestResolveGame(t *testing.T) {
	games := testGames()

	// GID（含两端空白）与 en_name 都必须得到同一规范化上下文。
	if g, oerr := ResolveGame(" 2 ", games); oerr != nil || g.ENName != "ys" {
		t.Fatalf("GID 解析: %+v %v", g, oerr)
	}
	if g, oerr := ResolveGame("ys", games); oerr != nil || g.GIDs() != "2" {
		t.Fatalf("en_name 解析: %+v %v", g, oerr)
	}

	// 拒绝：中文名、op_name 字段值、大小写折叠、前导零、+ 号、小数、0、负数、未知 GID。
	rejected := []string{"原神", "op_name", "YS", "02", "+2", "2.0", "0", "-2", "999", "2 2"}
	for _, sel := range rejected {
		_, oerr := ResolveGame(sel, games)
		if oerr == nil || oerr.Code != output.CodeInputInvalid {
			t.Errorf("selector %q 应 INPUT_INVALID: %v", sel, oerr)
		}
		if oerr != nil && (oerr.Action == nil || oerr.Action.Executable != "mys-cli" ||
			strings.Join(oerr.Action.Args, " ") != "forum games") {
			t.Errorf("selector %q 应给出可直接执行且不重复可执行文件名的下一步: %+v", sel, oerr.Action)
		}
	}

	// 未知 GID 的候选列表包含全部游戏（安全字段）。
	_, oerr := ResolveGame("999", games)
	cs, ok := oerr.Context["candidates"].([]map[string]string)
	if !ok || len(cs) != len(games) {
		t.Fatalf("candidates 缺失或长度不符: %+v", oerr.Context)
	}
	if cs[1]["en_name"] != "ys" || cs[1]["gids"] != "2" {
		t.Errorf("candidate = %+v", cs[1])
	}

	// 空选择器：缺必填项。
	if _, oerr := ResolveGame("", games); oerr == nil || !strings.Contains(oerr.Message, "--game") {
		t.Errorf("空 --game 应报缺必填项: %v", oerr)
	}
}

func TestResolveForum(t *testing.T) {
	forums := []Forum{
		{ID: "26", Name: "酒馆"},
		{ID: "43", Name: "攻略"},
		{ID: "44", Name: "同人"},
		{ID: "45", Name: "同人"}, // 重名
	}

	if f, oerr := ResolveForum("26", forums); oerr != nil || f.Name != "酒馆" {
		t.Fatalf("ID 解析: %+v %v", f, oerr)
	}
	if f, oerr := ResolveForum(" 酒馆 ", forums); oerr != nil || f.ID.String() != "26" {
		t.Fatalf("名称解析: %+v %v", f, oerr)
	}

	// 纯数字输入始终按 ID 解析：不存在的 ID 不会回退为名称匹配。
	_, oerr := ResolveForum("999", forums)
	if oerr == nil || oerr.Code != output.CodeInputInvalid {
		t.Fatalf("未知 ID 应 INPUT_INVALID: %v", oerr)
	}
	cs, ok := oerr.Context["candidates"].([]map[string]string)
	if !ok || len(cs) != len(forums) {
		t.Fatalf("未知 ID 应列出全部候选: %+v", oerr.Context)
	}

	// 重名：只列同名候选。
	_, oerr = ResolveForum("同人", forums)
	if oerr == nil || !strings.Contains(oerr.Message, "ambiguous") {
		t.Fatalf("重名应报不唯一: %v", oerr)
	}
	cs, _ = oerr.Context["candidates"].([]map[string]string)
	if len(cs) != 2 {
		t.Errorf("重名候选应为 2: %+v", cs)
	}

	// 名称大小写敏感：不做折叠。
	if _, oerr := ResolveForum("酒館", forums); oerr == nil {
		t.Error("不存在的名称应失败")
	}

	if _, oerr := ResolveForum("", forums); oerr == nil || !strings.Contains(oerr.Message, "--forum") {
		t.Errorf("空 --forum 应报缺必填项: %v", oerr)
	}
}

func TestParsePositiveInt64(t *testing.T) {
	good := map[string]int64{"1": 1, "26": 26, "9223372036854775807": 9223372036854775807}
	for s, want := range good {
		if v, ok := parsePositiveInt64(s); !ok || v != want {
			t.Errorf("parsePositiveInt64(%q) = %d,%v, want %d", s, v, ok, want)
		}
	}
	bad := []string{"0", "01", "+1", "-1", "1.0", "9223372036854775808", "", "١٢٣", "1 000"}
	for _, s := range bad {
		if _, ok := parsePositiveInt64(s); ok {
			t.Errorf("parsePositiveInt64(%q) 应拒绝", s)
		}
	}
}
