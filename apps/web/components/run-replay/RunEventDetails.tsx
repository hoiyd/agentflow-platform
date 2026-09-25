import type { RunEvent } from "../../lib/api";
import { RetrievedContext, RetrievalMetadata, stringPayload } from "./RunRetrievalDetails";
import { BudgetEventDetail } from "./RunUsagePanel";

export function Metric({ label, value, tone = "" }: { label: string; value: string; tone?: string }) {
  return (
    <div className={`metric ${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

export function EventDetail({ event }: { event: RunEvent }) {
  const payload = event.payload ?? {};
  const parameters = payload.parameters && typeof payload.parameters === "object" && !Array.isArray(payload.parameters)
    ? payload.parameters as Record<string, unknown> : null;
  const sampling = parameters && typeof parameters.temperature === "number"
    ? [
        `Temperature ${parameters.temperature}`,
        typeof parameters.top_p === "number" ? `Top-p ${parameters.top_p}` : "Top-p provider default",
        ...(typeof parameters.seed === "number" ? [`Seed ${parameters.seed}`] : [])
      ].join(" · ") : null;
  const isEstimated = payload.token_usage_estimated === true || payload.usage_estimated === true;
  return (
    <div className="event-detail">
      <div className="detail-kv">
        <span>Type</span>
        <strong>{event.type}</strong>
      </div>
      <div className="detail-kv">
        <span>Timestamp</span>
        <strong>{new Date(event.timestamp).toLocaleString()}</strong>
      </div>
	  {payload.simulated === true ? (
		<div className="detail-kv"><span>Model source</span><strong>Offline simulation</strong></div>
	  ) : null}
	  {event.type === "model.request_prepared" && sampling ? (
		<div className="detail-kv"><span>Sampling</span><strong>{sampling}</strong></div>
	  ) : null}
	  {event.type === "model.attempt_finished" ? (
		<>
		  <div className="detail-kv"><span>Attempt</span><strong>{String(payload.attempt ?? "Unknown")}</strong></div>
		  <div className="detail-kv"><span>Attempt status</span><strong>{stringPayload(payload, "status") || "Unknown"}</strong></div>
		  {stringPayload(payload, "finish_reason") ? (
			<div className="detail-kv"><span>Generation finish</span><strong>{stringPayload(payload, "finish_reason") === "missing" ? "Unconfirmed (not provided)" : stringPayload(payload, "finish_reason")}</strong></div>
		  ) : null}
		  {typeof payload.time_to_first_token_ms === "number" ? (
			<div className="detail-kv"><span>Attempt first token</span><strong>{formatDuration(payload.time_to_first_token_ms)}</strong></div>
		  ) : null}
		  {typeof payload.rate_limit_wait_ms === "number" ? (
			<div className="detail-kv"><span>Local rate wait</span><strong>{formatDuration(payload.rate_limit_wait_ms)}</strong></div>
		  ) : null}
		  {typeof payload.model_permit_wait_ms === "number" ? (
			<div className="detail-kv"><span>Model permit wait</span><strong>{formatDuration(payload.model_permit_wait_ms)}</strong></div>
		  ) : null}
		  {typeof payload.http_duration_ms === "number" ? (
			<div className="detail-kv"><span>HTTP + stream</span><strong>{formatDuration(payload.http_duration_ms)}</strong></div>
		  ) : null}
		  {typeof payload.http_time_to_first_token_ms === "number" ? (
			<div className="detail-kv"><span>HTTP first token</span><strong>{formatDuration(payload.http_time_to_first_token_ms)}</strong></div>
		  ) : null}
		  {typeof payload.output_tokens_per_second === "number" && payload.output_tokens_per_second > 0 ? (
			<div className="detail-kv"><span>Output rate</span><strong>{payload.output_tokens_per_second.toFixed(1)} tok/s</strong></div>
		  ) : null}
		  {stringPayload(payload, "error_kind") ? (
			<div className="detail-kv"><span>Error kind</span><strong>{stringPayload(payload, "error_kind")}</strong></div>
		  ) : null}
		</>
	  ) : null}
	  {eventDuration(event) ? (
        <div className="detail-kv">
          <span>Duration</span>
		  <strong>{formatDuration(eventDuration(event))}</strong>
        </div>
      ) : null}
      {stringPayload(payload, "executor") ? (
        <div className="detail-kv">
          <span>Executor</span>
          <strong>{stringPayload(payload, "executor")}</strong>
        </div>
      ) : null}
      {stringPayload(payload, "framework") ? (
        <div className="detail-kv">
          <span>Framework</span>
          <strong>{stringPayload(payload, "framework")}</strong>
        </div>
      ) : null}
      {stringPayload(payload, "agent_name") || stringPayload(payload, "agent_id") ? (
        <div className="detail-kv">
          <span>Agent</span>
          <strong>{stringPayload(payload, "agent_name") || stringPayload(payload, "agent_id")}</strong>
        </div>
      ) : null}
      {"memory_enabled" in payload ? (
        <div className="detail-kv">
          <span>Memory</span>
          <strong>{payload.memory_enabled === false ? "Disabled" : "Enabled"}</strong>
        </div>
      ) : null}
      {"retrieval_enabled" in payload ? (
        <div className="detail-kv">
          <span>Knowledge</span>
          <strong>{payload.retrieval_enabled === false ? "Disabled" : "Enabled"}</strong>
        </div>
      ) : null}
      <RetrievalMetadata payload={payload} />
      {Array.isArray(payload.configured_tools) ? (
        <div className="detail-kv">
          <span>Tools</span>
          <strong>{payload.configured_tools.length > 0 ? payload.configured_tools.join(", ") : "None"}</strong>
        </div>
      ) : null}
      {payload.usage_available !== false && ("prompt_tokens" in payload || "completion_tokens" in payload || "total_tokens" in payload) ? (
        <div className="token-strip">
          <Metric label="Prompt" value={formatTokenValue(payload.prompt_tokens, isEstimated)} />
          <Metric label="Completion" value={formatTokenValue(payload.completion_tokens, isEstimated)} />
          <Metric label="Total" value={formatTokenValue(payload.total_tokens, isEstimated)} />
        </div>
      ) : null}
      {event.type === "budget.exceeded" ? <BudgetEventDetail payload={payload} /> : null}
      <RetrievedContext payload={payload} />
      <section className="raw-json-panel">
        <div className="raw-json-title">
          <span>Raw event payload</span>
          <small>Full trace JSON</small>
        </div>
        <pre>{JSON.stringify(payload, null, 2)}</pre>
      </section>
    </div>
  );
}

export function formatTokenValue(value: unknown, estimated: boolean) {
  const numberValue = typeof value === "number" ? value : Number(value ?? 0);
  return `${Number.isFinite(numberValue) ? numberValue : 0}${estimated ? " est." : ""}`;
}

export function stepDuration(events: RunEvent[], stepId: string) {
  return events
	  .filter((event) => event.stage_id === stepId && event.type !== "model.attempt_finished")
	  .reduce((total, event) => total + eventDuration(event), 0);
}

export function eventDuration(event: RunEvent): number {
  return typeof event.payload.duration_ms === "number" ? event.payload.duration_ms : 0;
}

export function formatDuration(durationMS: number) {
  if (!durationMS) {
    return "0 ms";
  }
  if (durationMS < 1000) {
    return `${durationMS} ms`;
  }
  return `${(durationMS / 1000).toFixed(2)} s`;
}
