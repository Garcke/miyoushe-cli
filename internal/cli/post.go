package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/post"
	"mihoyo_cli/internal/protocol"
)

func newPostCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "post",
		Short: "帖子查看（写命令按协议证据门禁逐步开放）",
	}
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
		Short: "查看帖子列表（默认当前账号，--uid 查看他人公开帖子）",
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
				return output.Success(deps.Out,
					output.ListData{Items: page.Items, HasMore: page.HasMore},
					page.NextCursor, warnings)
			}
			printWarnings(deps, cmd, warnings)
			if len(page.Items) == 0 {
				fmt.Fprintln(deps.Out, "没有帖子")
				return nil
			}
			for _, it := range page.Items {
				fmt.Fprintf(deps.Out, "%s  vt=%d  %s  %s\n",
					it.PostID, it.ViewType, formatTime(it.CreatedAt), truncate(it.Subject, 40))
			}
			if page.HasMore {
				fmt.Fprintf(deps.Out, "下一页 cursor: %s\n", page.NextCursor)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&uid, "uid", "", "目标用户 UID（默认当前账号）")
	cmd.Flags().IntVar(&gids, "gids", 0, "按游戏 gids 过滤")
	cmd.Flags().StringVar(&cursor, "cursor", "", "服务端不透明游标")
	cmd.Flags().IntVar(&limit, "limit", 20, "本次输出总数上限")
	return cmd
}

func newPostShowCmd(deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "show <post-id>",
		Short: "查看帖子详情",
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
			fmt.Fprintf(deps.Out, "帖子: %s (%s)\n", d.PostID, d.Subject)
			fmt.Fprintf(deps.Out, "view_type: %d\n", d.ViewType)
			if d.Author != "" || d.AuthorUID != "" {
				fmt.Fprintf(deps.Out, "作者: %s (%s)\n", d.Author, d.AuthorUID)
			}
			fmt.Fprintf(deps.Out, "发布时间: %s\n", formatTime(d.CreatedAt))
			if d.Describe != "" {
				fmt.Fprintf(deps.Out, "正文: %s\n", d.Describe)
			}
			fmt.Fprintf(deps.Out, "图片: %d 张\n", len(d.Images))
			for _, u := range d.Images {
				fmt.Fprintf(deps.Out, "  - %s\n", u)
			}
			fmt.Fprintf(deps.Out, "视频: %d 个\n", len(d.Videos))
			for _, v := range d.Videos {
				fmt.Fprintf(deps.Out, "  - id=%s duration=%dms %s\n", v.VideoID, v.DurationMS, v.URL)
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
