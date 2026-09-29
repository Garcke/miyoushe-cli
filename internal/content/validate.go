// validate.go 实现 Parse 主流程：严格解码 → kind 规则校验 → 路径解析。
// 仅执行 ContentSpec 已定义的字段与 kind 规则，不增加隐式约束。
package content

import (
	"encoding/json"
	"strings"

	"mihoyo_cli/internal/output"
)

// specWire 是顶层镜像结构。指针类型区分"缺省"与"零值"，
// DisallowUnknownFields 拒绝设计之外的字段。
type specWire struct {
	SchemaVersion *int              `json:"schema_version"`
	Kind          *string           `json:"kind"`
	GIDs          *int              `json:"gids"`
	ForumID       *int64            `json:"forum_id"`
	ForumCateID   *int64            `json:"forum_cate_id"`
	Subject       *string           `json:"subject"`
	Blocks        []json.RawMessage `json:"blocks"`
	Topics        []topicWire       `json:"topics"`
	Cover         *string           `json:"cover"`
	IsOriginal    *bool             `json:"is_original"`
}

type topicWire struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

type typeWire struct {
	Type string `json:"type"`
}

type textBlockWire struct {
	Type string  `json:"type"`
	Text *string `json:"text"`
}

type imageBlockWire struct {
	Type string  `json:"type"`
	Path *string `json:"path"`
}

type videoBlockWire struct {
	Type  string  `json:"type"`
	Path  *string `json:"path"`
	Cover *string `json:"cover"`
}

// Parse 解析并校验 ContentSpec。baseDir 为 spec 文件所在目录，相对路径
// 以它为基准解析为绝对路径；文件存在性/大小/MIME 校验属于 dry-run 与
// 上传阶段，这里不做。
func Parse(data []byte, baseDir string) (Spec, *output.Error) {
	fail := func(format string, args ...any) (Spec, *output.Error) {
		return Spec{}, output.Err(output.CodeInputInvalid, format, args...)
	}

	var w specWire
	if oerr := decodeStrict(data, &w); oerr != nil {
		return fail("%s", oerr.Message)
	}

	if w.SchemaVersion == nil {
		return fail("Missing schema_version")
	}
	if *w.SchemaVersion != SchemaVersionV1 {
		return fail("Unsupported schema_version %d (only %d is supported)", *w.SchemaVersion, SchemaVersionV1)
	}
	if w.Kind == nil {
		return fail("Missing kind")
	}
	kind := Kind(*w.Kind)
	switch kind {
	case KindImage, KindArticle, KindVideo:
	default:
		return fail("Unknown kind %q (allowed: image/article/video)", *w.Kind)
	}
	if w.GIDs == nil || *w.GIDs <= 0 {
		return fail("Missing or invalid gids (must be a positive integer)")
	}
	if w.ForumID == nil || *w.ForumID <= 0 {
		return fail("Missing or invalid forum_id (must be a positive integer)")
	}
	forumCateID := int64(0)
	if w.ForumCateID != nil {
		forumCateID = *w.ForumCateID
	}

	subject := ""
	if w.Subject != nil {
		subject = *w.Subject
	}
	if kind != KindImage && strings.TrimSpace(subject) == "" {
		return fail("kind=%s requires a non-empty subject (title)", kind)
	}

	// blocks。
	if w.Blocks == nil {
		return fail("Missing blocks")
	}
	var blocks []Block
	videoCount := 0
	for i, raw := range w.Blocks {
		b, oerr := parseBlock(i, raw)
		if oerr != nil {
			return fail("%s", oerr.Message)
		}
		if b.Type == BlockVideo {
			videoCount++
		}
		if kind != KindVideo && b.Type == BlockVideo {
			return fail("kind=%s does not allow a video block (block %d)", kind, i+1)
		}
		blocks = append(blocks, b)
	}

	switch kind {
	case KindImage:
		if len(blocks) == 0 {
			return fail("kind=image requires at least one text or image block")
		}
	case KindArticle:
		hasText := false
		for _, b := range blocks {
			if b.Type == BlockText {
				hasText = true
				break
			}
		}
		if !hasText {
			return fail("kind=article requires at least one text block")
		}
	case KindVideo:
		if videoCount != 1 {
			return fail("kind=video requires exactly one video block, got %d", videoCount)
		}
	}

	// topics。
	var topics []Topic
	for _, t := range w.Topics {
		if strings.TrimSpace(t.ID) == "" {
			return fail("topics contains an empty id")
		}
		topic := Topic{ID: t.ID}
		if t.Name != nil {
			topic.Name = *t.Name
		}
		topics = append(topics, topic)
	}

	// 顶层 cover：image 与 video kind 明确拒绝，避免优先级歧义。
	var cover *SourceRef
	if w.Cover != nil {
		if kind == KindImage {
			return fail("kind=image does not allow a top-level cover")
		}
		if kind == KindVideo {
			return fail("kind=video does not allow a top-level cover (the cover belongs to the video block's cover field)")
		}
		ref, err := resolveSource(*w.Cover, baseDir)
		if err != nil {
			return fail("cover: %s", err.Error())
		}
		cover = &ref
	}

	// kind 专属路径解析：image 块与 video 块的引用在此统一解析。
	for i := range blocks {
		b := &blocks[i]
		switch b.Type {
		case BlockImage:
			ref, err := resolveSource(b.Image.Raw, baseDir)
			if err != nil {
				return fail("Block %d image.path: %s", i+1, err.Error())
			}
			b.Image = &ref
		case BlockVideo:
			pathRef, err := resolveSource(b.Video.Path.Raw, baseDir)
			if err != nil {
				return fail("Block %d video.path: %s", i+1, err.Error())
			}
			coverRef, err := resolveSource(b.Video.Cover.Raw, baseDir)
			if err != nil {
				return fail("Block %d video.cover: %s", i+1, err.Error())
			}
			b.Video = &VideoSource{Path: pathRef, Cover: coverRef}
		}
	}

	isOriginal := false
	if w.IsOriginal != nil {
		isOriginal = *w.IsOriginal
	}

	return Spec{
		SchemaVersion: *w.SchemaVersion,
		Kind:          kind,
		GIDs:          *w.GIDs,
		ForumID:       *w.ForumID,
		ForumCateID:   forumCateID,
		Subject:       subject,
		Blocks:        blocks,
		Topics:        topics,
		Cover:         cover,
		IsOriginal:    isOriginal,
	}, nil
}

