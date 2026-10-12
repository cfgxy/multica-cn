/**
 * Tail aggregation of issue references (RUYI-635 增量, mobile half;
 * flattened by RUYI-731).
 *
 * Web renders every issue/PR mention as plain body text and lists the
 * collected references in a footer; mobile's demote pass
 * (`lib/markdown/preprocess.ts`) applies the same body-text contract, and
 * this component is the tail block. RUYI-731 retires the two-level
 * anti-mistouch surface (whole-block pressable + bottom sheet): the block is
 * now a flat list — separator + header 「引用 · N」 + EVERY reference as one
 * tappable row, in first-appearance order. Rows navigate directly, the same
 * landing paths the old sheet rows took:
 *
 *   - issue rows push the in-app route (`resolveLinkAction`, the same
 *     decision prose links take);
 *   - PR rows open the canonical URL through RUYI-634's `openExternalUrl`
 *     channel (GitHub App deep link when installed, browser otherwise) with
 *     a failure Alert;
 *   - unresolved references stay non-clickable plain-text slots — the same
 *     "miss reads as what the author typed" contract the web footer follows.
 *
 * RUYI-731 self-filter: the current-issue context (`lib/current-issue-context`,
 * default null, mounted by the issue detail screen) makes a reference that
 * resolves to the CURRENT issue build no row; surfaces without the context
 * (chat, inbox) keep every row. The row-building policy (zip, cross-form
 * collapse, self-filter, kinds) lives in `lib/issue-reference-rows.ts`
 * (node-testable); this component only resolves and draws.
 *
 * Extraction happens once in `Markdown` (raw content, shared core
 * extractor, raw-deduped); this component only resolves and renders.
 */
import { useMemo, type ReactNode } from "react";
import { Alert, Linking, Pressable, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { router } from "expo-router";
import { useQueries } from "@tanstack/react-query";
import {
  parsePullRequestReference,
} from "@multica/core/markdown";
import type { IssueReference } from "@multica/core/markdown";
import { identifierMatchesPrefix } from "@multica/core/markdown";
import {
  issueDetailOptions,
  issueIdentifierOptions,
} from "@/data/queries/issues";
import { pullRequestLookupOptions } from "@/data/queries/github";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useWorkspaceIssuePrefix } from "@/lib/use-workspace-prefix";
import { resolveLinkAction } from "@/lib/markdown/link-route";
import {
  buildReferenceTailRows,
  type ReferenceTailRow,
} from "@/lib/issue-reference-rows";
import { useCurrentIssueId } from "@/lib/current-issue-context";
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

export function IssueReferenceList({
  references,
}: {
  references: IssueReference[];
}) {
  const { t } = useT("issues");
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const workspacePrefix = useWorkspaceIssuePrefix();
  const currentIssueId = useCurrentIssueId();
  const { colorOf } = useIssueStatuses();
  const { colorScheme } = useColorScheme();

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
    () => buildReferenceTailRows(references, results, currentIssueId),
    [references, results, currentIssueId],
  );

  if (references.length === 0 || rows.length === 0) return null;

  const headerLabel = t("detail.reference_tail_header", "References · {{count}}", {
    count: rows.length,
  });

  const navigateToIssue = (row: Extract<ReferenceTailRow, { kind: "issue" }>) => {
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

  return (
    // Separator + gray envelope (visual envelope unchanged; RUYI-730 owns the
    // rework). Rows are the only press targets — each navigates on its own.
    <View className="mt-3 border-t border-border pt-2.5">
      <View
        testID="issue-reference-block"
        className="flex-col gap-1 p-1"
        accessibilityLabel={t(
          "detail.referenced_issues",
          "Referenced issues",
        )}
      >
        <View className="flex-row items-center px-1 py-1">
          <Text className="text-[11px] font-medium tracking-[0.06em] text-muted-foreground">
            {headerLabel}
          </Text>
        </View>
        {rows.map((row) => {
          if (row.kind === "degraded") {
            // Plain-text slot — never tappable.
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
          const identifier = prOrIssueIdentifier(row);
          const title = prOrIssueTitle(row);
          return (
            <Pressable
              key={row.key}
              testID="issue-reference-row"
              className="flex-row items-center gap-2 rounded-md px-1 active:bg-accent"
              accessibilityRole="link"
              accessibilityLabel={`${identifier} ${title}`}
              onPress={
                row.kind === "issue"
                  ? () => navigateToIssue(row)
                  : () => void openPullRequest(row)
              }
            >
              {renderStateIcon(row, 14)}
              <Text className="shrink-0 text-xs font-medium text-muted-foreground">
                {identifier}
              </Text>
              <Text
                className="flex-1 text-xs leading-[18px] text-foreground"
                numberOfLines={2}
              >
                {title}
              </Text>
            </Pressable>
          );
        })}
      </View>
    </View>
  );
}
