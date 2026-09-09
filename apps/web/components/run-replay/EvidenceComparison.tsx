"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { GitCompareArrows, X } from "lucide-react";
import type { EpisodeReport, RunInfo, RunReplay } from "../../lib/api";
import { getEpisodeReport, getRunModelRequests, getRunReplay, listRuns } from "../../lib/api";
import {
  compareEvidence,
  evidenceDelta,
  type EvidenceComparison as Comparison,
  type EvidenceIdentity,
  type EvidenceMetric
} from "../../lib/evidence-comparison";

type Props = {
  currentReplay: RunReplay;
  currentReport: EpisodeReport;
  onClose: () => void;
};

export function EvidenceComparison({ currentReplay, currentReport, onClose }: Props) {
  const [runs, setRuns] = useState<RunInfo[]>([]);
  const [baselineRunId, setBaselineRunId] = useState("");
  const [comparison, setComparison] = useState<Comparison | null>(null);
  const [loadingRuns, setLoadingRuns] = useState(true);
  const [comparing, setComparing] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    listRuns(controller.signal)
      .then((items) => {
        const candidates = items.filter((run) => run.id !== currentReplay.run.id);
        setRuns(candidates);
        setBaselineRunId((current) => current || candidates[0]?.id || "");
      })
      .catch((err) => {
        if (!controller.signal.aborted) setError(errorMessage(err, "Failed to load comparison runs"));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoadingRuns(false);
      });
    return () => controller.abort();
  }, [currentReplay.run.id]);

  async function runComparison() {
    if (!baselineRunId || comparing) return;
    setComparing(true);
    setComparison(null);
    setError("");
    try {
      const [baselineReplay, baselineReport, baselineRequests, currentRequests] = await Promise.all([
        getRunReplay(baselineRunId),
        getEpisodeReport(baselineRunId),
        getRunModelRequests(baselineRunId),
        getRunModelRequests(currentReplay.run.id)
      ]);
      setComparison(compareEvidence(
        { replay: currentReplay, report: currentReport, modelRequests: currentRequests },
        { replay: baselineReplay, report: baselineReport, modelRequests: baselineRequests }
      ));
    } catch (err) {
      setError(errorMessage(err, "Failed to compare runs"));
    } finally {
      setComparing(false);
    }
  }

  return (
    <section className="evidence-comparison" aria-labelledby="evidence-comparison-title">
      <div className="evidence-comparison-header">
        <div>
          <div className="panel-title inline" id="evidence-comparison-title">Evidence comparison</div>
          <p>Compare this Run with one baseline from the current Workspace.</p>
        </div>
        <button aria-label="Close comparison" className="evidence-comparison-close" onClick={onClose} title="Close comparison" type="button">
          <X aria-hidden="true" size={16} />
        </button>
      </div>

      <div className="evidence-comparison-controls">
        <label>
          <span>Baseline run</span>
          <select
            disabled={loadingRuns || runs.length === 0}
            onChange={(event) => {
              setBaselineRunId(event.target.value);
              setComparison(null);
            }}
            value={baselineRunId}
          >
            {runs.length === 0 ? <option value="">No other runs available</option> : null}
            {runs.map((run) => (
              <option key={run.id} value={run.id}>{runOption(run)}</option>
            ))}
          </select>
        </label>
        <button className="run-link evidence-compare-action" disabled={!baselineRunId || comparing} onClick={runComparison} type="button">
          <GitCompareArrows aria-hidden="true" size={15} />
          {comparing ? "Comparing..." : "Compare evidence"}
        </button>
      </div>

      {error ? <div className="evidence-comparison-error" role="alert">{error}</div> : null}
      {comparison ? <ComparisonResult comparison={comparison} /> : null}
    </section>
  );
}

function ComparisonResult({ comparison }: { comparison: Comparison }) {
  const statusText = comparison.comparable
    ? comparison.mode === "single_variable"
      ? `Comparable · single variable: ${comparison.changedVariable}`
      : "Comparable · identical runtime configuration"
    : "Side-by-side only · deltas are disabled";
  return (
    <div className="evidence-comparison-result">
      <div className={`evidence-comparison-status ${comparison.comparable ? "comparable" : "incomparable"}`}>
        <strong>{statusText}</strong>
        {comparison.reasons.length > 0 ? <span>{comparison.reasons.join("; ")}</span> : null}
      </div>

      <ComparisonTable comparison={comparison} />

      <div className="evidence-comparison-output">
        <EvidenceColumn label="Baseline output" runId={comparison.baseline.runId} output={comparison.baseline.finalOutput} refs={comparison.baseline.evidenceRefs} />
        <EvidenceColumn label="Current output" runId={comparison.current.runId} output={comparison.current.finalOutput} refs={comparison.current.evidenceRefs} />
      </div>
      <p className="evidence-comparison-caveat">Deltas are current minus baseline. A two-Run comparison is diagnostic evidence, not a multi-trial quality result.</p>
    </div>
  );
}

