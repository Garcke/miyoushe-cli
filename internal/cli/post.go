package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/post"
	"mihoyo_cli/internal/presentation"
	"mihoyo_cli/internal/protocol"
)

func newPostCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "post",
		Short: "View posts",
	}
	cmd.RunE = groupRunE(cmd)
	cmd.AddCommand(newPostListCmd(deps), newPostShowCmd(deps))
	return cmd
}

func newPostListCmd(deps Deps) *cobra.Command {
	var (
		uid    string
		gids   int
		cursor string
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "View the post list (current account by default; --uid for another user's public posts)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, warnings, oerr := loadSessionWithWarning(deps)
			if oerr != nil {
				return oerr
			}
			svc := post.New(deps.ClientFor(protocol.HostBBS))
			page, oerr := svc.List(cmd.Context(), sess, post.ListOptions{
				UID:    uid,
				GIDs:   gids,
				Cursor: cursor,
				Limit:  limit,
			})
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				next, pag := listPagination(page.NextCursor, page.HasMore, nil)
				ctx := map[string]any{}
				if uid != "" {
					ctx["uid"] = uid
				}
				if gids > 0 {
					ctx["gids"] = gids
				}
				items := page.Items
				if items == nil {
					items = []post.Summary{}
				}
				return output.Success(deps.Out,
					output.NewListData(items, page.HasMore, ctx, pag),
					next, warnings)
			}
			printWarnings(deps, cmd, warnings)
			if len(page.Items) == 0 {
				fmt.Fprintln(deps.Out, "No posts")
				return nil
			}
			for _, it := range page.Items {
				fmt.Fprintf(deps.Out, "%s  %s  %s  %s\n",
					presentation.SafeInline(it.PostID), presentation.ClassifyPost(it.ViewType, false).Label, formatTime(it.CreatedAt),
					truncate(presentation.TitleOrPlaceholder(it.Subject), 40))
			}
			if page.HasMore {
				fmt.Fprintf(deps.Out, "Next cursor: %s\n", presentation.SafeInline(page.NextCursor))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&uid, "uid", "", "Target user UID (current account by default)")
	cmd.Flags().IntVar(&gids, "gids", 0, "Filter by game gids")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Server-side opaque cursor")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum number of items to output")
	return cmd
}

func newPostShowCmd(deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "show <post-id>",
		Short: "View post details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, warnings, oerr := loadSessionWithWarning(deps)
			if oerr != nil {
				return oerr
			}
			svc := post.New(deps.ClientFor(protocol.HostBBS))
			d, oerr := svc.Get(cmd.Context(), sess, args[0])
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				return output.Success(deps.Out, d, "", warnings)
			}
			printWarnings(deps, cmd, warnings)
			fmt.Fprintf(deps.Out, "Post: %s\n", presentation.SafeInline(d.PostID))
			fmt.Fprintf(deps.Out, "Type: %s\n", presentation.ClassifyPost(d.ViewType, len(d.Videos) > 0 && d.Videos[0].VideoID != "").Label)
			fmt.Fprintf(deps.Out, "Title: %s\n", presentation.TitleOrPlaceholder(d.Subject))
			if d.Author != "" || d.AuthorUID != "" {
				fmt.Fprintf(deps.Out, "Author: %s (%s)\n", presentation.SafeMaybe(d.Author), presentation.SafeInline(d.AuthorUID))
			}
			fmt.Fprintf(deps.Out, "Posted at: %s\n", formatTime(d.CreatedAt))
			fmt.Fprintf(deps.Out, "Body: %s\n", presentation.BodyOrPlaceholder(d.Describe))
			fmt.Fprintf(deps.Out, "Images: %d\n", len(d.Images))
			for _, u := range d.Images {
				fmt.Fprintf(deps.Out, "  - %s\n", presentation.SafeInline(u))
			}
			fmt.Fprintf(deps.Out, "Videos: %d\n", len(d.Videos))
			for _, v := range d.Videos {
				fmt.Fprintf(deps.Out, "  - id=%s duration=%dms %s\n", presentation.SafeInline(v.VideoID), v.DurationMS, presentation.SafeInline(v.URL))
			}
			return nil
		},
	}
}

func formatTime(sec int64) string {
	if sec <= 0 {
		return "-"
	}
	return time.Unix(sec, 0).Local().Format("2006-01-02 15:04")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
