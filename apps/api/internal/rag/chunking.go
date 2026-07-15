package rag

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

const (
	documentChunkSize    = 4000
	documentChunkOverlap = 500
)

var orderedMarkdownListPattern = regexp.MustCompile(`^\d+[.)]\s+`)

func BuildDocument(req domain.DocumentIngestRequest) (domain.Document, []domain.DocumentChunk, error) {
	title := strings.TrimSpace(req.Title)
	content := strings.TrimSpace(req.Content)
	if title == "" {
		return domain.Document{}, nil, errors.New("document title is required")
	}
	if content == "" {
		return domain.Document{}, nil, errors.New("document content is required")
	}
	sourceType := strings.TrimSpace(req.SourceType)
	if sourceType == "" {
		sourceType = "text"
	}
	metadata := req.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	now := time.Now().UTC()
	document := domain.Document{
		WorkspaceID: strings.TrimSpace(req.WorkspaceID),
		Title:       title,
		SourceType:  sourceType,
		SourceURI:   strings.TrimSpace(req.SourceURI),
		MimeType:    strings.TrimSpace(req.MimeType),
		Content:     content,
		Metadata:    metadata,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	format := documentFormatFromRequest(req)
	metadata["format"] = format
	if sourceType == "file" {
		if filename := strings.TrimSpace(req.SourceURI); filename != "" {
			metadata["filename"] = filename
		}
	}
	parts := buildDocumentChunkParts(content, format)
	chunks := make([]domain.DocumentChunk, 0, len(parts))
	for index, part := range parts {
		chunkMetadata := copyMetadata(metadata)
		chunkMetadata["title"] = title
		chunkMetadata["chunk_index"] = index
		if _, ok := chunkMetadata["chunk_type"]; !ok {
			chunkMetadata["chunk_type"] = "text"
		}
		chunks = append(chunks, domain.DocumentChunk{
			ChunkIndex: index,
			Content:    part.Content,
			TokenCount: estimateDocumentTokens(part.Content),
			Metadata:   chunkMetadata,
			CreatedAt:  now,
		})
		for key, value := range part.Metadata {
			chunks[len(chunks)-1].Metadata[key] = value
		}
	}
	return document, chunks, nil
}

type documentChunkPart struct {
	Content  string
	Metadata map[string]any
}

func buildDocumentChunkParts(content string, format string) []documentChunkPart {
	if format == "markdown" {
		return splitMarkdownContent(content)
	}
	parts := splitDocumentContent(content, documentChunkSize, documentChunkOverlap)
	chunks := make([]documentChunkPart, 0, len(parts))
	for _, part := range parts {
		chunks = append(chunks, documentChunkPart{
			Content:  part,
			Metadata: map[string]any{"chunk_type": "text"},
		})
	}
	return chunks
}

func documentFormatFromRequest(req domain.DocumentIngestRequest) string {
	sourceType := strings.ToLower(strings.TrimSpace(req.SourceType))
	mimeType := strings.ToLower(strings.TrimSpace(req.MimeType))
	if sourceType == "markdown" || strings.Contains(mimeType, "markdown") {
		return "markdown"
	}
	if value, ok := req.Metadata["format"].(string); ok && strings.EqualFold(strings.TrimSpace(value), "markdown") {
		return "markdown"
	}
	return "text"
}

func FormatFromFilename(filename string) (string, string, bool) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".txt":
		return "text", "text/plain", true
	case ".md", ".markdown":
		return "markdown", "text/markdown", true
	default:
		return "", "", false
	}
}

