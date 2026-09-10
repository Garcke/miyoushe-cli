// Package cli 组装 Cobra 命令树并注入依赖。
//
// 依赖注入约定：ClientFor 把协议 host 映射为 api.Client，测试用
// httptest 地址替换；不提供 --endpoint 之类的运行期覆盖。
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/auth"
	"mihoyo_cli/internal/output"
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
		panic("cli: 无效的固定 host: " + host)
	}
	return c
}

// NewRoot 组装命令树。当前只注册已达到证据门禁的只读命令与认证命令；
// 写命令（post create/edit/delete、draft save/publish/delete、
// favorite add/remove、operation）在缺脱敏 fixture 时保持不注册，
// 避免运行到一半才发现“尚未实现”。
func NewRoot(deps Deps) *cobra.Command {
	root := &cobra.Command{
		Use:   "mys",
		Short: "米游社社区命令行工具",
		Long: "mys 是米游社社区 CLI。扫码登录、角色与内容查看已可用；" +
			"内容发布类命令按协议证据门禁（fixture + 契约测试）分阶段开放。",
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}
	root.PersistentFlags().Bool("json", false, "输出稳定 JSON envelope（失败写 stderr）")
	root.AddCommand(
		newAuthCmd(deps),
		newRoleCmd(deps),
		newPostCmd(deps),
		newDraftCmd(deps),
		newFavoriteCmd(deps),
	)
	return root
}

// Execute 是进程入口：构建依赖、执行命令、映射退出码。
// 失败输出统一写 stderr：--json 时为错误 envelope，否则为一行提示。
func Execute() int {
	deps, err := DefaultDeps()
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化失败:", err)
		return output.ExitInternal
	}
	root := NewRoot(deps)
	if err := root.Execute(); err != nil {
		var oe *output.Error
		if !errors.As(err, &oe) {
			oe = output.Err(output.CodeInternal, "%v", err)
		}
		emitFailure(deps, root, oe)
		return oe.Exit
	}
	return output.ExitOK
}

func emitFailure(deps Deps, cmd *cobra.Command, oe *output.Error) {
	if jsonMode(cmd) {
		_ = output.Failure(deps.ErrOut, oe)
		return
	}
	fmt.Fprintln(deps.ErrOut, "错误:", oe.Message)
	for _, w := range oe.Warnings {
		fmt.Fprintln(deps.ErrOut, "警告:", w)
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
		fmt.Fprintln(deps.ErrOut, "警告:", w)
	}
}
