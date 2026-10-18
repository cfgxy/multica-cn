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
import {
  deriveChecksStatus,
  deriveMergeStatus,
  deriveReviewDecision,
  type PullRequestChecksStatus,
  type PullRequestMergeStatus,
} from "@multica/core/github";
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

export function PullRequestCard({
  pr,
  showTargetBranch = false,
  showMergeRisk = false,
}: {
  pr: GitHubPullRequest;
  showTargetBranch?: boolean;
  showMergeRisk?: boolean;
}) {
  const { t } = useT("issues");
  const { colorScheme } = useColorScheme();
  const visual = pullRequestStateVisual(pr.state);
  const labelKey = pullRequestStateLabelKey(pr.state);
  // Known states use the shared web-side label; unknown states fall back
  // to the raw server value (API Response Compatibility) — same as the
  // modal row.
  const stateLabel = labelKey ? t(labelKey, pr.state) : pr.state;
  const subtitle = formatPullRequestSubtitle(pr, stateLabel);
  const isTerminal = pr.state === "merged" || pr.state === "closed";
  const checksStatus = showMergeRisk && !isTerminal ? deriveChecksStatus(pr) : null;
  const mergeStatus = showMergeRisk && !isTerminal ? deriveMergeStatus(pr) : null;
  const reviewDecision = showMergeRisk && !isTerminal
    ? deriveReviewDecision(pr.review_decision, pr.snapshot_available)
    : null;
  const checksLabel = checksStatus ? getChecksStatusLabel(checksStatus, t) : null;
  const mergeLabel = mergeStatus ? getMergeStatusLabel(mergeStatus, t) : null;
  const reviewLabel = reviewDecision ? getReviewDecisionLabel(reviewDecision, t) : null;
  const staleLabel =
    showMergeRisk && !isTerminal && pr.snapshot_stale === true && (checksLabel || mergeLabel || reviewLabel)
      ? t("detail.pull_request_snapshot_stale_unknown", "Snapshot may be outdated")
      : null;
  const accessibilityLabel = [pr.title, checksLabel, mergeLabel, reviewLabel, staleLabel]
    .filter(Boolean)
    .join(". ");

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
          accessibilityLabel={accessibilityLabel}
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
            {showTargetBranch ? (
              <Text numberOfLines={1} className="mt-0.5 text-xs text-muted-foreground">
                {t("detail.pull_request_target_branch_label", "Target branch")}: {pr.base_branch || t("detail.pull_request_target_branch_unknown", "Unknown")}
              </Text>
            ) : null}
            {checksLabel || mergeLabel || reviewLabel || staleLabel ? (
              <View className={cn("mt-1 gap-0.5", pr.snapshot_stale === true ? "opacity-60" : "")}>
                {checksLabel ? (
                  <Text
                    testID="pull-request-checks-status"
                    className={cn("text-xs", checksStatusTone(checksStatus))}
                  >
                    {checksLabel}
                  </Text>
                ) : null}
                {mergeLabel ? (
                  <Text
                    testID="pull-request-merge-status"
                    className={cn("text-xs", mergeStatusTone(mergeStatus))}
                  >
                    {mergeLabel}
                  </Text>
                ) : null}
                {reviewLabel ? (
                  <Text
                    testID="pull-request-review-decision"
                    className={cn("text-xs", reviewDecisionTone(reviewDecision))}
                  >
                    {reviewLabel}
                  </Text>
                ) : null}
                {staleLabel ? (
                  <Text testID="pull-request-snapshot-stale" className="text-xs text-muted-foreground">
                    {staleLabel}
                  </Text>
                ) : null}
              </View>
            ) : null}
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

function getChecksStatusLabel(
  status: PullRequestChecksStatus,
  t: ReturnType<typeof useT>["t"],
): string | null {
  switch (status.kind) {
    case "failed": {
      if (status.failed <= 0 || status.total < status.failed) {
        return t("detail.pull_request_checks_failed_unknown", "Checks failed");
      }
      const names = status.names.slice(0, 2);
      const remaining = status.names.length - names.length;
      if (remaining > 0) {
        names.push(t("detail.pull_request_checks_more", { count: remaining }));
      }
      return names.length > 0
        ? t("detail.pull_request_checks_failed_named", {
            failed: status.failed,
            total: status.total,
            names: names.join(", "),
          })
        : t("detail.pull_request_checks_failed_count", {
            failed: status.failed,
            total: status.total,
          });
    }
    case "pending":
      return t("detail.pull_request_checks_running", {
        passed: status.passed,
        total: status.total,
        running: status.running,
      });
    case "passed":
      return t("detail.pull_request_checks_all_passed", { total: status.total });
    case "none":
      return t("detail.pull_request_checks_none");
    case "unknown":
      return t("detail.pull_request_checks_unknown");
    case "unavailable":
      return null;
  }
}

function getReviewDecisionLabel(
  decision: NonNullable<ReturnType<typeof deriveReviewDecision>>,
  t: ReturnType<typeof useT>["t"],
): string {
  switch (decision) {
    case "approved":
      return t("detail.pull_request_review_approved", "Approved");
    case "changes_requested":
      return t("detail.pull_request_review_changes_requested", "Changes requested");
    case "review_required":
      return t("detail.pull_request_review_required", "Review required");
    case "unknown":
      return t("detail.pull_request_review_unknown", "Review status unknown");
  }
}

function reviewDecisionTone(
  decision: ReturnType<typeof deriveReviewDecision>,
): string {
  switch (decision) {
    case "approved":
      return "text-emerald-600 dark:text-emerald-400";
    case "changes_requested":
      return "text-destructive";
    case "review_required":
      return "text-amber-600 dark:text-amber-400";
    default:
      return "text-muted-foreground";
  }
}

function getMergeStatusLabel(
  status: PullRequestMergeStatus,
  t: ReturnType<typeof useT>["t"],
): string | null {
  switch (status.kind) {
    case "conflicting":
      return t("detail.pull_request_merge_conflicting");
    case "ready":
      return t("detail.pull_request_merge_ready");
    case "blocked":
      return t("detail.pull_request_merge_blocked");
    case "behind":
      return t("detail.pull_request_merge_behind");
    case "unstable":
      return t("detail.pull_request_merge_unstable");
    case "has_hooks":
      return t("detail.pull_request_merge_has_hooks");
    case "none":
      return null;
  }
}

function checksStatusTone(status: PullRequestChecksStatus | null): string {
  switch (status?.kind) {
    case "failed":
      return "text-destructive";
    case "pending":
      return "text-amber-600 dark:text-amber-400";
    case "passed":
      return "text-emerald-600 dark:text-emerald-400";
    default:
      return "text-muted-foreground";
  }
}

function mergeStatusTone(status: PullRequestMergeStatus | null): string {
  switch (status?.kind) {
    case "conflicting":
    case "blocked":
    case "unstable":
      return "text-amber-600 dark:text-amber-400";
    case "ready":
      return "text-emerald-600 dark:text-emerald-400";
    default:
      return "text-muted-foreground";
  }
}
