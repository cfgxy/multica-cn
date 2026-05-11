/**
 * Header utility buttons shared across all primary tabs (Inbox / My Issues /
 * Chat / More). Provides the two global actions on the right: search and
 * create-issue. The workspace switcher does NOT live in the header — it
 * lives in the More tab. Header stays single-row: title left, actions right.
 *
 * Tab-specific actions (e.g. My Issues filter) MUST NOT live here — they
 * mix scope levels with global actions and would clutter the strip.
 */
import { Pressable } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { router } from "expo-router";
import { useWorkspaceStore } from "@/data/workspace-store";

const ICON_COLOR = "#3f3f46";
const HIT = { top: 8, right: 8, bottom: 8, left: 8 } as const;

export function HeaderActions() {
  const slug = useWorkspaceStore((s) => s.currentWorkspaceSlug);

  const onSearch = () => {
    if (slug) router.push(`/${slug}/search`);
  };
  const onCreate = () => {
    if (slug) router.push(`/${slug}/new-issue`);
  };

  return (
    <>
      <Pressable
        onPress={onSearch}
        hitSlop={HIT}
        className="size-9 items-center justify-center rounded-full active:bg-secondary"
        accessibilityLabel="Search"
      >
        <Ionicons name="search" size={20} color={ICON_COLOR} />
      </Pressable>
      <Pressable
        onPress={onCreate}
        hitSlop={HIT}
        className="size-9 items-center justify-center rounded-full active:bg-secondary"
        accessibilityLabel="New issue"
      >
        <Ionicons name="add" size={24} color={ICON_COLOR} />
      </Pressable>
    </>
  );
}
