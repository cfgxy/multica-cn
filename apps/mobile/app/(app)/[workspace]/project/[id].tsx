/**
 * Project detail screen. Single column, scrolling:
 *
 *   Header card (icon + title + description, tap → edit)
 *   Properties section (Status / Priority / Lead — tap chip → picker)
 *   Resources section (read-only by default, "Add" button → resource form)
 *   Related issues (Open / Done bucketed list)
 *
 * Per-record realtime: `useProjectRealtime(id, onDeleted=back)` subscribes
 * to `project:updated` (full replace) and `project:deleted` (pop back).
 *
 * Right-top "…" menu (ActionSheetIOS) → Edit / Delete. Delete asks for
 * confirmation via `Alert.alert` per iOS HIG (destructive actions need
 * a second tap).
 */
import { useCallback, useState } from "react";
import {
  ActionSheetIOS,
  ActivityIndicator,
  Alert,
  Linking,
  Platform,
  Pressable,
  RefreshControl,
  ScrollView,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Stack, router, useLocalSearchParams } from "expo-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Ionicons } from "@expo/vector-icons";
import type {
  CreateProjectResourceRequest,
  ProjectPriority,
  ProjectStatus,
} from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { ProjectHeaderCard } from "@/components/project/project-header-card";
import { ProjectPropertiesSection } from "@/components/project/project-properties-section";
import { ProjectRelatedIssues } from "@/components/project/project-related-issues";
import { ProjectResourcesSection } from "@/components/project/project-resources-section";
import { ProjectStatusPickerSheet } from "@/components/project/pickers/project-status-picker-sheet";
import { ProjectPriorityPickerSheet } from "@/components/project/pickers/project-priority-picker-sheet";
import {
  ProjectLeadPickerSheet,
  type LeadValue,
} from "@/components/project/pickers/project-lead-picker-sheet";
import { AddResourceSheet } from "@/components/project/add-resource-sheet";
import {
  projectDetailOptions,
  projectResourcesOptions,
} from "@/data/queries/projects";
import { issueKeys } from "@/data/queries/issue-keys";
import {
  useCreateProjectResource,
  useDeleteProject,
  useUpdateProject,
} from "@/data/mutations/projects";
import { useProjectRealtime } from "@/data/realtime/use-project-realtime";
import { useWorkspaceStore } from "@/data/workspace-store";

