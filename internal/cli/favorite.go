package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/favorite"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/post"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/role"
)

func newFavoriteCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "favorite",
		Short: "收藏查看（收藏/取消收藏按协议证据门禁逐步开放）",
	}
	cmd.AddCommand(newFavoriteListCmd(deps))
	return cmd
}

func newFavoriteListCmd(deps Deps) *cobra.Command {
	var (
		roleSel string
		cursor  string
		limit   int
		full    bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "查看指定角色的收藏帖子列表",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, warnings, oerr := loadSessionWithWarning(deps)
			if oerr != nil {
				return oerr
			}

			roleSvc := role.New(deps.ClientFor(protocol.HostTakumiMiyoushe))
			roles, oerr := roleSvc.List(cmd.Context(), sess, "")
			if oerr != nil {
				return oerr
			}
			r, oerr := favorite.ResolveSelector(roles, roleSel)
			if oerr != nil {
				return oerr
			}

			svc := favorite.New(deps.ClientFor(protocol.HostBBS))
			page, oerr := svc.List(cmd.Context(), sess, r, favorite.ListOptions{
				Cursor: cursor,
				Limit:  limit,
			})
			if oerr != nil {
				return oerr
			}

			warnings = append(warnings, fmt.Sprintf("使用角色 %s (%s)", r.GameUID, r.RegionName))

			var items any = page.Items
			if page.Items == nil {
				items = []post.Summary{}
			}
			if full {
				postSvc := post.New(deps.ClientFor(protocol.HostBBS))
				details, oerr := favorite.FillFull(cmd.Context(), sess, page.Items, postSvc, 2)
				if oerr != nil {
					return oerr
				}
				if details == nil {
					details = []post.Detail{}
				}
				items = details
			}

			if jsonMode(cmd) {
				return output.Success(deps.Out,
					output.ListData{Items: items, HasMore: page.HasMore},
					page.NextCursor, warnings)
			}
			printWarnings(deps, cmd, warnings)
			type row struct {
				id, subject string
				createdAt   int64
				viewType    int
			}
			var rows []row
			if full {
				for _, d := range items.([]post.Detail) {
					rows = append(rows, row{d.PostID, d.Subject, d.CreatedAt, d.ViewType})
				}
			} else {
				for _, s := range items.([]post.Summary) {
					rows = append(rows, row{s.PostID, s.Subject, s.CreatedAt, s.ViewType})
				}
			}
			if len(rows) == 0 {
				fmt.Fprintln(deps.Out, "该角色没有收藏帖子")
				return nil
			}
			for _, rr := range rows {
				fmt.Fprintf(deps.Out, "%s  vt=%d  %s  %s\n",
					rr.id, rr.viewType, formatTime(rr.createdAt), truncate(rr.subject, 40))
			}
			if page.HasMore {
				fmt.Fprintf(deps.Out, "下一页 cursor: %s\n", page.NextCursor)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&roleSel, "role", "", "角色选择器：game_biz:game_uid:region 或可唯一匹配的 game_uid")
	cmd.Flags().StringVar(&cursor, "cursor", "", "服务端不透明游标")
	cmd.Flags().IntVar(&limit, "limit", 20, "本次输出总数上限")
	cmd.Flags().BoolVar(&full, "full", false, "逐条补调帖子详情（更多请求，保留原顺序）")
	return cmd
}
