package rag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

type goldenCorpusManifest struct {
	DatasetID string `json:"dataset_id"`
	Version   string `json:"version"`
	Documents []struct {
		File string `json:"file"`
	} `json:"documents"`
}

func TestCanonicalGoldenDatasetV1Asset(t *testing.T) {
	t.Parallel()

	repositoryRoot := goldenAssetRepositoryRoot(t)
	datasetPath := filepath.Join(repositoryRoot, "examples", "knowledge", "golden-dataset.v1.json")
	corpusRoot := filepath.Join(repositoryRoot, "examples", "knowledge", "golden-v1")

	var dataset domain.RAGGoldenDataset
	readGoldenJSON(t, datasetPath, &dataset)
	if _, _, err := ResolveEvaluationCases(domain.RAGEvaluationRunRequest{Dataset: &dataset}); err != nil {
		t.Fatalf("canonical Golden Dataset is invalid: %v", err)
	}

	var manifest goldenCorpusManifest
	readGoldenJSON(t, filepath.Join(corpusRoot, "corpus-manifest.v1.json"), &manifest)
	if manifest.DatasetID != dataset.ID || manifest.Version != dataset.Version {
		t.Fatalf("corpus identity %s@%s does not match dataset %s@%s", manifest.DatasetID, manifest.Version, dataset.ID, dataset.Version)
	}

	documentPaths := make(map[string]string, len(manifest.Documents))
	for _, document := range manifest.Documents {
		if strings.TrimSpace(document.File) == "" {
			t.Fatal("corpus manifest contains an empty file")
		}
		if _, duplicate := documentPaths[document.File]; duplicate {
			t.Fatalf("corpus manifest contains duplicate file %q", document.File)
		}
		path := filepath.Join(corpusRoot, document.File)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("corpus file %q is unavailable: %v", document.File, err)
		}
		documentPaths[document.File] = path
	}

	requiredCategories := map[string]bool{
		"fact": false, "paraphrase": false, "exact-id": false, "multi-hop": false,
		"no-answer": false, "acl": false, "stale-data": false, "prompt-injection": false,
	}
	referencedDocuments := map[string]bool{}
	for _, evalCase := range dataset.Cases {
		for _, tag := range evalCase.Tags {
			if _, required := requiredCategories[tag]; required {
				requiredCategories[tag] = true
			}
		}
		validateGoldenAssetSources(t, corpusRoot, documentPaths, referencedDocuments, evalCase.ID, evalCase.ExpectedSources)
		validateGoldenAssetSources(t, corpusRoot, documentPaths, referencedDocuments, evalCase.ID, evalCase.ForbiddenSources)

		if hasTag(evalCase.Tags, "multi-hop") && (evalCase.RequiredSourceCount < 2 || len(evalCase.ExpectedSources) < 2) {
			t.Fatalf("multi-hop case %q must require at least two expected sources", evalCase.ID)
		}
		if (hasTag(evalCase.Tags, "acl") || hasTag(evalCase.Tags, "stale-data") || hasTag(evalCase.Tags, "no-answer")) && !hasTag(evalCase.Tags, "non-blocking") {
			t.Fatalf("case %q must remain non-blocking until its policy prerequisites are implemented", evalCase.ID)
		}
		if hasTag(evalCase.Tags, "prompt-injection") {
			if len(evalCase.ForbiddenSources) == 0 {
				t.Fatalf("prompt-injection case %q requires a forbidden source", evalCase.ID)
			}
			for _, source := range evalCase.ForbiddenSources {
				content := readGoldenFile(t, documentPaths[source.SourceURI])
				if len(DetectPromptInjection(content)) == 0 {
					t.Fatalf("prompt-injection source %q is not detected by policy %s", source.SourceURI, PromptInjectionPolicyVersion)
				}
			}
		}
	}

	for category, covered := range requiredCategories {
		if !covered {
			t.Errorf("canonical Golden Dataset does not cover category %q", category)
		}
	}
	for document := range documentPaths {
		if !referencedDocuments[document] {
			t.Errorf("corpus document %q is not referenced by any evaluation case", document)
		}
	}
}

func validateGoldenAssetSources(t *testing.T, corpusRoot string, documentPaths map[string]string, referenced map[string]bool, caseID string, sources []domain.RAGGoldenSource) {
	t.Helper()
	for _, source := range sources {
		if source.SourceURI == "" {
			continue
		}
		path, exists := documentPaths[source.SourceURI]
		if !exists {
			t.Fatalf("case %q references source_uri %q outside %s", caseID, source.SourceURI, corpusRoot)
		}
		referenced[source.SourceURI] = true
		content := strings.ToLower(readGoldenFile(t, path))
		for _, fragment := range source.ContentContains {
			if !strings.Contains(content, strings.ToLower(fragment)) {
				t.Fatalf("case %q expects fragment %q outside source %q", caseID, fragment, source.SourceURI)
			}
		}
	}
}

func goldenAssetRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve Golden Dataset test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", ".."))
}

func readGoldenJSON(t *testing.T, path string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(readGoldenFile(t, path)), target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func readGoldenFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func hasTag(tags []string, expected string) bool {
	for _, tag := range tags {
		if tag == expected {
			return true
		}
	}
	return false
}