function ComparisonTable({ comparison }: { comparison: Comparison }) {
  const identityRows: Array<[string, keyof EvidenceIdentity]> = [
    ["Task", "task"], ["Materials", "materials"], ["Mode", "mode"], ["Agent", "agent"],
    ["Model", "model"], ["Snapshot hash", "configHash"], ["Experiment ID", "experimentId"]
  ];
  const metricRows: Array<[string, EvidenceMetric, "count" | "duration"]> = [
    ["Total tokens", "totalTokens", "count"], ["Model calls", "modelCalls", "count"],
    ["Tool calls", "toolCalls", "count"], ["Duration", "durationMS", "duration"], ["Errors", "errors", "count"]
  ];
  const textRows: Array<[string, string | null, string | null]> = [
    ["Run status", comparison.baseline.status, comparison.current.status],
    ["Verification", comparison.baseline.verification, comparison.current.verification],
    ["Stop reason", comparison.baseline.stopReason, comparison.current.stopReason]
  ];
  return (
    <table className="evidence-comparison-table">
      <thead>
        <tr>
          <th scope="col">Signal</th>
          <th scope="col"><RunLink label="Baseline" runId={comparison.baseline.runId} /></th>
          <th scope="col"><RunLink label="Current" runId={comparison.current.runId} /></th>
          <th scope="col">Delta</th>
        </tr>
      </thead>
      <tbody>
        {identityRows.map(([label, key]) => (
          <ComparisonRow key={key} label={label} baseline={formatIdentity(comparison.baseline.identity[key])} current={formatIdentity(comparison.current.identity[key])} />
        ))}
        {textRows.map(([label, baseline, current]) => (
          <ComparisonRow key={label} label={label} baseline={baseline ?? "Unknown"} current={current ?? "Unknown"} />
        ))}
        {metricRows.map(([label, metric, format]) => (
          <ComparisonRow
            key={metric}
            label={label}
            baseline={formatMetric(comparison.baseline.metrics[metric], format)}
            current={formatMetric(comparison.current.metrics[metric], format)}
            delta={formatDelta(evidenceDelta(comparison, metric), format, comparison.comparable)}
          />
        ))}
      </tbody>
    </table>
  );
}

function ComparisonRow({ label, baseline, current, delta = "—" }: { label: string; baseline: string; current: string; delta?: string }) {
  return <tr><th scope="row">{label}</th><td>{baseline}</td><td>{current}</td><td>{delta}</td></tr>;
}

function RunLink({ label, runId }: { label: string; runId: string }) {
  return <Link href={`/runs/${encodeURIComponent(runId)}`}>{label}<span>{shortId(runId)}</span></Link>;
}

function EvidenceColumn({ label, runId, output, refs }: { label: string; runId: string; output: string | null; refs: string[] }) {
  return (
    <div>
      <div className="evidence-column-title"><span>{label}</span><code>{shortId(runId)}</code></div>
      <p>{truncate(output ?? "Unknown", 420)}</p>
      <div className="evidence-reference-title">Context and artifact references</div>
      {refs.length > 0 ? <ul>{refs.map((ref) => <li key={ref}><code>{ref}</code></li>)}</ul> : <span className="evidence-reference-empty">No references recorded.</span>}
    </div>
  );
}

function formatIdentity(value: string | string[] | null) {
  if (value === null) return "Unknown";
  if (Array.isArray(value)) return value.length === 0 ? "None recorded" : truncate(value.join(", "), 140);
  return truncate(value, 140);
}

function formatMetric(value: number | null, format: "count" | "duration") {
  if (value === null) return "Unknown";
  if (format === "duration") return value >= 1000 ? `${(value / 1000).toFixed(2)} s` : `${value} ms`;
  return new Intl.NumberFormat("en-US").format(value);
}

function formatDelta(value: number | null, format: "count" | "duration", comparable: boolean) {
  if (!comparable) return "Not calculated";
  if (value === null) return "Unknown";
  const prefix = value > 0 ? "+" : value < 0 ? "−" : "";
  return `${prefix}${formatMetric(Math.abs(value), format)}`;
}

function runOption(run: RunInfo) {
  const time = run.completed_at ?? run.updated_at ?? run.created_at;
  const date = time ? new Date(time).toLocaleString() : "Unknown time";
  return `${shortId(run.id)} · ${run.status} · ${date}`;
}

function shortId(value: string) {
  return value.length > 16 ? `${value.slice(0, 8)}…${value.slice(-5)}` : value;
}

function truncate(value: string, maxLength: number) {
  return value.length > maxLength ? `${value.slice(0, maxLength - 1)}…` : value;
}

function errorMessage(value: unknown, fallback: string) {
  return value instanceof Error ? value.message : fallback;
}
