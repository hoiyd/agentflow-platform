import type { EpisodeReport } from "../../lib/api";

export function EpisodeReportPanel({ report }: { report: EpisodeReport }) {
  const verificationTone =
    report.verification.status === "passed"
      ? "passed"
      : report.verification.status === "failed"
        ? "failed"
        : "needs-review";
  return (
    <section className="episode-report" id="run-verification-evidence">
      <div className="episode-report-header">
        <div>
          <div className="panel-title inline">Episode report</div>
          <p>
            {report.agent.name} captured {report.steps.length} steps, {report.llm_calls.length} LLM calls,{" "}
            {report.tool_calls.length} tool calls.
          </p>
        </div>
        <button className="run-link" onClick={() => exportEpisodeJSON(report)} type="button">
          Export JSON
        </button>
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
    </section>
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
