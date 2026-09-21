package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/post"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/search"
)

// newSearchCmd 搜索命令组。三个接口均匿名可调（无需登录）；
// gids 默认 2（原神），用户指令：默认携带以区分游戏类型。
func newSearchCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search",
		Short: "搜索帖子、话题与综合结果（匿名可用）",
		Long: `搜索米游社内容。
帖子与综合搜索必须携带非空 gids（默认 2=原神，可用 --gids 指定其他社区，
如 1=崩坏3 6=星穹铁道 8=绝区零 9=因缘精灵 10=星布谷地）；不提供全站搜索模式，
显式 --gids "" 会报输入错误。未知 gids 由服务端返回空结果。
话题搜索是跨社区搜索，不提供 --gids（该接口的 gids 无过滤效果）。`,
	}
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

// requireGIDsFlag 解析 --gids（V3 §3.1）：未显式指定时用默认 2；显式空值/
// 空白/非正整数一律 INPUT_INVALID，不静默回落到默认值。
func requireGIDsFlag(cmd *cobra.Command, gids string) (string, *output.Error) {
	v := strings.TrimSpace(gids)
	if v == "" {
		if cmd.Flags().Changed("gids") {
			return "", output.Err(output.CodeInputInvalid,
				"--gids 不能为空：帖子/综合搜索必须携带非空 gids（默认 2=原神），不提供全站搜索模式")
		}
		return search.DefaultGIDs, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return "", output.Err(output.CodeInputInvalid, "--gids 必须是正整数（收到 %q）", gids)
	}
	return v, nil
}

// requireSearchLimit 校验 --limit：未指定用 20；显式 0/1/2/负数报错，
// 不静默改成 3 后多输出结果（V3 §3.1）。
func requireSearchLimit(cmd *cobra.Command, limit int) (int, *output.Error) {
	if !cmd.Flags().Changed("limit") {
		return 20, nil
	}
	if limit < 3 {
		return 0, output.Err(output.CodeInputInvalid,
			"--limit 至少为 3（实测 <3 时服务端返回空列表；输入 %d 不静默修改）", limit)
	}
	return limit, nil
}

func newSearchPostsCmd(deps Deps) *cobra.Command {
	var gids, cursor, orderType string
	var limit int
	cmd := &cobra.Command{
		Use:   "posts <关键词>",
		Short: "搜索帖子",
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
			svc := newSearchService(deps)
			page, oerr := svc.Posts(cmd.Context(), search.PostsOptions{
				Keyword:   args[0],
				GIDs:      gidsVal,
				LastID:    cursor,
				Size:      limitVal,
				OrderType: orderType,
			})
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				return output.Success(deps.Out,
					output.ListData{Items: page.Items, HasMore: page.HasMore},
					page.NextCursor, nil)
			}
			printSearchPosts(deps, page.Items)
			if page.HasMore {
				fmt.Fprintf(deps.Out, "下一页 cursor: %s\n", page.NextCursor)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&gids, "gids", search.DefaultGIDs, "社区 gids（默认 2=原神；必须为非空正整数，不支持全站搜索）")
	cmd.Flags().StringVar(&cursor, "cursor", "", "服务端不透明游标")
	cmd.Flags().StringVar(&orderType, "order", "", "排序方式（服务端语义）")
	cmd.Flags().IntVar(&limit, "limit", 20, "本次请求条数（>=3）")
	return cmd
}

func newSearchTopicsCmd(deps Deps) *cobra.Command {
	var cursor string
	var limit int
	cmd := &cobra.Command{
		Use:   "topics <关键词>",
		Short: "搜索话题（跨社区，不支持按游戏过滤）",
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
				return output.Success(deps.Out,
					output.ListData{Items: page.Items, HasMore: page.HasMore},
					page.NextCursor, nil)
			}
			if len(page.Items) == 0 {
				fmt.Fprintln(deps.Out, "没有话题")
				return nil
			}
			for _, it := range page.Items {
				fmt.Fprintf(deps.Out, "%s  %s\n", it.ID.String(), it.Name)
			}
			if page.HasMore {
				fmt.Fprintf(deps.Out, "下一页 cursor: %s\n", page.NextCursor)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cursor, "cursor", "", "服务端不透明游标")
	cmd.Flags().IntVar(&limit, "limit", 20, "本次请求条数（>=3）")
	return cmd
}

func newSearchAllCmd(deps Deps) *cobra.Command {
	var gids string
	var preview bool
	cmd := &cobra.Command{
		Use:   "all <关键词>",
		Short: "综合搜索（话题/用户/百科分组）",
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
				Preview: preview,
			})
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				return output.Success(deps.Out, map[string]any{
					"topics": res.Topics,
					"users":  res.Users,
					"wikis":  res.Wikis,
					"posts":  res.Posts,
				}, "", nil)
			}
			if len(res.Topics) == 0 && len(res.Users) == 0 && len(res.Wikis) == 0 && len(res.Posts) == 0 {
				fmt.Fprintln(deps.Out, "没有结果")
				return nil
			}
			if len(res.Topics) > 0 {
				fmt.Fprintf(deps.Out, "话题（%d）:\n", len(res.Topics))
				for _, it := range res.Topics {
					fmt.Fprintf(deps.Out, "  %s  %s\n", it.ID.String(), it.Name)
				}
			}
			if len(res.Users) > 0 {
				fmt.Fprintf(deps.Out, "用户（%d）:\n", len(res.Users))
				for _, it := range res.Users {
					fmt.Fprintf(deps.Out, "  %s  %s\n", it.UID.String(), it.Nickname)
				}
			}
			if len(res.Wikis) > 0 {
				fmt.Fprintf(deps.Out, "百科（%d）:\n", len(res.Wikis))
				for _, it := range res.Wikis {
					fmt.Fprintf(deps.Out, "  %s  %s\n  %s\n", it.ID.String(), it.Title, it.BBSURL.String())
				}
			}
			if len(res.Posts) > 0 {
				fmt.Fprintf(deps.Out, "帖子（%d）:\n", len(res.Posts))
				printSearchEntries(deps, res.Posts)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&gids, "gids", search.DefaultGIDs, "社区 gids（影响 wiki 分组；默认 2=原神，必须为非空正整数）")
	cmd.Flags().BoolVar(&preview, "preview", true, "预览模式（服务端语义，实测不影响分组）")
	return cmd
}

func printSearchPosts(deps Deps, items []post.Summary) {
	if len(items) == 0 {
		fmt.Fprintln(deps.Out, "没有结果")
		return
	}
	for _, it := range items {
		fmt.Fprintf(deps.Out, "%s  vt=%d  %s  %s\n",
			it.PostID, it.ViewType, formatTime(it.CreatedAt), truncate(it.Subject, 40))
	}
}

func printSearchEntries(deps Deps, entries []post.Entry) {
	for i := range entries {
		sum, oerr := entries[i].Summary()
		if oerr != nil {
			continue
		}
		fmt.Fprintf(deps.Out, "  %s  %s\n", sum.PostID, truncate(sum.Subject, 40))
	}
}
