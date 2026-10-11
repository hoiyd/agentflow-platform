package rageval

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
)

const SchemaVersion = "rag-eval-v1"

type Config struct {
	TopK                    int     `json:"top_k"`
	MinSimilarity           float64 `json:"min_similarity"`
	MinimumEvidenceCoverage float64 `json:"minimum_evidence_coverage"`
	Chunker                 string  `json:"chunker"`
	RetrievalMode           string  `json:"retrieval_mode"`
}

type IndexBuildResult struct {
	Status        string                         `json:"status"`
	DurationMS    int64                          `json:"duration_ms"`
	ErrorCode     string                         `json:"error_code,omitempty"`
	Error         string                         `json:"error,omitempty"`
	IndexIdentity []domain.DocumentIndexIdentity `json:"index_identity"`
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
	Rank             int     `json:"rank"`
	SourceURI        string  `json:"source_uri,omitempty"`
	DocumentVersion  string  `json:"document_version,omitempty"`
	ChunkHash        string  `json:"chunk_hash,omitempty"`
	Confidence       string  `json:"confidence,omitempty"`
	Similarity       float64 `json:"similarity"`
	EvidenceCoverage float64 `json:"evidence_coverage"`
	FilterReason     string  `json:"filter_reason,omitempty"`
}

type Sample struct {
	CaseID            string         `json:"case_id"`
	Split             string         `json:"split"`
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
	CandidateSetHash  string         `json:"candidate_set_hash,omitempty"`
	RerankLatencyMS   int64          `json:"rerank_latency_ms"`
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
	SchemaVersion    string                 `json:"schema_version"`
	Corpus           CorpusIdentity         `json:"corpus"`
	Config           Config                 `json:"config"`
	Pipeline         PipelineIdentity       `json:"pipeline"`
	EmbeddingProfile EmbeddingProfileReport `json:"embedding_profile"`
	IndexBuild       IndexBuildResult       `json:"index_build"`
	QueryDurationMS  int64                  `json:"query_duration_ms"`
	CostSource       string                 `json:"cost_source"`
	Samples          []Sample               `json:"samples"`
	Summary          Summary                `json:"summary"`
	SplitSummaries   map[string]Summary     `json:"split_summaries"`
	Gate             evalreport.Gate        `json:"gate"`
	Comparison       *Comparison            `json:"comparison,omitempty"`
}
