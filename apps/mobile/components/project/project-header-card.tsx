/**
 * Header card for the project detail screen. Large emoji icon centered above
 * the title, with the description shown in full (no truncation) below.
 *
 * Mirrors the visual emphasis of web's `project-header.tsx` but in a single
 * vertical stack instead of the web sidebar layout — phones don't have the
 * horizontal real estate for a side-by-side header + properties layout.
 */
import { Pressable, View } from "react-native";
import type { Project } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ProjectIcon } from "@/components/ui/project-icon";

interface Props {
  project: Project;
  onEdit?: () => void;
}

export function ProjectHeaderCard({ project, onEdit }: Props) {
  return (
    <Pressable
      onPress={onEdit}
      disabled={!onEdit}
      className="px-4 pt-4 pb-3 active:bg-secondary/40"
    >
      <View className="items-start gap-2">
        <ProjectIcon icon={project.icon} size="lg" />
        <Text
          className="text-2xl font-bold text-foreground"
          selectable
        >
          {project.title}
        </Text>
        {project.description ? (
          <Text
            className="text-sm text-muted-foreground"
            selectable
          >
            {project.description}
          </Text>
        ) : onEdit ? (
          <Text className="text-sm text-muted-foreground/60 italic">
            Add a description
          </Text>
        ) : null}
      </View>
    </Pressable>
  );
}
