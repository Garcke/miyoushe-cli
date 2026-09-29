package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/presentation"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/role"
)

// resolveGameBiz 解析 --game-biz：
// flag 未出现时返回 ""（不过滤）；显式空串或纯空白本地 INPUT_INVALID，
// 不发任何请求；两端空白去除后按角色接口原值大小写敏感匹配，不做大小写
// 转换或近似匹配，不接受论坛 GID/en_name 别名。
func resolveGameBiz(cmd *cobra.Command, raw string) (string, *output.Error) {
	if !cmd.Flags().Changed("game-biz") {
		return "", nil
	}
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", output.Err(output.CodeInputInvalid,
			"--game-biz cannot be empty or blank when provided (got %q)", raw)
	}
	return v, nil
}

// filterRoles 按 game_biz 对角色接口原值做精确（大小写敏感）过滤；
// want 为空时原样返回全部角色。role list 与 role note 使用同一规则。
func filterRoles(roles []role.Role, want string) []role.Role {
	if want == "" {
		return roles
	}
	out := make([]role.Role, 0, len(roles))
	for _, r := range roles {
		if r.GameBiz == want {
			out = append(out, r)
		}
	}
	return out
}

// newRoleCmd 角色命令组：
//
//	role list [--game-biz]   只列绑定角色，不触发 LToken 交换或便签查询
//	role note [--game-biz]   绑定角色的实时便签
//
// 旧 `role list --note` 与顶层 `note` 已删除，不保留别名或兼容期。
func newRoleCmd(deps Deps) *cobra.Command {
	var listGameBiz, noteGameBiz string
	cmd := &cobra.Command{
		Use:   "role",
		Short: "Bound game role management",
		Long: `Manage the game roles bound to the current account.

  role list [--game-biz <business-id>]   list bound roles (no note queries)
  role note [--game-biz <business-id>]   real-time notes for bound roles

--game-biz filters by the exact business ID from the role interface
(e.g. hk4e_cn, nap_cn); it is case-sensitive and does not accept forum
GIDs or en_name values. Notes are verified for Genshin (hk4e_cn) and
Zenless Zone Zero (nap_cn); other games are reported as unsupported.`,
	}
	cmd.RunE = groupRunE(cmd)

	list := &cobra.Command{
		Use:   "list",
		Short: "View bound game roles (no LToken exchange or note queries)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			gameBiz, oerr := resolveGameBiz(cmd, listGameBiz)
			if oerr != nil {
				return oerr
			}
			sess, warnings, oerr := loadSessionWithWarning(deps)
			if oerr != nil {
				return oerr
			}
			svc := role.New(deps.ClientFor(protocol.HostTakumiMiyoushe))
			all, oerr := svc.List(cmd.Context(), sess, "")
			if oerr != nil {
				return oerr
			}
			roles := filterRoles(all, gameBiz)
			if roles == nil {
				roles = []role.Role{}
			}
			if jsonMode(cmd) {
				ctx := map[string]any{}
				if gameBiz != "" {
					ctx["game_biz"] = gameBiz
				}
				return output.Success(deps.Out,
					output.NewListData(roles, false, ctx, output.Pagination{
						Mode: "cursor", Resumable: false, NextArgs: []string{},
					}), "", warnings)
			}
			printWarnings(deps, cmd, warnings)
			if len(roles) == 0 {
				if gameBiz != "" {
					fmt.Fprintf(deps.Out, "No bound roles match game_biz %s\n", presentation.SafeInline(gameBiz))
					return nil
				}
				fmt.Fprintln(deps.Out, "Current account has no bound game roles")
				return nil
			}
			for _, r := range roles {
				chosen := ""
				if r.IsChosen {
					chosen = " *"
				}
				fmt.Fprintf(deps.Out, "%s  %s  %s  Lv.%d  %s (%s)%s\n",
					presentation.SafeInline(r.GameBiz), presentation.SafeInline(r.Region),
					presentation.SafeInline(r.GameUID), r.Level,
					presentation.SafeInline(r.Nickname), presentation.SafeInline(r.RegionName), chosen)
			}
			return nil
		},
	}
	list.Flags().StringVar(&listGameBiz, "game-biz", "", "Filter by exact role business ID (e.g. hk4e_cn; case-sensitive)")

	noteCmd := &cobra.Command{
		Use:   "note",
		Short: "View real-time notes for bound roles (resin/energy/commissions/expeditions)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			gameBiz, oerr := resolveGameBiz(cmd, noteGameBiz)
			if oerr != nil {
				return oerr
			}
			sess, warnings, oerr := loadSessionWithWarning(deps)
			if oerr != nil {
				return oerr
			}
			return runRoleNote(cmd, deps, sess, warnings, gameBiz)
		},
	}
	noteCmd.Flags().StringVar(&noteGameBiz, "game-biz", "", "Filter by exact role business ID (e.g. hk4e_cn; case-sensitive)")

	cmd.AddCommand(list, noteCmd)
	return cmd
}
