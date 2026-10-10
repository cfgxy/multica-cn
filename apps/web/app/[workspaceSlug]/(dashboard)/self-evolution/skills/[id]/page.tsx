"use client";

import { use } from "react";
import { SkillEvolutionDetailPage } from "@multica/views/self-evolution";

export default function SkillEvolutionDetailRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <SkillEvolutionDetailPage skillId={id} />;
}
