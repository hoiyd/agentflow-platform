import Link from "next/link";
import { ArrowLeft, FlaskConical } from "lucide-react";

import { EvidenceComparison } from "../../../components/evaluation/EvidenceComparison";

export default async function RunComparisonPage({
  searchParams
}: {
  searchParams?: Promise<{ run?: string }>;
}) {
  const params = await searchParams;
  return (
    <main className="evaluation-page">
      <header className="evaluation-header">
        <Link className="back-link" href="/workspace">
          <ArrowLeft aria-hidden="true" size={14} />
          Back to workspace
        </Link>
        <div className="evaluation-heading">
          <FlaskConical aria-hidden="true" size={22} />
          <div>
            <span>Evaluation</span>
            <h1>Run comparison</h1>
            <p>Inspect outcomes from repeated or single-variable test runs. Uncontrolled pairs remain available for review without misleading deltas.</p>
          </div>
        </div>
      </header>
      <EvidenceComparison initialCurrentRunId={params?.run ?? ""} />
    </main>
  );
}
