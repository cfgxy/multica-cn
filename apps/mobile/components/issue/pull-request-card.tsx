/**
 * Linked pull-request card in the issue timeline (RUYI-634) — the timeline
 * counterpart of the pull-requests modal row (RUYI-43), dressed in the
 * decision card's design language (Card surface, overline label, state
 * icon). One card per linked PR; the timeline interleave lives in
 * lib/timeline-pull-requests.ts.
 *
 * Tap opens `pr.html_url` through the ONE canonical-URL hand-off
 * (lib/pull-request-link.ts — the OS routes it into the GitHub app's PR
 * page when installed, into the browser otherwise; do not add a
 * canOpenURL("github://") probe). Failures Alert with the same message the
 * modal row shows — never a silent no-op.
 */
import { useCallback } from "react";
import { Alert, Linking, Pressable, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import type { GitHubPullRequest } from "@multica/core/types";
import { Card } from "@/components/ui/card";
import { Text } from "@/components/ui/text";
import {
  formatPullRequestSubtitle,
  pullRequestStateLabelKey,
  pullRequestStateVisual,
} from "@/lib/pull-request-display";
import { openExternalUrl } from "@/lib/pull-request-link";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";
import { cn } from "@/lib/utils";

export function PullRequestCard({ pr }: { pr: GitHubPullRequest }) {
  const { t } = useT("issues");
  const { colorScheme } = useColorScheme();
  const visual = pullRequestStateVisual(pr.state);
  const labelKey = pullRequestStateLabelKey(pr.state);
  // Known states use the shared web-side label; unknown states fall back
  // to the raw server value (API Response Compatibility) — same as the
  // modal row.
  const stateLabel = labelKey ? t(labelKey, pr.state) : pr.state;
  const subtitle = formatPullRequestSubtitle(pr, stateLabel);

  const onOpen = useCallback(async () => {
    const result = await openExternalUrl(pr.html_url, Linking);
    if (!result.ok) {
      Alert.alert(
        t(
          "mobile.detail.pull_request_open_failed",
          "Couldn't open this pull request.",
        ),
        result.message,
      );
    }
  }, [pr.html_url, t]);

  return (
    <View className={cn("px-4", visual.dimmed ? "opacity-80" : "")}>
      <Card>
        <Pressable
          onPress={onOpen}
          accessibilityRole="link"
          accessibilityLabel={pr.title}
          testID="pull-request-card"
          className="-mx-1 flex-row items-start gap-2 rounded-lg px-1 py-0.5 active:bg-accent/50"
        >
          <Ionicons
            name={visual.icon}
            size={16}
            color={THEME[colorScheme][visual.themeKey]}
            style={{ marginTop: 2 }}
          />
          <View className="flex-1">
            <Text className="text-xs font-medium text-muted-foreground">
              {t("mobile.detail.pull_request_card_label", "Pull request")}
            </Text>
            <Text
              numberOfLines={2}
              className="mt-1 text-sm font-medium leading-snug text-foreground"
            >
              {pr.title}
            </Text>
            <Text
              numberOfLines={1}
              className="mt-0.5 text-xs text-muted-foreground"
            >
              {subtitle}
            </Text>
          </View>
          <Ionicons
            name="chevron-forward"
            size={14}
            color={THEME[colorScheme].mutedForeground}
            style={{ marginTop: 3 }}
          />
        </Pressable>
      </Card>
    </View>
  );
}
