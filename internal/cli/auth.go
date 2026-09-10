package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/auth"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/role"
	"mihoyo_cli/internal/verify"
)

func newAuthCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "登录与凭据管理",
	}
	cmd.AddCommand(
		newAuthLoginCmd(deps),
		newAuthStatusCmd(deps),
		newAuthVerifyCmd(deps),
		newAuthLogoutCmd(deps),
	)
	return cmd
}

var progressText = map[string]string{
	"device_ready":    "设备上下文已就绪",
	"qr_ready":        "二维码已生成，请使用米游社 App 扫码",
	"waiting_scan":    "等待扫码…",
	"waiting_confirm": "已扫码，请在手机上确认…",
	"qr_expired":      "二维码已过期，正在重新生成…",
	"confirmed":       "手机已确认，正在提取凭据…",
	"exchanging":      "正在交换 SToken…",
	"saving":          "正在安全保存凭据…",
}

func newAuthLoginCmd(deps Deps) *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "login",
		Short: "扫码登录并保存 SToken 凭据",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout <= 0 {
				return output.Err(output.CodeInputInvalid, "--timeout 必须为正时长")
			}
			quiet := jsonMode(cmd)
			render := deps.Render(quiet)
			defer render.Cleanup() // 正常退出、错误、取消都清理临时二维码文件

			svc := &auth.Service{
				Store:    deps.Store,
				FPClient: deps.ClientFor(protocol.HostPublicData),
				QRClient: deps.ClientFor(protocol.HostHK4E),
				ExClient: deps.ClientFor(protocol.HostTakumi),
				Now:      deps.Now,
			}
			cfg := auth.DefaultConfig()
			cfg.Timeout = timeout
			progress := func(stage string) {
				if quiet {
					return
				}
				if text, ok := progressText[stage]; ok {
					fmt.Fprintln(deps.ErrOut, text)
				}
			}

			creds, oerr := svc.Login(cmd.Context(), cfg, render, progress)
			if oerr != nil {
				return oerr
			}

			pngPath := render.PNGPath()
			warnings := []string{
				"登录成功仅表示服务端已签发 SToken 并保存，尚未验证社区接口权限",
			}
			data := map[string]any{
				"uid_masked":       output.MaskID(creds.UID),
				"token_kind":       creds.TokenKind,
				"credentials_path": deps.Store.Path(),
				"png_path":         pngPath,
			}
			if quiet {
				return output.Success(deps.Out, data, "", warnings)
			}
			printWarnings(deps, cmd, warnings)
			fmt.Fprintln(deps.Out, "登录成功")
			fmt.Fprintf(deps.Out, "账号: %s\n", data["uid_masked"])
			fmt.Fprintf(deps.Out, "Token 类型: %s\n", creds.TokenKind)
			fmt.Fprintf(deps.Out, "保存位置: %s\n", deps.Store.Path())
			return nil
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 300*time.Second, "登录总等待上限（如 5m）")
	return cmd
}

func newAuthStatusCmd(deps Deps) *cobra.Command {
	// status 完全离线：不构建任何 API 客户端，不发起网络请求。
	return &cobra.Command{
		Use:   "status",
		Short: "查看本地登录状态（离线，不联网验证）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			creds, err := deps.Store.Load()
			if err != nil {
				return err
			}
			var warnings []string
			if creds != nil {
				if err := deps.Store.CheckPermissions(); err != nil {
					warnings = append(warnings, err.Message)
				}
			}

			if creds == nil {
				data := map[string]any{
					"logged_in":        false,
					"credentials_path": deps.Store.Path(),
				}
				if jsonMode(cmd) {
					return output.Success(deps.Out, data, "", warnings)
				}
				printWarnings(deps, cmd, warnings)
				fmt.Fprintf(deps.Out, "当前未登录（凭据文件不存在: %s）\n", deps.Store.Path())
				return nil
			}

			data := map[string]any{
				"logged_in":        true,
				"uid_masked":       output.MaskID(creds.UID),
				"token_kind":       creds.TokenKind,
				"saved_at":         creds.SavedAt,
				"expires_at":       creds.ExpiresAt,
				"credentials_path": deps.Store.Path(),
				"verified":         false,
			}
			if jsonMode(cmd) {
				return output.Success(deps.Out, data, "", append(warnings, "离线状态，未在线验证"))
			}
			printWarnings(deps, cmd, warnings)
			fmt.Fprintln(deps.Out, "已登录（未在线验证）")
			fmt.Fprintf(deps.Out, "UID: %s\n", data["uid_masked"])
			fmt.Fprintf(deps.Out, "Token 类型: %s\n", creds.TokenKind)
			fmt.Fprintf(deps.Out, "保存时间: %s\n", creds.SavedAt)
			fmt.Fprintln(deps.Out, "到期时间: 未知，以服务端鉴权为准")
			fmt.Fprintf(deps.Out, "保存路径: %s\n", deps.Store.Path())
			return nil
		},
	}
}

func newAuthVerifyCmd(deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "在线验证社区能力（只读，不打印凭据）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, warnings, oerr := loadSessionWithWarning(deps)
			if oerr != nil {
				return oerr
			}
			checker := role.New(deps.ClientFor(protocol.HostTakumiMiyoushe))
			report, oerr := verify.Check(cmd.Context(), sess, checker)
			if oerr != nil {
				return oerr
			}
			if jsonMode(cmd) {
				return output.Success(deps.Out, report, "", warnings)
			}
			printWarnings(deps, cmd, warnings)
			fmt.Fprintf(deps.Out, "凭据文件: %s\n", boolText(report.CredentialsValid))
			fmt.Fprintf(deps.Out, "服务端会话: %s\n", acceptedText(report.ServerAccepted))
			fmt.Fprintf(deps.Out, "protocol profile: %s\n", report.ProtocolProfile)
			fmt.Fprintln(deps.Out, "能力:")
			for _, c := range report.Capabilities {
				fmt.Fprintf(deps.Out, "  %-13s %-16s %s\n", c.Name, c.State, c.Reason)
			}
			return nil
		},
	}
}

func newAuthLogoutCmd(deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "删除本地凭据（幂等；不代表服务端撤销）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			existed, err := deps.Store.Delete()
			if err != nil {
				return err
			}
			warnings := []string{
				"本地删除不会使服务端已签发的 SToken 立即失效；如需强制失效，请使用米哈游/米游社提供的账号安全能力",
			}
			data := map[string]any{
				"deleted":          existed,
				"credentials_path": deps.Store.Path(),
			}
			if jsonMode(cmd) {
				return output.Success(deps.Out, data, "", warnings)
			}
			printWarnings(deps, cmd, warnings)
			if existed {
				fmt.Fprintf(deps.Out, "已删除本地凭据（%s）\n", deps.Store.Path())
			} else {
				fmt.Fprintln(deps.Out, "当前未登录")
			}
			return nil
		},
	}
}

func boolText(b bool) string {
	if b {
		return "有效"
	}
	return "无效"
}

func acceptedText(b bool) string {
	if b {
		return "接受当前 SToken"
	}
	return "拒绝当前 SToken"
}
