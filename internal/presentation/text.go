package presentation

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// SafeInline 返回可安全用于单行输出的远端文本：
//   - 换行、回车、制表等空白折叠为单个空格；
//   - C0/C1 控制字符与 ESC（ANSI 序列起始）转义为可见的 \xNN 形式；
//   - 非法 UTF-8 字节同样转义，避免终端误解析；
//   - 其余字符（中文、emoji、普通 Unicode）原样保留，不做翻译或改写。
func SafeInline(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteString(escapeByte(s[i]))
			i++
			lastSpace = false
			continue
		}
		i += size
		switch {
		case r == '\n' || r == '\r' || r == '\t' || r == '\v' || r == '\f':
			if !lastSpace && b.Len() > 0 {
				b.WriteByte(' ')
				lastSpace = true
			}
		case isEscapedControl(r):
			b.WriteString(escapeRune(r))
			lastSpace = false
		default:
			b.WriteRune(r)
			lastSpace = false
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// SafeBlock 保留结构化换行（\n），其余控制字符转义，
// 用于详情正文等允许多行的展示场景。
func SafeBlock(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteString(escapeByte(s[i]))
			i++
			continue
		}
		i += size
		switch {
		case r == '\n':
			b.WriteByte('\n')
		case r == '\r':
			// CRLF 统一为 LF：丢弃 CR。
		case r == '\t':
			b.WriteString("    ")
		case isEscapedControl(r):
			b.WriteString(escapeRune(r))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isEscapedControl 判断字符是否必须转义：C0（含 ESC）、DEL、C1。
func isEscapedControl(r rune) bool {
	return r < 0x20 || r == 0x7F || (r >= 0x80 && r <= 0x9F)
}

// escapeByte 把单个非法字节渲染为可见转义形式。
func escapeByte(b byte) string {
	const hex = "0123456789ABCDEF"
	return "\\x" + string(hex[(b>>4)&0xF]) + string(hex[b&0xF])
}

// escapeRune 把控制字符渲染为可见形式。
func escapeRune(r rune) string {
	const hex = "0123456789ABCDEF"
	if r <= 0xFF {
		return "\\x" + string(hex[(r>>4)&0xF]) + string(hex[r&0xF])
	}
	return "\\u" + string(hex[(r>>12)&0xF]) + string(hex[(r>>8)&0xF]) +
		string(hex[(r>>4)&0xF]) + string(hex[r&0xF])
}

// SafeMaybe 对可选文本应用 SafeInline；空串返回空串（由调用方决定占位）。
func SafeMaybe(s string) string {
	if s == "" {
		return ""
	}
	return SafeInline(s)
}

// TitleOrPlaceholder 返回标题展示值：空标题为 Not provided。
func TitleOrPlaceholder(title string) string {
	if strings.TrimSpace(title) == "" {
		return NotProvided
	}
	return SafeInline(title)
}

// BodyOrPlaceholder 返回正文展示值：空正文为 (no text content)。
func BodyOrPlaceholder(body string) string {
	if strings.TrimSpace(body) == "" {
		return NoTextContent
	}
	return SafeBlock(body)
}

// IsPrintable 报告文本是否不含需要转义的字符（测试与诊断用）。
func IsPrintable(s string) bool {
	for _, r := range s {
		if isEscapedControl(r) {
			return false
		}
		_ = unicode.IsPrint(r)
	}
	return true
}
