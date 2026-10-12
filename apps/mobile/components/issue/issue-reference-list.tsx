/**
 * Tail aggregation of issue references (RUYI-635 + 增量, mobile half).
 *
 * Web renders every issue/PR mention as plain body text and lists the
 * collected references in a footer; mobile's demote pass
 * (`lib/markdown/preprocess.ts`) applies the same body-text contract, and
 * this component is the tail block. The 增量 rework makes it an
 * anti-mistouch two-level surface (Owner points ①③④, Board 1 + Board 2):
 *
 *   - Level 1 — ONE Pressable for the whole block (separator + header
 *     「引用 · N」 + chevron + at most 2 preview rows + fold-count line).
 *     Rows are plain Views/Texts: nothing inline navigates any more, and
 *     any press just opens the sheet — a mistouch costs a modal, not a
 *     page leave. Titles wrap at 2 lines (`numberOfLines={2}`).
 *   - Level 2 — bottom sheet (transparent Modal, Board 2): title + close,
 *     grouped 任务 / Pull Request only when both kinds mix (first-appearance
 *     order kept, single-kind lists ungrouped), rows min-h 48 with inset
 *     separators, mask / × / Android back all close. Issue rows push the
 *     in-app route (`resolveLinkAction`, the same decision prose links
 *     take); PR rows open the canonical URL through RUYI-634's
 *     `openExternalUrl` channel (GitHub App deep link when installed,
 *     browser otherwise) with a failure Alert; unresolved references stay
 *     non-clickable plain-text slots.
 *
 * The fold cut and counts come from `partitionReferencePreview` in core —
 * the same arithmetic the web popover folds with. Row building/grouping
 * policy lives in `lib/issue-reference-rows.ts` (node-testable); this
 * component only resolves, partitions, and draws.
 *
 * Extraction happens once in `Markdown` (raw content, shared core
 * extractor, raw-deduped); this component only resolves and renders.
 */
import { useMemo, useRef, useState, type ReactNode } from "react";
import {
  Alert,
  Linking,
  Modal,
  PanResponder,
  Pressable,
  ScrollView,
  Text,
  View,
} from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { router } from "expo-router";
import { useQueries } from "@tanstack/react-query";
import {
  parseMergeRequestReference,
  parsePullRequestReference,
  partitionReferencePreview,
} from "@multica/core/markdown";
import type { IssueReference } from "@multica/core/markdown";
import { identifierMatchesPrefix } from "@multica/core/markdown";
import {
  issueDetailOptions,
  issueIdentifierOptions,
} from "@/data/queries/issues";
import {
  pullRequestLookupByNumberOptions,
  pullRequestLookupOptions,
} from "@/data/queries/github";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useWorkspaceIssuePrefix } from "@/lib/use-workspace-prefix";
import { resolveLinkAction } from "@/lib/markdown/link-route";
import {
  buildReferenceTailRows,
  groupReferenceTailRows,
  shouldCloseSheetOnDragRelease,
  type ReferenceTailRow,
} from "@/lib/issue-reference-rows";
import { pullRequestStateVisual } from "@/lib/pull-request-display";
import { openExternalUrl } from "@/lib/pull-request-link";
import { useIssueStatuses } from "@/lib/use-issue-statuses";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";
import { StatusIcon } from "@/components/ui/status-icon";

type ResolvedReferenceTailRow = Exclude<ReferenceTailRow, { kind: "degraded" }>;

function prOrIssueIdentifier(row: ResolvedReferenceTailRow): string {
  return row.kind === "issue"
    ? row.issue.identifier
    : `${row.pr.repo_owner}/${row.pr.repo_name}#${row.pr.number}`;
}

function prOrIssueTitle(row: ResolvedReferenceTailRow): string {
  return row.kind === "issue" ? row.issue.title : row.pr.title;
}

