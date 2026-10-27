/**
 * Stub route. The More tab button in (tabs)/_layout.tsx opens its dropdown
 * directly instead of navigating here. Expo Router still requires a file
 * at this path to register the Tabs.Screen entry.
 *
 * If a deep link or stale tab state somehow lands the user here, bounce
 * to inbox so they don't see a blank screen.
 */
import { Redirect } from "expo-router";
import { useWorkspaceStore } from "@/data/workspace-store";

export default function MoreStub() {
  const slug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  return <Redirect href={slug ? `/${slug}/inbox` : "/select-workspace"} />;
}
