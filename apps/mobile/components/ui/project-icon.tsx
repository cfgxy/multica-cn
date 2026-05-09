/**
 * Mobile ProjectIcon — RN port of
 * `packages/views/projects/components/project-icon.tsx`. Renders the
 * project's emoji icon with a 📁 fallback. Size scales font on Text since
 * RN doesn't have CSS line-height tricks.
 */
import { Text } from "@/components/ui/text";

export type ProjectIconSize = "sm" | "md" | "lg";

const SIZE_CLASS: Record<ProjectIconSize, string> = {
  sm: "text-sm leading-none",
  md: "text-base leading-none",
  lg: "text-2xl leading-none",
};

interface Props {
  icon?: string | null;
  size?: ProjectIconSize;
}

export function ProjectIcon({ icon, size = "sm" }: Props) {
  return <Text className={SIZE_CLASS[size]}>{icon || "📁"}</Text>;
}
