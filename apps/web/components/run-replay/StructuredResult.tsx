import type { components } from "../../lib/api-contract.gen";

export function StructuredResult({ result }: { result?: components["schemas"]["StructuredRunResult"] }) {
  if (!result) return null;
  return (
    <section className="episode-report" aria-label="Structured result">
      <div className="panel-title">Structured result · {result.mode} · Verification {result.verification_status}</div>
      {result.value ? (
        <details>
          <summary>Verified JSON · {result.verifier_id}</summary>
          <pre className="structured-result-json">{JSON.stringify(result.value, null, 2)}</pre>
        </details>
      ) : <p className="muted">No verified object available: {result.unavailable_reason?.replaceAll("_", " ") ?? result.status}.</p>}
    </section>
  );
}
