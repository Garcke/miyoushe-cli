package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/forum"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/post"
	"mihoyo_cli/internal/presentation"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/session"
)

// newForumCmd 社区/分区浏览命令组。
// games 与 list 匿名可调；posts 需要有效社区会话。
func newForumCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forum",
		Short: "Browse game communities and forum posts",
		Long: `Browse the forums (boards) of Miyoushe game communities.

  forum games                     list every game (GID, en_name, name)
  forum list --game <selector>    show one game's forums and rules
  forum posts --game --forum ...  browse a forum's post stream (requires login)
  forum feed --game --forum ...   anonymous hot/recent posts (no login)

--game accepts a positive GID or the exact en_name shown by forum games.
--forum accepts a positive forum ID or the exact forum name within that game.`,
	}
	cmd.RunE = groupRunE(cmd)
	cmd.AddCommand(
		newForumGamesCmd(deps),
		newForumListCmd(deps),
		newForumPostsCmd(deps),
		newForumFeedCmd(deps),
		newForumImagesCmd(deps),
	)
	return cmd
}

func newForumClient(deps Deps) *api.Client {
	return deps.ClientFor(protocol.HostBBS)
}

// resolveGame 读取游戏目录并解析 --game 选择器（匿名；纯本地解析）。
func resolveGame(ctx context.Context, deps Deps, selector string) (forum.GameMeta, *output.Error) {
	svc := forum.New(newForumClient(deps), session.Session{})
	games, oerr := svc.GamesMeta(ctx)
	if oerr != nil {
		return forum.GameMeta{}, oerr
	}
	return forum.ResolveGame(selector, games)
}

// resolveGameForum 解析 --game 与 --forum：归属校验在目录查询后、
// 帖子业务请求前完成。
func resolveGameForum(ctx context.Context, deps Deps, gameSel, forumSel string) (forum.GameMeta, forum.Forum, *output.Error) {
	game, oerr := resolveGame(ctx, deps, gameSel)
	if oerr != nil {
		return forum.GameMeta{}, forum.Forum{}, oerr
	}
	svc := forum.New(newForumClient(deps), session.Session{})
	disc, oerr := svc.Discussion(ctx, game.GIDs())
	if oerr != nil {
		return forum.GameMeta{}, forum.Forum{}, oerr
	}
	forums := make([]forum.Forum, 0, len(disc.Forums))
	for _, f := range disc.Forums {
		forums = append(forums, forum.Forum{ID: f.ID, Name: f.Name})
	}
	f, oerr := forum.ResolveForum(forumSel, forums)
	if oerr != nil {
		// 选择器失败时给出一条安全下一步：查看该游戏的分区目录。
		if oerr.Action == nil {
			oerr = oerr.WithAction(output.RunCommand("forum", "list", "--game", strings.TrimSpace(gameSel)))
		}
		return forum.GameMeta{}, forum.Forum{}, oerr
	}
	return game, f, nil
}

func newForumGamesCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "games",
		Short: "List every game community (GID, en_name, name)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := forum.New(newForumClient(deps), loadSessionOrEmpty(deps))
			games, oerr := svc.GamesMeta(cmd.Context())
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				items := make([]forum.GameMeta, 0, len(games))
				items = append(items, games...)
				return output.Success(deps.Out,
					output.NewListData(items, false, map[string]any{}, output.Pagination{
						Mode: "cursor", Resumable: false, NextArgs: []string{},
					}), "", nil)
			}
			// 人类输出只显示 GID / en_name / Name；has_wiki 只留在 JSON。
			for _, g := range games {
				fmt.Fprintf(deps.Out, "%s  %s  %s\n",
					presentation.SafeInline(g.GIDs()), presentation.SafeInline(g.ENName), presentation.SafeInline(g.Name))
			}
			return nil
		},
	}
	return cmd
}

