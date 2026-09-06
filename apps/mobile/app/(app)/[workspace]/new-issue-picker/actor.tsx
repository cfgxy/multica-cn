/**
 * Smart-mode actor picker route for the in-progress new-issue draft.
 * See ./assignee.tsx for the iOS-native nav header + UISearchController
 * pattern; the body swaps the member+agent+squad assignee list for the
 * quick-create agent/squad list (`QuickCreateActorPickerBody`).
 */
import { router } from "expo-router";
import { QuickCreateActorPickerBody } from "@/components/issue/pickers/quick-create-actor-picker-body";
import { useNewIssueDraftStore } from "@/data/stores/new-issue-draft-store";
import { useNativeSearchBar } from "@/lib/use-native-search-bar";
import { t } from "i18next";

export default function NewIssueActorPickerRoute() {
  const actor = useNewIssueDraftStore((s) => s.smartActor);
  const setSmartActor = useNewIssueDraftStore((s) => s.setSmartActor);
  const query = useNativeSearchBar(
    t(
      "modals:create_issue.agent.search_placeholder",
      "Search agents and squads…",
    ),
    { autoFocus: true },
  );

  return (
    <QuickCreateActorPickerBody
      value={actor}
      query={query}
      onChange={(next) => {
        setSmartActor(next);
        router.back();
      }}
    />
  );
}
