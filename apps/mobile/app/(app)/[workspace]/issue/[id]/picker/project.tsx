/**
 * Project picker route for an existing issue. See ./status.tsx for the
 * self-contained-route rationale.
 */
import { useMemo } from "react";
import { useLocalSearchParams, router } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { ProjectPickerBody } from "@/components/issue/pickers/project-picker-body";
import { issueDetailOptions } from "@/data/queries/issues";
import { findProject, projectListOptions } from "@/data/queries/projects";
import { useUpdateIssue } from "@/data/mutations/issues";
import { useWorkspaceStore } from "@/data/workspace-store";

export default function IssueProjectPickerRoute() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { data: issue } = useQuery(issueDetailOptions(wsId, id));
  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const updateIssue = useUpdateIssue(id);

  const project = useMemo(
    () => findProject(projects, issue?.project_id ?? null),
    [projects, issue?.project_id],
  );

  return (
    <ProjectPickerBody
      value={project ?? null}
      onChange={(next) => {
        updateIssue.mutate({ project_id: next?.id ?? null });
        router.back();
      }}
    />
  );
}
