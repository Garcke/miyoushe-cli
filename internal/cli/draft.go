package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/draft"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/protocol"
)

func newDraftCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "draft",
		Short: "草稿查看（保存/发布/删除按协议证据门禁逐步开放）",
	}
	cmd.AddCommand(newDraftListCmd(deps), newDraftShowCmd(deps))
	return cmd
}

func newDraftListCmd(deps Deps) *cobra.Command {
	var (
		kind   string
		cursor string
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "查看草稿箱列表",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, warnings, oerr := loadSessionWithWarning(deps)
			if oerr != nil {
				return oerr
			}
			svc := draft.New(deps.ClientFor(protocol.HostBBS))
			page, oerr := svc.List(cmd.Context(), sess, draft.ListOptions{
				Cursor: cursor,
				Limit:  limit,
			})
			if oerr != nil {
				return oerr
			}
			items := page.Items
			if kind != "" {
				filtered := items[:0:0]
				for _, it := range items {
					if draft.KindFromViewType(it.ViewType) == kind {
						filtered = append(filtered, it)
					}
				}
				items = filtered
			}
			if jsonMode(cmd) {
				if items == nil {
					items = []draft.Draft{}
				}
				return output.Success(deps.Out,
					output.ListData{Items: items, HasMore: page.HasMore},
					page.NextCursor, warnings)
			}
			printWarnings(deps, cmd, warnings)
			if len(items) == 0 {
				fmt.Fprintln(deps.Out, "草稿箱为空")
				return nil
			}
			for _, it := range items {
				fmt.Fprintf(deps.Out, "%s  vt=%d  %s  %s\n",
					it.DraftID, it.ViewType, formatTime(it.UpdatedAt), truncate(it.Subject, 40))
			}
			if page.HasMore {
				fmt.Fprintf(deps.Out, "下一页 cursor: %s\n", page.NextCursor)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "按内容类型过滤（image/article/video）")
	cmd.Flags().StringVar(&cursor, "cursor", "", "服务端不透明游标")
	cmd.Flags().IntVar(&limit, "limit", 20, "本次输出总数上限")
	return cmd
}

func newDraftShowCmd(deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "show <draft-id>",
		Short: "查看草稿详情",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, warnings, oerr := loadSessionWithWarning(deps)
			if oerr != nil {
				return oerr
			}
			svc := draft.New(deps.ClientFor(protocol.HostBBS))
			d, oerr := svc.Get(cmd.Context(), sess, args[0])
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				return output.Success(deps.Out, d, "", warnings)
			}
			printWarnings(deps, cmd, warnings)
			fmt.Fprintf(deps.Out, "草稿: %s (%s)\n", d.DraftID, d.Subject)
			fmt.Fprintf(deps.Out, "view_type: %d\n", d.ViewType)
			if d.Describe != "" {
				fmt.Fprintf(deps.Out, "正文: %s\n", d.Describe)
			}
			fmt.Fprintf(deps.Out, "图片: %d 张\n", len(d.Images))
			for _, u := range d.Images {
				fmt.Fprintf(deps.Out, "  - %s\n", u)
			}
			return nil
		},
	}
}
