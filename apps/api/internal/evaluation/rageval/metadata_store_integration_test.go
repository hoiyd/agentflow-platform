package rageval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Failure inventory: per-case predicates lost on either recall path/expansion;
// OR widens mandatory request or Workspace scope; updates leave inherited labels
// stale or re-embed; stale writes mutate data; inactive sources regain eligibility.
func TestMetadataGoldenStoreParity(t *testing.T) {
	t.Run("fixture", func(t *testing.T) { metadataGoldenStore(t, fixturestore.New(), "1", "2") })
	t.Run("postgres", func(t *testing.T) {
		pg, err := store.NewPostgresStore(pgfixture.DatabaseURL(t))
		if err != nil {
			t.Fatal(err)
		}
		defer pg.Close()
		workspace, err := pg.CreateWorkspace(t.Context(), identity.SuperUserID, "Metadata Golden", "")
		if err != nil {
			t.Fatal(err)
		}
		foreign, err := pg.CreateWorkspace(t.Context(), identity.SuperUserID, "Foreign Golden", "")
		if err != nil {
			t.Fatal(err)
		}
		metadataGoldenStore(t, pg, workspace.ID, foreign.ID)
	})
}

func metadataGoldenStore(t *testing.T, backend store.Store, workspace, foreign string) {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..", "..", "examples", "knowledge", "metadata-golden-v1")
	dataset, hash, err := loadDataset(filepath.Join(root, "dataset.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, bodies, corpusHash, err := loadCorpus(filepath.Join(root, "corpus-manifest.json"), dataset)
	if err != nil {
		t.Fatal(err)
	}
	embedder, err := newEvaluationEmbedder(EmbeddingProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	base := knowledge.NewKnowledgeBase(backend, embedder)
	documents := make(map[string]domain.Document)
	for index, d := range manifest.Documents {
		input := domain.DocumentIngestRequest{WorkspaceID: workspace, SourceKey: d.File, SourceURI: d.File, Title: d.Title,
			Content: bodies[index], SourceType: "markdown", MimeType: "text/markdown", Version: manifest.Version, Metadata: d.Metadata, MetadataSchemaVersion: d.MetadataSchemaVersion, Lifecycle: d.Lifecycle}
		document, err := base.Ingest(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		documents[d.File] = document
		input.WorkspaceID = foreign
		if _, err := base.Ingest(t.Context(), input); err != nil {
			t.Fatal(err)
		}
	}
	request := domain.RAGEvaluationRunRequest{Dataset: &dataset, WorkspaceID: workspace, TopK: 5, MinSimilarity: 0.15}
	result, err := base.Evaluate(withEmbeddingPhase(t.Context(), phaseQuery), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range result.Cases {
		if !c.Hit || c.Evidence == nil || c.Evidence.ContextLeakCount != 0 {
			t.Fatalf("%s: %+v", c.ID, c)
		}
		for _, source := range c.Evidence.ContextSources {
			if source.DocumentID != documents[source.SourceURI].ID || source.UpdatedAt.IsZero() || source.ChunkHash == "" {
				t.Fatalf("missing/current foreign source revision: %+v", source)
			}
		}
	}
	// Request AND wraps the case's OR, rather than being overwritten by it.
	bounded := dataset.Cases[6]
	bounded.ExpectedSources, bounded.RequiredSourceCount = dataset.Cases[0].ExpectedSources, 1
	bounded.ForbiddenSources = append(append([]domain.RAGGoldenSource(nil), bounded.ForbiddenSources...), dataset.Cases[2].ExpectedSources...)
	filter := &domain.MetadataFilter{SchemaVersion: "knowledge-metadata-v1", Where: domain.MetadataCondition{Field: "document.metadata.product", Op: "eq", Value: "Atlas"}}
	intersected, err := base.Evaluate(withEmbeddingPhase(t.Context(), phaseQuery), domain.RAGEvaluationRunRequest{Cases: []domain.RAGEvaluationCase{bounded}, WorkspaceID: workspace, MetadataFilter: filter})
	if err != nil || !intersected.Cases[0].Hit || len(intersected.Cases[0].Evidence.MetadataFilter.Where.All) != 2 {
		t.Fatalf("request scope widened: %+v %v", intersected, err)
	}

	atlas := documents["atlas-production.md"]
	_, usageBefore := embedder.report()
	updated, err := base.UpdateMetadata(t.Context(), workspace, atlas.ID, domain.DocumentMetadataUpdate{ExpectedUpdatedAt: atlas.UpdatedAt,
		MetadataSchemaVersion: atlas.MetadataSchemaVersion, Metadata: map[string]any{"product": "Atlas", "environment": "staging"}})
	if err != nil {
		t.Fatal(err)
	}
	_, usageAfter := embedder.report()
	if usageBefore != usageAfter || !updated.UpdatedAt.After(atlas.UpdatedAt) {
		t.Fatal("metadata update embedded or lost revision")
	}
	_, chunks, found, err := backend.GetDocument(atlas.ID)
	if err != nil || !found || len(chunks) == 0 {
		t.Fatal("updated chunks missing", err)
	}
	for _, chunk := range chunks {
		if chunk.Metadata["environment"] != "staging" {
			t.Fatal("stale inherited label", chunk)
		}
	}
	no := false
	noMatch := dataset.Cases[0]
	noMatch.Answerable, noMatch.ExpectedSources = &no, nil
	noMatch.ForbiddenSources = append(append([]domain.RAGGoldenSource(nil), noMatch.ForbiddenSources...), dataset.Cases[0].ExpectedSources...)
	changed, err := base.Evaluate(withEmbeddingPhase(t.Context(), phaseQuery), domain.RAGEvaluationRunRequest{Cases: []domain.RAGEvaluationCase{noMatch}, WorkspaceID: workspace})
	if err != nil || !changed.Cases[0].Hit {
		t.Fatalf("metadata update not effective: %+v %v", changed, err)
	}
	if _, err := base.UpdateMetadata(t.Context(), workspace, atlas.ID, domain.DocumentMetadataUpdate{ExpectedUpdatedAt: atlas.UpdatedAt, Metadata: atlas.Metadata, MetadataSchemaVersion: atlas.MetadataSchemaVersion}); err == nil {
		t.Fatal("stale edit succeeded")
	}
	encoded, err := json.MarshalIndent(map[string]any{"dataset_hash": hash, "corpus_hash": corpusHash, "workspace": workspace,
		"initial": result, "request_intersection": intersected, "metadata_update": changed, "before_revision": atlas.UpdatedAt, "after_revision": updated.UpdatedAt,
		"limitations": []string{"deterministic embeddings; real retrieval/Context selection", "Workspace scope, not Document ACL or live quality"}}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("EVALUATION_REPORT_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "rag-metadata-"+filepath.Base(t.Name())+".json"), encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
