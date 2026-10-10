/**
 * Decision Center row content (RUYI-530) — presentational half pulled out
 * of (tabs)/decisions.tsx so the screen stays a thin SectionList shell and
 * the row is render-testable in isolation (same split as the inbox's
 * inbox-row.tsx / swipeable-inbox-row.tsx).
 *
 * RUYI-494 IA stays fixed: one row per CARD, line 1 = identifier + issue
 * title (+ 「有推荐」 badge), line 2 = that card's own question + time-ago.
 * RUYI-530 adds the card creator's avatar (left column, inbox-row sizing)
 * and the inbox read style for decided rows: answered/cancelled render the
 * muted title + faded secondary line, open keeps full contrast.
 * RUYI-622 aligns the typography tokens with the tasks-tab issue row
 * (issue-row-inbox.tsx): muted text-xs identifier, text-sm title (open =
 * semibold foreground, decided = muted), text-xs secondary line — one
 * typographic language across the bottom-tab lists.
 */
import { Pressable, View } from "react-native";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import {
  isDecidedDecisionRow,
  resolveDecisionActorType,
  type DecisionInboxRow,
} from "@/lib/decision-inbox-display";
import { timeAgo } from "@/lib/time-ago";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/use-t";

interface Props {
  row: DecisionInboxRow;
  onPress: () => void;
}

export function DecisionInboxRow({ row, onPress }: Props) {
  const { t } = useT("decisions");
  const decided = isDecidedDecisionRow(row.status);
  // ActorAvatar renders a real glyph for every input (initials / system
  // icon fallback), so mapping unknown creator types to `system` keeps the
  // left column from ever rendering blank — no crash path, no empty slot.
  const actorType = resolveDecisionActorType(row.createdByType);

  return (
    <Pressable
      accessibilityRole="button"
      className="px-4 py-3 active:bg-muted"
      onPress={onPress}
    >
      <View className="flex-row gap-3">
        <ActorAvatar
          type={actorType}
          id={row.createdById || null}
          size={36}
        />
        <View className="flex-1 min-w-0">
          {/* Line 1 — identifier uses the tasks-tab row's muted text-xs
              treatment (RUYI-622); the cap keeps it a stable anchor at large
              system sizes, same rule as the issue detail header title. */}
          <View className="flex-row items-center gap-2">
            <Text
              numberOfLines={1}
              maxFontSizeMultiplier={1}
              className="text-xs text-muted-foreground shrink-0"
            >
              {row.identifier}
            </Text>
            <Text
              numberOfLines={1}
              className={cn(
                "flex-1 text-sm",
                decided ? "text-muted-foreground" : "font-semibold text-foreground",
              )}
            >
              {row.issueTitle}
            </Text>
            {row.recommended ? (
              <View className="shrink-0 rounded-full bg-brand/10 px-2 py-0.5">
                <Text className="text-xs text-brand">
                  {t("badge.recommended", "Has recommendation")}
                </Text>
              </View>
            ) : null}
          </View>
          {/* Line 2 — the card's own question; two cards on one issue differ
              here, which is the whole point of the one-row-per-card IA. The
              decided fade mirrors the inbox read style's /60 opacity. Size
              follows the tasks-tab row's text-xs secondary line (RUYI-622). */}
          <View className="mt-1 flex-row items-center gap-2">
            <Text
              numberOfLines={1}
              className={cn(
                "flex-1 text-xs",
                decided ? "text-muted-foreground/60" : "text-muted-foreground",
              )}
            >
              {row.question}
            </Text>
            <Text
              numberOfLines={1}
              maxFontSizeMultiplier={1}
              className={cn(
                "shrink-0 text-xs",
                decided ? "text-muted-foreground/60" : "text-muted-foreground",
              )}
            >
              {timeAgo(row.createdAt)}
            </Text>
          </View>
        </View>
      </View>
    </Pressable>
  );
}
