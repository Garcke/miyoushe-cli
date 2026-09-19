// Package content 实现 ContentSpec 的解析、校验与路径解析。
//
// 设计来源：docs/architecture/community-features.md §4/§5。解析是确定性的
// 纯本地转换：不发网络请求、不读文件内容；相对路径以 ContentSpec 文件所在
// 目录解析，文件本身的存在性/大小/MIME 校验属于上传与 dry-run 阶段。
//
// 解析纪律（§4）：拒绝未知字段、重复 JSON 键、非法 UTF-8、空正文块和
// kind/block 不匹配；block v1 字段集固定，额外字段一律报错。
package content

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Kind 是帖子类型。image 为短帖（view_type=2），article 为长文（5），
// video 为视频（1/5 形态由发布适配器按 fixture 决定）。
type Kind string

const (
	KindImage   Kind = "image"
	KindArticle Kind = "article"
	KindVideo   Kind = "video"
)

// BlockType 是正文块类型。
type BlockType string

const (
	BlockText  BlockType = "text"
	BlockImage BlockType = "image"
	BlockVideo BlockType = "video"
)

// SchemaVersionV1 是当前唯一的规范版本。
const SchemaVersionV1 = 1

// SourceRef 是一个本地文件引用：Raw 为 spec 原文（相对或绝对路径），
// Absolute 为按 spec 所在目录解析后的绝对路径（Clean 处理）。
type SourceRef struct {
	Raw      string
	Absolute string
}

// VideoSource 是视频块的本地引用：视频本体 + 必填封面。
type VideoSource struct {
	Path  SourceRef
	Cover SourceRef
}

// Block 是一个正文块。按 Type 只有一个载荷字段非 nil（Text 恒有）。
type Block struct {
	Type  BlockType
	Text  string       // text 块
	Image *SourceRef   // image 块
	Video *VideoSource // video 块
}

// Topic 是话题引用。ID 为米游社话题 id（字符串形式），Name 为展示名。
type Topic struct {
	ID   string
	Name string
}

// Spec 是校验通过的完整内容规范。Block 顺序即发布顺序。
type Spec struct {
	SchemaVersion int
	Kind          Kind
	GIDs          int
	ForumID       int64
	ForumCateID   int64
	Subject       string
	Blocks        []Block
	Topics        []Topic
	// Cover 是长文顶层封面（仅 article 可设；image/video kind 设置即报错）。
	Cover      *SourceRef
	IsOriginal bool
}

// VideoBlock 返回唯一的视频块（kind=video 时恰好一个；调用方先校验）。
func (s Spec) VideoBlock() (Block, bool) {
	for _, b := range s.Blocks {
		if b.Type == BlockVideo {
			return b, true
		}
	}
	return Block{}, false
}

// StringRef 把顶层 cover 字段转成 SourceRef。
func StringRef(raw, baseDir string) (SourceRef, error) {
	return resolveSource(raw, baseDir)
}

// resolveSource 解析单个文件引用：拒绝 URL 与空值，绝对路径直接 Clean，
// 相对路径以 baseDir（spec 文件所在目录）为基准。
func resolveSource(raw, baseDir string) (SourceRef, error) {
	if strings.TrimSpace(raw) == "" {
		return SourceRef{}, fmt.Errorf("文件引用为空")
	}
	if strings.Contains(raw, "://") {
		return SourceRef{}, fmt.Errorf("只接受本地文件引用，不接受远程 URL: %q", raw)
	}
	if filepath.IsAbs(raw) {
		return SourceRef{Raw: raw, Absolute: filepath.Clean(raw)}, nil
	}
	return SourceRef{Raw: raw, Absolute: filepath.Clean(filepath.Join(baseDir, raw))}, nil
}
