/**
 * Project picker — single-select over workspace projects. Tap a row to set,
 * tap the "No project" row to clear. Mirrors web's `ProjectPicker`
 * (`packages/views/projects/components/project-picker.tsx`) in behavior.
 *
 * Container: iOS pageSheet via shared `<SheetShell>` (CLAUDE.md Lesson #6).
 *
 * Phase 1 does NOT support inline project creation — mobile users who want
 * a new project create it on web.
 */
import { useMemo, useState } from "react";
import { ActivityIndicator, FlatList, Pressable, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import { Ionicons } from "@expo/vector-icons";
import type { Project } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ProjectIcon } from "@/components/ui/project-icon";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { TextField } from "@/components/ui/text-field";
import { SheetShell } from "@/components/ui/sheet-shell";
import { projectListOptions } from "@/data/queries/projects";
import { useWorkspaceStore } from "@/data/workspace-store";
import { cn } from "@/lib/utils";

interface Props {
  visible: boolean;
  value: Project | null;
  onChange: (next: Project | null) => void;
  onClose: () => void;
}

export function ProjectPickerSheet({
  visible,
  value,
  onChange,
  onClose,
}: Props) {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { data: projects, isLoading } = useQuery(projectListOptions(wsId));
  const [query, setQuery] = useState("");

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const all = projects ?? [];
    const sorted = [...all].sort((a, b) => a.title.localeCompare(b.title));
    if (!q) return sorted;
    return sorted.filter((p) => p.title.toLowerCase().includes(q));
  }, [projects, query]);

  const pick = (next: Project | null) => {
    onChange(next);
    onClose();
  };

  return (
    <SheetShell visible={visible} onClose={onClose} title="Project">
      <View className="px-3 pt-2 pb-2 border-b border-border">
        <TextField
          value={query}
          onChangeText={setQuery}
          placeholder="Search projects"
          autoCapitalize="none"
          autoCorrect={false}
        />
      </View>
      {isLoading ? (
        <View className="px-3 py-8 items-center">
          <ActivityIndicator />
        </View>
      ) : (
        <FlatList
          data={filtered}
          className="flex-1"
          keyExtractor={(item) => item.id}
          keyboardShouldPersistTaps="handled"
          automaticallyAdjustKeyboardInsets
          ListHeaderComponent={
            <NoProjectRow
              checked={value === null}
              onPress={() => pick(null)}
            />
          }
          renderItem={({ item }) => (
            <ProjectRow
              project={item}
              checked={item.id === value?.id}
              onPress={() => pick(item)}
            />
          )}
          ListEmptyComponent={
            <View className="px-3 py-6 items-center">
              <Text className="text-xs text-muted-foreground text-center">
                {query
                  ? "No matches."
                  : "No projects in this workspace yet.\nCreate them on web."}
              </Text>
            </View>
          }
        />
      )}
    </SheetShell>
  );
}

function NoProjectRow({
  checked,
  onPress,
}: {
  checked: boolean;
  onPress: () => void;
}) {
  return (
    <Pressable
      onPress={onPress}
      className={cn(
        "flex-row items-center gap-3 px-3 py-2.5 border-b border-border active:bg-secondary",
        checked && "bg-secondary",
      )}
    >
      <Ionicons
        name="close-circle-outline"
        size={16}
        color={MOBILE_PLACEHOLDER_COLOR}
      />
      <Text className="flex-1 text-sm text-muted-foreground">No project</Text>
      {checked ? <Text className="text-xs text-muted-foreground">✓</Text> : null}
    </Pressable>
  );
}

function ProjectRow({
  project,
  checked,
  onPress,
}: {
  project: Project;
  checked: boolean;
  onPress: () => void;
}) {
  return (
    <Pressable
      onPress={onPress}
      className={cn(
        "flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary",
        checked && "bg-secondary",
      )}
    >
      <ProjectIcon icon={project.icon} size="md" />
      <Text className="flex-1 text-sm text-foreground" numberOfLines={1}>
        {project.title}
      </Text>
      {checked ? <Text className="text-xs text-muted-foreground">✓</Text> : null}
    </Pressable>
  );
}
