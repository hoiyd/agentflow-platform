package rageval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evalreport"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/store"
)

const SchemaVersion = "rag-eval-v1"

type Options struct {
	DatasetPath        string
	CorpusManifestPath string
	TopK               int
	MinSimilarity      float64
	Revision           string
}

type Config struct {
	TopK          int     `json:"top_k"`
	MinSimilarity float64 `json:"min_similarity"`
	Chunker       string  `json:"chunker"`
}

type CorpusIdentity struct {
	DatasetID string `json:"dataset_id"`
	Version   string `json:"version"`
	Hash      string `json:"hash"`
	Documents int    `json:"documents"`
}

type PipelineIdentity struct {
	Embedding      domain.EmbeddingInfo     `json:"embedding"`
	Fusion         domain.FusionInfo        `json:"fusion"`
	Reranker       domain.RerankerInfo      `json:"reranker"`
	RelevanceGate  domain.RelevanceGateInfo `json:"relevance_gate"`
	SecurityPolicy string                   `json:"security_policy"`
}

type RankedSource struct {
	Rank            int    `json:"rank"`
	SourceURI       string `json:"source_uri,omitempty"`
	DocumentVersion string `json:"document_version,omitempty"`
	ChunkHash       string `json:"chunk_hash,omitempty"`
}

type Sample struct {
	CaseID            string         `json:"case_id"`
	Classification    string         `json:"classification"`
	Status            string         `json:"status"`
	Answerable        bool           `json:"answerable"`
	Passed            bool           `json:"passed"`
	HitAt1            bool           `json:"hit_at_1"`
	HitAt3            bool           `json:"hit_at_3"`
	HitAt5            bool           `json:"hit_at_5"`
	BestRank          int            `json:"best_rank,omitempty"`
	ReciprocalRank    float64        `json:"reciprocal_rank"`
	NDCG              float64        `json:"ndcg"`
	PredictedNoAnswer bool           `json:"predicted_no_answer"`
	LeakCount         int            `json:"leak_count"`
	BlockedCandidates int            `json:"blocked_candidates"`
	LatencyMS         int64          `json:"latency_ms"`
	FailureReason     string         `json:"failure_reason,omitempty"`
	ErrorCode         string         `json:"error_code,omitempty"`
	Error             string         `json:"error,omitempty"`
	Sources           []RankedSource `json:"sources"`
}

type Summary struct {
	Samples           int      `json:"samples"`
	Evaluated         int      `json:"evaluated"`
	Passed            int      `json:"passed"`
	Failed            int      `json:"failed"`
	NotEvaluated      int      `json:"not_evaluated"`
	GatingSamples     int      `json:"gating_samples"`
	GatingFailures    int      `json:"gating_failures"`
	AnswerableSamples int      `json:"answerable_samples"`
	HitAt1            float64  `json:"hit_at_1"`
	HitAt3            float64  `json:"hit_at_3"`
	HitAt5            float64  `json:"hit_at_5"`
	MRR               float64  `json:"mrr"`
	NDCG              float64  `json:"ndcg"`
	NoAnswerPrecision *float64 `json:"no_answer_precision"`
	NoAnswerRecall    *float64 `json:"no_answer_recall"`
	LeakCount         int      `json:"leak_count"`
	BlockedCandidates int      `json:"blocked_candidates"`
	MeanLatencyMS     float64  `json:"mean_latency_ms"`
}

type Comparison struct {
	Mode             string             `json:"mode"`
	Comparable       bool               `json:"comparable"`
	ChangedVariables []string           `json:"changed_variables"`
	Reasons          []string           `json:"reasons"`
	Regressions      []string           `json:"regressions"`
	Deltas           map[string]float64 `json:"deltas"`
}

type Report struct {
	evalreport.Identity
	SchemaVersion string           `json:"schema_version"`
	Corpus        CorpusIdentity   `json:"corpus"`
	Config        Config           `json:"config"`
	Pipeline      PipelineIdentity `json:"pipeline"`
	CostSource    string           `json:"cost_source"`
	Samples       []Sample         `json:"samples"`
	Summary       Summary          `json:"summary"`
	Gate          evalreport.Gate  `json:"gate"`
	Comparison    *Comparison      `json:"comparison,omitempty"`
}

type corpusManifest struct {
	DatasetID string           `json:"dataset_id"`
	Version   string           `json:"version"`
	Documents []corpusDocument `json:"documents"`
}

type corpusDocument struct {
	File     string         `json:"file"`
	Title    string         `json:"title"`
	Metadata map[string]any `json:"metadata"`
}

