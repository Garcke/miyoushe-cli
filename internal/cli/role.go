package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/role"
)

func newRoleCmd(deps Deps) *cobra.Command {
	var gameBiz string
	cmd := &cobra.Command{
		Use:   "role",
		Short: "绑定角色管理",
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "查看当前账号绑定的游戏角色",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, warnings, oerr := loadSessionWithWarning(deps)
			if oerr != nil {
				return oerr
			}
			svc := role.New(deps.ClientFor(protocol.HostTakumiMiyoushe))
			roles, oerr := svc.List(cmd.Context(), sess, gameBiz)
			if oerr != nil {
				return oerr
			}
			if roles == nil {
				roles = []role.Role{}
			}
			if jsonMode(cmd) {
				return output.Success(deps.Out, output.ListData{Items: roles, HasMore: false}, "", warnings)
			}
			printWarnings(deps, cmd, warnings)
			if len(roles) == 0 {
				fmt.Fprintln(deps.Out, "当前账号没有绑定游戏角色")
				return nil
			}
			for _, r := range roles {
				chosen := ""
				if r.IsChosen {
					chosen = " *"
				}
				fmt.Fprintf(deps.Out, "%s  %s  %s  Lv.%d  %s (%s)%s\n",
					r.GameBiz, r.Region, r.GameUID, r.Level, r.Nickname, r.RegionName, chosen)
			}
			return nil
		},
	}
	list.Flags().StringVar(&gameBiz, "game-biz", "", "按游戏业务过滤（如 hk4e_cn）")
	cmd.AddCommand(list)
	return cmd
}
