// selector.go 实现 Forum 的游戏与分区选择器。
//
// 契约：
//   - --game 只接受匹配 [1-9][0-9]* 且可表示为有符号 64 位整数的 GID，
//     或 getGameList 的 en_name 原始小写 ASCII 值；
//   - --forum 只接受正整数 forum ID，或指定游戏目录内精确、唯一的服务端
//     原始名称；
//   - 两端空白可以去除，中间内容与大小写不得改写；
//   - 纯数字输入始终按 ID 解析；
//   - 解析失败返回 INPUT_INVALID，可携带安全的 candidates 列表；
//     不自动选择近似项，也不在解析失败后发出帖子请求。
package forum

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
)

// positiveIntPattern 是严格正整数形态：无前导零、无 + 号、无小数。
var positiveIntPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

// parsePositiveInt64 解析严格正整数；溢出或形态不符返回 false。
func parsePositiveInt64(s string) (int64, bool) {
	if !positiveIntPattern.MatchString(s) {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false // 溢出
	}
	return v, true
}

// GameMeta 是 getGameList 提供的游戏元数据。
// 上游字段名是 id（2026-09-24 实测），gids 仅作旧样例兼容的兜底键。
type GameMeta struct {
	// CLI 的 JSON 契约沿用 gids；上游响应字段名是 id。
	ID      api.FlexString `json:"gids"`
	Name    string         `json:"name"`
	ENName  string         `json:"en_name"`
	OpName  string         `json:"op_name"`
	HasWiki bool           `json:"has_wiki"`
}

// UnmarshalJSON 解析目录条目：以 id 为准，缺失时兼容 gids 键。
func (g *GameMeta) UnmarshalJSON(b []byte) error {
	var raw struct {
		ID      api.FlexString `json:"id"`
		GIDs    api.FlexString `json:"gids"`
		Name    string         `json:"name"`
		ENName  string         `json:"en_name"`
		OpName  string         `json:"op_name"`
		HasWiki bool           `json:"has_wiki"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	g.ID = raw.ID
	if g.ID == "" {
		g.ID = raw.GIDs
	}
	g.Name, g.ENName, g.OpName, g.HasWiki = raw.Name, raw.ENName, raw.OpName, raw.HasWiki
	return nil
}

// GIDs 返回游戏 GID（上游字段名为 id）。
func (g GameMeta) GIDs() string { return g.ID.String() }

// ResolveGame 解析 --game 选择器。
// 目录由调用方从 getGameList 取得；本函数是纯函数，不发请求。
func ResolveGame(selector string, games []GameMeta) (GameMeta, *output.Error) {
	sel := strings.TrimSpace(selector)
	if sel == "" {
		return GameMeta{}, output.Err(output.CodeInputInvalid,
			"Missing required option --game").WithAction(output.RunCommand("forum", "games"))
	}
	if id, ok := parsePositiveInt64(sel); ok {
		for _, g := range games {
			if g.GIDs() == strconv.FormatInt(id, 10) {
				return g, nil
			}
		}
		oe := output.Err(output.CodeInputInvalid, "Game GID %s is not in the current game directory", sel)
		oe.Context = map[string]any{"candidates": gameCandidates(games)}
		return GameMeta{}, oe.WithAction(output.RunCommand("forum", "games"))
	}
	// en_name：服务端原始小写 ASCII 值，精确匹配（不做大小写折叠）。
	for _, g := range games {
		if g.ENName != "" && sel == g.ENName {
			return g, nil
		}
	}
	oe := output.Err(output.CodeInputInvalid,
		"Unknown game selector %q; use a positive GID or the en_name shown by forum games", sel)
	oe.Context = map[string]any{"candidates": gameCandidates(games)}
	return GameMeta{}, oe.WithAction(output.RunCommand("forum", "games"))
}

func gameCandidates(games []GameMeta) []map[string]string {
	out := make([]map[string]string, 0, len(games))
	for _, g := range games {
		out = append(out, map[string]string{
			"gids":    g.GIDs(),
			"en_name": g.ENName,
			"name":    g.Name,
		})
	}
	return out
}

// ResolveForum 解析 --forum 选择器（在指定游戏的目录内）。
// 纯数字输入始终按 ID 解析；名称按服务端原文大小写敏感精确匹配。
func ResolveForum(selector string, forums []Forum) (Forum, *output.Error) {
	sel := strings.TrimSpace(selector)
	if sel == "" {
		return Forum{}, output.Err(output.CodeInputInvalid, "Missing required option --forum")
	}
	if id, ok := parsePositiveInt64(sel); ok {
		for _, f := range forums {
			if f.ID.String() == strconv.FormatInt(id, 10) {
				return f, nil
			}
		}
		oe := output.Err(output.CodeInputInvalid,
			"Forum ID %s does not belong to the selected game", sel)
		oe.Context = map[string]any{"candidates": forumCandidates(forums)}
		return Forum{}, oe
	}
	var matches []Forum
	for _, f := range forums {
		if f.Name != "" && sel == f.Name {
			matches = append(matches, f)
		}
	}
	switch len(matches) {
	case 0:
		oe := output.Err(output.CodeInputInvalid,
			"No forum named %q in the selected game", sel)
		oe.Context = map[string]any{"candidates": forumCandidates(forums)}
		return Forum{}, oe
	case 1:
		return matches[0], nil
	default:
		// 不唯一：只列出同名候选。
		oe := output.Err(output.CodeInputInvalid,
			"Forum name %q is ambiguous; pick one by forum ID", sel)
		oe.Context = map[string]any{"candidates": forumCandidates(matches)}
		return Forum{}, oe
	}
}

func forumCandidates(forums []Forum) []map[string]string {
	out := make([]map[string]string, 0, len(forums))
	for _, f := range forums {
		out = append(out, map[string]string{
			"forum_id": f.ID.String(),
			"name":     f.Name,
		})
	}
	return out
}
