package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/note"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/presentation"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/role"
	"mihoyo_cli/internal/session"
)

// newNoteService 构造便签服务（战绩域 + passport 域），并加载记录域设备覆盖。
func newNoteService(deps Deps) *note.Service {
	return &note.Service{
		Record:       deps.ClientFor(note.HostRecord),
		Passport:     deps.ClientFor(protocol.HostPassportAPI),
		RecordDevice: loadRecordDevice(deps),
	}
}

// recordDeviceFile 是记录域（便签/战绩）的设备身份覆盖文件：
//
//	{"device_id": "…", "device_fp": "…"}
//
// 记录域按"设备信任"放行（2026-09-21 实测）：新设备即时注册指纹也会被
// 1034/5003 拒绝；填入 App 长期使用的设备身份即可稳定调用。
// 文件位于凭据目录（与 credentials.json 同级），不存在时回退会话设备。
func loadRecordDevice(deps Deps) *protocol.DeviceContext {
	dir := filepath.Dir(deps.Store.Path())
	data, err := os.ReadFile(filepath.Join(dir, "record_device.json"))
	if err != nil {
		return nil
	}
	var v struct {
		DeviceID string `json:"device_id"`
		DeviceFP string `json:"device_fp"`
	}
	if json.Unmarshal(data, &v) != nil || v.DeviceID == "" || v.DeviceFP == "" {
		return nil
	}
	return &protocol.DeviceContext{DeviceID: v.DeviceID, DeviceFP: v.DeviceFP}
}

// runRoleNote 编排 role note：
// 全部绑定角色 → 本地 game_biz 精确过滤 → 仅在存在已验证游戏时换取
// LToken 并逐个查询。每个角色给出 available/unsupported/failed 状态；
// 任一已支持角色查询失败时继续处理其余角色，最终以 PARTIAL_FAILURE
// （退出码 5）返回与正常列表同形状的 partial_data；LToken 失败按原始
// 认证/远端错误分类，不改标为"暂不支持"。
func runRoleNote(cmd *cobra.Command, deps Deps, sess session.Session, warnings []string, gameBiz string) error {
	rsvc := role.New(deps.ClientFor(protocol.HostTakumiMiyoushe))
	all, oerr := rsvc.List(cmd.Context(), sess, "")
	if oerr != nil {
		return oerr
	}
	roles := filterRoles(all, gameBiz)

	ctx := map[string]any{}
	if gameBiz != "" {
		ctx["game_biz"] = gameBiz
	}
	pag := output.Pagination{Mode: "cursor", Resumable: false, NextArgs: []string{}}

	// 未验证游戏直接标 unsupported，保持角色顺序；已验证游戏的查询结果
	// 按下标回填。没有任何已验证角色时不换 LToken。
	notes := make(map[int]note.Note, len(roles))
	queryable := make([]int, 0, len(roles))
	for i, r := range roles {
		if note.Verified(r.GameBiz) {
			queryable = append(queryable, i)
			continue
		}
		notes[i] = *note.Unsupported(r)
	}

	available, failed := 0, 0
	if len(queryable) > 0 {
		nsvc := newNoteService(deps)
		ltoken, oerr := nsvc.GetLToken(cmd.Context(), sess)
		if oerr != nil {
			return oerr
		}
		for _, i := range queryable {
			n, oerr := nsvc.Fetch(cmd.Context(), sess, ltoken, roles[i])
			if oerr != nil {
				if n == nil {
					n = failedNoteItem(roles[i], oerr)
				}
				notes[i] = *n
				failed++
				continue
			}
			notes[i] = *n
			available++
		}
	}

	items := make([]note.Note, 0, len(roles))
	for i := range roles {
		if n, ok := notes[i]; ok {
			items = append(items, n)
		}
	}

	var partial *output.Error
	if failed > 0 {
		oe := output.Err(output.CodePartialFailure,
			"%d of %d note queries failed; per-role states are in partial_data", failed, len(queryable))
		oe = oe.WithContext(map[string]any{
			"available_count":   available,
			"unsupported_count": len(roles) - len(queryable),
			"failed_count":      failed,
		})
		oe.PartialData = output.NewListData(items, false, ctx, pag)
		oe.Warnings = warnings
		partial = oe
	}

	if jsonMode(cmd) {
		if partial != nil {
			return partial
		}
		return output.Success(deps.Out, output.NewListData(items, false, ctx, pag), "", warnings)
	}
	printWarnings(deps, cmd, warnings)
	if len(items) == 0 {
		if gameBiz != "" {
			fmt.Fprintf(deps.Out, "No bound roles match game_biz %s\n", presentation.SafeInline(gameBiz))
			return nil
		}
		fmt.Fprintln(deps.Out, "Current account has no bound game roles")
		return nil
	}
	// 部分失败属于失败终态：逐角色状态写入 stderr，保持 stdout 为空。
	out := io.Writer(deps.Out)
	if partial != nil {
		out = deps.ErrOut
	}
	printRoleNotes(out, items)
	if partial != nil {
		return partial
	}
	return nil
}

// failedNoteItem 构造失败角色的占位结果（Fetch 未返回结果时的防御路径）。
func failedNoteItem(r role.Role, oerr *output.Error) *note.Note {
	code := oerr.Code
	return &note.Note{
		GameBiz: r.GameBiz, Region: r.Region, UID: r.GameUID,
		Status: note.StatusFailed, Supported: note.Verified(r.GameBiz),
		Summary: []string{}, Reason: oerr.Message, ErrorCode: &code,
	}
}

// printRoleNotes 人类模式输出：状态直观可见，远端文本经安全渲染。
// w 由调用方选择：成功写 stdout；部分失败写 stderr（
// 人类模式失败时 stdout 必须为空）。
func printRoleNotes(w io.Writer, items []note.Note) {
	for _, n := range items {
		fmt.Fprintf(w, "%s  %s  %s  [%s]\n",
			presentation.SafeInline(n.GameBiz), presentation.SafeInline(n.Region),
			presentation.SafeInline(n.UID), n.Status)
		switch n.Status {
		case note.StatusAvailable:
			if len(n.Summary) == 0 {
				fmt.Fprintln(w, "  No note data")
				continue
			}
			for _, line := range n.Summary {
				fmt.Fprintf(w, "  %s\n", presentation.SafeInline(line))
			}
		case note.StatusUnsupported:
			fmt.Fprintf(w, "  Not supported: %s\n", presentation.SafeInline(n.Reason))
		default:
			fmt.Fprintf(w, "  Query failed: %s\n", presentation.SafeInline(n.Reason))
		}
	}
}
