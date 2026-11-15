package loadtest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const reportSchema = "agentflow-bounded-load-report-v1"

type suiteConfig struct {
	MaxConcurrentRuns     int           `json:"max_concurrent_runs"`
	RunQueueSize          int           `json:"run_queue_size"`
	RunQueueWait          time.Duration `json:"run_queue_wait_ns"`
	MaxModelRequests      int           `json:"max_model_requests"`
	ProviderDelay         time.Duration `json:"provider_delay_ns"`
	ProviderLongTailEvery int64         `json:"provider_long_tail_every"`
	ToolDelay             time.Duration `json:"tool_delay_ns"`
	RequestTimeout        time.Duration `json:"request_timeout_ns"`
	MemoryQueueSize       int           `json:"memory_queue_size"`
	MemoryJobTimeout      time.Duration `json:"memory_job_timeout_ns"`
	SoakDuration          time.Duration `json:"soak_duration_ns"`
	SoakArrivalInterval   time.Duration `json:"soak_arrival_interval_ns"`
}

type phaseReport struct {
	Name                 string         `json:"name"`
	Offered              int            `json:"offered"`
	Accepted             int            `json:"accepted"`
	Rejected             int            `json:"rejected"`
	RejectionRate        float64        `json:"rejection_rate"`
	Completed            int            `json:"completed"`
	Failed               int            `json:"failed"`
	ArrivalIntervalMS    int64          `json:"arrival_interval_ms"`
	DurationMS           int64          `json:"duration_ms"`
	QueueWaitP50MS       int64          `json:"queue_wait_p50_ms"`
	QueueWaitP95MS       int64          `json:"queue_wait_p95_ms"`
	ServiceP50MS         int64          `json:"service_p50_ms"`
	ServiceP95MS         int64          `json:"service_p95_ms"`
	PeakActiveRuns       int            `json:"peak_active_runs"`
	PeakSameConversation int            `json:"peak_same_conversation"`
	FailureCodes         map[string]int `json:"failure_codes"`
}

type controlReport struct {
	PeakProviderRequests    int64  `json:"peak_provider_requests"`
	PeakToolRequests        int64  `json:"peak_tool_requests"`
	Provider429Code         string `json:"provider_429_code"`
	ToolTimeoutCode         string `json:"tool_timeout_code"`
	TimeoutReleasedPermit   bool   `json:"timeout_released_permit"`
	CanceledRequestRecovers bool   `json:"canceled_request_recovers"`
	IgnoredCancelRecovered  bool   `json:"ignored_cancel_recovered"`
	RunBudgetCode           string `json:"run_budget_code"`
	RPMWaitCanceled         bool   `json:"rpm_wait_canceled"`
	RPMRecovered            bool   `json:"rpm_recovered"`
	TPMCapacityCode         string `json:"tpm_capacity_code"`
	MemorySyncAccepted      int64  `json:"memory_sync_accepted"`
	MemorySyncRejected      int64  `json:"memory_sync_rejected"`
}

type resourceReport struct {
	GoroutinesBefore        int    `json:"goroutines_before"`
	GoroutinesAfter         int    `json:"goroutines_after"`
	GoroutineDelta          int    `json:"goroutine_delta"`
	HeapAllocBeforeBytes    uint64 `json:"heap_alloc_before_bytes"`
	HeapAllocAfterBytes     uint64 `json:"heap_alloc_after_bytes"`
	HeapAllocDeltaBytes     int64  `json:"heap_alloc_delta_bytes"`
	ConnectionsOpened       int64  `json:"connections_opened"`
	ConnectionsAfterCleanup int64  `json:"connections_after_cleanup"`
}

type loadReport struct {
	SchemaVersion string         `json:"schema_version"`
	GitRevision   string         `json:"git_revision"`
	ConfigHash    string         `json:"config_hash"`
	GoVersion     string         `json:"go_version"`
	GOOS          string         `json:"goos"`
	GOARCH        string         `json:"goarch"`
	CPUCount      int            `json:"cpu_count"`
	StartedAt     time.Time      `json:"started_at"`
	CompletedAt   time.Time      `json:"completed_at"`
	Config        suiteConfig    `json:"config"`
	Phases        []phaseReport  `json:"phases"`
	Controls      controlReport  `json:"controls"`
	Resources     resourceReport `json:"resources"`
	Limitations   []string       `json:"limitations"`
}

func hashConfig(config suiteConfig) string {
	content, _ := json.Marshal(config)
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func gitRevision() string {
	content, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(content))
}

func writeReport(t *testing.T, report loadReport) {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv("EVALUATION_REPORT_DIR"))
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bounded-load.json"), content, 0600); err != nil {
		t.Fatal(err)
	}
	var table strings.Builder
	table.WriteString("# Bounded Load / Soak Evidence\n\n")
	table.WriteString("| Phase | Offered | Accepted | Rejected | Rejection rate | Completed | Failed | Queue p50/p95 ms | Service p50/p95 ms | Peak runs |\n")
	table.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, phase := range report.Phases {
		fmt.Fprintf(&table, "| %s | %d | %d | %d | %.1f%% | %d | %d | %d/%d | %d/%d | %d |\n",
			phase.Name, phase.Offered, phase.Accepted, phase.Rejected, phase.RejectionRate*100, phase.Completed, phase.Failed,
			phase.QueueWaitP50MS, phase.QueueWaitP95MS, phase.ServiceP50MS, phase.ServiceP95MS, phase.PeakActiveRuns)
	}
	table.WriteString("\nFixture throughput validates local scheduling boundaries only; it is not real model QPS or an SLO.\n")
	if err := os.WriteFile(filepath.Join(dir, "bounded-load.md"), []byte(table.String()), 0600); err != nil {
		t.Fatal(err)
	}
}
