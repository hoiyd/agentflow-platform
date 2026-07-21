import type { RunEvent, RunUsageLedger } from "../lib/api";

type ResourceUsageRow = {
  key: string;
  label: string;
  used: number;
  limit: number;
  format: (value: number) => string;
};

type RunUsagePanelProps = {
  ledger: RunUsageLedger;
  activeRuntimeMS: number;
  events: RunEvent[];
};

export function RunUsagePanel({ ledger, activeRuntimeMS, events }: RunUsagePanelProps) {
  const rows: ResourceUsageRow[] = [
    usageRow("model_calls", "Model calls", ledger.totals.model_calls, ledger.budget.max_model_calls, formatCount),
    usageRow("prompt_tokens", "Prompt tokens", ledger.totals.prompt_tokens, ledger.budget.max_prompt_tokens, formatCount),
    usageRow(
      "completion_tokens",
      "Completion tokens",
      ledger.totals.completion_tokens,
      ledger.budget.max_completion_tokens,
      formatCount
    ),
    usageRow("total_tokens", "Total tokens", ledger.totals.total_tokens, ledger.budget.max_total_tokens, formatCount),
    usageRow("tool_calls", "Tool calls", ledger.totals.tool_calls, ledger.budget.max_tool_calls, formatCount),
    usageRow("runtime_ms", "Active runtime", activeRuntimeMS, ledger.budget.max_runtime_ms, formatResourceDuration),
    usageRow(
      "estimated_cost_micros",
      "Estimated cost",
      ledger.totals.estimated_cost_micros,
      ledger.budget.max_estimated_cost_micros,
      formatMicrodollars
    )
  ];
  const hasLedger = ledger.entries.length > 0 || Boolean(ledger.updated_at) || rows.some((row) => row.limit > 0);
  const exceededEvent = [...events].reverse().find((event) => event.type === "budget.exceeded");

  return (
    <section className="usage-ledger" aria-labelledby="resource-usage-title">
      <div className="usage-ledger-header">
        <div>
          <div className="panel-title inline" id="resource-usage-title">
            Resource usage
          </div>
          <div className="usage-ledger-meta">
            <span>{hasLedger ? "Frozen budget" : "Legacy run"}</span>
            <span>{ledger.entries.length} ledger entries</span>
            <span className={ledger.totals.open_reservations > 0 ? "active" : ""}>
              {ledger.totals.open_reservations} open reservations
            </span>
          </div>
        </div>
        {ledger.updated_at ? (
          <time dateTime={ledger.updated_at}>Updated {new Date(ledger.updated_at).toLocaleString()}</time>
        ) : null}
      </div>

      {exceededEvent ? <BudgetExceededNotice event={exceededEvent} /> : null}

      {!hasLedger ? (
        <div className="usage-ledger-empty">No usage ledger was recorded for this legacy run.</div>
      ) : (
        <table className="usage-ledger-table">
          <thead>
            <tr>
              <th scope="col">Resource</th>
              <th scope="col">Used</th>
              <th scope="col">Limit</th>
              <th scope="col">Remaining</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <ResourceUsageTableRow key={row.key} row={row} />
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

function ResourceUsageTableRow({ row }: { row: ResourceUsageRow }) {
  const percent = row.limit > 0 ? Math.min(100, (row.used / row.limit) * 100) : 0;
  const tone = row.limit > 0 && row.used > row.limit ? "exceeded" : row.limit > 0 && percent >= 80 ? "near-limit" : "";
  return (
    <tr className={tone}>
      <th scope="row">{row.label}</th>
      <td>
        <strong>{row.format(row.used)}</strong>
        {row.limit > 0 ? (
          <span className="usage-meter" aria-label={`${Math.round((row.used / row.limit) * 100)} percent used`}>
            <span style={{ width: `${percent}%` }} />
          </span>
        ) : null}
      </td>
      <td>{row.limit > 0 ? row.format(row.limit) : "No limit"}</td>
      <td>{row.limit > 0 ? row.format(Math.max(0, row.limit - row.used)) : "No limit"}</td>
    </tr>
  );
}

function BudgetExceededNotice({ event }: { event: RunEvent }) {
  const payload = event.payload ?? {};
  const resource = stringPayload(payload, "resource") ?? "resource";
  const used = numericPayload(payload, "used");
  const limit = numericPayload(payload, "limit");
  const requested = numericPayload(payload, "requested");
  return (
    <div className="usage-budget-alert" role="status">
      <strong>Budget exceeded</strong>
      <span>{resourceLabel(resource)}</span>
      <span>{formatBudgetValue(resource, used)} used / {formatBudgetValue(resource, limit)} limit</span>
      {requested > 0 ? <span>{formatBudgetValue(resource, requested)} requested</span> : null}
    </div>
  );
}

export function BudgetEventDetail({ payload }: { payload: Record<string, unknown> }) {
  const resource = stringPayload(payload, "resource") ?? "resource";
  const purpose = stringPayload(payload, "purpose");
  const format = (value: number) => formatBudgetValue(resource, value);
  return (
    <section className="budget-event-summary" aria-label="Budget event details">
      <BudgetDetail label="Resource" value={resourceLabel(resource)} />
      <BudgetDetail label="Used" value={format(numericPayload(payload, "used"))} />
      <BudgetDetail label="Limit" value={format(numericPayload(payload, "limit"))} />
      <BudgetDetail label="Requested" value={format(numericPayload(payload, "requested"))} />
      {purpose ? <BudgetDetail label="Purpose" value={purpose} /> : null}
    </section>
  );
}

function BudgetDetail({ label, value }: { label: string; value: string }) {
  return (
    <div className="detail-kv">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function usageRow(
  key: string,
  label: string,
  used: number,
  limit: number | undefined,
  format: (value: number) => string
): ResourceUsageRow {
  return { key, label, used, limit: limit ?? 0, format };
}

function stringPayload(payload: Record<string, unknown>, key: string) {
  return typeof payload[key] === "string" ? payload[key] : undefined;
}

function numericPayload(payload: Record<string, unknown>, key: string) {
  const value = payload[key];
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function formatCount(value: number) {
  return new Intl.NumberFormat("en-US", { maximumFractionDigits: 0 }).format(Math.max(0, value));
}

function formatResourceDuration(durationMS: number) {
  if (durationMS >= 60_000) {
    const minutes = durationMS / 60_000;
    return `${minutes.toFixed(Number.isInteger(minutes) ? 0 : 1)} min`;
  }
  if (durationMS >= 1000) {
    return `${(durationMS / 1000).toFixed(durationMS % 1000 === 0 ? 0 : 1)} s`;
  }
  return `${Math.max(0, durationMS)} ms`;
}

function formatMicrodollars(value: number) {
  const dollars = Math.max(0, value) / 1_000_000;
  return `$${dollars.toFixed(dollars < 0.01 ? 6 : 2)}`;
}

function resourceLabel(resource: string) {
  const labels: Record<string, string> = {
    model_calls: "Model calls",
    prompt_tokens: "Prompt tokens",
    completion_tokens: "Completion tokens",
    total_tokens: "Total tokens",
    tool_calls: "Tool calls",
    runtime_ms: "Active runtime",
    estimated_cost_micros: "Estimated cost"
  };
  return labels[resource] ?? resource.replaceAll("_", " ");
}

function formatBudgetValue(resource: string, value: number) {
  if (resource === "runtime_ms") {
    return formatResourceDuration(value);
  }
  if (resource === "estimated_cost_micros") {
    return formatMicrodollars(value);
  }
  return formatCount(value);
}
