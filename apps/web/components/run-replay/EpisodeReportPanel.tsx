import { useEffect, useState } from "react";
import { ChevronDown, Download, RefreshCw } from "lucide-react";
import { getEpisodeReport, type EpisodeReport } from "../../lib/api";
import { createLatestRequestController } from "../../lib/latest-request";

export function EpisodeReportPanel({ runId }: { runId: string }) {
  const [report, setReport] = useState<EpisodeReport | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [requests] = useState(createLatestRequestController);
  useEffect(() => () => requests.cancel(), [requests]);

  async function load() {
    const request = requests.begin();
    setLoading(true);
    setError("");
    try {
      const value = await getEpisodeReport(runId, request.signal);
      if (!request.isCurrent()) return;
      if (value.run.id !== runId) throw new Error("Episode report belongs to another Run");
      setReport(value);
    } catch (error) {
      if (request.isCurrent()) setError(error instanceof Error ? error.message : "Failed to load episode report");
    } finally {
      if (request.isCurrent()) setLoading(false);
    }
  }

  return <details className="episode-report" id="run-verification-evidence" onToggle={event => {
    if (event.currentTarget.open && !report && !loading && !error) void load();
  }}>
    <summary className="episode-report-toggle">Episode report <ChevronDown aria-hidden="true" size={16} /></summary>
    <div className="episode-report-body">
      {loading ? <div role="status">Loading report...</div> : null}
      {error ? <div role="alert" className="error">Episode report unavailable: {error}</div> : null}
      {!report && error ? <button className="run-link" type="button" disabled={loading} onClick={() => void load()}>
        <RefreshCw aria-hidden="true" size={14} /> Retry report
      </button> : null}
      {report ? <ReportContent report={report} onRefresh={() => void load()} loading={loading} /> : null}
    </div>
  </details>;
}

function ReportContent({ report, onRefresh, loading }: { report: EpisodeReport; onRefresh: () => void; loading: boolean }) {
  const verificationTone =
    report.verification.status === "passed"
      ? "passed"
      : report.verification.status === "failed"
        ? "failed"
        : "needs-review";
  return (
    <>
      <div className="episode-report-header">
        <div>
          <p>
            {report.agent.name} captured {report.steps.length} steps, {report.llm_calls.length} LLM calls,{" "}
            {report.tool_calls.length} tool calls.
          </p>
        </div>
        <div className="episode-report-actions">
          <button className="run-link" onClick={onRefresh} disabled={loading} type="button"><RefreshCw aria-hidden="true" size={14} /> Refresh report</button>
          <button className="run-link" onClick={() => exportEpisodeJSON(report)} type="button"><Download aria-hidden="true" size={14} /> Export JSON</button>
        </div>
      </div>

      <div className="episode-report-grid">
        <div className="episode-card">
          <span>Verification</span>
          <strong className={`episode-verification ${verificationTone}`}>{report.verification.status}</strong>
        </div>
        <div className="episode-card">
          <span>Retrieved context</span>
          <strong>{report.retrievals.memories.length + report.retrievals.chunks.length}</strong>
        </div>
        <div className="episode-card">
          <span>Final output</span>
          <strong>{report.final_output ? "Captured" : "Missing"}</strong>
        </div>
      </div>

      <div className="episode-sections">
        <div>
          <div className="episode-section-title">Task</div>
          <p>{report.task || "No task text captured."}</p>
        </div>
        <div>
          <div className="episode-section-title">Evidence</div>
          {report.verification.evidence.length > 0 ? (
            <ul>
              {report.verification.evidence.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          ) : (
            <p>No positive evidence recorded.</p>
          )}
        </div>
        <div>
          <div className="episode-section-title">Warnings</div>
          {report.verification.warnings.length > 0 ? (
            <ul>
              {report.verification.warnings.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          ) : (
            <p>No warnings.</p>
          )}
        </div>
        <div>
          <div className="episode-section-title">Errors</div>
          {report.errors.length > 0 ? (
            <ul>
              {report.errors.map((item, index) => (
                <li key={`${item.source}-${index}`}>
                  {item.source}: {item.message}
                </li>
              ))}
            </ul>
          ) : (
            <p>No errors recorded.</p>
          )}
        </div>
      </div>
    </>
  );
}

function exportEpisodeJSON(report: EpisodeReport) {
  const blob = new Blob([JSON.stringify(report, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `agentflow-episode-${report.run.id}.json`;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}
