package qr

import (
	"bytes"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestRender_TerminalAndPNG(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, false)
	path, err := r.Render("https://user.mihoyo.com/qr_code_in_game.html?app_id=12&ticket=abc&expire=1893456000")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	defer os.Remove(path)
	// 终端输出非空且包含块字符（半块渲染）。
	if buf.Len() == 0 {
		t.Error("终端二维码为空")
	}
	if !strings.Contains(buf.String(), "█") && !strings.Contains(buf.String(), "▀") && !strings.Contains(buf.String(), "▄") {
		t.Error("终端输出不像二维码字符画")
	}
	// PNG 存在且为合法 PNG 头。
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 PNG: %v", err)
	}
	if len(data) < 8 || !bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G'}) {
		t.Error("文件不是 PNG")
	}
	// 临时 PNG 位于系统临时目录、用户私有权限，残留也不对其他本地用户开放。
	if r.PNGPath() != path {
		t.Errorf("PNGPath = %s, want %s", r.PNGPath(), path)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("PNG 权限应为 0600: %v", err)
		}
	}
}

func TestRender_QuietMode(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, true)
	path, err := r.Render("https://example.test/qr")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if buf.Len() != 0 {
		t.Errorf("Quiet 模式不应写终端: %q", buf.String())
	}
}

func TestRender_RebuildReplacesContent(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, true)
	p1, err := r.Render("https://example.test/ticket-1")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := r.Render("https://example.test/ticket-2")
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 {
		t.Errorf("同一会话应复用临时文件: %s vs %s", p1, p2)
	}
	defer r.Cleanup()
}

func TestCleanup(t *testing.T) {
	r := NewRenderer(&bytes.Buffer{}, true)
	path, err := r.Render("https://example.test/x")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("清理后文件仍存在")
	}
	// 幂等。
	if err := r.Cleanup(); err != nil {
		t.Fatalf("重复 Cleanup: %v", err)
	}
}
