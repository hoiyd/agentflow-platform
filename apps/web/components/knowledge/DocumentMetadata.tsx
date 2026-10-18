import type { DocumentDetail } from "../../lib/knowledge-api";

export function DocumentMetadata({ label, metadata, version, schema }: {
  label: string;
  metadata: Record<string, unknown>;
  version?: string;
  schema?: DocumentDetail["metadata_schema"];
}) {
  return (
    <details className="document-metadata">
      <summary>{label} · {version || "Legacy / unvalidated"}</summary>
      <table aria-label={label}>
        <thead><tr><th>Field</th><th>Value</th><th>Ownership</th></tr></thead>
        <tbody>
          {Object.entries(metadata).sort(([a], [b]) => a.localeCompare(b)).map(([key, value]) => {
            const field = version && version === schema?.version ? schema.fields.find(field => field.key === key) : undefined;
            const owner = !version ? "Legacy / unvalidated" : field?.owner === "system_derived" ? "System-derived" : field?.owner === "owner_declared" ? "Owner-declared" : "Unverified extension";
            return <tr key={key}><th scope="row">{key}</th><td><code>{JSON.stringify(value)}</code></td><td>{owner}</td></tr>;
          })}
        </tbody>
      </table>
    </details>
  );
}
