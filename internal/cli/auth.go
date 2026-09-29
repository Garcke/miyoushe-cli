package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"mihoyo_cli/internal/auth"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/presentation"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/role"
	"mihoyo_cli/internal/verify"
)

func newAuthCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Login and credential management",
	}
	cmd.RunE = groupRunE(cmd)
	cmd.AddCommand(
		newAuthLoginCmd(deps),
		newAuthStatusCmd(deps),
		newAuthVerifyCmd(deps),
		newAuthLogoutCmd(deps),
	)
	return cmd
}

var progressText = map[string]string{
	"device_ready":    "Preparing device context",
	"qr_ready":        "QR code ready; scan it with the Miyoushe app",
	"waiting_scan":    "Waiting for scan",
	"waiting_confirm": "Scanned; waiting for confirmation",
	"qr_expired":      "QR code expired; regenerating",
	"confirmed":       "Confirmed; exchanging credentials",
	"exchanging":      "Exchanging SToken",
	"saving":          "Saving credentials",
}

func newAuthLoginCmd(deps Deps) *cobra.Command {
	var timeout time.Duration
	var mode string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in by QR code and save SToken credentials",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout <= 0 {
				return output.Err(output.CodeInputInvalid, "--timeout must be a positive duration")
			}
			if mode != "passport" && mode != "hk4e" {
				return output.Err(output.CodeInputInvalid, "--mode supports only passport or hk4e")
			}
			quiet := jsonMode(cmd)
			render := deps.Render(quiet)
			defer render.Cleanup() // 正常退出、错误、取消都清理临时二维码文件

			svc := &auth.Service{
				Store:          deps.Store,
				FPClient:       deps.ClientFor(protocol.HostPublicData),
				QRClient:       deps.ClientFor(protocol.HostHK4E),
				ExClient:       deps.ClientFor(protocol.HostPassportAPI),
				PassportClient: deps.ClientFor(protocol.HostPassportAPI),
				Now:            deps.Now,
			}
			cfg := auth.DefaultConfig()
			cfg.Timeout = timeout
			progress := func(stage string) {
				if quiet && stage != "qr_ready" {
					return
				}
				if text, ok := progressText[stage]; ok {
					fmt.Fprintln(deps.ErrOut, text)
				}
				if quiet && stage == "qr_ready" {
					fmt.Fprintf(deps.ErrOut, "QR code PNG: %s (removed after login)\n", render.PNGPath())
				}
			}

			// passport：ma-cn-passport 扫码直出 SToken（默认，实测链路）；
			// hk4e：游戏码 + Game Token 交换。
			loginFunc := svc.LoginPassport
			if mode == "hk4e" {
				loginFunc = svc.Login
			}
			creds, oerr := loginFunc(cmd.Context(), cfg, render, progress)
			if oerr != nil {
				return oerr
			}

			warnings := []string{
				"Login success only means the server issued and saved an SToken; community API permission has not been verified",
			}
			data := map[string]any{
				"uid":              creds.UID,
				"token_kind":       creds.TokenKind,
				"credentials_path": deps.Store.Path(),
				"png_path":         "",
			}
			if quiet {
				return output.Success(deps.Out, data, "", warnings)
			}
			printWarnings(deps, cmd, warnings)
			fmt.Fprintln(deps.Out, "Login succeeded")
			fmt.Fprintf(deps.Out, "Account: %s\n", presentation.SafeInline(creds.UID))
			fmt.Fprintf(deps.Out, "Token type: %s\n", creds.TokenKind)
			fmt.Fprintf(deps.Out, "Path: %s\n", deps.Store.Path())
			return nil
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 300*time.Second, "Total login wait limit (e.g. 5m)")
	cmd.Flags().StringVar(&mode, "mode", "passport", "Login flow: passport (ma-cn-passport QR code direct) or hk4e (game code + exchange)")
	return cmd
}

func newAuthStatusCmd(deps Deps) *cobra.Command {
	// status 完全离线：不构建任何 API 客户端，不发起网络请求。
	return &cobra.Command{
		Use:   "status",
		Short: "Show local login status (offline, no network verification)",
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
				fmt.Fprintf(deps.Out, "Not logged in (credentials file not found: %s)\n", deps.Store.Path())
				return nil
			}

			data := map[string]any{
				"logged_in":        true,
				"uid":              creds.UID,
				"token_kind":       creds.TokenKind,
				"saved_at":         creds.SavedAt,
				"expires_at":       creds.ExpiresAt,
				"credentials_path": deps.Store.Path(),
				"verified":         false,
			}
			if jsonMode(cmd) {
				return output.Success(deps.Out, data, "", append(warnings, "Offline status; not verified online"))
			}
			printWarnings(deps, cmd, warnings)
			fmt.Fprintln(deps.Out, "Logged in (not verified online)")
			fmt.Fprintf(deps.Out, "UID: %s\n", presentation.SafeInline(creds.UID))
			fmt.Fprintf(deps.Out, "Token type: %s\n", creds.TokenKind)
			fmt.Fprintf(deps.Out, "Saved at: %s\n", creds.SavedAt)
			fmt.Fprintln(deps.Out, "Expires at: unknown; the server decides on authentication")
			fmt.Fprintf(deps.Out, "Path: %s\n", deps.Store.Path())
			return nil
		},
	}
}

func newAuthVerifyCmd(deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Verify community capabilities online (read-only; no credentials printed)",
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
			fmt.Fprintf(deps.Out, "Credentials file: %s\n", boolText(report.CredentialsValid))
			fmt.Fprintf(deps.Out, "Server session: %s\n", acceptedText(report.ServerAccepted))
			fmt.Fprintf(deps.Out, "protocol profile: %s\n", report.ProtocolProfile)
			fmt.Fprintln(deps.Out, "Capabilities:")
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
		Short: "Delete local credentials (idempotent; does not revoke on the server)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			existed, err := deps.Store.Delete()
			if err != nil {
				return err
			}
			warnings := []string{
				"Local deletion does not immediately invalidate the SToken already issued by the server; to force invalidation, use the account security features provided by miHoYo/Miyoushe",
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
				fmt.Fprintf(deps.Out, "Deleted local credentials (%s)\n", deps.Store.Path())
			} else {
				fmt.Fprintln(deps.Out, "Not logged in")
			}
			return nil
		},
	}
}

func boolText(b bool) string {
	if b {
		return "valid"
	}
	return "invalid"
}

func acceptedText(b bool) string {
	if b {
		return "Accepts the current SToken"
	}
	return "Rejects the current SToken"
}