func Run(ctx context.Context, opts Options) (Report, error) {
	if strings.TrimSpace(opts.DatasetPath) == "" || strings.TrimSpace(opts.CorpusManifestPath) == "" {
		return Report{}, errors.New("dataset and corpus manifest paths are required")
	}
	if opts.TopK == 0 {
		opts.TopK = 5
	}
	if opts.TopK < 1 || opts.TopK > 20 || opts.MinSimilarity < 0 || opts.MinSimilarity > 1 {
		return Report{}, errors.New("top-k must be 1-20 and min-similarity must be 0-1")
	}
	startedAt := time.Now().UTC()
	dataset, datasetHash, err := loadDataset(opts.DatasetPath)
	if err != nil {
		return Report{}, err
	}
	manifest, documents, corpusHash, err := loadCorpus(opts.CorpusManifestPath, dataset)
	if err != nil {
		return Report{}, err
	}
	dir, err := os.MkdirTemp("", "agentflow-rag-eval-")
	if err != nil {
		return Report{}, err
	}
	defer os.RemoveAll(dir)
	fileStore, err := store.NewFileStore(filepath.Join(dir, "eval.json"))
	if err != nil {
		return Report{}, err
	}
	client := openai.NewClientWithTimeoutAndEmbeddingModel("", "", "https://offline.invalid/v1", "", "local_hash_embedding", 1536, time.Second)
	base := knowledge.NewKnowledgeBase(fileStore, client)
	for index, document := range documents {
		if _, err := base.Ingest(ctx, domain.DocumentIngestRequest{Title: manifest.Documents[index].Title, Version: manifest.Version,
			Content: document, SourceType: "markdown", SourceURI: manifest.Documents[index].File,
			MimeType: "text/markdown", Metadata: manifest.Documents[index].Metadata}); err != nil {
			return Report{}, fmt.Errorf("ingest corpus document %q: %w", manifest.Documents[index].File, err)
		}
	}
	report := Report{Identity: evalreport.Identity{ReportFormat: evalreport.Format, EvaluationKind: "offline_rag",
		DatasetID: dataset.ID, DatasetVersion: dataset.Version, DatasetHash: datasetHash,
		GitRevision: normalizedRevision(opts.Revision), StartedAt: startedAt}, SchemaVersion: SchemaVersion,
		Corpus:     CorpusIdentity{DatasetID: manifest.DatasetID, Version: manifest.Version, Hash: corpusHash, Documents: len(documents)},
		Config:     Config{TopK: opts.TopK, MinSimilarity: opts.MinSimilarity, Chunker: rag.DocumentChunkerVersion},
		CostSource: "not_applicable: deterministic local embedding", Samples: make([]Sample, 0, len(dataset.Cases))}
	for _, evaluationCase := range dataset.Cases {
		sample := runSample(ctx, base, evaluationCase, opts.TopK, opts.MinSimilarity, &report.Pipeline)
		report.Samples = append(report.Samples, sample)
	}
	report.Summary = summarize(report.Samples)
	report.Gate = gate(report.Samples)
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func runSample(ctx context.Context, base *knowledge.KnowledgeBase, evaluationCase domain.RAGEvaluationCase, topK int, minSimilarity float64, pipeline *PipelineIdentity) Sample {
	classification := "gating"
	if contains(evaluationCase.Tags, "non-blocking") {
		classification = "diagnostic"
	}
	sample := Sample{CaseID: evaluationCase.ID, Classification: classification, Status: "completed",
		Answerable: evaluationCase.Answerable != nil && *evaluationCase.Answerable, Sources: []RankedSource{}}
	if err := ctx.Err(); err != nil {
		sample.Status, sample.ErrorCode, sample.Error = "not_evaluated", "context_canceled", err.Error()
		return sample
	}
	startedAt := time.Now()
	response, err := base.Search(ctx, domain.DocumentSearch{Query: evaluationCase.Query, Limit: topK, MinSimilarity: minSimilarity}, topK)
	sample.LatencyMS = time.Since(startedAt).Milliseconds()
	if err != nil {
		info := failure.Describe(err)
		sample.Status, sample.ErrorCode = "failed", info.Code
		sample.Error, _ = redaction.Text(err.Error())
		return sample
	}
	if pipeline.Embedding.Provider == "" {
		*pipeline = PipelineIdentity{Embedding: response.Embedding, Fusion: response.Fusion, Reranker: response.Reranker,
			RelevanceGate: response.RelevanceGate, SecurityPolicy: response.Security.PolicyVersion}
	}
	result := rag.EvaluateCase(evaluationCase, response.Items)
	sample.Passed, sample.HitAt1, sample.HitAt3, sample.HitAt5, sample.BestRank = result.Hit, result.HitAt1, result.HitAt3, result.HitAt5, result.BestRank
	sample.PredictedNoAnswer = len(response.Items) == 0
	sample.LeakCount = rag.CountForbiddenMatches(evaluationCase, response.Items)
	sample.BlockedCandidates = response.Security.BlockedCandidates
	sample.FailureReason = result.FailureReason
	if sample.Answerable && sample.BestRank > 0 {
		sample.ReciprocalRank = 1 / float64(sample.BestRank)
		sample.NDCG = 1 / math.Log2(float64(sample.BestRank)+1)
	}
	for index, item := range response.Items {
		sample.Sources = append(sample.Sources, RankedSource{Rank: index + 1, SourceURI: item.Document.SourceURI,
			DocumentVersion: item.Document.Version, ChunkHash: item.Chunk.ContentHash})
	}
	return sample
}

func summarize(samples []Sample) Summary {
	summary := Summary{Samples: len(samples)}
	var noAnswerTP, noAnswerFP, noAnswerFN int
	for _, sample := range samples {
		if sample.Classification == "gating" {
			summary.GatingSamples++
			if !sample.Passed || sample.Status != "completed" {
				summary.GatingFailures++
			}
		}
		if sample.Answerable {
			summary.AnswerableSamples++
		}
		if sample.PredictedNoAnswer && !sample.Answerable {
			noAnswerTP++
		}
		if sample.PredictedNoAnswer && sample.Answerable {
			noAnswerFP++
		}
		if !sample.PredictedNoAnswer && !sample.Answerable {
			noAnswerFN++
		}
		switch sample.Status {
		case "not_evaluated":
			summary.NotEvaluated++
			continue
		case "failed":
			summary.Failed++
			continue
		}
		summary.Evaluated++
		if sample.Passed {
			summary.Passed++
		} else {
			summary.Failed++
		}
		summary.LeakCount += sample.LeakCount
		summary.BlockedCandidates += sample.BlockedCandidates
		summary.MeanLatencyMS += float64(sample.LatencyMS)
		if sample.Answerable {
			summary.MRR += sample.ReciprocalRank
			summary.NDCG += sample.NDCG
			if sample.HitAt1 {
				summary.HitAt1++
			}
			if sample.HitAt3 {
				summary.HitAt3++
			}
			if sample.HitAt5 {
				summary.HitAt5++
			}
		}
	}
	if summary.AnswerableSamples > 0 {
		denominator := float64(summary.AnswerableSamples)
		summary.HitAt1 /= denominator
		summary.HitAt3 /= denominator
		summary.HitAt5 /= denominator
		summary.MRR /= denominator
		summary.NDCG /= denominator
	}
	if summary.Evaluated > 0 {
		summary.MeanLatencyMS /= float64(summary.Evaluated)
	}
	if denominator := noAnswerTP + noAnswerFP; denominator > 0 {
		value := float64(noAnswerTP) / float64(denominator)
		summary.NoAnswerPrecision = &value
	}
	if denominator := noAnswerTP + noAnswerFN; denominator > 0 {
		value := float64(noAnswerTP) / float64(denominator)
		summary.NoAnswerRecall = &value
	}
	return summary
}

func gate(samples []Sample) evalreport.Gate {
	result := evalreport.Gate{Passed: true, Reasons: []string{}}
	for _, sample := range samples {
		if sample.Classification != "gating" {
			continue
		}
		result.BlockingSamples++
		if sample.Status != "completed" || !sample.Passed {
			result.Passed = false
			result.BlockingFailures++
			result.Reasons = append(result.Reasons, sample.CaseID+": "+sampleFailure(sample))
		}
	}
	if result.BlockingSamples == 0 {
		result.Passed = false
		result.Reasons = append(result.Reasons, "dataset contains no gating samples")
	}
	return result
}

func Compare(current, baseline Report, singleVariableAblation bool) Comparison {
	comparison := Comparison{Mode: "regression", Comparable: true, ChangedVariables: []string{}, Reasons: []string{}, Regressions: []string{}, Deltas: map[string]float64{}}
	if singleVariableAblation {
		comparison.Mode = "single_variable_ablation"
	}
	if baseline.ReportFormat != evalreport.Format || baseline.SchemaVersion != SchemaVersion {
		comparison.Reasons = append(comparison.Reasons, "report schema differs")
	}
	if current.DatasetID != baseline.DatasetID || current.DatasetVersion != baseline.DatasetVersion || current.DatasetHash != baseline.DatasetHash {
		comparison.Reasons = append(comparison.Reasons, "dataset identity differs")
	}
	if current.Corpus != baseline.Corpus {
		comparison.Reasons = append(comparison.Reasons, "corpus identity differs")
	}
	comparison.ChangedVariables = changedVariables(current, baseline)
	if len(comparison.ChangedVariables) > 0 && (!singleVariableAblation || len(comparison.ChangedVariables) != 1) {
		comparison.Reasons = append(comparison.Reasons, "pipeline comparison requires identical inputs or exactly one explicit ablation variable")
	}
	comparison.Comparable = len(comparison.Reasons) == 0
	if !comparison.Comparable {
		return comparison
	}
	comparison.Deltas["hit_at_1"] = current.Summary.HitAt1 - baseline.Summary.HitAt1
	comparison.Deltas["hit_at_3"] = current.Summary.HitAt3 - baseline.Summary.HitAt3
	comparison.Deltas["hit_at_5"] = current.Summary.HitAt5 - baseline.Summary.HitAt5
	comparison.Deltas["mrr"] = current.Summary.MRR - baseline.Summary.MRR
	comparison.Deltas["ndcg"] = current.Summary.NDCG - baseline.Summary.NDCG
	comparison.Deltas["mean_latency_ms"] = current.Summary.MeanLatencyMS - baseline.Summary.MeanLatencyMS
	comparison.Deltas["leak_count"] = float64(current.Summary.LeakCount - baseline.Summary.LeakCount)
	if current.Summary.GatingFailures > baseline.Summary.GatingFailures {
		comparison.Regressions = append(comparison.Regressions, "gating failures increased")
	}
	if current.Summary.MRR < baseline.Summary.MRR {
		comparison.Regressions = append(comparison.Regressions, "MRR decreased")
	}
	if current.Summary.NDCG < baseline.Summary.NDCG {
		comparison.Regressions = append(comparison.Regressions, "NDCG decreased")
	}
	if current.Summary.LeakCount > baseline.Summary.LeakCount {
		comparison.Regressions = append(comparison.Regressions, "forbidden-source leaks increased")
	}
	return comparison
}

func ApplyComparison(report *Report, baseline Report, singleVariableAblation bool) {
	comparison := Compare(*report, baseline, singleVariableAblation)
	report.Comparison = &comparison
	if !comparison.Comparable {
		report.Gate.Passed = false
		report.Gate.Reasons = append(report.Gate.Reasons, "baseline is not comparable: "+strings.Join(comparison.Reasons, ", "))
	} else if len(comparison.Regressions) > 0 {
		report.Gate.Passed = false
		report.Gate.Reasons = append(report.Gate.Reasons, comparison.Regressions...)
	}
}

func changedVariables(current, baseline Report) []string {
	changes := []string{}
	if current.Config.TopK != baseline.Config.TopK {
		changes = append(changes, "top_k")
	}
	if current.Config.MinSimilarity != baseline.Config.MinSimilarity {
		changes = append(changes, "min_similarity")
	}
	if current.Config.Chunker != baseline.Config.Chunker {
		changes = append(changes, "chunker")
	}
	if current.Pipeline.Embedding != baseline.Pipeline.Embedding {
		changes = append(changes, "embedding")
	}
	if current.Pipeline.Fusion != baseline.Pipeline.Fusion {
		changes = append(changes, "fusion")
	}
	if current.Pipeline.Reranker != baseline.Pipeline.Reranker {
		changes = append(changes, "reranker")
	}
	if current.Pipeline.RelevanceGate != baseline.Pipeline.RelevanceGate {
		changes = append(changes, "relevance_gate")
	}
	if current.Pipeline.SecurityPolicy != baseline.Pipeline.SecurityPolicy {
		changes = append(changes, "security_policy")
	}
	return changes
}

func loadDataset(path string) (domain.RAGGoldenDataset, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return domain.RAGGoldenDataset{}, "", fmt.Errorf("read dataset: %w", err)
	}
	var dataset domain.RAGGoldenDataset
	if err := json.Unmarshal(content, &dataset); err != nil {
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
	if err := json.Unmarshal(content, &manifest); err != nil {
		return manifest, nil, "", fmt.Errorf("decode corpus manifest: %w", err)
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
func normalizedRevision(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return strings.TrimSpace(value)
}
func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
func sampleFailure(sample Sample) string {
	if sample.ErrorCode != "" {
		return sample.ErrorCode
	}
	if sample.FailureReason != "" {
		return sample.FailureReason
	}
	return sample.Status
}
