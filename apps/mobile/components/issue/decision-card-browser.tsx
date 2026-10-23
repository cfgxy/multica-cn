/**
 * Decision card browser (RUYI-863) — the mobile screen body shared by the
 * three entries: the sidebar's more-menu entry (first card, "all"), the
 * info-feed single-card deep-link (pinned card, "all"), and the batch
 * entry (first pending card, "open"). One data source (the issue's card
 * list), one per-card view (DecisionCard) — entries differ only in their
 * initial filter + card, mirroring web's DecisionCardBrowser in
 * packages/views. Position rules come from
 * @multica/core/issues/decision-card-browser (shared pure functions), so
 * web and mobile cannot drift on filter/ordering/advance semantics.
 *
 * Mobile divergence (intentional, per the container rules in
 * apps/mobile/CLAUDE.md): web renders the browser in a dialog; mobile
 * renders it as a full screen because a card with options + context is
 * the whole usable width on a phone. The browser body itself is one
 * component, not three.
 */
import { useMemo, useState } from "react";
import { Pressable, ScrollView, View } from "react-native";
import {
  decisionBrowserNeighbor,
  filterDecisionCards,
  initialDecisionBrowserCardId,
  resolveDecisionBrowserPosition,
  type DecisionCardFilter,
} from "@multica/core/issues/decision-card-browser";
import type { IssueDecision } from "@multica/core/types";
import { Button } from "@/components/ui/button";
import { Text } from "@/components/ui/text";
import { DecisionCard } from "./decision-card";
import { useColorScheme } from "@/lib/use-color-scheme";
import { useT } from "@/lib/use-t";
import { cn } from "@/lib/utils";

const FILTER_OPTIONS: readonly DecisionCardFilter[] = [
  "all",
  "open",
  "answered",
  "cancelled",
];

function filterLabelKey(f: DecisionCardFilter): "filter_all" | "status_open" | "status_answered" | "status_cancelled" {
  switch (f) {
    case "all":
      return "filter_all";
    case "open":
      return "status_open";
    case "answered":
      return "status_answered";
    case "cancelled":
      return "status_cancelled";
  }
}

export interface MobileDecisionCardBrowserProps {
  /** The issue's full card list; created_at order is applied here. */
  decisions: readonly IssueDecision[];
  initialFilter?: DecisionCardFilter;
  initialCardId?: string | null;
}

export function MobileDecisionCardBrowser({
  decisions,
  initialFilter = "all",
  initialCardId,
}: MobileDecisionCardBrowserProps) {
  const { t } = useT("issues");
  const { colorScheme } = useColorScheme();
  const [filter, setFilter] = useState<DecisionCardFilter>(initialFilter);
  const [cardId, setCardId] = useState(() =>
    initialDecisionBrowserCardId(decisions, initialFilter, initialCardId),
  );

  const sorted = useMemo(
    () =>
      [...decisions].sort(
        (a, b) => Date.parse(a.created_at) - Date.parse(b.created_at),
      ),
    [decisions],
  );

  // Same advance rule as web: a card that left the active filter's bucket
  // (answered under "open", restored under "cancelled") moves the pointer
  // to the next matching card; an emptied "open" set is the done state.
  const resolvedId = resolveDecisionBrowserPosition(sorted, filter, cardId);
  const filtered = filterDecisionCards(sorted, filter);
  const currentIndex = resolvedId
    ? filtered.findIndex((d) => d.id === resolvedId)
    : -1;
  const current = currentIndex >= 0 ? filtered[currentIndex] : undefined;

  return (
    <View className="flex-1 bg-background" testID="decision-card-browser">
      {/* Status filter — a horizontal chip row (mobile idiom) over the
          web's dropdown select; options and semantics are identical. */}
      <View className="flex-row gap-2 px-4 py-2" testID="decision-browser-filter">
        {FILTER_OPTIONS.map((f) => {
          const active = f === filter;
          return (
            <Pressable
              key={f}
              onPress={() => setFilter(f)}
              accessibilityRole="button"
              accessibilityState={{ selected: active }}
              testID={`decision-browser-filter-${f}`}
              className={cn(
                "rounded-full border px-3 py-1",
                active
                  ? "border-brand bg-accent/40"
                  : "border-border bg-transparent",
              )}
            >
              <Text
                className={cn(
                  "text-xs font-medium",
                  active ? "text-brand" : "text-muted-foreground",
                )}
              >
                {t(`decisions.${filterLabelKey(f)}`, f === "all" ? "All" : f === "open" ? "Pending" : f === "answered" ? "Answered" : "Cancelled")}
              </Text>
            </Pressable>
          );
        })}
      </View>

      {current ? (
        <ScrollView contentContainerClassName="flex-1 gap-3 py-3">
          <View className="flex-row items-center px-4">
            <Text className="text-xs text-muted-foreground" testID="decision-browser-position">
              {currentIndex + 1} / {filtered.length}
            </Text>
          </View>
          <DecisionCard decision={current} />
          <View className="flex-row justify-between gap-2 px-4 pb-2">
            <Button
              size="sm"
              variant="outline"
              disabled={currentIndex === 0}
              onPress={() => {
                const id = decisionBrowserNeighbor(sorted, filter, resolvedId, "previous");
                if (id) setCardId(id);
              }}
              testID="decision-browser-previous"
            >
              <Text>{t("decisions.previous", "Previous")}</Text>
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={currentIndex === filtered.length - 1}
              onPress={() => {
                const id = decisionBrowserNeighbor(sorted, filter, resolvedId, "next");
                if (id) setCardId(id);
              }}
              testID="decision-browser-next"
            >
              <Text>{t("decisions.next", "Next")}</Text>
            </Button>
          </View>
        </ScrollView>
      ) : filter === "open" ? (
        <Text className="flex-1 p-5 text-center text-sm text-muted-foreground" testID="decision-browser-done">
          {t("decisions.browser_done", "All pending decisions answered")}
        </Text>
      ) : (
        <Text className="flex-1 p-5 text-center text-sm text-muted-foreground" testID="decision-browser-empty">
          {t(filter === "all" ? "decisions.empty_issue" : "decisions.filter_empty", filter === "all" ? "No decision cards" : "No matching decision cards")}
        </Text>
      )}
    </View>
  );
}
