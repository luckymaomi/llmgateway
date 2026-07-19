package canonical

// ContentPartType identifies a canonical message content block.
type ContentPartType string

const (
	ContentPartText     ContentPartType = "text"
	ContentPartImageURL ContentPartType = "image_url"
)

type ImageURL struct {
	URL    string
	Detail string
}

type ContentPart struct {
	Type     ContentPartType
	Text     string
	ImageURL *ImageURL
}

func TextContent(text string) []ContentPart {
	return []ContentPart{{Type: ContentPartText, Text: text}}
}
