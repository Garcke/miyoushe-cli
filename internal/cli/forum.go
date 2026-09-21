package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/forum"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/session"
)

// newForumCmd 社区/分区浏览命令组。分区目录与讨论区匿名可调；
// 分区帖子流需登录会话（Cookie + bbs DS）。
func newForumCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forum",
		Short: "浏览游戏社区与分区帖子",
		Long: `浏览米游社各游戏社区的分区（版块）。
不同游戏的分区集合不同（原神 6 区、因缘精灵 2 区、大别野 8 区…）。
先用 forum games 找到游戏 gids 与分区 ID，再用 forum discussion <gids>
查看讨论区与各分区规则，用 forum posts <forum-id> --gids <id> 浏览帖子。`,
	}
	cmd.AddCommand(
		newForumGamesCmd(deps),
		newForumDiscussionCmd(deps),
		newForumPostsCmd(deps),
	)
	return cmd
}

func newForumClient(deps Deps) *api.Client {
	return deps.ClientFor(protocol.HostBBS)
}

func newForumGamesCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "games",
		Short: "查看全游戏分区目录",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := forum.New(newForumClient(deps), loadSessionOrEmpty(deps))
			games, oerr := svc.GamesForums(cmd.Context())
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				return output.Success(deps.Out, map[string]any{"games": games}, "", nil)
			}
			for _, g := range games {
				fmt.Fprintf(deps.Out, "game_id=%s（%d 个分区）:\n", g.GameID.String(), len(g.Forums))
				for _, f := range g.Forums {
					fmt.Fprintf(deps.Out, "  %s  %s\n", f.ID.String(), f.Name)
				}
			}
			return nil
		},
	}
	return cmd
}

func newForumPostsCmd(deps Deps) *cobra.Command {
	var gids, cursor, sortType string
	cmd := &cobra.Command{
		Use:   "posts <forum-id>",
		Short: "浏览分区帖子流（如 26=原神·酒馆 948=因缘会馆）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, warnings, oerr := loadSessionWithWarning(deps)
			if oerr != nil {
				return oerr
			}
			svc := forum.New(newForumClient(deps), sess)
			page, oerr := svc.ForumPosts(cmd.Context(), forum.ForumPostsOptions{
				ForumID:  args[0],
				GIDs:     gids,
				SortType: sortType,
				LastID:   cursor,
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
	cmd.Flags().StringVar(&gids, "gids", "", "所属游戏 gids（必须；分区所属社区，如 26→2 948→9）")
	cmd.Flags().StringVar(&sortType, "sort", "", "排序（服务端语义，实测 1 为另一排序）")
	cmd.Flags().StringVar(&cursor, "cursor", "", "服务端不透明游标（每页固定 20 条）")
	cmd.MarkFlagRequired("gids")
	return cmd
}

// newForumDiscussionCmd 查看单游戏讨论区与分区元数据（匿名只读，V3 §5.1）。
func newForumDiscussionCmd(deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "discussion <gids>",
		Short: "查看游戏讨论区与各分区规则（匿名）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := forum.New(newForumClient(deps), loadSessionOrEmpty(deps))
			d, oerr := svc.Discussion(cmd.Context(), args[0])
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				return output.Success(deps.Out, d, "", nil)
			}
			fmt.Fprintf(deps.Out, "游戏 gids: %s\n", args[0])
			fmt.Fprintf(deps.Out, "讨论区: %s (%s)\n", d.DiscussionID.String(), notProvided(d.Subject))
			for _, f := range d.Forums {
				fmt.Fprintf(deps.Out, "  %s  %s\n", f.ID.String(), notProvided(f.Name))
				// 描述行始终输出；缺失时显示“未提供”，不静默省略（R3）。
				fmt.Fprintf(deps.Out, "      描述: %s\n", notProvided(f.Des))
			}
			return nil
		},
	}
}

// notProvided 缺失字段显示“未提供”，不凭空推断（V3 §5.1）。
func notProvided(s string) string {
	if s == "" {
		return "未提供"
	}
	return s
}

// loadSessionOrEmpty 加载会话；无凭据时返回空会话（匿名接口仍可用）。
func loadSessionOrEmpty(deps Deps) session.Session {
	sess, err := deps.Store.Load()
	if err != nil || sess == nil {
		return session.Session{}
	}
	return session.Session{
		UID: sess.UID, MID: sess.MID, Stoken: sess.Stoken,
		DeviceID: sess.DeviceID, DeviceFP: sess.DeviceFP,
	}
}
