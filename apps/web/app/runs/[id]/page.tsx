import { RunReplay } from "../../../components/run-replay/RunReplay";

export default async function RunReplayPage({ params, searchParams }: {
  params: Promise<{ id: string }>;
  searchParams: Promise<{ event?: string }>;
}) {
  const { id } = await params;
  const { event } = await searchParams;
  return <RunReplay initialEventId={event} runId={id} />;
}
