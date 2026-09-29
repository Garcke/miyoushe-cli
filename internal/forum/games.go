// games.go 实现游戏元数据目录：
// GET /apihub/api/getGameList 是 GID、服务端游戏名、en_name、op_name 与
// has_wiki 的来源（2026-09-24 上游实测：匿名，UA 即可；响应
// data.list = [{id, name, en_name, op_name, has_wiki, ...}]，"gids" 即 id）。
// 匿名可读；不从 Forum 名称反推游戏别名。
package forum

import (
	"context"
	"encoding/json"

	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/protocol"
)

// gameListData 容忍上游两种常见包裹：data.list 数组或 data 直接为数组。
type gameListData struct {
	List []GameMeta `json:"list"`
	// 兼容 data 直接为数组的形态（由 rawGameData 处理）。
}

// GamesMeta 拉取游戏元数据列表（匿名请求，不携带会话 Cookie）。
// 目录请求失败属于远端/协议错误，不报成“未知游戏”。
func (s *Service) GamesMeta(ctx context.Context) ([]GameMeta, *output.Error) {
	h := protocol.CommonHeaders(protocol.ClientTypeAndroid, protocol.DeviceContext{})
	var raw json.RawMessage
	if oerr := s.Client.DoJSON(ctx, "GET", "/apihub/api/getGameList", nil, nil, h, &raw); oerr != nil {
		return nil, oerr
	}
	games, oerr := parseGameList(raw)
	if oerr != nil {
		return nil, oerr
	}
	if len(games) == 0 {
		return nil, output.Err(output.CodeProtocolMismatch, "Game directory response contained no games")
	}
	for i := range games {
		if games[i].GIDs() == "" {
			return nil, output.Err(output.CodeProtocolMismatch, "Game directory entry is missing gids")
		}
	}
	return games, nil
}

// parseGameList 解析目录响应：接受 {list:[...]} 与直接数组两种形态；
// 结构不符返回 PROTOCOL_MISMATCH，不静默返回空列表。
func parseGameList(raw json.RawMessage) ([]GameMeta, *output.Error) {
	trimmed := trimSpaceBytes(raw)
	if len(trimmed) == 0 {
		return nil, output.Err(output.CodeProtocolMismatch, "Game directory response is empty")
	}
	if trimmed[0] == '[' {
		var arr []GameMeta
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, output.Err(output.CodeProtocolMismatch, "Game directory array could not be parsed")
		}
		return arr, nil
	}
	var data gameListData
	if err := json.Unmarshal(trimmed, &data); err != nil {
		return nil, output.Err(output.CodeProtocolMismatch, "Game directory structure mismatch")
	}
	return data.List, nil
}

func trimSpaceBytes(b []byte) []byte {
	start := 0
	for start < len(b) && (b[start] == ' ' || b[start] == '\n' || b[start] == '\r' || b[start] == '\t') {
		start++
	}
	end := len(b)
	for end > start && (b[end-1] == ' ' || b[end-1] == '\n' || b[end-1] == '\r' || b[end-1] == '\t') {
		end--
	}
	return b[start:end]
}
