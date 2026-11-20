import { Download } from "lucide-react";
import type { RunReplay } from "../../lib/api";
import { replayCandidateMessage } from "../../lib/run-replay-normalization";

export function RunReplayExport({ replay }: { replay: RunReplay }) {
  function download() {
    const answer = replayCandidateMessage(replay);
    // Replay carries Conversation history; the download intentionally does not.
    const evidence = {
      run: replay.run,
      runtime_snapshot: replay.runtime_snapshot,
      candidate: answer ? replay.candidate : undefined,
      messages: answer ? [answer] : [],
      summary: replay.summary,
      steps: replay.steps,
      usage_ledger: replay.usage_ledger,
      run_events: replay.run_events,
      tool_artifacts: replay.tool_artifacts,
      verification_evidence: replay.verification_evidence,
      verification_artifacts: replay.verification_artifacts,
      structured_result: replay.structured_result
    };
    const url = URL.createObjectURL(new Blob([JSON.stringify(evidence, null, 2)], { type: "application/json" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = `agentflow-run-${replay.run.id}.json`;
    link.click();
    URL.revokeObjectURL(url);
  }

  return <button className="run-link" onClick={download} type="button">
    <Download aria-hidden="true" size={15} /> Export Run JSON
  </button>;
}
