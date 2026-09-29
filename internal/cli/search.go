package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/post"
	"mihoyo_cli/internal/presentation"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/search"
)

// newSearchCmd 搜索命令组。三个接口均匿名可调（无需登录）；
// 帖子和综合搜索要求显式提供 --gids，不使用默认社区。
func newSearchCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Search posts, topics and mixed results (anonymous)",
		Long: `Search Miyoushe content.
Post and mixed search require an explicit --gids (positive integer). There is
no default community and no site-wide mode; run "forum games" to discover GIDs.
Unknown GIDs are decided by the server. Topic search is cross-community and
takes no --gids (the endpoint does not filter by GID).
search posts --order accepts only the verified protocol values: 1 = hot (the
server default) and 2 = newest first. All other values are rejected locally
before any request.
Mixed search takes no --preview.`,
	}
	cmd.RunE = groupRunE(cmd)
	// 证据门禁：users / wiki / guides / videos / images / official
	// 在各自门禁通过前不注册独立命令（适配器可留在内部包中，不暴露到 Help）。
	cmd.AddCommand(
		newSearchPostsCmd(deps),
		newSearchTopicsCmd(deps),
		newSearchAllCmd(deps),
	)
	return cmd
}

func newSearchService(deps Deps) *search.Service {
	return search.New(deps.ClientFor(protocol.HostBBS))
}

// requireGIDsFlag 解析 --gids：必须显式传入严格正整数
// （[1-9][0-9]*，可表示为 int64，无前导零/加号/小数）；未出现、空值、
// 空白、0、负数、Unicode 数字与溢出都在创建客户端和网络请求之前失败。
// 不从配置、环境变量或历史会话补入默认值。
func requireGIDsFlag(cmd *cobra.Command, gids string) (string, *output.Error) {
	if !cmd.Flags().Changed("gids") {
		return "", output.Err(output.CodeInputInvalid, "Missing required option --gids").
			WithAction(output.RunCommand(output.Executable, "forum", "games"))
	}
	v := strings.TrimSpace(gids)
	if !positiveIntPattern.MatchString(v) {
		return "", output.Err(output.CodeInputInvalid,
			"--gids must be a positive integer in [1-9][0-9]* (got %q)", gids).
			WithAction(output.RunCommand(output.Executable, "forum", "games"))
	}
	if _, err := strconv.ParseInt(v, 10, 64); err != nil {
		return "", output.Err(output.CodeInputInvalid, "--gids overflows int64 (got %q)", gids)
	}
	return v, nil
}

// positiveIntPattern 与 forum 选择器使用同一形态约束，但不做目录白名单：
// 搜索的未知 GID 交给远端处理，以兼容新社区。
var positiveIntPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

// requireSearchLimit 校验 --limit：未指定用 20；显式 0/1/2/负数报错，
// 不静默改成 3 后多输出结果。
func requireSearchLimit(cmd *cobra.Command, limit int) (int, *output.Error) {
	if !cmd.Flags().Changed("limit") {
		return 20, nil
	}
	if limit < 3 {
		return 0, output.Err(output.CodeInputInvalid,
			"--limit must be at least 3 (the server returns an empty list below 3; input %d is not silently changed)", limit)
	}
	return limit, nil
}

// requireSearchOrder 校验 --order：
// 只接受已验证的协议取值 1（最热，等价服务端默认）与 2（最新，created_at
// 严格倒序）；其他取值（含 0/3/4/5 与非数字）在发请求前本地拒绝，零请求。
// 缺省不传时保持服务端默认，不在本地改写为 1。
func requireSearchOrder(cmd *cobra.Command, order string) (string, *output.Error) {
	if !cmd.Flags().Changed("order") {
		return "", nil
	}
	v := strings.TrimSpace(order)
	if v != "1" && v != "2" {
		return "", output.Err(output.CodeInputInvalid,
			"--order accepts only the verified protocol values 1 (hot, the server default) and 2 (newest first); got %q", order)
	}
	return v, nil
}

// searchListData 构造搜索列表命令的统一 data：
//   - resumable 仅在 has_more 且游标非空时为 true；
//   - 可续页时 next_args = baseArgs + ["--cursor", next_cursor, "--json"]
//     （参数化执行数组，不含可执行文件名，不得拼成 shell 字符串）；
//   - 不可续页时 next_args=[]，顶层游标由调用方清空。
func searchListData(items any, hasMore bool, nextCursor string, baseArgs []string, ctx map[string]any) output.ListData {
	nextArgs := []string{}
	resumable := hasMore && nextCursor != ""
	if resumable {
		nextArgs = append(nextArgs, baseArgs...)
		nextArgs = append(nextArgs, "--cursor", nextCursor, "--json")
	}
	return output.NewListData(items, hasMore, ctx, output.Pagination{
		Mode: "cursor", Resumable: resumable, NextArgs: nextArgs,
	})
}

// searchNextCursor 归一化顶层游标：不可续页或游标为空时必须为空串，
// 不把空游标编造成可继续的请求。
func searchNextCursor(hasMore bool, nextCursor string) string {
	if !hasMore || nextCursor == "" {
		return ""
	}
	return nextCursor
}