/**
 * Shared actionable-row body (sheet level): icon + identifier + 2-line
 * title + chevron. Branch-specific bits arrive pre-built so each kind
 * keeps its own narrowed typing here.
 */
function renderActionableRow({
  key,
  testID,
  onPress,
  icon,
  identifier,
  title,
  chevronColor,
  showDivider,
}: {
  key: string;
  testID: string;
  onPress: () => void;
  icon: ReactNode;
  identifier: string;
  title: string;
  chevronColor: string;
  showDivider: boolean;
}) {
  return (
    <View key={key}>
      {showDivider ? <View className="ml-12 h-px bg-border" /> : null}
      <Pressable
        testID={testID}
        className="min-h-[48px] flex-row items-center gap-3 px-4 py-2 active:bg-accent"
        accessibilityRole="link"
        accessibilityLabel={`${identifier} ${title}`}
        onPress={onPress}
      >
        {icon}
        <Text className="text-[13px] font-medium text-muted-foreground">
          {identifier}
        </Text>
        <Text className="flex-1 text-sm leading-5 text-foreground" numberOfLines={2}>
          {title}
        </Text>
        <Ionicons
          name="chevron-forward"
          size={16}
          color={chevronColor}
        />
      </Pressable>
    </View>
  );
}

export function IssueReferenceList({
  references,
}: {
  references: IssueReference[];
}) {
  const { t } = useT("issues");
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const workspacePrefix = useWorkspaceIssuePrefix();
  const { colorOf } = useIssueStatuses();
  const { colorScheme } = useColorScheme();
  const insets = useSafeAreaInsets();
  const [sheetOpen, setSheetOpen] = useState(false);

  // Board 2 close path #3: swipe down on the sheet header to dismiss. The
  // responder only claims the gesture once a vertical drag emerges, so taps
  // on the header (including the × button) still reach their Pressables; the
  // threshold decision itself is node-tested policy.
  const sheetDrag = useRef(
    PanResponder.create({
      onMoveShouldSetPanResponder: (_e, g) =>
        Math.abs(g.dy) > 8 && Math.abs(g.dy) > Math.abs(g.dx),
      onPanResponderRelease: (_e, g) => {
        if (shouldCloseSheetOnDragRelease(g.dy, g.vy)) setSheetOpen(false);
      },
    }),
  ).current;

  const results = useQueries({
    queries: useMemo(
      () =>
        references.map((ref) => {
          if (ref.form === "identifier") {
            // Same gate as web's useIssueReferenceResolutions: once the
            // workspace prefix is known, a mismatched bare identifier cannot
            // resolve here — skip the network instead of spending a
            // guaranteed-404 request (RUYI-705 defense in depth; extraction
            // normally filters these already). Unknown prefix falls through
            // and lets the exact match decide, web parity.
            const prefixMatches = identifierMatchesPrefix(
              ref.ref,
              workspacePrefix,
            );
            return {
              ...issueIdentifierOptions(wsId, ref.ref),
              enabled: !!wsId && !!ref.ref && prefixMatches,
            };
          }
          if (ref.form === "pull_request") {
            // The extractor only emits parseable tokens; the re-parse here is
            // a cheap guard so a malformed ref never spends a query.
            const parts = parsePullRequestReference(ref.ref);
            return pullRequestLookupOptions(
              wsId,
              parts?.owner ?? "",
              parts?.repo ?? "",
              parts?.number ?? 0,
            );
          }
          if (ref.form === "merge_request") {
            // RUYI-728: body-text `MR !N` shares the tail's resolution —
            // unique-hit by-number lookup; a miss (never synced, or the same
            // number in several repos) reads as null and the row degrades,
            // the same web-footer contract.
            const parts = parseMergeRequestReference(ref.ref);
            return pullRequestLookupByNumberOptions(wsId, parts?.number ?? 0);
          }
          return issueDetailOptions(wsId, ref.ref);
        }),
      [references, wsId, workspacePrefix],
    ),
    // Zip positions with `references` below; a failed / loading query reads
    // as unresolved and degrades, the same "every miss looks alike" contract
    // the web footer follows.
    combine: (results) => results.map((r) => r.data ?? null),
  });

  const rows = useMemo(
    () => buildReferenceTailRows(references, results),
    [references, results],
  );
  const { previews, hidden, hiddenPrCount } = useMemo(
    () => partitionReferencePreview(rows, (row) => row.kind),
    [rows],
  );
  const groups = useMemo(() => groupReferenceTailRows(rows), [rows]);

  if (references.length === 0) return null;

  const headerLabel = t("detail.reference_tail_header", "References · {{count}}", {
    count: rows.length,
  });
  const collapsedLine =
    hidden.length === 0
      ? null
      : hiddenPrCount > 0
        ? t("detail.reference_tail_more_with_pr", "{{count}} more references (incl. {{prCount}} PRs)", {
            count: hidden.length,
            prCount: hiddenPrCount,
          })
        : t("detail.reference_tail_more", "{{count}} more references", {
            count: hidden.length,
          });

  const navigateToIssue = (row: Extract<ReferenceTailRow, { kind: "issue" }>) => {
    setSheetOpen(false);
    const action = resolveLinkAction(
      `mention://issue/${row.issue.id}`,
      wsSlug ?? null,
    );
    if (action.kind === "route") router.push(action.path);
  };

  const openPullRequest = async (
    row: Extract<ReferenceTailRow, { kind: "pr" }>,
  ) => {
    const result = await openExternalUrl(row.pr.html_url, Linking);
    if (!result.ok) {
      Alert.alert(
        t(
          "mobile.detail.pull_request_open_failed",
          "Couldn't open this pull request.",
        ),
        result.message,
      );
    }
  };

  // Per-kind state glyph; branching on `row.kind` (not a boolean alias)
  // is what lets TS narrow `row.issue` / `row.pr` and the derived visual.
  const renderStateIcon = (
    row: ResolvedReferenceTailRow,
    size: number,
  ): ReactNode => {
    if (row.kind === "issue") {
      return (
        <StatusIcon
          status={row.issue.status}
          color={colorOf(row.issue.status)}
          size={size}
        />
      );
    }
    const visual = pullRequestStateVisual(row.pr.state);
    return (
      <Ionicons
        name={visual.icon}
        size={size}
        color={THEME[colorScheme][visual.themeKey]}
        style={visual.dimmed ? { opacity: 0.8 } : null}
      />
    );
  };

  const renderSheetRow = (row: ReferenceTailRow, showDivider: boolean) => {
    if (row.kind === "degraded") {
      // Plain-text slot — never tappable (Board 2 walkthrough #4).
      return (
        <View key={row.key}>
          {showDivider ? <View className="ml-12 h-px bg-border" /> : null}
          <View className="min-h-[48px] flex-row items-center px-4 py-2">
            <Text className="flex-1 text-sm text-muted-foreground" numberOfLines={2}>
              {row.fallback}
            </Text>
          </View>
        </View>
      );
    }
    let onPress: () => void;
    let testID: string;
    if (row.kind === "issue") {
      onPress = () => navigateToIssue(row);
      testID = "reference-sheet-issue-row";
    } else {
      onPress = () => void openPullRequest(row);
      testID = "reference-sheet-pr-row";
    }
    return renderActionableRow({
      key: row.key,
      testID,
      onPress,
      icon: renderStateIcon(row, 16),
      identifier: prOrIssueIdentifier(row),
      title: prOrIssueTitle(row),
      chevronColor: THEME[colorScheme].mutedForeground,
      showDivider,
    });
  };

  const sheetRows: ReferenceTailRow[] = groups.grouped
    ? [
        ...groups.tasks,
        ...groups.prs,
        ...groups.degraded,
      ]
    : rows;

  return (
    <>
      {/* Board 1: separator + gray-tap whole block; rows are NOT pressables. */}
      <View className="mt-3 border-t border-border pt-2.5">
        <Pressable
          testID="issue-reference-block"
          className="flex-col gap-1 rounded-[10px] p-1 active:bg-accent"
          accessibilityRole="button"
          accessibilityLabel={t(
            "detail.referenced_issues",
            "Referenced issues",
          )}
          onPress={() => setSheetOpen(true)}
        >
          <View className="flex-row items-center px-1 py-1">
            <Text className="text-[11px] font-medium tracking-[0.06em] text-muted-foreground">
              {headerLabel}
            </Text>
            <Ionicons
              name="chevron-forward"
              size={14}
              color={THEME[colorScheme].mutedForeground}
              style={{ marginLeft: "auto" }}
            />
          </View>
          {previews.map((row) => {
            if (row.kind === "degraded") {
              return (
                <Text
                  key={row.key}
                  className="px-1 text-xs text-muted-foreground"
                  numberOfLines={2}
                >
                  {row.fallback}
                </Text>
              );
            }
            return (
              <View
                key={row.key}
                testID="issue-reference-row"
                className="flex-row items-center gap-2 px-1"
              >
                {renderStateIcon(row, 14)}
                <Text className="shrink-0 text-xs font-medium text-muted-foreground">
                  {prOrIssueIdentifier(row)}
                </Text>
                <Text
                  className="flex-1 text-xs leading-[18px] text-foreground"
                  numberOfLines={2}
                >
                  {prOrIssueTitle(row)}
                </Text>
              </View>
            );
          })}
          {collapsedLine ? (
            <Text className="px-1 text-xs text-muted-foreground">
              {collapsedLine}
            </Text>
          ) : null}
        </Pressable>
      </View>

      {/* Board 2: bottom sheet — the ONLY surface where rows navigate. */}
      <Modal
        visible={sheetOpen}
        transparent
        animationType="slide"
        onRequestClose={() => setSheetOpen(false)}
      >
        <Pressable
          className="flex-1 bg-black/40"
          onPress={() => setSheetOpen(false)}
        >
          <View
            className="mt-auto rounded-t-2xl bg-popover"
            style={{ maxHeight: "75%", paddingBottom: insets.bottom + 8 }}
          >
            <View
              className="flex-row items-center border-b border-border px-4 py-3"
              {...sheetDrag.panHandlers}
            >
              <Text className="flex-1 text-base font-bold text-foreground">
                {headerLabel}
              </Text>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel={t(
                  "mobile.detail.reference_sheet_close",
                  "Close",
                )}
                hitSlop={12}
                onPress={() => setSheetOpen(false)}
                className="-mr-1 p-2"
              >
                <Ionicons
                  name="close"
                  size={20}
                  color={THEME[colorScheme].mutedForeground}
                />
              </Pressable>
            </View>
            <ScrollView nestedScrollEnabled>
              {groups.grouped ? (
                <>
                  <Text className="px-4 pt-3 pb-1 text-[11px] font-medium tracking-[0.06em] text-muted-foreground">
                    {t("detail.reference_group_tasks", "Tasks")}
                  </Text>
                  {groups.tasks.map((row, i) =>
                    renderSheetRow(row, i > 0),
                  )}
                  <Text className="px-4 pt-3 pb-1 text-[11px] font-medium tracking-[0.06em] text-muted-foreground">
                    {t("detail.reference_group_prs", "Pull Request")}
                  </Text>
                  {groups.prs.map((row, i) => renderSheetRow(row, i > 0))}
                  {groups.degraded.map((row) => renderSheetRow(row, false))}
                </>
              ) : (
                sheetRows.map((row, i) => renderSheetRow(row, i > 0))
              )}
            </ScrollView>
          </View>
        </Pressable>
      </Modal>
    </>
  );
}
