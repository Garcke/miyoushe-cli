package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/draft"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/presentation"
	"mihoyo_cli/internal/protocol"
)

func newDraftCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "draft",
		Short: "View drafts",
	}
	cmd.RunE = groupRunE(cmd)
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
		Short: "View the draft list (cross-bucket first-page preview by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("kind") {
				return output.Err(output.CodeInputInvalid,
					"--kind has been removed: the draft view_type buckets do not map one-to-one to content types (video drafts and long posts share view_type=5), "+
						"and per-item detail classification is not implemented; use --view-type 1|2|5 to browse by bucket")
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
			next := page.NextCursor
			if !page.HasMore {
				next = ""
			}
			mode := "cursor"
			resumable := page.HasMore && next != ""
			ctx := map[string]any{}
			var nextArgs []string
			if viewType == 0 {
				// Cross-bucket first-page preview cannot paginate .
				mode = "preview"
				resumable = false
				ctx["scope"] = "cross-bucket-preview"
				nextArgs = []string{"draft", "list", "--view-type", "1"}
			} else {
				ctx["view_type"] = viewType
				if cursor != "" {
					ctx["cursor"] = cursor
				}
				if next != "" {
					nextArgs = []string{"draft", "list", "--view-type", strconv.Itoa(viewType), "--cursor", next}
				}
			}
			if jsonMode(cmd) {
				items := page.Items
				if items == nil {
					items = []draft.Draft{}
				}
				// 服务端 view_type 与来源桶冲突等提示进入结构化 notices。
				return output.SuccessNotices(deps.Out,
					output.NewListData(items, page.HasMore, ctx, output.Pagination{
						Mode: mode, Resumable: resumable, NextArgs: nextArgs,
					}),
					next, warnings, page.Notices)
			}
			printWarnings(deps, cmd, warnings)
			for _, n := range page.Notices {
				fmt.Fprintf(deps.ErrOut, "Warning: %s\n", presentation.SafeInline(n.Message))
			}
			if len(page.Items) == 0 {
				fmt.Fprintln(deps.Out, "Draft box is empty")
				return nil
			}
			for _, it := range page.Items {
				fmt.Fprintf(deps.Out, "%s  %s  %s  %s\n",
					presentation.SafeInline(it.DraftID), it.ContentTypeLabel, formatTime(it.UpdatedAt),
					truncate(presentation.TitleOrPlaceholder(it.Subject), 40))
			}
			if page.HasMore && page.NextCursor != "" {
				fmt.Fprintf(deps.Out, "Next cursor: %s\n", presentation.SafeInline(page.NextCursor))
			} else if page.HasMore {
				fmt.Fprintln(deps.Out, "More drafts available (cross-bucket preview cannot paginate; use --view-type 1|2|5 --cursor)")
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&viewType, "view-type", 0, "Draft type bucket: 1/2/5 (default 0 = cross-bucket first-page preview)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Server-side opaque cursor (single-bucket mode only)")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum number of items to output")
	// --kind 保留占位以给出明确迁移提示。
	cmd.Flags().String("kind", "", "Removed (see --help output and the --kind error message)")
	_ = cmd.Flags().MarkHidden("kind")
	return cmd
}

func newDraftShowCmd(deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "show <draft-id>",
		Short: "View draft details",
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
			fmt.Fprintf(deps.Out, "Draft: %s\n", presentation.SafeInline(d.DraftID))
			fmt.Fprintf(deps.Out, "Type: %s\n", d.ContentTypeLabel)
			fmt.Fprintf(deps.Out, "Title: %s\n", presentation.TitleOrPlaceholder(d.Subject))
			fmt.Fprintf(deps.Out, "Body: %s\n", presentation.BodyOrPlaceholder(d.Describe))
			fmt.Fprintf(deps.Out, "Images: %d\n", len(d.Images))
			for _, u := range d.Images {
				fmt.Fprintf(deps.Out, "  - %s\n", presentation.SafeInline(u))
			}
			return nil
		},
	}
}
