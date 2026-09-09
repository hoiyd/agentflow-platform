import { RunReplay } from "../../../components/run-replay/RunReplay";

export default async function RunReplayPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <RunReplay runId={id} />;
}