// newForumListCmd 匿名查看单游戏的 Forum 目录与规则。
func newForumListCmd(deps Deps) *cobra.Command {
	var gameSel string
	cmd := &cobra.Command{
		Use:   "list --game <selector>",
		Short: "Show one game's forums and rules (anonymous)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			game, oerr := resolveGame(cmd.Context(), deps, gameSel)
			if oerr != nil {
				return oerr
			}
			svc := forum.New(newForumClient(deps), loadSessionOrEmpty(deps))
			disc, oerr := svc.Discussion(cmd.Context(), game.GIDs())
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				items := make([]map[string]any, 0, len(disc.Forums))
				for _, f := range disc.Forums {
					items = append(items, map[string]any{
						"forum_id":  f.ID.String(),
						"name":      f.Name,
						"des":       f.Des,
						"game_gids": game.GIDs(),
						"en_name":   game.ENName,
						"game_name": game.Name,
					})
				}
				ctx := map[string]any{"gids": game.GIDs(), "en_name": game.ENName}
				return output.Success(deps.Out,
					output.NewListData(items, false, ctx, output.Pagination{
						Mode: "cursor", Resumable: false, NextArgs: []string{},
					}), "", nil)
			}
			fmt.Fprintf(deps.Out, "%s (%s, gids=%s)\n",
				presentation.SafeInline(game.Name), presentation.SafeInline(game.ENName), presentation.SafeInline(game.GIDs()))
			for _, f := range disc.Forums {
				fmt.Fprintf(deps.Out, "  %s  %s\n",
					presentation.SafeInline(f.ID.String()), presentation.TitleOrPlaceholder(f.Name))
				// 描述行始终输出；缺失显示 Not provided，不推断发帖权限。
				fmt.Fprintf(deps.Out, "      Description: %s\n", presentation.TitleOrPlaceholder(f.Des))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&gameSel, "game", "", "Game selector: positive GID or exact en_name (see forum games)")
	_ = cmd.MarkFlagRequired("game")
	return cmd
}

// newForumPostsCmd 使用登录会话浏览分区帖子流。
// 不提供排序 flag：sort_type 保留为协议观察，命令固定服务端默认序。
func newForumPostsCmd(deps Deps) *cobra.Command {
	var gameSel, forumSel, cursor string
	cmd := &cobra.Command{
		Use:   "posts --game <selector> --forum <selector>",
		Short: "Browse a forum's post stream (requires login)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// 顺序：语法 → 游戏 → Forum 归属 → 会话 → 帖子请求。
			game, f, oerr := resolveGameForum(cmd.Context(), deps, gameSel, forumSel)
			if oerr != nil {
				return oerr
			}
			sess, warnings, oerr := loadSessionWithWarning(deps)
			if oerr != nil {
				return oerr
			}
			svc := forum.New(newForumClient(deps), sess)
			page, oerr := svc.ForumPosts(cmd.Context(), forum.ForumPostsOptions{
				ForumID: f.ID.String(),
				GIDs:    game.GIDs(),
				LastID:  cursor,
			})
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				next, pag := listPagination(page.NextCursor, page.HasMore, nil)
				ctx := map[string]any{
					"gids":     game.GIDs(),
					"forum_id": f.ID.String(),
					"forum":    f.Name,
				}
				items := page.Items
				if items == nil {
					items = []post.Summary{}
				}
				return output.Success(deps.Out,
					output.NewListData(items, page.HasMore, ctx, pag), next, warnings)
			}
			printWarnings(deps, cmd, warnings)
			if len(page.Items) == 0 {
				fmt.Fprintf(deps.Out, "No results found in %s.\n", presentation.SafeInline(f.Name))
				return nil
			}
			for _, it := range page.Items {
				fmt.Fprintf(deps.Out, "%s  %s  %s  %s\n",
					presentation.SafeInline(it.PostID), presentation.ClassifyPost(it.ViewType, false).Label, formatTime(it.CreatedAt),
					truncate(presentation.TitleOrPlaceholder(it.Subject), 40))
			}
			if page.HasMore {
				fmt.Fprintln(deps.Out, "More results are available.")
				fmt.Fprintf(deps.Out, "Next cursor: %s\n", presentation.SafeInline(page.NextCursor))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&gameSel, "game", "", "Game selector: positive GID or exact en_name (see forum games)")
	cmd.Flags().StringVar(&forumSel, "forum", "", "Forum selector: positive forum ID or exact forum name in that game")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Server-side opaque cursor (server-chosen page size)")
	_ = cmd.MarkFlagRequired("game")
	_ = cmd.MarkFlagRequired("forum")
	return cmd
}

