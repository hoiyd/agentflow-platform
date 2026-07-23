package rag

import (
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestBuildDocumentChunksSplitsAndCopiesMetadata(t *testing.T) {
	content := strings.Repeat("agentflow deployment password amber-9137. ", 180)
	document, chunks, err := BuildDocument(documentIngestRequestForTest("Deploy Notes", content))
	if err != nil {
		t.Fatalf("build chunks: %v", err)
	}
	if document.Title != "Deploy Notes" {
		t.Fatalf("unexpected document title: %q", document.Title)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for index, chunk := range chunks {
		if chunk.ChunkIndex != index {
			t.Fatalf("expected chunk index %d, got %d", index, chunk.ChunkIndex)
		}
		if chunk.Metadata["project"] != "agentflow" {
			t.Fatalf("expected copied metadata, got %#v", chunk.Metadata)
		}
		if chunk.TokenCount <= 0 {
			t.Fatalf("expected token estimate, got %d", chunk.TokenCount)
		}
	}
}

func TestBuildMarkdownDocumentChunksUsesStructureMetadata(t *testing.T) {
	content := strings.Join([]string{
		"# AgentFlow",
		"",
		"Intro paragraph for the project.",
		"",
		"## Setup",
		"",
		"- Install Postgres",
		"1. Enable pgvector",
		"",
		"```sql",
		"CREATE EXTENSION IF NOT EXISTS vector;",
		"```",
	}, "\n")
	document, chunks, err := BuildDocument(domain.DocumentIngestRequest{
		Title:      "README",
		Content:    content,
		SourceType: "markdown",
	})
	if err != nil {
		t.Fatalf("build markdown chunks: %v", err)
	}
	if document.Metadata["format"] != "markdown" {
		t.Fatalf("expected markdown format metadata, got %#v", document.Metadata)
	}

	var foundList bool
	var foundOrderedList bool
	var foundCode bool
	for _, chunk := range chunks {
		switch chunk.Metadata["chunk_type"] {
		case "list":
			foundList = true
			foundOrderedList = foundOrderedList || strings.Contains(chunk.Content, "1. Enable pgvector")
			if chunk.Metadata["heading_path"] != "AgentFlow > Setup" {
				t.Fatalf("expected setup heading path, got %#v", chunk.Metadata)
			}
			if !strings.HasPrefix(chunk.Content, "# AgentFlow\n## Setup\n\n") {
				t.Fatalf("expected heading context in list chunk, got %q", chunk.Content)
			}
		case "code":
			foundCode = true
			if strings.Count(chunk.Content, "```") != 2 {
				t.Fatalf("expected fenced code block to stay intact, got %q", chunk.Content)
			}
			if !strings.Contains(chunk.Content, "## Setup") {
				t.Fatalf("expected heading context in code chunk, got %q", chunk.Content)
			}
			if chunk.Metadata["code_language"] != "sql" {
				t.Fatalf("expected sql code language, got %#v", chunk.Metadata)
			}
		}
	}
	if !foundList || !foundOrderedList || !foundCode {
		t.Fatalf("expected unordered list, ordered list, and code chunks, got %#v", chunks)
	}
}

func TestRerankDocumentChunksBoostsLexicalAndMetadataMatches(t *testing.T) {
	items := []domain.RetrievedDocumentChunk{
		{
			Document: domain.Document{ID: "doc_en", Title: "English Notes"},
			Chunk: domain.DocumentChunk{
				ID:      "chunk_en",
				Content: "This unrelated English deployment note has a decent vector score.",
				Metadata: map[string]any{
					"heading_path": "Deploy",
				},
			},
			Similarity:   0.72,
			RecencyBoost: 0.01,
			Score:        0.73,
			VectorRank:   1,
		},
		{
			Document: domain.Document{ID: "doc_cn", Title: "家常菜谱"},
			Chunk: domain.DocumentChunk{
				ID:      "chunk_cn",
				Content: "家常做饭包括切菜、炒菜、煮米饭。",
				Metadata: map[string]any{
					"heading_path": "厨房 > 做饭",
				},
			},
			Similarity:   0.61,
			RecencyBoost: 0.01,
			Score:        0.62,
			VectorRank:   2,
			LexicalRank:  1,
		},
	}

	reranked := Rerank("做饭", ReciprocalRankFusion(items), 2)
	if len(reranked) != 2 {
		t.Fatalf("expected two reranked chunks, got %d", len(reranked))
	}
	if reranked[0].Document.ID != "doc_cn" {
		t.Fatalf("expected lexical/metadata match first, got %#v", reranked)
	}
	if reranked[0].LexicalBoost <= 0 || reranked[0].MetadataBoost <= 0 || reranked[0].RRFScore <= 0 || reranked[0].RerankScore <= normalizedRRFScore(reranked[0].RRFScore) {
		t.Fatalf("expected rerank boosts on Chinese match, got %#v", reranked[0])
	}
}

func documentIngestRequestForTest(title string, content string) domain.DocumentIngestRequest {
	return domain.DocumentIngestRequest{
		Title:   title,
		Content: content,
		Metadata: map[string]any{
			"project": "agentflow",
		},
	}
}
