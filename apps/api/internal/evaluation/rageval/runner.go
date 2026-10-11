package rageval

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Options struct {
	DatasetPath             string
	CorpusManifestPath      string
	TopK                    int
	MinSimilarity           float64
	MinimumEvidenceCoverage float64
	Revision                string
	RetrievalMode           string
	EmbeddingProfile        EmbeddingProfileOptions
	Reranker                rag.RerankerConfig
	LiveReranking           bool
}

func Run(ctx context.Context, opts Options) (Report, error) {
	if strings.TrimSpace(opts.DatasetPath) == "" || strings.TrimSpace(opts.CorpusManifestPath) == "" {
		return Report{}, errors.New("dataset and corpus manifest paths are required")
	}
	if opts.TopK == 0 {
		opts.TopK = 5
	}
	if opts.MinimumEvidenceCoverage == 0 {
		opts.MinimumEvidenceCoverage = rag.DefaultHeuristicRelevanceGateConfig().MinimumEvidenceCoverage
	}
	opts.RetrievalMode = strings.ToLower(strings.TrimSpace(opts.RetrievalMode))
	if opts.RetrievalMode == "" {
		opts.RetrievalMode = rag.RetrievalModeHybrid
	}
	if opts.TopK < 1 || opts.TopK > 20 || opts.MinSimilarity < 0 || opts.MinSimilarity > 1 || opts.MinimumEvidenceCoverage < 0.05 || opts.MinimumEvidenceCoverage > 1 {
		return Report{}, errors.New("top-k must be 1-20 and similarity/evidence thresholds must be within their documented ranges")
	}
	if opts.RetrievalMode != rag.RetrievalModeHybrid && opts.RetrievalMode != rag.RetrievalModeDenseOnly && opts.RetrievalMode != rag.RetrievalModeLexicalOnly {
		return Report{}, errors.New("retrieval mode must be hybrid, dense_only, or lexical_only")
	}
	mode := strings.ToLower(strings.TrimSpace(opts.Reranker.Mode))
	if mode == "tei" && !opts.LiveReranking {
		return Report{}, errors.New("TEI evaluation requires explicit live-reranking authorization")
	}
	reranker, err := rag.NewReranker(opts.Reranker)
	if err != nil {
		return Report{}, err
	}
	identity, err := reranker.Rerank(ctx, rag.RerankRequest{})
	if err != nil {
		return Report{}, err
	}
	embedder, err := newEvaluationEmbedder(opts.EmbeddingProfile)
	if err != nil {
		return Report{}, err
	}
	if embedder.options.Name != EmbeddingProfileHash {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, embedder.options.Timeout)
		defer cancel()
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
	fixtureStore := fixturestore.New()
	gateConfig := rag.DefaultHeuristicRelevanceGateConfig()
	gateConfig.MinimumEvidenceCoverage = opts.MinimumEvidenceCoverage
	if embedder.options.Name != EmbeddingProfileHash {
		gateConfig.ConfigVersion = fmt.Sprintf("semantic-eval-evidence-coverage-%.2f", gateConfig.MinimumEvidenceCoverage)
	} else if gateConfig.MinimumEvidenceCoverage != rag.DefaultHeuristicRelevanceGateConfig().MinimumEvidenceCoverage {
		gateConfig.ConfigVersion = fmt.Sprintf("eval-evidence-coverage-%.2f", gateConfig.MinimumEvidenceCoverage)
	}
	relevanceGate := rag.NewHeuristicRelevanceGate(gateConfig)
	evidence := &rerankerEvidence{Reranker: reranker}
	retriever := rag.NewRetrievalPipelineWithMode(fixtureStore, evidence, relevanceGate, opts.RetrievalMode)
	base := knowledge.NewKnowledgeBaseWithRetriever(fixtureStore, embedder, retriever)
	costSource := "not_applicable: deterministic local embedding"
	if embedder.options.Name != EmbeddingProfileHash {
		costSource = "unavailable: embedding API usage and pricing were not reported"
	}
	if mode == "tei" {
		costSource = "unavailable: reranker usage and pricing were not reported; embedding profile recorded separately"
	}
	evaluationKind := "offline_rag"
	if embedder.options.Name != EmbeddingProfileHash {
		evaluationKind = "semantic_retrieval"
	}
	report := Report{Identity: evalreport.Identity{ReportFormat: evalreport.Format, EvaluationKind: evaluationKind,
		DatasetID: dataset.ID, DatasetVersion: dataset.Version, DatasetHash: datasetHash,
		GitRevision: normalizedRevision(opts.Revision), StartedAt: startedAt}, SchemaVersion: SchemaVersion,
		Corpus: CorpusIdentity{DatasetID: manifest.DatasetID, Version: manifest.Version, Hash: corpusHash, Documents: len(documents)},
		Config: Config{TopK: opts.TopK, MinSimilarity: opts.MinSimilarity,
			MinimumEvidenceCoverage: opts.MinimumEvidenceCoverage, Chunker: rag.DocumentChunkerVersion, RetrievalMode: opts.RetrievalMode},
		Pipeline: PipelineIdentity{Fusion: rag.RRFInfo(), Reranker: identity.Info,
			RelevanceGate: relevanceGate.Info(), SecurityPolicy: rag.PromptInjectionPolicyVersion},
		EmbeddingProfile: EmbeddingProfileReport{Name: embedder.options.Name, Live: embedder.options.Live,
			ConfiguredModel: embedder.options.Model, ConfiguredDimensions: embedder.options.Dimensions,
			MaxCalls: embedder.options.MaxCalls, MaxInputTokens: embedder.options.MaxInputTokens,
			RetryMaxAttempts: embedder.options.RetryMaxAttempts, TimeoutMS: embedder.options.Timeout.Milliseconds(),
			IndexInputTransform: "document-chunker-v1 chunk content", QueryInputTransform: "trim + 3000-character cap",
			DistanceMetric: "cosine_similarity", LexicalImplementation: "file_token_overlap_heuristic"},
		IndexBuild: IndexBuildResult{Status: "completed", IndexIdentity: []domain.DocumentIndexIdentity{}},
		CostSource: costSource, Samples: make([]Sample, 0, len(dataset.Cases))}
	indexStarted := time.Now()
	for index, document := range documents {
		descriptor := manifest.Documents[index]
		version := strings.TrimSpace(descriptor.Version)
		if version == "" {
			version = manifest.Version
		}
		if _, err := base.Ingest(withEmbeddingPhase(ctx, phaseIndex), domain.DocumentIngestRequest{Title: descriptor.Title, SourceKey: descriptor.SourceKey, Version: version,
			Content: document, SourceType: "markdown", SourceURI: descriptor.File,
			MimeType: "text/markdown", Metadata: manifest.Documents[index].Metadata}); err != nil {
			report.IndexBuild.DurationMS = time.Since(indexStarted).Milliseconds()
			finishIndexFailure(&report, dataset, fmt.Errorf("ingest corpus document %q: %w", manifest.Documents[index].File, err))
			finishReport(&report, embedder)
			return report, nil
		}
	}
	report.IndexBuild.DurationMS = time.Since(indexStarted).Milliseconds()
	report.IndexBuild.IndexIdentity, err = fixtureStore.ListDocumentIndexIdentities(domain.DefaultWorkspaceID)
	if err != nil {
		finishIndexFailure(&report, dataset, fmt.Errorf("inspect evaluation index: %w", err))
		finishReport(&report, embedder)
		return report, nil
	}
	report.Pipeline.Embedding, _ = embedder.report()
	queryStarted := time.Now()
	for _, evaluationCase := range dataset.Cases {
		evidence.hash, evidence.latencyMS = "", 0
		sample := runSample(withEmbeddingPhase(ctx, phaseQuery), base, evaluationCase, opts.TopK, opts.MinSimilarity, &report.Pipeline)
		sample.CandidateSetHash, sample.RerankLatencyMS = evidence.hash, evidence.latencyMS
		report.Samples = append(report.Samples, sample)
	}
	report.QueryDurationMS = time.Since(queryStarted).Milliseconds()
	report.Summary = summarize(report.Samples)
	report.SplitSummaries = summarizeSplits(report.Samples)
	report.Gate = gate(report.Samples)
	finishReport(&report, embedder)
	return report, nil
}

