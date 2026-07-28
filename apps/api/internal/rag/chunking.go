package rag

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
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
	content := normalizeDocumentContent(req.Content)
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
	metadata := copyMetadata(req.Metadata)
	contentHash := sourceHash(content)
	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = "sha256:" + contentHash
	}
	now := time.Now().UTC()
	document := domain.Document{
		WorkspaceID: strings.TrimSpace(req.WorkspaceID),
		Title:       title,
		Version:     version,
		ContentHash: contentHash,
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
	parts := buildDocumentChunkParts(content, format, version)
	chunks := make([]domain.DocumentChunk, 0, len(parts))
	for index, part := range parts {
		chunkMetadata := copyMetadata(metadata)
		chunkMetadata["title"] = title
		chunkMetadata["chunk_index"] = index
		if _, ok := chunkMetadata["chunk_type"]; !ok {
			chunkMetadata["chunk_type"] = "text"
		}
		chunks = append(chunks, domain.DocumentChunk{
			ChunkSource: domain.ChunkSource{
				ParentID: part.ParentID, SectionPath: append([]string{}, part.SectionPath...),
				StartOffset: part.StartOffset, EndOffset: part.EndOffset,
				DocumentVersion: version, ContentHash: sourceHash(part.Content),
			},
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
	Content     string
	ParentID    string
	SectionPath []string
	StartOffset int
	EndOffset   int
	Metadata    map[string]any
}

func buildDocumentChunkParts(content string, format string, documentVersion string) []documentChunkPart {
	if format == "markdown" {
		return splitMarkdownContent(content, documentVersion)
	}
	ranges := splitDocumentContentRanges(content, documentChunkSize, documentChunkOverlap)
	chunks := make([]documentChunkPart, 0, len(ranges))
	parentID := sourceParentID(documentVersion, nil, 0)
	for _, part := range ranges {
		chunks = append(chunks, documentChunkPart{
			Content: part.Content, ParentID: parentID,
			StartOffset: part.StartOffset, EndOffset: part.EndOffset,
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

func splitMarkdownContent(content string, documentVersion string) []documentChunkPart {
	lines := sourceLines(content)
	headingPath := []string{}
	parts := []documentChunkPart{}
	buffer := []sourceLine{}
	bufferType := ""
	inCode := false
	codeFence := ""
	codeLanguage := ""
	sectionStart := 0

	flush := func() {
		source, startOffset, _ := sourceRange(content, buffer)
		if source == "" {
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
		}
		if codeLanguage != "" && bufferType == "code" {
			metadata["code_language"] = codeLanguage
		}
		parts = appendMarkdownChunkParts(parts, source, startOffset, metadata, headingPath,
			sourceParentID(documentVersion, headingPath, sectionStart))
		buffer = nil
		bufferType = ""
		codeLanguage = ""
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line.Text)
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
			sectionStart = line.StartOffset
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
		return buildDocumentChunkParts(content, "text", documentVersion)
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

func appendMarkdownChunkParts(parts []documentChunkPart, source string, startOffset int, metadata map[string]any, sectionPath []string, parentID string) []documentChunkPart {
	headingContext := markdownHeadingContext(sectionPath)
	contentSize := documentChunkSize
	if headingContext != "" {
		contentSize -= len([]rune(headingContext)) + 2
		if contentSize < documentChunkSize/2 {
			contentSize = documentChunkSize / 2
		}
	}
	ranges := splitDocumentContentRanges(source, contentSize, documentChunkOverlap)
	for _, sourcePart := range ranges {
		partMetadata := copyMetadata(metadata)
		if len(ranges) > 1 {
			partMetadata["split_reason"] = "oversize_markdown_block"
		}
		chunkContent := sourcePart.Content
		if headingContext != "" {
			chunkContent = headingContext + "\n\n" + chunkContent
		}
		parts = append(parts, documentChunkPart{
			Content: chunkContent, ParentID: parentID,
			SectionPath: append([]string(nil), sectionPath...),
			StartOffset: startOffset + sourcePart.StartOffset,
			EndOffset:   startOffset + sourcePart.EndOffset,
			Metadata:    partMetadata,
		})
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

type sourceRangePart struct {
	Content     string
	StartOffset int
	EndOffset   int
}

func splitDocumentContentRanges(content string, size int, overlap int) []sourceRangePart {
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
	byteOffsets := make([]int, len(runes)+1)
	byteOffset := 0
	for index, value := range runes {
		byteOffsets[index] = byteOffset
		byteOffset += len(string(value))
	}
	byteOffsets[len(runes)] = len(content)
	parts := []sourceRangePart{}
	for start := 0; start < len(runes); {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		rawStart := byteOffsets[start]
		rawEnd := byteOffsets[end]
		raw := content[rawStart:rawEnd]
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" {
			leadingBytes := strings.Index(raw, trimmed)
			partStart := rawStart + leadingBytes
			parts = append(parts, sourceRangePart{Content: trimmed, StartOffset: partStart, EndOffset: partStart + len(trimmed)})
		}
		if end == len(runes) {
			break
		}
		start = end - overlap
	}
	return parts
}

type sourceLine struct {
	Text        string
	StartOffset int
	EndOffset   int
}

func sourceLines(content string) []sourceLine {
	lines := make([]sourceLine, 0, strings.Count(content, "\n")+1)
	start := 0
	for start <= len(content) {
		end := strings.IndexByte(content[start:], '\n')
		if end < 0 {
			lines = append(lines, sourceLine{Text: content[start:], StartOffset: start, EndOffset: len(content)})
			break
		}
		end += start
		lines = append(lines, sourceLine{Text: content[start:end], StartOffset: start, EndOffset: end})
		start = end + 1
		if start == len(content) {
			lines = append(lines, sourceLine{StartOffset: start, EndOffset: start})
			break
		}
	}
	return lines
}

func sourceRange(content string, lines []sourceLine) (string, int, int) {
	if len(lines) == 0 {
		return "", 0, 0
	}
	start := lines[0].StartOffset
	end := lines[len(lines)-1].EndOffset
	raw := content[start:end]
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", 0, 0
	}
	leadingBytes := strings.Index(raw, trimmed)
	start += leadingBytes
	return trimmed, start, start + len(trimmed)
}

func normalizeDocumentContent(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return strings.TrimSpace(content)
}

func sourceHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func sourceParentID(documentVersion string, sectionPath []string, sectionStart int) string {
	identity := documentVersion + "\x00" + strings.Join(sectionPath, "\x00") + "\x00" + strconv.Itoa(sectionStart)
	return "parent_" + sourceHash(identity)[:24]
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
