import { getEpisodeReport, getRunReplay, type EpisodeReport, type RunReplay } from "./api.ts";

export type ReplayPageData = {
  data: RunReplay;
  report: EpisodeReport | null;
  reportError: string;
};

export async function getReplayPageData(runId: string): Promise<ReplayPageData> {
  const [replayResult, reportResult] = await Promise.allSettled([getRunReplay(runId), getEpisodeReport(runId)]);
  if (replayResult.status === "rejected") throw replayResult.reason;
  return {
    data: replayResult.value,
    report: reportResult.status === "fulfilled" ? reportResult.value : null,
    reportError: reportResult.status === "rejected" ? errorMessage(reportResult.reason) : ""
  };
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "Failed to load episode report";
}