func newSearchPostsCmd(deps Deps) *cobra.Command {
	var gids, cursor, orderType string
	var limit int
	cmd := &cobra.Command{
		Use:   "posts <keyword>",
		Short: "Search posts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			gidsVal, oerr := requireGIDsFlag(cmd, gids)
			if oerr != nil {
				return oerr
			}
			limitVal, oerr := requireSearchLimit(cmd, limit)
			if oerr != nil {
				return oerr
			}
			orderVal, oerr := requireSearchOrder(cmd, orderType)
			if oerr != nil {
				return oerr
			}
			svc := newSearchService(deps)
			page, oerr := svc.Posts(cmd.Context(), search.PostsOptions{
				Keyword:   args[0],
				GIDs:      gidsVal,
				LastID:    cursor,
				Size:      limitVal,
				OrderType: orderVal,
			})
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				ctx := map[string]any{"query": args[0], "gids": gidsVal, "scope": "posts"}
				if cmd.Flags().Changed("order") {
					ctx["order"] = orderVal
				}
				nextArgs := []string{"search", "posts", args[0], "--gids", gidsVal, "--limit", strconv.Itoa(limitVal)}
				if cmd.Flags().Changed("order") {
					nextArgs = append(nextArgs, "--order", orderVal)
				}
				next := searchNextCursor(page.HasMore, page.NextCursor)
				return output.Success(deps.Out,
					searchListData(page.Items, page.HasMore, next, nextArgs, ctx), next, nil)
			}
			printSearchPosts(deps, page.Items)
			if page.HasMore {
				fmt.Fprintf(deps.Out, "Next cursor: %s\n", presentation.SafeInline(page.NextCursor))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&gids, "gids", "", "Community GIDs (required; positive integer; see forum games)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Server-side opaque cursor")
	cmd.Flags().StringVar(&orderType, "order", "", "Sort order: 1 = hot (server default), 2 = newest first (only these verified values)")
	cmd.Flags().IntVar(&limit, "limit", 20, "Number of items for this request (>=3)")
	return cmd
}

func newSearchTopicsCmd(deps Deps) *cobra.Command {
	var cursor string
	var limit int
	cmd := &cobra.Command{
		Use:   "topics <keyword>",
		Short: "Search topics (cross-community; no per-game filtering)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			limitVal, oerr := requireSearchLimit(cmd, limit)
			if oerr != nil {
				return oerr
			}
			svc := newSearchService(deps)
			page, oerr := svc.Topics(cmd.Context(), search.TopicsOptions{
				Keyword: args[0],
				LastID:  cursor,
				Size:    limitVal,
			})
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				ctx := map[string]any{"query": args[0], "scope": "topics"}
				nextArgs := []string{"search", "topics", args[0], "--limit", strconv.Itoa(limitVal)}
				next := searchNextCursor(page.HasMore, page.NextCursor)
				return output.Success(deps.Out,
					searchListData(page.Items, page.HasMore, next, nextArgs, ctx), next, nil)
			}
			if len(page.Items) == 0 {
				fmt.Fprintln(deps.Out, "No topics")
				return nil
			}
			for _, it := range page.Items {
				fmt.Fprintf(deps.Out, "%s  %s\n", presentation.SafeInline(it.ID.String()), presentation.SafeInline(it.Name))
			}
			if page.HasMore {
				fmt.Fprintf(deps.Out, "Next cursor: %s\n", presentation.SafeInline(page.NextCursor))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cursor, "cursor", "", "Server-side opaque cursor")
	cmd.Flags().IntVar(&limit, "limit", 20, "Number of items for this request (>=3)")
	return cmd
}

func newSearchUsersCmd(deps Deps) *cobra.Command {
	var cursor string
	var limit int
	cmd := &cobra.Command{
		Use:   "users <keyword>",
		Short: "Search users",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newSearchService(deps)
			page, oerr := svc.Users(cmd.Context(), search.UsersOptions{
				Keyword: args[0],
				LastID:  cursor,
				Size:    limit,
			})
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				return output.Success(deps.Out,
					output.ListData{Items: page.Items, HasMore: page.HasMore},
					page.NextCursor, nil)
			}
			if len(page.Items) == 0 {
				fmt.Fprintln(deps.Out, "No users")
				return nil
			}
			for _, it := range page.Items {
				intro := it.Introduce
				if intro == "" {
					intro = "-"
				}
				fmt.Fprintf(deps.Out, "%s  %s  %s\n",
					it.UID.String(), presentation.SafeInline(it.Nickname), truncate(presentation.SafeInline(intro), 30))
			}
			if page.HasMore {
				fmt.Fprintf(deps.Out, "Next cursor: %s\n", presentation.SafeInline(page.NextCursor))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cursor, "cursor", "", "Server-side opaque cursor")
	cmd.Flags().IntVar(&limit, "limit", 20, "Number of items for this request")
	return cmd
}

