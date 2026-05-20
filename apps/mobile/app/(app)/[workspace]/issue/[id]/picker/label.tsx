/**
 * Label picker route for an existing issue — multi-select. Stays open so
 * the user can toggle several labels before dragging the sheet away
 * (no automatic router.back() on each toggle).
 *
 * See ./status.tsx for the self-contained-route rationale.
 */
import { useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { LabelPickerBody } from "@/components/issue/pickers/label-picker-body";
import { issueDetailOptions } from "@/data/queries/issues";
import { useAttachLabel, useDetachLabel } from "@/data/mutations/issues";
import { useWorkspaceStore } from "@/data/workspace-store";

export default function IssueLabelPickerRoute() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { data: issue } = useQuery(issueDetailOptions(wsId, id));
  const attachLabel = useAttachLabel(id);
  const detachLabel = useDetachLabel(id);

  const attached = issue?.labels ?? [];

  return (
    <LabelPickerBody
      attached={attached}
      onAttach={(label) => attachLabel.mutate({ label })}
      onDetach={(labelId) => detachLabel.mutate({ labelId })}
    />
  );
}
