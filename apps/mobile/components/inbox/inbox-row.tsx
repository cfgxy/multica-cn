/**
 * Inbox row content — the visual half, no gesture wrapping. Pulled out of
 * (tabs)/inbox.tsx so swipeable-inbox-row.tsx can wrap it with the gesture
 * recognizer without duplicating layout. Keep this file purely presentational
 * — the swipe and the press behaviour live in the wrapper.
 *
 * Visual structure mirrors web's InboxListItem
 * (packages/views/inbox/components/inbox-list-item.tsx). Per
 * apps/mobile/CLAUDE.md "Visual alignment is baseline":
 *   - Right column stacks vertically: status icon on top row, time on bottom.
 *   - Secondary line uses the type-aware `InboxDetailLabel`, not raw body.
 *
 * RUYI-554 unified status language (documented mobile divergence from web,
 * which still renders an unread dot): unread vs read is typographic only —
 * unread = semibold foreground title, read = muted title — and the row's
 * agent avatar carries the agent-activity state (breathing / grayed /
 * static) instead of a presence dot.
 */
import { Pressable, View } from "react-native";
import type { InboxItem } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { StatusIcon } from "@/components/ui/status-icon";
import { IssueAgentActivityBadge } from "@/components/issue/issue-agent-activity-badge";
import type { IssueActivity } from "@/lib/issue-agent-activity";
import { selectActorActivity } from "@/lib/issue-agent-activity";
import { InboxDetailLabel } from "@/components/inbox/detail-label";
import { getInboxDisplayTitle } from "@/lib/inbox-display";
import { useIssueStatuses } from "@/lib/use-issue-statuses";
import { timeAgo } from "@/lib/time-ago";
import { cn } from "@/lib/utils";

interface Props {
  item: InboxItem;
  /**
   * Active agent-task groups for this item's issue, sliced from the shared
   * workspace snapshot by the screen (RUYI-76 ③). Undefined / empty groups
   * render nothing — same visibility rule as web's inbox badge
   * (IssueAgentActivityIndicator with hoverCard={false}).
   */
  activity?: IssueActivity;
  onPress: () => void;
}

export function InboxRow({ item, activity, onPress }: Props) {
  const isUnread = !item.read;
  const { categoryOf, colorOf } = useIssueStatuses();
  const displayTitle = getInboxDisplayTitle(item);
  const actorType = item.actor_type ?? item.recipient_type;
  const actorId = item.actor_id ?? item.recipient_id;

  return (
    <Pressable onPress={onPress} className="bg-background active:bg-secondary px-4 py-3">
      <View className="flex-row gap-3">
        {/* Avatar state is the unified activity language (RUYI-554): the
            actor's own task on this issue drives breathing (running) /
            grayed (queued) / static — the corner presence dot is retired
            from list rows. */}
        <ActorAvatar
          type={actorType}
          id={actorId}
          size={36}
          activity={selectActorActivity(activity, actorId) ?? undefined}
        />
        <View className="flex-1 min-w-0">
          {/* Top row: [identifier + title] (left) | [status icon] (right).
              Unread state is typographic only (RUYI-554): bold-vs-regular
              weight + foreground-vs-muted colour — the old leading blue dot
              is gone. The identifier anchors the row to its issue (RUYI-314):
              muted and shrink-0 like issue-row.tsx's identifier column, so a
              long title truncates before the identifier does. */}
          <View className="flex-row items-center gap-2">
            <View className="flex-row items-center gap-1.5 flex-1 min-w-0">
              {item.issue_identifier ? (
                <Text className="text-xs text-muted-foreground shrink-0">
                  {item.issue_identifier}
                </Text>
              ) : null}
              <Text
                className={cn(
                  "flex-1 text-sm",
                  isUnread
                    ? "font-semibold text-foreground"
                    : "text-muted-foreground",
                )}
                numberOfLines={1}
              >
                {displayTitle}
              </Text>
            </View>
            {/* The glyph is per category, so it alone cannot tell "In Review"
                from a custom "Human Review" — a move between two statuses of
                one category would leave this row pixel-identical and read as
                "the inbox never updated" (MUL-6395). Colour is what carries a
                custom status's identity; `colorOf` is null for a built-in,
                which keeps it on its category token. */}
            {item.issue_status ? (
              <StatusIcon
                status={item.issue_status}
                category={categoryOf(item.issue_status)}
                color={colorOf(item.issue_status)}
                size={14}
              />
            ) : null}
          </View>
          {/* Bottom row: [type-aware detail label] (left) | [agent activity
              badge + time] (right). Badge placement mirrors web's
              InboxListItem — badge sits immediately left of the timestamp
              (packages/views/inbox/components/inbox-list-item.tsx:200). */}
          <View className="flex-row items-center gap-2 mt-0.5">
            <View className="flex-1 min-w-0">
              <InboxDetailLabel
                item={item}
                className={
                  isUnread
                    ? "text-muted-foreground"
                    : "text-muted-foreground/60"
                }
              />
            </View>
            {item.issue_id && activity && (activity.running.length > 0 || activity.queued.length > 0) ? (
              <View className="mr-1 shrink-0">
                <IssueAgentActivityBadge
                  running={activity.running}
                  queued={activity.queued}
                />
              </View>
            ) : null}
            <Text
              className={cn(
                "text-xs shrink-0",
                isUnread
                  ? "text-muted-foreground"
                  : "text-muted-foreground/60",
              )}
            >
              {timeAgo(item.created_at)}
            </Text>
          </View>
        </View>
      </View>
    </Pressable>
  );
}
