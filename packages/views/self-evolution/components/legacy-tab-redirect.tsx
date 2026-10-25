"use client";

import { useEffect } from "react";
import { useWorkspacePaths } from "@multica/core/paths";
import { useNavigation } from "../../navigation";

export function LegacySelfEvolutionTabRedirect({
  destination,
}: {
  destination: "versions" | "quiz" | "proposals";
}) {
  const navigation = useNavigation();
  const paths = useWorkspacePaths();
  const search = navigation.searchParams.toString();
  const hash = navigation.hash;
  const base = destination === "proposals"
    ? paths.selfEvolutionClauseGovernance()
    : paths.selfEvolutionQuality();

  useEffect(() => {
    const params = new URLSearchParams(search);
    params.set("tab", destination);
    navigation.replace(`${base}?${params.toString()}${hash}`);
  }, [base, destination, hash, navigation, search]);

  return null;
}
