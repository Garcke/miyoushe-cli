package content

import (
	"path/filepath"
	"strings"
	"testing"
)

const validVideoSpec = `{
  "schema_version": 1,
  "kind": "video",
  "gids": 8,
  "forum_id": 57,
  "forum_cate_id": 15,
  "subject": "标题",
  "blocks": [
    {"type": "text", "text": "第一段正文\n"},
    {"type": "video", "path": "assets/oceans.mp4", "cover": "./assets/cover.png"},
    {"type": "image", "path": "assets/a.png"}
  ],
  "topics": [{"id": "123", "name": "话题名"}],
  "is_original": true
}`

func TestParse_ValidVideoSpec(t *testing.T) {
	spec, oerr := Parse([]byte(validVideoSpec), "/base/dir")
	if oerr != nil {
		t.Fatalf("Parse: %v", oerr)
	}
	if spec.Kind != KindVideo || spec.GIDs != 8 || spec.ForumID != 57 || spec.ForumCateID != 15 {
		t.Errorf("spec 头部 = %+v", spec)
	}
	if spec.Subject != "标题" || !spec.IsOriginal || len(spec.Topics) != 1 || spec.Topics[0].ID != "123" {
		t.Errorf("spec 内容 = %+v", spec)
	}
	if len(spec.Blocks) != 3 {
		t.Fatalf("blocks = %d", len(spec.Blocks))
	}
	if spec.Blocks[0].Type != BlockText || spec.Blocks[0].Text != "第一段正文\n" {
		t.Errorf("blocks[0] = %+v", spec.Blocks[0])
	}
	vb := spec.Blocks[1]
	if vb.Type != BlockVideo || vb.Video == nil {
		t.Fatalf("blocks[1] = %+v", vb)
	}
	// 相对路径以 spec 目录解析；"./" 与普通相对路径都 Clean 到同一绝对路径。
	wantPath := filepath.Clean(filepath.Join("/base/dir", "assets/oceans.mp4"))
	if vb.Video.Path.Absolute != wantPath {
		t.Errorf("video path = %s, want %s", vb.Video.Path.Absolute, wantPath)
	}
	wantCover := filepath.Clean(filepath.Join("/base/dir", "assets/cover.png"))
	if vb.Video.Cover.Absolute != wantCover {
		t.Errorf("video cover = %s, want %s", vb.Video.Cover.Absolute, wantCover)
	}
	wantImage := filepath.Clean(filepath.Join("/base/dir", "assets/a.png"))
	if spec.Blocks[2].Image.Absolute != wantImage {
		t.Errorf("image path = %s, want %s", spec.Blocks[2].Image.Absolute, wantImage)
	}
	if _, ok := spec.VideoBlock(); !ok {
		t.Error("VideoBlock 应返回视频块")
	}
}

