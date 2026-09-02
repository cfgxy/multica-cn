/**
 * Related pull requests modal (RUYI-43).
 *
 * Lists the PRs linked to the issue (branch name / title / body referenced
 * its identifier) — the mobile counterpart of the web sidebar's
 * "Pull requests" section (`PullRequestList` in
 * packages/views/issues/components/pull-request-list.tsx), same endpoint,
 * same cache key shape (data/queries/github.ts). UI diverges by design:
 * the web sidebar collapses at 4 rows; a phone modal is a scroll surface,
 * so every row renders and the visible set stays identical to web.
 *
 * Tapping a row opens `pr.html_url`. The GitHub-App-installed vs. browser
 * split is delegated to the OS via that one canonical URL — see the
 * rationale in lib/pull-request-link.ts (documented there; do not add a
 * canOpenURL("github://") probe).
 *
 * `presentation: "modal"` (registered in the workspace _layout, mirroring
 * issue/[id]/comments): full-page slide-up with a native header; Android
 * system back closes it. Loading / empty states mirror the web section's
 * caption + the issue screen's spinner/retry patterns.
 */
import { useCallback } from "react";
import {
  ActivityIndicator,
  Alert,
  Linking,
  Pressable,
  ScrollView,
  View,
} from "react-native";
import { useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { Ionicons } from "@expo/vector-icons";
import type { GitHubPullRequest } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { issuePullRequestsOptions } from "@/data/queries/github";
import {
  formatPullRequestSubtitle,
  pullRequestStateLabelKey,
  pullRequestStateVisual,
} from "@/lib/pull-request-display";
import { openExternalUrl } from "@/lib/pull-request-link";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

export default function IssuePullRequestsRoute() {
  const { t } = useT("issues");
  const { id } = useLocalSearchParams<{ id: string }>();
  const { data, isLoading, error, refetch } = useQuery(
    issuePullRequestsOptions(id ?? null),
  );
  const prs = data?.pull_requests ?? [];

  const onOpen = useCallback(
    async (pr: GitHubPullRequest) => {
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
    },
    [t],
  );

  return (
    <View className="flex-1 bg-background">
      {isLoading ? (
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator />
        </View>
      ) : error ? (
        <View className="flex-1 items-center justify-center gap-3 px-6">
          <Text className="text-center text-sm text-destructive">
            {error instanceof Error ? error.message : String(error)}
          </Text>
          <Button variant="outline" onPress={() => refetch()}>
            <Text>{t("mobile.detail.retry", "Retry")}</Text>
          </Button>
        </View>
      ) : prs.length === 0 ? (
        // Same caption semantics as the web section's empty state.
        <View className="flex-1 items-center justify-center px-6">
          <Text className="text-center text-sm text-muted-foreground">
            {t(
              "detail.pull_requests_empty",
              "No linked pull requests yet. Reference this issue's identifier in a PR's branch name, title, or body to auto-link it.",
            )}
          </Text>
        </View>
      ) : (
        <ScrollView showsVerticalScrollIndicator={false}>
          <View className="gap-1 px-4 pb-4 pt-2">
            {prs.map((pr) => (
              <PullRequestRow key={pr.id} pr={pr} onOpen={onOpen} />
            ))}
          </View>
        </ScrollView>
      )}
    </View>
  );
}

function PullRequestRow({
  pr,
  onOpen,
}: {
  pr: GitHubPullRequest;
  onOpen: (pr: GitHubPullRequest) => void;
}) {
  const { t } = useT("issues");
  const { colorScheme } = useColorScheme();
  const visual = pullRequestStateVisual(pr.state);
  const labelKey = pullRequestStateLabelKey(pr.state);
  // Known states use the shared web-side label; unknown states fall back
  // to the raw server value (API Response Compatibility).
  const stateLabel = labelKey ? t(labelKey, pr.state) : pr.state;
  const subtitle = formatPullRequestSubtitle(pr, stateLabel);

  return (
    <Pressable
      onPress={() => onOpen(pr)}
      accessibilityRole="link"
      accessibilityLabel={pr.title}
      className={`flex-row items-start gap-2.5 rounded-lg px-2 py-2.5 active:bg-accent/50 ${
        visual.dimmed ? "opacity-80" : ""
      }`}
    >
      <Ionicons
        name={visual.icon}
        size={16}
        color={THEME[colorScheme][visual.themeKey]}
        style={{ marginTop: 2 }}
      />
      <View className="flex-1">
        <Text
          numberOfLines={2}
          className="text-sm font-medium leading-snug text-foreground"
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
  );
}