func finishIndexFailure(report *Report, dataset domain.RAGGoldenDataset, err error) {
	info := failure.Describe(err)
	message, _ := redaction.Text(err.Error())
	report.IndexBuild.Status, report.IndexBuild.ErrorCode, report.IndexBuild.Error = "failed", info.Code, message
	for _, evaluationCase := range dataset.Cases {
		classification := "gating"
		if contains(evaluationCase.Tags, "non-blocking") {
			classification = "diagnostic"
		}
		report.Samples = append(report.Samples, Sample{CaseID: evaluationCase.ID, Split: evaluationSplit(evaluationCase.Tags),
			Classification: classification, Status: "not_evaluated", Answerable: evaluationCase.Answerable != nil && *evaluationCase.Answerable,
			ErrorCode: info.Code, Error: message, Sources: []RankedSource{}})
	}
	report.Summary = summarize(report.Samples)
	report.SplitSummaries = summarizeSplits(report.Samples)
	report.Gate = gate(report.Samples)
}

func finishReport(report *Report, embedder *evaluationEmbedder) {
	identity, usage := embedder.report()
	if report.Pipeline.Embedding.Provider == "" {
		report.Pipeline.Embedding = identity
	}
	report.EmbeddingProfile.Usage = usage
	report.CompletedAt = time.Now().UTC()
}

func normalizedRevision(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return strings.TrimSpace(value)
}
