package presentation

import (
	"strings"
	"testing"
)

func TestSafeInline_EscapesControlsAndFoldsNewlines(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"plain text", "plain text"},
		{"line1\nline2", "line1 line2"},
		{"line1\r\nline2", "line1 line2"},
		{"tab\tsep", "tab sep"},
		{"bell\x07", "bell\\x07"},
		{"esc\x1b[31mred", "esc\\x1B[31mred"},
		{"del\x7f", "del\\x7F"},
		{"c1\x9b", "c1\\x9B"},
		{"中文标题", "中文标题"},
		{"emoji😀", "emoji😀"},
		{"  trailing  ", "  trailing"},
	}
	for _, tc := range cases {
		if got := SafeInline(tc.in); got != tc.want {
			t.Errorf("SafeInline(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// 防止终端注入：输出中不得残留 ESC 字节。
	if out := SafeInline("x\x1b[2Jy"); strings.ContainsRune(out, 0x1b) {
		t.Errorf("ESC 未转义: %q", out)
	}
}

func TestSafeBlock_KeepsNewlinesEscapesControls(t *testing.T) {
	in := "para1\r\npara2\x1b[0m\n\tindent"
	got := SafeBlock(in)
	if !strings.Contains(got, "para1\npara2") {
		t.Errorf("CRLF 应统一为 LF: %q", got)
	}
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("ESC 未转义: %q", got)
	}
	if !strings.Contains(got, "    indent") {
		t.Errorf("TAB 应展开为空格: %q", got)
	}
}

func TestClassifyPost(t *testing.T) {
	cases := []struct {
		viewType int
		vod      bool
		want     ContentType
		label    string
	}{
		{1, false, ContentMixedMediaPost, "Mixed media post"},
		{2, false, ContentImageTextPost, "Image/text post"},
		{5, false, ContentArticleOrVideo, "Article or video post"},
		{0, false, ContentUnknown, "Unknown content type"},
		{99, false, ContentUnknown, "Unknown content type"},
		{5, true, ContentVideoPost, "Video post"}, // VOD 证据优先于数字
	}
	for _, tc := range cases {
		info := ClassifyPost(tc.viewType, tc.vod)
		if info.Type != tc.want || info.Label != tc.label {
			t.Errorf("ClassifyPost(%d, vod=%v) = %s/%s, want %s/%s",
				tc.viewType, tc.vod, info.Type, info.Label, tc.want, tc.label)
		}
	}
}

func TestClassifyDraft_NeverAssertsVideoByViewType(t *testing.T) {
	// 草稿端点未稳定暴露 VOD 字段：view_type=5 只能是 article_or_video。
	info := ClassifyDraft(5, SourceResponse)
	if info.Type != ContentArticleOrVideo {
		t.Errorf("草稿 view_type=5 不得断言视频: %+v", info)
	}
	bucket := ClassifyDraft(2, SourceQueryBucket)
	if bucket.Type != ContentImageTextPost || bucket.Source != SourceQueryBucket {
		t.Errorf("桶来源应保留: %+v", bucket)
	}
	unknown := ClassifyDraft(0, SourceQueryBucket)
	if unknown.Type != ContentUnknown || unknown.Source != SourceUnknown {
		t.Errorf("未知类型来源应为 unknown: %+v", unknown)
	}
}

func TestPlaceholders(t *testing.T) {
	if TitleOrPlaceholder("") != NotProvided || TitleOrPlaceholder("  ") != NotProvided {
		t.Error("空标题应显示 Not provided")
	}
	if TitleOrPlaceholder("标题") != "标题" {
		t.Error("标题应保留 Unicode 原文")
	}
	if BodyOrPlaceholder("") != NoTextContent {
		t.Error("空正文应显示 (no text content)")
	}
	if got := BodyOrPlaceholder("第一行\n第二行"); got != "第一行\n第二行" {
		t.Errorf("正文应保留结构化换行: %q", got)
	}
}