func newSearchGuidesCmd(deps Deps) *cobra.Command {
	var gids, cursor string
	var limit int
	cmd := &cobra.Command{
		Use:   "guides <keyword>",
		Short: "Search guides (the index covers guide content only; broad keywords may return 0)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newSearchService(deps)
			page, oerr := svc.Walkthroughs(cmd.Context(), search.WalkthroughsOptions{
				Keyword: args[0],
				GIDs:    gids,
				LastID:  cursor,
				Size:    limit,
			})
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				return output.Success(deps.Out, map[string]any{
					"posts":    page.Posts,
					"topics":   page.Topics,
					"has_more": page.HasMore,
				}, page.NextCursor, nil)
			}
			if len(page.Posts) == 0 && len(page.Topics) == 0 {
				fmt.Fprintln(deps.Out, "No guide results (the index covers guide content only; try a more specific keyword)")
				return nil
			}
			if len(page.Posts) > 0 {
				fmt.Fprintf(deps.Out, "Guide posts (%d):\n", len(page.Posts))
				for _, it := range page.Posts {
					fmt.Fprintf(deps.Out, "%s  %s  %s  %s\n",
						presentation.SafeInline(it.PostID), presentation.ClassifyPost(it.ViewType, false).Label, formatTime(it.CreatedAt),
						truncate(presentation.TitleOrPlaceholder(it.Subject), 40))
				}
			}
			if len(page.Topics) > 0 {
				fmt.Fprintf(deps.Out, "Related topics (%d):\n", len(page.Topics))
				for _, it := range page.Topics {
					fmt.Fprintf(deps.Out, "  %s  %s\n", presentation.SafeInline(it.ID.String()), presentation.SafeInline(it.Name))
				}
			}
			if page.HasMore {
				fmt.Fprintf(deps.Out, "Next cursor: %s\n", presentation.SafeInline(page.NextCursor))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&gids, "gids", "", "Community GIDs (required; positive integer; see forum games)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Server-side opaque cursor")
	cmd.Flags().IntVar(&limit, "limit", 20, "Number of items for this request")
	return cmd
}

func newSearchAllCmd(deps Deps) *cobra.Command {
	var gids string
	cmd := &cobra.Command{
		Use:   "all <keyword>",
		Short: "Mixed search (topics/users/wiki groups)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			gidsVal, oerr := requireGIDsFlag(cmd, gids)
			if oerr != nil {
				return oerr
			}
			svc := newSearchService(deps)
			res, oerr := svc.Comprehensive(cmd.Context(), search.ComprehensiveOptions{
				Keyword: args[0],
				GIDs:    gidsVal,
				// --preview 不再作为公开参数；适配器保持既有
				// 默认请求形式（preview=1），不顺带改变综合搜索结果。
				Preview: true,
			})
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				return output.Success(deps.Out, res.Data(args[0], gidsVal), "", nil)
			}
			if len(res.Topics) == 0 && len(res.Users) == 0 && len(res.Wikis) == 0 && len(res.Posts) == 0 {
				fmt.Fprintln(deps.Out, "No results")
				return nil
			}
			if len(res.Topics) > 0 {
				fmt.Fprintf(deps.Out, "Topics (%d):\n", len(res.Topics))
				for _, it := range res.Topics {
					fmt.Fprintf(deps.Out, "  %s  %s\n", presentation.SafeInline(it.ID.String()), presentation.SafeInline(it.Name))
				}
			}
			if len(res.Users) > 0 {
				fmt.Fprintf(deps.Out, "Users (%d):\n", len(res.Users))
				for _, it := range res.Users {
					fmt.Fprintf(deps.Out, "  %s  %s\n", presentation.SafeInline(it.UID.String()), presentation.SafeInline(it.Nickname))
				}
			}
			if len(res.Wikis) > 0 {
				fmt.Fprintf(deps.Out, "Wiki (%d):\n", len(res.Wikis))
				for _, it := range res.Wikis {
					fmt.Fprintf(deps.Out, "  %s  %s\n  %s\n",
						presentation.SafeInline(it.ID.String()), presentation.SafeInline(it.Title), presentation.SafeInline(it.BBSURL.String()))
				}
			}
			if len(res.Posts) > 0 {
				fmt.Fprintf(deps.Out, "Posts (%d):\n", len(res.Posts))
				printSearchPosts(deps, res.Posts)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&gids, "gids", "", "Community GIDs (required; positive integer; also selects the wiki group)")
	// --preview 已移出公开参数面；传 --preview 会得到
	// 未知 flag 的 INPUT_INVALID（root.SetFlagErrorFunc）。
	return cmd
}

func printSearchPosts(deps Deps, items []post.Summary) {
	if len(items) == 0 {
		fmt.Fprintln(deps.Out, "No results")
		return
	}
	for _, it := range items {
		fmt.Fprintf(deps.Out, "%s  vt=%d  %s  %s\n",
			presentation.SafeInline(it.PostID), it.ViewType, formatTime(it.CreatedAt),
			truncate(presentation.TitleOrPlaceholder(it.Subject), 40))
	}
}
