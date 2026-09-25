"use client";

import Link from "next/link";
import { ArrowLeft, FlaskConical } from "lucide-react";

import { KnowledgeEvaluation } from "../../../components/knowledge/KnowledgeEvaluation";
import { useKnowledgeEvaluation } from "../../../components/knowledge/useKnowledgeEvaluation";

export default function RetrievalEvaluationPage() {
  const evaluation = useKnowledgeEvaluation();

  return (
    <main className="evaluation-page">
      <header className="evaluation-header">
        <Link className="back-link" href="/workspace?view=knowledge">
          <ArrowLeft aria-hidden="true" size={14} /> Back to Knowledge
        </Link>
        <div className="evaluation-heading">
          <FlaskConical aria-hidden="true" size={22} />
          <div>
            <span>Evaluation</span>
            <h1>Retrieval evaluation</h1>
          </div>
        </div>
      </header>
      <KnowledgeEvaluation model={evaluation} />
    </main>
  );
}