func splitMarkdownContent(content string) []documentChunkPart {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	headingPath := []string{}
	parts := []documentChunkPart{}
	buffer := []string{}
	bufferType := ""
	inCode := false
	codeFence := ""
	codeLanguage := ""

	flush := func() {
		text := strings.TrimSpace(strings.Join(buffer, "\n"))
		if text == "" {
			buffer = nil
			bufferType = ""
			return
		}
		metadata := map[string]any{
			"chunk_type":    bufferType,
			"source_format": "markdown",
		}
		if len(headingPath) > 0 {
			metadata["heading_path"] = strings.Join(headingPath, " > ")
			text = markdownHeadingContext(headingPath) + "\n\n" + text
		}
		if codeLanguage != "" && bufferType == "code" {
			metadata["code_language"] = codeLanguage
		}
		parts = appendChunkPart(parts, text, metadata)
		buffer = nil
		bufferType = ""
		codeLanguage = ""
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inCode {
			buffer = append(buffer, line)
			if strings.HasPrefix(trimmed, codeFence) {
				inCode = false
				flush()
			}
			continue
		}
		if fence, language, ok := markdownFenceStart(trimmed); ok {
			flush()
			inCode = true
			codeFence = fence
			codeLanguage = language
			bufferType = "code"
			buffer = append(buffer, line)
			continue
		}
		if level, title, ok := markdownHeading(trimmed); ok {
			flush()
			if level <= len(headingPath) {
				headingPath = headingPath[:level-1]
			}
			headingPath = append(headingPath, title)
			continue
		}
		if trimmed == "" {
			flush()
			continue
		}
		lineType := "paragraph"
		if isMarkdownListLine(trimmed) {
			lineType = "list"
		}
		if bufferType != "" && bufferType != lineType {
			flush()
		}
		bufferType = lineType
		buffer = append(buffer, line)
	}
	flush()
	if len(parts) == 0 {
		return buildDocumentChunkParts(content, "text")
	}
	return parts
}

func markdownHeadingContext(headingPath []string) string {
	lines := make([]string, 0, len(headingPath))
	for index, heading := range headingPath {
		heading = strings.TrimSpace(heading)
		if heading == "" {
			continue
		}
		level := index + 1
		if level > 6 {
			level = 6
		}
		lines = append(lines, strings.Repeat("#", level)+" "+heading)
	}
	return strings.Join(lines, "\n")
}

func appendChunkPart(parts []documentChunkPart, content string, metadata map[string]any) []documentChunkPart {
	if len([]rune(content)) <= documentChunkSize {
		return append(parts, documentChunkPart{Content: content, Metadata: metadata})
	}
	for _, part := range splitDocumentContent(content, documentChunkSize, documentChunkOverlap) {
		partMetadata := copyMetadata(metadata)
		partMetadata["chunk_type"] = metadata["chunk_type"]
		partMetadata["split_reason"] = "oversize_markdown_block"
		parts = append(parts, documentChunkPart{Content: part, Metadata: partMetadata})
	}
	return parts
}

func markdownFenceStart(trimmed string) (string, string, bool) {
	for _, fence := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, fence) {
			return fence, strings.TrimSpace(strings.TrimPrefix(trimmed, fence)), true
		}
	}
	return "", "", false
}

func markdownHeading(trimmed string) (int, string, bool) {
	if !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(trimmed) || trimmed[level] != ' ' {
		return 0, "", false
	}
	title := strings.TrimSpace(trimmed[level:])
	if title == "" {
		return 0, "", false
	}
	return level, title, true
}

func isMarkdownListLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "- ") ||
		strings.HasPrefix(trimmed, "* ") ||
		strings.HasPrefix(trimmed, "+ ") ||
		orderedMarkdownListPattern.MatchString(trimmed)
}

func splitDocumentContent(content string, size int, overlap int) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	if size <= 0 {
		size = documentChunkSize
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= size {
		overlap = size / 4
	}
	runes := []rune(content)
	if len(runes) <= size {
		return []string{content}
	}
	parts := []string{}
	for start := 0; start < len(runes); {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		parts = append(parts, strings.TrimSpace(string(runes[start:end])))
		if end == len(runes) {
			break
		}
		start = end - overlap
	}
	return parts
}

func copyMetadata(metadata map[string]any) map[string]any {
	copied := make(map[string]any, len(metadata))
	for key, value := range metadata {
		copied[key] = value
	}
	return copied
}

func estimateDocumentTokens(content string) int {
	runes := len([]rune(strings.TrimSpace(content)))
	if runes == 0 {
		return 0
	}
	return runes/4 + 1
}