// newForumFeedCmd 匿名分区帖子流：热帖（默认）或最新帖。
// 与需要登录的 forum posts 明确分离；--order 只选择两个匿名
// 端点，不向服务端透传 sort_type。
func newForumFeedCmd(deps Deps) *cobra.Command {
	var gameSel, forumSel, orderSel, cursor string
	cmd := &cobra.Command{
		Use:   "feed --game <selector> --forum <selector> [--order hot|recent]",
		Short: "Browse a forum's hot or recent posts anonymously (no login)",
		Long: `Browse a forum's post stream without logging in.

  --order hot     hot posts (default; paginates with an opaque cursor)
  --order recent  newest posts first (paginates with an opaque cursor)

Anonymous endpoints return roughly 20 posts per page and ignore page-size
parameters. Cold forums may have no hot posts; recent posts still return a page.
Use "forum posts" when you need the logged-in stream.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// 顺序：语法 → 游戏 → Forum 归属 → 请求（无会话；不得触发登录）。
			order := forum.FeedOrder(strings.ToLower(strings.TrimSpace(orderSel)))
			if order == "" {
				order = forum.FeedHot
			}
			if !forum.ValidFeedOrder(order) {
				return output.Err(output.CodeInputInvalid,
					"--order supports only hot or recent (got %q)", orderSel)
			}
			game, f, oerr := resolveGameForum(cmd.Context(), deps, gameSel, forumSel)
			if oerr != nil {
				return oerr
			}
			svc := forum.New(newForumClient(deps), session.Session{})
			page, oerr := svc.Feed(cmd.Context(), forum.FeedOptions{
				ForumID: f.ID.String(),
				GIDs:    game.GIDs(),
				Order:   order,
				Cursor:  cursor,
			})
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				next, pag := listPagination(page.NextCursor, page.HasMore, nil)
				ctx := map[string]any{
					"gids":     game.GIDs(),
					"forum_id": f.ID.String(),
					"forum":    f.Name,
					"order":    string(order),
				}
				items := page.Items
				if items == nil {
					items = []post.Summary{}
				}
				return output.Success(deps.Out,
					output.NewListData(items, page.HasMore, ctx, pag), next, nil)
			}
			if len(page.Items) == 0 {
				fmt.Fprintf(deps.Out, "No results found in %s (%s feed).\n",
					presentation.SafeInline(f.Name), order)
				return nil
			}
			for _, it := range page.Items {
				fmt.Fprintf(deps.Out, "%s  %s  %s  %s\n",
					presentation.SafeInline(it.PostID), presentation.ClassifyPost(it.ViewType, false).Label, formatTime(it.CreatedAt),
					truncate(presentation.TitleOrPlaceholder(it.Subject), 40))
			}
			if page.HasMore {
				fmt.Fprintln(deps.Out, "More results are available.")
				fmt.Fprintf(deps.Out, "Next cursor: %s\n", presentation.SafeInline(page.NextCursor))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&gameSel, "game", "", "Game selector: positive GID or exact en_name (see forum games)")
	cmd.Flags().StringVar(&forumSel, "forum", "", "Forum selector: positive forum ID or exact forum name in that game")
	cmd.Flags().StringVar(&orderSel, "order", "hot", "Feed: hot (default) or recent")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Feed-specific opaque cursor (hot: last_id; recent: page)")
	_ = cmd.MarkFlagRequired("game")
	_ = cmd.MarkFlagRequired("forum")
	return cmd
}

// notProvided 缺失字段显示 Not provided，不凭空推断。
func notProvided(s string) string {
	return presentation.TitleOrPlaceholder(s)
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

// newForumImagesCmd 图片分区榜单（同人榜）。实测（2026-09-21）：必须带 type
// （取自 getImagePostListType 的分类，缺省返回 0 条）；非图片分区分类为空。
// v4 文档未列该命令，保留并按新的选择器模型对齐参数。
func newForumImagesCmd(deps Deps) *cobra.Command {
	var gameSel, forumSel, listType, cursor string
	cmd := &cobra.Command{
		Use:   "images --game <selector> --forum <selector>",
		Short: "Browse an image forum ranking (fan art board, requires login)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			game, f, oerr := resolveGameForum(cmd.Context(), deps, gameSel, forumSel)
			if oerr != nil {
				return oerr
			}
			sess, warnings, oerr := loadSessionWithWarning(deps)
			if oerr != nil {
				return oerr
			}
			svc := forum.New(newForumClient(deps), sess)

			if listType == "" {
				types, oerr := svc.ImageTypes(cmd.Context(), game.GIDs(), f.ID.String())
				if oerr != nil {
					return oerr
				}
				if len(types) == 0 {
					if jsonMode(cmd) {
						return output.Success(deps.Out,
							output.NewListData([]post.Summary{}, false, map[string]any{
								"forum_id": f.ID.String(),
							}, output.Pagination{Mode: "cursor", Resumable: false, NextArgs: []string{}}),
							"", warnings)
					}
					printWarnings(deps, cmd, warnings)
					fmt.Fprintf(deps.Out, "Forum %s is not an image forum (no ranking categories).\n", presentation.SafeInline(f.ID.String()))
					return nil
				}
				listType = strconv.Itoa(types[0])
			}

			page, oerr := svc.ImagePosts(cmd.Context(), forum.ImagePostsOptions{
				ForumID: f.ID.String(),
				GIDs:    game.GIDs(),
				Type:    listType,
				LastID:  cursor,
			})
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				next, pag := listPagination(page.NextCursor, page.HasMore, nil)
				ctx := map[string]any{
					"gids":          game.GIDs(),
					"forum_id":      f.ID.String(),
					"ranking_type":  listType,
					"ranking_title": page.Title,
				}
				items := page.Items
				if items == nil {
					items = []post.Summary{}
				}
				return output.Success(deps.Out,
					output.NewListData(items, page.HasMore, ctx, pag), next, warnings)
			}
			printWarnings(deps, cmd, warnings)
			if len(page.Items) == 0 {
				fmt.Fprintln(deps.Out, "Ranking is empty.")
				return nil
			}
			fmt.Fprintf(deps.Out, "%s (type=%s, %d items)\n",
				presentation.TitleOrPlaceholder(page.Title), presentation.SafeInline(listType), len(page.Items))
			for _, it := range page.Items {
				fmt.Fprintf(deps.Out, "%s  %s  %s  %s\n",
					presentation.SafeInline(it.PostID), presentation.ClassifyPost(it.ViewType, false).Label, formatTime(it.CreatedAt),
					truncate(presentation.TitleOrPlaceholder(it.Subject), 40))
			}
			if page.HasMore {
				fmt.Fprintln(deps.Out, "More results are available.")
				fmt.Fprintf(deps.Out, "Next cursor: %s\n", presentation.SafeInline(page.NextCursor))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&gameSel, "game", "", "Game selector: positive GID or exact en_name (see forum games)")
	cmd.Flags().StringVar(&forumSel, "forum", "", "Forum selector: positive forum ID or exact forum name in that game")
	cmd.Flags().StringVar(&listType, "type", "", "Ranking type (defaults to the first category; measured 1/2/3)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Server-side opaque cursor")
	_ = cmd.MarkFlagRequired("game")
	_ = cmd.MarkFlagRequired("forum")
	return cmd
}