export default function ProjectDetail() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const qc = useQueryClient();

  const detail = useQuery(projectDetailOptions(wsId, id));
  const updateProject = useUpdateProject(id);
  const deleteProject = useDeleteProject(id);
  const createResource = useCreateProjectResource(id);

  const [statusOpen, setStatusOpen] = useState(false);
  const [priorityOpen, setPriorityOpen] = useState(false);
  const [leadOpen, setLeadOpen] = useState(false);
  const [resourceOpen, setResourceOpen] = useState(false);

  // Per-record realtime — when another client deletes the project we're
  // viewing, pop back so the user isn't stranded on a 404.
  useProjectRealtime(id, () => router.back());

  const onRefresh = useCallback(async () => {
    await Promise.all([
      detail.refetch(),
      qc.invalidateQueries({ queryKey: projectResourcesOptions(wsId, id).queryKey }),
      qc.invalidateQueries({
        queryKey: [...issueKeys.list(wsId), "byProject", id],
      }),
    ]);
  }, [detail, qc, wsId, id]);

  const project = detail.data;

  // EMPTY_PROJECT carries an empty id — parseWithFallback returned the
  // fallback because the response shape drifted. Treat as "not found".
  const projectMissing = !project || project.id === "";

  const onPressMore = () => {
    if (!project) return;
    const wsUrl = process.env.EXPO_PUBLIC_WEB_URL;
    const options = [
      "Cancel",
      "Edit details",
      ...(wsUrl ? ["Open on web"] : []),
      "Delete",
    ];
    const destructiveIndex = options.length - 1;
    ActionSheetIOS.showActionSheetWithOptions(
      {
        options,
        cancelButtonIndex: 0,
        destructiveButtonIndex: destructiveIndex,
      },
      (i) => {
        if (i === 1) {
          if (wsSlug) router.push(`/${wsSlug}/project/${id}/edit`);
          return;
        }
        if (wsUrl && i === 2) {
          Linking.openURL(`${wsUrl}/${wsSlug}/projects/${id}`);
          return;
        }
        if (i === destructiveIndex) {
          onDelete();
        }
      },
    );
  };

  const onDelete = () => {
    Alert.alert(
      "Delete project?",
      "This cannot be undone. Issues in this project will become unassigned from any project.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Delete",
          style: "destructive",
          onPress: () => {
            deleteProject.mutate(undefined, {
              onSuccess: () => router.back(),
            });
          },
        },
      ],
    );
  };

  const onAddResource = (body: CreateProjectResourceRequest) => {
    createResource.mutate(body, {
      onSuccess: () => setResourceOpen(false),
      onError: (err) => {
        Alert.alert(
          "Failed to attach resource",
          err instanceof Error ? err.message : "Unknown error",
        );
      },
    });
  };

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
      <Stack.Screen
        options={{
          title: project?.title || "Project",
          headerBackTitle: "Back",
          headerRight: project
            ? () => (
                <Pressable onPress={onPressMore} className="px-2 py-1">
                  <Ionicons
                    name="ellipsis-horizontal"
                    size={20}
                    color={Platform.OS === "ios" ? "#0a84ff" : "#71717a"}
                  />
                </Pressable>
              )
            : undefined,
        }}
      />
      {detail.isLoading ? (
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator />
        </View>
      ) : detail.error || projectMissing ? (
        <View className="flex-1 items-center justify-center px-6 gap-3">
          <Text className="text-sm text-destructive text-center">
            Failed to load project:{" "}
            {detail.error instanceof Error
              ? detail.error.message
              : "not found"}
          </Text>
          <Button variant="outline" onPress={() => detail.refetch()}>
            <Text>Retry</Text>
          </Button>
        </View>
      ) : (
        <ScrollView
          contentContainerClassName="pb-10"
          refreshControl={
            <RefreshControl
              refreshing={detail.isRefetching}
              onRefresh={onRefresh}
            />
          }
          keyboardDismissMode="on-drag"
        >
          <ProjectHeaderCard
            project={project}
            onEdit={() => {
              if (wsSlug) router.push(`/${wsSlug}/project/${id}/edit`);
            }}
          />
          <ProjectPropertiesSection
            project={project}
            onPressStatus={() => setStatusOpen(true)}
            onPressPriority={() => setPriorityOpen(true)}
            onPressLead={() => setLeadOpen(true)}
          />
          <ProjectResourcesSection
            projectId={id}
            onAdd={() => setResourceOpen(true)}
          />
          <View className="h-3" />
          <ProjectRelatedIssues projectId={id} />
        </ScrollView>
      )}

      {project ? (
        <>
          <ProjectStatusPickerSheet
            visible={statusOpen}
            value={project.status}
            onChange={(next: ProjectStatus) =>
              updateProject.mutate({ status: next })
            }
            onClose={() => setStatusOpen(false)}
          />
          <ProjectPriorityPickerSheet
            visible={priorityOpen}
            value={project.priority}
            onChange={(next: ProjectPriority) =>
              updateProject.mutate({ priority: next })
            }
            onClose={() => setPriorityOpen(false)}
          />
          <ProjectLeadPickerSheet
            visible={leadOpen}
            value={
              project.lead_type && project.lead_id
                ? { type: project.lead_type, id: project.lead_id }
                : null
            }
            onChange={(next: LeadValue | null) =>
              updateProject.mutate(
                next
                  ? { lead_type: next.type, lead_id: next.id }
                  : { lead_type: null, lead_id: null },
              )
            }
            onClose={() => setLeadOpen(false)}
          />
          <AddResourceSheet
            visible={resourceOpen}
            onSubmit={onAddResource}
            onClose={() => setResourceOpen(false)}
            submitting={createResource.isPending}
          />
        </>
      ) : null}
    </SafeAreaView>
  );
}
