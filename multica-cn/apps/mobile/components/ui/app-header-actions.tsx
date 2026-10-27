/**
 * Header utility buttons shared across primary tabs (Inbox / My Issues).
 * Provides two global actions on the right: search and create-issue.
 *
 * The workspace menu (global nav, workspace switcher, settings) is reached
 * via the "More" tab in the bottom bar.
 *
 * Tab-specific actions (e.g. My Issues filter) MUST NOT live here — they
 * mix scope levels with global actions and would clutter the strip.
 */
import { router } from "expo-router";
import { IconButton } from "@/components/ui/icon-button";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useT } from "@/lib/use-t";

export function HeaderActions() {
  const { t } = useT("common");
  const slug = useWorkspaceStore((s) => s.currentWorkspaceSlug);

  const onSearch = () => {
    if (slug) router.push(`/${slug}/search`);
  };
  const onCreate = () => {
    if (slug) router.push(`/${slug}/new-issue`);
  };

  return (
    <>
      <IconButton
        name="search"
        onPress={onSearch}
        accessibilityLabel={t("mobile.common.search", "Search")}
      />
      <IconButton
        name="add"
        iconSize={24}
        onPress={onCreate}
        // fallback 与 layout:sidebar.new_issue 的 en 值逐字一致（'New Issue'），
        // 资源缺失时不产生大小写漂移。
        accessibilityLabel={t("layout:sidebar.new_issue", "New Issue")}
      />
    </>
  );
}
