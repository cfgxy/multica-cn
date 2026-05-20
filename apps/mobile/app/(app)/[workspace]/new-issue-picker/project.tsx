/**
 * Project picker route for the in-progress new-issue draft. See ./status.tsx.
 */
import { router } from "expo-router";
import { ProjectPickerBody } from "@/components/issue/pickers/project-picker-body";
import { useNewIssueDraftStore } from "@/data/stores/new-issue-draft-store";

export default function NewIssueProjectPickerRoute() {
  const project = useNewIssueDraftStore((s) => s.project);
  const setProject = useNewIssueDraftStore((s) => s.setProject);

  return (
    <ProjectPickerBody
      value={project}
      onChange={(next) => {
        setProject(next);
        router.back();
      }}
    />
  );
}
