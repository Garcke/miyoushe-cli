package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/favorite"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/post"
	"mihoyo_cli/internal/presentation"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/role"
)

func newFavoriteCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "favorite",
		Short: "View favorites",
	}
	cmd.RunE = groupRunE(cmd)
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
		Short: "View the favorite posts of a given role",
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

			warnings = append(warnings, fmt.Sprintf("Using role %s (%s)", r.GameUID, r.RegionName))

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
				next, pag := listPagination(page.NextCursor, page.HasMore, nil)
				ctx := map[string]any{
					"game_uid": r.GameUID,
					"region":   r.Region,
				}
				return output.Success(deps.Out,
					output.NewListData(items, page.HasMore, ctx, pag),
					next, warnings)
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
				fmt.Fprintln(deps.Out, "This role has no favorite posts")
				return nil
			}
			for _, rr := range rows {
				fmt.Fprintf(deps.Out, "%s  vt=%d  %s  %s\n",
					presentation.SafeInline(rr.id), rr.viewType, formatTime(rr.createdAt), truncate(presentation.TitleOrPlaceholder(rr.subject), 40))
			}
			if page.HasMore {
				fmt.Fprintf(deps.Out, "Next cursor: %s\n", presentation.SafeInline(page.NextCursor))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&roleSel, "role", "", "Role selector: game_biz:game_uid:region or a uniquely matching game_uid")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Server-side opaque cursor")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum number of items to output")
	cmd.Flags().BoolVar(&full, "full", false, "Also fetch post details for each item (more requests; original order preserved)")
	return cmd
}
