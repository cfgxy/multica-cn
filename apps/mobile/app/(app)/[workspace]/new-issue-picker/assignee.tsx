/**
 * Assignee picker route for the in-progress new-issue draft. See ./status.tsx.
 */
import { router } from "expo-router";
import { AssigneePickerBody } from "@/components/issue/pickers/assignee-picker-body";
import { useNewIssueDraftStore } from "@/data/stores/new-issue-draft-store";

export default function NewIssueAssigneePickerRoute() {
  const assignee = useNewIssueDraftStore((s) => s.assignee);
  const setAssignee = useNewIssueDraftStore((s) => s.setAssignee);

  return (
    <AssigneePickerBody
      value={assignee}
      onChange={(next) => {
        setAssignee(next);
        router.back();
      }}
    />
  );
}
