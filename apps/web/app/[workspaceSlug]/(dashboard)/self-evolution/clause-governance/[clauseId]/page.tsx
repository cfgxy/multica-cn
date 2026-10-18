import { ClauseGovernanceDetailPage } from "@multica/views/self-evolution";

export default async function ClauseGovernanceDetailRoute({
  params,
}: {
  params: Promise<{ clauseId: string }>;
}) {
  const { clauseId } = await params;
  return <ClauseGovernanceDetailPage clauseId={clauseId} />;
}
