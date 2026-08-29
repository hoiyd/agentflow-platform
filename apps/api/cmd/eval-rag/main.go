package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"agentflow-platform/apps/api/app"
	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/domain"
)

type options struct {
	datasetPath    string
	corpusManifest string
	corpusRoot     string
	workspaceID    string
	topK           int
	minSimilarity  float64
	seedOnly       bool
	enforce        bool
	jsonOutput     bool
}

type corpusManifest struct {
	DatasetID string                   `json:"dataset_id"`
	Version   string                   `json:"version"`
	Documents []corpusManifestDocument `json:"documents"`
}

type corpusManifestDocument struct {
	File     string         `json:"file"`
	Title    string         `json:"title"`
	Metadata map[string]any `json:"metadata"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 2
	}
	evaluator, err := app.NewOfflineRAGEvaluator(config.Load())
	if err != nil {
		fmt.Fprintf(stderr, "initialize RAG evaluator: %v\n", err)
		return 1
	}
	defer func() {
		if closeErr := evaluator.Close(); closeErr != nil {
			fmt.Fprintf(stderr, "close RAG evaluator: %v\n", closeErr)
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if opts.corpusManifest != "" {
		if err := seedCorpus(ctx, evaluator, opts, stdout); err != nil {
			fmt.Fprintf(stderr, "seed evaluation corpus: %v\n", err)
			return 1
		}
	}
	if opts.seedOnly {
		return 0
	}

	dataset, err := decodeJSONFile[domain.RAGGoldenDataset](opts.datasetPath)
	if err != nil {
		fmt.Fprintf(stderr, "load Golden Dataset: %v\n", err)
		return 1
	}
	result, err := evaluator.Evaluate(ctx, domain.RAGEvaluationRunRequest{
		Dataset: &dataset, WorkspaceID: opts.workspaceID,
		TopK: opts.topK, MinSimilarity: opts.minSimilarity,
	})
	if err != nil {
		fmt.Fprintf(stderr, "evaluate Golden Dataset: %v\n", err)
		return 1
	}
	if err := writeResult(stdout, result, opts.jsonOutput); err != nil {
		fmt.Fprintf(stderr, "write evaluation result: %v\n", err)
		return 1
	}
	if opts.enforce && len(gatingMisses(result)) > 0 {
		return 1
	}
	return 0
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	var opts options
	flags := flag.NewFlagSet("eval-rag", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.datasetPath, "dataset", "", "path to a versioned Golden Dataset JSON file")
	flags.StringVar(&opts.corpusManifest, "corpus-manifest", "", "optional corpus manifest to seed before evaluation")
	flags.StringVar(&opts.corpusRoot, "corpus-root", "", "directory containing files referenced by the corpus manifest")
	flags.StringVar(&opts.workspaceID, "workspace", domain.DefaultWorkspaceID, "workspace namespace used for seeding and retrieval")
	flags.IntVar(&opts.topK, "top-k", 5, "maximum retrieved items scored per case")
	flags.Float64Var(&opts.minSimilarity, "min-similarity", 0.15, "minimum retrieval similarity")
	flags.BoolVar(&opts.seedOnly, "seed-only", false, "seed the corpus without running evaluation")
	flags.BoolVar(&opts.enforce, "enforce", false, "exit non-zero when a gating case misses")
	flags.BoolVar(&opts.jsonOutput, "json", false, "write the full evaluation result as JSON")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	opts.datasetPath = strings.TrimSpace(opts.datasetPath)
	opts.corpusManifest = strings.TrimSpace(opts.corpusManifest)
	opts.corpusRoot = strings.TrimSpace(opts.corpusRoot)
	if opts.topK < 1 || opts.topK > 50 {
		return options{}, errors.New("top-k must be between 1 and 50")
	}
	if opts.minSimilarity < 0 || opts.minSimilarity > 1 {
		return options{}, errors.New("min-similarity must be between 0 and 1")
	}
	if opts.corpusManifest != "" && opts.corpusRoot == "" {
		return options{}, errors.New("corpus-root is required with corpus-manifest")
	}
	if opts.seedOnly && opts.corpusManifest == "" {
		return options{}, errors.New("seed-only requires corpus-manifest")
	}
	if !opts.seedOnly && opts.datasetPath == "" {
		return options{}, errors.New("dataset is required unless seed-only is set")
	}
	return opts, nil
}

func seedCorpus(ctx context.Context, evaluator *app.OfflineRAGEvaluator, opts options, output io.Writer) error {
	manifest, err := decodeJSONFile[corpusManifest](opts.corpusManifest)
	if err != nil {
		return err
	}
	if strings.TrimSpace(manifest.DatasetID) == "" || strings.TrimSpace(manifest.Version) == "" {
		return errors.New("corpus manifest requires dataset_id and version")
	}
	existing, err := evaluator.ListDocuments(opts.workspaceID)
	if err != nil {
		return err
	}
	existingSources := make(map[string]struct{}, len(existing))
	for _, document := range existing {
		existingSources[document.SourceURI] = struct{}{}
	}
	for _, document := range manifest.Documents {
		sourceURI := strings.TrimSpace(document.File)
		if sourceURI == "" || strings.TrimSpace(document.Title) == "" {
			return errors.New("every corpus document requires file and title")
		}
		if _, ok := existingSources[sourceURI]; ok {
			fmt.Fprintf(output, "skip   %s (already indexed)\n", sourceURI)
			continue
		}
		path, err := pathWithinRoot(opts.corpusRoot, sourceURI)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", sourceURI, err)
		}
		metadata := cloneMetadata(document.Metadata)
		metadata["golden_dataset_id"] = manifest.DatasetID
		metadata["golden_corpus_version"] = manifest.Version
		if _, err := evaluator.Ingest(ctx, domain.DocumentIngestRequest{
			WorkspaceID: opts.workspaceID, Title: document.Title, Content: string(content),
			SourceType: "markdown", SourceURI: sourceURI, MimeType: "text/markdown", Metadata: metadata,
		}); err != nil {
			return fmt.Errorf("ingest %s: %w", sourceURI, err)
		}
		fmt.Fprintf(output, "seeded %s\n", sourceURI)
	}
	return nil
}

func decodeJSONFile[T any](path string) (T, error) {
	var value T
	file, err := os.Open(path)
	if err != nil {
		return value, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return value, errors.New("JSON file contains trailing content")
	}
	return value, nil
}

func pathWithinRoot(root, name string) (string, error) {
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("corpus file %q must be relative to corpus-root", name)
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	candidate, err := filepath.Abs(filepath.Join(rootPath, name))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootPath, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("corpus file %q escapes corpus-root", name)
	}
	return candidate, nil
}

func cloneMetadata(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source)+2)
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func writeResult(output io.Writer, result domain.RAGEvaluationRunResponse, asJSON bool) error {
	if asJSON {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	if result.Dataset != nil {
		fmt.Fprintf(output, "\n%s@%s\n", result.Dataset.ID, result.Dataset.Version)
	}
	fmt.Fprintf(output, "total=%d hit@1=%d hit@3=%d hit@5=%d misses=%d blocked=%d\n\n",
		result.Summary.Total, result.Summary.HitAt1, result.Summary.HitAt3,
		result.Summary.HitAt5, result.Summary.Misses, result.Summary.BlockedCandidates)
	for _, item := range result.Cases {
		classification := "gating"
		if hasTag(item.Tags, "non-blocking") {
			classification = "diagnostic"
		}
		detail := item.FailureReason
		if item.Hit && item.Answerable {
			detail = fmt.Sprintf("rank %d", item.BestRank)
		} else if item.Hit {
			detail = "correct no-answer"
		}
		status := "MISS"
		if item.Hit {
			status = "PASS"
		}
		fmt.Fprintf(output, "%s [%s] %s: %s\n", status, classification, item.ID, detail)
	}
	return nil
}

func gatingMisses(result domain.RAGEvaluationRunResponse) []domain.RAGEvaluationCaseResult {
	misses := make([]domain.RAGEvaluationCaseResult, 0)
	for _, item := range result.Cases {
		if !item.Hit && !hasTag(item.Tags, "non-blocking") {
			misses = append(misses, item)
		}
	}
	return misses
}

func hasTag(tags []string, expected string) bool {
	for _, tag := range tags {
		if tag == expected {
			return true
		}
	}
	return false
}
