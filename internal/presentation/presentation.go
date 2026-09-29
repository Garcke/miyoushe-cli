// Package presentation 把 domain 结果转换为统一的、端点感知的展示模型
// 它是纯函数层：不发网络请求、不读凭据、
// 不改变业务状态。
//
// 职责：
//   - 远端文本安全渲染（控制字符转义、单行折叠），防止终端注入；
//   - 端点感知的内容类型与稳定英文标签（view_type 不是全局枚举）；
//   - 缺失值占位文案（Not provided / (no text content)）。
package presentation

// 缺失值占位：展示文案不写入 JSON。
const (
	// NotProvided 用于人类模式的缺失标题/名称/描述。
	NotProvided = "Not provided"
	// NoTextContent 用于人类模式的空正文。
	NoTextContent = "(no text content)"
)

// ContentType 是稳定的业务内容类型枚举，跨端点语义一致。
type ContentType string

const (
	ContentVideoPost      ContentType = "video_post"
	ContentImageTextPost  ContentType = "image_text_post"
	ContentMixedMediaPost ContentType = "mixed_media_post"
	ContentArticleOrVideo ContentType = "article_or_video"
	ContentUnknown        ContentType = "unknown"
)

// ContentTypeSource 是业务分类结论的证据来源。
type ContentTypeSource string

const (
	SourceResponse       ContentTypeSource = "response"
	SourceDetailResponse ContentTypeSource = "detail_response"
	SourceQueryBucket    ContentTypeSource = "query_bucket"
	SourceUnknown        ContentTypeSource = "unknown"
)

// ContentTypeInfo 是一次端点感知分类的结果。
type ContentTypeInfo struct {
	Type   ContentType
	Label  string
	Source ContentTypeSource
}

// Label 返回人类英文标签。
func Label(t ContentType) string {
	switch t {
	case ContentVideoPost:
		return "Video post"
	case ContentImageTextPost:
		return "Image/text post"
	case ContentMixedMediaPost:
		return "Mixed media post"
	case ContentArticleOrVideo:
		return "Article or video post"
	default:
		return "Unknown content type"
	}
}

// ClassifyPost 按帖子/搜索端点证据分类。
// hasVerifiedVOD 仅当“当前端点的响应模型已明确承载并验证 VOD 字段”时为 true；
// VOD 证据优先于数字 view_type。
func ClassifyPost(viewType int, hasVerifiedVOD bool) ContentTypeInfo {
	if hasVerifiedVOD {
		return ContentTypeInfo{Type: ContentVideoPost, Label: Label(ContentVideoPost), Source: SourceResponse}
	}
	return classifyByViewType(viewType, SourceResponse)
}

// ClassifyDraft 按草稿端点证据分类。草稿详情模型尚未稳定暴露 VOD 字段，
// 因此 view_type=5 只能归类为 article_or_video，不能断言视频。
func ClassifyDraft(viewType int, source ContentTypeSource) ContentTypeInfo {
	if source == "" {
		source = SourceResponse
	}
	return classifyByViewType(viewType, source)
}

func classifyByViewType(viewType int, source ContentTypeSource) ContentTypeInfo {
	var t ContentType
	switch viewType {
	case 1:
		t = ContentMixedMediaPost
	case 2:
		t = ContentImageTextPost
	case 5:
		t = ContentArticleOrVideo
	default:
		t = ContentUnknown
		source = SourceUnknown
	}
	return ContentTypeInfo{Type: t, Label: Label(t), Source: source}
}
