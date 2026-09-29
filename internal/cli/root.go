// Package cli 组装 Cobra 命令树并注入依赖。
//
// 依赖注入约定：ClientFor 把协议 host 映射为 api.Client，测试用
// httptest 地址替换；不提供 --endpoint 之类的运行期覆盖。
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/auth"
	"mihoyo_cli/internal/buildinfo"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/presentation"
	"mihoyo_cli/internal/qr"
	"mihoyo_cli/internal/session"
	"mihoyo_cli/internal/store"
)

// Deps 是命令层的全部依赖。
type Deps struct {
	Store     *store.Store
	Now       func() time.Time
	ClientFor func(host string) *api.Client
	Render    func(quiet bool) auth.Renderer
	Out       io.Writer
	ErrOut    io.Writer
}

// DefaultDeps 构造生产依赖。
func DefaultDeps() (Deps, error) {
	st, err := store.Default()
	if err != nil {
		return Deps{}, err
	}
	return Deps{
		Store:     st,
		Now:       time.Now,
		ClientFor: defaultClientFor,
		Render: func(quiet bool) auth.Renderer {
			return qr.NewRenderer(os.Stdout, quiet)
		},
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}, nil
}

func defaultClientFor(host string) *api.Client {
	c, err := api.New("https://" + host)
	if err != nil {
		// 固定 host 白名单都是合法 URL；到这里说明代码错误。
		panic("cli: invalid fixed host: " + host)
	}
	return c
}

// NewRoot 组装命令树。当前只注册已达到证据门禁的只读命令与认证命令；
// 写命令（post create/edit/delete、draft save/publish/delete、
// favorite add/remove、operation）在缺脱敏 fixture 时保持不注册，
// 避免运行到一半才发现“尚未实现”。
func NewRoot(deps Deps) *cobra.Command {
	root := &cobra.Command{
		Use:     output.Executable,
		Short:   "Miyoushe community CLI",
		Version: buildinfo.String(),
		Long: `mys-cli is a command-line client for the Miyoushe community.
It supports QR login and read-only role, post, draft, favorite, forum, and
search commands. Publishing and other write operations are not available.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}
	root.SetVersionTemplate("{{printf \"%s version %s\\n\" .Name .Version}}")
	root.RunE = groupRunE(root)
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return output.Err(output.CodeInputInvalid, "%v", err).
			WithAction(output.RunCommand(append(commandArgs(c), "--help")...))
	})
	root.PersistentFlags().Bool("json", false, "emit a stable JSON envelope on stdout")
	root.AddCommand(
		newAuthCmd(deps),
		newRoleCmd(deps),
		newPostCmd(deps),
		newDraftCmd(deps),
		newFavoriteCmd(deps),
		newSearchCmd(deps),
		newForumCmd(deps),
	)
	return root
}

// Execute 是进程入口：构建依赖、执行命令、映射退出码。
// 失败输出统一写 stderr：--json 时为错误 envelope，否则为一行提示。
func Execute() int {
	deps, err := DefaultDeps()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error [INTERNAL]:", err)
		return output.ExitInternal
	}
	root := NewRoot(deps)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root.SetContext(ctx)
	if err := root.Execute(); err != nil {
		var oe *output.Error
		if !errors.As(err, &oe) {
			// Cobra 的未知命令、未知 flag、参数数量错误统一映射为 INPUT_INVALID。
			oe = output.Err(output.CodeInputInvalid, "%v", err).
				WithAction(output.RunCommand("--help"))
		}
		emitFailure(deps, root, oe)
		return oe.Exit
	}
	return output.ExitOK
}

// emitFailure 按统一契约输出失败：JSON 模式的最终 envelope 写 stdout；
// 人类模式在 stderr 输出 "Error [CODE]: message." 与最多一条可执行下一步。
func emitFailure(deps Deps, cmd *cobra.Command, oe *output.Error) {
	if jsonMode(cmd) {
		_ = output.Failure(deps.Out, oe)
		return
	}
	fmt.Fprintf(deps.ErrOut, "Error [%s]: %s.\n", oe.Code, presentation.SafeInline(oe.Message))
	if a := oe.Action; a != nil && a.Type == "run_command" && a.Executable != "" {
		next := a.Executable
		for _, arg := range a.Args {
			next += " " + arg
		}
		fmt.Fprintf(deps.ErrOut, "Next: %s\n", next)
	}
	for _, w := range oe.Warnings {
		fmt.Fprintf(deps.ErrOut, "Warning: %s\n", presentation.SafeInline(w))
	}
}

// unknownCommandErr 构造未知子命令的 INPUT_INVALID 错误并列出可用子命令。
func unknownCommandErr(cmd *cobra.Command, arg string) *output.Error {
	names := make([]string, 0, len(cmd.Commands()))
	for _, c := range cmd.Commands() {
		if c.IsAvailableCommand() && c.Name() != "help" {
			names = append(names, c.Name())
		}
	}
	oe := output.Err(output.CodeInputInvalid,
		"Unknown command %q for %q. Available commands: %s",
		arg, cmd.CommandPath(), strings.Join(names, ", "))
	return oe.WithAction(output.RunCommand(append(commandArgs(cmd), "--help")...))
}

// groupRunE 是命令组的默认 RunE：无参数时显示帮助（父命令单独运行是正常用法），
// 未知子命令必须以 INPUT_INVALID 失败，不能退回 Help 并返回 0。
func groupRunE(cmd *cobra.Command) func(*cobra.Command, []string) error {
	return func(c *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return unknownCommandErr(cmd, args[0])
	}
}

// commandArgs 把命令路径转换为 action 参数数组（去掉可执行名）：
// "mys-cli forum list" -> ["forum", "list"]；根命令得到空数组。
func commandArgs(c *cobra.Command) []string {
	parts := strings.Split(c.CommandPath(), " ")
	if len(parts) <= 1 {
		return []string{}
	}
	return parts[1:]
}

// listPagination 统一列表分页契约：
// has_more=false 时顶层 next_cursor 必须为空串；resumable 仅在确有可续游标时为 true。
func listPagination(cursor string, hasMore bool, nextArgs []string) (string, output.Pagination) {
	if !hasMore {
		cursor = ""
	}
	if nextArgs == nil {
		nextArgs = []string{}
	}
	return cursor, output.Pagination{
		Mode:      "cursor",
		Resumable: hasMore && cursor != "",
		NextArgs:  nextArgs,
	}
}

// jsonMode 读取根命令上的持久 --json 标志。
func jsonMode(cmd *cobra.Command) bool {
	if f := cmd.Flag("json"); f != nil {
		return f.Value.String() == "true"
	}
	return false
}

// loadSessionWithWarning 加载会话并收集权限提示。
func loadSessionWithWarning(deps Deps) (session.Session, []string, *output.Error) {
	provider := session.NewProvider(deps.Store)
	sess, oerr := provider.Load()
	if oerr != nil {
		return sess, nil, oerr
	}
	var warnings []string
	if err := deps.Store.CheckPermissions(); err != nil {
		warnings = append(warnings, err.Message)
	}
	return sess, warnings, nil
}

// printWarnings 人类模式下把提示写 stderr。
func printWarnings(deps Deps, cmd *cobra.Command, warnings []string) {
	if jsonMode(cmd) {
		return
	}
	for _, w := range warnings {
		fmt.Fprintf(deps.ErrOut, "Warning: %s\n", presentation.SafeInline(w))
	}
}
