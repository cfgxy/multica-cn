/**
 * Tasks-tab issue row in the inbox visual style (RUYI-413) — inbox-row's
 * two-line layout restated for the `Issue` type (mobile cannot reuse the
 * inbox component itself: different data shape, and apps/mobile/CLAUDE.md
 * sharing whitelist):
 *
 *   [assignee avatar]  [identifier · priority · title]
 *                      [description (1 line) | agent-activity badge · time]
 *
 * Divergences from inbox-row, all intentional:
 *   - No unread dot / read styling — a read state is an inbox-only concept;
 *     the title is always foreground + medium (the "unread" weight).
 *   - No trailing status icon and no CustomStatusChip — the section header
 *     already names the status CATEGORY and RUYI-413 drops per-row status
 *     glyphs/badges. Custom-status display names live on the detail page.
 *   - Subtitle is the raw issue description, not the type-aware
 *     InboxDetailLabel — a task's detail line IS its description.
 *   - Time is the issue's last activity (updated_at), not an inbox item's
 *     created_at.
 *
 * The agent-activity badge is the SAME component and data slice the inbox
 * row renders (IssueAgentActivityBadge fed by deriveIssueActivityMap over
 * the shared workspace snapshot) — one visual language for "an agent is on
 * this" across both lists.
 *
 * Left column: assignee when set; a neutral placeholder otherwise, so every
 * row's text edge stays aligned (web's list-row simply omits the avatar,
 * which would leave a ragged left edge in this left-anchored layout).
 *
 * Scope note: `IssueRow` (compact single-line style) still serves my-issues,
 * pins and project related-issues — RUYI-413 changes the Tasks tab only.
 */
import { Ionicons } from "@expo/vector-icons";
import { Pressable, View } from "react-native";
import type { Issue } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { PriorityIcon } from "@/components/ui/priority-icon";
import { IssueAgentActivityBadge } from "@/components/issue/issue-agent-activity-badge";
import type { IssueActivity } from "@/lib/issue-agent-activity";
import { timeAgo } from "@/lib/time-ago";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";

interface Props {
  issue: Issue;
  /**
   * Active agent-task groups for this issue, sliced from the shared
   * workspace snapshot by the screen (same shape the inbox screen passes).
   * Undefined / empty groups render nothing — same visibility rule as the
   * inbox row.
   */
  activity?: IssueActivity;
  onPress: () => void;
}

export function IssueRowInbox({ issue, activity, onPress }: Props) {
  const { colorScheme } = useColorScheme();
  const hasAssignee = !!(issue.assignee_type && issue.assignee_id);
  const hasActivity =
    !!activity && (activity.running.length > 0 || activity.queued.length > 0);
  return (
    <Pressable
      onPress={onPress}
      className="bg-background active:bg-secondary px-4 py-3"
    >
      <View className="flex-row gap-3">
        {hasAssignee ? (
          <ActorAvatar
            type={issue.assignee_type}
            id={issue.assignee_id}
            size={36}
            showPresence
          />
        ) : (
          <View
            style={{ width: 36, height: 36 }}
            className="items-center justify-center rounded-full bg-muted"
          >
            <Ionicons
              name="person"
              size={20}
              color={THEME[colorScheme].mutedForeground}
            />
          </View>
        )}
        <View className="flex-1 min-w-0">
          <View className="flex-row items-center gap-2">
            <Text className="text-xs text-muted-foreground shrink-0">
              {issue.identifier}
            </Text>
            <PriorityIcon priority={issue.priority} size={14} />
            <Text
              className="flex-1 text-sm font-medium text-foreground"
              numberOfLines={1}
            >
              {issue.title}
            </Text>
          </View>
          <View className="flex-row items-center gap-2 mt-0.5">
            {issue.description ? (
              <View className="flex-1 min-w-0">
                <Text
                  className="text-xs text-muted-foreground"
                  numberOfLines={1}
                >
                  {issue.description}
                </Text>
              </View>
            ) : (
              <View className="flex-1" />
            )}
            {hasActivity ? (
              <View className="mr-1 shrink-0">
                <IssueAgentActivityBadge
                  running={activity.running}
                  queued={activity.queued}
                />
              </View>
            ) : null}
            <Text className="text-xs text-muted-foreground shrink-0">
              {timeAgo(issue.updated_at)}
            </Text>
          </View>
        </View>
      </View>
    </Pressable>
  );
}
