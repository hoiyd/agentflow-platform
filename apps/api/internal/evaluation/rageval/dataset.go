package rageval

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type corpusManifest struct {
	DatasetID string           `json:"dataset_id"`
	Version   string           `json:"version"`
	Documents []corpusDocument `json:"documents"`
}

type corpusDocument struct {
	File                  string                    `json:"file"`
	SourceKey             string                    `json:"source_key,omitempty"`
	Version               string                    `json:"version,omitempty"`
	Title                 string                    `json:"title"`
	Metadata              map[string]any            `json:"metadata"`
	MetadataSchemaVersion string                    `json:"metadata_schema_version,omitempty"`
	Lifecycle             *domain.DocumentLifecycle `json:"lifecycle,omitempty"`
}

func loadDataset(path string) (domain.RAGGoldenDataset, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return domain.RAGGoldenDataset{}, "", fmt.Errorf("read dataset: %w", err)
	}
	var dataset domain.RAGGoldenDataset
	if err := rag.DecodeEvaluationJSON(content, &dataset); err != nil {
		return dataset, "", fmt.Errorf("decode dataset: %w", err)
	}
	if _, _, err := rag.ResolveEvaluationCases(domain.RAGEvaluationRunRequest{Dataset: &dataset}); err != nil {
		return dataset, "", err
	}
	return dataset, digest(content), nil
}

func loadCorpus(manifestPath string, dataset domain.RAGGoldenDataset) (corpusManifest, []string, string, error) {
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return corpusManifest{}, nil, "", fmt.Errorf("read corpus manifest: %w", err)
	}
	var manifest corpusManifest
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, nil, "", fmt.Errorf("decode corpus manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return manifest, nil, "", errors.New("trailing corpus manifest JSON")
	}
	if strings.TrimSpace(manifest.DatasetID) != dataset.ID || strings.TrimSpace(manifest.Version) != dataset.Version || len(manifest.Documents) == 0 {
		return manifest, nil, "", errors.New("corpus manifest identity must match the non-empty dataset")
	}
	hash := sha256.New()
	hash.Write(content)
	root := filepath.Dir(manifestPath)
	documents := make([]string, 0, len(manifest.Documents))
	seen := map[string]struct{}{}
	for _, document := range manifest.Documents {
		name := filepath.Clean(strings.TrimSpace(document.File))
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || strings.TrimSpace(document.Title) == "" {
			return manifest, nil, "", fmt.Errorf("invalid corpus document %q", document.File)
		}
		if _, duplicate := seen[name]; duplicate {
			return manifest, nil, "", fmt.Errorf("duplicate corpus document %q", name)
		}
		seen[name] = struct{}{}
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return manifest, nil, "", fmt.Errorf("read corpus document %q: %w", name, err)
		}
		if len(body) > 1<<20 {
			return manifest, nil, "", fmt.Errorf("corpus document %q exceeds 1 MiB", name)
		}
		hash.Write([]byte(name))
		hash.Write(body)
		documents = append(documents, string(body))
	}
	return manifest, documents, hex.EncodeToString(hash.Sum(nil)), nil
}

func digest(content []byte) string { sum := sha256.Sum256(content); return hex.EncodeToString(sum[:]) }
