/*
 * Mobile time-ago formatter. Mirrors the algorithm in
 * packages/views/inbox/components/inbox-list-item.tsx `useTimeAgo` so
 * "X minutes ago" reads identically across web/desktop and mobile (Behavioral
 * parity rule in apps/mobile/CLAUDE.md).
 */
import i18n from "i18next";

export function timeAgo(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime();
  const minutes = Math.floor(diff / 60000);
  if (minutes < 1) return i18n.t("common:time.just_now", "just now");
  if (minutes < 60) return i18n.t("common:time.minutes_ago", `{{count}}m ago`, { count: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return i18n.t("common:time.hours_ago", `{{count}}h ago`, { count: hours });
  const days = Math.floor(hours / 24);
  if (days < 7) return i18n.t("common:time.days_ago", `{{count}}d ago`, { count: days });
  const weeks = Math.floor(days / 7);
  if (weeks < 5) return `${weeks}w ago`;
  return new Date(dateStr).toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
  });
}