// parseBlock 解析单个块：先宽容地读出 type（块字段集因类型而异），
// 再按类型严格解码固定字段集（此时未知字段才会被拒绝）。
func parseBlock(index int, raw json.RawMessage) (Block, *output.Error) {
	fail := func(format string, args ...any) (Block, *output.Error) {
		return Block{}, output.Err(output.CodeInputInvalid, format, args...)
	}
	var tw typeWire
	if err := json.Unmarshal(raw, &tw); err != nil {
		return fail("Block %d: %s", index+1, friendlyJSONError(err))
	}
	switch BlockType(tw.Type) {
	case BlockText:
		var w textBlockWire
		if oerr := decodeStrict(raw, &w); oerr != nil {
			return fail("Block %d: %s", index+1, oerr.Message)
		}
		if w.Text == nil || strings.TrimSpace(*w.Text) == "" {
			return fail("Block %d: text block cannot be empty", index+1)
		}
		return Block{Type: BlockText, Text: *w.Text}, nil
	case BlockImage:
		var w imageBlockWire
		if oerr := decodeStrict(raw, &w); oerr != nil {
			return fail("Block %d: %s", index+1, oerr.Message)
		}
		if w.Path == nil {
			return fail("Image block is missing path")
		}
		return Block{Type: BlockImage, Image: &SourceRef{Raw: *w.Path}}, nil
	case BlockVideo:
		var w videoBlockWire
		if oerr := decodeStrict(raw, &w); oerr != nil {
			return fail("Block %d: %s", index+1, oerr.Message)
		}
		if w.Path == nil {
			return fail("Video block is missing path")
		}
		if w.Cover == nil {
			return fail("Video block is missing cover (video posts require an explicit cover)")
		}
		return Block{Type: BlockVideo, Video: &VideoSource{
			Path:  SourceRef{Raw: *w.Path},
			Cover: SourceRef{Raw: *w.Cover},
		}}, nil
	default:
		return fail("Block %d: unknown block type %q (allowed: text/image/video)", index+1, tw.Type)
	}
}