func TestParse_ErrorCases(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		wantMsg string
	}{
		{"video 缺封面", `{"schema_version":1,"kind":"video","gids":8,"forum_id":57,"subject":"t",
			"blocks":[{"type":"video","path":"a.mp4"}]}`, "缺少 cover"},
		{"video 两个视频块", `{"schema_version":1,"kind":"video","gids":8,"forum_id":57,"subject":"t",
			"blocks":[{"type":"video","path":"a.mp4","cover":"c.png"},{"type":"video","path":"b.mp4","cover":"c.png"}]}`, "恰好一个"},
		{"video 零视频块", `{"schema_version":1,"kind":"video","gids":8,"forum_id":57,"subject":"t",
			"blocks":[{"type":"text","text":"hi"}]}`, "恰好一个"},
		{"image kind 带 video 块", `{"schema_version":1,"kind":"image","gids":8,"forum_id":57,
			"blocks":[{"type":"video","path":"a.mp4","cover":"c.png"}]}`, "不允许 video 块"},
		{"article kind 带 video 块", `{"schema_version":1,"kind":"article","gids":8,"forum_id":57,"subject":"t",
			"blocks":[{"type":"video","path":"a.mp4","cover":"c.png"}]}`, "不允许 video 块"},
		{"image kind 顶层 cover", `{"schema_version":1,"kind":"image","gids":8,"forum_id":57,"cover":"c.png",
			"blocks":[{"type":"text","text":"hi"}]}`, "不允许设置顶层 cover"},
		{"video kind 顶层 cover", `{"schema_version":1,"kind":"video","gids":8,"forum_id":57,"subject":"t","cover":"c.png",
			"blocks":[{"type":"video","path":"a.mp4","cover":"c.png"}]}`, "不允许设置顶层 cover"},
		{"video 缺标题", `{"schema_version":1,"kind":"video","gids":8,"forum_id":57,
			"blocks":[{"type":"video","path":"a.mp4","cover":"c.png"}]}`, "非空 subject"},
		{"article 缺标题", `{"schema_version":1,"kind":"article","gids":8,"forum_id":57,
			"blocks":[{"type":"text","text":"hi"}]}`, "非空 subject"},
		{"article 无 text 块", `{"schema_version":1,"kind":"article","gids":8,"forum_id":57,"subject":"t",
			"blocks":[{"type":"image","path":"a.png"}]}`, "至少需要一个 text 块"},
		{"image 零块", `{"schema_version":1,"kind":"image","gids":8,"forum_id":57,"blocks":[]}`, "至少需要一个"},
		{"未知字段", `{"schema_version":1,"kind":"image","gids":8,"forum_id":57,"blocks":[{"type":"text","text":"hi"}],"hack":1}`, "unknown field"},
		{"块未知字段", `{"schema_version":1,"kind":"image","gids":8,"forum_id":57,
			"blocks":[{"type":"text","text":"hi","extra":1}]}`, "unknown field"},
		{"重复键", `{"schema_version":1,"kind":"image","gids":8,"forum_id":57,
			"blocks":[{"type":"text","text":"hi"}],"kind":"video"}`, "重复的 JSON 键"},
		{"块内重复键", `{"schema_version":1,"kind":"image","gids":8,"forum_id":57,
			"blocks":[{"type":"text","text":"a","text":"b"}]}`, "重复的 JSON 键"},
		{"空 text 块", `{"schema_version":1,"kind":"image","gids":8,"forum_id":57,
			"blocks":[{"type":"text","text":"  "}]}`, "不能为空"},
		{"未知块类型", `{"schema_version":1,"kind":"image","gids":8,"forum_id":57,
			"blocks":[{"type":"audio","path":"a.mp3"}]}`, "未知块类型"},
		{"远程 URL", `{"schema_version":1,"kind":"image","gids":8,"forum_id":57,
			"blocks":[{"type":"image","path":"https://example.com/a.png"}]}`, "本地文件"},
		{"schema_version 过新", `{"schema_version":2,"kind":"image","gids":8,"forum_id":57,
			"blocks":[{"type":"text","text":"hi"}]}`, "schema_version"},
		{"缺 gids", `{"schema_version":1,"kind":"image","forum_id":57,
			"blocks":[{"type":"text","text":"hi"}]}`, "gids"},
		{"缺 forum_id", `{"schema_version":1,"kind":"image","gids":8,
			"blocks":[{"type":"text","text":"hi"}]}`, "forum_id"},
		{"未知 kind", `{"schema_version":1,"kind":"poll","gids":8,"forum_id":57,
			"blocks":[{"type":"text","text":"hi"}]}`, "未知 kind"},
		{"缺 blocks", `{"schema_version":1,"kind":"image","gids":8,"forum_id":57}`, "blocks"},
		{"空 topics id", `{"schema_version":1,"kind":"image","gids":8,"forum_id":57,
			"blocks":[{"type":"text","text":"hi"}],"topics":[{"id":" "}]}`, "空 id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, oerr := Parse([]byte(c.spec), "/base")
			if oerr == nil {
				t.Fatalf("期望报错，实际成功")
			}
			if !strings.Contains(oerr.Message, c.wantMsg) {
				t.Errorf("错误消息 %q 不含 %q", oerr.Message, c.wantMsg)
			}
		})
	}
}

func TestParse_NonUTF8AndBOM(t *testing.T) {
	if _, oerr := Parse([]byte{'{', 0xff, 0xfe, '}'}, "/base"); oerr == nil || !strings.Contains(oerr.Message, "UTF-8") {
		t.Errorf("oerr = %v", oerr)
	}
	bom := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"schema_version":1,"kind":"image","gids":8,"forum_id":57,"blocks":[{"type":"text","text":"hi"}]}`)...)
	if _, oerr := Parse(bom, "/base"); oerr == nil || !strings.Contains(oerr.Message, "BOM") {
		t.Errorf("oerr = %v", oerr)
	}
}

func TestParse_AbsolutePathAndArticleCover(t *testing.T) {
	specJSON := `{
		"schema_version": 1,
		"kind": "article",
		"gids": 9,
		"forum_id": 47,
		"subject": "长文",
		"cover": "C:/imgs/cover.jpg",
		"blocks": [{"type": "text", "text": "正文"}]
	}`
	spec, oerr := Parse([]byte(specJSON), "/base")
	if oerr != nil {
		t.Fatalf("Parse: %v", oerr)
	}
	if spec.Cover == nil || spec.Cover.Absolute != `C:\imgs\cover.jpg` {
		t.Errorf("cover = %+v", spec.Cover)
	}
}
