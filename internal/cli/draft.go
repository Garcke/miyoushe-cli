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
		viewType int
		cursor   string
		limit    int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "查看草稿箱列表（默认跨桶首页预览）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("kind") {
				return output.Err(output.CodeInputInvalid,
					"--kind 已移除：草稿的 view_type 桶与内容类型不是一一对应（视频草稿与长文同为 view_type=5），"+
						"逐条详情分类尚未实现；请改用 --view-type 1|2|5 按桶查看")
			}
			sess, warnings, oerr := loadSessionWithWarning(deps)
			if oerr != nil {
				return oerr
			}
			svc := draft.New(deps.ClientFor(protocol.HostBBS))
			page, oerr := svc.List(cmd.Context(), sess, draft.ListOptions{
				ViewType: viewType,
				Cursor:   cursor,
				Limit:    limit,
			})
			if oerr != nil {
				return oerr
			}
			warnings = append(warnings, page.Warnings...)
			if jsonMode(cmd) {
				if page.Items == nil {
					page.Items = []draft.Draft{}
				}
				return output.Success(deps.Out,
					output.ListData{Items: page.Items, HasMore: page.HasMore},
					page.NextCursor, warnings)
			}
			printWarnings(deps, cmd, warnings)
			if len(page.Items) == 0 {
				fmt.Fprintln(deps.Out, "草稿箱为空")
				return nil
			}
			for _, it := range page.Items {
				fmt.Fprintf(deps.Out, "%s  vt=%d  %s  %s\n",
					it.DraftID, it.ViewType, formatTime(it.UpdatedAt), truncate(it.Subject, 40))
			}
			if page.HasMore && page.NextCursor != "" {
				fmt.Fprintf(deps.Out, "下一页 cursor: %s\n", page.NextCursor)
			} else if page.HasMore {
				fmt.Fprintln(deps.Out, "还有更多草稿（跨桶预览不支持续页，请用 --view-type 1|2|5 --cursor 遍历）")
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&viewType, "view-type", 0, "草稿类型桶：1/2/5（默认 0=跨桶首页预览）")
	cmd.Flags().StringVar(&cursor, "cursor", "", "服务端不透明游标（仅单桶模式）")
	cmd.Flags().IntVar(&limit, "limit", 20, "本次输出总数上限")
	// --kind 保留占位以给出明确迁移提示（ARCHITECTURE-V2 §6：桶号≠内容类型）。
	cmd.Flags().String("kind", "", "已移除（见 --help 输出与 --kind 报错说明）")
	_ = cmd.Flags().MarkHidden("kind")
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
