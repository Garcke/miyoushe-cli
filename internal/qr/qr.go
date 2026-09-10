// Package qr 把二维码 URL 编码为终端字符画与私有临时 PNG 文件。
// 不调用任何第三方在线二维码服务；不打印二维码原始 URL/ticket。
package qr

import (
	"fmt"
	"io"
	"os"

	qrcore "github.com/skip2/go-qrcode"

	"mihoyo_cli/internal/store"
)

// Renderer 渲染并持有本次登录的临时 PNG；每次 Render 用新二维码对象
// 重建矩阵（重建二维码绝不复用旧内容），PNG 路径保持稳定以便覆写。
type Renderer struct {
	Stdout io.Writer
	// Quiet 为 true 时不向终端输出字符画（--json 模式），仅写 PNG。
	Quiet bool

	path string
}

// NewRenderer 构造渲染器。
func NewRenderer(stdout io.Writer, quiet bool) *Renderer {
	return &Renderer{Stdout: stdout, Quiet: quiet}
}

// Render 向终端输出二维码并写入临时 PNG，返回 PNG 路径。
func (r *Renderer) Render(content string) (string, error) {
	code, err := qrcore.New(content, qrcore.Low)
	if err != nil {
		return "", fmt.Errorf("二维码编码失败: %w", err)
	}
	if !r.Quiet && r.Stdout != nil {
		// 半块字符画；false 表示不使用静区反色。
		fmt.Fprintln(r.Stdout, code.ToSmallString(false))
	}
	if r.path == "" {
		f, err := os.CreateTemp("", "mys-login-*.png")
		if err != nil {
			return "", fmt.Errorf("创建临时二维码文件失败: %w", err)
		}
		r.path = f.Name()
		f.Close()
		// 新文件从创建时就受保护（认证设计 §5）。
		if err := store.HardenFile(r.path); err != nil {
			os.Remove(r.path)
			r.path = ""
			return "", fmt.Errorf("临时二维码文件加固失败: %w", err)
		}
	}
	png, err := code.PNG(512)
	if err != nil {
		return "", fmt.Errorf("生成二维码 PNG 失败: %w", err)
	}
	if err := os.WriteFile(r.path, png, 0o600); err != nil {
		return "", fmt.Errorf("写入二维码 PNG 失败: %w", err)
	}
	return r.path, nil
}

// PNGPath 返回当前临时 PNG 路径（未渲染时为空）。
func (r *Renderer) PNGPath() string { return r.path }

// Cleanup 清理临时 PNG；幂等。异常断电残留的文件因创建时已受用户权限
// 保护，仍不会对其他本地用户开放。
func (r *Renderer) Cleanup() error {
	if r.path == "" {
		return nil
	}
	err := os.Remove(r.path)
	r.path = ""
	return err
}
